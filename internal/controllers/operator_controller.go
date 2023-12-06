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
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	catalogd "github.com/operator-framework/catalogd/api/core/v1alpha1"
	"github.com/operator-framework/deppy/pkg/deppy/solver"
	rukpakv1alpha1 "github.com/operator-framework/rukpak/api/v1alpha1"
	kappctrlv1alpha1 "github.com/vmware-tanzu/carvel-kapp-controller/pkg/apis/kappctrl/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	operatorsv1alpha1 "github.com/operator-framework/operator-controller/api/v1alpha1"
	"github.com/operator-framework/operator-controller/internal/catalogmetadata"
	"github.com/operator-framework/operator-controller/internal/controllers/validators"
	olmvariables "github.com/operator-framework/operator-controller/internal/resolution/variables"
)


var yttRegistryTemplate = `
#@ load("@ytt:yaml", "yaml")
#@ load("@ytt:data", "data")
#@ load("@ytt:assert", "assert")
#@ load("@ytt:overlay", "overlay")
#! format.star

#! getinstallns - get annotation value or default
#!
#@ def getinstallns(annotations, key, default):
#@   return annotations[key] if key in annotations else default
#@ end

#! validate target namespaces based on install modes
#!
#@ def validateTargetNamespaces():
#@  supportedInstallModes = data.values.spec.installModes
#@  for mode in supportedInstallModes:
#@     if mode.type == "AllNamespaces" and mode.supported:
#@        return
#@     end
#@  end
#@  assert.fail("expect all namespaces to be set")
#@ end

#@ def getAnnotations(depspec):
#@  if 'annotations' in depspec:
#@     return depspec.annotations + data.values.metadata.annotations
#@  end
#@ return data.values.metadata.annotations
#@ end

#@ def getAllPerms():
#@  res = {}
#@  if 'permissions' in data.values.spec.install.spec:
#@     res = data.values.spec.install.spec.permissions
#@  end
#@  if 'clusterPermissions' in data.values.spec.install.spec:
#@     res = data.values.spec.install.spec.clusterPermissions
#@  end
#@  return res
#@ end

#@ def getSAName(depspec):
#@  if 'serviceAccountName' in depspec:
#@    return depspec.serviceAccountName
#@  end
#@ return "default-test"
#@ end

#! get TargetNamespace based on install modes
#! 
#@ def getTargetNamespace():
#@  supportedInstallModes = data.values.spec.installModes
#@  for mode in supportedInstallModes:
#@     supported_modes = []
#@     if mode.supported:
#@       supported_modes.append(mode.type)
#@     end
#@  end
#@
#@  if "AllNamespaces" in supported_modes:
#@    return ""
#@  elif "OwnNamespace" in supported_modes:
#@    return getinstallns(data.values.metadata.annotations, "operatorframework.io/suggested-namespace", dep.name + "-system")
#@  end
#@
#@ end

#! get names hashed for cluster roles and roles
#!
#@ def gethashName(csvName, saName):
#@  baseName = "{}-{}".format(csvName, saName)
#@  hashStr =  (str(hash(baseName)))[:63-len(baseName)-1]
#@  return "{}-{}".format(baseName, hashStr)
#@ end

#! Deployments
#@ for dep in data.values.spec.install.spec.deployments:
---
apiVersion: apps/v1
kind: Deployment
metadata:
  annotations: #@ getAnnotations(dep.spec.template.metadata)
  name: #@ dep.name
  namespace: #@ getinstallns(data.values.metadata.annotations, "operatorframework.io/suggested-namespace", dep.name + "-system")
spec: #@ dep.spec
---
apiVersion: v1
kind: ServiceAccount
metadata:
  creationTimestamp: null
  name: #@ getSAName(dep.spec.template.spec)
  namespace: #@ getinstallns(data.values.metadata.annotations, "operatorframework.io/suggested-namespace", dep.name + "-system")
#@ end


#! Service account based on permissions and cluster permissions
#@ if 'permissions' in data.values.spec.install.spec:
#@ for perm in data.values.spec.install.spec.permissions:
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: #@ getSAName(perm)
  namespace: #@ getinstallns(data.values.metadata.annotations, "operatorframework.io/suggested-namespace", dep.name + "-system")
#@ end
#@ end


#@ if 'clusterPermissions' in data.values.spec.install.spec:
#@ for perm in data.values.spec.install.spec.clusterPermissions:
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: #@ getSAName(perm)
  namespace: #@ getinstallns(data.values.metadata.annotations, "operatorframework.io/suggested-namespace", dep.name + "-system")
#@ end
#@ end


#! Create cluster roles if in all namespace mode
#@ if getTargetNamespace() == "":
#@ allPerms = getAllPerms()
#@ for perm in allPerms:
#@ saName = getSAName(perm)
#@ clusterrolename = gethashName(data.values.metadata.name, saName)
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  namespace: #@ getinstallns(data.values.metadata.annotations, "operatorframework.io/suggested-namespace", dep.name + "-system")
  name: #@ clusterrolename
#@overlay/match by=overlay.subset({"rules": "..."})
rules:
#@ for rule in perm.rules:
- apiGroups:
  - ""
  resources: #@ rule.resources
  verbs: #@ rule.verbs
#@ end
- apiGroups:
  - ""
  resources:
  - namespaces
  verbs:
  - get
  - list
  - watch
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  creationTimestamp: null
  name: #@ clusterrolename
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: #@ clusterrolename
subjects:
- kind: ServiceAccount
  name: #@ saName
  namespace: #@ getinstallns(data.values.metadata.annotations, "operatorframework.io/suggested-namespace", dep.name + "-system")
#@ end
#@ end


#! Create roles and role bindings if !allnamespacemode
#!
#@ if getTargetNamespace() != "":
#@ if 'permissions' in data.values.spec.install.spec:
#@ for perm in data.values.spec.install.spec.permissions:
#@ saName = getSAName(perm)
#@ rolename = gethashName(data.values.metadata.name, saName)
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  namespace: #@ getinstallns(data.values.metadata.annotations, "operatorframework.io/suggested-namespace", dep.name + "-system")
  name: #@ rolename
rules: #@ perm.rules
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  creationTimestamp: null
  name: #@ rolename
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: #@ rolename
subjects:
- kind: ServiceAccount
  name: #@ saName
  namespace: #@ getinstallns(data.values.metadata.annotations, "operatorframework.io/suggested-namespace", dep.name + "-system")
#@ end
#@ end
#!
#!
#!
#@ if 'clusterPermissions' in data.values.spec.install.spec:
#@ for perm in data.values.spec.install.spec.clusterPermissions:
#@ saName = getSAName(perm)
#@ clusterrolename = gethashName(data.values.metadata.name, saName)
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  namespace: #@ getinstallns(data.values.metadata.annotations, "operatorframework.io/suggested-namespace", dep.name + "-system")
  name: #@ clusterrolename
#@overlay/match by=overlay.subset({"rules": "..."})
rules: #@ perm.rules
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  creationTimestamp: null
  name: #@ clusterrolename
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: #@ clusterrolename
subjects:
- kind: ServiceAccount
  name: #@ saName
  namespace: #@ getinstallns(data.values.metadata.annotations, "operatorframework.io/suggested-namespace", dep.name + "-system")
#@ end
#@ end
#@ end

#! namespace
#!
---
apiVersion: v1
kind: Namespace
metadata:
  creationTimestamp: null
  name: #@ getinstallns(data.values.metadata.annotations, "operatorframework.io/suggested-namespace", dep.name + "-system")
spec: {}
`

