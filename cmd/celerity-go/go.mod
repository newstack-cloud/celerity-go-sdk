module github.com/newstack-cloud/celerity-go-sdk/cmd/celerity-go

go 1.26.0

// Released in lock-step and resolved from the checkout. The directive is kept
// after release, since a dependency's replace is ignored by whoever requires it.
replace github.com/newstack-cloud/celerity-go-sdk => ../..

require (
	github.com/newstack-cloud/celerity-go-sdk v0.2.1
	github.com/stretchr/testify v1.12.1
)

require (
	golang.org/x/mod v0.29.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
)

require (
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/tools v0.38.0
)
