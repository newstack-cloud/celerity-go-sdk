module github.com/newstack-cloud/celerity-go-sdk/config/local

go 1.26.0

require (
	github.com/newstack-cloud/celerity-go-sdk v0.0.0
	github.com/redis/go-redis/v9 v9.22.0
	github.com/stretchr/testify v1.12.1
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

// Core is not published yet, so the module is resolved from the repository.
// Dropped when core cuts its first release, after which this module requires a
// version like any other consumer does.
replace github.com/newstack-cloud/celerity-go-sdk => ../..
