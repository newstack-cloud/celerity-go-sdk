package config_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
)

// Configuration arrives as strings, and a handler wants the types it declared.
type BindTestSuite struct {
	suite.Suite
}

func TestBindTestSuite(t *testing.T) {
	suite.Run(t, new(BindTestSuite))
}

type settings struct {
	Region  string        `config:"REGION"`
	Retries int           `config:"RETRIES"`
	Ratio   float64       `config:"RATIO"`
	Debug   bool          `config:"DEBUG"`
	Timeout time.Duration `config:"TIMEOUT"`
	// Untagged, so nothing binds to it.
	Internal string
}

func (s *BindTestSuite) bind(values map[string]string, out any) error {
	svc := config.New()
	svc.Register("settings", config.NewNamespace(
		config.MapBackend{"settings": values}, "settings"))
	return svc.Bind(context.Background(), out)
}

func (s *BindTestSuite) Test_values_take_the_types_the_struct_declared() {
	var got settings
	err := s.bind(map[string]string{
		"REGION":  "eu-west-2",
		"RETRIES": "3",
		"RATIO":   "0.25",
		"DEBUG":   "true",
		"TIMEOUT": "30s",
	}, &got)

	s.Require().NoError(err)
	s.Equal("eu-west-2", got.Region)
	s.Equal(3, got.Retries)
	s.InDelta(0.25, got.Ratio, 0.0001)
	s.True(got.Debug)
	// A duration is an int64, so reading "30s" as one would have failed where
	// what was meant is clear.
	s.Equal(30*time.Second, got.Timeout)
}

func (s *BindTestSuite) Test_an_absent_key_leaves_a_default_in_place() {
	got := settings{Region: "eu-west-1", Retries: 5}

	err := s.bind(map[string]string{"RETRIES": "2"}, &got)

	s.Require().NoError(err)
	s.Equal("eu-west-1", got.Region, "a struct built with defaults keeps them")
	s.Equal(2, got.Retries)
}

func (s *BindTestSuite) Test_a_value_that_will_not_parse_stops_the_application() {
	cases := []struct {
		name   string
		values map[string]string
		phrase string
	}{
		{
			// Present and wrong should stop an application rather than
			// silently read as zero, which is a plausible retry count.
			name:   "not a number",
			values: map[string]string{"RETRIES": "quite a few"},
			phrase: "whole number",
		},
		{
			name:   "not a duration",
			values: map[string]string{"TIMEOUT": "30"},
			phrase: "duration",
		},
		{
			name:   "not a boolean",
			values: map[string]string{"DEBUG": "yes please"},
			phrase: "true or false",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			var got settings
			err := s.bind(tc.values, &got)

			s.Require().Error(err)
			s.Contains(err.Error(), tc.phrase)
			for key := range tc.values {
				s.Contains(err.Error(), key, "the error should name the key")
			}
		})
	}
}

func (s *BindTestSuite) Test_an_untagged_field_is_left_alone() {
	got := settings{Internal: "untouched"}

	err := s.bind(map[string]string{"Internal": "changed"}, &got)

	s.Require().NoError(err)
	s.Equal("untouched", got.Internal)
}

func (s *BindTestSuite) Test_binding_needs_somewhere_to_write() {
	cases := []struct {
		name string
		out  any
	}{
		{"a value rather than a pointer", settings{}},
		{"nothing at all", nil},
		{"a pointer to something that is not a struct", new(string)},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			err := s.bind(map[string]string{"REGION": "eu-west-2"}, tc.out)

			s.Require().Error(err)
			s.Contains(err.Error(), "pointer to a struct")
		})
	}
}

func (s *BindTestSuite) Test_a_pointer_to_a_pointer_is_allocated() {
	// As the request binder does, so a handler holding *settings is filled
	// rather than left nil.
	var got *settings

	err := s.bind(map[string]string{"REGION": "eu-west-2"}, &got)

	s.Require().NoError(err)
	s.Require().NotNil(got)
	s.Equal("eu-west-2", got.Region)
}

