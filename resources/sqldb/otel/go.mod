module github.com/newstack-cloud/celerity-go-sdk/resources/sqldb/otel

go 1.26.0

// Released in lock-step and resolved from the checkout. The directive is kept
// after release, since a dependency's replace is ignored by whoever requires it.
replace github.com/newstack-cloud/celerity-go-sdk => ../../..

require (
	github.com/XSAM/otelsql v0.42.0
	github.com/newstack-cloud/celerity-go-sdk v0.1.0
	go.opentelemetry.io/otel v1.42.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/metric v1.42.0 // indirect
	go.opentelemetry.io/otel/trace v1.42.0 // indirect
)
