package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
)

// Secret returns a value the deployment either wrote down or left a reference
// to, and whether it found one.
//
// A local session seeds the literal, where it is a fixed constant nobody needs
// to protect. A deployed resource has a generated credential living in a
// secret, and what is recorded is its id: the credential can rotate, and a copy
// in the parameter store would keep working right up until it silently did not.
//
// Neither is an error when both are absent, since a cache with no auth
// configured is a legitimate thing to deploy. The caller decides whether it can
// do without one.
func Secret(
	ctx context.Context, s *Session, f resources.Fields, literalSuffix, refSuffix string,
) (string, bool, error) {
	literal, err := f.Optional(ctx, literalSuffix, "")
	if err != nil {
		return "", false, err
	}
	if literal != "" {
		return literal, true, nil
	}

	id, err := f.Optional(ctx, refSuffix, "")
	if err != nil || id == "" {
		return "", false, err
	}

	value, err := s.SecretString(ctx, id)
	if err != nil {
		return "", false, fmt.Errorf(
			"celerity: reading the credential for %s from the secret %q: %w", f.Ref, id, err)
	}
	return value, true, nil
}

// PasswordIn takes the password out of a secret, whichever shape it is held
// in.
//
// A reference is an opaque id handed to whoever knows how to read it, and what
// comes back is taken either way:
//
//   - a JSON object, from which the password field is read. AWS deployments
//     create the secret in this shape to match an AWS-managed RDS secret, so
//     the same rotation tooling works against either.
//   - anything else, which is the password itself.
//
// An object carrying no password is a secret of the wrong kind rather than a
// password that happens to look like JSON, for example one pointed at the
// cluster's master secret by mistake. Sending the whole object as the password
// would fail as a wrong password, which is the hardest thing to tell from a
// rotation that has not finished.
func PasswordIn(raw string, ref resources.Ref) (string, error) {
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return raw, nil
	}

	password, ok := fields["password"].(string)
	if !ok || password == "" {
		return "", fmt.Errorf(
			"celerity: the secret holding the password for %s is an object with no "+
				"password field", ref)
	}
	return password, nil
}

// SecretsAPI is what this package calls on Secrets Manager, which is one read.
type SecretsAPI interface {
	GetSecretValue(
		context.Context, *secretsmanager.GetSecretValueInput, ...func(*secretsmanager.Options),
	) (*secretsmanager.GetSecretValueOutput, error)
}

// SecretString reads one secret's string value.
//
// Read directly rather than through the config package's own AWS provider: that
// reads a whole store of application configuration, and this is one credential
// belonging to a resource.
func (s *Session) SecretString(ctx context.Context, id string) (string, error) {
	client, err := s.secretsClient(ctx)
	if err != nil {
		return "", err
	}

	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(id),
	})
	if err != nil {
		return "", err
	}
	if out.SecretString == nil {
		return "", fmt.Errorf("the secret holds no string value")
	}
	return *out.SecretString, nil
}

func (s *Session) secretsClient(ctx context.Context) (SecretsAPI, error) {
	if s.secretsAPI != nil {
		return s.secretsAPI, nil
	}
	return s.secrets.Get(func() (SecretsAPI, error) {
		cfg, err := s.Config(ctx)
		if err != nil {
			return nil, err
		}
		return secretsmanager.NewFromConfig(cfg), nil
	})
}

// RegionOf reads the region a deployment recorded for a resource, which is
// empty for everything it created for this application.
//
// Empty is the application's own region, which is where everything a
// deployment created for it lives. A resource the blueprint declares as
// external carries one, since it may be anywhere and, for a bucket, its ARN
// cannot say where: an S3 bucket ARN has no region segment.
func RegionOf(ctx context.Context, ref resources.Ref) (string, error) {
	return resources.Fields{Ref: ref}.Optional(ctx, resources.FieldRegion, "")
}

// KeyFor is the cache key for the client a resource is reached with.
func KeyFor(ctx context.Context, ref resources.Ref) (ClientKey, error) {
	region, err := RegionOf(ctx, ref)
	if err != nil {
		return ClientKey{}, err
	}
	return ClientKey{Region: region}, nil
}
