package telemetry

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"sort"
	"strings"
)

// Environment variables a deployment configures logging with.
//
// The same names the other SDKs read, so one deployment configures
// every language the same way.
const (
	// LogLevelEnvVar is the lowest level written: debug, info, warn or error.
	// Anything else, including an empty value, is info.
	LogLevelEnvVar = "CELERITY_LOG_LEVEL"
	// LogFormatEnvVar is json, human, or auto. Auto, which is the default,
	// writes human-readable records in a development session and JSON
	// everywhere else.
	LogFormatEnvVar = "CELERITY_LOG_FORMAT"
)

// Log formats a deployment can ask for.
const (
	FormatJSON  = "json"
	FormatHuman = "human"
	FormatAuto  = "auto"
)

// NewLogger returns the logger an application writes through where it supplies
// none of its own.
//
// JSON by default, because a deployed application's records are read by a log
// aggregator rather than by a person. Human-readable in a development session,
// where they are read by a person as they scroll past and JSON is noise around
// the one line they are looking for.
//
// An application that wants something else passes it to [celerity.WithLogger],
// and anything slog can write through works, the format here is a default
// rather than a constraint.
func NewLogger(out io.Writer, platform string) *slog.Logger {
	return slog.New(NewHandler(out, platform))
}

// NewHandler returns the handler [NewLogger] writes through.
//
// Exported so that an application adding attributes of its own, or wrapping
// this in a handler that samples, starts from the same format rather than
// rebuilding it.
func NewHandler(out io.Writer, platform string) slog.Handler {
	options := &slog.HandlerOptions{Level: levelFromEnv()}
	if humanReadable(platform) {
		return &humanHandler{out: out, options: options}
	}
	return slog.NewJSONHandler(out, options)
}

// Reports whether records are for a person reading them as they
// go rather than for a log aggregator.
func humanReadable(platform string) bool {
	switch strings.ToLower(os.Getenv(LogFormatEnvVar)) {
	case FormatHuman:
		return true
	case FormatJSON:
		return false
	default:
		return platform == "" || platform == "local"
	}
}

func levelFromEnv() slog.Level {
	switch strings.ToLower(os.Getenv(LogLevelEnvVar)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Writes a record as a line with its attributes indented beneath, for a person
// watching a development session.
//
// The shape the Celerity CLI renders a session's logs in, so an application run
// under the CLI and one run directly read the same way. Which is also why the
// attributes are not on the message's line: a dispatch carries the trace, the
// handler and the route before the application has added anything of its own,
// and a dozen of those on one line wraps across a terminal into something
// nobody can scan.
//
// Written out rather than taken from a library, because what it has to do is
// put the message first and the attributes after it in a stable order, and a
// dependency for that is one every application carries to read its own logs.
type humanHandler struct {
	out     io.Writer
	options *slog.HandlerOptions
	// groups and attrs are what With and WithGroup bound, carried so that a
	// handler derived from this one writes what the original would.
	groups []string
	attrs  []slog.Attr
}

func (h *humanHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.options.Level.Level()
}

func (h *humanHandler) Handle(_ context.Context, record slog.Record) error {
	var out strings.Builder

	fmt.Fprintf(&out, "%s %-5s %s\n",
		record.Time.Format("15:04:05.000"), levelLabel(record.Level), record.Message)

	// h.attrs were named when they were bound, since a group opened after them
	// does not contain them. The record's own are inside whatever is open now.
	var inRecord []slog.Attr
	record.Attrs(func(attr slog.Attr) bool {
		inRecord = append(inRecord, attr)
		return true
	})
	bound := append(slices.Clone(h.attrs), flattened(h.groups, inRecord)...)

	for _, attr := range ordered(bound) {
		fmt.Fprintf(&out, "    %s: %s\n", attr.Key, formatValue(attr.Value))
	}

	_, err := io.WriteString(h.out, out.String())
	return err
}

// Resolves a group to one attribute per leaf, named by the path to it, so that
// a record and the JSON of the same record carry the same keys. An empty group
// contributes nothing, the way it contributes no object to the JSON.
func flattened(groups []string, attrs []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if attr.Value.Kind() != slog.KindGroup {
			out = append(out, slog.Attr{Key: qualify(groups, attr.Key), Value: attr.Value})
			continue
		}
		out = append(out, flattened(append(groups, attr.Key), attr.Value.Group())...)
	}
	return out
}

// ordered puts the trace attributes first and leaves the rest as they were
// bound, which is the order they were written in.
func ordered(attrs []slog.Attr) []slog.Attr {
	first := make([]slog.Attr, 0, 2)
	rest := make([]slog.Attr, 0, len(attrs))
	for _, key := range []string{TraceIDKey, SpanIDKey} {
		for _, attr := range attrs {
			if attr.Key == key {
				first = append(first, attr)
			}
		}
	}

	for _, attr := range attrs {
		if attr.Key != TraceIDKey && attr.Key != SpanIDKey {
			rest = append(rest, attr)
		}
	}

	// Sorted rather than left as they were written, so that the same record
	// reads the same way every time and a person looking for one key knows
	// where to look. The trace is lifted out of that because it is what gets
	// copied when a record starts an investigation, and alphabetically it would
	// sit in the middle of the application's own keys.
	sort.Slice(rest, func(i, j int) bool { return rest[i].Key < rest[j].Key })
	return append(first, rest...)
}

// Names a key by the groups it is inside, the way the JSON handler nests it.
func qualify(groups []string, key string) string {
	if len(groups) == 0 {
		return key
	}
	return strings.Join(groups, ".") + "." + key
}

// Binds attributes, naming them by the groups open at the time: a group opened
// afterwards does not contain them, which is what slog's own handlers do and
// what makes a record here carry the same keys as the JSON of it.
func (h *humanHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	derived := *h
	derived.attrs = append(slices.Clone(h.attrs), flattened(h.groups, attrs)...)
	return &derived
}

func (h *humanHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	derived := *h
	derived.groups = append(slices.Clone(h.groups), name)
	return &derived
}

func levelLabel(level slog.Level) string {
	switch {
	case level < slog.LevelInfo:
		return "DEBUG"
	case level < slog.LevelWarn:
		return "INFO"
	case level < slog.LevelError:
		return "WARN"
	default:
		return "ERROR"
	}
}

// Quotes a value that would otherwise run into the next attribute,
// and leaves one that reads cleanly alone: a quoted handler name on every line
// is harder to read than an unquoted one.
func formatValue(value slog.Value) string {
	text := value.String()
	if text == "" || strings.ContainsAny(text, " \t\"=") {
		return fmt.Sprintf("%q", text)
	}
	return text
}
