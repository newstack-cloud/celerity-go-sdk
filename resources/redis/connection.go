package redis

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
)

// Connection reads what the deployment recorded about the cache, for an
// application building a client of its own.
//
// The same values this package connects with, read the same way, so a client
// built from them reaches the cache this handle reaches. Nothing is connected
// here, and a password held in the platform's secret store is read when the
// returned function is called rather than now.
func (c *redisCache) Connection(ctx context.Context) (cache.Connection, error) {
	settings, fields, err := c.recorded(ctx)
	if err != nil {
		return cache.Connection{}, err
	}

	password, err := c.passwordFor(ctx, fields, settings)
	if err != nil {
		return cache.Connection{}, err
	}

	return cache.Connection{
		Host:        settings.host,
		Port:        settings.port,
		TLS:         settings.encrypted(),
		ClusterMode: settings.clustered,
		User:        settings.user,
		AuthMode:    settings.authMode,
		KeyPrefix:   settings.keyPrefix,
		Password:    password,
	}, nil
}

// passwordFor is how a connection gets its password, which is the one thing
// about a cache that differs by platform.
func (c *redisCache) passwordFor(
	ctx context.Context, f resources.Fields, settings recorded,
) (func(context.Context) (string, error), error) {
	if settings.authMode != resources.AuthIAM {
		// Read now rather than per call as a recorded password does not expire,
		// and reading it once is what the connection this package makes does.
		token, err := c.password(ctx, f)
		if err != nil {
			return nil, err
		}

		return func(context.Context) (string, error) {
			return token, nil
		}, nil
	}

	signed, err := c.signer(ctx, f, settings.host, settings.user)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (string, error) {
		_, token, err := signed(ctx)
		return token, err
	}, nil
}
