package awstest

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssdkconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

// Target is the AWS an integration suite runs against, an emulator on the
// machine running the tests by default, or a real account when one is
// configured.
type Target struct {
	// Endpoint the SDK is pointed at. Empty against an account, so that the SDK
	// resolves each service's own.
	Endpoint string

	// Region every client is built for, and the one a signature is scoped to.
	Region string

	// Emulator says the endpoint is a stand-in, which is what makes test
	// credentials safe and path-style addressing necessary.
	Emulator bool

	// Prefix goes in front of every resource name a suite creates, so that two
	// runs sharing an account do not share a table.
	Prefix string
}

const (
	envEndpoint = "CELERITY_TEST_AWS_ENDPOINT"
	envRegion   = "CELERITY_TEST_AWS_REGION"
	envEmulator = "CELERITY_TEST_AWS_EMULATOR"
	envPrefix   = "CELERITY_TEST_AWS_RESOURCE_PREFIX"

	defaultEndpoint = "http://127.0.0.1:4566"
	defaultRegion   = "eu-west-2"

	emulatorKeyID  = "test"
	emulatorSecret = "test"
)

// TargetFromEnv reads where this run points. Configuring nothing is the
// ordinary case and uses the emulator that the compose file brings up.
func TargetFromEnv() Target {
	target := Target{
		Endpoint: os.Getenv(envEndpoint),
		Region:   os.Getenv(envRegion),
		Emulator: emulatorMode(),
		Prefix:   os.Getenv(envPrefix),
	}
	if target.Region == "" {
		target.Region = defaultRegion
	}
	if target.Emulator && target.Endpoint == "" {
		target.Endpoint = defaultEndpoint
	}
	return target
}

// An unset or unreadable value is an emulator, because the mistake worth
// preventing is a suite creating tables in an account that nobody meant it to
// reach, not one failing to.
func emulatorMode() bool {
	raw := os.Getenv(envEmulator)
	if raw == "" {
		return true
	}

	on, err := strconv.ParseBool(raw)
	if err != nil {
		return true
	}

	return on
}

// AWS is what a suite's SetupSuite calls including where this run points, and a
// configuration for creating the infrastructure a deployment would have
// created.
func AWS(t *testing.T) (Target, aws.Config) {
	t.Helper()
	target := TargetFromEnv()
	return target, target.Config(t)
}

// Config points the process at the target and returns a configuration built for
// it.
//
// The environment is set as well as the configuration returned, because a
// provider builds its own clients from the ambient configuration: setting it
// here is what makes a suite exercise that path rather than one built for the
// test.
func (target Target) Config(t *testing.T) aws.Config {
	t.Helper()
	t.Setenv("AWS_REGION", target.Region)

	options := []func(*awssdkconfig.LoadOptions) error{
		awssdkconfig.WithRegion(target.Region),
	}
	if target.Endpoint != "" {
		t.Setenv("AWS_ENDPOINT_URL", target.Endpoint)
		options = append(options, awssdkconfig.WithBaseEndpoint(target.Endpoint))
	}
	if target.Emulator {
		// An emulator checks that a request was signed and not who signed it,
		// so the credential is invented here. Clearing the session token as
		// well, because one left in the environment would otherwise be paired
		// with these and rejected.
		t.Setenv("AWS_ACCESS_KEY_ID", emulatorKeyID)
		t.Setenv("AWS_SECRET_ACCESS_KEY", emulatorSecret)
		t.Setenv("AWS_SESSION_TOKEN", "")
		options = append(options, awssdkconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(emulatorKeyID, emulatorSecret, "")))
	}
	// Against an account the credentials are left to the default chain, which
	// is the profile, the environment or the role the runner assumed, so that
	// nothing about authenticating for real lives in the test suites.

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg, err := awssdkconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		t.Fatalf("loading the configuration for %s: %v", target, err)
	}
	return cfg
}

// Name is what a suite calls a resource it creates. Against an emulator that is
// usually the base name; against an account a prefix keeps concurrent runs, and
// the people sharing the account, out of each other's way.
func (target Target) Name(base string) string {
	return target.Prefix + base
}

// Reachable skips a suite when the target is not there, so that a run without
// it reports what is missing rather than a wall of failures.
func (target Target) Reachable(t *testing.T, probe func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := probe(ctx); err != nil {
		t.Skipf("AWS is not reachable at %s: %v", target, err)
	}
}

func (target Target) String() string {
	if target.Endpoint != "" {
		return target.Endpoint
	}

	return "the " + target.Region + " region"
}