// OperatorReconciler reconciles a Operator object
type OperatorReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Resolver *solver.DeppySolver
}

//+kubebuilder:rbac:groups=operators.operatorframework.io,resources=operators,verbs=get;list;watch
//+kubebuilder:rbac:groups=operators.operatorframework.io,resources=operators/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=operators.operatorframework.io,resources=operators/finalizers,verbs=update

//+kubebuilder:rbac:groups=kappctrl.k14s.io,resources=apps,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=core.rukpak.io,resources=bundledeployments,verbs=get;list;watch;create;update;patch

//+kubebuilder:rbac:groups=catalogd.operatorframework.io,resources=catalogs,verbs=list;watch
//+kubebuilder:rbac:groups=catalogd.operatorframework.io,resources=catalogmetadata,verbs=list;watch

func (r *OperatorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx).WithName("operator-controller")
	l.V(1).Info("starting")
	defer l.V(1).Info("ending")

	var existingOp = &operatorsv1alpha1.Operator{}
	if err := r.Get(ctx, req.NamespacedName, existingOp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

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
//
// Today we always return ctrl.Result{} and an error.
// But in the future we might update this function
// to return different results (e.g. requeue).
//
//nolint:unparam
func (r *OperatorReconciler) reconcile(ctx context.Context, op *operatorsv1alpha1.Operator) (ctrl.Result, error) {
	fmt.Println("here reconcileed")

	// validate spec
	if err := validators.ValidateOperatorSpec(op); err != nil {
		// Set the TypeInstalled condition to Unknown to indicate that the resolution
		// hasn't been attempted yet, due to the spec being invalid.
		op.Status.InstalledBundleResource = ""
		setInstalledStatusConditionUnknown(&op.Status.Conditions, "installation has not been attempted as spec is invalid", op.GetGeneration())
		// Set the TypeResolved condition to Unknown to indicate that the resolution
		// hasn't been attempted yet, due to the spec being invalid.
		op.Status.ResolvedBundleResource = ""
		setResolvedStatusConditionUnknown(&op.Status.Conditions, "validation has not been attempted as spec is invalid", op.GetGeneration())
		return ctrl.Result{}, nil
	}
	// run resolution
	solution, err := r.Resolver.Solve(ctx)
	if err != nil {
		op.Status.InstalledBundleResource = ""
		setInstalledStatusConditionUnknown(&op.Status.Conditions, "installation has not been attempted as resolution failed", op.GetGeneration())
		op.Status.ResolvedBundleResource = ""
		setResolvedStatusConditionFailed(&op.Status.Conditions, err.Error(), op.GetGeneration())
		return ctrl.Result{}, err
	}

	if err := solution.Error(); err != nil {
		op.Status.InstalledBundleResource = ""
		setInstalledStatusConditionUnknown(&op.Status.Conditions, "installation has not been attempted as resolution is unsatisfiable", op.GetGeneration())
		op.Status.ResolvedBundleResource = ""
		setResolvedStatusConditionFailed(&op.Status.Conditions, err.Error(), op.GetGeneration())
		return ctrl.Result{}, err
	}

	// lookup the bundle in the solution that corresponds to the
	// Operator's desired package name.
	bundle, err := r.bundleFromSolution(solution, op.Spec.PackageName)
	if err != nil {
		op.Status.InstalledBundleResource = ""
		setInstalledStatusConditionUnknown(&op.Status.Conditions, "installation has not been attempted as resolution failed", op.GetGeneration())
		op.Status.ResolvedBundleResource = ""
		setResolvedStatusConditionFailed(&op.Status.Conditions, err.Error(), op.GetGeneration())
		return ctrl.Result{}, err
	}

	// Now we can set the Resolved Condition, and the resolvedBundleSource field to the bundle.Image value.
	op.Status.ResolvedBundleResource = bundle.Image
	setResolvedStatusConditionSuccess(&op.Status.Conditions, fmt.Sprintf("resolved to %q", bundle.Image), op.GetGeneration())

	mediaType, err := bundle.MediaType()
	if err != nil {
		setInstalledStatusConditionFailed(&op.Status.Conditions, err.Error(), op.GetGeneration())
		return ctrl.Result{}, err
	}
	// bundleProvisioner, err := mapBundleMediaTypeToBundleProvisioner(mediaType)
	// if err != nil {
	// 	setInstalledStatusConditionFailed(&op.Status.Conditions, err.Error(), op.GetGeneration())
	// 	return ctrl.Result{}, err
	// }

	csvName, err := r.getCSVName(*op, bundle)
	if err != nil {
		return ctrl.Result{}, err
	}
	app := r.generateExpectedApp(*op, bundle.Image, csvName, mediaType)

	if err := r.ensureApp(ctx, app); err != nil {
		return ctrl.Result{}, err
	}

	// Ensure a BundleDeployment exists with its bundle source from the bundle
	// image we just looked up in the solution.
	// dep := r.generateExpectedBundleDeployment(*op, bundle.Image, bundleProvisioner)
	// if err := r.ensureBundleDeployment(ctx, dep); err != nil {
	// 	// originally Reason: operatorsv1alpha1.ReasonInstallationFailed
	// 	op.Status.InstalledBundleResource = ""
	// 	setInstalledStatusConditionFailed(&op.Status.Conditions, err.Error(), op.GetGeneration())
	// 	return ctrl.Result{}, err
	// }

	// // convert existing unstructured object into bundleDeployment for easier mapping of status.
	// existingTypedBundleDeployment := &rukpakv1alpha1.BundleDeployment{}
	// if err := runtime.DefaultUnstructuredConverter.FromUnstructured(dep.UnstructuredContent(), existingTypedBundleDeployment); err != nil {
	// 	// originally Reason: operatorsv1alpha1.ReasonInstallationStatusUnknown
	// 	op.Status.InstalledBundleResource = ""
	// 	setInstalledStatusConditionUnknown(&op.Status.Conditions, err.Error(), op.GetGeneration())
	// 	return ctrl.Result{}, err
	// }

	// // Let's set the proper Installed condition and InstalledBundleResource field based on the
	// // existing BundleDeployment object status.
	// mapBDStatusToInstalledCondition(existingTypedBundleDeployment, op)

	// set the status of the operator based on the respective bundle deployment status conditions.
	return ctrl.Result{}, nil
}

func mapBDStatusToInstalledCondition(existingTypedBundleDeployment *rukpakv1alpha1.BundleDeployment, op *operatorsv1alpha1.Operator) {
	bundleDeploymentReady := apimeta.FindStatusCondition(existingTypedBundleDeployment.Status.Conditions, rukpakv1alpha1.TypeInstalled)
	if bundleDeploymentReady == nil {
		op.Status.InstalledBundleResource = ""
		setInstalledStatusConditionUnknown(&op.Status.Conditions, "bundledeployment status is unknown", op.GetGeneration())
		return
	}

	if bundleDeploymentReady.Status != metav1.ConditionTrue {
		op.Status.InstalledBundleResource = ""
		setInstalledStatusConditionFailed(
			&op.Status.Conditions,
			fmt.Sprintf("bundledeployment not ready: %s", bundleDeploymentReady.Message),
			op.GetGeneration(),
		)
		return
	}

	bundleDeploymentSource := existingTypedBundleDeployment.Spec.Template.Spec.Source
	switch bundleDeploymentSource.Type {
	case rukpakv1alpha1.SourceTypeImage:
		op.Status.InstalledBundleResource = bundleDeploymentSource.Image.Ref
		setInstalledStatusConditionSuccess(
			&op.Status.Conditions,
			fmt.Sprintf("installed from %q", bundleDeploymentSource.Image.Ref),
			op.GetGeneration(),
		)
	case rukpakv1alpha1.SourceTypeGit:
		resource := bundleDeploymentSource.Git.Repository + "@" + bundleDeploymentSource.Git.Ref.Commit
		op.Status.InstalledBundleResource = resource
		setInstalledStatusConditionSuccess(
			&op.Status.Conditions,
			fmt.Sprintf("installed from %q", resource),
			op.GetGeneration(),
		)
	default:
		op.Status.InstalledBundleResource = ""
		setInstalledStatusConditionUnknown(
			&op.Status.Conditions,
			fmt.Sprintf("unknown bundledeployment source type %q", bundleDeploymentSource.Type),
			op.GetGeneration(),
		)
	}
}

func (r *OperatorReconciler) bundleFromSolution(solution *solver.Solution, packageName string) (*catalogmetadata.Bundle, error) {
	for _, variable := range solution.SelectedVariables() {
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

func(r *OperatorReconciler) getCSVName(o operatorsv1alpha1.Operator, bundle *catalogmetadata.Bundle) (string, error) {
	if bundle == nil {
		return "", errors.New("nil bundle passed.")
	}
	ver, err := bundle.Version()
	if err != nil  {
		return "", err
	}

	if ver != nil {
		return "", errors.New("cannot find resolved version")
	}
	return fmt.Sprintf("%s.%s.clusterserviceversion.yaml", o.Spec.PackageName, ver), nil
}

func(r *OperatorReconciler) generateExpectedApp(o operatorsv1alpha1.Operator, bundlePath, csvName, mediaType string) *kappctrlv1alpha1.App {
	var (
		appSpecTemplate []kappctrlv1alpha1.AppTemplate
		subPath string = ""
	)
	if mediaType == catalogmetadata.MediaTypeRegistry {
		subPath = "manifests"
		appSpecTemplate = append(appSpecTemplate, kappctrlv1alpha1.AppTemplate{
			Ytt: &kappctrlv1alpha1.AppTemplateYtt{
				Inline: &kappctrlv1alpha1.AppFetchInline{
					Paths: map[string]string{
						"config.yml": yttRegistryTemplate,
					},
				},
				IgnoreUnknownComments: true,
				ValuesFrom: []kappctrlv1alpha1.AppTemplateValuesSource{
					{
						Path: csvName,
					},
				},
				FileMarks: []string{fmt.Sprintf("%s:exclude=true", csvName)},
			},
		})
	}
	
	app := &kappctrlv1alpha1.App{
		ObjectMeta: metav1.ObjectMeta{
			Name: o.GetName(),
			// for now hardcoding to "default"
			Namespace: "default",
		},
		Spec: kappctrlv1alpha1.AppSpec{
			// hardcoding to the SA that has cluster-admin priviledges
			ServiceAccountName: "default-ns-sa",
			Fetch: []kappctrlv1alpha1.AppFetch{
				{
					Image: &kappctrlv1alpha1.AppFetchImage{
						URL: bundlePath,
						SubPath: subPath,
					},
				},
			},
			Template: appSpecTemplate,
			Deploy: []kappctrlv1alpha1.AppDeploy{
				{
					Kapp: &kappctrlv1alpha1.AppDeployKapp{},
				},
			},
		},
	}
	app.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion:         operatorsv1alpha1.GroupVersion.String(),
			Kind:               "Operator",
			Name:               o.Name,
			UID:                o.UID,
			Controller:         pointer.Bool(true),
			BlockOwnerDeletion: pointer.Bool(true),
		},
	})
	return app
}

