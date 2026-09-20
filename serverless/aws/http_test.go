package aws_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	awsadapter "github.com/newstack-cloud/celerity-go-sdk/serverless/aws"
)

// A handler is written once and must see the same request whether the Celerity
// runtime routed it or API Gateway did, so these cases pin the mapping against
// what the runtime produces rather than against what is convenient to map.
type HTTPTestSuite struct {
	suite.Suite
	mapper awsadapter.Mapper
}

func TestHTTPTestSuite(t *testing.T) {
	suite.Run(t, new(HTTPTestSuite))
}

func fixture(s *suite.Suite, name string) []byte {
	payload, err := os.ReadFile(filepath.Join("testdata", name))
	s.Require().NoError(err)
	return payload
}

func (s *HTTPTestSuite) Test_a_proxy_event_becomes_a_request() {
	req, err := s.mapper.ToRequest(fixture(&s.Suite, "http.json"))

	s.Require().NoError(err)
	s.Equal("POST", req.Method, "the method is canonicalised, as the runtime gives it")
	s.Equal("/orders/ord_42/files/reports/q1/summary.pdf", req.Path)
	s.Equal("203.0.113.7", req.SourceIP)
	s.Equal("req-abc-123", req.RequestID)
	s.JSONEq(`{"note":"ship it"}`, string(req.Body))
}

func (s *HTTPTestSuite) Test_the_route_arrives_in_the_routers_form() {
	req, err := s.mapper.ToRequest(fixture(&s.Suite, "http.json"))

	s.Require().NoError(err)
	// The blueprint declares {path+}; the router, and so every handler tag,
	// spells it {*path}.
	s.Equal("/orders/{orderId}/files/{*path}", req.Route)
}

func (s *HTTPTestSuite) Test_a_catch_all_is_split_into_segments() {
	req, err := s.mapper.ToRequest(fixture(&s.Suite, "http.json"))

	s.Require().NoError(err)
	// API Gateway gives one value with the separators still in it. Split, so a
	// handler binding a []string sees what the runtime would have given it, and
	// one binding a string is not silently handed only the first segment.
	s.Equal(handler.Values{"reports", "q1", "summary.pdf"}, req.PathParams["path"])
	s.Equal(handler.Values{"ord_42"}, req.PathParams["orderId"])
}

func (s *HTTPTestSuite) Test_header_names_are_lowercased_and_cookies_are_folded_in() {
	req, err := s.mapper.ToRequest(fixture(&s.Suite, "http.json"))

	s.Require().NoError(err)
	s.Equal("application/json", req.Headers.Get("content-type"))
	s.Equal("trace-9", req.Headers.Get("x-trace-id"))
	// The platform lists cookies separately; a handler reads the header.
	s.Equal("session=abc123; theme=dark", req.Headers.Get("cookie"))
}

func (s *HTTPTestSuite) Test_route_keys_without_a_template() {
	cases := []struct {
		name     string
		routeKey string
		rawPath  string
		want     string
	}{
		{"the fallback route names no template", "$default", "/anything/here", "/anything/here"},
		{"a route key with no method", "/health", "/health", "/health"},
		{"an ordinary template", "GET /orders/{orderId}", "/orders/1", "/orders/{orderId}"},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			payload, err := json.Marshal(events.APIGatewayV2HTTPRequest{
				RouteKey: tc.routeKey,
				RawPath:  tc.rawPath,
				RequestContext: events.APIGatewayV2HTTPRequestContext{
					HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "GET"},
				},
			})
			s.Require().NoError(err)

			req, err := s.mapper.ToRequest(payload)

			s.Require().NoError(err)
			s.Equal(tc.want, req.Route)
		})
	}
}

func (s *HTTPTestSuite) Test_a_base64_body_is_decoded() {
	body := []byte{0x89, 0x50, 0x4e, 0x47}
	payload, err := json.Marshal(events.APIGatewayV2HTTPRequest{
		RouteKey:        "POST /upload",
		RawPath:         "/upload",
		Body:            base64.StdEncoding.EncodeToString(body),
		IsBase64Encoded: true,
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "POST"},
		},
	})
	s.Require().NoError(err)

	req, err := s.mapper.ToRequest(payload)

	s.Require().NoError(err)
	s.Equal(body, req.Body, "a handler receives the bytes, not the encoding of them")
}

func (s *HTTPTestSuite) Test_an_event_of_the_wrong_shape_is_refused() {
	_, err := s.mapper.ToRequest([]byte(`not an event`))

	s.Require().Error(err)
	s.Contains(err.Error(), "API Gateway HTTP",
		"the error should name what the function was wired to expect")
}

