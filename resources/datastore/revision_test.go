package datastore_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// A revision is opaque, so the only things worth asserting are the three states
// it can be in and which of them a write will accept.
func TestRevision(t *testing.T) {
	cases := []struct {
		name     string
		revision datastore.Revision
		known    bool
		value    string
		carried  bool
	}{
		{
			name:     "one a read produced",
			revision: datastore.NewRevision("r-1"),
			known:    true,
			value:    "r-1",
			carried:  true,
		},
		{
			name:     "an item that carries none",
			revision: datastore.Unrevisioned,
			known:    true,
			carried:  false,
		},
		{
			name:     "a provider reading back an empty attribute",
			revision: datastore.NewRevision(""),
			known:    true,
			carried:  false,
		},
		{
			// A variable that was never assigned. Refused by a write rather
			// than treated as "no precondition", which would mean there would
			// be no concurrency check.
			name:     "the zero value",
			revision: datastore.Revision{},
			known:    false,
			carried:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.known, tc.revision.Known())

			value, carried := tc.revision.Value()
			assert.Equal(t, tc.carried, carried)
			assert.Equal(t, tc.value, value)
		})
	}
}

func TestUnrevisionedIsUsableButCarriesNothing(t *testing.T) {
	value, carried := datastore.Unrevisioned.Value()

	assert.True(t, datastore.Unrevisioned.Known(),
		"a read of an item written before revisions still has to produce a usable revision")
	assert.False(t, carried)
	assert.Empty(t, value)
}
