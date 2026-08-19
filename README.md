# Celerity Go SDK

Write your handlers once, and run them in a containerised Celerity runtime or in
a provider's serverless environment without changing a line.

See [celerityframework.io](https://celerityframework.io) for the framework
documentation.

> **Status: early.** The protocol client, the registration API and the module
> layout are in place. The AWS adapter, the resource implementations and
> handler extraction are scaffolded and not yet implemented.

## Installing

```bash
go get github.com/newstack-cloud/celerity-go-sdk
```

The platform modules, `serverless/aws` and `resources/aws`, are added by the
build for the blueprint's deploy target rather than by hand.

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
| Build tool | `.../cmd/celerity-go` | Invoked by the Celerity CLI |

The split is by dependency weight, and only the deploy target's modules are
linked: a Lambda binary carries the AWS SDK and nothing else, however many
platforms the SDK comes to support.

## Documentation

- [Framework documentation](https://celerityframework.io)
- [Contributing](./CONTRIBUTING.md)
- [Dependency updates](./docs/RENOVATE.md)

## Licence

Apache 2.0. See [LICENSE](./LICENSE).
