package awstest

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// Secrets stands in for Secrets Manager, which two resource kinds read a
// credential through.
type Secrets struct {
	Asked  string
	Value  string
	Absent bool
	Err    error
}

func (f *Secrets) GetSecretValue(
	_ context.Context,
	in *secretsmanager.GetSecretValueInput,
	_ ...func(*secretsmanager.Options),
) (*secretsmanager.GetSecretValueOutput, error) {
	f.Asked = aws.ToString(in.SecretId)
	if f.Err != nil {
		return nil, f.Err
	}
	if f.Absent {
		return &secretsmanager.GetSecretValueOutput{}, nil
	}

	return &secretsmanager.GetSecretValueOutput{
		SecretString: aws.String(f.Value),
	}, nil
}
