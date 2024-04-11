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
	"io/fs"

	"github.com/operator-framework/operator-controller/internal/catalogmetadata"
)

type resolveOption struct {
	installedVersion string
}

type Resolver[T any] interface {
	Resolve(ctx context.Context, allBundles []*catalogmetadata.Bundle, object T, opts resolveOption) (*catalogmetadata.Bundle, error)
}

// Generic func signature cannot take variadic parameter.
type ResolveFunc[T any] func(ctx context.Context, allBundles []*catalogmetadata.Bundle, object T, opts resolveOption) (*catalogmetadata.Bundle, error)

func (r ResolveFunc[T]) Resolve(ctx context.Context, allBundles []*catalogmetadata.Bundle, object T, opts resolveOption) (*catalogmetadata.Bundle, error) {
	return r(ctx, allBundles, object, opts)
}

type Unpacker[T any] interface {
	Unpack(ctx context.Context, object T) (*Result, error)
}

// Result conveys progress information about unpacking bundle content.
type Result struct {
	// Bundle contains the full filesystem of a bundle's root directory.
	Bundle fs.FS

	// State is the current state of unpacking the bundle content.
	State State

	// Message is contextual information about the progress of unpacking the
	// bundle content.
	Message string
}

type State string

const (
	// StatePending conveys that a request for unpacking a bundle has been
	// acknowledged, but not yet started.
	StatePending State = "Pending"

	// StateUnpacking conveys that the source is currently unpacking a bundle.
	// This state should be used when the bundle contents are being downloaded
	// and processed.
	StateUnpacking State = "Unpacking"

	// StateUnpacked conveys that the bundle has been successfully unpacked.
	StateUnpacked State = "Unpacked"
)
