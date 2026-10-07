package celeritytest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/celeritytest"
	celerityhandler "github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
)

// What the harness puts on a request, and which handler each verb reaches.
// Wrappers that only delegate are where a copy-paste mistake hides, so each is
// exercised rather than assumed.
type RequestTestSuite struct {
	suite.Suite
}

func TestRequestTestSuite(t *testing.T) {
	suite.Run(t, new(RequestTestSuite))
}

// echoed is what the handler saw, reported back so a test can read it.
type echoed struct {
	Method    string `json:"method"`
	Query     string `json:"query"`
	Header    string `json:"header"`
	SourceIP  string `json:"sourceIP"`
	Body      string `json:"body"`
	RequestID string `json:"requestID"`
}

func echoApp() *celerity.App {
	app := celerity.New(celerity.WithResourceProvider(celeritytest.Resources()))

	echo := func(ctx context.Context, req *celerityhandler.Request) (*celerityhandler.Response, error) {
		encoded, err := json.Marshal(echoed{
			Method:    req.Method,
			Query:     req.QueryParams.Get("since"),
			Header:    req.Headers.Get("x-trace"),
			SourceIP:  req.SourceIP,
			Body:      string(req.Body),
			RequestID: req.RequestID,
		})
		if err != nil {
			return nil, err
		}
		return &celerityhandler.Response{
			Status:  http.StatusOK,
			Headers: celerityhandler.Params{"x-answered-by": {"echo"}},
			Body:    encoded,
		}, nil
	}

	for _, method := range []string{
		http.MethodGet, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodHead,
	} {
		celerity.HTTP(app, method, "/echo", echo, celerity.Named("echo"+method))
	}
	return app
}

func (s *RequestTestSuite) Test_each_verb_reaches_the_handler_registered_for_it() {
	app := echoApp()
	harness := celeritytest.New(s.T(), app)

	cases := []struct {
		name string
		send func(testing.TB) *celeritytest.Response
		want string
	}{
		{"get", func(tb testing.TB) *celeritytest.Response { return harness.GET(tb, "/echo") }, http.MethodGet},
		{"post", func(tb testing.TB) *celeritytest.Response { return harness.POST(tb, "/echo") }, http.MethodPost},
		{"put", func(tb testing.TB) *celeritytest.Response { return harness.PUT(tb, "/echo") }, http.MethodPut},
		{"patch", func(tb testing.TB) *celeritytest.Response { return harness.PATCH(tb, "/echo") }, http.MethodPatch},
		{"delete", func(tb testing.TB) *celeritytest.Response { return harness.DELETE(tb, "/echo") }, http.MethodDelete},
	}

	for _, test := range cases {
		s.Run(test.name, func() {
			var got echoed
			test.send(s.T()).AssertStatus(s.T(), http.StatusOK).Decode(s.T(), &got)

			s.Equal(test.want, got.Method)
		})
	}
}

func (s *RequestTestSuite) Test_a_method_without_a_helper_is_sent_with_do() {
	app := echoApp()
	harness := celeritytest.New(s.T(), app)

	var got echoed
	harness.Do(s.T(), "head", "/echo").Decode(s.T(), &got)

	s.Equal(http.MethodHead, got.Method, "lowercased by the caller, canonical on the request")
}

func (s *RequestTestSuite) Test_the_options_a_request_was_given_reach_the_handler() {
	app := echoApp()
	harness := celeritytest.New(s.T(), app)

	var got echoed
	harness.POST(s.T(), "/echo",
		celeritytest.Body([]byte("raw")),
		celeritytest.Query("since", "2026-01-01"),
		celeritytest.Header("X-Trace", "abc"),
		celeritytest.SourceIP("203.0.113.1"),
	).Decode(s.T(), &got)

	s.Equal("raw", got.Body)
	s.Equal("2026-01-01", got.Query)
	s.Equal("abc", got.Header, "a header name is lowercased, as a handler sees it")
	s.Equal("203.0.113.1", got.SourceIP)
}

func (s *RequestTestSuite) Test_a_response_header_is_readable() {
	app := echoApp()
	harness := celeritytest.New(s.T(), app)

	response := harness.GET(s.T(), "/echo")

	s.Equal("echo", response.Header("X-Answered-By"), "read by any casing")
}

func (s *RequestTestSuite) Test_every_dispatch_is_named_so_two_runs_can_be_compared() {
	app := echoApp()
	harness := celeritytest.New(s.T(), app)

	var first, second echoed
	harness.GET(s.T(), "/echo").Decode(s.T(), &first)
	harness.GET(s.T(), "/echo").Decode(s.T(), &second)

	s.Equal("request-1", first.RequestID)
	s.Equal("request-3", second.RequestID, "counted rather than random")
}

func (s *RequestTestSuite) Test_a_body_that_cannot_be_encoded_stops_the_test() {
	app := echoApp()
	harness := celeritytest.New(s.T(), app)

	failed := failureOf(func(tb testing.TB) {
		// A channel has no JSON encoding, which is a fault in what the test is
		// sending rather than in what it is testing.
		harness.POST(tb, "/echo", celeritytest.JSONBody(make(chan int)))
	})

	s.Require().True(failed.stopped)
	s.Contains(failed.message, "encoding the request body")
}

func (s *RequestTestSuite) Test_a_cache_and_a_database_are_reached_by_name_like_the_rest() {
	// The handle an application holds is a tracing wrapper around the double
	// rather than the double itself, so a test reads the double from the
	// provider by name. That is the documented way to reach one, and the only
	// one that does not depend on what wraps it.
	res := celeritytest.Resources()
	app := celerity.New(celerity.WithResourceProvider(res))
	_ = resources.Cache(app, "sessions")
	_ = resources.SQLDatabase(app, "ordersDb")
	s.Require().NoError(app.Err())

	fromProvider, err := res.Cache(resources.Ref{Kind: resources.KindCache, Name: "sessions"})
	s.Require().NoError(err)
	s.Same(res.CacheNamed("sessions"), fromProvider, "one double per name")

	// A database is the one kind with no double to be identical to: an
	// unsupplied one refuses the call rather than recording it.
	database, err := res.SQLDatabase(resources.Ref{
		Kind: resources.KindSQLDatabase, Name: "ordersDb"})
	s.Require().NoError(err)
	_, err = database.Writer(context.Background())
	s.ErrorContains(err, "no in-memory double")

	s.Equal("celeritytest", res.Name())
	s.NoError(res.Close(context.Background()), "the doubles hold nothing to give back")
}
