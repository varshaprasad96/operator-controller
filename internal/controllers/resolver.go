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
	"fmt"
	"sort"

	mmsemver "github.com/Masterminds/semver/v3"
	ocv1alpha1 "github.com/operator-framework/operator-controller/api/v1alpha1"
	"github.com/operator-framework/operator-controller/internal/catalogmetadata"
	catalogfilter "github.com/operator-framework/operator-controller/internal/catalogmetadata/filter"
	catalogsort "github.com/operator-framework/operator-controller/internal/catalogmetadata/sort"
)

var ClusterExtensionResolver Resolver[ocv1alpha1.ClusterExtension] = ResolveFunc[ocv1alpha1.ClusterExtension](Resolve)

func Resolve(ctx context.Context, allBundles []*catalogmetadata.Bundle, extension ocv1alpha1.ClusterExtension, opts resolveOption) (*catalogmetadata.Bundle, error) {
	packageName := extension.Spec.Source.Name
	channelName := extension.Spec.Source.Channel
	versionRange := extension.Spec.Source.Version

	predicates := []catalogfilter.Predicate[catalogmetadata.Bundle]{
		catalogfilter.WithPackageName(packageName),
	}

	if channelName != "" {
		predicates = append(predicates, catalogfilter.InChannel(channelName))
	}

	if versionRange != "" {
		vr, err := mmsemver.NewConstraint(versionRange)
		if err != nil {
			return nil, fmt.Errorf("invalid version range %q: %w", versionRange, err)
		}
		predicates = append(predicates, catalogfilter.InMastermindsSemverRange(vr))
	}
	resultSet := catalogfilter.Filter(allBundles, catalogfilter.And(predicates...))

	if len(resultSet) == 0 {
		var versionError, channelError, existingVersionError string
		if versionRange != "" {
			versionError = fmt.Sprintf(" matching version %q", versionRange)
		}
		if channelName != "" {
			channelError = fmt.Sprintf(" in channel %q", channelName)
		}
		if opts.installedVersion != "" {
			existingVersionError = fmt.Sprintf(" which upgrades currently installed version %q", opts.installedVersion)
		}
		return nil, fmt.Errorf("no package %q%s%s%s found", packageName, versionError, channelError, existingVersionError)
	}

	sort.SliceStable(resultSet, func(i, j int) bool {
		return catalogsort.ByVersion(resultSet[i], resultSet[j])
	})
	sort.SliceStable(resultSet, func(i, j int) bool {
		return catalogsort.ByDeprecated(resultSet[i], resultSet[j])
	})
	return resultSet[0], nil
}
