package celerity_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
)

// Handler tags must match the runtime's construction byte for byte: a tag that
// differs fails the startup handshake rather than misrouting later, so these
// cases mirror the formats in the runtime's event_queue.rs.
type TagsTestSuite struct {
	suite.Suite
}

func TestTagsTestSuite(t *testing.T) {
	suite.Run(t, new(TagsTestSuite))
}

func (s *TagsTestSuite) Test_http_tags_are_method_and_route() {
	cases := []struct {
		name   string
		method string
		route  string
		want   string
	}{
		{"simple route", "GET", "/orders", "GET::/orders"},
		{"path parameter", "POST", "/orders/{orderId}", "POST::/orders/{orderId}"},
		{"lowercase method is canonicalised", "get", "/orders", "GET::/orders"},
		{"root", "GET", "/", "GET::/"},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.Equal(tc.want, celerity.HTTPTag(tc.method, tc.route))
		})
	}
}

func (s *TagsTestSuite) Test_the_other_tag_formats() {
	s.Equal("sendMessage::$default", celerity.WebSocketTag("sendMessage", "$default"))
	s.Equal("source::orderQueue::processOrder", celerity.SourceTag("orderQueue", "processOrder"))
	s.Equal("custom::recalculatePricing", celerity.CustomTag("recalculatePricing"))
}

func (s *TagsTestSuite) Test_routes_are_normalised_to_the_routers_form() {
	cases := []struct {
		name  string
		route string
		want  string
	}{
		{"catch-all becomes the router's form", "/files/{path+}", "/files/{*path}"},
		{"ordinary parameters are untouched", "/orders/{orderId}", "/orders/{orderId}"},
		{"mixed", "/orders/{orderId}/files/{rest+}", "/orders/{orderId}/files/{*rest}"},
		{"no parameters", "/health", "/health"},
		{"underscores are allowed in names", "/files/{file_path+}", "/files/{*file_path}"},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.Equal(tc.want, celerity.NormaliseRoute(tc.route))
		})
	}
}
