package config

import "fmt"

// MissingStoreError reports a store a backend does not hold.
//
// Distinct from a store that is empty: this is a deployment that did not
// happen, and a handler reading a value it was promised should be told that
// rather than told the value is absent.
type MissingStoreError struct {
	StoreID string
}

func (e *MissingStoreError) Error() string {
	return fmt.Sprintf("celerity: no config store %q", e.StoreID)
}

// MissingKeyError reports a key the store does not hold.
type MissingKeyError struct {
	Namespace string
	Key       string
}

func (e *MissingKeyError) Error() string {
	if e.Namespace == "" {
		return fmt.Sprintf("celerity: no config value for %q", e.Key)
	}
	return fmt.Sprintf("celerity: no config value for %q in namespace %q", e.Key, e.Namespace)
}

// MissingNamespaceError reports a namespace nothing registered.
//
// Names what is registered, because the cause is nearly always a name that
// disagrees with the blueprint's and the answer is in the list.
type MissingNamespaceError struct {
	Name       string
	Registered []string
}

func (e *MissingNamespaceError) Error() string {
	if len(e.Registered) == 0 {
		return fmt.Sprintf(
			"celerity: no config namespace %q, and none is registered: "+
				"the application declares no celerity/config resource",
			e.Name,
		)
	}
	return fmt.Sprintf("celerity: no config namespace %q, registered: %v", e.Name, e.Registered)
}

// AmbiguousNamespaceError reports a value asked for without a namespace where
// more than one is registered.
type AmbiguousNamespaceError struct {
	Registered []string
}

func (e *AmbiguousNamespaceError) Error() string {
	return fmt.Sprintf(
		"celerity: %d config namespaces are registered, so a value has to name one: "+
			"use Namespace(name).Get, registered: %v",
		len(e.Registered), e.Registered,
	)
}
