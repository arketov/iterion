package mongo

import (
	"github.com/arketov/iterion/pkg/store"
	"github.com/arketov/iterion/pkg/store/storetest"
	"testing"
)

func TestUsageOriginMongo(t *testing.T) {
	storetest.RunUsageOriginConformance(t, func(t *testing.T) store.RunStore { return correctionMongoStore(t) })
}
