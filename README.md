# Celerity Go SDK

Write your handlers once, and run them in a containerised Celerity runtime or in
a provider's serverless environment without changing a line.

See [celerityframework.io](https://celerityframework.io) for the framework
documentation.

> **Status: early.** The protocol client, the registration API, the module
> layout, configuration and the AWS Lambda adapter are in place. The resource
> implementations and handler extraction are scaffolded and not yet
> implemented.

## Installing

```bash
go get github.com/newstack-cloud/celerity-go-sdk
```

The platform modules, `serverless/aws`, `resources/aws` and `config/aws`, are
added by the build for the blueprint's deploy target rather than by hand.

## An application

```go
package main

import (
    "context"

    "github.com/newstack-cloud/celerity-go-sdk/celerity"
    "github.com/newstack-cloud/celerity-go-sdk/resources"
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

func createOrder(store resources.DatastoreClient) celerity.HandlerFunc[CreateOrder, Order] {
    return func(ctx context.Context, req CreateOrder) (Order, error) {
        order := Order{ID: newID(), Status: "pending", Total: req.Total}
        return order, store.Put(ctx, resources.Key{Partition: order.ID}, order)
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

## Where it runs

One binary serves every target. `celerity.Run` picks from the environment,
asking each linked adapter whether it recognises its own platform:

| Environment | Mode |
|---|---|
| `CELERITY_RUNTIME_SOCKET` set | Connects to the Celerity runtime over the IPC stream |
| A linked adapter recognises the platform | Hands off to that adapter's event loop |
| `CELERITY_EXTRACT_MANIFEST` set | Prints the handler manifest and exits |

## Modules

| Module | Import path | Linked when |
|---|---|---|
| Core | `github.com/newstack-cloud/celerity-go-sdk` | Always |
| AWS Lambda adapter | `.../serverless/aws` | Target is `aws-serverless` |
| AWS resources | `.../resources/aws` | Target is `aws` or `aws-serverless` |
| AWS configuration | `.../config/aws` | Target is `aws` or `aws-serverless` |
| Local configuration | `.../config/local` | Building for `celerity dev`, not for a deployment |
| Build tool | `.../cmd/celerity-go` | Invoked by the Celerity CLI |

The split is by dependency weight, and a target's binary carries that target's
modules rather than every platform's, however many the SDK comes to support.

A development session builds its own artefact to mount into the runtime
container, so `celerity-go generate --local` adds what that session reads and a
deployed function carries none of it. Which provider serves is decided at
startup from the platform either way, so an application does nothing.

## Documentation

- [Framework documentation](https://celerityframework.io)
- [Contributing](./CONTRIBUTING.md)
- [Dependency updates](./docs/RENOVATE.md)

## Licence

Apache 2.0. See [LICENSE](./LICENSE).
