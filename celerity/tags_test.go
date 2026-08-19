package celerity_test

import (
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
)

// Handler tags must match the runtime's construction byte for byte: a tag that
// differs fails the startup handshake rather than misrouting later, so these
// cases mirror the formats in the runtime's event_queue.rs.

func TestHTTPTag(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			if got := celerity.HTTPTag(tc.method, tc.route); got != tc.want {
				t.Errorf("HTTPTag(%q, %q) = %q, want %q", tc.method, tc.route, got, tc.want)
			}
		})
	}
}

func TestOtherTagFormats(t *testing.T) {
	if got := celerity.WebSocketTag("sendMessage", "$default"); got != "sendMessage::$default" {
		t.Errorf("WebSocketTag = %q", got)
	}
	if got := celerity.SourceTag("orderQueue", "processOrder"); got != "source::orderQueue::processOrder" {
		t.Errorf("SourceTag = %q", got)
	}
	if got := celerity.CustomTag("recalculatePricing"); got != "custom::recalculatePricing" {
		t.Errorf("CustomTag = %q", got)
	}
}

func TestNormaliseRoute(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			if got := celerity.NormaliseRoute(tc.route); got != tc.want {
				t.Errorf("NormaliseRoute(%q) = %q, want %q", tc.route, got, tc.want)
			}
		})
	}
}
