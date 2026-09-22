// Command testapp is the handlers executable the integration suite exercises.
//
// The runtime launches it as a separate process and talks to it over the IPC
// stream, so this is an ordinary Celerity application: what it registers has to
// match the blueprint the runtime is serving, or the handshake fails at
// startup, which is one of the things the suite checks.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"regexp"
	"sync"

	"github.com/go-playground/validator/v10"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/validation/playground"
)

type order struct {
	ID     string `json:"id"      path:"orderId"`
	Status string `json:"status"`
}

// createOrder carries the validation rules the way a template project does:
// struct tags read by go-playground/validator, reported to the caller as
// structured detail.
type createOrder struct {
	CustomerID string  `json:"customerId" validate:"required"`
	Total      float64 `json:"total"      validate:"gt=0"`
	Email      string  `json:"email"      validate:"omitempty,email"`
	// A rule of this application's own, registered below.
	SKU string `json:"sku" validate:"omitempty,sku"`
}

var skuPattern = regexp.MustCompile(`^[A-Z]{2}-\d{4}$`)

type orderCreated struct {
	ID         string `json:"id"`
	CustomerID string `json:"customerId"`
	Status     string `json:"status"`
}

type filePath struct {
	// The blueprint declares /files/{filePath+}, which reaches a handler as the
	// router's {*filePath}, one value per path segment. Bound to a string the
	// segments are rejoined; bound to a slice they arrive as they were split,
	// and both are exercised here.
	Path     string   `json:"path"     path:"filePath"`
	Segments []string `json:"segments" path:"filePath"`
}

func main() {
	// go-playground/validator is the standard choice for Celerity Go
	// applications, wired the way a template project wires it.
	app := celerity.New(celerity.WithValidator(playground.New(
		// A custom rule and what a caller who breaks it is told, registered
		// together so the rule cannot ship without an explanation.
		playground.WithRule("sku", func(fl validator.FieldLevel) bool {
			return skuPattern.MatchString(fl.Field().String())
		}, "must be a SKU, such as AB-1234"),
	)))

	celerity.Get(app, "/orders/{orderId}", getOrder, celerity.Named("getOrderHandler"))
	celerity.Post(app, "/orders", createOrderHandler, celerity.Named("createOrderHandler"))
	celerity.Get(app, "/files/{filePath+}", getFile, celerity.Named("getFileHandler"))
	celerity.Get(app, "/orders/{orderId}/receipt", getReceipt, celerity.Named("getReceiptHandler"))

	// Wiring from the blueprint: no method, no path, just the name. The options
	// the blueprint knows nothing about are still stated here.
	celerity.Handler(app, "archiveOrderHandler", archiveOrder, celerity.MaxConcurrent(2))

	// The route is stated here; the route key comes from the blueprint, which
	// the SDK reconciles against before declaring what it serves.
	celerity.OnConnect(app, wsConnect, celerity.Named("wsConnectHandler"))
	celerity.OnMessage(app, "sendMessage", wsSendMessage, celerity.Named("wsSendMessageHandler"))
	celerity.OnDisconnect(app, wsDisconnect, celerity.Named("wsDisconnectHandler"))

	// Nothing triggers a custom handler, it is reached by name through the
	// runtime's local invoke endpoint or from another handler. The name is the
	// blueprint's resource name, which is what the runtime builds its tag from.
	celerity.Invoke(app, "recalculatePricingHandler", recalculatePricing)

	// A batch handler and a scheduled one. Both are keyed by the source id the
	// blueprint gave them, and neither answers a caller, so what they receive is
	// reported over HTTP instead.
	celerity.Consume(app, "ordersQueue", processOrders, celerity.Named("processOrdersHandler"))
	celerity.Schedule(app, "nightlyReport", buildReport, celerity.Named("buildReportHandler"))
	celerity.Get(app, "/processed", processed, celerity.Named("processedHandler"))

	celerity.Run(app)
}

// seen records what the handlers with no response of their own received, so the
// suite can assert on them over HTTP.
//
// Guarded because a consumer batch and a request are handled on different
// goroutines, and the runtime may dispatch several batches at once.
var seen = struct {
	mu        sync.Mutex
	orders    []string
	schedules []string
	// A count of the times the handler determines processing has failed
	// where the message should be left on its source and delivered again.
	poisonAttempts map[string]int
}{poisonAttempts: map[string]int{}}

type processedReport struct {
	Orders         []string       `json:"orders"`
	Schedules      []string       `json:"schedules"`
	PoisonAttempts map[string]int `json:"poisonAttempts"`
}

