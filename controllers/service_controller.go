package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/huin/goupnp/dcps/internetgateway1"
	"github.com/huin/goupnp/dcps/internetgateway2"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	holepunchAnnotationName          = "holepunch/punch-external"
	holepunchPortMapAnnotationPrefix = "holepunch.port/"
	leaseDurationSeconds             = 3600
)

// ServiceReconciler reconciles a Service object
type ServiceReconciler struct {
	client.Client
	Log            logr.Logger
	Scheme         *runtime.Scheme
	RouterRootDesc string
}

// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=services/status,verbs=get

func (r *ServiceReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("service", req.NamespacedName)

	// Get the service
	var service corev1.Service
	if err := r.Get(ctx, req.NamespacedName, &service); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// We only care about services that have our annotation on them
	if !hasHolepunchAnnotation(service) {
		// Nothing to be done
		return ctrl.Result{}, nil
	}

	// We only care about LoadBalancer services. We need a real internal IP to map to!
	if service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		// This means we've put the annotation on a service that isn't a loadbalancer.
		log.Error(nil, "Holepunch enabled on non-LoadBalancer service")
		// TODO emit event onto the service
		return ctrl.Result{}, nil
	}

	// Get the port mapping, if one exists. This instructs us to setup the UPnP mappings to use a *different* external
	// and internal port. Some routers may not support this feature.
	portMapping, err := getHolepunchPortMapping(service)
	if err != nil {
		return ctrl.Result{}, err
	}

	var router RouterClient
	// Find a router to configure
	if r.RouterRootDesc == "" {
		router, err = PickRouterClient(ctx)
		if err != nil {
			log.Error(err, "Failed to find router to configure")
			return ctrl.Result{}, err
		}
	} else {
		router, err = PickRouterClient(ctx, r.RouterRootDesc)
		if err != nil {
			log.Error(err, "Failed to find router to configure")
			return ctrl.Result{}, err
		}
	}

	// Ask that router for *it's* external IP.
	// This is where the term "external" gets weird. There's the underlying pods in the K8s cluster which have IPs, then
	// the service has an IP inside the cluster, but it also has an "external" IP which is really an IP on the user's
	// home network (usually), and when we ask the *router* for "external" we really do mean public internet IP.
	externalIP, err := router.GetExternalIPAddress()
	if err != nil {
		log.Error(err, "Failed to resolve external IP address")
		return ctrl.Result{}, err
	}
	log = log.WithValues("external-ip", externalIP)

	// Find the service's IP, that we're hoping is a local network IP from the perspective of the router.
	serviceIP, err := getServiceIP(service)
	if err != nil {
		log.Error(err, "Failed to get IP for service (has it not been allocated yet?)")
		return ctrl.Result{}, err
	}
	log = log.WithValues("service-ip", serviceIP)

	description := fmt.Sprintf("Mapping for %s/%s", service.Name, service.Namespace)

	// Try to forward every port
	for _, servicePort := range service.Spec.Ports {
		// For some reason the Kubernetes Service API thinks a port can be an int32. On Linux at least it'll *always*
		// be a uint16 so this is a safe cast.
		portNumber := uint16(servicePort.Port)
		protocol, err := toUPnPProtocol(servicePort.Protocol)
		if err != nil {
			log.Error(err, "Unable to resolve protocol to use")
			return ctrl.Result{}, err
		}

		// Figure out if we want to map the port
		externalPort, ok := portMapping[portNumber]
		if !ok {
			// We didn't have a mapping for this port.
			externalPort = portNumber
		}

		// Log out
		portLogger := log.WithValues("forwarding-port", portNumber,
			"external-port", externalPort,
			"upnp-description", description,
			"lease-duration", leaseDurationSeconds)
		portLogger.Info("Attempting to forward port from router with UPnP")

		if err = router.AddPortMapping(
			"",
			// External port number to expose to Internet:
			externalPort,
			// Forward TCP (this could be "UDP" if we wanted that instead).
			protocol,
			// Internal port number on the LAN to forward to.
			// Some routers might not support this being different to the external
			// port number.
			portNumber,
			// Internal address on the LAN we want to forward to.
			serviceIP,
			// Enabled:
			true,
			// Informational description for the client requesting the port forwarding.
			description,
			// How long should the port forward last for in seconds.
			// If you want to keep it open for longer and potentially across router
			// resets, you might want to periodically request before this elapses.
			leaseDurationSeconds,
		); err != nil {
			portLogger.Error(err, "Failed to configure UPnP port-forwarding")
			return ctrl.Result{}, err
		}
	}

	// Even on a "success" we need to come back before our lease is up to redo it.
	log.Info("Success, ports forwarded.", "reschedule-seconds", leaseDurationSeconds-30)
	return ctrl.Result{RequeueAfter: (leaseDurationSeconds - 30) * time.Second}, nil
}

