/*
Copyright 2023.

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
	"strings"

	operatorsv1alpha1 "github.com/operator-framework/operator-controller/api/v1alpha1"
	"github.com/operator-framework/operator-controller/internal/resolution"
	rukpakv1alpha1 "github.com/operator-framework/rukpak/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// OperatorReconciler reconciles a Operator object
type OperatorReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	resolver *resolution.OperatorResolver
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
	l := log.FromContext(ctx).WithName("reconcile")
	l.V(1).Info("starting")
	defer l.V(1).Info("ending")

	existingOp := &operatorsv1alpha1.Operator{}
	err := r.Get(ctx, req.NamespacedName, existingOp)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then, it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			l.Info("operator resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		l.Error(err, "Failed to get memcached")
		return ctrl.Result{}, err
	}

	// Setting the status on operator object before reconciling.
	if existingOp.Status.Conditions == nil || len(existingOp.Status.Conditions) == 0 {
		meta.SetStatusCondition(&existingOp.Status.Conditions, metav1.Condition{Type: operatorsv1alpha1.TypeReady, Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation, installation in progress"})
		if err = r.Status().Update(ctx, existingOp); err != nil {
			l.Error(err, "Failed to update Operator status while reconciling")
			return ctrl.Result{}, err
		}

		// Re-fetching the object with updated status.
		if err := r.Get(ctx, req.NamespacedName, existingOp); err != nil {
			l.Error(err, "Failed to re-fetch memcached")
			return ctrl.Result{}, err
		}
	}

	// TODO(varsha): add the code to check if bundledeployment exists, if not then send it for resolution to deppy.
	// ref: https://github.com/operator-framework/operator-controller/tree/prototype

	reconciledOp := existingOp.DeepCopy()
	res, reconcileErr := r.reconcile(ctx, reconciledOp)

	// Do checks before any Update()s, as Update() may modify the resource structure!
	updateStatus := !equality.Semantic.DeepEqual(existingOp.Status, reconciledOp.Status)
	updateFinalizers := !equality.Semantic.DeepEqual(existingOp.Finalizers, reconciledOp.Finalizers)
	unexpectedFieldsChanged := checkForUnexpectedFieldChange(*existingOp, *reconciledOp)

	if updateStatus {
		if updateErr := r.Status().Update(ctx, reconciledOp); updateErr != nil {
			return res, utilerrors.NewAggregate([]error{reconcileErr, updateErr})
		}
	}

	if unexpectedFieldsChanged {
		panic("spec or metadata changed by reconciler")
	}

	if updateFinalizers {
		if updateErr := r.Update(ctx, reconciledOp); updateErr != nil {
			return res, utilerrors.NewAggregate([]error{reconcileErr, updateErr})
		}
	}

	return res, reconcileErr
}

// Compare resources - ignoring status & metadata.finalizers
func checkForUnexpectedFieldChange(a, b operatorsv1alpha1.Operator) bool {
	a.Status, b.Status = operatorsv1alpha1.OperatorStatus{}, operatorsv1alpha1.OperatorStatus{}
	a.Finalizers, b.Finalizers = []string{}, []string{}
	return !equality.Semantic.DeepEqual(a, b)
}

// Helper function to do the actual reconcile
func (r *OperatorReconciler) reconcile(ctx context.Context, op *operatorsv1alpha1.Operator) (ctrl.Result, error) {

	// todo(perdasilva): this is a _hack_ we probably want to find a better way to ride or die resolve and update
	solution, err := r.resolver.Resolve(ctx)
	status := metav1.ConditionTrue
	reason := operatorsv1alpha1.ReasonResolutionSucceeded
	message := "resolution was successful"
	if err != nil {
		status = metav1.ConditionTrue
		reason = operatorsv1alpha1.ReasonResolutionFailed
		message = err.Error()
	}

	// todo(perdasilva): more hacks - need to fix up the solution structure to be more useful
	packageVariableIDMap := map[string]string{}
	if solution != nil {
		for variableID, ok := range solution {
			if ok {
				idComponents := strings.Split(string(variableID), "/")
				packageVariableIDMap[idComponents[1]] = string(variableID)
			}
		}
	}

	operatorList := &operatorsv1alpha1.OperatorList{}
	if err := r.Client.List(ctx, operatorList); err != nil {
		return ctrl.Result{}, err
	}

	for _, operator := range operatorList.Items {
		apimeta.SetStatusCondition(&operator.Status.Conditions, metav1.Condition{
			Type:               operatorsv1alpha1.TypeReady,
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: op.GetGeneration(),
		})
		if varID, ok := packageVariableIDMap[operator.Spec.PackageName]; ok {
			operator.Status.BundlePath = varID
		}
		if err := r.Client.Status().Update(ctx, &operator); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *OperatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.resolver = resolution.NewOperatorResolver(mgr.GetClient(), resolution.HardcodedEntitySource)

	err := ctrl.NewControllerManagedBy(mgr).
		For(&operatorsv1alpha1.Operator{}).
		Owns(&rukpakv1alpha1.BundleDeployment{}).
		Complete(r)

	if err != nil {
		return err
	}
	return nil
}
