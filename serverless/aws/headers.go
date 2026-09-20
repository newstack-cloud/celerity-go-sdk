package aws

import (
	"strings"
)

// SplitHeadersEnvVar names further headers to divide back into the values the
// client sent, as a comma-separated list of header names.
//
//	CELERITY_SPLIT_HEADERS=x-tags,x-forwarded-host
//
// This is additive, the names given here are divided in addition to [SplitHeaders],
// never instead of them. The default list holds only headers that cannot
// contain a comma except as a separator, so there is nothing in it a deployment
// would need to take out.
//
// Read once, when the adapter is built at process start. See [Mapper].
const SplitHeadersEnvVar = "CELERITY_SPLIT_HEADERS"

// SplitHeaders are the headers divided at commas by default.
//
// API Gateway joins repeated headers with a comma and payload format 2.0 has no
// multi-value header field, so a header that arrives with several values cannot
// be told from one value that contains a comma. Splitting is therefore a guess,
// and this list holds only the headers where the guess cannot be wrong as their
// grammar is a list of tokens, and a token may not contain a comma, so every
// comma in the value is a separator.
//
// Two headers here are a deliberate, narrower judgement rather than a
// guarantee. A media-type parameter, a cache directive value and an entity tag
// may each be a quoted string, and a quoted string may contain a comma, so
// accept, content-type, cache-control, if-match and if-none-match can in
// principle be divided wrongly. They are included because they are the headers
// clients actually repeat.
//
// Deliberately absent, and available through [SplitHeadersEnvVar] for a
// deployment that knows its own clients:
//
//   - date, expires, if-modified-since, if-unmodified-since, last-modified and
//     retry-after, which carry an HTTP-date, and an HTTP-date contains a comma
//     after the day name: splitting "Mon, 19 Sep 2026 10:00:00 GMT" gives a
//     handler "Mon". These are not judgement calls, they are wrong every time,
//     and they appear on every conditional request.
//   - user-agent, which is free-form
var SplitHeaders = []string{
	"accept",
	"accept-charset",
	"accept-encoding",
	"accept-language",
	"access-control-request-headers",
	"cache-control",
	"connection",
	"content-encoding",
	"content-language",
	"content-type",
	"if-match",
	"if-none-match",
	"te",
	"trailer",
	"transfer-encoding",
	"upgrade",
	"vary",
	"via",
	"x-forwarded-for",
}

// What a mapper that captured no configuration divides.
var defaultSplitNames = buildSplitNames("")

func buildSplitNames(configured string) map[string]bool {
	names := make(map[string]bool, len(SplitHeaders)+4)
	for _, name := range SplitHeaders {
		names[name] = true
	}

	for name := range strings.SplitSeq(configured, ",") {
		if trimmed := strings.ToLower(strings.TrimSpace(name)); trimmed != "" {
			names[trimmed] = true
		}
	}

	return names
}
