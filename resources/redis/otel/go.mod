module github.com/newstack-cloud/celerity-go-sdk/resources/redis/otel

go 1.26.0

// Released in lock-step and resolved from the checkout. The directive is kept
// after release, since a dependency's replace is ignored by whoever requires it.
replace github.com/newstack-cloud/celerity-go-sdk => ../../..

replace github.com/newstack-cloud/celerity-go-sdk/resources/redis => ..

require (
	github.com/newstack-cloud/celerity-go-sdk/resources/redis v0.1.0
	github.com/redis/go-redis/extra/redisotel/v9 v9.23.0
	github.com/redis/go-redis/v9 v9.23.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/newstack-cloud/celerity-go-sdk v0.1.0 // indirect
	github.com/redis/go-redis/extra/rediscmd/v9 v9.23.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/otel/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	go.uber.org/atomic v1.12.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
)
