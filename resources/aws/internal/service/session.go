// Package service is what the AWS resource packages share: the ambient
// configuration, the Secrets Manager reads a credential needs, and the registry
// the provider finds each resource package through.
//
// Internal because none of it is a part of the resources API.
// It exists so that resources/aws/datastore
// and resources/aws/cache can share what is common to every AWS resource
// without either importing the other, and without the provider having to import
// a package for a service the application does not use.
package service

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

// Session is the ambient AWS configuration, loaded once and shared by every
// resource one provider builds.
//
// Loading reads the environment and may reach the instance metadata service for
// credentials, which is worth paying for once rather than per resource.
type Session struct {
	once    sync.Once
	cfg     aws.Config
	loadErr error

	// load is what a test stands in for, so that a suite drives a session with
	// no AWS to reach. Nil everywhere else.
	load func(context.Context) (aws.Config, error)

	// Secrets Manager is shared: two resource kinds read a credential from a
	// secret, and neither is the owner of the client that does it.
	secrets    Lazy[SecretsAPI]
	secretsAPI SecretsAPI
}

// UseSecrets stands Secrets Manager in, for a test reading a credential with no
// AWS to reach.
func (s *Session) UseSecrets(api SecretsAPI) {
	s.secretsAPI = api
}

// NewSession returns a session that loads the ambient configuration on first
// use.
func NewSession() *Session {
	return &Session{}
}

// NewSessionWith returns a session that loads what the given function returns,
// for a test driving a provider with no AWS to reach.
func NewSessionWith(load func(context.Context) (aws.Config, error)) *Session {
	return &Session{load: load}
}

// Config loads the ambient AWS configuration once.
//
// The first caller's context is the one the load runs under, which is the same
// bargain the config provider makes: it happens on a handler's first use of any
// resource, and every later caller gets what that one loaded.
func (s *Session) Config(ctx context.Context) (aws.Config, error) {
	s.once.Do(func() {
		if s.load != nil {
			s.cfg, s.loadErr = s.load(ctx)
		} else {
			s.cfg, s.loadErr = awsconfig.LoadDefaultConfig(ctx)
		}
		if s.loadErr == nil {
			// Added once, to the configuration every client in this process is
			// built from, so a client built later traces without having to ask.
			// A cache, a queue and a data store are separate clients over one
			// configuration, which is why this is here rather than at each.
			s.cfg.APIOptions = append(s.cfg.APIOptions, traceCalls)
		}
	})
	if s.loadErr != nil {
		return aws.Config{}, fmt.Errorf(
			"celerity: loading AWS configuration to reach a resource: %w", s.loadErr)
	}
	return s.cfg, nil
}

// ConfigFor returns the ambient configuration with its region replaced, for a
// resource the deployment recorded somewhere other than where the application
// runs.
//
// An empty region, which is what a deployment records for everything it created
// for this application, is the application's own. The credentials are the same
// either way: reaching a resource in another account is the far side granting
// this role through a resource policy rather than this process holding another
// account's keys.
func (s *Session) ConfigFor(ctx context.Context, region string) (aws.Config, error) {
	cfg, err := s.Config(ctx)
	if err != nil {
		return aws.Config{}, err
	}
	if region == "" || region == cfg.Region {
		return cfg, nil
	}

	regional := cfg.Copy()
	regional.Region = region
	return regional, nil
}

// Lazy builds a value once, on first use, and hands the same one to everything
// after.
//
// A service client is safe to share and expensive enough to be worth building
// once, but cannot be built before a context exists, so it cannot be built when
// the handle is taken.
type Lazy[T any] struct {
	once sync.Once
	val  T
	err  error
}

// Get builds the value on the first call and returns what that call produced on
// every one after.
func (l *Lazy[T]) Get(build func() (T, error)) (T, error) {
	l.once.Do(func() { l.val, l.err = build() })
	return l.val, l.err
}
