module github.com/newstack-cloud/celerity-go-sdk/resources/sqldb/otel

go 1.26.0

// Core is not published yet, so the module is resolved from the repository.
// Dropped when core cuts its first release.
replace github.com/newstack-cloud/celerity-go-sdk => ../../..

require (
	github.com/XSAM/otelsql v0.42.0
	github.com/newstack-cloud/celerity-go-sdk v0.0.0
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