func getHolepunchPortMapping(service corev1.Service) (map[uint16]uint16, error) {
	portMapping := make(map[uint16]uint16)
	for annotationName, annotationValue := range service.Annotations {
		if strings.HasPrefix(annotationName, holepunchPortMapAnnotationPrefix) {
			// The term "internal port" is "port on our local network", or "port with the Kubenretes service" exposes.
			// Meanwhile "external port" is "port exposed by the router on the open internet". If we have the annotaiton
			// "holepunch.port/80: 3000" that means that our "internal port" is 80 and our "external port" is 3000.
			internalPortStr := strings.TrimPrefix(annotationName, holepunchPortMapAnnotationPrefix)
			internalPort, err := strconv.ParseUint(internalPortStr, 10, 16)
			if err != nil {
				return nil, err
			}
			externalPortStr := annotationValue
			externalPort, err := strconv.ParseUint(externalPortStr, 10, 16)
			if err != nil {
				return nil, err
			}
			// These casts to uint16 (from uint64) are safe because we told strconv.ParseUint earlier to confine to 16
			// bits only.
			portMapping[uint16(internalPort)] = uint16(externalPort)
		}
	}
	return portMapping, nil
}

func hasHolepunchAnnotation(service corev1.Service) bool {
	for name, value := range service.Annotations {
		if name == holepunchAnnotationName {
			return value == "true"
		}
	}
	return false
}

func toUPnPProtocol(serviceProtocol corev1.Protocol) (string, error) {
	if serviceProtocol == corev1.ProtocolTCP {
		return "TCP", nil
	} else if serviceProtocol == corev1.ProtocolUDP {
		return "UDP", nil
	} else {
		// This could happen, for example with corev1.ProtocolSTCP
		return "", errors.New(fmt.Sprintf("protocol type %s not supported", serviceProtocol))
	}
}

func getServiceIP(service corev1.Service) (string, error) {
	for _, ingress := range service.Status.LoadBalancer.Ingress {
		if ingress.IP != "" {
			// TODO don't just take the first
			return ingress.IP, nil
		}
	}
	return "", errors.New("no IP available for LoadBalancer (not yet allocated?)")
}

type RouterClient interface {
	AddPortMapping(
		NewRemoteHost string,
		NewExternalPort uint16,
		NewProtocol string,
		NewInternalPort uint16,
		NewInternalClient string,
		NewEnabled bool,
		NewPortMappingDescription string,
		NewLeaseDuration uint32,
	) (err error)

	GetExternalIPAddress() (
		NewExternalIPAddress string,
		err error,
	)
}

func PickRouterClient(ctx context.Context, rootDesc ...string) (RouterClient, error) {
	tasks, _ := errgroup.WithContext(ctx)
	var err error
	var u *url.URL
	if len(rootDesc) > 0 {
		u, err = url.Parse(rootDesc[0])
		if err != nil {
			return nil, err
		}
	}
	// Request each type of client in parallel, and return what is found.
	var ip1Clients []*internetgateway2.WANIPConnection1
	tasks.Go(func() error {
		if u != nil {
			ip1Clients, err = internetgateway2.NewWANIPConnection1ClientsByURL(u)
		} else {
			ip1Clients, _, err = internetgateway2.NewWANIPConnection1Clients()
		}
		return err
	})
	var ip2Clients []*internetgateway2.WANIPConnection2
	tasks.Go(func() error {
		if u != nil {
			ip2Clients, err = internetgateway2.NewWANIPConnection2ClientsByURL(u)
		} else {
			ip2Clients, _, err = internetgateway2.NewWANIPConnection2Clients()
		}
		return err
	})
	var ppp1Clients []*internetgateway2.WANPPPConnection1
	tasks.Go(func() error {
		if u != nil {
			ppp1Clients, err = internetgateway2.NewWANPPPConnection1ClientsByURL(u)
		} else {
			ppp1Clients, _, err = internetgateway2.NewWANPPPConnection1Clients()
		}
		return err
	})

	var ip1V1Clients []*internetgateway1.WANIPConnection1
	tasks.Go(func() error {
		if u != nil {
			ip1V1Clients, err = internetgateway1.NewWANIPConnection1ClientsByURL(u)
		} else {
			ip1V1Clients, _, err = internetgateway1.NewWANIPConnection1Clients()
		}
		return err
	})
	var ppp1V1Clients []*internetgateway1.WANPPPConnection1
	tasks.Go(func() error {
		if u != nil {
			ppp1V1Clients, err = internetgateway1.NewWANPPPConnection1ClientsByURL(u)
		} else {
			ppp1V1Clients, _, err = internetgateway1.NewWANPPPConnection1Clients()
		}
		return err
	})

	tasks.Wait()

	// Trivial handling for where we find exactly one device to talk to, you
	// might want to provide more flexible handling than this if multiple
	// devices are found.
	switch {
	case len(ip2Clients) > 0:
		return ip2Clients[0], nil
	case len(ip1Clients) > 0:
		return ip1Clients[0], nil
	case len(ppp1Clients) > 0:
		return ppp1Clients[0], nil
	case len(ip1V1Clients) > 0:
		return ip1V1Clients[0], nil
	case len(ppp1V1Clients) > 0:
		return ppp1V1Clients[0], nil
	default:
		return nil, errors.New("No services found")
	}
}

func (r *ServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Service{}).
		Complete(r)
}
