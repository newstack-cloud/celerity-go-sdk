package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

// extensionCache reads a secret through the parameters and secrets Lambda
// extension, which holds it in a local cache beside the function.
//
// Worth using where it is attached: a warm function reads from localhost rather
// than paying a request to Secrets Manager, and every function in a fleet
// reading its own secret on every cold start is the load that makes that
// matter.
type extensionCache struct {
	port     string
	fallback config.Backend
	client   *http.Client
}

// extensionTimeout bounds a read from the extension.
//
// It is a process on the same machine, so a request that is slow is a request
// that has already failed, and waiting on it only delays the fall back to the
// service that does have the values.
const extensionTimeout = 5 * time.Second

// newExtensionCache returns a backend reading through the extension on a port,
// falling back to the service behind it.
func newExtensionCache(port string, fallback config.Backend) *extensionCache {
	return &extensionCache{
		port:     port,
		fallback: fallback,
		client:   &http.Client{Timeout: extensionTimeout},
	}
}

// Caching reports that this backend holds values between reads, satisfying
// [config.SelfCaching].
//
// It is what stops the config package refreshing on top of the extension. A
// refresh would re-read the extension's cache rather than the store, costing a
// request to learn nothing, and the extension expires its own entries.
func (b *extensionCache) Caching() bool {
	return true
}

// Fetch reads the secret from the extension, falling back to the service.
//
// The extension is a cache in front of Secrets Manager, so anything wrong with
// it is a reason to go to the thing it caches rather than to fail: the values
// are still there to be read. The fallback is logged, since an extension that
// is attached and not answering is worth knowing about even though the
// application carried on.
func (b *extensionCache) Fetch(ctx context.Context, storeID string) (map[string]string, error) {
	raw, err := b.read(ctx, storeID)
	if err != nil {
		telemetry.LoggerFrom(ctx).WarnContext(ctx,
			"could not read configuration through the parameters and secrets extension, "+
				"reading it from Secrets Manager instead",
			"store", storeID,
			"error", err,
		)
		return b.fallback.Fetch(ctx, storeID)
	}
	if raw == "" {
		return map[string]string{}, nil
	}
	return config.ValuesFromJSON([]byte(raw), storeID)
}

// read asks the extension for a secret and returns its string value.
func (b *extensionCache) read(ctx context.Context, storeID string) (string, error) {
	endpoint := fmt.Sprintf(
		"http://localhost:%s/secretsmanager/get?secretId=%s",
		b.port, url.QueryEscape(storeID),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	// The function's own credentials token, which the extension requires so
	// that only the function it belongs to can read its secrets.
	req.Header.Set("X-Aws-Parameters-Secrets-Token", os.Getenv(SessionTokenEnvVar))

	res, err := b.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the extension answered %s", res.Status)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}

	// The extension answers in the shape the service does, of which only the
	// secret's own value is wanted.
	var answer struct {
		SecretString string `json:"SecretString"`
	}
	if err := decodeJSON(body, &answer); err != nil {
		return "", err
	}
	return answer.SecretString, nil
}

// decodeJSON is separate so the error says what could not be read rather than
// naming a type the caller never mentioned.
func decodeJSON(raw []byte, out any) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("the extension's answer is not the expected JSON: %w", err)
	}
	return nil
}
