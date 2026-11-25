package openrouter

import (
	"testing"
	"time"
)

func TestResolveEndpointHealth(t *testing.T) {
	f := false
	tests := []struct {
		name    string
		enabled *bool
		ttl     int32
		want    EndpointHealthOptions
	}{
		{"defaults", nil, 0, EndpointHealthOptions{Enabled: true, TTL: 60 * time.Second}},
		{"negative ttl uses default", nil, -5, EndpointHealthOptions{Enabled: true, TTL: 60 * time.Second}},
		{"override", &f, 15, EndpointHealthOptions{Enabled: false, TTL: 15 * time.Second}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveEndpointHealth(tc.enabled, tc.ttl); got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestNewClientDefaultsAndOverride(t *testing.T) {
	c := New("k", "", "m", 0, nil)
	if got := c.HealthOptions(); !got.Enabled || got.TTL != DefaultEndpointHealthTTL {
		t.Fatalf("default: %+v", got)
	}
	f := false
	c.WithEndpointHealth(ResolveEndpointHealth(&f, 5))
	if got := c.HealthOptions(); got.Enabled || got.TTL != 5*time.Second {
		t.Fatalf("override: %+v", got)
	}
}
