package main

import (
	"fmt"
	"go/build/constraint"
	"slices"
	"sort"
	"strings"
)

// The deploy targets the Celerity CLI names, from apps/cli/internal/compose/consts.go
// in the Celerity monorepo. A target is the blueprint's, so it is what decides
// which platform packages an application is built against.
const (
	TargetAWS              = "aws"
	TargetAWSServerless    = "aws-serverless"
	TargetGCloud           = "gcloud"
	TargetGCloudServerless = "gcloud-serverless"
	TargetAzure            = "azure"
	TargetAzureServerless  = "azure-serverless"
)

// The set of SDK packages a deploy target needs linked.
type platform struct {
	// Adapter is the serverless adapter, empty for a containerised target where
	// the Celerity runtime serves events over the IPC stream and no adapter is
	// involved at all.
	Adapter string
	// Resources is the resource provider, needed by both containerised and
	// serverless deployments since handler code reaches a bucket the same way in
	// either.
	Resources string
	// Config is the provider that reads the platform's own configuration
	// stores, a parameter store or a secret manager.
	Config string
}

const modulePath = "github.com/newstack-cloud/celerity-go-sdk"

// A target maps to packages rather than to a name the SDK resolves at runtime,
// because Go links what is imported: the mapping has to be applied before the
// build, not during it.
var platforms = map[string]platform{
	TargetAWS: {
		Resources: modulePath + "/resources/aws",
		Config:    modulePath + "/config/aws",
	},
	TargetAWSServerless: {
		Adapter:   modulePath + "/serverless/aws",
		Resources: modulePath + "/resources/aws",
		Config:    modulePath + "/config/aws",
	},
	TargetGCloud: {
		Resources: modulePath + "/resources/gcp",
		Config:    modulePath + "/config/gcp",
	},
	TargetGCloudServerless: {
		Adapter:   modulePath + "/serverless/gcp",
		Resources: modulePath + "/resources/gcp",
		Config:    modulePath + "/config/gcp",
	},
	TargetAzure: {
		Resources: modulePath + "/resources/azure",
		Config:    modulePath + "/config/azure",
	},
	TargetAzureServerless: {
		Adapter:   modulePath + "/serverless/azure",
		Resources: modulePath + "/resources/azure",
		Config:    modulePath + "/config/azure",
	},
}

// LocalConfig reads configuration from the Valkey instance a local development
// session runs, and is linked for such a session rather than for a deployment.
//
// A session builds its own artefact to mount into the runtime container, so
// what it links need not match what is shipped: a deployed function may not have a use
// for a Redis client and should not carry one if so. Which provider serves is still
// decided at startup from the platform, exactly as a serverless adapter is, so
// an application does nothing either way.
const LocalConfig = modulePath + "/config/local"

// LocalResources is the resource provider a local development session runs
// under, and is linked for such a session rather than for a deployment.
//
// It serves a queue and a topic and delegates the other four kinds.
const LocalResources = modulePath + "/resources/local"

// LocalResourceBackend is what actually carries a local queue and topic: a
// Redis stream and a Redis channel, which is what the runtime's consumer side
// reads in a session.
//
// Separate from the provider because it is a separate decision. The provider
// knows which kinds a session serves; what serves them is registered by
// whichever backend is linked, so a backend that is not Redis can replace this
// without the provider changing.
const LocalResourceBackend = modulePath + "/resources/local/redis"

// Telemetry maps what a blueprint asks to be traced with to the module that
// does it.
//
// Separate from the deploy target, because tracing does not belong to a specific platform.
var Telemetry = map[string]string{
	"otel":          modulePath + "/telemetry/otel",
	"opentelemetry": modulePath + "/telemetry/otel",
	"none":          "",
}

// Instrumentation maps Go packages to the corresponding tracing module.
//
// This is separate from the tracer, and linked per backend rather than wholesale,
// because each carries the instrumentation library for one backend: an
// application with no database should not link a SQL instrumentation, and one
// that never touches AWS should not link the AWS SDK's.
var Instrumentation = map[string]map[string]string{
	modulePath + "/telemetry/otel": {
		"redis": modulePath + "/resources/redis/otel",
		"sqldb": modulePath + "/resources/sqldb/otel",
	},
}