// SetupWithManager sets up the controller with the Manager.
func (r *OperatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		For(&operatorsv1alpha1.Operator{}).
		Watches(&catalogd.Catalog{},
			handler.EnqueueRequestsFromMapFunc(operatorRequestsForCatalog(mgr.GetClient(), mgr.GetLogger()))).
		Owns(&kappctrlv1alpha1.App{}).
		Complete(r)

	if err != nil {
		return err
	}
	return nil
}


func(r *OperatorReconciler) ensureApp(ctx context.Context, desiredAppInstance *kappctrlv1alpha1.App) error {
	existingAppInstance, err := r.existingApp(ctx, desiredAppInstance.Name)
	if client.IgnoreNotFound(err) != nil {
		return err
	}

	// Better to convert to unstructured as done with BD.
	if equality.Semantic.DeepDerivative(desiredAppInstance, existingAppInstance) {
		*desiredAppInstance = *existingAppInstance
		return nil
	}
	return r.Client.Patch(ctx, desiredAppInstance, client.Apply, client.ForceOwnership, client.FieldOwner("operator-controller"))
}

func(r *OperatorReconciler) existingApp(ctx context.Context, name string) (*kappctrlv1alpha1.App, error) {
	existingApp := &kappctrlv1alpha1.App{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: name}, existingApp); err != nil {
		return nil, err
	}
	return existingApp, nil
}


