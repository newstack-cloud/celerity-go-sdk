// Package tests holds the suite that runs against a real Celerity runtime
// container.
//
// The runtime launches the handlers executable in tests/testapp and talks to it
// over the IPC stream, so the application under test runs inside the container
// and this suite drives it over HTTP. What it covers is what a stand-in cannot:
// routing, blueprint parsing and handler tag construction belong to the
// runtime, and a stand-in that agreed with the SDK about them would prove
// nothing. The protocol itself is covered in internal/ipc.
//
// Every file here is behind a //go:build integration tag, so go test ./...
// stays fast and needs nothing installed:
//
//	bash scripts/run-tests.sh --with-integration
package tests
