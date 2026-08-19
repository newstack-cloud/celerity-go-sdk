//go:build integration

// Integration tests against a real Celerity runtime.
//
// The runtime launches the handlers executable in tests/testapp and talks to it
// over the IPC stream, so these tests drive the runtime over HTTP and assert on
// what the Go handlers answered. That covers the part a stand-in cannot:
// routing, blueprint parsing and handler tag construction belong to the
// runtime.
//
// Run with:
//
//	bash scripts/run-tests.sh --with-runtime
package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func runtimeURL() string {
	if url := os.Getenv("CELERITY_TEST_RUNTIME_URL"); url != "" {
		return url
	}
	return "http://127.0.0.1:8080"
}

// get sends a request and decodes the answer, failing the test on anything
// other than the status it was told to expect.
func get(t *testing.T, path string, wantStatus int, into any) {
	t.Helper()

	res, err := http.Get(runtimeURL() + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != wantStatus {
		t.Fatalf("GET %s: status = %d, want %d, body: %s", path, res.StatusCode, wantStatus, body)
	}
	if into != nil {
		if err := json.Unmarshal(body, into); err != nil {
			t.Fatalf("GET %s: decoding %s: %v", path, body, err)
		}
	}
}

// TestMain waits for the runtime to attach its handlers executable, since the
// container starts in parallel with this binary.
func TestMain(m *testing.M) {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		res, err := http.Get(runtimeURL() + "/orders/readiness-probe")
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				os.Exit(m.Run())
			}
		}
		time.Sleep(250 * time.Millisecond)
	}

	// A handshake that never completes surfaces here rather than as every test
	// timing out on its own.
	println("the runtime never attached its handlers executable; check its logs")
	os.Exit(1)
}

type order struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type orderCreated struct {
	ID         string `json:"id"`
	CustomerID string `json:"customerId"`
	Status     string `json:"status"`
}

type filePath struct {
	Path     string   `json:"path"`
	Segments []string `json:"segments"`
}

func TestPathParameterReachesAGoHandler(t *testing.T) {
	var got order
	get(t, "/orders/order-123", http.StatusOK, &got)

	// The parameter is the runtime's routing and the binding is the SDK's, so
	// this is where the two are checked against each other.
	if got.ID != "order-123" || got.Status != "found" {
		t.Errorf("response = %+v, want the order id bound from the path", got)
	}
}

func TestRequestBodyReachesAGoHandler(t *testing.T) {
	res, err := http.Post(
		runtimeURL()+"/orders",
		"application/json",
		strings.NewReader(`{"customerId":"cust-1"}`),
	)
	if err != nil {
		t.Fatalf("POST /orders: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status = %d, want 200, body: %s", res.StatusCode, body)
	}

	var got orderCreated
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decoding the response: %v", err)
	}
	if got.ID != "new-cust-1" || got.CustomerID != "cust-1" {
		t.Errorf("response = %+v, want the body decoded into the handler's type", got)
	}
}

func TestCatchAllYieldsOneValuePerSegment(t *testing.T) {
	var got filePath
	get(t, "/files/reports/2026/q1.pdf", http.StatusOK, &got)

	// The bug this pins down: binding the first value alone truncated the path
	// to its first segment, and only a real runtime produces the multi-value
	// parameter that exposes it.
	if len(got.Segments) != 3 {
		t.Errorf("segments = %v, want one per path segment", got.Segments)
	}
	if got.Path != "reports/2026/q1.pdf" {
		t.Errorf("path = %q, want the whole remaining path", got.Path)
	}
}

func TestCatchAllKeepsAnEncodedSeparatorInASegment(t *testing.T) {
	var got filePath
	get(t, "/files/reports/q1%2Ffinal.pdf", http.StatusOK, &got)

	// Segments are split from the raw path before being percent-decoded, so an
	// encoded separator stays inside one segment rather than creating another.
	if len(got.Segments) != 2 || got.Segments[1] != "q1/final.pdf" {
		t.Errorf("segments = %v, want the encoded separator kept in one segment", got.Segments)
	}
}

func TestSingleSegmentCatchAll(t *testing.T) {
	var got filePath
	get(t, "/files/readme.md", http.StatusOK, &got)

	if got.Path != "readme.md" || len(got.Segments) != 1 {
		t.Errorf("response = %+v, want a single segment", got)
	}
}

func TestUnroutedPathIsANotFound(t *testing.T) {
	get(t, "/nothing-serves-this", http.StatusNotFound, nil)
}
