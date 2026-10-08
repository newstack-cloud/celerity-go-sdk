package telemetry_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

// What a deployment configures logging with, and what a person reads in a
// development session. The format is the part worth pinning: a record is read
// by an aggregator in one case and by a person scrolling past in the other, and
// those want different things.
type LoggingTestSuite struct {
	suite.Suite
}

func TestLoggingTestSuite(t *testing.T) {
	suite.Run(t, new(LoggingTestSuite))
}

// human returns the lines a record was written as, in the readable format.
func (s *LoggingTestSuite) human(write func(*slog.Logger)) []string {
	s.T().Helper()
	s.T().Setenv(telemetry.LogFormatEnvVar, telemetry.FormatHuman)

	var out bytes.Buffer
	write(telemetry.NewLogger(&out, "local"))

	return strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
}

func (s *LoggingTestSuite) Test_a_record_is_a_line_and_its_attributes_are_indented_beneath() {
	// Rather than all on one line: a dispatch carries the trace, the handler
	// and the route before the application has added anything, and a dozen of
	// those wraps across a terminal into something nobody can scan.
	lines := s.human(func(logger *slog.Logger) {
		logger.Info("order created",
			slog.String("order.id", "o-1"),
			slog.Int("order.total", 1495))
	})

	s.Require().Len(lines, 3)
	s.Contains(lines[0], "INFO")
	s.True(strings.HasSuffix(lines[0], "order created"), "the message ends the first line")
	s.Equal("    order.id: o-1", lines[1])
	s.Equal("    order.total: 1495", lines[2])
}

func (s *LoggingTestSuite) Test_a_record_with_no_attributes_is_one_line() {
	lines := s.human(func(logger *slog.Logger) { logger.Info("serving") })

	s.Require().Len(lines, 1)
	s.True(strings.HasSuffix(lines[0], "serving"))
}

func (s *LoggingTestSuite) Test_the_trace_comes_first_and_the_rest_is_sorted() {
	// The trace is what gets copied when a record starts an investigation, and
	// sorted alphabetically it would sit in the middle of the application's own
	// keys. Everything else is sorted so the same record reads the same twice.
	lines := s.human(func(logger *slog.Logger) {
		logger.Info("dispatched",
			slog.String("zebra", "last"),
			slog.String(telemetry.SpanIDKey, "span-1"),
			slog.String("apple", "first"),
			slog.String(telemetry.TraceIDKey, "trace-1"))
	})

	s.Require().Len(lines, 5)
	s.Equal("    "+telemetry.TraceIDKey+": trace-1", lines[1])
	s.Equal("    "+telemetry.SpanIDKey+": span-1", lines[2])
	s.Equal("    apple: first", lines[3])
	s.Equal("    zebra: last", lines[4])
}

func (s *LoggingTestSuite) Test_a_group_is_written_as_one_attribute_per_leaf() {
	// The keys a reader sees are the keys the JSON of the same record carries,
	// so a question asked of one can be asked of the other.
	lines := s.human(func(logger *slog.Logger) {
		logger.Info("called",
			slog.Group("http", slog.String("method", "POST"), slog.Int("status", 201)))
	})

	s.Require().Len(lines, 3)
	s.Equal("    http.method: POST", lines[1])
	s.Equal("    http.status: 201", lines[2])
}

func (s *LoggingTestSuite) Test_what_with_and_with_group_bound_is_written_too() {
	lines := s.human(func(logger *slog.Logger) {
		logger.With(slog.String("handler.name", "createOrder")).
			WithGroup("order").
			Info("created", slog.String("id", "o-1"))
	})

	s.Require().Len(lines, 3)
	s.Equal("    handler.name: createOrder", lines[1])
	s.Equal("    order.id: o-1", lines[2], "the group names the key it is inside")
}

func (s *LoggingTestSuite) Test_a_value_that_would_run_into_the_next_line_is_quoted() {
	lines := s.human(func(logger *slog.Logger) {
		logger.Info("failed",
			slog.String("reason", "a conditional check failed"),
			slog.String("plain", "retried"))
	})

	s.Require().Len(lines, 3)
	s.Equal(`    plain: retried`, lines[1], "left alone where it reads cleanly")
	s.Equal(`    reason: "a conditional check failed"`, lines[2])
}

func (s *LoggingTestSuite) Test_a_deployment_gets_json_and_a_session_gets_the_readable_form() {
	for _, tc := range []struct {
		name     string
		format   string
		platform string
		json     bool
	}{
		{"a session, nothing asked for", "", "local", false},
		{"no platform at all", "", "", false},
		{"a deployment", "", "aws", true},
		{"a deployment asking to read them itself", telemetry.FormatHuman, "aws", false},
		{"a session asking for json, which the CLI does", telemetry.FormatJSON, "local", true},
	} {
		s.Run(tc.name, func() {
			s.T().Setenv(telemetry.LogFormatEnvVar, tc.format)

			var out bytes.Buffer
			telemetry.NewLogger(&out, tc.platform).Info("hello", slog.String("k", "v"))

			s.Equal(tc.json, strings.HasPrefix(out.String(), "{"), out.String())
		})
	}
}

func (s *LoggingTestSuite) Test_the_level_is_read_from_the_environment() {
	for _, tc := range []struct {
		env     string
		written []string
		absent  []string
	}{
		{"", []string{"info", "warn", "error"}, []string{"debug"}},
		{"debug", []string{"debug", "info", "warn", "error"}, nil},
		{"warn", []string{"warn", "error"}, []string{"debug", "info"}},
		{"warning", []string{"warn", "error"}, []string{"info"}},
		{"error", []string{"error"}, []string{"warn", "info"}},
		{"nonsense", []string{"info"}, []string{"debug"}},
	} {
		s.Run("CELERITY_LOG_LEVEL="+tc.env, func() {
			s.T().Setenv(telemetry.LogLevelEnvVar, tc.env)
			s.T().Setenv(telemetry.LogFormatEnvVar, telemetry.FormatHuman)

			var out bytes.Buffer
			logger := telemetry.NewLogger(&out, "local")
			logger.Debug("debug")
			logger.Info("info")
			logger.Warn("warn")
			logger.Error("error")

			for _, message := range tc.written {
				s.Contains(out.String(), message)
			}
			for _, message := range tc.absent {
				s.NotContains(out.String(), message)
			}
		})
	}
}

func (s *LoggingTestSuite) Test_the_handler_is_the_one_the_logger_writes_through() {
	// Exported so an application adding attributes of its own, or wrapping this
	// in one that samples, starts from the same format rather than rebuilding it.
	var out bytes.Buffer
	s.T().Setenv(telemetry.LogFormatEnvVar, telemetry.FormatHuman)

	handler := telemetry.NewHandler(&out, "local")
	slog.New(handler).Info("direct", slog.String("k", "v"))

	s.Contains(out.String(), "direct")
	s.Contains(out.String(), "    k: v")
}

func (s *LoggingTestSuite) Test_a_record_is_written_with_the_time_it_happened() {
	s.T().Setenv(telemetry.LogFormatEnvVar, telemetry.FormatHuman)

	var out bytes.Buffer
	telemetry.NewLogger(&out, "local").Info("now")

	stamp := strings.Fields(out.String())[0]
	_, err := time.Parse("15:04:05.000", stamp)
	s.Require().NoError(err, "the first field is the time, to the millisecond")
}
