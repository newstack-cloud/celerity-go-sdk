package resources

import (
	"context"
	"fmt"
	"strconv"
)

// Fields reads what a deployment recorded about a resource.
//
// A bucket or a queue is one identifier and doesn't need this. A cache or a
// database is an endpoint, a port, a user and how to authenticate, each written
// under the resource's own key with a suffix, and this is the reading of those.
type Fields struct {
	Ref Ref
}

// Required returns a value the resource cannot be reached without. Absent is an
// error naming the key, rather than a connection to an empty host.
func (f Fields) Required(ctx context.Context, suffix string) (string, error) {
	value, ok, err := f.Ref.Field(ctx, suffix)
	if err != nil {
		return "", err
	}

	if !ok || value == "" {
		return "", fmt.Errorf(
			"celerity: the deployment recorded no %q for %s, which is needed to reach it",
			suffix, f.Ref,
		)
	}
	return value, nil
}

// Optional returns a value, or the fallback where the deployment recorded
// none.
func (f Fields) Optional(ctx context.Context, suffix, fallback string) (string, error) {
	value, ok, err := f.Ref.Field(ctx, suffix)
	if err != nil {
		return "", err
	}

	if !ok || value == "" {
		return fallback, nil
	}

	return value, nil
}

// Number returns a whole number, or the fallback where the deployment recorded
// none. A value that is not a whole number is an error naming the key.
func (f Fields) Number(ctx context.Context, suffix string, fallback int) (int, error) {
	value, ok, err := f.Ref.Field(ctx, suffix)
	if err != nil || !ok || value == "" {
		return fallback, err
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf(
			"celerity: the %q recorded for %s is %q, which is not a whole number",
			suffix, f.Ref, value)
	}
	return parsed, nil
}

// Boolean reads a flag, which is on unless the deployment wrote exactly
// "false". Deliberately not strconv.ParseBool, so every SDK reads what the
// deploy engine wrote the same way.
func (f Fields) Boolean(ctx context.Context, suffix string, fallback bool) (bool, error) {
	value, ok, err := f.Ref.Field(ctx, suffix)
	if err != nil || !ok || value == "" {
		return fallback, err
	}
	return value != "false", nil
}

// The ways a deployment says a cache or a database is reached, written under
// the resource's own key as _authMode.
const (
	AuthPassword = "password"
	AuthIAM      = "iam"
)

// FieldRegion is the suffix a deployment records a resource's region under,
// where the resource is not in the application's own region.
//
// Absent for everything a deployment created for this application, which is
// where the application is. Written for a resource the blueprint declares as
// external, which may be somewhere else, and read through [Fields.Optional] so
// that absence means the application's own region rather than a failure.
//
// Named here rather than in a provider module because the suffix is the deploy
// engine's: it writes one shape for every language.
const FieldRegion = "_region"
