package celerity

import "github.com/newstack-cloud/celerity-go-sdk/handler"

// Errors carrying a status, re-exported so an application deals with one
// package. A handler returning one of these is answered with that status; any
// other error is a fault, reported to the runtime and answered 500.
//
//	func getOrder(ctx context.Context, in GetOrder) (Order, error) {
//	    order, err := store.Get(ctx, in.OrderID)
//	    if errors.Is(err, sql.ErrNoRows) {
//	        return Order{}, celerity.NotFound("no such order")
//	    }
//	    return order, err
//	}
type (
	StatusError = handler.StatusError
	StatusCoder = handler.StatusCoder
)

var (
	Status         = handler.Status
	Statusf        = handler.Statusf
	WrapWithStatus = handler.Wrap

	BadRequest          = handler.BadRequest
	Unauthorized        = handler.Unauthorized
	Forbidden           = handler.Forbidden
	NotFound            = handler.NotFound
	Conflict            = handler.Conflict
	Gone                = handler.Gone
	UnprocessableEntity = handler.UnprocessableEntity
	TooManyRequests     = handler.TooManyRequests
	NotImplemented      = handler.NotImplemented
	ServiceUnavailable  = handler.ServiceUnavailable
)
