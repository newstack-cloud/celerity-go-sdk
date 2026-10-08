module github.com/newstack-cloud/celerity-go-sdk/config/local

go 1.26.0

require (
	github.com/newstack-cloud/celerity-go-sdk v0.1.0
	github.com/redis/go-redis/v9 v9.22.0
	github.com/stretchr/testify v1.12.1
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

// Released in lock-step and resolved from the checkout. The directive is kept
// after release, since a dependency's replace is ignored by whoever requires it.
replace github.com/newstack-cloud/celerity-go-sdk => ../..