// Shared by several applications and embedded into each one's own settings,
// which is Go's way of factoring configuration they hold in common.
type shared struct {
	Region  string        `config:"REGION"`
	Timeout time.Duration `config:"TIMEOUT"`
}

// unexported embedded, which is the ordinary way to share within one package.
type internals struct {
	Debug bool `config:"DEBUG"`
}

type embedding struct {
	shared
	internals
	Name string `config:"NAME"`
}

func (s *BindTestSuite) Test_what_is_embedded_binds_as_though_it_were_declared_here() {
	// Its fields are promoted, so a tag on them is a tag on this struct.
	var got embedding
	err := s.bind(map[string]string{
		"NAME":    "orders",
		"REGION":  "eu-west-2",
		"TIMEOUT": "30s",
		"DEBUG":   "true",
	}, &got)

	s.Require().NoError(err)
	s.Equal("orders", got.Name)
	s.Equal("eu-west-2", got.Region)
	s.Equal(30*time.Second, got.Timeout)
	s.True(got.Debug, "an unexported embedded type's fields are promoted too")
}

type deep struct {
	embedding
	Extra string `config:"EXTRA"`
}

func (s *BindTestSuite) Test_embedding_is_followed_however_deep_it_goes() {
	var got deep
	err := s.bind(map[string]string{"REGION": "eu-west-2", "EXTRA": "x"}, &got)

	s.Require().NoError(err)
	s.Equal("eu-west-2", got.Region, "two levels down")
	s.Equal("x", got.Extra)
}

// Shared is exported so that it can be embedded by pointer as reflection cannot
// allocate an embedded pointer to an unexported type.
type Shared struct {
	Region string `config:"REGION"`
}

type embeddedPointer struct {
	*Shared
	Name string `config:"NAME"`
}

func (s *BindTestSuite) Test_an_embedded_pointer_is_allocated() {
	// The fields it holds are promoted whether or not anything built it yet.
	var got embeddedPointer
	err := s.bind(map[string]string{"NAME": "orders", "REGION": "eu-west-2"}, &got)

	s.Require().NoError(err)
	s.Require().NotNil(got.Shared)
	s.Equal("eu-west-2", got.Region)
}

type embeddedUnexportedPointer struct {
	*shared
	Name string `config:"NAME"`
}

func (s *BindTestSuite) Test_an_embedded_pointer_to_an_unexported_type_is_refused() {
	// Its promoted fields can never be filled, so a caller who tagged them
	// would read zero values and be told nothing. encoding/json refuses the
	// same shape for the same reason.
	var got embeddedUnexportedPointer

	err := s.bind(map[string]string{"REGION": "eu-west-2"}, &got)

	s.Require().Error(err)
	s.Contains(err.Error(), "unexported")
	s.Contains(err.Error(), "export the type, or embed it by value",
		"the error should say what to do about it")
}

// A struct that groups its configuration the way the application thinks of it,
// rather than as one flat list of names that have to carry their own grouping.
type grouped struct {
	Name     string `config:"NAME"`
	Database struct {
		Host    string        `config:"HOST"`
		Port    int           `config:"PORT"`
		Timeout time.Duration `config:"TIMEOUT"`
	} `config:"DATABASE"`
	Cache struct {
		Host string `config:"HOST"`
	} `config:"CACHE"`
}

func (s *BindTestSuite) Test_a_tagged_struct_is_a_level_of_the_key() {
	var got grouped
	err := s.bind(map[string]string{
		"NAME":             "orders",
		"DATABASE_HOST":    "db.example.com",
		"DATABASE_PORT":    "5432",
		"DATABASE_TIMEOUT": "30s",
		"CACHE_HOST":       "cache.example.com",
	}, &got)

	s.Require().NoError(err)
	s.Equal("orders", got.Name)
	s.Equal("db.example.com", got.Database.Host)
	s.Equal(5432, got.Database.Port)
	s.Equal(30*time.Second, got.Database.Timeout)
	// Two groups holding the same field name, which is the point of grouping:
	// neither has to carry the other's prefix in its own tag.
	s.Equal("cache.example.com", got.Cache.Host)
}

