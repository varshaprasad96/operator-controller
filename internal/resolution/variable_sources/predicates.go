package variable_sources

import (
	"github.com/blang/semver/v4"
	"github.com/operator-framework/operator-registry/pkg/api"

	"github.com/operator-framework/deppy/pkg/deppy/input"
)

func WithPackageName(packageName string) input.Predicate {
	return func(entity *input.Entity) bool {
		bundleEntity := NewBundleEntity(entity)
		name, err := bundleEntity.PackageName()
		if err != nil {
			return false
		}
		return name == packageName
	}
}

func InSemverRange(semverRange semver.Range) input.Predicate {
	return func(entity *input.Entity) bool {
		bundleEntity := NewBundleEntity(entity)
		bundleVersion, err := bundleEntity.Version()
		if err != nil {
			return false
		}
		return semverRange(*bundleVersion)
	}
}

func InChannel(channelName string) input.Predicate {
	return func(entity *input.Entity) bool {
		bundleEntity := NewBundleEntity(entity)
		bundleChannel, err := bundleEntity.ChannelName()
		if err != nil {
			return false
		}
		return channelName == bundleChannel
	}
}

func ProvidesGVK(gvk *api.GroupVersionKind) input.Predicate {
	return func(entity *input.Entity) bool {
		bundleEntity := NewBundleEntity(entity)
		providedGVKs, err := bundleEntity.ProvidedGVKs()
		if err != nil {
			return false
		}
		for i := 0; i < len(providedGVKs); i++ {
			providedGVK := &providedGVKs[i]
			if providedGVK.String() == gvk.String() {
				return true
			}
		}
		return false
	}
}