func (s *HTTPTestSuite) Test_a_response_carries_status_headers_and_body() {
	out, err := s.mapper.FromResponse(&handler.Response{
		Status:  201,
		Headers: handler.Params{"content-type": {"application/json"}},
		Body:    []byte(`{"id":"ord_42"}`),
	})

	s.Require().NoError(err)
	res, ok := out.(events.APIGatewayV2HTTPResponse)
	s.Require().True(ok)
	s.Equal(201, res.StatusCode)
	s.Equal("application/json", res.Headers["content-type"])
	s.JSONEq(`{"id":"ord_42"}`, res.Body)
	s.False(res.IsBase64Encoded)
}

func (s *HTTPTestSuite) Test_set_cookie_is_carried_as_cookies_rather_than_a_header() {
	out, err := s.mapper.FromResponse(&handler.Response{
		Status: 200,
		Headers: handler.Params{
			"Set-Cookie": {"session=abc; HttpOnly", "theme=dark"},
			"x-request":  {"one", "two"},
		},
	})

	s.Require().NoError(err)
	res, ok := out.(events.APIGatewayV2HTTPResponse)
	s.Require().True(ok)
	// Folding two Set-Cookie headers into one comma-separated value is
	// forbidden, and an HTTP API has no multi-value header field to use, so
	// they go where the platform carries them.
	s.ElementsMatch([]string{"session=abc; HttpOnly", "theme=dark"}, res.Cookies)
	s.NotContains(res.Headers, "Set-Cookie")
	s.Equal("one, two", res.Headers["x-request"], "every other name is joined")
}

func (s *HTTPTestSuite) Test_a_body_that_is_not_text_is_declared_as_encoded() {
	body := []byte{0xff, 0xfe, 0x00, 0x01}

	out, err := s.mapper.FromResponse(&handler.Response{Status: 200, Body: body})

	s.Require().NoError(err)
	res, ok := out.(events.APIGatewayV2HTTPResponse)
	s.Require().True(ok)
	s.True(res.IsBase64Encoded, "without the declaration the client is served the base64 text")
	s.Equal(base64.StdEncoding.EncodeToString(body), res.Body)
}

func (s *HTTPTestSuite) Test_repeated_query_parameters_keep_their_values() {
	req, err := s.mapper.ToRequest(fixture(&s.Suite, "http.json"))

	s.Require().NoError(err)
	// Read from the raw query string, which is what the client sent, so
	// ?include=lines&include=totals is two values. The platform's own
	// queryStringParameters map has already joined them with a comma
	// indistinguishable from one the caller wrote.
	s.Equal(handler.Values{"lines", "totals"}, req.QueryParams["include"])
}

func (s *HTTPTestSuite) Test_query_parameters_are_decoded_as_the_client_wrote_them() {
	cases := []struct {
		name  string
		raw   string
		param string
		want  handler.Values
	}{
		{"a single value", "q=celerity", "q", handler.Values{"celerity"}},
		{"repeated", "tag=a&tag=b&tag=c", "tag", handler.Values{"a", "b", "c"}},
		{
			// The comma is the caller's own, not the platform's separator,
			// which is exactly the distinction the joined map cannot make.
			name:  "one value that contains a comma",
			raw:   "q=hello%2Cworld",
			param: "q",
			want:  handler.Values{"hello,world"},
		},
		{"a plus is a space", "q=hello+world", "q", handler.Values{"hello world"}},
		{"percent-encoded", "path=%2Freports%2Fq1", "path", handler.Values{"/reports/q1"}},
		{"a flag with no value", "verbose", "verbose", handler.Values{""}},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			req, err := s.mapper.ToRequest(s.requestWithQuery(tc.raw))

			s.Require().NoError(err)
			s.Equal(tc.want, req.QueryParams[tc.param])
		})
	}
}

func (s *HTTPTestSuite) Test_a_malformed_escape_does_not_lose_the_rest() {
	// The pairs that are well-formed are still the caller's.
	req, err := s.mapper.ToRequest(s.requestWithQuery("good=yes&bad=%zz"))

	s.Require().NoError(err)
	s.Equal(handler.Values{"yes"}, req.QueryParams["good"])
}

func (s *HTTPTestSuite) Test_an_event_with_no_raw_query_string_falls_back_to_the_map() {
	// Which an event built by hand rather than by API Gateway can leave out.
	payload, err := json.Marshal(events.APIGatewayV2HTTPRequest{
		RouteKey:              "GET /orders",
		RawPath:               "/orders",
		QueryStringParameters: map[string]string{"limit": "25"},
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "GET"},
		},
	})
	s.Require().NoError(err)

	req, err := s.mapper.ToRequest(payload)

	s.Require().NoError(err)
	s.Equal(handler.Values{"25"}, req.QueryParams["limit"])
}

func (s *HTTPTestSuite) requestWithQuery(raw string) []byte {
	payload, err := json.Marshal(events.APIGatewayV2HTTPRequest{
		RouteKey:       "GET /orders",
		RawPath:        "/orders",
		RawQueryString: raw,
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "GET"},
		},
	})
	s.Require().NoError(err)
	return payload
}
