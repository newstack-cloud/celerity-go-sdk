package ipc

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/layer"
)

// Config is what a client needs in order to serve a handler process.
type Config struct {
	// Tags are the handler tags this process serves, declared in the handshake
	// and checked against the blueprint by the runtime.
	Tags []string
	// Reconcile is given what the blueprint declares, before this process
	// declares itself, and returns the tags to declare.
	//
	// It is how a handler whose tag is not knowable from code alone settles it:
	// a WebSocket route keyed by something the API states, or a handler that
	// stated only its name and takes its routing from the blueprint. Returning
	// an error fails the handshake, which is the right outcome for a handler
	// that could never be dispatched to. Optional.
	Reconcile func(blueprint []HandlerConfig) ([]string, error)
	// Resolve returns the pipeline for a tag.
	Resolve func(tag string) (layer.Next, bool)
	// Concurrency is the worker pool size, and the initial credit.
	Concurrency int
	// Limits caps individual tags within the credit window, so one slow handler
	// cannot consume the whole of it.
	//
	// A function rather than a map because it is read after Reconcile which can
	// change a tag to the one the blueprint declares, and a cap keyed by the
	// tag built beforehand would name a handler the runtime does not know.
	// Optional.
	Limits     func() map[string]int
	SDKVersion string
}

// ProtocolVersionError reports a handshake refused because the contract this
// SDK speaks is not one the runtime serves.
//
// Nothing about the application causes it, so nothing about the application
// fixes it, a rebuild is required against an SDK whose contract the runtime serves.
type ProtocolVersionError struct {
	// Declared is what this SDK was built against.
	Declared ProtocolVersion
	// Served is what the runtime sent with its configuration, which is the
	// zero value if it sent none.
	Served ProtocolVersion
}

func (e *ProtocolVersionError) Error() string {
	return fmt.Sprintf(
		"this SDK speaks IPC protocol %s and the runtime serves %s, rebuild against an SDK the runtime serves",
		e.Declared, e.Served,
	)
}

// TagMismatchError reports a handshake refused because the handlers registered
// in code do not match the ones the blueprint declares.
//
// It is a startup error rather than a 404 in production, which is the whole
// point of the check.
type TagMismatchError struct {
	Unknown   []string
	Unhandled []string
}

func (e *TagMismatchError) Error() string {
	var b strings.Builder
	b.WriteString("handler registration does not match the blueprint")
	if len(e.Unknown) > 0 {
		b.WriteString("\n  registered in code but not declared by the blueprint: ")
		b.WriteString(strings.Join(e.Unknown, ", "))
	}
	if len(e.Unhandled) > 0 {
		b.WriteString("\n  declared by the blueprint but not registered in code: ")
		b.WriteString(strings.Join(e.Unhandled, ", "))
	}
	return b.String()
}

// Client serves one handler process against the runtime.
type Client struct {
	config    Config
	transport Transport

	mu       sync.Mutex
	inFlight map[string]context.CancelFunc

	out       chan *ToRuntime
	wsMu      sync.Mutex
	wsWaiting map[string]chan *WsSendAck
}

// New creates a client over a transport.
func New(transport Transport, config Config) *Client {
	if config.Concurrency <= 0 {
		config.Concurrency = 1
	}
	return &Client{
		config:    config,
		transport: transport,
		inFlight:  make(map[string]context.CancelFunc),
		out:       make(chan *ToRuntime, config.Concurrency*2),
		wsWaiting: make(map[string]chan *WsSendAck),
	}
}

// Serve runs the handshake and then the frame loop, returning when the stream
// closes or the context is cancelled.
func (c *Client) Serve(ctx context.Context) error {
	config, err := c.awaitConfig()
	if err != nil {
		return err
	}

	if c.config.Reconcile != nil {
		tags, err := c.config.Reconcile(config.Handlers)
		if err != nil {
			return err
		}
		c.config.Tags = tags
	}

	if err := c.declareReady(config); err != nil {
		return err
	}

	return c.loop(ctx)
}

