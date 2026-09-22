package main

import (
	"fmt"
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

// platform is the set of SDK packages a deploy target needs linked.
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

// GenerateOptions configures what the generated file links beyond the deploy
// target's own packages.
type GenerateOptions struct {
	// Local adds what a development session reads, which a deployed artefact
	// has no use for. The Celerity CLI sets it when the build is for
	// `celerity dev` rather than for a deployment.
	Local bool
}

// built lists the platform packages that exist today, so that a target whose
// other packages are ready is not held back by one that is not, and an import
// is never written for a package that cannot be resolved.
var built = map[string]bool{
	modulePath + "/serverless/aws": true,
	modulePath + "/resources/aws":  true,
	modulePath + "/config/aws":     true,
	LocalConfig:                    true,
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

	linked := []string{p.Adapter, p.Resources, p.Config}
	if opts.Local {
		linked = append(linked, LocalConfig)
	}

	imports := make([]string, 0, len(linked))
	for _, pkg := range linked {
		if pkg != "" && built[pkg] {
			imports = append(imports, pkg)
		}
	}
	sort.Strings(imports)

	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by celerity-go for the %q deploy target. DO NOT EDIT.\n", target)
	if opts.Local {
		b.WriteString("//\n// Built for a local development session.\n")
	}
	b.WriteString("//\n")
	b.WriteString("// Regenerate with: celerity-go generate --target <target>\n\n")
	fmt.Fprintf(&b, "package %s\n\n", packageName)

	if len(imports) == 1 {
		fmt.Fprintf(&b, "import _ %q\n", imports[0])
		return b.String(), nil
	}

	b.WriteString("import (\n")
	for _, path := range imports {
		fmt.Fprintf(&b, "\t_ %q\n", path)
	}
	b.WriteString(")\n")

	return b.String(), nil
}
