package variable_sources_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/operator-framework/deppy/pkg/deppy/input"
	"github.com/operator-framework/operator-controller/internal/resolution/variable_sources"
	"github.com/operator-framework/operator-registry/alpha/property"
)

const (
	TypeBundlePath = "olm.bundle.path"
)

var _ = Describe("BundleVariable", func() {
	var (
		bundleEntity *variable_sources.BundleEntity
	)

	BeforeEach(func() {
		bundleEntity = variable_sources.NewBundleEntity(input.NewEntity("bundle-1", map[string]string{
			TypeBundlePath:       "quay.io/cat/bar:latest",
			property.TypePackage: `{"packageName": "test-package", "version": "1.0.0"}`,
			property.TypeChannel: `{"channelName":"stable","priority":0}`,
		}))
	})

	It("should return the correct bundle path", func() {
		Expect(bundleEntity.BundlePath()).To(Equal("quay.io/cat/bar:latest"))
	})
})
