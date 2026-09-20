package aws

import (
	"context"
	"fmt"
	"sync"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// A service that can send messages to an AWS API Gateway v2 WebSocket
// connection vie the API Gateway Management API.
type postToConnection interface {
	PostToConnection(
		ctx context.Context,
		in *apigatewaymanagementapi.PostToConnectionInput,
		opts ...func(*apigatewaymanagementapi.Options),
	) (*apigatewaymanagementapi.PostToConnectionOutput, error)
}

// Pushes messages to connected clients through the API Gateway
// Management API.
//
// It implements [handler.WebSocketSender] and deliberately not
// [handler.BinarySender]: API Gateway WebSocket APIs carry text frames only and
// disconnect a client that sends a binary one, so an application asking whether
// the capability is there is told no rather than having a send fail.
//
// The acknowledgement fields on [handler.OutboundMessage] are ignored.
// API Gateway reports whether it accepted a message and nothing about what the
// client made of it, so there is nothing to wait for and no loss event to
// raise; a message asking for a client acknowledgement still gets one, since
// the client answers over its own connection and that arrives as an ordinary
// message.
type sender struct {
	endpoint string

	once   sync.Once
	client postToConnection
	err    error
}

func newSender(endpoint string) *sender { return &sender{endpoint: endpoint} }

// Send delivers every message and reports exactly which ones failed.
//
// Messages go out concurrently. Each is a separate call to the platform, one
// client's unreachable connection should not hold up another's, and a handler
// fanning a message out to a room is the ordinary case.
func (s *sender) Send(ctx context.Context, messages ...handler.OutboundMessage) error {
	if len(messages) == 0 {
		return nil
	}

	client, err := s.resolveClient(ctx)
	if err != nil {
		return err
	}

	failures := make([]*handler.SendFailure, len(messages))
	var wg sync.WaitGroup

	for i, message := range messages {
		wg.Go(func() {
			if err := post(ctx, client, message); err != nil {
				failures[i] = &handler.SendFailure{
					Index:        i,
					ConnectionID: message.ConnectionID,
					Error:        err.Error(),
				}
			}
		})
	}
	wg.Wait()

	return sendError(failures)
}

func post(ctx context.Context, client postToConnection, message handler.OutboundMessage) error {
	_, err := client.PostToConnection(ctx, &apigatewaymanagementapi.PostToConnectionInput{
		ConnectionId: &message.ConnectionID,
		Data:         message.Message,
	})
	return err
}

// Collects the failures into one error, keeping the indexes they were
// sent under so a caller can retry exactly those.
func sendError(failures []*handler.SendFailure) error {
	collected := make([]handler.SendFailure, 0, len(failures))
	for _, failure := range failures {
		if failure != nil {
			collected = append(collected, *failure)
		}
	}

	if len(collected) == 0 {
		return nil
	}

	return &handler.SendError{Failures: collected}
}

// Builds the client once and reuses it across invocations.
//
// Building it reads the environment and may reach the instance metadata service
// for credentials, which is work that belongs to the first send rather than to
// every one.
func (s *sender) resolveClient(ctx context.Context) (postToConnection, error) {
	s.once.Do(func() {
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			s.err = fmt.Errorf("loading AWS configuration to push to a websocket connection: %w", err)
			return
		}
		s.client = apigatewaymanagementapi.NewFromConfig(cfg, func(o *apigatewaymanagementapi.Options) {
			o.BaseEndpoint = &s.endpoint
		})
	})
	return s.client, s.err
}
