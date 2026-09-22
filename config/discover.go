package config

import (
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Environment variables the deploy engine sets, describing the stores a
// blueprint's celerity/config resources were deployed as.
//
// The store's identifier cannot come from the blueprint. A celerity/config
// resource may omit its name, in which case one is generated when it is
// created. The deployment is what knows, and sets the appropriate environment
// variables.
const (
	// StoreIDEnvVar names the store of an application with one.
	StoreIDEnvVar = "CELERITY_CONFIG_STORE_ID"
	// StoreKindEnvVar is the environment variable for how the store holds its values.
	StoreKindEnvVar = "CELERITY_CONFIG_STORE_KIND"
	// RefreshEnvVar is how long values are held before being read again, in
	// milliseconds. Zero holds them for the life of the process.
	RefreshEnvVar = "CELERITY_CONFIG_REFRESH_INTERVAL_MS"

	// An application with several stores describes each one under its own name:
	//
	//	CELERITY_CONFIG_ORDERS_STORE_ID
	//	CELERITY_CONFIG_ORDERS_STORE_KIND
	//	CELERITY_CONFIG_ORDERS_STORE_PREFIX
	//	CELERITY_CONFIG_ORDERS_NAMESPACE
	namespacePrefix = "CELERITY_CONFIG_"
	storeIDSuffix   = "_STORE_ID"
	storeKindSuffix = "_STORE_KIND"
	prefixSuffix    = "_STORE_PREFIX"
	namespaceSuffix = "_NAMESPACE"
)

// DefaultNamespace is what a store described without a name is registered as.
//
// An application does not normally have only one store. The deploy engine
// describes the resources store alongside whatever the blueprint declares, so
// the ordinary shape is two: [ResourcesNamespace], holding the identifiers of
// deployed infrastructure, and one the application declared and reads. That
// second one is what [Service.Get] reaches without being named, the resources
// store being the deployment's rather than the application's.
const DefaultNamespace = "default"

// DefaultRefresh is how long values are held in memory before re-fetching.
//
// A store is read once and then held, so without this a rotated secret would
// not be picked up until the execution environment was recycled, which for a
// warm serverless function can be hours.
const DefaultRefresh = 30 * time.Second

// FromEnvironment builds the service the deployment described.
//
// This is the ordinary route, and an application doesn't need to configure anything,
// [celerity.New] calls it, the stores come from what the deploy engine set, and
// the backend reading them comes from whichever provider module is linked.
//
// An application whose deployment described no store gets a service with no
// namespaces rather than an error. Asking it for a value then says that the
// application declares no celerity/config resource, which is true and more
// useful than a failure at startup for configuration nothing may read.
func FromEnvironment() (*Service, error) {
	return fromEnvironment(os.Environ(), CurrentPlatform())
}

func fromEnvironment(environ []string, platform Platform) (*Service, error) {
	svc := New()
	refresh := refreshInterval(environ)

	for _, discovered := range discoverStores(environ) {
		backend, err := backendFor(platform, discovered.kind)
		if err != nil {
			return nil, err
		}

		opts := []NamespaceOption{}
		if discovered.prefix != "" {
			opts = append(opts, WithPrefix(discovered.prefix))
		}
		// A backend that holds values between reads is left alone, refreshing
		// on top of one re-reads its cache rather than the store.
		if refresh > 0 && !caches(backend) {
			opts = append(opts, WithRefresh(refresh))
		}
		svc.Register(discovered.name, NewNamespace(backend, discovered.storeID, opts...))
	}
	return svc, nil
}

// A config/secret store the deployment described.
type store struct {
	name    string
	storeID string
	kind    StoreKind
	prefix  string
}

// discoverStores reads the stores out of the environment.
//
// Both forms, together. An application's own store may be described without a
// name, in which case it is registered as [DefaultNamespace], and the deploy
// engine describes the resources store under its name whatever the application
// does: reading only one form would drop the other, and dropping the resources
// store leaves every resource handle unable to resolve the thing it names.
func discoverStores(environ []string) []store {
	vars := environMap(environ)

	var stores []store
	if storeID := vars[StoreIDEnvVar]; storeID != "" {
		stores = append(stores, store{
			name:    DefaultNamespace,
			storeID: storeID,
			kind:    StoreKind(vars[StoreKindEnvVar]),
		})
	}

	for name, value := range vars {
		key, found := namedStoreKey(name)
		if !found || value == "" {
			continue
		}
		stores = append(stores, store{
			// The name to register under, which is what a handler asks for.
			// Falls back to the prefix and then to the key itself, so a
			// deployment that says only where the values are still produces a
			// namespace with a usable name.
			name: firstNonEmpty(
				vars[namespacePrefix+key+namespaceSuffix],
				vars[namespacePrefix+key+prefixSuffix],
				strings.ToLower(key),
			),
			storeID: value,
			kind:    StoreKind(vars[namespacePrefix+key+storeKindSuffix]),
			prefix:  vars[namespacePrefix+key+prefixSuffix],
		})
	}

	// Sorted so that a service is built the same way every time, whatever
	// order the environment happens to be in.
	sort.Slice(stores, func(i, j int) bool { return stores[i].name < stores[j].name })
	return stores
}

// namedStoreKey returns the name a store was described under, from the variable
// holding its identifier.
//
//	CELERITY_CONFIG_ORDERS_STORE_ID  ->  ORDERS
func namedStoreKey(name string) (string, bool) {
	rest, found := strings.CutPrefix(name, namespacePrefix)
	if !found {
		return "", false
	}

	key, found := strings.CutSuffix(rest, storeIDSuffix)
	if !found || key == "" {
		return "", false
	}

	// CELERITY_CONFIG_STORE_ID is the unnamed store, handled separately, and
	// would otherwise be read as a store named "STORE".
	if key == "STORE" {
		return "", false
	}

	return key, true
}

// refreshInterval is how long values are held before being read again.
//
// Zero disables it, which a deployment asks for by saying so. A backend that
// caches for itself is exempted separately, through [SelfCaching], rather than
// by this package knowing which platforms cache and when.
func refreshInterval(environ []string) time.Duration {
	raw, set := environMap(environ)[RefreshEnvVar]
	if !set {
		return DefaultRefresh
	}

	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

func environMap(environ []string) map[string]string {
	vars := make(map[string]string, len(environ))
	for _, entry := range environ {
		if name, value, found := strings.Cut(entry, "="); found {
			vars[name] = value
		}
	}
	return vars
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
