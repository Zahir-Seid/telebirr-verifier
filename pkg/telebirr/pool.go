package telebirr

import (
	"sort"
	"time"
)

// RouteHealth reports whether a relay answered a probe with a usable receipt.
type RouteHealth int

const (
	// RouteHealthUnknown is the zero value.
	RouteHealthUnknown RouteHealth = iota

	// RouteHealthOperational means the relay returned a valid receipt.
	RouteHealthOperational

	// RouteHealthUnavailable means the relay failed or answered unusably.
	RouteHealthUnavailable
)

// String implements fmt.Stringer.
func (h RouteHealth) String() string {
	switch h {
	case RouteHealthOperational:
		return "operational"
	case RouteHealthUnavailable:
		return "unavailable"
	default:
		return "unknown"
	}
}

// RouteStatus is the outcome of probing one relay route.
type RouteStatus struct {
	ID        string
	Label     string
	Role      Role
	Status    RouteHealth
	LatencyMS int64
}

// ProbeDetails aggregates [Client.Probe] results across the whole pool.
type ProbeDetails struct {
	ActiveRouteID      string // first operational route, empty when none
	PreferredAvailable bool   // whether the preferred route is operational
	Routes             []RouteStatus
}

// routeState is the circuit breaker bookkeeping kept per relay URL.
type routeState struct {
	consecutiveFailures int
	circuitOpenUntil    time.Time
	lastSuccessAt       time.Time
	averageLatencyMS    float64
	hasLatency          bool
}

// recordSuccess marks a route healthy, folds its latency into an EWMA
// (70% previous, 30% latest) and promotes it to the active route.
func (c *Client) recordSuccess(route Route, latencyMS int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := c.routeState(route.URL)
	state.consecutiveFailures = 0
	state.circuitOpenUntil = time.Time{}
	state.lastSuccessAt = time.Now()
	if !state.hasLatency {
		state.averageLatencyMS = float64(latencyMS)
		state.hasLatency = true
	} else {
		state.averageLatencyMS = state.averageLatencyMS*0.7 + float64(latencyMS)*0.3
	}
	c.active = route.URL
}

// recordTransportFailure counts a transport failure and trips the circuit
// breaker once the threshold is reached. An active route that trips is
// demoted.
func (c *Client) recordTransportFailure(route Route, cooldown time.Duration, threshold int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := c.routeState(route.URL)
	state.consecutiveFailures++
	if state.consecutiveFailures >= threshold {
		state.circuitOpenUntil = time.Now().Add(cooldown)
	} else {
		state.circuitOpenUntil = time.Time{}
	}
	if !state.circuitOpenUntil.IsZero() && c.active == route.URL {
		c.active = ""
	}
}

// routeState returns the bookkeeping entry for a URL, creating it if needed.
// The caller must hold c.mu.
func (c *Client) routeState(url string) *routeState {
	state, ok := c.state[url]
	if !ok {
		state = &routeState{}
		c.state[url] = state
	}
	return state
}

// orderedAvailableRoutes returns candidates best-first: the active route,
// then preferred role, then most recently successful, then lowest average
// latency, then configuration order. Routes with open circuits are skipped;
// if every circuit is open, exactly one half-open recovery candidate (the one
// whose cooldown expires soonest) is returned so stale breaker state can
// never make all routes permanently unreachable.
func (c *Client) orderedAvailableRoutes(now time.Time) []Route {
	c.mu.Lock()
	defer c.mu.Unlock()

	candidates := make([]Route, len(c.routes))
	copy(candidates, c.routes)

	originalOrder := make(map[string]int, len(candidates))
	for i, r := range candidates {
		originalOrder[r.URL] = i
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]

		leftActive, rightActive := left.URL == c.active, right.URL == c.active
		if leftActive != rightActive {
			return leftActive
		}
		if left.Role != right.Role {
			return left.Role == RolePreferred
		}
		leftState, rightState := c.routeState(left.URL), c.routeState(right.URL)
		if !leftState.lastSuccessAt.Equal(rightState.lastSuccessAt) {
			return leftState.lastSuccessAt.After(rightState.lastSuccessAt)
		}
		leftLatency, rightLatency := latencyOrMax(leftState), latencyOrMax(rightState)
		if leftLatency != rightLatency {
			return leftLatency < rightLatency
		}
		return originalOrder[left.URL] < originalOrder[right.URL]
	})

	available := make([]Route, 0, len(candidates))
	for _, route := range candidates {
		if c.routeState(route.URL).circuitOpenUntil.After(now) {
			continue
		}
		available = append(available, route)
	}
	if len(available) > 0 {
		return available
	}
	if len(candidates) == 0 {
		return nil
	}

	soonestRecovery := candidates[0]
	for _, route := range candidates[1:] {
		if c.routeState(route.URL).circuitOpenUntil.Before(c.routeState(soonestRecovery.URL).circuitOpenUntil) {
			soonestRecovery = route
		}
	}
	return []Route{soonestRecovery}
}

func latencyOrMax(state *routeState) float64 {
	if !state.hasLatency {
		return float64(int64(^uint64(0) >> 1))
	}
	return state.averageLatencyMS
}
