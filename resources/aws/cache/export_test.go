package cache

import (
	"context"
	"time"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
)

// Tokens signs the connection tokens a cache reached with IAM authentication
// uses as its password. There is no SDK operation behind it, so it is worth
// reaching directly.
type Tokens = elastiCacheTokens

func TokensFor(s *service.Session, host, user, region string) *Tokens {
	return &elastiCacheTokens{session: s, host: host, user: user, region: region}
}

func (t *Tokens) Credentials(ctx context.Context) (string, string, error) {
	return t.credentials(ctx)
}

// Expire makes the held token look spent, so that a test can see whether a
// fresh one is signed rather than waiting a quarter of an hour for it.
func (t *Tokens) Expire() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expires = time.Now().Add(-time.Second)
}

// ExpiresAt reports when the held token stops being used, which is how a test
// can see that one was signed again without waiting for a clock.
func (t *Tokens) ExpiresAt() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.expires
}

// Credentials is what this package contributes to a cache, which is the whole
// of what is AWS's about one.
func Credentials(s *service.Session, ref resources.Ref) credentials {
	return credentials{session: s, ref: ref}
}
