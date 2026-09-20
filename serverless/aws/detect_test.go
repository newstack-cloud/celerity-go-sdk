package aws_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	awsadapter "github.com/newstack-cloud/celerity-go-sdk/serverless/aws"
)

// Detection is what lets a function that was told nothing still route, so these
// cases cover each shape and the one thing no shape can settle.
type DetectTestSuite struct {
	suite.Suite
}

func TestDetectTestSuite(t *testing.T) {
	suite.Run(t, new(DetectTestSuite))
}

func (s *DetectTestSuite) Test_each_source_is_recognised_by_its_shape() {
	cases := []struct {
		name    string
		fixture string
		want    handler.Kind
	}{
		{"an API Gateway HTTP event", "http.json", handler.KindHTTP},
		{"an API Gateway WebSocket event", "websocket.json", handler.KindWebSocket},
		{"an SQS event", "sqs.json", handler.KindConsumer},
		{"an EventBridge event", "eventbridge.json", handler.KindSchedule},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			kind, err := awsadapter.NewMapper().Detect(fixture(&s.Suite, tc.fixture))

			s.Require().NoError(err)
			s.Equal(tc.want, kind)
		})
	}
}

func (s *DetectTestSuite) Test_what_matches_nothing_is_a_custom_invocation() {
	cases := []struct {
		name    string
		payload string
	}{
		// A custom payload is whatever the caller sent, so it is what is left
		// rather than something positively identified.
		{"a caller's own object", `{"orderId":"ord_42"}`},
		{"an array", `[1,2,3]`},
		{"not JSON at all", `plain text`},
		{"an empty object", `{}`},
		{
			// Records alone is not enough: an S3 notification uses the same
			// envelope, and only aws:sqs reaches a function as a consumer batch.
			name:    "an envelope from another source",
			payload: `{"Records":[{"eventSource":"aws:s3"}]}`,
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			kind, err := awsadapter.NewMapper().Detect([]byte(tc.payload))

			s.Require().NoError(err)
			s.Equal(handler.KindCustom, kind)
		})
	}
}

func (s *DetectTestSuite) Test_the_deployment_is_believed_over_the_payload() {
	// The only thing that can tell a custom invocation from a payload that
	// happens to resemble another source.
	s.T().Setenv(awsadapter.HandlerKindEnvVar, string(handler.KindCustom))

	kind, err := awsadapter.NewMapper().Detect(fixture(&s.Suite, "http.json"))

	s.Require().NoError(err)
	s.Equal(handler.KindCustom, kind)
}

func (s *DetectTestSuite) Test_a_kind_that_is_not_one_falls_back_to_the_payload() {
	cases := []struct {
		name string
		env  string
	}{
		{"a typo", "htttp"},
		{"empty", ""},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.T().Setenv(awsadapter.HandlerKindEnvVar, tc.env)

			kind, err := awsadapter.NewMapper().Detect(fixture(&s.Suite, "http.json"))

			s.Require().NoError(err)
			s.Equal(handler.KindHTTP, kind)
		})
	}
}
