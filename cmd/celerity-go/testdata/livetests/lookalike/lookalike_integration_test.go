//go:build integration

package lookalike_test

import (
	"testing"

	"example.com/livetests/lookalike"
)

func TestLiveNodesAreCounted(t *testing.T) {
	cluster := lookalike.Cluster{Nodes: []string{"a", "b"}}

	if cluster.Live() != 2 {
		t.Fatalf("got %d", cluster.Live())
	}
}
