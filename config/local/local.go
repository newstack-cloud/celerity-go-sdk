// Package local reads configuration from the Valkey instance a local Celerity
// development session runs.
//
// It is selected by importing it, which is how an application picks a platform
// without naming one anywhere else:
//
//	import _ "github.com/newstack-cloud/celerity-go-sdk/config/local"
//
// An init registers it for the local platform, so `celerity dev` reaches the
// same store the other SDKs do. The generated platform file links it
// alongside whichever provider the deploy target needs, because a local session
// runs the artefact built for that target with CELERITY_PLATFORM set to local:
// an application does nothing to have configuration work in both.
//
// # What it reads
//
// One key per store, holding a JSON object of the values. That is what the
// Celerity CLI writes, and reading it any other way would make the tooling
// language-specific: the CLI populates one store for every SDK.
//
// A separate module because it needs a Redis client, and an application
// deployed to a cloud should not carry one to reach a store it will never read.
package local

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/redis/go-redis/v9"

	"github.com/newstack-cloud/celerity-go-sdk/config"
)

// Environment variables a local session sets, naming the Valkey it runs.
const (
	HostEnvVar = "CELERITY_CONFIG_VALKEY_HOST"
	PortEnvVar = "CELERITY_CONFIG_VALKEY_PORT"
)

// Name identifies this provider in errors.
const Name = "local-valkey"

const (
	defaultHost = "localhost"
	defaultPort = "6379"
)

func init() { config.RegisterProvider(&Provider{}) }

// Provider reads configuration from a local session's Valkey.
//
// An application does not construct one: importing this package registers it,
// and it is selected when the platform is local.
type Provider struct {
	once   sync.Once
	client *redis.Client
}

// Name identifies the provider.
func (p *Provider) Name() string { return Name }

// Platform is the platform this provider serves.
func (p *Provider) Platform() config.Platform {
	return config.PlatformLocal
}

// Backend returns the backend reading a store.
//
// The kind is ignored. A local session holds every store the same way, one key
// to a store, because there is nothing here to hold them in differently: the
// kinds a deployment names are a cloud's own services.
func (p *Provider) Backend(config.StoreKind) (config.Backend, error) {
	return &backend{provider: p}, nil
}

// address is the Valkey to reach, which a local session names and which
// defaults to one on this machine so that a developer running the application
// directly still reads the store the session seeded.
func address() string {
	host := os.Getenv(HostEnvVar)
	if host == "" {
		host = defaultHost
	}
	port := os.Getenv(PortEnvVar)
	if port == "" {
		port = defaultPort
	}
	return host + ":" + port
}

// client builds the client once and reuses it, since it pools its own
// connections and building one per read would dial per read.
func (p *Provider) resolveClient() *redis.Client {
	p.once.Do(func() {
		p.client = redis.NewClient(&redis.Options{Addr: address()})
	})
	return p.client
}

type backend struct{ provider *Provider }

// Fetch returns the values a store holds.
//
// A key that is not there is an empty store rather than an error: a local
// session seeds what the blueprint declares, and a store nothing has written to
// yet is empty rather than missing. A Valkey that cannot be reached is an
// error, because that is a session that is not running and saying so is more
// use than an application quietly reading no configuration.
func (b *backend) Fetch(ctx context.Context, storeID string) (map[string]string, error) {
	raw, err := b.provider.resolveClient().Get(ctx, storeID).Result()
	if err == redis.Nil {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf(
			"celerity: reading the config store %q from the local session at %s: %w",
			storeID, address(), err,
		)
	}

	return config.ValuesFromJSON([]byte(raw), storeID)
}
