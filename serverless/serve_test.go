package serverless_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/serverless"
)

// Everything with a rule in it lives in the serverless package rather than in
// each adapter, so these cases pin down the rules a provider inherits:
// resolution precedence, what an unroutable event means per source, and when a
// client's message is acknowledged.
type DispatcherTestSuite struct {
	suite.Suite
}

func TestDispatcherTestSuite(t *testing.T) {
	suite.Run(t, new(DispatcherTestSuite))
}

func (s *DispatcherTestSuite) Test_a_function_told_its_handler_by_id_uses_it() {
	adapter := &dispatchAdapter{kind: handler.KindHTTP}
	resolver := &fakeResolver{
		byName: map[string]string{"processOrder": "GET::/orders"},
		only:   "GET::/wrong",
	}
	s.T().Setenv(serverless.HandlerIDEnvVar, "processOrder")

	_, err := serverless.NewInvoker(adapter, resolver)(context.Background(), []byte(`{}`))

	s.Require().NoError(err)
	s.Equal("GET::/orders", resolver.invoked)
}

func (s *DispatcherTestSuite) Test_resolution_falls_back_through_tag_to_the_only_handler() {
	cases := []struct {
		name string
		id   string
		tag  string
		want string
	}{
		{
			name: "the id names nothing, so the tag is tried",
			id:   "notRegistered",
			tag:  "GET::/orders",
			want: "GET::/orders",
		},
		{
			// The ordinary case: a Celerity deployment gives each handler its
			// own function, so a function told nothing still resolves.
			name: "neither is set, so the one handler of the kind is it",
			want: "GET::/only",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.T().Setenv(serverless.HandlerIDEnvVar, tc.id)
			s.T().Setenv(serverless.HandlerTagEnvVar, tc.tag)
			adapter := &dispatchAdapter{kind: handler.KindHTTP}
			resolver := &fakeResolver{
				byTag: map[string]bool{"GET::/orders": true},
				only:  "GET::/only",
			}

			_, err := serverless.NewInvoker(adapter, resolver)(context.Background(), []byte(`{}`))

			s.Require().NoError(err)
			s.Equal(tc.want, resolver.invoked)
		})
	}
}

func (s *DispatcherTestSuite) Test_resolution_happens_once_and_is_kept() {
	adapter := &dispatchAdapter{kind: handler.KindHTTP}
	resolver := &fakeResolver{only: "GET::/orders"}
	invoke := serverless.NewInvoker(adapter, resolver)

	for range 3 {
		_, err := invoke(context.Background(), []byte(`{}`))
		s.Require().NoError(err)
	}

	// A warm invocation shouldn't do a lookup.
	s.Equal(1, resolver.lookups)
}

func (s *DispatcherTestSuite) Test_an_unroutable_request_is_answered_rather_than_failed() {
	cases := []struct {
		name string
		kind handler.Kind
	}{
		// A client-visible answer: an unrouted request is an answer, not an
		// operational failure.
		{"http", handler.KindHTTP},
		{"websocket", handler.KindWebSocket},
		{"custom", handler.KindCustom},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			adapter := &dispatchAdapter{kind: tc.kind}

			out, err := serverless.NewInvoker(adapter, &fakeResolver{})(
				context.Background(), []byte(`{}`))

			s.Require().NoError(err)
			s.NotNil(out)
		})
	}
}

func (s *DispatcherTestSuite) Test_an_unroutable_batch_or_schedule_fails_the_invocation() {
	cases := []struct {
		name string
		kind handler.Kind
	}{
		// The quiet answer is the wrong one. Reporting no batch item failures
		// tells the queue every message was handled and it drains silently, and
		// returning a value from a schedule makes the invocation a success, so a
		// schedule that reaches nothing looks like one that is running.
		{"consumer", handler.KindConsumer},
		{"schedule", handler.KindSchedule},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			adapter := &dispatchAdapter{kind: tc.kind}

			_, err := serverless.NewInvoker(adapter, &fakeResolver{})(
				context.Background(), []byte(`{}`))

			s.Require().ErrorIs(err, serverless.ErrNoHandler)
		})
	}
}

func (s *DispatcherTestSuite) Test_a_websocket_handler_is_given_a_sender() {
	adapter := &dispatchAdapter{kind: handler.KindWebSocket, sender: &countingSender{}}
	var seen bool
	resolver := &fakeResolver{only: "event::sendMessage", observe: func(ctx context.Context) {
		_, seen = serverless.WebSocketSenderFrom(ctx)
	}}

	_, err := serverless.NewInvoker(adapter, resolver)(context.Background(), []byte(`{}`))

	s.Require().NoError(err)
	s.True(seen, "a handler reaches the sender through the context it was given")
}

func (s *DispatcherTestSuite) Test_receipt_is_acknowledged_before_the_handler_runs() {
	// The protocol has a message that asked to be acknowledged acknowledged on
	// receipt, which is what lets a client stop its resend timer without
	// waiting on however long the work takes.
	adapter := &acknowledgingAdapter{
		dispatchAdapter: &dispatchAdapter{kind: handler.KindWebSocket, sender: &countingSender{}},
	}
	var ackedFirst bool
	resolver := &fakeResolver{only: "event::sendMessage", observe: func(context.Context) {
		ackedFirst = adapter.acked
	}}

	_, err := serverless.NewInvoker(adapter, resolver)(context.Background(), []byte(`{}`))

	s.Require().NoError(err)
	s.True(ackedFirst)
}

func (s *DispatcherTestSuite) Test_receipt_is_acknowledged_even_where_nothing_serves_the_message() {
	// The client asked whether its message arrived, and it did.
	adapter := &acknowledgingAdapter{
		dispatchAdapter: &dispatchAdapter{kind: handler.KindWebSocket, sender: &countingSender{}},
	}

	_, err := serverless.NewInvoker(adapter, &fakeResolver{})(context.Background(), []byte(`{}`))

	s.Require().NoError(err)
	s.True(adapter.acked)
}

func (s *DispatcherTestSuite) Test_a_failed_acknowledgement_does_not_fail_the_invocation() {
	// The message itself did arrive. The client's own resend, after its
	// timeout, is the recovery the protocol already specifies.
	adapter := &acknowledgingAdapter{dispatchAdapter: &dispatchAdapter{
		kind:   handler.KindWebSocket,
		sender: &countingSender{},
		ackErr: errors.New("the connection is gone"),
	}}
	resolver := &fakeResolver{only: "event::sendMessage"}

	out, err := serverless.NewInvoker(adapter, resolver)(context.Background(), []byte(`{}`))

	s.Require().NoError(err)
	s.NotNil(out)
}

func (s *DispatcherTestSuite) Test_an_adapter_that_acknowledges_nothing_is_not_asked_to() {
	// The Celerity runtime acknowledges receipt itself, so an adapter for a
	// transport that does the same implements none of this.
	adapter := &dispatchAdapter{kind: handler.KindWebSocket, sender: &countingSender{}}
	resolver := &fakeResolver{only: "event::sendMessage"}

	_, err := serverless.NewInvoker(adapter, resolver)(context.Background(), []byte(`{}`))

	s.Require().NoError(err)
	s.False(adapter.acked)
}
