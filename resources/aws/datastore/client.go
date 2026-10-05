package datastore

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// Linking this package is what lets an application reach a data store on AWS.
// `celerity-go generate` writes the import when the blueprint declares one.
func init() {
	service.RegisterDatastore(func(s *service.Session) service.Builder[datastore.Client] {
		return newStores(s).build
	})
}

// API is what this package calls on DynamoDB.
//
// This is narrow on purpose. A service client has hundreds of methods and this package
// uses a handful, so the interface is also the list of what a handler reaching a
// data store needs permission to do.
type API interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	BatchGetItem(
		context.Context, *dynamodb.BatchGetItemInput, ...func(*dynamodb.Options),
	) (*dynamodb.BatchGetItemOutput, error)
	BatchWriteItem(
		context.Context, *dynamodb.BatchWriteItemInput, ...func(*dynamodb.Options),
	) (*dynamodb.BatchWriteItemOutput, error)
	UpdateItem(
		context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options),
	) (*dynamodb.UpdateItemOutput, error)
	TransactWriteItems(
		context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options),
	) (*dynamodb.TransactWriteItemsOutput, error)
	DescribeTable(context.Context, *dynamodb.DescribeTableInput, ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error)
}

// stores builds data store handles for one provider, sharing a DynamoDB client
// per region between them.
type stores struct {
	session *service.Session
	clients service.Clients[API]

	// api is a test's stand-in, and nil everywhere else.
	api API
}

func newStores(s *service.Session) *stores {
	return &stores{session: s}
}

func (s *stores) build(ref resources.Ref) (datastore.Client, error) {
	return &dynamoStore{stores: s, ref: ref}, nil
}

func (s *stores) dynamo(ctx context.Context, key service.ClientKey) (API, error) {
	if s.api != nil {
		return s.api, nil
	}

	return s.clients.Get(ctx, key, func() (API, error) {
		cfg, err := s.session.ConfigFor(ctx, key.Region)
		if err != nil {
			return nil, err
		}

		return dynamodb.NewFromConfig(cfg), nil
	})
}