// awaitConfig waits for the RuntimeConfig the runtime sends first, before the
// handler declares itself, which is what lets the SDK compare code against
// blueprint.
func (c *Client) awaitConfig() (*RuntimeConfig, error) {
	frame, err := c.transport.Recv()
	if err != nil {
		return nil, fmt.Errorf("waiting for runtime configuration: %w", err)
	}
	if frame.Config == nil {
		return nil, errors.New("runtime sent a frame before its configuration")
	}
	return frame.Config, nil
}

func (c *Client) declareReady(config *RuntimeConfig) error {
	tags := append([]string(nil), c.config.Tags...)
	sort.Strings(tags)

	var limits map[string]int
	if c.config.Limits != nil {
		limits = c.config.Limits()
	}

	ready := &Ready{
		HandlerTags:   tags,
		InitialCredit: uint32(c.config.Concurrency),
		SDKVersion:    c.config.SDKVersion,
		Limits:        limitsFrom(limits),
		Protocol:      ProtocolVersion{Major: ProtocolMajor, Minor: ProtocolMinor},
	}
	if err := c.transport.Send(&ToRuntime{Ready: ready}); err != nil {
		return fmt.Errorf("declaring handlers: %w", err)
	}

	frame, err := c.transport.Recv()
	if err != nil {
		return fmt.Errorf("waiting for the runtime to accept this handler: %w", err)
	}
	if frame.ReadyAck == nil {
		return errors.New("runtime sent a frame other than an acknowledgement of readiness")
	}
	if !frame.ReadyAck.Accepted {
		if frame.ReadyAck.Reason == RefusedProtocolVersion {
			return &ProtocolVersionError{
				Declared: ready.Protocol,
				Served:   config.Protocol,
			}
		}
		return &TagMismatchError{
			Unknown:   frame.ReadyAck.UnknownTags,
			Unhandled: frame.ReadyAck.UnhandledTags,
		}
	}
	return nil
}

func limitsFrom(limits map[string]int) []HandlerLimit {
	if len(limits) == 0 {
		return nil
	}
	out := make([]HandlerLimit, 0, len(limits))
	for tag, max := range limits {
		out = append(out, HandlerLimit{HandlerTag: tag, MaxConcurrent: uint32(max)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].HandlerTag < out[j].HandlerTag })
	return out
}

// cancel cancels an in-flight event.
//
// A cancellation may name an event that has already completed, which is
// ordinary rather than an error: the runtime does not wait to find out whether
// the handler finished before deciding nobody is waiting for the result.
func (c *Client) cancel(id string) {
	c.mu.Lock()
	stop, ok := c.inFlight[id]
	delete(c.inFlight, id)
	c.mu.Unlock()

	if ok {
		stop()
	}
}

func (c *Client) track(id string, stop context.CancelFunc) {
	c.mu.Lock()
	c.inFlight[id] = stop
	c.mu.Unlock()
}

func (c *Client) untrack(id string) {
	c.mu.Lock()
	delete(c.inFlight, id)
	c.mu.Unlock()
}

// resultFor runs one dispatch and always produces a result carrying a credit
// grant.
//
// The grant is emitted by the same defer that recovers a panic. A result that
// does not carry one permanently shrinks the credit window, and enough of them
// stall the stream with no error reported anywhere, so the grant cannot be left
// to the happy path.
func (c *Client) resultFor(ctx context.Context, ev *handler.Event) (res *handler.Result) {
	res = &handler.Result{ID: ev.ID}

	defer func() {
		if r := recover(); r != nil {
			res = &handler.Result{ID: ev.ID, Error: errorFrom(r)}
		}
	}()

	invoke, ok := c.config.Resolve(ev.Tag)
	if !ok {
		return &handler.Result{ID: ev.ID, Error: &handler.Error{
			Message: fmt.Sprintf("no handler registered for tag %q", ev.Tag),
			Type:    "UnroutableEvent",
		}}
	}

	out, err := invoke(ctx, ev)
	if err != nil {
		return &handler.Result{ID: ev.ID, Error: errorFrom(err)}
	}
	return out
}

func errorFrom(v any) *handler.Error {
	switch e := v.(type) {
	case *handler.Error:
		return e
	case error:
		return &handler.Error{Message: e.Error(), Type: fmt.Sprintf("%T", e)}
	default:
		return &handler.Error{Message: fmt.Sprint(v), Type: "panic"}
	}
}
