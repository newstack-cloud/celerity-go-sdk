package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/newstack-cloud/celerity-go-sdk/config"
)

// The one call each backend makes, named as an interface so that what this
// package decides is testable without AWS.
type secretsAPI interface {
	GetSecretValue(
		ctx context.Context,
		in *secretsmanager.GetSecretValueInput,
		opts ...func(*secretsmanager.Options),
	) (*secretsmanager.GetSecretValueOutput, error)
}

type parametersAPI interface {
	GetParametersByPath(
		ctx context.Context,
		in *ssm.GetParametersByPathInput,
		opts ...func(*ssm.Options),
	) (*ssm.GetParametersByPathOutput, error)
}

// secretsManager reads a store held as a single secret.
type secretsManager struct{ provider *Provider }

// Fetch reads the secret and decodes the store out of it.
//
// A secret with no string value is an empty store rather than an error: the
// deployment created it and has not written to it yet, which is a store with
// nothing in it.
func (b *secretsManager) Fetch(ctx context.Context, storeID string) (map[string]string, error) {
	client, err := b.client(ctx)
	if err != nil {
		return nil, err
	}

	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(storeID),
	})
	if err != nil {
		return nil, fmt.Errorf("celerity: reading the config store %q from Secrets Manager: %w", storeID, err)
	}
	if out.SecretString == nil || *out.SecretString == "" {
		return map[string]string{}, nil
	}
	return config.ValuesFromJSON([]byte(*out.SecretString), storeID)
}

func (b *secretsManager) client(ctx context.Context) (secretsAPI, error) {
	if b.provider.secrets != nil {
		return b.provider.secrets, nil
	}
	cfg, err := b.provider.awsConfig(ctx)
	if err != nil {
		return nil, err
	}
	return secretsmanager.NewFromConfig(cfg), nil
}

// parameterStore reads a store held as parameters under a path.
type parameterStore struct{ provider *Provider }

// Fetch reads every parameter under the store's path.
//
// Decrypted, since a store of mostly plain text may still hold a value that is
// not, and recursively, so that a key with a slash in it is read as one key
// rather than being missed. Paged until the store is exhausted: a store larger
// than one page would otherwise be read as whatever fitted in the first, which
// is a subtly wrong configuration rather than a failure.
func (b *parameterStore) Fetch(ctx context.Context, storeID string) (map[string]string, error) {
	client, err := b.client(ctx)
	if err != nil {
		return nil, err
	}

	prefix := storeID
	if prefix == "" || prefix[len(prefix)-1] != '/' {
		prefix += "/"
	}

	values := map[string]string{}
	var next *string
	for {
		out, err := client.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
			Path:           aws.String(prefix),
			Recursive:      aws.Bool(true),
			WithDecryption: aws.Bool(true),
			NextToken:      next,
		})
		if err != nil {
			return nil, fmt.Errorf(
				"celerity: reading the config store %q from Parameter Store: %w", storeID, err)
		}

		collect(values, prefix, out.Parameters)

		next = out.NextToken
		if next == nil || *next == "" {
			return values, nil
		}
	}
}

// collect takes the parameters of one page, keyed by what they are called
// within the store rather than by their full path.
func collect(values map[string]string, prefix string, params []ssmtypes.Parameter) {
	for _, param := range params {
		if param.Name == nil || param.Value == nil {
			continue
		}
		name := *param.Name
		if len(name) > len(prefix) && name[:len(prefix)] == prefix {
			name = name[len(prefix):]
		}
		values[name] = *param.Value
	}
}

func (b *parameterStore) client(ctx context.Context) (parametersAPI, error) {
	if b.provider.parameters != nil {
		return b.provider.parameters, nil
	}
	cfg, err := b.provider.awsConfig(ctx)
	if err != nil {
		return nil, err
	}
	return ssm.NewFromConfig(cfg), nil
}
