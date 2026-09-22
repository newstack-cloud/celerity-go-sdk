package aws

import (
	"context"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/newstack-cloud/celerity-go-sdk/config"
)

// ProviderWith builds a provider whose calls go to stand-ins rather than to
// AWS, so that what this package decides is testable without an account.
//
// Only the one call each backend makes is stood in for: every rule about which
// store is read, how a page is collected and what a failure means is on this
// side of it.
func ProviderWith(secrets SecretsFunc, parameters ParametersFunc) *Provider {
	p := &Provider{}
	if secrets != nil {
		p.secrets = secrets
	}
	if parameters != nil {
		p.parameters = parameters
	}
	return p
}

// ExtensionBackend builds the extension-backed backend against a given base
// port and fallback, as [Provider.Backend] does when the extension is attached.
func ExtensionBackend(port string, client *http.Client, fallback config.Backend) config.Backend {
	return &extensionCache{port: port, fallback: fallback, client: client}
}

// SecretsFunc and ParametersFunc adapt a function to the one call a backend
// makes, so a case states its answer inline.
type SecretsFunc func(secretID string) (*string, error)

type ParametersFunc func(path string, next *string) (params map[string]string, nextToken *string, err error)

func (f SecretsFunc) GetSecretValue(
	_ context.Context,
	in *secretsmanager.GetSecretValueInput,
	_ ...func(*secretsmanager.Options),
) (*secretsmanager.GetSecretValueOutput, error) {
	value, err := f(*in.SecretId)
	if err != nil {
		return nil, err
	}
	return &secretsmanager.GetSecretValueOutput{SecretString: value}, nil
}

func (f ParametersFunc) GetParametersByPath(
	_ context.Context,
	in *ssm.GetParametersByPathInput,
	_ ...func(*ssm.Options),
) (*ssm.GetParametersByPathOutput, error) {
	values, next, err := f(*in.Path, in.NextToken)
	if err != nil {
		return nil, err
	}

	params := make([]ssmtypes.Parameter, 0, len(values))
	for name, value := range values {
		params = append(params, ssmtypes.Parameter{
			Name:  aws.String(name),
			Value: aws.String(value),
		})
	}
	return &ssm.GetParametersByPathOutput{Parameters: params, NextToken: next}, nil
}

// ClientOf returns the HTTP client a backend reads the extension through, so a
// test can check it is the same one from read to read.
func ClientOf(backend config.Backend) *http.Client {
	cache, ok := backend.(*extensionCache)
	if !ok {
		return nil
	}
	return cache.client
}

// NewExtensionBackend builds the extension-backed backend the way
// [Provider.Backend] does, client and all.
func NewExtensionBackend(port string, fallback config.Backend) config.Backend {
	return newExtensionCache(port, fallback)
}
