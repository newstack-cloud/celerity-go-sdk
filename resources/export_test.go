package resources

// ResetProviders clears what has registered.
//
// There is no unregistering in production, an init registers, and an
// application links what it links. A test has to be able to establish what
// happens with none linked, with one and with several, and those are three
// states of one process-wide list.
func ResetProviders() {
	providerMu.Lock()
	defer providerMu.Unlock()
	providers = nil
}
