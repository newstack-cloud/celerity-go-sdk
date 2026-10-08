package otel

import (
	"context"
	"fmt"
	"net/url"

	"go.opentelemetry.io/contrib/propagators/aws/xray"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

// Configure builds a tracer provider from what the environment asked for and
// installs it, along with the propagators and the flusher that goes with it.
//
// Reports whether it installed one. Where the environment did not ask for
// exporting there is nothing to install, which is the ordinary case for an
// application that doesn't trace anything, OpenTelemetry's own no-op answers instead.
//
// An application that wants its provider built differently does not have to
// avoid this. Whatever it installs afterwards wins, because the tracer resolves
// the provider per call rather than capturing one, and `init` runs before
// `main` does. Installing its own [telemetry.SetFlusher] alongside is what
// makes a shutdown flush what it built.
func Configure(ctx context.Context) (bool, error) {
	settings := ReadSettings()
	if !settings.Enabled {
		return false, nil
	}

	provider, err := providerFor(ctx, settings)
	if err != nil {
		return false, err
	}

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagatorFor(settings))
	telemetry.SetFlusher(flusherFor(provider))

	return true, nil
}

func endpointError(endpoint string, err error) error {
	return fmt.Errorf(
		"celerity: exporting traces to %q: %w.\nSet %s or %s to a collector that can be "+
			"reached, or unset %s to stop exporting",
		endpoint, err, EndpointEnvVar, CelerityEndpointEnvVar, EnabledEnvVar)
}

func providerFor(ctx context.Context, settings Settings) (*sdktrace.TracerProvider, error) {
	// Checked here rather than left to the exporter, which connects lazily and
	// would accept a typo now and export nowhere later. An application that
	// asked to be traced and was not is the hardest telemetry to notice.
	if _, err := url.Parse(settings.Endpoint); err != nil {
		return nil, endpointError(settings.Endpoint, err)
	}

	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(settings.Endpoint))
	if err != nil {
		return nil, endpointError(settings.Endpoint, err)
	}

	// Schemaless rather than carrying one of its own and merging two resources
	// with different schema versions is a conflict, and the SDK's default
	// resource moves to whatever version it was built against.
	described, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(settings.ServiceName),
		semconv.ServiceVersion(settings.ServiceVersion),
	))
	if err != nil {
		return nil, fmt.Errorf("celerity: describing the service in its traces: %w", err)
	}

	options := []sdktrace.TracerProviderOption{
		// Batched rather than one request per span, to make
		// trace exports more efficient and less likely to interfere
		// with application performance.
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(described),
	}
	if settings.AWS {
		// X-Ray will not accept a trace id it did not generate the shape of,
		// so a trace started here has to carry one.
		options = append(options, sdktrace.WithIDGenerator(xray.NewIDGenerator()))
	}

	return sdktrace.NewTracerProvider(options...), nil
}

// Chooses the propagator for how a trace arriving from upstream is read and how one
// leaving is written.
//
// W3C always, since that is what every other Celerity runtime speaks, and
// X-Ray's own header alongside it on AWS, where the platform itself propagates
// in that format and a trace would otherwise break at the boundary. W3C Baggage
// travels with both.
func propagatorFor(settings Settings) propagation.TextMapPropagator {
	carried := []propagation.TextMapPropagator{
		propagation.TraceContext{},
		propagation.Baggage{},
	}
	if settings.AWS {
		carried = append(carried, xray.Propagator{})
	}
	return propagation.NewCompositeTextMapPropagator(carried...)
}

// flusherFor hands the provider's held spans over.
//
// ForceFlush rather than Shutdown, because this is called where an execution
// environment is about to be frozen as well as where a process is stopping, and
// a provider shut down on the first invocation would export nothing on the
// next.
func flusherFor(provider *sdktrace.TracerProvider) telemetry.Flusher {
	return func(ctx context.Context) error {
		if err := provider.ForceFlush(ctx); err != nil {
			return fmt.Errorf("celerity: handing over the traces held: %w", err)
		}
		return nil
	}
}

// Stated so that a change to the SDK's provider type is a failure here rather
// than at the call site that wanted it.
var _ oteltrace.TracerProvider = (*sdktrace.TracerProvider)(nil)
