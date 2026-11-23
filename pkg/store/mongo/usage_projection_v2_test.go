package mongo

import (
	"github.com/arketov/iterion/pkg/store"
	"github.com/arketov/iterion/pkg/store/storetest"
	"testing"
)

func TestUsageV2ProjectionMongo(t *testing.T) {
	storetest.RunUsageProjectionV2(t, func(t *testing.T) store.RunStore { return correctionMongoStore(t) })
}
func TestUsageV2ProjectionMongoUpgrade(t *testing.T) {
	storetest.RunUsageProjectionV2Upgrade(t, func(t *testing.T) store.RunStore { return correctionMongoStore(t) })
}

func TestReportedSessionMongo(t *testing.T) {
	storetest.RunReportedSession(t, func(t *testing.T) store.RunStore { return correctionMongoStore(t) })
}
