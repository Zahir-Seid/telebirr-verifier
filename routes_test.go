package telebirr

import (
	"reflect"
	"testing"
)

func lookupWith(env map[string]string) LookupFunc {
	return func(key string) string { return env[key] }
}

func TestRoutesFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		expected []Route
	}{
		{
			name:     "unset variables",
			env:      map[string]string{},
			expected: nil,
		},
		{
			name: "single url becomes preferred",
			env: map[string]string{
				EnvFallbackProxies: "https://leul.et/api/receipt?ref=",
			},
			expected: []Route{
				{ID: "preferred", Label: "leul.et", Role: RolePreferred, URL: "https://leul.et/api/receipt?ref="},
			},
		},
		{
			name: "multiple urls with labels",
			env: map[string]string{
				EnvFallbackProxies: "https://a.et/r?ref=, https://b.et/r?ref=",
				EnvProxyLabels:     "Alpha,Beta",
			},
			expected: []Route{
				{ID: "preferred", Label: "Alpha", Role: RolePreferred, URL: "https://a.et/r?ref="},
				{ID: "relay-1", Label: "Beta", Role: RoleFallback, URL: "https://b.et/r?ref="},
			},
		},
		{
			name: "subdomain of leul.et keeps default label",
			env: map[string]string{
				EnvFallbackProxies: "https://api.leul.et/verify?ref=,https://x.example.com/",
			},
			expected: []Route{
				{ID: "preferred", Label: "leul.et", Role: RolePreferred, URL: "https://api.leul.et/verify?ref="},
				{ID: "relay-1", Label: "Community relay 1", Role: RoleFallback, URL: "https://x.example.com/"},
			},
		},
		{
			name: "non leul.et preferred gets generic label",
			env: map[string]string{
				EnvFallbackProxies: "https://relay.example.org/?ref=",
			},
			expected: []Route{
				{ID: "preferred", Label: "Preferred relay", Role: RolePreferred, URL: "https://relay.example.org/?ref="},
			},
		},
		{
			name: "unsafe labels fall back to defaults",
			env: map[string]string{
				EnvFallbackProxies: "https://a.et/,https://b.et/",
				EnvProxyLabels:     "has spaces & symbols!,ok_name",
			},
			expected: []Route{
				{ID: "preferred", Label: "Preferred relay", Role: RolePreferred, URL: "https://a.et/"},
				{ID: "relay-1", Label: "ok_name", Role: RoleFallback, URL: "https://b.et/"},
			},
		},
		{
			name: "empty entries are skipped",
			env: map[string]string{
				EnvFallbackProxies: " , https://c.et/ ,",
			},
			expected: []Route{
				{ID: "preferred", Label: "Preferred relay", Role: RolePreferred, URL: "https://c.et/"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RoutesFromEnv(lookupWith(tt.env))
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("RoutesFromEnv() = %+v\nwant %+v", got, tt.expected)
			}
		})
	}
}

func TestSafePublicLabel(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"with spaces ", "with spaces"},
		{"collapsed   spaces", "collapsed spaces"},
		{"dots-dashes_under", "dots-dashes_under"},
		{"", ""},
		{"   ", ""},
		{"symbol$rejected", ""},
		{"unicode ትዕይንት", ""},
		{"this label is far too long for the allow list", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := safePublicLabel(tt.input); got != tt.expected {
				t.Errorf("safePublicLabel(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
