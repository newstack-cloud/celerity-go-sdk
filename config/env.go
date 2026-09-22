package config

import (
	"os"
	"strings"
)

// PlatformEnvVar names the platform an application is deployed to.
//
// The Celerity CLI sets it, "local" for a development session, and it is what
// selects the provider a store is read through.
const PlatformEnvVar = "CELERITY_PLATFORM"

// Platform is the platform an application is running on.
type Platform string

const (
	PlatformAWS   Platform = "aws"
	PlatformAzure Platform = "azure"
	PlatformGCP   Platform = "gcp"
	PlatformLocal Platform = "local"
	// PlatformOther is anything else, including nothing being set, which is
	// what a containerised deployment on hardware the SDK has no name for is.
	PlatformOther Platform = "other"
)

// CurrentPlatform returns the platform this process is running on.
//
// Anything the SDK does not have a name for is [PlatformOther] rather than an
// error: it is used to choose a provider, and a platform nobody named has no
// provider to choose.
func CurrentPlatform() Platform {
	switch Platform(strings.ToLower(os.Getenv(PlatformEnvVar))) {
	case PlatformAWS:
		return PlatformAWS
	case PlatformAzure:
		return PlatformAzure
	case PlatformGCP:
		return PlatformGCP
	case PlatformLocal:
		return PlatformLocal
	default:
		return PlatformOther
	}
}