// GenerateOptions configures what the generated file links beyond the deploy
// target's own packages.
type GenerateOptions struct {
	// BuildTags constrains the generated file to the builds that need it, which
	// is how a test package's copy is kept out of the ordinary test run. The
	// providers are heavy and registering them where no test asked changes what
	// the registry answers. Empty for the main file, which is every non-test build.
	BuildTags []string
	// Local adds what a development session reads, which a deployed artefact
	// has no use for. The Celerity CLI sets it when the build is for
	// `celerity dev` rather than for a deployment.
	Local bool
	// SQLEngines are the engines the blueprint's database resources declare,
	// one driver linked for each. Empty where the application declares no
	// database, which is the ordinary case and links none.
	SQLEngines []string
	// Resources are the kinds of resource the blueprint declares, one package
	// linked for each.
	//
	// A platform's resource module is a package per kind, so an application
	// with a data store and nothing else should not need object storage or a messaging
	// client. Naming the kinds here is what decides that, since Go links what is
	// imported and cannot choose afterwards.
	Resources []string
	// Telemetry is what the blueprint asks traces to be exported with, which
	// is a key of [Telemetry].
	Telemetry string
}

var resourcePackages = map[string]string{
	"bucket":      "bucket",
	"queue":       "queue",
	"topic":       "topic",
	"cache":       "cache",
	"datastore":   "datastore",
	"sqlDatabase": "sqldb",
}

// RedisCache is the cache, whatever the target deploys to.
const RedisCache = modulePath + "/resources/redis"

// UnknownResourceError reports a kind of resource the SDK has no package for.
type UnknownResourceError struct {
	Kind string
}

func (e *UnknownResourceError) Error() string {
	supported := make([]string, 0, len(resourcePackages))
	for kind := range resourcePackages {
		supported = append(supported, kind)
	}
	sort.Strings(supported)

	return fmt.Sprintf(
		"unknown resource kind %q. Supported: %s",
		e.Kind, strings.Join(supported, ", "),
	)
}

// Resolves the kinds a blueprint declares to the packages that
// serve them, under the platform's own resource module.
//
// An unknown kind is an error rather than nothing, since linking no package
// would surface as a resource that cannot be reached.
func resourceImports(resources string, kinds []string) ([]string, error) {
	if resources == "" {
		return nil, nil
	}

	var imports []string
	for _, kind := range kinds {
		pkg, ok := resourcePackages[kind]
		if !ok {
			return nil, &UnknownResourceError{Kind: kind}
		}
		imports = append(imports, resources+"/"+pkg)
		// A cache needs the platform's credentials and the Redis client that
		// uses them, which is a module of its own.
		if kind == "cache" {
			imports = append(imports, RedisCache)
		}
	}
	return imports, nil
}

var sqlDrivers = map[string]string{
	enginePostgres: "github.com/jackc/pgx/v5/stdlib",
	engineMySQL:    "github.com/go-sql-driver/mysql",
}

// The engines a blueprint's database resources declare, which the SDK's
// resources package names as well.
const (
	enginePostgres = "postgres"
	engineMySQL    = "mysql"
)

// UnknownEngineError reports a database engine the SDK has no driver for.
type UnknownEngineError struct {
	Engine string
}

func (e *UnknownEngineError) Error() string {
	supported := make([]string, 0, len(sqlDrivers))
	for engine := range sqlDrivers {
		supported = append(supported, engine)
	}
	sort.Strings(supported)

	return fmt.Sprintf(
		"unknown database engine %q. Supported: %s",
		e.Engine, strings.Join(supported, ", "),
	)
}

