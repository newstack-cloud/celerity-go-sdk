# Celerity Go SDK

[![Go Reference](https://pkg.go.dev/badge/github.com/newstack-cloud/celerity-go-sdk.svg)](https://pkg.go.dev/github.com/newstack-cloud/celerity-go-sdk)

Write your handlers once, and run them in a containerised Celerity runtime or in
a provider's serverless environment without changing a line.

See [celerityframework.io](https://celerityframework.io) for the framework
documentation.

## Installing

```bash
go get github.com/newstack-cloud/celerity-go-sdk
```

The platform modules, `serverless/aws`, `resources/aws` and `config/aws`, plus
`resources/redis` where a blueprint declares a cache, are
added by the build for the blueprint's deploy target rather than by hand.

## An application

```go
package main

import (
    "context"

    "github.com/newstack-cloud/celerity-go-sdk/celerity"
    "github.com/newstack-cloud/celerity-go-sdk/resources"
    "github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

type CreateOrder struct {
    CustomerID string  `json:"customerId"`
    Total      float64 `json:"total"`
}

type Order struct {
    ID     string  `json:"id"`
    Status string  `json:"status"`
    Total  float64 `json:"total"`
}

func main() {
    app := celerity.New()

    orders := resources.Datastore(app, "ordersTable")

    celerity.Post(app, "/orders", createOrder(orders))
    celerity.Get(app, "/orders/{orderId}", getOrder(orders))

    celerity.Run(app)
}

func createOrder(store datastore.Client) celerity.HandlerFunc[CreateOrder, Order] {
    return func(ctx context.Context, req CreateOrder) (Order, error) {
        order := Order{ID: newID(), Status: "pending", Total: req.Total}
        _, err := store.Put(ctx, datastore.Key{Partition: order.ID}, order)
        return order, err
    }
}
```

Path and query parameters bind through struct tags, so a handler signature
carries only what the handler is about:

```go
type GetOrder struct {
    OrderID string `path:"orderId"`
    Expand  bool   `query:"expand"`
}
```

## Handler kinds

```go
celerity.Get(app, "/orders/{orderId}", getOrder)         // HTTP
celerity.OnMessage(app, "sendMessage", chat.Send)         // WebSocket
celerity.Consume(app, "orderEvents", orders.Process)      // queues and streams
celerity.Schedule(app, "nightlyReport", reports.Nightly)  // scheduled rules
celerity.Invoke(app, "recalculatePricing", pricing.Recalculate)
```

Layers are Go's usual middleware shape, so anything already written that way
composes:

```go
celerity.Post(app, "/orders", createOrder,
    celerity.With(ratelimit.Layer(100)),
    celerity.ProtectedBy("jwt"),
)
```

## Configuration

The stores a blueprint's `celerity/config` resources were deployed as are read
from whatever the platform holds them in, and an application does minimal work to
wire that up:

```go
func main() {
    app := celerity.New()
    cfg := app.Config()

    celerity.Get(app, "/orders/{orderId}", getOrder(cfg))

    celerity.Run(app)
}

func getOrder(cfg *config.Service) celerity.HandlerFunc[GetOrder, Order] {
    return func(ctx context.Context, req GetOrder) (Order, error) {
        region, err := cfg.Get(ctx, "REGION")
        ...
    }
}
```

The service is given to the handler rather than reached through its context. It
exists before any event does and is the same object for every one of them, so
it is a dependency rather than anything about a request, and the resource graph
the Celerity CLI recovers from the source is built out of the arguments a
handler is constructed with.

Values bind into a struct, where a tagged struct is a prefix rather than a
value, so configuration is grouped the way the application thinks of it:

```go
type Settings struct {
    Name     string `config:"NAME"`
    Database struct {
        Host    string        `config:"HOST"`
        Timeout time.Duration `config:"TIMEOUT"`
    } `config:"DATABASE"`
}

var settings Settings
err := cfg.Bind(ctx, &settings) // NAME, DATABASE_HOST, DATABASE_TIMEOUT
```

Each level is punctuated with an underscore or a slash, whichever the store was
written with, so a parameter store holding a hierarchy as a path and a secret
holding compound keys read into the same struct.

## Resources

A handler reaches infrastructure through provider-agnostic interfaces, by the
name the blueprint gave the resource:

```go
orders := resources.Datastore(app, "ordersTable")
uploads := resources.Bucket(app, "uploadsBucket")
```

Buckets, queues, topics, document stores, caches and SQL databases each have an
interface in `resources`, and a provider module implements them: `resources/aws`
against S3, SQS, SNS, DynamoDB, ElastiCache and RDS.

What the resource is actually called is decided when it is created, so a handle
carries the blueprint name and resolves on first use, against the topology the
Celerity CLI writes into the bundle and the identifiers the deploy engine
records. Nothing is read while handles are being taken, so registration costs no
requests and a resource an application declares but never reaches costs nothing
at all.

Handles are also what the extraction pass reads to work out which resources each
handler reaches, and so which IAM grants it needs, which is why the name is
expected to be a compile-time constant.

Every kind follows the same shape: a handle taken by blueprint name, operations
on the provider-agnostic interface, and errors that mean the same thing whatever
the store underneath. An absence is that interface's `ErrNotFound`:

```go
found, err := orders.Get(ctx, datastore.Key{Partition: id}, &order)
if errors.Is(err, datastore.ErrNotFound) { ... }
```

A read that can return more than fits in one answer returns a page and an opaque
cursor, which is what a handler hands its caller as the token for the next one:

```go
cursor, err := orders.Query(ctx, datastore.Query{
    Partition: customerID,
    Limit:     50,
    Cursor:    req.Cursor,
}, &page)
```

To read one through instead, range over it. Pages are fetched as they are
needed, so breaking out early stops the fetching:

```go
for order, err := range datastore.Items[Order](ctx, orders, query) {
    if err != nil {
        return err
    }
    total += order.Total
}
```

`bucket.Objects` does the same for a bucket.

Each interface carries what its own kind needs beyond that: sort conditions and
conditional writes on a data store, ranged reads and versions on a bucket,
partial-failure results on a queue or a topic, separate writer and reader pools
on a SQL database. The [framework documentation](https://celerityframework.io)
covers each in full, including which of them cost an extra request on which
provider.

One of them is a build concern rather than a handler one. `database/sql` takes
its driver from whatever the program imported, and the SDK imports none, so
`celerity-go generate` links one per engine the blueprint declares:

```bash
celerity-go generate --target aws-serverless --sql-engine postgres
```

An application with no database carries no driver, and changing engine is a
blueprint edit rather than a source edit.

## Testing

`celeritytest` runs handlers through the application's own pipeline, so the
layers, guards and context a deployment applies are the ones a test exercises.
No runtime, no network, nothing to install.

The application is built by its own constructor rather than reassembled by the
test: `orders.App(opts ...celerity.Option)` is the application, and a test
passes one option. What the application depends on, a payment gateway or a
clock, arrives through that constructor as the application's own argument; what
Celerity provides is substituted with `celerity.WithResourceProvider`.

```go
func TestCreatingAnOrder(t *testing.T) {
	res := celeritytest.Resources()
	app := orders.App(celerity.WithResourceProvider(res))
	harness := celeritytest.New(t, app)

	harness.POST(t, "/orders", celeritytest.JSONBody(order{ID: "o-1", Total: 10})).
		AssertStatus(t, http.StatusCreated)

	// The double is a working one, so what the handler wrote is there to read.
	sent := res.QueueNamed("workQueue").Sent()
	require.Equal(t, "o-1", sent[0].Text())
}
```

A bucket, queue, topic and data store are held in memory. A write
followed by a read answers with what was written, a query filters, a batch
reports its three lists. A cache is a recording stub, since reimplementing a
subset of Redis in-memory for a surface the size of the cache adds complexity with little benefit, so a recording stub suffices.

A SQL database has no double at all. What a handler has to be right about is the
statement it sends, and only an engine can judge that, so an unsupplied database
refuses the call and names the way to test it:
`celeritytest.Resources().WithDatabase("ordersDb", db)` hands over the engine a
`celerity dev` session runs, or whatever the suite starts for itself.

`Consume`, `ConsumeJSON`, `Schedule`, `Invoke` and `Send` dispatch the other
handler kinds.

`celeritytest.NewTracer(t)` records the spans a dispatch produced, for asserting
telemetry the application wrote: a span a handler opened with
`telemetry.Traced`, the attributes it set, the error it recorded.

```go
span := tracer.AssertSpan(t, "orders.price_quote")
total, _ := span.Attr("order.total")
```

Asserting the SDK's own span names is not what it is for: those are the SDK's
behaviour, not the handler's. The seam is process-wide, so a test installing a
recorder cannot call `t.Parallel`, and a second install fails rather than
quietly collecting both tests' spans.

### Integration tests

`celeritytest.Live(t)` serves resources from what a `celerity dev test` session
brought up instead of from doubles, reached through the provider the build
linked, so a handler runs against a real engine while still being dispatched in
process through the application's own pipeline. The test links the providers a
session links:

```go
func TestCreatingAnOrder(t *testing.T) {
	res := celeritytest.Live(t)
	work := res.QueueNamed("workQueue") // this one stays a double
	harness := celeritytest.New(t, orders.App(celerity.WithResourceProvider(res)))

	harness.POST(t, "/orders", celeritytest.JSONBody(order{ID: "o-1"})).
		AssertStatus(t, http.StatusCreated)

	require.Equal(t, "o-1", work.Sent()[0].Text()) // and the store was real
}
```

The two mix per resource: asking for a double by name substitutes it for that
one, and doing so after the application took its handles fails rather than
handing back a double nothing will reach.

A test binary is a different binary from the one `generate` writes the main file
for, so it has to link the providers too. `generate --test-packages` writes them,
into the test packages that call `celeritytest.Live` and no others:

```
celerity-go generate --target aws-serverless --local --test-packages
```

Which packages those are is resolved by the type checker rather than matched in
the text, the file it writes carries the suite's own build tag so an ordinary
`go test` does not link any of it, and the imports come from the same generator as the
main file, so the two cannot disagree about what the application links.
`--test-tags` names the tags the suites are behind, and defaults to
`integration`.

## Telemetry

Logging is `log/slog`, so any handler an application already has works. Without
one the SDK writes JSON, and one readable line per record in a development
session:

| Variable | Values |
|---|---|
| `CELERITY_LOG_LEVEL` | `debug`, `info`, `warn`, `error` |
| `CELERITY_LOG_FORMAT` | `json`, `human`, `auto` (the default: human in a session, JSON elsewhere) |

A dispatch opens a span, and every resource operation opens one under it. Where
a trace is being recorded, `trace_id` and `span_id` are bound into the handler's
logger, so a record written with `telemetry.LoggerFrom(ctx).Info(...)` leads
back to the trace.

```
celerity.handler.http          handler.name=createOrder http.route=/orders
  ├─ celerity.datastore.put_item   datastore.partition=o-1
  │    └─ DynamoDB.PutItem         aws.request_id=... attempt=1
  └─ celerity.queue.send_message   queue.resource=workQueue
       └─ SQS.SendMessage          http.response.status_code=200
```

The span names match the other SDKs, so one trace reads the same
whichever language served the request. Exporting them needs
`generate --telemetry otel`; an application that links nothing still produces
them, to a tracer that doesn't record anything.

What that module exports to is the deployment's to configure, with the same
variables and defaults other SDKs also read. OpenTelemetry's own names
win, so an operator who knows OpenTelemetry configures this the way they would
anything else:

| Variable | Default |
|---|---|
| `CELERITY_TELEMETRY_ENABLED` | `false`, and nothing is exported until it is `true` |
| `OTEL_EXPORTER_OTLP_ENDPOINT`, else `CELERITY_TRACE_OTLP_COLLECTOR_ENDPOINT` | `http://otelcollector:4317` |
| `OTEL_SERVICE_NAME` | `celerity-app` |
| `OTEL_SERVICE_VERSION` | `0.0.0` |

W3C trace context is propagated always and X-Ray's alongside it where the
platform is AWS, which also decides the trace id format, since X-Ray refuses one
it did not generate the shape of.

An application that wants something else, a sampler of its own or an exporter
the module does not build, installs its provider with the OpenTelemetry SDK and
takes precedence, the tracer resolves the provider per call, and an init runs before main
does. Pair it with `telemetry.SetFlusher` so a shutdown hands over what it
holds.

AWS service calls are traced by the SDK itself, with no extra dependency. A
cache and a database are traced by the modules above, which carry the
instrumentation each backend needs.

## Where it runs

One binary serves every target. `celerity.Run` picks from the environment,
asking each linked adapter whether it recognises its own platform:

| Environment | Mode |
|---|---|
| `CELERITY_RUNTIME_SOCKET` set | Connects to the Celerity runtime over the IPC stream |
| A linked adapter recognises the platform | Hands off to that adapter's event loop |
| `CELERITY_EXTRACT_MANIFEST` set | Prints the handler manifest and exits |

`celerity-go extract` drives that last one. It builds the application and runs
it, which is what makes the manifest describe what the binary will actually
serve, and then reads the source to resolve which resources each handler
reaches: that handle is captured in a closure or held on a receiver, and Go
offers no reflection into either. A resource named by something other than a
constant fails extraction rather than being dropped, since the answer becomes
IAM grants and a missing one is a permission nobody asked about.
`celerity.Uses` declares what the walk cannot see.

## Modules

| Module | Import path | Linked when |
|---|---|---|
| Core | `github.com/newstack-cloud/celerity-go-sdk` | Always |
| AWS Lambda adapter | `.../serverless/aws` | Target is `aws-serverless` |
| AWS resources | `.../resources/aws` | Target is `aws` or `aws-serverless`. A package per resource kind, and a build links the ones the blueprint declares |
| Redis cache | `.../resources/redis` | The blueprint declares a cache, whatever the target |
| AWS configuration | `.../config/aws` | Target is `aws` or `aws-serverless` |
| Local configuration | `.../config/local` | Building for `celerity dev`, not for a deployment |
| Local resources | `.../resources/local` | Building for `celerity dev`. Serves a queue and a topic and hands the rest to the target's own provider |
| OTel tracing | `.../telemetry/otel` | `generate --telemetry otel` |
| Redis tracing | `.../resources/redis/otel` | `--telemetry otel` and the blueprint declares a cache |
| SQL tracing | `.../resources/sqldb/otel` | `--telemetry otel` and the blueprint declares a database |
| Build tool | `.../cmd/celerity-go` | Invoked by the Celerity CLI |

The split is by dependency weight, and a target's binary carries that target's
modules rather than every platform's, however many the SDK comes to support.
Within `resources/aws` the same reasoning goes one level down: each resource kind
is its own package, so an application with a data store and nothing else does not include an S3, SQS or SNS client.

A cache is the exception to the per-platform arrangement. Every managed cache
speaks Redis, so `resources/redis` is the whole implementation for every target,
and a platform module contributes only how a password is obtained.

A development session builds its own artefact to mount into the runtime
container, so `celerity-go generate --local` adds what that session reads and a
deployed function carries none of it. Which provider serves is decided at
startup from the platform either way, so an application does nothing.

## Documentation

This README is an outline. The guides live in the Go SDK section of the
framework documentation, which is where to look for anything in depth.

- [Framework documentation](https://celerityframework.io)
- [Contributing](./CONTRIBUTING.md)
- [Dependency updates](./docs/RENOVATE.md)

## Licence

Apache 2.0. See [LICENSE](./LICENSE).
