package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// SenderWith builds a sender that posts through the given function rather than
// through AWS, so that what a send does with a partial failure is testable
// without a gateway.
//
// Only the one call the sender makes is stood in for: everything decided about
// a batch is decided on this side of it.
func SenderWith(post func(connectionID string, data []byte) error) handler.WebSocketSender {
	s := newSender("https://example.invalid/test")
	s.client = postFunc(post)
	// Marked resolved so the sender does not go looking for AWS credentials.
	s.once.Do(func() {})
	return s
}

type postFunc func(connectionID string, data []byte) error

func (f postFunc) PostToConnection(
	_ context.Context,
	in *apigatewaymanagementapi.PostToConnectionInput,
	_ ...func(*apigatewaymanagementapi.Options),
) (*apigatewaymanagementapi.PostToConnectionOutput, error) {
	if err := f(*in.ConnectionId, in.Data); err != nil {
		return nil, err
	}
	return &apigatewaymanagementapi.PostToConnectionOutput{}, nil
}
