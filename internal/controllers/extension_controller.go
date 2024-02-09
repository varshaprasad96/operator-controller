/*
Copyright 2024.

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
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	catalogd "github.com/operator-framework/catalogd/api/core/v1alpha1"
	"github.com/operator-framework/deppy/pkg/deppy"
	"github.com/operator-framework/deppy/pkg/deppy/solver"
	rukpakv1alpha2 "github.com/operator-framework/rukpak/api/v1alpha2"

	ocv1alpha1 "github.com/operator-framework/operator-controller/api/v1alpha1"
	"github.com/operator-framework/operator-controller/internal"
	"github.com/operator-framework/operator-controller/internal/catalogmetadata"
	"github.com/operator-framework/operator-controller/internal/controllers/validators"
	olmvariables "github.com/operator-framework/operator-controller/internal/resolution/variables"
	kappctrlv1alpha1 "github.com/vmware-tanzu/carvel-kapp-controller/pkg/apis/kappctrl/v1alpha1"
)

// ExtensionReconciler reconciles a Extension object
type ExtensionReconciler struct {
	client.Client
	BundleProvider BundleProvider
	Scheme         *runtime.Scheme
	Resolver       *solver.Solver
	HasKappApis bool
}

var kappApiUnavailableError error = errors.New("kapp-controller apis unavailable on cluster.")

//+kubebuilder:rbac:groups=olm.operatorframework.io,resources=extensions,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=olm.operatorframework.io,resources=extensions/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=olm.operatorframework.io,resources=extensions/finalizers,verbs=update
//+kubebuilder:rbac:groups=kappctrl.k14s.io,resources=apps,verbs=get;list;watch;create;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ExtensionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx).WithName("extension-controller")
	l.V(1).Info("starting")
	defer l.V(1).Info("ending")

	var existingExt = &ocv1alpha1.Extension{}
	if err := r.Client.Get(ctx, req.NamespacedName, existingExt); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	reconciledExt := existingExt.DeepCopy()
	res, reconcileErr := r.reconcile(ctx, reconciledExt)

	// Do checks before any Update()s, as Update() may modify the resource structure!
	updateStatus := !equality.Semantic.DeepEqual(existingExt.Status, reconciledExt.Status)
	updateFinalizers := !equality.Semantic.DeepEqual(existingExt.Finalizers, reconciledExt.Finalizers)
	unexpectedFieldsChanged := r.checkForUnexpectedFieldChange(*existingExt, *reconciledExt)

	if updateStatus {
		if updateErr := r.Client.Status().Update(ctx, reconciledExt); updateErr != nil {
			return res, utilerrors.NewAggregate([]error{reconcileErr, updateErr})
		}
	}

	if unexpectedFieldsChanged {
		panic("spec or metadata changed by reconciler")
	}

	if updateFinalizers {
		if updateErr := r.Client.Update(ctx, reconciledExt); updateErr != nil {
			return res, utilerrors.NewAggregate([]error{reconcileErr, updateErr})
		}
	}

	return res, reconcileErr
}

// Compare resources - ignoring status & metadata.finalizers
func (*ExtensionReconciler) checkForUnexpectedFieldChange(a, b ocv1alpha1.Extension) bool {
	a.Status, b.Status = ocv1alpha1.ExtensionStatus{}, ocv1alpha1.ExtensionStatus{}
	a.Finalizers, b.Finalizers = []string{}, []string{}
	return !equality.Semantic.DeepEqual(a, b)
}

// Helper function to do the actual reconcile
//
// Today we always return ctrl.Result{} and an error.
// But in the future we might update this function
// to return different results (e.g. requeue).
//
//nolint:unparam
func (r *ExtensionReconciler) reconcile(ctx context.Context, ext *ocv1alpha1.Extension) (ctrl.Result, error) {

	// Don't do anything if Paused
	if ext.Spec.Managed == ocv1alpha1.ManagedStatePaused {
		return ctrl.Result{}, nil
	}

	// validate spec
	if err := validators.ValidateExtensionSpec(ext); err != nil {
		// Set the TypeInstalled condition to Unknown to indicate that the resolution
		// hasn't been attempted yet, due to the spec being invalid.
		ext.Status.InstalledBundleResource = ""
		setInstalledStatusConditionUnknown(&ext.Status.Conditions, "installation has not been attempted as spec is invalid", ext.GetGeneration())
		// Set the TypeResolved condition to Unknown to indicate that the resolution
		// hasn't been attempted yet, due to the spec being invalid.
		ext.Status.ResolvedBundleResource = ""
		setResolvedStatusConditionUnknown(&ext.Status.Conditions, "validation has not been attempted as spec is invalid", ext.GetGeneration())

		setDeprecationStatusesUnknown(&ext.Status.Conditions, "deprecation checks have not been attempted as spec is invalid", ext.GetGeneration())
		return ctrl.Result{}, nil
	}

	// gather vars for resolution
	vars, err := r.variables(ctx)
	if err != nil {
		ext.Status.InstalledBundleResource = ""
		setInstalledStatusConditionUnknown(&ext.Status.Conditions, "installation has not been attempted due to failure to gather data for resolution", ext.GetGeneration())
		ext.Status.ResolvedBundleResource = ""
		setResolvedStatusConditionFailed(&ext.Status.Conditions, err.Error(), ext.GetGeneration())

		setDeprecationStatusesUnknown(&ext.Status.Conditions, "deprecation checks have not been attempted due to failure to gather data for resolution", ext.GetGeneration())
		return ctrl.Result{}, err
	}

	// run resolution
	selection, err := r.Resolver.Solve(vars)
	if err != nil {
		ext.Status.InstalledBundleResource = ""
		setInstalledStatusConditionUnknown(&ext.Status.Conditions, "installation has not been attempted as resolution failed", ext.GetGeneration())
		ext.Status.ResolvedBundleResource = ""
		setResolvedStatusConditionFailed(&ext.Status.Conditions, err.Error(), ext.GetGeneration())

		setDeprecationStatusesUnknown(&ext.Status.Conditions, "deprecation checks have not been attempted as resolution failed", ext.GetGeneration())
		return ctrl.Result{}, err
	}

	// lookup the bundle in the solution that corresponds to the
	// ClusterExtension's desired package name.
	bundle, err := r.bundleFromSolution(selection, ext.Spec.Source.Package.Name)
	if err != nil {
		ext.Status.InstalledBundleResource = ""
		setInstalledStatusConditionUnknown(&ext.Status.Conditions, "installation has not been attempted as resolution failed", ext.GetGeneration())
		ext.Status.ResolvedBundleResource = ""
		setResolvedStatusConditionFailed(&ext.Status.Conditions, err.Error(), ext.GetGeneration())

		setDeprecationStatusesUnknown(&ext.Status.Conditions, "deprecation checks have not been attempted as resolution failed", ext.GetGeneration())
		return ctrl.Result{}, err
	}

	// TODO: kapp-controller integration
	// * lookup the bundle in the solution/selection that corresponds to the Extension's desired Source
	// * set the status of the Extension based on the respective deployed application status conditions.

	// Now we can set the Resolved Condition, and the resolvedBundleSource field to the bundle.Image value.
	ext.Status.ResolvedBundleResource = bundle.Image
	setResolvedStatusConditionSuccess(&ext.Status.Conditions, fmt.Sprintf("resolved to %q", bundle.Image), ext.GetGeneration())

	// TODO: Question - Should we set the deprecation statuses after we have successfully resolved instead of after a successful installation?

	mediaType, err := bundle.MediaType()
	if err != nil {
		setInstalledStatusConditionFailed(&ext.Status.Conditions, err.Error(), ext.GetGeneration())
		setDeprecationStatusesUnknown(&ext.Status.Conditions, "deprecation checks have not been attempted as installation has failed", ext.GetGeneration())
		return ctrl.Result{}, err
	}

	if !r.HasKappApis {
		setInstalledStatusConditionFailed(&ext.Status.Conditions, kappApiUnavailableError.Error(), ext.GetGeneration())
		setDeprecationStatusesUnknown(&ext.Status.Conditions, "deprecation checks have not been attempted as installation has failed", ext.GetGeneration())
		return ctrl.Result{}, kappApiUnavailableError
	}

	if mediaType != catalogmetadata.MediaTypeRegistry {
		app := r.GenerateExpectedApp(*ext, bundle.Image)
		if err := r.ensureApp(ctx, app); err != nil {
			// originally Reason: ocv1alpha1.ReasonInstallationFailed
			ext.Status.InstalledBundleResource = ""
			setInstalledStatusConditionFailed(&ext.Status.Conditions, err.Error(), ext.GetGeneration())
			setDeprecationStatusesUnknown(&ext.Status.Conditions, "deprecation checks have not been attempted as installation has failed", ext.GetGeneration())
			return ctrl.Result{}, err
		}

		// Converting into structured so that we can map the relevant status to Extension. 
		existingTypedApp := &kappctrlv1alpha1.App{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(app.UnstructuredContent(), existingTypedApp); err != nil {
			// originally Reason: ocv1alpha1.ReasonInstallationStatusUnknown
			

			
			ext.Status.InstalledBundleResource = ""
			setInstalledStatusConditionUnknown(&ext.Status.Conditions, err.Error(), ext.GetGeneration())
			setDeprecationStatusesUnknown(&ext.Status.Conditions, "deprecation checks have not been attempted as installation has failed", ext.GetGeneration())
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ExtensionReconciler) ensureApp(ctx context.Context, desiredApp *unstructured.Unstructured) error {
	existingApp, err := r.existingAppUnstructured(ctx, desiredApp.GetName(), desiredApp.GetNamespace())
	if client.IgnoreNotFound(err) != nil {
		return err
	}

	// If the existing BD already has everything that the desired BD has, no need to contact the API server.
	// Make sure the status of the existingBD from the server is as expected.
	if equality.Semantic.DeepDerivative(desiredApp, existingApp) {
		*desiredApp = *existingApp
		return nil
	}

	return r.Client.Patch(ctx, desiredApp, client.Apply, client.ForceOwnership, client.FieldOwner("operator-controller"))
}

func (r *ExtensionReconciler) existingAppUnstructured(ctx context.Context, name, namespace string) (*unstructured.Unstructured, error) {
	existingApp := &kappctrlv1alpha1.App{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, existingApp)
	if err != nil {
		return nil, err
	}
	existingApp.APIVersion = "kappctrl.k14s.io/v1alpha1"
	existingApp.Kind = "App"
	unstrExistingAppObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(existingApp)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: unstrExistingAppObj}, nil
}


func (r *ExtensionReconciler) GenerateExpectedApp(o ocv1alpha1.Extension, bundlePath string) *unstructured.Unstructured {
	// We use unstructured here to avoid problems of serializing default values when sending patches to the apiserver.
	// If you use a typed object, any default values from that struct get serialized into the JSON patch, which could
	// cause unrelated fields to be patched back to the default value even though that isn't the intention. Using an
	// unstructured ensures that the patch contains only what is specified. Using unstructured like this is basically
	// identical to "kubectl apply -f"
	spec := map[string]interface{}{
		"serviceAccountName": o.Spec.ServiceAccountName,
		"fetch": []interface{}{
			map[string]interface{}{
				"image": map[string]interface{}{
					"url": bundlePath,
				},
			},
		},
		"template": []interface{}{
			map[string]interface{}{
				"ytt": map[string]interface{}{},
			},
		},
		"deploy": []interface{}{
			map[string]interface{}{
				"kapp": map[string]interface{}{},
			},
		},
	}

	app := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "kappctrl.k14s.io/v1alpha1",
			"kind": "App",
			"metadata": map[string]interface{}{
				"name":      o.GetName(),
				"namespace": o.GetNamespace(),
			},
			"spec": spec,
		},
	}

	app.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion:         ocv1alpha1.GroupVersion.String(),
			Kind:               "Extension",
			Name:               o.Name,
			UID:                o.UID,
			Controller:         pointer.Bool(true),
			BlockOwnerDeletion: pointer.Bool(true),
		},
	})
	return app
}


func (r *ExtensionReconciler) bundleFromSolution(selection []deppy.Variable, packageName string) (*catalogmetadata.Bundle, error) {
	for _, variable := range selection {
		switch v := variable.(type) {
		case *olmvariables.BundleVariable:
			bundlePkgName := v.Bundle().Package
			if packageName == bundlePkgName {
				return v.Bundle(), nil
			}
		}
	}
	return nil, fmt.Errorf("bundle for package %q not found in solution", packageName)
}

func (r *ExtensionReconciler) variables(ctx context.Context) ([]deppy.Variable, error) {
	allBundles, err := r.BundleProvider.Bundles(ctx)
	if err != nil {
		return nil, err
	}
	extensionList := ocv1alpha1.ExtensionList{}
	if err := r.Client.List(ctx, &extensionList); err != nil {
		return nil, err
	}
	bundleDeploymentList := rukpakv1alpha2.BundleDeploymentList{}
	if err := r.Client.List(ctx, &bundleDeploymentList); err != nil {
		return nil, err
	}

	return GenerateVariables(allBundles, internal.ExtensionArrayToInterface(extensionList.Items), bundleDeploymentList.Items)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ExtensionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// TODO: Add watch for kapp-controller resources
	return ctrl.NewControllerManagedBy(mgr).
		For(&ocv1alpha1.Extension{}).
		// Enqueue apps only owned by extension?
		Owns(&kappctrlv1alpha1.App{}).
		Watches(&catalogd.Catalog{},
			handler.EnqueueRequestsFromMapFunc(extensionRequestsForCatalog(mgr.GetClient(), mgr.GetLogger()))).
		Complete(r)
}

// Generate reconcile requests for all extensions affected by a catalog change
func extensionRequestsForCatalog(c client.Reader, logger logr.Logger) handler.MapFunc {
	return func(ctx context.Context, _ client.Object) []reconcile.Request {
		// no way of associating an extension to a catalog so create reconcile requests for everything
		extensions := ocv1alpha1.ExtensionList{}
		err := c.List(ctx, &extensions)
		if err != nil {
			logger.Error(err, "unable to enqueue extensions for catalog reconcile")
			return nil
		}
		var requests []reconcile.Request
		for _, ext := range extensions.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: ext.GetNamespace(),
					Name:      ext.GetName(),
				},
			})
		}
		return requests
	}
}
