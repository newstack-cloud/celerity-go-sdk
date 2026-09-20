package aws

import (
	"encoding/base64"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/aws/aws-lambda-go/events"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// ToRequest maps an API Gateway HTTP API v2 proxy event into an HTTP request.
func (m Mapper) ToRequest(payload []byte) (*handler.Request, error) {
	event, err := decode[events.APIGatewayV2HTTPRequest](payload, "API Gateway HTTP")
	if err != nil {
		return nil, err
	}

	route := routeOf(event.RouteKey, event.RawPath)
	return &handler.Request{
		Method:      strings.ToUpper(event.RequestContext.HTTP.Method),
		Path:        event.RawPath,
		Route:       route,
		PathParams:  pathParamsOf(event.PathParameters, route),
		QueryParams: queryParamsOf(event.RawQueryString, event.QueryStringParameters),
		Headers:     m.headersOf(event.Headers, event.Cookies),
		SourceIP:    event.RequestContext.HTTP.SourceIP,
		RequestID:   event.RequestContext.RequestID,
		Body:        bodyOf(event.Body, event.IsBase64Encoded),
	}, nil
}

// Returns the matched route template in the router's form.
//
// A route key is "{METHOD} {route}", where the route is stated in the
// blueprint's form, so /files/{path+} becomes /files/{*path} and a handler sees
// the same Route it would have under the Celerity runtime.
//
// $default names the fallback route, which has no template of its own. The raw
// path is the closest true answer as it is what was matched, if not the pattern
// that matched it.
func routeOf(routeKey, rawPath string) string {
	if routeKey == "" || routeKey == "$default" {
		return rawPath
	}

	route := routeKey
	if _, rest, found := strings.Cut(routeKey, " "); found {
		route = rest
	}

	return handler.NormaliseRoute(route)
}

// Maps the matched path parameters, splitting a catch-all into one
// value per segment.
//
// API Gateway gives a catch-all as one value with the separators still in it,
// where the Celerity runtime gives the segments. The split is what makes a
// handler binding []string see the same thing on both, and what stops a
// handler binding a string from being handed a/b/c where the runtime would
// have given it the same and a slice binding would have silently held one
// element.
//
// Segments are not decoded here, they arrive percent-encoded and stay that
// way, as [handler.Request.PathParams] specifies, so a segment containing an
// encoded separator survives the split intact.
func pathParamsOf(params map[string]string, route string) handler.Params {
	out := make(handler.Params, len(params))
	catchAll, hasCatchAll := handler.CatchAllParam(route)

	for name, value := range params {
		if hasCatchAll && name == catchAll {
			out[name] = strings.Split(value, "/")
			continue
		}
		out[name] = handler.Values{value}
	}

	return out
}

// Lowercases header names and folds the cookies the platform lists separately
// back into a cookie header.
//
// API Gateway joins repeated headers into one comma-separated value before the
// function sees them, and payload format 2.0 has no multi-value header field,
// so the division the client made is not recoverable from the event. A header
// on the split list is divided back up; every other header is carried as the
// one value it arrived as, because splitting it would invent a division that
// may never have been made. See [SplitHeadersEnvVar].
func (m Mapper) headersOf(headers map[string]string, cookies []string) handler.Params {
	splittable := m.splitHeaders
	if splittable == nil {
		splittable = defaultSplitNames
	}

	out := make(handler.Params, len(headers)+1)
	for name, value := range headers {
		lower := strings.ToLower(name)
		if splittable[lower] && strings.Contains(value, ",") {
			out[lower] = splitHeaderValue(value)
			continue
		}
		out[lower] = handler.Values{value}
	}
	if len(cookies) > 0 {
		// Never split: a cookie value is opaque and may contain anything, which
		// is why the platform keeps these out of the headers in the first place.
		out["cookie"] = handler.Values{strings.Join(cookies, "; ")}
	}
	return out
}

func splitHeaderValue(value string) handler.Values {
	parts := strings.Split(value, ",")
	out := make(handler.Values, 0, len(parts))
	for _, part := range parts {
		out = append(out, strings.TrimSpace(part))
	}
	return out
}

// queryParamsOf reads the query parameters from the raw query string.
//
// The raw string is what the client sent, so repetition survives exactly where
// ?include=lines&include=totals is two values, which is what the Celerity
// runtime gives a handler and what the queryStringParameters map cannot say,
// having already joined them with a comma that is indistinguishable from one
// the caller wrote.
//
// The map is read only when there is no raw string to read, which an event
// built by hand rather than by API Gateway can leave out.
func queryParamsOf(raw string, params map[string]string) handler.Params {
	if raw == "" {
		return singleValued(params)
	}

	// Malformed escapes are reported alongside whatever did parse. The pairs
	// that are well-formed are still the caller's, and dropping all of them
	// over one bad escape would lose more than it protects.
	values, _ := url.ParseQuery(raw)

	out := make(handler.Params, len(values))
	for name, v := range values {
		out[name] = handler.Values(v)
	}

	return out
}

// Carries the platform's one value per name, for an event that
// gave no raw query string to read instead.
func singleValued(params map[string]string) handler.Params {
	out := make(handler.Params, len(params))
	for name, value := range params {
		out[name] = handler.Values{value}
	}
	return out
}

func bodyOf(body string, isBase64 bool) []byte {
	if body == "" {
		return nil
	}
	if !isBase64 {
		return []byte(body)
	}
	decoded, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		// The platform said the body was encoded and it was not, so the bytes
		// as given are the only truthful answer available.
		return []byte(body)
	}
	return decoded
}

// FromResponse maps a handler's response into what API Gateway expects.
//
// A body that is not valid UTF-8 is base64-encoded and declared as such, which
// is the only way the platform carries bytes. The declaration is what stops API
// Gateway from delivering the base64 text itself to the client.
func (m Mapper) FromResponse(res *handler.Response) (any, error) {
	if res == nil {
		return events.APIGatewayV2HTTPResponse{StatusCode: 204}, nil
	}

	out := events.APIGatewayV2HTTPResponse{StatusCode: res.Status}
	out.Headers, out.Cookies = responseHeaders(res.Headers)

	if len(res.Body) > 0 {
		if utf8.Valid(res.Body) {
			out.Body = string(res.Body)
		} else {
			out.Body = base64.StdEncoding.EncodeToString(res.Body)
			out.IsBase64Encoded = true
		}
	}
	return out, nil
}

// responseHeaders splits the response's headers into the two things an HTTP API
// carries them as.
//
// Set-Cookie goes in Cookies, one entry per cookie: RFC 9110 forbids folding
// two Set-Cookie headers into one comma-separated value, and an HTTP API has no
// multi-value header field to put them in either. Every other name is joined,
// which is what the platform accepts.
func responseHeaders(headers handler.Params) (map[string]string, []string) {
	if len(headers) == 0 {
		return nil, nil
	}

	out := make(map[string]string, len(headers))
	var cookies []string
	for name, values := range headers {
		if strings.EqualFold(name, "set-cookie") {
			cookies = append(cookies, values...)
			continue
		}
		out[name] = strings.Join(values, ", ")
	}
	return out, cookies
}
