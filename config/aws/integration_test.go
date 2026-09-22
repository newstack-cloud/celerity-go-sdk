//go:build integration

// The AWS backends against the services they call, emulated by LocalStack, as
// the other SDKs test theirs.
//
// What the suite alongside this cannot establish: that a request is shaped the
// way the service expects. A stand-in agrees with whatever this package sends
// it, so reading a whole path, decrypting a SecureString, following a paging
// token and telling a secret with no string value from one that is not there
// are all things only the service can answer for.
//
// Run with: bash scripts/run-tests.sh --with-integration
package aws_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssdkconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	awsconfig "github.com/newstack-cloud/celerity-go-sdk/config/aws"
)

type AWSIntegrationTestSuite struct {
	suite.Suite
	ssm     *ssm.Client
	secrets *secretsmanager.Client
}

func TestAWSIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(AWSIntegrationTestSuite))
}

func endpoint() string {
	if url := os.Getenv("CELERITY_TEST_LOCALSTACK_URL"); url != "" {
		return url
	}
	port := os.Getenv("CELERITY_TEST_LOCALSTACK_PORT")
	if port == "" {
		port = "4566"
	}
	return "http://127.0.0.1:" + port
}

// SetupSuite points the SDK at LocalStack and seeds the stores.
//
// Through the environment rather than through client options, because the
// provider builds its own clients from the ambient configuration: setting it
// here is what makes the suite exercise that path rather than one built for
// the test.
func (s *AWSIntegrationTestSuite) SetupSuite() {
	s.T().Setenv("AWS_ENDPOINT_URL", endpoint())
	s.T().Setenv("AWS_REGION", "eu-west-2")
	s.T().Setenv("AWS_ACCESS_KEY_ID", "test")
	s.T().Setenv("AWS_SECRET_ACCESS_KEY", "test")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cfg, err := awssdkconfig.LoadDefaultConfig(ctx,
		awssdkconfig.WithRegion("eu-west-2"),
		awssdkconfig.WithBaseEndpoint(endpoint()),
		awssdkconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	s.Require().NoError(err)

	s.ssm = ssm.NewFromConfig(cfg)
	s.secrets = secretsmanager.NewFromConfig(cfg)

	if _, err := s.ssm.DescribeParameters(ctx, &ssm.DescribeParametersInput{}); err != nil {
		s.T().Skipf("no LocalStack at %s: %v", endpoint(), err)
	}

	s.seedParameters(ctx)
	s.seedSecrets(ctx)
}

func (s *AWSIntegrationTestSuite) seedParameters(ctx context.Context) {
	params := []struct {
		name  string
		value string
		kind  ssmtypes.ParameterType
	}{
		{"/app/config/DB_HOST", "rds.amazonaws.com", ssmtypes.ParameterTypeString},
		{"/app/config/DB_PORT", "5432", ssmtypes.ParameterTypeString},
		// Encrypted at rest, so reading it proves the read asks for decryption:
		// without that the value comes back as ciphertext, which a handler
		// would use as though it were the password.
		{"/app/config/API_KEY", "secret-key-123", ssmtypes.ParameterTypeSecureString},
		// Below the path rather than directly under it, so reading it proves
		// the read is recursive.
		{"/app/config/nested/DEEP_KEY", "deep-value", ssmtypes.ParameterTypeString},
	}

	for _, p := range params {
		_, err := s.ssm.PutParameter(ctx, &ssm.PutParameterInput{
			Name:      aws.String(p.name),
			Value:     aws.String(p.value),
			Type:      p.kind,
			Overwrite: aws.Bool(true),
		})
		s.Require().NoError(err, "seeding %s", p.name)
	}
}

func (s *AWSIntegrationTestSuite) seedSecrets(ctx context.Context) {
	// Removed first, since a secret left by an earlier run cannot be created
	// again and its contents are not this run's.
	for _, name := range []string{"app/database-config", "app/binary-secret"} {
		_, _ = s.secrets.DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{
			SecretId:                   aws.String(name),
			ForceDeleteWithoutRecovery: aws.Bool(true),
		})
	}

	_, err := s.secrets.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name: aws.String("app/database-config"),
		SecretString: aws.String(
			`{"DB_HOST":"rds.amazonaws.com","DB_PORT":"3306","DB_PASSWORD":"s3cret"}`),
	})
	s.Require().NoError(err)

	// A secret holding bytes rather than a string, which a config store never
	// is, and which the backend has to tell apart from an empty one.
	_, err = s.secrets.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String("app/binary-secret"),
		SecretBinary: []byte("binary-data"),
	})
	s.Require().NoError(err)
}

// backend builds the provider's backend the way the config package does, with
// no stand-in: the client is the provider's own, built from the ambient
// configuration.
func (s *AWSIntegrationTestSuite) backend(kind string) func(string) (map[string]string, error) {
	provider := &awsconfig.Provider{}
	backend, err := provider.Backend(config.StoreKind(kind))
	s.Require().NoError(err)

	return func(storeID string) (map[string]string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return backend.Fetch(ctx, storeID)
	}
}

func (s *AWSIntegrationTestSuite) Test_every_parameter_under_a_path_is_read() {
	fetch := s.backend("parameter-store")

	values, err := fetch("/app/config")

	s.Require().NoError(err)
	s.Equal("rds.amazonaws.com", values["DB_HOST"])
	s.Equal("5432", values["DB_PORT"])
	// Decrypted, not the ciphertext a read without decryption returns.
	s.Equal("secret-key-123", values["API_KEY"])
}

func (s *AWSIntegrationTestSuite) Test_parameters_below_the_path_are_read_too() {
	fetch := s.backend("parameter-store")

	values, err := fetch("/app/config")

	s.Require().NoError(err)
	// Keyed by the rest of its path, so a store can hold a name with a
	// separator in it without two keys colliding.
	s.Equal("deep-value", values["nested/DEEP_KEY"])
}

func (s *AWSIntegrationTestSuite) Test_a_path_stated_either_way_reads_the_same_store() {
	withSlash, err := s.backend("parameter-store")("/app/config/")
	s.Require().NoError(err)

	withoutSlash, err := s.backend("parameter-store")("/app/config")
	s.Require().NoError(err)

	s.Equal(withoutSlash, withSlash)
	s.Equal("rds.amazonaws.com", withSlash["DB_HOST"])
}

func (s *AWSIntegrationTestSuite) Test_a_path_holding_nothing_is_an_empty_store() {
	values, err := s.backend("parameter-store")("/nothing/here")

	s.Require().NoError(err)
	s.Empty(values)
}

func (s *AWSIntegrationTestSuite) Test_a_secret_holding_a_store_is_read_and_decoded() {
	values, err := s.backend("secrets-manager")("app/database-config")

	s.Require().NoError(err)
	s.Equal(map[string]string{
		"DB_HOST":     "rds.amazonaws.com",
		"DB_PORT":     "3306",
		"DB_PASSWORD": "s3cret",
	}, values)
}

func (s *AWSIntegrationTestSuite) Test_a_secret_holding_bytes_is_an_empty_store() {
	// A config store is JSON text, so a secret with no string value holds no
	// store. Reported as empty rather than as an error, which is what an
	// application whose secret was created for something else sees.
	values, err := s.backend("secrets-manager")("app/binary-secret")

	s.Require().NoError(err)
	s.Empty(values)
}

func (s *AWSIntegrationTestSuite) Test_a_secret_that_does_not_exist_is_an_error() {
	// Distinct from an empty store: the deployment did not happen.
	_, err := s.backend("secrets-manager")("nothing/here")

	s.Require().Error(err)
	s.Contains(err.Error(), "nothing/here")
}
