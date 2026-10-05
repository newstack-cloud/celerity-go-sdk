package local

import "github.com/newstack-cloud/celerity-go-sdk/resources"

// WithPlatform returns a provider that delegates to the given one, so that a
// suite drives the division of labour without arranging the process-wide
// registry the real selection reads.
func WithPlatform(platform resources.Provider) *Provider {
	return &Provider{platform: platform}
}
