// Package aws implements the Celerity resource interfaces against AWS.
//
// It is selected by importing it:
//
//	import _ "github.com/newstack-cloud/celerity-go-sdk/resources/aws"
//
// An init registers the provider, so resource handles taken through the
// resources package are built by it without an application passing it anywhere.
//
// It provides a resources.Provider building S3 buckets, SQS queues, SNS topics,
// DynamoDB data stores, ElastiCache caches and RDS databases, so that handler
// code deals only in the provider-agnostic interfaces from the resources
// package.
//
// It is a separate module so that an application which never touches AWS does
// not compile aws-sdk-go-v2 into its binary, which on Lambda is a cold start
// cost rather than only a disk one.
package aws
