package resolution

import (
	"fmt"
	"sync"

	"github.com/blang/semver/v4"
	"github.com/operator-framework/operator-registry/alpha/property"
	"github.com/operator-framework/operator-registry/pkg/api"

	"github.com/operator-framework/deppy/pkg/lib/util"

	"github.com/operator-framework/deppy/pkg/deppy/input"
)

type BundleEntity struct {
	*input.Entity

	// these properties are lazy loaded as they are requested
	packageName      string
	version          string
	providedGVKs     []api.GroupVersionKind
	requiredGVKs     []api.GroupVersionKind
	requiredPackages []property.PackageRequired
	channel          string
	semVersion       *semver.Version
	mu               sync.RWMutex
}

func NewBundleEntity(entity *input.Entity) *BundleEntity {
	return &BundleEntity{
		Entity: entity,
		mu:     sync.RWMutex{},
	}
}

func (b *BundleEntity) PackageName() (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.packageName == "" {
		bundlePackage, err := loadFromEntity[property.Package](b.Entity, property.TypePackage)
		if err != nil {
			return "", fmt.Errorf("error determining package for entity '%s': %s", b.ID, err)
		}
		b.packageName = bundlePackage.PackageName

		// set version as well since we get that for free
		b.version = bundlePackage.Version
	}
	return b.packageName, nil
}

func (b *BundleEntity) Version() (*semver.Version, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.version == "" {
		bundlePackage, err := loadFromEntity[property.Package](b.Entity, property.TypePackage)
		if err != nil {
			return nil, fmt.Errorf("error determining bundle version for entity '%s': %s", b.ID, err)
		}
		b.version = bundlePackage.Version

		// set package name as well since we get that for free
		b.packageName = bundlePackage.PackageName

	}
	if b.semVersion == nil {
		semVer, err := semver.Parse(b.version)
		if err != nil {
			return nil, fmt.Errorf("could not parse semver (%s) for entity '%s': %s", b.version, b.ID, err)
		}
		b.semVersion = &semVer
	}
	return b.semVersion, nil
}

func (b *BundleEntity) ProvidedGVKs() ([]api.GroupVersionKind, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.providedGVKs == nil {
		providedGVKs, err := loadFromEntity[[]api.GroupVersionKind](b.Entity, property.TypeGVK)
		if err != nil {
			return nil, fmt.Errorf("could not determine bundle provided gvks for entity '%s': %s", b.ID, err)
		}
		b.providedGVKs = providedGVKs
	}
	return b.providedGVKs, nil
}

func (b *BundleEntity) RequiredGVKs() ([]api.GroupVersionKind, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.requiredGVKs == nil {
		requiredGVKs, err := loadFromEntity[[]api.GroupVersionKind](b.Entity, property.TypeGVKRequired)
		if err != nil {
			return nil, fmt.Errorf("could not determine bundle required gvks for entity '%s': %s", b.ID, err)
		}
		b.requiredGVKs = requiredGVKs
	}
	return b.requiredGVKs, nil
}

func (b *BundleEntity) RequiredPackages() ([]property.PackageRequired, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.requiredPackages == nil {
		requiredPackages, err := loadFromEntity[[]property.PackageRequired](b.Entity, property.TypePackageRequired)
		if err != nil {
			return nil, fmt.Errorf("could not determine bundle required gvks for entity '%s': %s", b.ID, err)
		}
		b.requiredPackages = requiredPackages
	}
	return b.requiredPackages, nil
}

func (b *BundleEntity) Channel() (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.channel == "" {
		channel, err := loadFromEntity[string](b.Entity, property.TypeChannel)
		if err != nil {
			return "", fmt.Errorf("could not determine bundle required gvks for entity '%s': %s", b.ID, err)
		}
		b.channel = channel
	}
	return b.channel, nil
}

func loadFromEntity[T interface{}](entity *input.Entity, propertyName string) (T, error) {
	deserializedProperty := *new(T)
	propertyValue, ok := entity.Properties[propertyName]
	if !ok {
		return deserializedProperty, fmt.Errorf("property '%s' not found", propertyName)
	}
	if err := util.JSONUnmarshal([]byte(propertyValue), &deserializedProperty); err != nil {
		return deserializedProperty, fmt.Errorf("property '%s' ('%s') could not be parsed: %s", propertyName, propertyValue, err)
	}
	return deserializedProperty, nil
}
