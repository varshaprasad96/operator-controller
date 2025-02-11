package variable_sources

import (
	"context"
	"fmt"
	"sort"

	"github.com/blang/semver/v4"
	"github.com/operator-framework/deppy/pkg/deppy"
	"github.com/operator-framework/deppy/pkg/deppy/constraint"
	"github.com/operator-framework/deppy/pkg/deppy/input"
)

type BundleVariable struct {
	*input.SimpleVariable
	bundleEntity *BundleEntity
	dependencies []*BundleEntity
}

func (b *BundleVariable) BundleEntity() *BundleEntity {
	return b.bundleEntity
}

func (b *BundleVariable) Dependencies() []*BundleEntity {
	return b.dependencies
}

func NewBundleVariable(bundleEntity *BundleEntity, dependencyBundleEntities []*BundleEntity) *BundleVariable {
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
	var bundleEntityQueue []*BundleEntity
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
		var head *BundleEntity
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

func (b *BundlesAndDepsVariableSource) getEntityDependencies(ctx context.Context, bundleEntity *BundleEntity, entitySource input.EntitySource) ([]*BundleEntity, error) {
	var dependencies []*BundleEntity
	added := map[deppy.Identifier]struct{}{}

	// gather required package dependencies
	// todo(perdasilva): disambiguate between not found and actual errors
	requiredPackages, _ := bundleEntity.RequiredPackages()
	for _, requiredPackage := range requiredPackages {
		semverRange, err := semver.ParseRange(requiredPackage.VersionRange)
		if err != nil {
			return nil, err
		}
		packageDependencyBundles, err := entitySource.Filter(ctx, input.And(WithPackageName(requiredPackage.PackageName), InSemverRange(semverRange)))
		if err != nil {
			return nil, err
		}
		if len(packageDependencyBundles) == 0 {
			return nil, fmt.Errorf("could not find package dependencies for bundle '%s'", bundleEntity.ID)
		}
		for i := 0; i < len(packageDependencyBundles); i++ {
			entity := packageDependencyBundles[i]
			if _, ok := added[entity.ID]; !ok {
				dependencies = append(dependencies, NewBundleEntity(&entity))
				added[entity.ID] = struct{}{}
			}
		}
	}

	// gather required gvk dependencies
	// todo(perdasilva): disambiguate between not found and actual errors
	gvkDependencies, _ := bundleEntity.RequiredGVKs()
	for i := 0; i < len(gvkDependencies); i++ {
		gvkDependencyBundles, err := entitySource.Filter(ctx, ProvidesGVK(&gvkDependencies[i]))
		if err != nil {
			return nil, err
		}
		if len(gvkDependencyBundles) == 0 {
			return nil, fmt.Errorf("could not find gvk dependencies for bundle '%s'", bundleEntity.ID)
		}
		for i := 0; i < len(gvkDependencyBundles); i++ {
			entity := gvkDependencyBundles[i]
			if _, ok := added[entity.ID]; !ok {
				dependencies = append(dependencies, NewBundleEntity(&entity))
				added[entity.ID] = struct{}{}
			}
		}
	}

	// sort bundles in version order
	sort.SliceStable(dependencies, func(i, j int) bool {
		return ByChannelAndVersion(dependencies[i].Entity, dependencies[j].Entity)
	})

	return dependencies, nil

}
