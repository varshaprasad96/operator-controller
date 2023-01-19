package variable_sources

import (
	"context"
	"fmt"
	"sort"

	"github.com/blang/semver/v4"

	"github.com/operator-framework/deppy/pkg/ext/olm"

	"github.com/operator-framework/deppy/pkg/ext/olm/variable_sources/util"

	"github.com/operator-framework/deppy/pkg/deppy"
	"github.com/operator-framework/deppy/pkg/deppy/constraint"
	"github.com/operator-framework/deppy/pkg/deppy/input"
)

type BundleVariable struct {
	*input.SimpleVariable
	bundleEntity *olm.BundleEntity
	dependencies []*olm.BundleEntity
}

func (b *BundleVariable) BundleEntity() *olm.BundleEntity {
	return b.bundleEntity
}

func (b *BundleVariable) Dependencies() []*olm.BundleEntity {
	return b.dependencies
}

func NewBundleVariable(bundleEntity *olm.BundleEntity, dependencyBundleEntities []*olm.BundleEntity) *BundleVariable {
	var dependencyIDs []deppy.Identifier
	for _, bundle := range dependencyBundleEntities {
		dependencyIDs = append(dependencyIDs, bundle.ID)
	}
	var constraints []deppy.Constraint
	if len(dependencyIDs) > 0 {
		constraints = append(constraints, constraint.Dependency(dependencyIDs...))
	}
	return &BundleVariable{
		SimpleVariable: input.NewSimpleVariable(bundleEntity.ID, constraints...),
		bundleEntity:   bundleEntity,
		dependencies:   dependencyBundleEntities,
	}
}

var _ input.VariableSource = &BundlesAndDepsVariableSource{}

type BundlesAndDepsVariableSource struct {
	variableSources []input.VariableSource
}

func NewBundlesAndDepsVariableSource(inputVariableSources ...input.VariableSource) *BundlesAndDepsVariableSource {
	return &BundlesAndDepsVariableSource{
		variableSources: inputVariableSources,
	}
}

func (b *BundlesAndDepsVariableSource) GetVariables(ctx context.Context, entitySource input.EntitySource) ([]deppy.Variable, error) {
	var variables []deppy.Variable

	// extract required package variables
	for _, variableSource := range b.variableSources {
		inputVariables, err := variableSource.GetVariables(ctx, entitySource)
		if err != nil {
			return nil, err
		}
		variables = append(variables, inputVariables...)
	}

	// create bundle queue for dependency resolution
	var bundleEntityQueue []*olm.BundleEntity
	for _, variable := range variables {
		switch v := variable.(type) {
		case *RequiredPackageVariable:
			for _, bundleEntity := range v.BundleEntities() {
				bundleEntityQueue = append(bundleEntityQueue, bundleEntity)
			}
		}
	}

	// build bundle and dependency variables
	visited := map[deppy.Identifier]struct{}{}
	for len(bundleEntityQueue) > 0 {
		// pop head of queue
		var head *olm.BundleEntity
		head, bundleEntityQueue = bundleEntityQueue[0], bundleEntityQueue[1:]

		// ignore bundles that have already been processed
		if _, ok := visited[head.ID]; ok {
			continue
		}
		visited[head.ID] = struct{}{}

		// get bundle dependencies
		dependencyEntityBundles, err := b.getEntityDependencies(ctx, head, entitySource)
		if err != nil {
			return nil, fmt.Errorf("could not determine dependencies for entity with id '%s': %s", head.ID, err)
		}

		// add bundle dependencies to queue for processing
		bundleEntityQueue = append(bundleEntityQueue, dependencyEntityBundles...)

		// create variable
		variables = append(variables, NewBundleVariable(head, dependencyEntityBundles))
	}

	return variables, nil
}

func (b *BundlesAndDepsVariableSource) getEntityDependencies(ctx context.Context, bundleEntity *olm.BundleEntity, entitySource input.EntitySource) ([]*olm.BundleEntity, error) {
	var dependencies []*olm.BundleEntity

	// gather required package dependencies
	requiredPackages, _ := bundleEntity.RequiredPackages()
	for _, requiredPackage := range requiredPackages {
		semverRange, err := semver.ParseRange(requiredPackage.VersionRange)
		if err != nil {
			return nil, err
		}
		packageDependencyBundles, err := entitySource.Filter(ctx, input.And(util.WithPackageName(requiredPackage.PackageName), util.InSemverRange(semverRange)))
		if err != nil {
			return nil, err
		}
		for _, entity := range packageDependencyBundles {
			dependencies = append(dependencies, olm.NewBundleEntity(&entity))
		}
	}

	// gather required gvk dependencies
	gvkDependencies, _ := bundleEntity.RequiredGVKs()
	for i := 0; i < len(gvkDependencies); i++ {
		gvkDependencyBundles, err := entitySource.Filter(ctx, util.ProvidesGVK(&gvkDependencies[i]))
		if err != nil {
			return nil, err
		}
		for _, entity := range gvkDependencyBundles {
			dependencies = append(dependencies, olm.NewBundleEntity(&entity))
		}
	}

	// sort bundles in version order
	sort.SliceStable(dependencies, func(i, j int) bool {
		return util.ByChannelAndVersion(dependencies[i].Entity, dependencies[j].Entity)
	})

	return dependencies, nil

}
