package bucket

import (
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
)

// Buckets returns a builder driving the given APIs, so that a suite exercises
// the real store against stand-ins rather than against S3.
func Buckets(api API, presign PresignAPI) func(resources.Ref) (bucket.Store, error) {
	return (&buckets{api: api, presignAPI: presign}).build
}
