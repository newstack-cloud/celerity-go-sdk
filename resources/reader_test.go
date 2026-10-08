package resources_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
)

// A resource reached through a family of values, a cache or a database, reads
// several fields to build one connection. The reader is so that reads a list
// and checks once, which means the first failure has to survive the reads after
// it and those reads must not be attempted: one unreadable value makes the
// whole resource unreachable, so there is nothing to salvage from the rest.
type ReaderTestSuite struct {
	suite.Suite
}

func TestReaderTestSuite(t *testing.T) {
	suite.Run(t, new(ReaderTestSuite))
}

// fields is what a deployment recorded about a cache called sessions.
func (s *ReaderTestSuite) fields(values map[string]string) resources.Fields {
	return resources.Fields{Ref: resources.Ref{
		Kind: resources.KindCache,
		Name: "sessions",
		Config: serviceWith(
			config.Links{"sessions": {Type: "cache", ConfigKey: "sessionsCache"}},
			values),
	}}
}

func (s *ReaderTestSuite) Test_every_value_is_read_where_nothing_fails() {
	read := s.fields(map[string]string{"sessionsCache_host": "cache-1"}).Reader()

	host := read.Required(context.Background(), "_host")
	port := read.Number(context.Background(), "_port", 6379)
	mode := read.Optional(context.Background(), "_authMode", "password")
	encrypted := read.Boolean(context.Background(), "_tls", true)

	s.Require().NoError(read.Err())
	s.Equal("cache-1", host)
	s.Equal(6379, port, "the fallback, since the deployment recorded none")
	s.Equal("password", mode)
	s.True(encrypted)
}

func (s *ReaderTestSuite) Test_the_first_failure_is_kept_and_the_reads_after_it_are_skipped() {
	// Zero rather than the fallback, so a caller that forgot to check does not
	// quietly connect to something plausible.
	read := s.fields(map[string]string{}).Reader()

	host := read.Required(context.Background(), "_host")
	port := read.Number(context.Background(), "_port", 6379)
	mode := read.Optional(context.Background(), "_authMode", "password")
	encrypted := read.Boolean(context.Background(), "_tls", true)

	s.Require().Error(read.Err())
	s.Empty(host)
	s.Zero(port, "the fallback is not applied after a failure")
	s.Empty(mode)
	s.False(encrypted)
}

func (s *ReaderTestSuite) Test_a_later_failure_does_not_replace_the_first() {
	// The first is the one worth reporting, since the reads after it were
	// skipped and a second error would name a field nobody tried to read.
	read := s.fields(map[string]string{}).Reader()

	_ = read.Required(context.Background(), "_host")
	first := read.Err()
	_ = read.Required(context.Background(), "_user")

	s.Require().Error(first)
	s.Equal(first, read.Err())
}

func (s *ReaderTestSuite) Test_a_reader_reads_the_same_values_the_fields_do() {
	// The reader is the same reads with the checking moved, so the two must not
	// be able to answer differently.
	fields := s.fields(map[string]string{
		"sessionsCache_host": "cache-1",
		"sessionsCache_port": "6380",
	})

	host, err := fields.Required(context.Background(), "_host")
	s.Require().NoError(err)
	port, err := fields.Number(context.Background(), "_port", 6379)
	s.Require().NoError(err)

	read := fields.Reader()
	s.Equal(host, read.Required(context.Background(), "_host"))
	s.Equal(port, read.Number(context.Background(), "_port", 6379))
	s.Require().NoError(read.Err())
}
