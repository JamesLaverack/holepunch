package controllers

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sync/errgroup"
	"github.com/huin/goupnp/dcps/internetgateway2"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	holepunchAnnotationName = "holepunch/punch-external"
	leaseDurationSeconds    = 3600
)

// ServiceReconciler reconciles a Service object
type ServiceReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services/status,verbs=get;update;patch

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

	// We need to verify that we've got an IP to map onto
	serviceIP, err := getServiceIP(service)
	if err != nil {
		log.Error(err, "Failed to get IP for service")
		return ctrl.Result{}, err
	}
	log = log.WithValues("service-ip", serviceIP)
	log.Info("Discovered Service IP")

	router, err := PickRouterClient(ctx)
	if err != nil {
		log.Error(err, "Failed to find router to configure")
		return ctrl.Result{}, err
	}

	externalIP, err := router.GetExternalIPAddress()
	if err != nil {
		log.Error(err, "Failed to resolve external IP address")
		return ctrl.Result{}, err
	}
	log = log.WithValues("external-ip", externalIP)
	log.Info("Discovered External IP")

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
		description := "foo" //fmt.Sprintf("%s-%s-%d-%s", service.Name, service.Namespace, portNumber, protocol)
		portLogger := log.WithValues("forwarding-port", portNumber,
			"upnp-description", description,
			"lease-duration", leaseDurationSeconds)
		portLogger.Info("Attempting to forward port from router with UPnP")
		if err = router.AddPortMapping(
			"",
			// External port number to expose to Internet:
			portNumber,
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

	return ctrl.Result{}, nil
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

func PickRouterClient(ctx context.Context) (RouterClient, error) {
	tasks, _ := errgroup.WithContext(ctx)
	// Request each type of client in parallel, and return what is found.
	var ip1Clients []*internetgateway2.WANIPConnection1
	tasks.Go(func() error {
		var err error
		ip1Clients, _, err = internetgateway2.NewWANIPConnection1Clients()
		return err
	})
	var ip2Clients []*internetgateway2.WANIPConnection2
	tasks.Go(func() error {
		var err error
		ip2Clients, _, err = internetgateway2.NewWANIPConnection2Clients()
		return err
	})
	var ppp1Clients []*internetgateway2.WANPPPConnection1
	tasks.Go(func() error {
		var err error
		ppp1Clients, _, err = internetgateway2.NewWANPPPConnection1Clients()
		return err
	})

	if err := tasks.Wait(); err != nil {
		return nil, err
	}

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
	default:
		return nil, errors.New("No services found")
	}
}

func (r *ServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Service{}).
		Complete(r)
}
