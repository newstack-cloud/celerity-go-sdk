// Package aws reads configuration from the stores AWS holds it in.
//
// It is selected by importing it, which is how an application picks a platform
// without naming one anywhere else:
//
//	import _ "github.com/newstack-cloud/celerity-go-sdk/config/aws"
//
// An init registers it for the AWS platform, and the generated platform file
// links it for an AWS deploy target, so an application does nothing to have
// its configuration read.
//
// # Which store a value comes from
//
// A blueprint's celerity/config resource lists the keys that hold no sensitive
// value, and the deploy engine uses that to decide how the store is created: a
// store of mostly plain text becomes parameters under a path, and one whose
// values are sensitive becomes a single secret. It says which in the
// environment, and this package reads whichever it names.
//
// A secret is read through the parameters and secrets Lambda extension where
// the function has it attached, since that is a local cache and avoids a
// request per cold start. Parameters are always read through the SDK: the
// extension has no equivalent of the call that reads a tree of parameters by path prefix.
//
// Everything about the extension is here rather than in core, which holds no
// provider's environment variables.
package aws

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"

	"github.com/newstack-cloud/celerity-go-sdk/config"
)

// The kinds of store AWS holds configuration in, which a deployment names in
// CELERITY_CONFIG_STORE_KIND. These are AWS's own services, which is why they
// are named here and not in core.
const (
	// StoreSecretsManager holds a whole store as one secret, read and decoded
	// in a single request. The default, since a store commonly holds values
	// that are all sensitive.
	StoreSecretsManager config.StoreKind = "secrets-manager"
	// StoreParameterStore holds each value separately under a path, which is
	// what a store of mostly plain text wants.
	StoreParameterStore config.StoreKind = "parameter-store"
)

// ExtensionPortEnvVar is set by AWS when the parameters and secrets extension
// layer is attached to a function, and is how this package knows the local
// cache is there to use.
const ExtensionPortEnvVar = "PARAMETERS_SECRETS_EXTENSION_HTTP_PORT"

// SessionTokenEnvVar is the function's own credentials token, which the
// extension requires on every request so that only the function it belongs to
// can read its secrets.
const SessionTokenEnvVar = "AWS_SESSION_TOKEN"

// Name identifies this provider in errors.
const Name = "aws"

func init() { config.RegisterProvider(&Provider{}) }

// Provider reads configuration from AWS.
//
// An application does not construct one, importing this package registers it,
// and it is selected when the platform is AWS.
type Provider struct {
	// Loaded once and shared, since loading it reads the environment and may
	// reach the instance metadata service for credentials, which should be captured
	// once and passed through the application.
	once    sync.Once
	cfg     aws.Config
	loadErr error

	// Stood in for by tests, which have no AWS to reach. Nil in every other
	// case, where a client is built from the loaded configuration.
	secrets    secretsAPI
	parameters parametersAPI
}

// awsConfig loads the AWS configuration once.
func (p *Provider) awsConfig(ctx context.Context) (aws.Config, error) {
	p.once.Do(func() { p.cfg, p.loadErr = awsconfig.LoadDefaultConfig(ctx) })
	if p.loadErr != nil {
		return aws.Config{}, fmt.Errorf(
			"loading AWS configuration to read a config store: %w",
			p.loadErr,
		)
	}
	return p.cfg, nil
}

// Name identifies the provider.
func (p *Provider) Name() string { return Name }

// Platform is the platform this provider serves.
func (p *Provider) Platform() config.Platform {
	return config.PlatformAWS
}

// Backend returns the backend reading a store of this kind.
//
// A kind AWS does not hold configuration in is an error naming what it does,
// since it means the deployment asked for something this provider cannot do
// and every value would otherwise read as absent.
func (p *Provider) Backend(kind config.StoreKind) (config.Backend, error) {
	switch kind {
	case StoreParameterStore:
		// Always through the SDK. The extension serves one secret or one
		// parameter at a time and has no equivalent of reading a whole path,
		// which is how a store of parameters is read.
		return &parameterStore{provider: p}, nil

	case StoreSecretsManager, "":
		secrets := &secretsManager{provider: p}
		if port := os.Getenv(ExtensionPortEnvVar); port != "" {
			return newExtensionCache(port, secrets), nil
		}
		return secrets, nil

	default:
		return nil, fmt.Errorf(
			"AWS holds configuration as %q or %q, and the deployment asked for %q",
			StoreSecretsManager, StoreParameterStore, kind,
		)
	}
}
