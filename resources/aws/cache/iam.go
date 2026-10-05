package cache

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/smithy-go/logging"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
)

// elastiCacheTokens signs the tokens a cache reached with IAM authentication
// takes as its password.
//
// ElastiCache has no call that mints one. The token is a request to connect,
// presigned with the function's own credentials, which the cluster verifies
// against IAM: what is sent as a password is the signed URL with its scheme
// removed. There is no SDK operation for it, so it is signed here.
type elastiCacheTokens struct {
	session *service.Session
	host    string
	user    string
	region  string

	mu      sync.Mutex
	token   string
	expires time.Time
}

// How long a signed connection token is accepted for, which is ElastiCache's
// own limit rather than a choice.
const elastiCacheTokenLifetime = 15 * time.Minute

// Signed again a minute before it lapses, so a connection is never opened with
// one that expires while it is being made.
const elastiCacheTokenMargin = time.Minute

// credentials is what the Redis client calls to authenticate a new connection.
//
// A pool outlives a token, so this is asked on every connection rather than
// once. The answer is held until it is nearly spent, since signing reads the
// credentials rather than making a request and is cheap but not free.
func (t *elastiCacheTokens) credentials(ctx context.Context) (string, string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.token != "" && time.Now().Before(t.expires) {
		return t.user, t.token, nil
	}

	token, err := t.sign(ctx)
	if err != nil {
		return "", "", err
	}

	t.token = token
	t.expires = time.Now().Add(elastiCacheTokenLifetime - elastiCacheTokenMargin)
	return t.user, t.token, nil
}

func (t *elastiCacheTokens) sign(ctx context.Context) (string, error) {
	cfg, err := t.session.Config(ctx)
	if err != nil {
		return "", err
	}

	region := t.region
	if region == "" {
		region = cfg.Region
	}
	if region == "" {
		return "", fmt.Errorf(
			"celerity: signing a connection token for %s needs the region it is in, and "+
				"neither its %q nor the ambient AWS configuration gives one", t.host, "_region")
	}

	credentials, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return "", fmt.Errorf("celerity: signing a connection token for %s: %w", t.host, err)
	}

	query := url.Values{
		"Action": {"connect"},
		"User":   {t.user},
		// The signer takes the lifetime from the request rather than as an
		// argument, and a presigned request carrying none is refused.
		"X-Amz-Expires": {strconv.Itoa(int(elastiCacheTokenLifetime.Seconds()))},
	}
	endpoint := &url.URL{
		Scheme:   "https",
		Host:     t.host,
		Path:     "/",
		RawQuery: query.Encode(),
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", fmt.Errorf("celerity: signing a connection token for %s: %w", t.host, err)
	}

	signed, _, err := v4.NewSigner().PresignHTTP(
		ctx, credentials, req,
		// The payload is empty, and this is its SHA-256. SigV4 signs a hash of
		// the body whether or not there is one.
		emptyPayloadHash,
		"elasticache", region, time.Now(),
		func(o *v4.SignerOptions) { o.Logger = logging.Nop{} },
	)
	if err != nil {
		return "", fmt.Errorf("celerity: signing a connection token for %s: %w", t.host, err)
	}

	// What ElastiCache takes as the password is the signed URL without its
	// scheme, which is not a form the signer produces.
	return strings.TrimPrefix(signed, "https://"), nil
}

// The SHA-256 of nothing, which is what SigV4 signs for a request with no body.
const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
