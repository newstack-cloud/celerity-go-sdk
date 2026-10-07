package celeritytest

import (
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

// Stated so that a kind the contract gains and a double has not is a failure
// naming the type rather than one naming whichever call site first wanted it.
var (
	_ resources.Provider = (*Provider)(nil)
	_ resources.Closer   = (*Provider)(nil)
	_ bucket.Store       = (*Bucket)(nil)
	_ queue.Client       = (*Queue)(nil)
	_ topic.Client       = (*Topic)(nil)
	_ datastore.Client   = (*Datastore)(nil)
	_ cache.Client       = (*CacheStub)(nil)
	_ sqldb.Client       = refusingDatabase{}
)

// The recording tracer is a telemetry.Tracer, stated for the same reason.
var _ telemetry.Tracer = (*Tracer)(nil)
