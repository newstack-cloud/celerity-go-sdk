module github.com/newstack-cloud/celerity-go-sdk/resources/aws

go 1.26.0

// Core is not published yet, so the module is resolved from the repository.
// Dropped when core cuts its first release, after which this module requires a
// version like any other consumer does.
replace github.com/newstack-cloud/celerity-go-sdk => ../..

// The cache is one implementation for every platform, and this module supplies
// only the credentials it reaches a managed cache with, so it depends on the
// cache module rather than the other way round. Not published yet either, so
// resolved from the repository the same way core is.
replace github.com/newstack-cloud/celerity-go-sdk/resources/redis => ../redis

require (
	github.com/aws/aws-sdk-go-v2 v1.47.1
	github.com/aws/aws-sdk-go-v2/config v1.33.5
	github.com/aws/aws-sdk-go-v2/credentials v1.20.5
	github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue v1.21.7
	github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression v1.9.7
	github.com/aws/aws-sdk-go-v2/feature/rds/auth v1.7.3
	github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager v0.4.8
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.69.1
	github.com/aws/aws-sdk-go-v2/service/s3 v1.113.2
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.50.0
	github.com/aws/aws-sdk-go-v2/service/sns v1.47.1
	github.com/aws/aws-sdk-go-v2/service/sqs v1.52.0
	github.com/aws/smithy-go v1.28.1
	github.com/go-sql-driver/mysql v1.10.1
	github.com/jackc/pgx/v5 v5.11.0
	github.com/newstack-cloud/celerity-go-sdk v0.0.0
	github.com/newstack-cloud/celerity-go-sdk/resources/redis v0.0.0-00010101000000-000000000000
	github.com/stretchr/testify v1.12.1
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.20 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.20.0 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.4 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.4 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodbstreams v1.43.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.19 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.11.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.13.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.20.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.10.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.38.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.43.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.51.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/redis/go-redis/v9 v9.22.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/grpc v1.83.1 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)