// Resolves the engines a blueprint declares to the packages
// that serve them.
//
// An engine may carry its own import path, as engine=import/path, for an
// application that wants a driver other than the one chosen for it. Unknown
// without one is an error rather than nothing, since linking no driver would
// surface as a database that cannot be reached.
func sqlDriverImports(engines []string) ([]string, error) {
	var imports []string
	for _, engine := range engines {
		name, path, named := strings.Cut(engine, "=")
		if named {
			imports = append(imports, path)
			continue
		}

		path, ok := sqlDrivers[name]
		if !ok {
			return nil, &UnknownEngineError{Engine: name}
		}
		imports = append(imports, path)
	}

	return imports, nil
}

// built lists the platform packages that exist today, acts as a single
// source of truth for packages to be imported in the generated entry points
// for builds.
var built = map[string]bool{
	modulePath + "/serverless/aws":       true,
	modulePath + "/resources/aws":        true,
	modulePath + "/config/aws":           true,
	LocalConfig:                          true,
	LocalResources:                       true,
	LocalResourceBackend:                 true,
	modulePath + "/telemetry/otel":       true,
	modulePath + "/resources/aws/otel":   true,
	modulePath + "/resources/redis/otel": true,
	modulePath + "/resources/sqldb/otel": true,
}

// implemented lists the targets whose packages exist today. A target that is
// mapped but not implemented is refused by name, rather than failing later as
// an unresolvable import that says nothing about why.
var implemented = map[string]bool{
	TargetAWS:           true,
	TargetAWSServerless: true,
}

// UnsupportedTargetError reports a deploy target this SDK cannot build for.
type UnsupportedTargetError struct {
	Target string
}

func (e *UnsupportedTargetError) Error() string {
	supported := make([]string, 0, len(implemented))
	for target := range implemented {
		supported = append(supported, target)
	}
	sort.Strings(supported)

	if _, mapped := platforms[e.Target]; mapped {
		return fmt.Sprintf(
			"the Go SDK does not support the %q deploy target yet. Supported: %s",
			e.Target, strings.Join(supported, ", "),
		)
	}

	return fmt.Sprintf(
		"unknown deploy target %q. Supported: %s",
		e.Target, strings.Join(supported, ", "),
	)
}

// PlatformFor returns the packages a deploy target needs linked.
func PlatformFor(target string) (platform, error) {
	p, ok := platforms[target]
	if !ok || !implemented[target] {
		return platform{}, &UnsupportedTargetError{Target: target}
	}
	return p, nil
}

// GeneratedFileName is the file the platform imports are written to.
//
// It carries the _gen suffix so that it reads as derived, and it is expected to
// be gitignored: the blueprint decides its contents, so a committed copy is a
// second answer to a question that already has one.
const GeneratedFileName = "celerity_platform_gen.go"

// The module a named exporter is traced with, refused by
// name rather than silently linking nothing.
func telemetryImport(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	pkg, known := Telemetry[name]
	if !known {
		return "", fmt.Errorf(
			"unknown telemetry %q: pass one of %s, or leave it unset to link none",
			name, strings.Join(sortedKeys(Telemetry), ", "))
	}
	return pkg, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}

	sort.Strings(keys)
	return keys
}

// Which backends the resources a blueprint declares are reached
// through, which is what decides the instrumentation linked.
//
// Only the two platform-agnostic backends a module is linked for. A platform's own service calls
// are traced by the provider itself, which needs nothing linked at this level
// because it adds no additional dependency.
func backendsFor(_ platform, kinds []string) []string {
	backends := map[string]bool{}
	for _, kind := range kinds {
		switch kind {
		case "cache":
			backends["redis"] = true
		case "sqlDatabase":
			backends["sqldb"] = true
		}
	}
	return sortedKeys(toStrings(backends))
}

func toStrings(set map[string]bool) map[string]string {
	out := make(map[string]string, len(set))
	for key := range set {
		out[key] = key
	}
	return out
}

