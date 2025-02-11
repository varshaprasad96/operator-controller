package variable_sources_test

import (
	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/operator-framework/deppy/pkg/deppy/input"
	"github.com/operator-framework/operator-controller/internal/resolution/variable_sources"
	"github.com/operator-framework/operator-registry/alpha/property"
	"github.com/operator-framework/operator-registry/pkg/api"
)

var _ = Describe("Predicates", func() {
	Describe("WithPackageName", func() {
		It("should return true when the entity has the same package name", func() {
			entity := input.NewEntity("test", map[string]string{
				property.TypePackage: `{"packageName": "mypackage", "version": "1.0.0"}`,
			})
			Expect(variable_sources.WithPackageName("mypackage")(entity)).To(BeTrue())
			Expect(variable_sources.WithPackageName("notmypackage")(entity)).To(BeFalse())
		})
	})

	Describe("InSemverRange", func() {
		It("should return true when the entity has the has version in the right range", func() {
			entity := input.NewEntity("test", map[string]string{
				property.TypePackage: `{"packageName": "mypackage", "version": "1.0.0"}`,
			})
			inRange := semver.MustParseRange(">=1.0.0")
			notInRange := semver.MustParseRange(">=2.0.0")
			Expect(variable_sources.InSemverRange(inRange)(entity)).To(BeTrue())
			Expect(variable_sources.InSemverRange(notInRange)(entity)).To(BeFalse())
		})
	})

	Describe("InChannel", func() {
		It("should return true when the entity has the has version in the right range", func() {
			entity := input.NewEntity("test", map[string]string{
				property.TypeChannel: `{"channelName":"stable","priority":0}`,
			})
			Expect(variable_sources.InChannel("stable")(entity)).To(BeTrue())
			Expect(variable_sources.InChannel("unstable")(entity)).To(BeFalse())
		})
	})

	Describe("ProvidesGVK", func() {
		It("should return true when the entity provides the specified gvk", func() {
			entity := input.NewEntity("test", map[string]string{
				property.TypeGVK: `[{"group":"foo.io","kind":"Foo","version":"v1"},{"group":"bar.io","kind":"Bar","version":"v1"}]`,
			})
			Expect(variable_sources.ProvidesGVK(&api.GroupVersionKind{
				Group:   "foo.io",
				Version: "v1",
				Kind:    "Foo",
			})(entity)).To(BeTrue())
			Expect(variable_sources.ProvidesGVK(&api.GroupVersionKind{
				Group:   "baz.io",
				Version: "v1alpha1",
				Kind:    "Baz",
			})(entity)).To(BeFalse())
		})
	})
})
