package otel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
	"github.com/newstack-cloud/celerity-go-sdk/telemetry/otel"
)

// What a deployment configures tracing with. The names and the defaults are a
// contract with the other SDKs rather than this package's own, so one
// deployment configures every language alike, and OpenTelemetry's own variables
// win over Celerity's so that an operator who knows one configures the other.
type SetupTestSuite struct {
	suite.Suite
}

func TestSetupTestSuite(t *testing.T) {
	suite.Run(t, new(SetupTestSuite))
}

func (s *SetupTestSuite) SetupTest() {
	for _, name := range []string{
		otel.EnabledEnvVar, otel.EndpointEnvVar, otel.CelerityEndpointEnvVar,
		otel.ServiceNameEnvVar, otel.ServiceVersionEnvVar, "CELERITY_PLATFORM",
	} {
		s.T().Setenv(name, "")
	}
}

func (s *SetupTestSuite) Test_nothing_is_exported_unless_the_deployment_asked() {
	// An application that traces nothing should not open a connection to a
	// collector it was never told about.
	configured, err := otel.Configure(context.Background())

	s.Require().NoError(err)
	s.False(configured)
}

func (s *SetupTestSuite) Test_a_deployment_that_asked_gets_a_provider_and_a_flusher() {
	s.T().Setenv(otel.EnabledEnvVar, "true")
	s.T().Cleanup(func() {
		telemetry.SetFlusher(nil)
	})

	configured, err := otel.Configure(context.Background())

	s.Require().NoError(err)
	s.True(configured)
	// The exporter is not reached, since gRPC connects lazily, so what this
	// says is that something is now holding spans and willing to hand them over.
	s.NoError(telemetry.Flush(context.Background()))
}

func (s *SetupTestSuite) Test_the_collector_is_read_from_opentelemetrys_name_first() {
	// An operator who knows OpenTelemetry configures this the way they would
	// anything else; Celerity's name is what a deployment sets for them.
	s.T().Setenv(otel.CelerityEndpointEnvVar, "http://celerity:4317")
	s.T().Setenv(otel.EndpointEnvVar, "http://otel:4317")

	s.Equal("http://otel:4317", otel.ReadSettings().Endpoint)
}

func (s *SetupTestSuite) Test_celeritys_name_is_the_fallback() {
	s.T().Setenv(otel.CelerityEndpointEnvVar, "http://celerity:4317")

	s.Equal("http://celerity:4317", otel.ReadSettings().Endpoint)
}

func (s *SetupTestSuite) Test_the_defaults_are_the_ones_every_sdk_uses() {
	settings := otel.ReadSettings()

	s.Equal(otel.DefaultEndpoint, settings.Endpoint)
	s.Equal(otel.DefaultServiceName, settings.ServiceName)
	s.Equal(otel.DefaultServiceVersion, settings.ServiceVersion)
	s.False(settings.Enabled, "exporting is opt in")
	s.False(settings.AWS)
}

func (s *SetupTestSuite) Test_enabling_is_only_true_and_is_not_case_sensitive() {
	for _, tc := range []struct {
		value   string
		enabled bool
	}{
		{"true", true}, {"TRUE", true}, {"True", true},
		{"1", false}, {"yes", false}, {"", false}, {"false", false},
	} {
		s.Run("CELERITY_TELEMETRY_ENABLED="+tc.value, func() {
			s.T().Setenv(otel.EnabledEnvVar, tc.value)

			s.Equal(tc.enabled, otel.ReadSettings().Enabled)
		})
	}
}

func (s *SetupTestSuite) Test_the_platform_decides_whether_the_trace_format_is_xrays() {
	// X-Ray will not accept a trace id it did not generate the shape of, and
	// the platform propagates in its own header, so a trace started on AWS has
	// to carry both or it breaks at the boundary.
	for _, tc := range []struct {
		platform string
		aws      bool
	}{
		{"aws", true},
		{"aws-serverless", true},
		{"AWS", true},
		{"gcp", false},
		{"local", false},
		{"", false},
	} {
		s.Run("CELERITY_PLATFORM="+tc.platform, func() {
			s.T().Setenv("CELERITY_PLATFORM", tc.platform)

			s.Equal(tc.aws, otel.ReadSettings().AWS)
		})
	}
}

func (s *SetupTestSuite) Test_a_service_named_by_the_deployment_is_what_the_traces_carry() {
	s.T().Setenv(otel.ServiceNameEnvVar, "orders")
	s.T().Setenv(otel.ServiceVersionEnvVar, "2.1.0")

	settings := otel.ReadSettings()

	s.Equal("orders", settings.ServiceName)
	s.Equal("2.1.0", settings.ServiceVersion)
}

func (s *SetupTestSuite) Test_a_collector_that_cannot_be_resolved_is_refused_by_name() {
	// Reported rather than silently falling back, since an application that
	// asked to be traced and was not is the hardest telemetry to notice.
	s.T().Setenv(otel.EnabledEnvVar, "true")
	s.T().Setenv(otel.EndpointEnvVar, "not-a-url://%%")

	_, err := otel.Configure(context.Background())

	s.Require().Error(err)
	s.Contains(err.Error(), otel.EndpointEnvVar, "naming where to fix it")
	s.Contains(err.Error(), otel.EnabledEnvVar, "and how to stop trying")
}
