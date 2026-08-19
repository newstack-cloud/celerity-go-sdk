// Command testapp is the handlers executable the integration suite exercises.
//
// The runtime launches it as a separate process and talks to it over the IPC
// stream, so this is an ordinary Celerity application: what it registers has to
// match the blueprint the runtime is serving, or the handshake fails at
// startup, which is one of the things the suite checks.
package main

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
)

type order struct {
	ID     string `json:"id"      path:"orderId"`
	Status string `json:"status"`
}

type createOrder struct {
	CustomerID string `json:"customerId"`
}

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
	app := celerity.New(celerity.WithConcurrency(4))

	celerity.Get(app, "/orders/{orderId}", getOrder, celerity.Named("getOrderHandler"))
	celerity.Post(app, "/orders", createOrderHandler, celerity.Named("createOrderHandler"))
	celerity.Get(app, "/files/{filePath+}", getFile, celerity.Named("getFileHandler"))

	celerity.Run(app)
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