// Takes the whole batch and fails only the records it could not
// handle, so one bad message is redriven alone rather than taking the good ones
// with it.
func processOrders(_ context.Context, batch *handler.ConsumerBatch) (*handler.BatchResult, error) {
	res := &handler.BatchResult{}

	seen.mu.Lock()
	defer seen.mu.Unlock()
	for _, record := range batch.Records {
		var order struct {
			OrderID string `json:"orderId"`
			Poison  bool   `json:"poison"`
		}
		if err := json.Unmarshal(record.Body, &order); err != nil {
			res.Fail(record.MessageID, err)
			continue
		}
		// A record the application decides it cannot handle, so the suite can
		// see that naming one leaves it behind and acknowledges the rest.
		if order.Poison {
			seen.poisonAttempts[order.OrderID]++
			res.Fail(record.MessageID, errors.New("this order cannot be processed"))
			continue
		}
		seen.orders = append(seen.orders, order.OrderID)
	}
	return res, nil
}

func buildReport(_ context.Context, trigger *handler.ScheduleTrigger) error {
	seen.mu.Lock()
	defer seen.mu.Unlock()
	seen.schedules = append(seen.schedules, trigger.ScheduleID)
	return nil
}

func processed(_ context.Context, _ struct{}) (processedReport, error) {
	seen.mu.Lock()
	defer seen.mu.Unlock()
	attempts := make(map[string]int, len(seen.poisonAttempts))
	maps.Copy(attempts, seen.poisonAttempts)
	return processedReport{
		Orders:         append([]string(nil), seen.orders...),
		Schedules:      append([]string(nil), seen.schedules...),
		PoisonAttempts: attempts,
	}, nil
}

// pricingRequest is validated exactly as an HTTP handler's input is, so a
// custom invocation with a bad payload is answered with the same structured
// detail rather than reaching the handler.
type pricingRequest struct {
	OrderID  string  `json:"orderId"  validate:"required"`
	Discount float64 `json:"discount" validate:"gte=0,lte=100"`
}

type pricingResult struct {
	OrderID string  `json:"orderId"`
	Total   float64 `json:"total"`
}

func recalculatePricing(_ context.Context, in pricingRequest) (pricingResult, error) {
	return pricingResult{
		OrderID: in.OrderID,
		Total:   100 - in.Discount,
	}, nil
}

func getOrder(_ context.Context, in order) (order, error) {
	return order{ID: in.ID, Status: "found"}, nil
}

func createOrderHandler(_ context.Context, in createOrder) (orderCreated, error) {
	return orderCreated{
		ID:         "new-" + in.CustomerID,
		CustomerID: in.CustomerID,
		Status:     "created",
	}, nil
}

func getFile(_ context.Context, in filePath) (filePath, error) {
	return in, nil
}

type chatMessage struct {
	Event string `json:"event"`
	Text  string `json:"text"`
}

func wsConnect(_ context.Context, _ map[string]any) (map[string]any, error) {
	return nil, nil
}

func wsDisconnect(_ context.Context, _ map[string]any) (map[string]any, error) {
	return nil, nil
}

// wsSendMessage answers on the side channel rather than by returning a value: a
// WebSocket message is acknowledged, not replied to, so anything the client
// should see has to be pushed back to its connection.
func wsSendMessage(ctx context.Context, in chatMessage) (map[string]any, error) {
	sender, ok := celerity.WebSocketSenderFrom(ctx)
	if !ok {
		return nil, errors.New("no websocket sender in the handler's context")
	}

	message, err := json.Marshal(map[string]string{
		"event": "messageReceived",
		"echo":  in.Text,
	})
	if err != nil {
		return nil, err
	}

	return nil, sender.Send(ctx, handler.OutboundMessage{
		ConnectionID: celerity.ConnectionID(ctx),
		Message:      message,
	})
}

// getReceipt answers with a status-carrying error, so the suite can check the
// status survives the runtime rather than only the pipeline.
func getReceipt(_ context.Context, in order) (order, error) {
	if in.ID != "known" {
		return order{}, celerity.NotFound("no receipt for that order")
	}
	return order{ID: in.ID, Status: "receipted"}, nil
}

type archiveRequest struct {
	OrderID string `json:"orderId" path:"orderId"`
}

type archived struct {
	OrderID string `json:"orderId"`
	Status  string `json:"status"`
	// Route is what the runtime dispatched this under, so the suite can see the
	// blueprint's routing reached the handler.
	Route string `json:"route"`
}

// archiveOrder is routed by the blueprint rather than by code.
func archiveOrder(ctx context.Context, in archiveRequest) (archived, error) {
	route := ""
	if ev, ok := celerity.EventFrom(ctx); ok && ev.HTTP != nil {
		route = ev.HTTP.Route
	}
	return archived{OrderID: in.OrderID, Status: "archived", Route: route}, nil
}
