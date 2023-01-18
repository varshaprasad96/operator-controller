/*
Copyright 2022.

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

	rukpakv1alpha1 "github.com/operator-framework/rukpak/api/v1alpha1"
	olmcliManager "github.com/perdasilva/olmcli/manager"
	"github.com/perdasilva/olmcli/resolution"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/operator-framework/operator-controller/api/v1alpha1"
	operatorsv1alpha1 "github.com/operator-framework/operator-controller/api/v1alpha1"
)

// Definitions to manage status conditions. Here, "Operator" refers to Operator object.
const (
	// operatorInstall represents the status of Operator's installation.
	operatorInstallStatus = "InstallOperator"
	// operatorResolution represents the status of deppy's resolution.
	operatorResolutionStatus = "DependencyResolution"
)

// OperatorReconciler reconciles a Operator object
type OperatorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Pass a generic instance of the backing store that could be constrained on catalogsrc
	PackageInstaller *olmcliManager.PackageInstaller
}

//+kubebuilder:rbac:groups=operators.operatorframework.io,resources=operators,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=operators.operatorframework.io,resources=operators/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=operators.operatorframework.io,resources=operators/finalizers,verbs=update
//+kubebuilder:rbac:groups=core.rukpak.io,resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=core.rukpak.io,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.rukpak.io,resources=pods,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Operator object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.1/pkg/reconcile
func (r *OperatorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	operator := &operatorsv1alpha1.Operator{}
	err := r.Get(ctx, req.NamespacedName, operator)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then, it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			log.Info("operator resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get memcached")
		return ctrl.Result{}, err
	}

	// Setting the status on operator object before reconciling.
	if operator.Status.Conditions == nil || len(operator.Status.Conditions) == 0 {
		meta.SetStatusCondition(&operator.Status.Conditions, metav1.Condition{Type: operatorInstallStatus, Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation, installation in progress"})
		if err = r.Status().Update(ctx, operator); err != nil {
			log.Error(err, "Failed to update Operator status while reconciling")
			return ctrl.Result{}, err
		}

		// Re-fetching the object with updated status.
		if err := r.Get(ctx, req.NamespacedName, operator); err != nil {
			log.Error(err, "Failed to re-fetch memcached")
			return ctrl.Result{}, err
		}
	}

	// Fetch the packageName and try installing and try installing
	packageName := operator.Spec.PackageName

	// bundle the errors, since we don't want to reconcile immediately after one pacakge's installation is unsuccessful.
	var errs []error
	existingBundleDeployment := &rukpakv1alpha1.BundleDeployment{}
	// Get the bundledeployment for the existing operator, if not present then create one.
	// TODO: update the status if its already present? What happens when two Operator object's try to point to single bundle deployment?
	// Do we add them to owner references?
	err = r.Get(ctx, client.ObjectKey{Name: packageName}, existingBundleDeployment)
	if err != nil && apierrors.IsNotFound(err) {
		var installables []resolution.Installable
		if len(packageName) != 0 {
			installables, err = r.resolve(ctx, packageName)
			if err != nil {
				log.Error(err, "Error resolving")
				meta.SetStatusCondition(&operator.Status.Conditions, metav1.Condition{Type: operatorResolutionStatus, Status: metav1.ConditionFalse, Reason: "Error", Message: "Error during resolution from deppy"})
				return ctrl.Result{}, err
			}
			meta.SetStatusCondition(&operator.Status.Conditions, metav1.Condition{Type: operatorResolutionStatus, Status: metav1.ConditionTrue, Reason: "Resolved", Message: "Resolution sucessful"})
			// Update status based on operator resolution
			if err = r.Status().Update(ctx, operator); err != nil {
				log.Error(err, "Failed to update Operator status")
				return ctrl.Result{}, err
			}
		}

		if len(installables) != 0 {
			// Use rukpak for installation
			for _, installable := range installables {
				bundleDeployment, err := r.bundleDeploymentFromInstallable(&installable, operator)
				if err != nil {
					errs = append(errs, err)
				}
				if err := r.Client.Create(ctx, bundleDeployment); err != nil {
					errs = append(errs, err)
				}
			}

			// Re-fetching the updated object to avoid "Object has been modified error"
			if err := r.Get(ctx, req.NamespacedName, operator); err != nil {
				log.Error(err, "Failed to re-fetch operator")
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(&operator.Status.Conditions, metav1.Condition{Type: operatorInstallStatus, Status: metav1.ConditionTrue, Reason: "Installed", Message: "Successfully installed bundle deployment"})
			if err = r.Status().Update(ctx, operator); err != nil {
				log.Error(err, "Failed to update Operator status after installation")
				errs = append(errs, err)
			}
		}

	} else if err != nil {
		log.Error(err, "Failed to get bundledeployment")
	}
	return ctrl.Result{}, utilerrors.NewAggregate(errs)
}

func (r *OperatorReconciler) resolve(ctx context.Context, packageName string) ([]resolution.Installable, error) {
	packageRequired, err := resolution.NewRequiredPackage(packageName)
	if err != nil {
		return nil, err
	}

	return r.PackageInstaller.Resolve(ctx, packageRequired)
}

// SetupWithManager sets up the controller with the Manager.
func (r *OperatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorsv1alpha1.Operator{}).
		Owns(&rukpakv1alpha1.BundleDeployment{}).
		Complete(r)
}

func (r *OperatorReconciler) bundleDeploymentFromInstallable(installable *resolution.Installable, operator *v1alpha1.Operator) (*rukpakv1alpha1.BundleDeployment, error) {
	bundledep := &rukpakv1alpha1.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: installable.PackageName,
			Annotations: map[string]string{
				"annotations.olm.io/repository": installable.Repository,
				"annotations.olm.io/version":    installable.Version,
				"annotations.olm.io/channel":    installable.ChannelName,
			},
		},
		Spec: rukpakv1alpha1.BundleDeploymentSpec{
			ProvisionerClassName: "core-rukpak-io-plain",
			Template: &rukpakv1alpha1.BundleTemplate{
				Spec: rukpakv1alpha1.BundleSpec{
					ProvisionerClassName: "core-rukpak-io-registry",
					Source: rukpakv1alpha1.BundleSource{
						Type: rukpakv1alpha1.SourceTypeImage,
						Image: &rukpakv1alpha1.ImageSource{
							Ref:                 installable.GetBundlePath(),
							ImagePullSecretName: "regcred",
						},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(operator, bundledep, r.Scheme); err != nil {
		return nil, err
	}
	return bundledep, nil
}

// Some logic for adding annotations instead of owner references to figure out the case where multiple Operator objects
// own a single bundle deployment.
// func (r *OperatorReconciler) deleteBundleDeployment(ctx context.Context, operator *v1alpha1.Operator) error {
// 	bundleDeployments, err := getBundleDeploymentsFromOperator(operator)
// 	if err != nil {
// 		return err
// 	}

// 	errs := []error{}
// 	for _, bundleDeployment := range bundleDeployments {
// 		bd := &rukpakv1alpha1.BundleDeployment{}
// 		if err := r.Client.Get(ctx, client.ObjectKey{Name: bundleDeployment}, bd); err != nil {
// 			errs = append(errs, fmt.Errorf("error findling the bundle deployment mentioned in this object %s: %v", bundleDeployment, err))
// 			continue
// 		}

// 		// If the object is present in cluster, then go ahead and delete it
// 		if err := r.Client.Delete(ctx, bd); err != nil {
// 			errs = append(errs, fmt.Errorf("unable to delete bundledeployment %s: %v", bundleDeployment, err))
// 		}
// 	}
// 	return utilerrors.NewAggregate(errs)
// }

// func getBundleDeploymentsFromOperator(operator *v1alpha1.Operator) ([]string, error) {
// 	bundleDeployments := make([]string, 0)
// 	errs := []error{}

// 	annotations := operator.GetAnnotations()
// 	for key, value := range annotations {
// 		if strings.Contains(key, operatorBundleDeploymentLabel) {
// 			str := strings.Split(value, "/")
// 			if len(str) < 2 {
// 				errs = append(errs, fmt.Errorf("cannot read bundledeployment label %s", key))
// 				continue
// 			}
// 			bundleDeployments = append(bundleDeployments, str[1])
// 		}
// 	}
// 	return bundleDeployments, utilerrors.NewAggregate(errs)
// }

// Add finalizer to identify if the object is up for deletion.
// if !controllerutil.ContainsFinalizer(operator, operatorFinalizer) {
// 	log.Info("Adding finalizer for Operator object")
// 	if ok := controllerutil.AddFinalizer(operator, operatorFinalizer); !ok {
// 		log.Error(err, "Error adding finalizer")
// 		return ctrl.Result{Requeue: true}, nil
// 	}
// 	if err = r.Update(ctx, operator); err != nil {
// 		log.Error(err, "Failed to update after adding finalizer")
// 		return ctrl.Result{}, err
// 	}
// }

// Check if finalizer is present and the object is up for deletion. If so delete the bundleDeployment.
// Figure out the logic when multiple owner references are set for a bundle deployment.
// isMarkedToBeDeleted := operator.GetDeletionTimestamp() != nil
// if isMarkedToBeDeleted {
// 	if controllerutil.ContainsFinalizer(operator, operatorFinalizer) {
// 		log.Info("Performing Finalizer operations before deleting CR")
// 	}

// 	meta.SetStatusCondition(&operator.Status.Conditions, metav1.Condition{Type: operatorUninstall,
// 		Status: metav1.ConditionUnknown, Reason: "Finalizing",
// 		Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", operator.Name)})

// 	if err := r.Status().Update(ctx, operator); err != nil {
// 		log.Error(err, "Failed to update Operator status")
// 		return ctrl.Result{}, err
// 	}

// 	if err := r.deleteBundleDeployment(ctx, operator); err != nil {
// 		log.Info("Error deleting bundle deployments")
// 		return ctrl.Result{}, err
// 	}

// 	// Re-fetch the resource and update status
// 	if err := r.Get(ctx, req.NamespacedName, operator); err != nil {
// 		log.Error(err, "Failed to re-fetch")
// 		return ctrl.Result{}, err
// 	}

// 	meta.SetStatusCondition(&operator.Status.Conditions, metav1.Condition{Type: operatorUninstall,
// 		Status: metav1.ConditionTrue, Reason: "Finalizing",
// 		Message: fmt.Sprintf("Finalizer operations for custom resource %s name were successfully accomplished", operator.Name)})

// 	if err := r.Status().Update(ctx, operator); err != nil {
// 		log.Error(err, "Failed to update Memcached status")
// 		return ctrl.Result{}, err
// 	}

// 	log.Info("Removing Finalizer for Operator after successfully deleting bundle deployments")
// 	if ok := controllerutil.RemoveFinalizer(operator, operatorFinalizer); !ok {
// 		log.Error(err, "Failed to remove finalizer")
// 		return ctrl.Result{Requeue: true}, nil
// 	}

// 	if err := r.Update(ctx, operator); err != nil {
// 		log.Error(err, "Failed to remove finalizer for Memcached")
// 		return ctrl.Result{}, err
// 	}

// 	return ctrl.Result{}, nil
// }
