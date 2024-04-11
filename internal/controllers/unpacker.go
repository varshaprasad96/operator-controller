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

	ocv1alpha1 "github.com/operator-framework/operator-controller/api/v1alpha1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type image struct {
	Client       client.Client
	KubeClient   kubernetes.Interface
	PodNamespace string
	UnpackImage  string
}

func NewImageUnpackerForClusterExtension(client client.Client, kubeClient kubernetes.Interface, podNamespace, unpackImage string) Unpacker[ocv1alpha1.ClusterExtension] {
	return &image{
		Client:       client,
		KubeClient:   kubeClient,
		PodNamespace: podNamespace,
		UnpackImage:  unpackImage,
	}
}

func (i *image) Unpack(ctx context.Context, clusterExtension ocv1alpha1.ClusterExtension) (*Result, error) {
	return nil, nil
}