// mapBundleMediaTypeToBundleProvisioner maps an olm.bundle.mediatype property to a
// rukpak bundle provisioner class name that is capable of unpacking the bundle type
func mapBundleMediaTypeToBundleProvisioner(mediaType string) (string, error) {
	switch mediaType {
	case catalogmetadata.MediaTypePlain:
		return "core-rukpak-io-plain", nil
	// To ensure compatibility with bundles created with OLMv0 where the
	// olm.bundle.mediatype property doesn't exist, we assume that if the
	// property is empty (i.e doesn't exist) that the bundle is one created
	// with OLMv0 and therefore should use the registry provisioner
	case catalogmetadata.MediaTypeRegistry, "":
		return "core-rukpak-io-registry", nil
	default:
		return "", fmt.Errorf("unknown bundle mediatype: %s", mediaType)
	}
}

// setResolvedStatusConditionSuccess sets the resolved status condition to success.
func setResolvedStatusConditionSuccess(conditions *[]metav1.Condition, message string, generation int64) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:               operatorsv1alpha1.TypeResolved,
		Status:             metav1.ConditionTrue,
		Reason:             operatorsv1alpha1.ReasonSuccess,
		Message:            message,
		ObservedGeneration: generation,
	})
}

// setResolvedStatusConditionFailed sets the resolved status condition to failed.
func setResolvedStatusConditionFailed(conditions *[]metav1.Condition, message string, generation int64) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:               operatorsv1alpha1.TypeResolved,
		Status:             metav1.ConditionFalse,
		Reason:             operatorsv1alpha1.ReasonResolutionFailed,
		Message:            message,
		ObservedGeneration: generation,
	})
}

