package mongo

import (
	"github.com/arketov/iterion/pkg/store"
	"github.com/arketov/iterion/pkg/store/storetest"
	"testing"
)

func TestPrototypeObservationMongo(t *testing.T) {
	storetest.RunUsageObservationPrototype(t, func(t *testing.T) store.RunStore { return correctionMongoStore(t) })
}
func TestPrototypeObservationMongoPricing(t *testing.T) {
	storetest.RunUsageObservationPricingPrototype(t, func(t *testing.T) store.RunStore { return correctionMongoStore(t) })
}

func TestPrototypeObservationIteration(t *testing.T) {
	storetest.RunUsageIterationPrototype(t, func(t *testing.T) store.RunStore { return correctionMongoStore(t) })
}
