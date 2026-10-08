// Package lookalike has a Live of its own.
//
// Which is the ordinary way the scan could go wrong: it looks for a call named
// Live, and plenty of code has one that means something else.
package lookalike

// Cluster is the application's own, and nothing to do with the SDK.
type Cluster struct {
	Nodes []string
}

// Live reports how many of the cluster's nodes are answering.
func (c Cluster) Live() int {
	return len(c.Nodes)
}