// setResolvedStatusConditionUnknown sets the resolved status condition to unknown.
func setResolvedStatusConditionUnknown(conditions *[]metav1.Condition, message string, generation int64) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:               operatorsv1alpha1.TypeResolved,
		Status:             metav1.ConditionUnknown,
		Reason:             operatorsv1alpha1.ReasonResolutionUnknown,
		Message:            message,
		ObservedGeneration: generation,
	})
}

// setInstalledStatusConditionSuccess sets the installed status condition to success.
func setInstalledStatusConditionSuccess(conditions *[]metav1.Condition, message string, generation int64) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:               operatorsv1alpha1.TypeInstalled,
		Status:             metav1.ConditionTrue,
		Reason:             operatorsv1alpha1.ReasonSuccess,
		Message:            message,
		ObservedGeneration: generation,
	})
}

// setInstalledStatusConditionFailed sets the installed status condition to failed.
func setInstalledStatusConditionFailed(conditions *[]metav1.Condition, message string, generation int64) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:               operatorsv1alpha1.TypeInstalled,
		Status:             metav1.ConditionFalse,
		Reason:             operatorsv1alpha1.ReasonInstallationFailed,
		Message:            message,
		ObservedGeneration: generation,
	})
}

// setInstalledStatusConditionUnknown sets the installed status condition to unknown.
func setInstalledStatusConditionUnknown(conditions *[]metav1.Condition, message string, generation int64) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:               operatorsv1alpha1.TypeInstalled,
		Status:             metav1.ConditionUnknown,
		Reason:             operatorsv1alpha1.ReasonInstallationStatusUnknown,
		Message:            message,
		ObservedGeneration: generation,
	})
}

// Generate reconcile requests for all operators affected by a catalog change
func operatorRequestsForCatalog(c client.Reader, logger logr.Logger) handler.MapFunc {
	return func(ctx context.Context, _ client.Object) []reconcile.Request {
		// no way of associating an operator to a catalog so create reconcile requests for everything
		operators := operatorsv1alpha1.OperatorList{}
		err := c.List(ctx, &operators)
		if err != nil {
			logger.Error(err, "unable to enqueue operators for catalog reconcile")
			return nil
		}
		var requests []reconcile.Request
		for _, op := range operators.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: op.GetNamespace(),
					Name:      op.GetName(),
				},
			})
		}
		return requests
	}
}
