package redis

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
)

// defaultCachePort is the Redis protocol's own port, used where the deployment
// recorded none.
const defaultCachePort = 6379

// What the deployment recorded about the cache.
//
// Read in one place because there are two things this package does with the
// same seven values: connect a client of its own, and hand them to an
// application building one. Reading them twice is two chances to disagree, and
// the pair that matters most is the rule below, which decides whether a signed
// token crosses the network in the clear.
type recorded struct {
	host      string
	port      int
	authMode  string
	tls       bool
	clustered bool
	user      string
	keyPrefix string
}

// Whether the connection is encrypted.
//
// What the deployment recorded, or true regardless where the cache is reached
// with the platform's own identity. IAM authentication sends a signed token,
// which is a credential in its own right and must not be readable in transit.
func (r recorded) encrypted() bool {
	return r.tls || r.authMode == resources.AuthIAM
}

// Reads what the deployment recorded, and nothing else. No connection is made
// and no secret is read, both of which are the caller's to do with what this
// answers.
func (c *redisCache) recorded(ctx context.Context) (recorded, resources.Fields, error) {
	fields := resources.Fields{Ref: c.ref}
	read := fields.Reader()

	settings := recorded{
		host:      read.Required(ctx, "_host"),
		port:      read.Number(ctx, "_port", defaultCachePort),
		authMode:  read.Optional(ctx, "_authMode", resources.AuthPassword),
		tls:       read.Boolean(ctx, "_tls", true),
		clustered: read.Boolean(ctx, "_clusterMode", false),
		user:      read.Optional(ctx, "_user", ""),
		keyPrefix: read.Optional(ctx, "_keyPrefix", ""),
	}

	if err := read.Err(); err != nil {
		return recorded{}, fields, err
	}

	return settings, fields, nil
}
