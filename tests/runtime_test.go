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
//	bash scripts/run-tests.sh --with-integration
package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

func runtimeURL() string {
	if url := os.Getenv("CELERITY_TEST_RUNTIME_URL"); url != "" {
		return url
	}
	return "http://127.0.0.1:8080"
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

// HTTP routed by the real runtime and handled by Go.
type RuntimeHTTPTestSuite struct {
	suite.Suite
}

func TestRuntimeHTTPTestSuite(t *testing.T) {
	suite.Run(t, new(RuntimeHTTPTestSuite))
}

// get sends a request and decodes the answer, failing on anything other than
// the status it was told to expect.
func (s *RuntimeHTTPTestSuite) get(path string, wantStatus int, into any) {
	res, err := http.Get(runtimeURL() + path)
	s.Require().NoError(err, "GET %s", path)
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	s.Require().NoError(err)
	s.Require().Equal(wantStatus, res.StatusCode, "GET %s answered %s", path, body)

	if into != nil {
		s.Require().NoError(json.Unmarshal(body, into), "decoding %s", body)
	}
}

func (s *RuntimeHTTPTestSuite) Test_a_path_parameter_reaches_a_go_handler() {
	var got order
	s.get("/orders/order-123", http.StatusOK, &got)

	// The parameter is the runtime's routing and the binding is the SDK's, so
	// this is where the two are checked against each other.
	s.Equal(order{ID: "order-123", Status: "found"}, got)
}

func (s *RuntimeHTTPTestSuite) Test_a_request_body_reaches_a_go_handler() {
	res, err := http.Post(
		runtimeURL()+"/orders",
		"application/json",
		strings.NewReader(`{"customerId":"cust-1","total":12.5}`),
	)
	s.Require().NoError(err, "POST /orders")
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	s.Require().NoError(err)
	s.Require().Equal(http.StatusOK, res.StatusCode, "answered %s", body)

	var got orderCreated
	s.Require().NoError(json.Unmarshal(body, &got))
	s.Equal("new-cust-1", got.ID, "the body should decode into the handler's type")
	s.Equal("cust-1", got.CustomerID)
}

func (s *RuntimeHTTPTestSuite) Test_a_catch_all_yields_one_value_per_segment() {
	var got filePath
	s.get("/files/reports/2026/q1.pdf", http.StatusOK, &got)

	// The bug this pins down: binding the first value alone truncated the path
	// to its first segment, and only a real runtime produces the multi-value
	// parameter that exposes it.
	s.Equal([]string{"reports", "2026", "q1.pdf"}, got.Segments)
	s.Equal("reports/2026/q1.pdf", got.Path)
}

func (s *RuntimeHTTPTestSuite) Test_a_catch_all_keeps_an_encoded_separator_in_a_segment() {
	var got filePath
	s.get("/files/reports/q1%2Ffinal.pdf", http.StatusOK, &got)

	// Segments are split from the raw path before being percent-decoded, so an
	// encoded separator stays inside one segment rather than creating another.
	s.Equal([]string{"reports", "q1/final.pdf"}, got.Segments)
}

func (s *RuntimeHTTPTestSuite) Test_a_single_segment_catch_all() {
	var got filePath
	s.get("/files/readme.md", http.StatusOK, &got)

	s.Equal("readme.md", got.Path)
	s.Equal([]string{"readme.md"}, got.Segments)
}

func (s *RuntimeHTTPTestSuite) Test_an_unrouted_path_is_a_not_found() {
	s.get("/nothing-serves-this", http.StatusNotFound, nil)
}

func (s *RuntimeHTTPTestSuite) Test_a_status_carrying_error_survives_the_runtime() {
	// The pipeline turns a status-carrying error into a response; this checks
	// the runtime passes that status through rather than replacing it with the
	// 500 an unhandled error would produce.
	var answer struct{ Message string }
	s.get("/orders/unknown/receipt", http.StatusNotFound, &answer)

	s.Equal("no receipt for that order", answer.Message,
		"the handler's own message should reach the caller")
}

func (s *RuntimeHTTPTestSuite) Test_a_successful_answer_on_the_same_route_still_works() {
	var got order
	s.get("/orders/known/receipt", http.StatusOK, &got)

	s.Equal("receipted", got.Status)
}

// post sends a body and returns the status and decoded answer.
func (s *RuntimeHTTPTestSuite) post(path, body string, into any) int {
	res, err := http.Post(runtimeURL()+path, "application/json", strings.NewReader(body))
	s.Require().NoError(err, "POST %s", path)
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	s.Require().NoError(err)
	if into != nil {
		s.Require().NoError(json.Unmarshal(raw, into), "decoding %s", raw)
	}
	return res.StatusCode
}

// The body shape every Celerity SDK answers a refusal with.
type validationAnswer struct {
	Message string `json:"message"`
	Details []struct {
		Code    string   `json:"code"`
		Path    []string `json:"path"`
		Message string   `json:"message"`
	} `json:"details"`
}

// The test app validates with go-playground/validator, the standard choice for
// Celerity Go applications. This checks the issues it produces survive the
// runtime in the shape the other SDKs answer with.
func (s *RuntimeHTTPTestSuite) Test_validation_issues_survive_the_runtime() {
	var got validationAnswer
	status := s.post("/orders", `{"total":0}`, &got)

	s.Require().Equal(http.StatusBadRequest, status)
	s.Equal("the request is not valid", got.Message)
	s.Require().Len(got.Details, 2, "both broken rules should be reported, not just the first")

	byPath := map[string]string{}
	codes := map[string]string{}
	for _, d := range got.Details {
		s.Require().Len(d.Path, 1)
		byPath[d.Path[0]] = d.Message
		codes[d.Path[0]] = d.Code
	}

	// Paths are the names the caller sent, not the Go field names.
	s.Equal("is required", byPath["customerId"])
	s.Equal("required", codes["customerId"])
	s.Equal("must be greater than 0", byPath["total"])
	s.Equal("gt", codes["total"])
}

func (s *RuntimeHTTPTestSuite) Test_a_validation_answer_names_no_go_identifiers() {
	res, err := http.Post(
		runtimeURL()+"/orders", "application/json", strings.NewReader(`{"email":"nope"}`))
	s.Require().NoError(err)
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	s.Require().NoError(err)

	body := string(raw)
	s.Contains(body, "must be a valid email address")

	// go-playground's own message is "Key: 'createOrder.Email' Error:Field
	// validation for 'Email' failed on the 'email' tag". None of that is
	// something a caller can act on.
	for _, leak := range []string{"createOrder", "Field validation", "CustomerID", "Key:"} {
		s.NotContains(body, leak, "the answer describes the implementation")
	}
}

// A rule the application wrote itself, reported exactly as a built-in one is.
func (s *RuntimeHTTPTestSuite) Test_a_custom_rule_is_reported_like_any_other() {
	var got validationAnswer
	status := s.post("/orders", `{"customerId":"c1","total":1,"sku":"banana"}`, &got)

	s.Require().Equal(http.StatusBadRequest, status)
	s.Require().Len(got.Details, 1)

	// The tag is the code, so a client branches on "sku" the same way it would
	// on "required".
	s.Equal("sku", got.Details[0].Code)
	s.Equal([]string{"sku"}, got.Details[0].Path)
	s.Equal("must be a SKU, such as AB-1234", got.Details[0].Message)
}

func (s *RuntimeHTTPTestSuite) Test_a_value_passing_a_custom_rule_is_accepted() {
	var created orderCreated
	status := s.post("/orders", `{"customerId":"c1","total":1,"sku":"AB-1234"}`, &created)

	s.Require().Equal(http.StatusOK, status)
	s.Equal("new-c1", created.ID)
}

// A handler that stated only its name, routed entirely by the blueprint. That
// it is reachable at all means the tag the runtime built from the blueprint's
// annotations is the one the handler declared at the handshake.
func (s *RuntimeHTTPTestSuite) Test_a_blueprint_routed_handler_is_reachable() {
	var got struct {
		OrderID string `json:"orderId"`
		Status  string `json:"status"`
		Route   string `json:"route"`
	}
	status := s.post("/orders/order-9/archive", `{}`, &got)

	s.Require().Equal(http.StatusOK, status)
	s.Equal("archived", got.Status)

	// The path parameter binds from a route the code never stated.
	s.Equal("order-9", got.OrderID)
	s.Equal("/orders/{orderId}/archive", got.Route)
}
