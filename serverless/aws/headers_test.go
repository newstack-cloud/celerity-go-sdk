package aws_test

import (
	"encoding/json"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	awsadapter "github.com/newstack-cloud/celerity-go-sdk/serverless/aws"
)

// API Gateway joins repeated headers with a comma and payload format 2.0 has no
// multi-value header field, so the division the client made is not recoverable
// and splitting is a guess. These cases pin down where the guess cannot be
// wrong, and that everything else is left alone.
type HeadersTestSuite struct {
	suite.Suite
}

func TestHeadersTestSuite(t *testing.T) {
	suite.Run(t, new(HeadersTestSuite))
}

func (s *HeadersTestSuite) Test_a_header_whose_grammar_is_a_token_list_is_divided() {
	cases := []struct {
		header string
		value  string
		want   handler.Values
	}{
		{"accept-encoding", "gzip, deflate, br", handler.Values{"gzip", "deflate", "br"}},
		{"accept-language", "en-GB,en;q=0.9", handler.Values{"en-GB", "en;q=0.9"}},
		{"x-forwarded-for", "203.0.113.7, 198.51.100.4", handler.Values{"203.0.113.7", "198.51.100.4"}},
		{"connection", "keep-alive, upgrade", handler.Values{"keep-alive", "upgrade"}},
		{"via", "1.1 alpha, 1.1 beta", handler.Values{"1.1 alpha", "1.1 beta"}},
		{"accept", "application/json, text/plain;q=0.9", handler.Values{"application/json", "text/plain;q=0.9"}},
		{"cache-control", "no-cache, max-age=0", handler.Values{"no-cache", "max-age=0"}},
		{"if-none-match", `"a", "b"`, handler.Values{`"a"`, `"b"`}},
	}

	for _, tc := range cases {
		s.Run(tc.header, func() {
			req, err := awsadapter.NewMapper().ToRequest(s.requestWith(tc.header, tc.value))

			s.Require().NoError(err)
			s.Equal(tc.want, req.Headers[tc.header])
		})
	}
}

func (s *HeadersTestSuite) Test_a_header_that_can_carry_a_comma_of_its_own_is_left_whole() {
	cases := []struct {
		name   string
		header string
		value  string
	}{
		{"free-form", "user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7, rv:2)"},
		{"an application's own header", "x-trace-id", "trace-9,trace-10"},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			req, err := awsadapter.NewMapper().ToRequest(s.requestWith(tc.header, tc.value))

			s.Require().NoError(err)
			s.Equal(handler.Values{tc.value}, req.Headers[tc.header])
		})
	}
}

func (s *HeadersTestSuite) Test_a_deployment_can_add_headers_it_knows_its_clients_repeat() {
	s.T().Setenv(awsadapter.SplitHeadersEnvVar, "x-trace-id, x-tags")

	req, err := awsadapter.NewMapper().ToRequest(s.requestWith("x-tags", "one, two"))

	s.Require().NoError(err)
	s.Equal(handler.Values{"one", "two"}, req.Headers["x-tags"])
}

func (s *HeadersTestSuite) Test_configuring_adds_to_the_defaults_rather_than_replacing_them() {
	// The default list holds only headers that cannot contain a comma except as
	// a separator, so there is nothing in it a deployment would need to remove.
	s.T().Setenv(awsadapter.SplitHeadersEnvVar, "x-trace-id")

	req, err := awsadapter.NewMapper().ToRequest(s.requestWith("accept-encoding", "gzip, br"))

	s.Require().NoError(err)
	s.Equal(handler.Values{"gzip", "br"}, req.Headers["accept-encoding"])
}

func (s *HeadersTestSuite) Test_a_configured_list_is_read_forgivingly() {
	cases := []struct {
		name string
		env  string
	}{
		{"spaces around names", " x-trace-id , x-tags "},
		{"names in any case", "X-Trace-Id,X-Tags"},
		{"empty entries", "x-trace-id,,x-tags,"},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.T().Setenv(awsadapter.SplitHeadersEnvVar, tc.env)

			req, err := awsadapter.NewMapper().ToRequest(s.requestWith("x-trace-id", "a,b"))

			s.Require().NoError(err)
			s.Equal(handler.Values{"a", "b"}, req.Headers["x-trace-id"])
		})
	}
}

func (s *HeadersTestSuite) Test_no_http_date_header_is_ever_divided_by_default() {
	// The comma after the day name is part of the date, so dividing one of
	// these is wrong every time rather than the judgement call the rest of the
	// list involves, and they appear on every conditional request. This guards
	// the rule that built the default list, not any one header's value.
	for _, name := range []string{
		"date",
		"expires",
		"if-modified-since",
		"if-unmodified-since",
		"last-modified",
		"retry-after",
	} {
		s.Run(name, func() {
			value := "Mon, 19 Sep 2026 10:00:00 GMT"

			req, err := awsadapter.NewMapper().ToRequest(s.requestWith(name, value))

			s.Require().NoError(err)
			s.Equal(handler.Values{value}, req.Headers[name])
		})
	}
}

func (s *HeadersTestSuite) Test_a_split_header_with_no_comma_is_one_value() {
	req, err := awsadapter.NewMapper().ToRequest(s.requestWith("accept-encoding", "gzip"))

	s.Require().NoError(err)
	s.Equal(handler.Values{"gzip"}, req.Headers["accept-encoding"])
}

func (s *HeadersTestSuite) Test_cookies_are_never_divided() {
	// A cookie value is opaque and may contain anything, which is why the
	// platform keeps these out of the headers in the first place.
	payload, err := json.Marshal(events.APIGatewayV2HTTPRequest{
		RouteKey: "GET /orders",
		RawPath:  "/orders",
		Cookies:  []string{"session=a,b,c", "theme=dark"},
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "GET"},
		},
	})
	s.Require().NoError(err)

	req, err := awsadapter.NewMapper().ToRequest(payload)

	s.Require().NoError(err)
	s.Equal(handler.Values{"session=a,b,c; theme=dark"}, req.Headers["cookie"])
}

func (s *HeadersTestSuite) requestWith(name, value string) []byte {
	payload, err := json.Marshal(events.APIGatewayV2HTTPRequest{
		RouteKey: "GET /orders",
		RawPath:  "/orders",
		Headers:  map[string]string{name: value},
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "GET"},
		},
	})
	s.Require().NoError(err)
	return payload
}
