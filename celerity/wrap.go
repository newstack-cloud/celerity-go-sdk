package celerity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// Answers an error from an HTTP handler.
//
// An error carrying a status is an answer the handler chose, so it becomes a
// response. Anything else is a fault nobody handled, and is returned as an
// error so the runtime reports it and answers 500 itself.
func httpErrorResult(ev *handler.Event, err error) (*handler.Result, error) {
	if res, ok := handler.ResponseForError(err); ok {
		return &handler.Result{ID: ev.ID, HTTP: res}, nil
	}
	return nil, err
}

// The wrappers turn a typed handler into the untyped [handler.Func] the
// dispatcher and the adapters work in. Decoding is the framework's job so that
// a handler signature carries only what the handler is about.

func wrapHTTP(
	h func(context.Context, *handler.Request) (*handler.Response, error),
) handler.Func {
	return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
		res, err := h(ctx, ev.HTTP)
		if err != nil {
			return httpErrorResult(ev, err)
		}
		return &handler.Result{ID: ev.ID, HTTP: res}, nil
	}
}

func wrapTypedHTTP[In, Out any](validate func(any) error, h HandlerFunc[In, Out]) handler.Func {
	return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
		var in In
		if err := bindRequest(ev.HTTP, &in); err != nil {
			// The request is malformed, which is the caller's mistake and a 400
			// rather than the 500 an unhandled error would produce. The issues
			// name the field without naming the Go type it failed to become.
			return httpErrorResult(ev, decodeFailure(err))
		}

		if validate != nil {
			if err := validate(in); err != nil {
				return httpErrorResult(ev, err)
			}
		}

		out, err := h(ctx, in)
		if err != nil {
			return httpErrorResult(ev, err)
		}

		res, err := jsonResponse(out)
		if err != nil {
			return nil, err
		}
		return &handler.Result{ID: ev.ID, HTTP: res}, nil
	}
}

func wrapTypedWebSocket[In, Out any](validate func(any) error, h HandlerFunc[In, Out]) handler.Func {
	return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
		var in In
		if err := decodeInto(ev.WebSocket.Message, &in); err != nil {
			return nil, err
		}
		// A WebSocket message is acknowledged rather than answered, so a
		// rejection is reported on the acknowledgement, there is no response for
		// a body to travel in.
		if validate != nil {
			if err := validate(in); err != nil {
				return &handler.Result{ID: ev.ID, WebSocket: &handler.Ack{
					Success: false,
					Error:   err.Error(),
				}}, nil
			}
		}
		if _, err := h(ctx, in); err != nil {
			return nil, err
		}
		return &handler.Result{ID: ev.ID, WebSocket: &handler.Ack{Success: true}}, nil
	}
}

func wrapConsumer(
	h func(context.Context, *handler.ConsumerBatch) (*handler.BatchResult, error),
) handler.Func {
	return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
		res, err := h(ctx, ev.Consumer)
		if err != nil {
			return nil, err
		}
		if res == nil {
			res = &handler.BatchResult{}
		}
		return &handler.Result{ID: ev.ID, Consumer: res}, nil
	}
}

func wrapSchedule(h func(context.Context, *handler.ScheduleTrigger) error) handler.Func {
	return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
		if err := h(ctx, ev.Schedule); err != nil {
			return nil, err
		}
		return &handler.Result{ID: ev.ID, Schedule: &handler.Ack{Success: true}}, nil
	}
}

func wrapCustom[In, Out any](validate func(any) error, h HandlerFunc[In, Out]) handler.Func {
	return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
		var in In
		if err := decodeInto(ev.Custom.Input, &in); err != nil {
			return nil, err
		}
		if validate != nil {
			if err := validate(in); err != nil {
				return nil, err
			}
		}

		out, err := h(ctx, in)
		if err != nil {
			return nil, err
		}

		encoded, err := json.Marshal(out)
		if err != nil {
			return nil, fmt.Errorf("encoding custom handler output: %w", err)
		}
		return &handler.Result{
			ID:     ev.ID,
			Custom: &handler.CustomInvokeResult{Output: encoded},
		}, nil
	}
}

// decodeInto decodes a body into a typed value, treating an empty body as an
// absent one rather than as malformed, since a GET or a disconnect carries none.
func decodeInto(body []byte, out any) error {
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decoding request body: %w", err)
	}
	return nil
}

// jsonResponse encodes a handler's return value.
//
// A handler returning a *handler.Response takes control of the status and
// headers; anything else becomes a 200 with a JSON body.
func jsonResponse(out any) (*handler.Response, error) {
	if res, ok := out.(*handler.Response); ok {
		return res, nil
	}

	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encoding response body: %w", err)
	}
	return &handler.Response{
		Status:  200,
		Headers: handler.Params{"content-type": {"application/json"}},
		Body:    body,
	}, nil
}

func shortFuncName(full string) string {
	if full == "" {
		return ""
	}
	// "github.com/acme/app/orders.Create.func1" -> "Create"
	if i := strings.LastIndex(full, "/"); i >= 0 {
		full = full[i+1:]
	}
	parts := strings.Split(full, ".")
	for _, part := range parts[1:] {
		if !strings.HasPrefix(part, "func") {
			return part
		}
	}
	return parts[len(parts)-1]
}

func guardRedefinedError(name, file string, line int) error {
	return fmt.Errorf("guard %q is already registered, and %s:%d registers it again", name, file, line)
}

// Turns a body that could not be read into a 400 carrying issues.
//
// The decoder's own message names Go types and struct fields, so the field is
// kept and the rest is replaced with something a caller can act on.
func decodeFailure(err error) error {
	return &handler.ValidationError{
		Message: "the request body could not be read",
		Issues:  handler.IssuesFromDecode(err),
		Err:     err,
	}
}

// Serves a handler whose event source is not known until the
// blueprint has been read.
//
// The typed wrappers are chosen at registration, which a handler that states
// only its name cannot be. Its tag, and therefore, its source, comes from the
// blueprint. This reads the source from the event instead, and is otherwise the
// same work the per-source wrappers do.
func wrapTypedAny[In, Out any](validate func(any) error, h HandlerFunc[In, Out]) handler.Func {
	http := wrapTypedHTTP(validate, h)
	websocket := wrapTypedWebSocket(validate, h)
	custom := wrapCustom(validate, h)

	return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
		switch ev.Kind {
		case handler.KindHTTP:
			return http(ctx, ev)
		case handler.KindWebSocket:
			return websocket(ctx, ev)
		case handler.KindCustom:
			return custom(ctx, ev)
		default:
			// Consumer and schedule handlers take a batch and a trigger rather
			// than a decoded value, so they are registered with Consume and
			// Schedule. Reaching here means the blueprint bound this name to
			// one of those, which is a mismatch worth naming rather than a
			// batch quietly handled as though it were one message.
			return nil, fmt.Errorf(
				"handler %q takes a decoded input, and the blueprint binds it to a %s source: "+
					"register it with Consume or Schedule instead",
				ev.Tag, ev.Kind,
			)
		}
	}
}