func (s *BindTestSuite) Test_a_hierarchy_held_as_a_path_binds_the_same() {
	// A parameter store holds a hierarchy as a path and this package hands the
	// rest of it back as the key, so the same struct reads either. A handler is
	// not supposed to know which store its configuration came from.
	var got grouped
	err := s.bind(map[string]string{
		"NAME":          "orders",
		"DATABASE/HOST": "db.example.com",
		"DATABASE/PORT": "5432",
	}, &got)

	s.Require().NoError(err)
	s.Equal("db.example.com", got.Database.Host)
	s.Equal(5432, got.Database.Port)
}

type deeplyGrouped struct {
	Services struct {
		Pricing struct {
			URL string `config:"URL"`
		} `config:"PRICING"`
		Billing struct {
			URL string `config:"URL"`
		} `config:"BILLING"`
	} `config:"SERVICES"`
}

func (s *BindTestSuite) Test_grouping_nests_as_deep_as_it_is_written() {
	var got deeplyGrouped
	err := s.bind(map[string]string{
		"SERVICES_PRICING_URL": "https://pricing.internal",
		"SERVICES_BILLING_URL": "https://billing.internal",
	}, &got)

	s.Require().NoError(err)
	// Two groups at the same level, which is where a shared prefix would show
	// up as one being read under the other's name.
	s.Equal("https://pricing.internal", got.Services.Pricing.URL)
	s.Equal("https://billing.internal", got.Services.Billing.URL)
}

func (s *BindTestSuite) Test_a_grouped_value_that_will_not_parse_names_its_whole_key() {
	var got grouped

	err := s.bind(map[string]string{"DATABASE_PORT": "not a number"}, &got)

	s.Require().Error(err)
	s.Contains(err.Error(), "DATABASE_PORT",
		"the error should name the key as written, not the field's own segment")
}

type untaggedGroup struct {
	Database struct {
		Host string `config:"HOST"`
	}
	Name string `config:"NAME"`
}

func (s *BindTestSuite) Test_an_untagged_struct_field_is_no_level_at_all() {
	// Nothing says what its keys would be nested under, so it is left alone
	// rather than read as though its fields were at the top.
	var got untaggedGroup
	err := s.bind(map[string]string{"NAME": "orders", "HOST": "db.example.com"}, &got)

	s.Require().NoError(err)
	s.Equal("orders", got.Name)
	s.Empty(got.Database.Host)
}

func (s *BindTestSuite) Test_a_value_that_will_not_parse_names_the_key_wherever_it_is() {
	var got embedding

	err := s.bind(map[string]string{"TIMEOUT": "not a duration"}, &got)

	s.Require().Error(err)
	s.Contains(err.Error(), "TIMEOUT", "even though the field is embedded")
	s.Contains(err.Error(), "duration")
}

// A hierarchy with fields that use "_" as a part
// of the field name and not just as separators.
type mixedHierarchy struct {
	Services struct {
		Pricing struct {
			URL     string `config:"url"`
			Timeout string `config:"read_timeout"`
		} `config:"pricing"`
	} `config:"services"`
}

func (s *BindTestSuite) Test_each_level_is_punctuated_however_its_key_was_written() {
	cases := []struct {
		name string
		key  string
	}{
		{"paths throughout", "services/pricing/url"},
		{"underscores throughout", "services_pricing_url"},
		{"a path above an underscore", "services/pricing_url"},
		{"an underscore above a path", "services_pricing/url"},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			var got mixedHierarchy

			err := s.bind(map[string]string{tc.key: "https://pricing"}, &got)

			s.Require().NoError(err)
			s.Equal("https://pricing", got.Services.Pricing.URL,
				"%s did not reach the field", tc.key)
		})
	}
}

func (s *BindTestSuite) Test_a_field_name_may_contain_the_separator_itself() {
	// The tag is one name, whatever is in it: only the levels above it are
	// punctuation.
	var got mixedHierarchy

	err := s.bind(map[string]string{"services/pricing/read_timeout": "5s"}, &got)

	s.Require().NoError(err)
	s.Equal("5s", got.Services.Pricing.Timeout)
}
