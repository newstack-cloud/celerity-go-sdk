package celeritytest

// NotDetectedMessage says why nothing serves live resources.
//
// Two different mistakes answer the same way from the registry, and they are
// fixed differently: a build that linked no provider has imports to add, and one
// that linked several has an environment to set, since which of them serves is
// the platform's answer rather than the import order's.
func NotDetectedMessage(linked []string) string {
	return notDetected(linked)
}
