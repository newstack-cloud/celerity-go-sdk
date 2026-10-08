// Package otel traces the commands a Celerity application sends to its cache.
//
// It is selected by importing it, which `celerity-go generate --telemetry otel`
// writes alongside the tracer itself:
//
//	import _ "github.com/newstack-cloud/celerity-go-sdk/resources/redis/otel"
//
// # What it adds
//
// The resources package already produces a span per operation, naming what the
// application asked for: celerity.cache.get, celerity.cache.sorted_set_add.
// This adds the span underneath, naming the command actually sent and the node
// it went to, which on a cluster is what shows a command crossing to a shard
// nobody expected it to.
//
// # A module of its own
//
// Every platform links the cache, so an instrumentation dependency carried by
// the cache module would be carried by every application. So the cache offers a
// seam and this fills it, from an init.
package otel

import (
	"github.com/redis/go-redis/extra/redisotel/v9"
	goredis "github.com/redis/go-redis/v9"

	celerityredis "github.com/newstack-cloud/celerity-go-sdk/resources/redis"
)

func init() {
	celerityredis.Instrument(Instrument)
}

// Instrument traces the commands sent through a client.
//
// This is for tracing only, metrics are a separate hook rather than a flag here, since a
// deployment exporting traces and not metrics is the typical case and the
// cache's seam takes more than one hook.
//
// Exported for an application wiring its own order of initialisation, or a
// test. Importing the package is the ordinary way.
func Instrument(client goredis.UniversalClient) error {
	return redisotel.InstrumentTracing(client)
}
