/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	holepunchAnnotationName = "holepunch/punch-external"
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

	log.Info("Identified service of interest")
	// TODO everything

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

func (r *ServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Service{}).
		Complete(r)
}
