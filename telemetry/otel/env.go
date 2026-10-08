package otel

import (
	"os"
	"strings"
)

// Environment variables telemetry is configured with.
//
// OpenTelemetry's own are read first and Celerity's are the fallback, so an
// operator who knows OpenTelemetry configures this the way they would configure
// anything else, and a Celerity deployment that knows nothing about the
// application still points it at the right collector. The same names and the
// same order the other SDKs read, so one deployment configures every
// language alike.
const (
	// EnabledEnvVar turns exporting on. Anything but true leaves the
	// application tracing to OpenTelemetry's no-op, which costs a call rather
	// than a branch at every span.
	EnabledEnvVar = "CELERITY_TELEMETRY_ENABLED"
	// EndpointEnvVar is OpenTelemetry's own name for where to export to.
	EndpointEnvVar = "OTEL_EXPORTER_OTLP_ENDPOINT"
	// CelerityEndpointEnvVar is what a Celerity deployment sets instead.
	CelerityEndpointEnvVar = "CELERITY_TRACE_OTLP_COLLECTOR_ENDPOINT"
	// ServiceNameEnvVar names the application in the traces it produces.
	ServiceNameEnvVar = "OTEL_SERVICE_NAME"
	// ServiceVersionEnvVar is the version recorded alongside the name.
	ServiceVersionEnvVar = "OTEL_SERVICE_VERSION"
)

// Defaults for what a deployment did not say.
const (
	// DefaultEndpoint is the collector a development session runs.
	DefaultEndpoint = "http://otelcollector:4317"
	// DefaultServiceName is what an application is called where it says
	// nothing, which is better than a trace of unnamed spans.
	DefaultServiceName = "celerity-app"
	// DefaultServiceVersion is what is recorded where a build stamped none.
	DefaultServiceVersion = "0.0.0"
)

// Settings is what the environment asked for.
type Settings struct {
	// Enabled reports whether an exporter is configured at all.
	Enabled bool
	// Endpoint is the collector to export to.
	Endpoint string
	// ServiceName and ServiceVersion name the application in its traces.
	ServiceName    string
	ServiceVersion string
	// AWS reports whether the platform is one whose tracing has an identifier
	// format of its own, which decides the propagator and the id generator.
	AWS bool
}

// ReadSettings resolves the settings from the environment.
//
// Exported so that a test, or an application building a provider of its own,
// can read what a deployment asked for rather than reading the variables again
// and disagreeing about the defaults.
func ReadSettings() Settings {
	return Settings{
		Enabled:        strings.EqualFold(os.Getenv(EnabledEnvVar), "true"),
		Endpoint:       firstSet(DefaultEndpoint, EndpointEnvVar, CelerityEndpointEnvVar),
		ServiceName:    firstSet(DefaultServiceName, ServiceNameEnvVar),
		ServiceVersion: firstSet(DefaultServiceVersion, ServiceVersionEnvVar),
		AWS:            strings.HasPrefix(strings.ToLower(os.Getenv(platformEnvVar)), "aws"),
	}
}

// platformEnvVar names the platform the application runs on, which core reads
// too. Read here rather than imported so that this package depends on the name
// and not on core's resolution of it into a Platform.
const platformEnvVar = "CELERITY_PLATFORM"

// firstSet is the first of the variables that was set, and the fallback where
// none was.
func firstSet(fallback string, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return fallback
}
