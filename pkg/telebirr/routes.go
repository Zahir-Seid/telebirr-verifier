package telebirr

import (
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Role classifies a fallback route within the route pool.
type Role int

const (
	// RoleUnknown is the zero value; it never appears on routes returned by
	// RoutesFromEnv.
	RoleUnknown Role = iota

	// RolePreferred marks the first configured route. It is tried first and
	// is reported separately by [Client.Probe].
	RolePreferred

	// RoleFallback marks every route after the first.
	RoleFallback
)

// String implements fmt.Stringer.
func (r Role) String() string {
	switch r {
	case RolePreferred:
		return "preferred"
	case RoleFallback:
		return "fallback"
	default:
		return "unknown"
	}
}

// Route describes one fallback relay endpoint that can resolve receipt
// references as JSON (or HTML). The URL must be a prefix such that appending
// the reference number yields a fetchable address, e.g.
// "https://relay.example.et/api/receipt?ref=".
type Route struct {
	ID    string // stable identifier, e.g. "preferred" or "relay-1"
	Label string // short public name used in logs and probes
	Role  Role
	URL   string
}

// Environment variables recognised by the package.
const (
	// EnvFallbackProxies is a comma-separated list of relay URL prefixes.
	EnvFallbackProxies = "FALLBACK_PROXIES"

	// EnvProxyLabels optionally carries comma-separated public labels for
	// the routes in EnvFallbackProxies.
	EnvProxyLabels = "TELEBIRR_PROXY_LABELS"

	// EnvProxyKey is appended to relay URLs as &key=<value> when set.
	EnvProxyKey = "TELEBIRR_PROXY_KEY"

	// EnvProxyTimeoutMs caps a single relay attempt (default 18000).
	EnvProxyTimeoutMs = "TELEBIRR_PROXY_TIMEOUT_MS"

	// EnvHedgeDelayMs delays launching an extra parallel attempt (default 1000).
	EnvHedgeDelayMs = "TELEBIRR_HEDGE_DELAY_MS"

	// EnvProxyCooldownMs sets the circuit breaker cooldown (default 60000).
	EnvProxyCooldownMs = "TELEBIRR_PROXY_COOLDOWN_MS"

	// EnvTotalTimeoutMs bounds one full verification (default 20000).
	EnvTotalTimeoutMs = "TELEBIRR_TOTAL_TIMEOUT_MS"

	// EnvProxyFailureThreshold opens a circuit after N consecutive
	// transport failures on one route (default 2).
	EnvProxyFailureThreshold = "TELEBIRR_PROXY_FAILURE_THRESHOLD"

	// EnvMaxParallelProxies caps concurrent relay attempts, clamped to 1..4
	// (default 2).
	EnvMaxParallelProxies = "TELEBIRR_MAX_PARALLEL_PROXIES"

	// EnvSkipPrimaryVerification skips the official source when "true".
	EnvSkipPrimaryVerification = "SKIP_PRIMARY_VERIFICATION"
)

// LookupFunc resolves environment variables; it enables tests and embedders to
// substitute their own configuration source. It may return "" for unset keys.
type LookupFunc func(key string) string

// OSLookup reads from the process environment.
func OSLookup(key string) string { return os.Getenv(key) }

var labelPattern = regexp.MustCompile(`^[A-Za-z0-9 ._-]{1,32}$`)

// RoutesFromEnv builds the fallback route list from comma-separated URLs in
// FALLBACK_PROXIES, optionally labelled via TELEBIRR_PROXY_LABELS. The first
// URL becomes the preferred route. Invalid or empty entries are skipped.
//
// A nil lookup falls back to [OSLookup].
func RoutesFromEnv(lookup LookupFunc) []Route {
	if lookup == nil {
		lookup = OSLookup
	}

	rawURLs := splitCSV(lookup(EnvFallbackProxies))
	if len(rawURLs) == 0 {
		return nil
	}
	rawLabels := strings.Split(lookup(EnvProxyLabels)+",", ",")

	routes := make([]Route, 0, len(rawURLs))
	for i, rawURL := range rawURLs {
		routeURL := strings.TrimSpace(rawURL)
		if routeURL == "" {
			continue
		}
		role := RoleFallback
		id := "relay-" + strconv.Itoa(len(routes))
		if len(routes) == 0 {
			role = RolePreferred
			id = "preferred"
		}
		label := ""
		if i < len(rawLabels) {
			label = safePublicLabel(rawLabels[i])
		}
		if label == "" {
			label = defaultRouteLabel(routeURL, len(routes))
		}
		routes = append(routes, Route{ID: id, Label: label, Role: role, URL: routeURL})
	}
	return routes
}

// safePublicLabel normalises a label and rejects values outside a conservative
// allow-list so hostile env content cannot leak into logs or APIs.
func safePublicLabel(value string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if normalized == "" || !labelPattern.MatchString(normalized) {
		return ""
	}
	return normalized
}

// defaultRouteLabel derives a public label when none is configured: the
// hostname for the well-known leul.et relays, positional names otherwise.
func defaultRouteLabel(rawURL string, index int) string {
	if index == 0 {
		if parsed, err := url.Parse(rawURL); err == nil && parsed.Hostname() != "" {
			host := strings.ToLower(parsed.Hostname())
			if host == "leul.et" || strings.HasSuffix(host, ".leul.et") {
				return "leul.et"
			}
		}
		return "Preferred relay"
	}
	return "Community relay " + strconv.Itoa(index)
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}
