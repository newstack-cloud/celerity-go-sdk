package celerity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// The wrappers turn a typed handler into the untyped [handler.Func] the
// dispatcher and the adapters work in. Decoding is the framework's job so that
// a handler signature carries only what the handler is about.

func wrapHTTP(
	h func(context.Context, *handler.Request) (*handler.Response, error),
) handler.Func {
	return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
		res, err := h(ctx, ev.HTTP)
		if err != nil {
			return nil, err
		}
		return &handler.Result{ID: ev.ID, HTTP: res}, nil
	}
}

func wrapTypedHTTP[In, Out any](h HandlerFunc[In, Out]) handler.Func {
	return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
		var in In
		if err := bindRequest(ev.HTTP, &in); err != nil {
			return nil, err
		}

		out, err := h(ctx, in)
		if err != nil {
			return nil, err
		}

		res, err := jsonResponse(out)
		if err != nil {
			return nil, err
		}
		return &handler.Result{ID: ev.ID, HTTP: res}, nil
	}
}

func wrapTypedWebSocket[In, Out any](h HandlerFunc[In, Out]) handler.Func {
	return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
		var in In
		if err := decodeInto(ev.WebSocket.Message, &in); err != nil {
			return nil, err
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

func wrapCustom[In, Out any](h HandlerFunc[In, Out]) handler.Func {
	return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
		var in In
		if err := decodeInto(ev.Custom.Input, &in); err != nil {
			return nil, err
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