// GeneratePlatformFile renders the file that links the deploy target's platform
// packages into an application.
//
// Go links what is imported, so something has to name the platform packages.
// Having a developer do it would put a deployment decision in application
// source, where retargeting means an edit and supporting a new platform means
// every application learns about it. The blueprint already names the target and
// the CLI already runs the build, so the import is derived from the blueprint
// instead, which is the same division the Node SDK gets from a deployment
// naming the entry point it wants.
func GeneratePlatformFile(packageName, target string, opts GenerateOptions) (string, error) {
	p, err := PlatformFor(target)
	if err != nil {
		return "", err
	}

	imports, err := platformImports(p, opts)
	if err != nil {
		return "", err
	}

	drivers, err := sqlDriverImports(opts.SQLEngines)
	if err != nil {
		return "", err
	}
	sort.Strings(drivers)
	drivers = slices.Compact(drivers)

	constrained, err := buildConstraint(opts.BuildTags)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	writeHeader(&b, packageName, target, constrained, opts.Local)
	writeImports(&b, imports, drivers)
	return b.String(), nil
}

func platformImports(p platform, opts GenerateOptions) ([]string, error) {
	linked := []string{p.Adapter, p.Resources, p.Config}
	if opts.Local {
		linked = append(linked, LocalConfig, LocalResources, LocalResourceBackend)
	}

	tracing, err := telemetryImport(opts.Telemetry)
	if err != nil {
		return nil, err
	}
	linked = append(linked, tracing)
	for _, backend := range backendsFor(p, opts.Resources) {
		linked = append(linked, Instrumentation[tracing][backend])
	}

	kinds, err := resourceImports(p.Resources, opts.Resources)
	if err != nil {
		return nil, err
	}

	imports := make([]string, 0, len(linked)+len(kinds))
	for _, pkg := range linked {
		// Empty where a target declares no such module, and absent from built
		// where the module is not released yet.
		if pkg != "" && built[pkg] {
			imports = append(imports, pkg)
		}
	}
	imports = append(imports, kinds...)

	sort.Strings(imports)
	return slices.Compact(imports), nil
}

func writeHeader(b *strings.Builder, packageName, target, constrained string, local bool) {
	if constrained != "" {
		fmt.Fprintf(b, "//go:build %s\n\n", constrained)
	}
	fmt.Fprintf(b, "// Code generated by celerity-go for the %q deploy target. DO NOT EDIT.\n", target)
	if local {
		b.WriteString("//\n// Built for a local development session.\n")
	}
	b.WriteString("//\n")
	b.WriteString("// Regenerate with: celerity-go generate --target <target>\n\n")
	fmt.Fprintf(b, "package %s\n\n", packageName)
}

func writeImports(b *strings.Builder, imports, drivers []string) {
	if len(imports)+len(drivers) == 1 {
		fmt.Fprintf(b, "import _ %q\n", append(imports, drivers...)[0])
		return
	}

	b.WriteString("import (\n")
	for _, path := range imports {
		fmt.Fprintf(b, "\t_ %q\n", path)
	}
	writeDrivers(b, len(imports), drivers)
	b.WriteString(")\n")
}

func writeDrivers(b *strings.Builder, imports int, drivers []string) {
	if len(drivers) == 0 {
		return
	}
	if imports > 0 {
		b.WriteString("\n")
	}
	b.WriteString("\t// The database drivers the blueprint's engines need.\n")
	for _, path := range drivers {
		fmt.Fprintf(b, "\t_ %q\n", path)
	}
}

// The //go:build expression for the tags a file is needed
// under.
//
// Any of them rather than all. Each tag names a suite that reaches live
// resources, and the file is needed when any one of those is being built.
func buildConstraint(tags []string) (string, error) {
	named := make([]string, 0, len(tags))
	for _, tag := range tags {
		if trimmed := strings.TrimSpace(tag); trimmed != "" {
			named = append(named, trimmed)
		}
	}
	if len(named) == 0 {
		return "", nil
	}

	expression := strings.Join(named, " || ")
	if _, err := constraint.Parse("//go:build " + expression); err != nil {
		return "", fmt.Errorf(
			"%q cannot be a build constraint: %w.\nA build tag is letters, digits, "+
				"underscores and dots, and --test-tags takes them comma separated",
			expression, err)
	}
	return expression, nil
}
