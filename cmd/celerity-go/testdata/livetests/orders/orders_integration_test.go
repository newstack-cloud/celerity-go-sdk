//go:build integration

package orders_test

import (
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/celeritytest"
)

// A fixture for the generator's tests rather than an example to copy: nothing
// here runs, and what it is for is to be a package that reaches live resources
// so the scan has something to find. A worked integration suite is in the Go
// SDK documentation.
//
// Written as a call rather than a bare reference to the function, because that
// is the shape a real suite has and so the shape the scan has to resolve.
func TestFixtureReachesLiveResources(t *testing.T) {
	res := celeritytest.Live(t)
	_ = res
}
