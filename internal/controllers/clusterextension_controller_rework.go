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

	ocv1alpha1 "github.com/operator-framework/operator-controller/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type Option func(*clusterExtensionReconcilerCombined)

type clusterExtensionReconcilerCombined struct {
	client.Client
	BundleProvider     BundleProvider
	Scheme             *runtime.Scheme
	resolver           Resolver[ocv1alpha1.ClusterExtension]
	unpacker           Unpacker[ocv1alpha1.ClusterExtension]
	supportedMediaType func(string) bool
}

func WithResolver(r Resolver[ocv1alpha1.ClusterExtension]) Option {
	return func(c *clusterExtensionReconcilerCombined) {
		c.resolver = r
	}
}

func WithUnpacker(r Unpacker[ocv1alpha1.ClusterExtension]) Option {
	return func(c *clusterExtensionReconcilerCombined) {
		c.unpacker = r
	}
}

func (r *clusterExtensionReconcilerCombined) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx).WithName("operator-controller")
	l.V(1).Info("starting")
	defer l.V(1).Info("ending")

	var existingExt = &ocv1alpha1.ClusterExtension{}
	if err := r.Get(ctx, req.NamespacedName, existingExt); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	reconciledExt := existingExt.DeepCopy()
	res, reconcileErr := r.reconcile(ctx, reconciledExt)

	// Do checks before any Update()s, as Update() may modify the resource structure!
	updateStatus := !equality.Semantic.DeepEqual(existingExt.Status, reconciledExt.Status)
	updateFinalizers := !equality.Semantic.DeepEqual(existingExt.Finalizers, reconciledExt.Finalizers)
	unexpectedFieldsChanged := checkForUnexpectedFieldChange(*existingExt, *reconciledExt)

	if updateStatus {
		if updateErr := r.Status().Update(ctx, reconciledExt); updateErr != nil {
			return res, utilerrors.NewAggregate([]error{reconcileErr, updateErr})
		}
	}

	if unexpectedFieldsChanged {
		panic("spec or metadata changed by reconciler")
	}

	if updateFinalizers {
		if updateErr := r.Update(ctx, reconciledExt); updateErr != nil {
			return res, utilerrors.NewAggregate([]error{reconcileErr, updateErr})
		}
	}
	return res, reconcileErr
}

func (r *clusterExtensionReconcilerCombined) reconcile(ctx context.Context, ext *ocv1alpha1.ClusterExtension) (ctrl.Result, error) {
	_ = log.FromContext(ctx).WithName("clusterextension-controller")

	if r.isPaused(ext) {
		// set the right status and condition
		return ctrl.Result{}, nil
	}

	allBundles, err := r.BundleProvider.Bundles(ctx)
	if err != nil {
		// set the right status and condition
		return ctrl.Result{}, err
	}

	installedVersion, err := r.getInstalledVersion(ctx, types.NamespacedName{Name: ext.GetName()})
	if err != nil {
		// set the right status and condition
		return ctrl.Result{}, err
	}

	bundle, err := r.resolver.Resolve(ctx, allBundles, *ext, resolveOption{installedVersion: installedVersion})
	if err != nil {
		// set the right status and condition
		return ctrl.Result{}, err
	}

	// Now we can set the Resolved Condition, and the resolvedBundleSource field to the bundle.Image value.
	ext.Status.ResolvedBundle = bundleMetadataFor(bundle)

	mediaType, err := bundle.MediaType()
	if err != nil {
		// set the right status and condition
		return ctrl.Result{}, err
	}

	if !r.supportedMediaType(mediaType) {
		// set the right status and condition
		return ctrl.Result{}, errors.New("unsupported bundle type")
	}

	return ctrl.Result{}, nil
}

func (r *clusterExtensionReconcilerCombined) isPaused(ext *ocv1alpha1.ClusterExtension) bool {
	if ext == nil {
		return false
	}
	return ext.Spec.Paused
}

func SetupWithManager(mgr manager.Manager, systemNsCache cache.Cache, systemNamespace string, opts ...Option) error {
	b := &clusterExtensionReconcilerCombined{
		Client: mgr.GetClient(),
	}

	for _, o := range opts {
		o(b)
	}

	// TODO: Add relevant watches.
	return ctrl.NewControllerManagedBy(mgr).For(&ocv1alpha1.Extension{}).Complete(b)
}

func (r *clusterExtensionReconcilerCombined) getInstalledVersion(ctx context.Context, namespacedName types.NamespacedName) (string, error) {
	return "", nil
}
