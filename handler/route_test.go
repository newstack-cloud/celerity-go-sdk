package handler_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// A blueprint spells a catch-all {name+} and the router spells it {*name}.
// Both the runtime path and a serverless adapter translate between them, and a
// handler must see the same route either way, so there is one definition and
// these are its cases.
type RouteTestSuite struct {
	suite.Suite
}

func TestRouteTestSuite(t *testing.T) {
	suite.Run(t, new(RouteTestSuite))
}

func (s *RouteTestSuite) Test_a_catch_all_takes_the_routers_form() {
	cases := []struct {
		name  string
		route string
		want  string
	}{
		{"a catch-all", "/files/{path+}", "/files/{*path}"},
		{"alongside an ordinary parameter", "/orders/{orderId}/files/{rest+}", "/orders/{orderId}/files/{*rest}"},
		{"underscores are allowed in names", "/files/{file_path+}", "/files/{*file_path}"},
		{"ordinary parameters are untouched", "/orders/{orderId}", "/orders/{orderId}"},
		{"no parameters", "/health", "/health"},
		{"already in the router's form", "/files/{*path}", "/files/{*path}"},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.Equal(tc.want, handler.NormaliseRoute(tc.route))
		})
	}
}

func (s *RouteTestSuite) Test_the_catch_all_parameter_is_named_in_either_form() {
	cases := []struct {
		name  string
		route string
		want  string
		found bool
	}{
		{"the blueprint's form", "/files/{path+}", "path", true},
		{"the router's form", "/files/{*path}", "path", true},
		{"alongside ordinary parameters", "/orders/{orderId}/files/{rest+}", "rest", true},
		{"a route with no catch-all", "/orders/{orderId}", "", false},
		{"a route with no parameters", "/health", "", false},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			got, found := handler.CatchAllParam(tc.route)

			s.Equal(tc.found, found)
			s.Equal(tc.want, got)
		})
	}
}
