package telebirr

import (
	"testing"
	"time"
)

func newPoolClient(t *testing.T, routes ...Route) *Client {
	t.Helper()
	return mustClient(t, WithRoutes(routes))
}

func TestRecordSuccess_ResetsBreakerAndPromotesActive(t *testing.T) {
	routeA := Route{ID: "a", Label: "a", Role: RolePreferred, URL: "https://a.example/"}
	routeB := Route{ID: "b", Label: "b", Role: RoleFallback, URL: "https://b.example/"}
	client := newPoolClient(t, routeA, routeB)

	client.recordTransportFailure(routeA, time.Minute, DefaultFailureThreshold)
	client.recordTransportFailure(routeA, time.Minute, DefaultFailureThreshold)
	client.mu.Lock()
	tripped := !client.routeState(routeA.URL).circuitOpenUntil.IsZero()
	client.mu.Unlock()
	if !tripped {
		t.Fatal("circuit did not trip after reaching the failure threshold")
	}

	client.recordSuccess(routeB, 120)
	client.mu.Lock()
	defer client.mu.Unlock()

	if client.active != routeB.URL {
		t.Errorf("active = %q, want %q", client.active, routeB.URL)
	}
	state := client.routeState(routeB.URL)
	switch {
	case state.consecutiveFailures != 0:
		t.Errorf("consecutiveFailures = %d after success", state.consecutiveFailures)
	case !state.hasLatency || state.averageLatencyMS != 120:
		t.Errorf("averageLatencyMS = %v (hasLatency=%v), want 120", state.averageLatencyMS, state.hasLatency)
	case state.lastSuccessAt.IsZero():
		t.Error("lastSuccessAt not recorded")
	}
}

func TestRecordFailure_EWMAAndTripThreshold(t *testing.T) {
	route := Route{ID: "a", Label: "a", Role: RolePreferred, URL: "https://a.example/"}
	client := newPoolClient(t, route)

	client.recordSuccess(route, 100)
	client.recordSuccess(route, 200)

	client.mu.Lock()
	state := client.routeState(route.URL)
	client.mu.Unlock()
	expectedEWMA := 100*0.7 + 200*0.3
	if state.averageLatencyMS != expectedEWMA {
		t.Errorf("averageLatencyMS = %v, want %v", state.averageLatencyMS, expectedEWMA)
	}

	client.recordTransportFailure(route, time.Minute, 2)
	client.mu.Lock()
	openAfterOne := client.routeState(route.URL).circuitOpenUntil
	client.mu.Unlock()
	if !openAfterOne.IsZero() {
		t.Error("circuit opened below the failure threshold")
	}

	client.recordTransportFailure(route, time.Minute, 2)
	client.mu.Lock()
	circuitOpenUntil := client.routeState(route.URL).circuitOpenUntil
	isActive := client.active == route.URL
	client.mu.Unlock()
	if circuitOpenUntil.IsZero() {
		t.Error("circuit did not open at the threshold")
	}
	if isActive {
		t.Error("tripped route remained active")
	}
}

func TestOrderedAvailableRoutes_PreferenceOrder(t *testing.T) {
	preferred := Route{ID: "p", Label: "p", Role: RolePreferred, URL: "https://p.example/"}
	fallback1 := Route{ID: "f1", Label: "f1", Role: RoleFallback, URL: "https://f1.example/"}
	fallback2 := Route{ID: "f2", Label: "f2", Role: RoleFallback, URL: "https://f2.example/"}
	client := newPoolClient(t, preferred, fallback1, fallback2)

	// Each success promotes its route to active, so fallback1 leads; the
	// rest follow by role (preferred outranks fallback) then recency.
	client.recordSuccess(fallback2, 300)
	client.recordSuccess(fallback1, 50)

	got := client.orderedAvailableRoutes(time.Now())
	expected := []string{fallback1.URL, preferred.URL, fallback2.URL}
	for i, want := range expected {
		if got[i].URL != want {
			t.Fatalf("order = %v, want %v", urlsOf(got), expected)
		}
	}

	// A successful preferred route also becomes the active route.
	client.recordSuccess(preferred, 900)
	got = client.orderedAvailableRoutes(time.Now())
	if got[0].URL != preferred.URL {
		t.Errorf("preferred route not first after success: %v", urlsOf(got))
	}
}

func TestOrderedAvailableRoutes_ActiveRouteFirst(t *testing.T) {
	first := Route{ID: "r0", Label: "r0", Role: RolePreferred, URL: "https://r0.example/"}
	second := Route{ID: "r1", Label: "r1", Role: RoleFallback, URL: "https://r1.example/"}
	client := newPoolClient(t, first, second)

	client.recordSuccess(second, 100)
	client.recordSuccess(first, 100)

	client.mu.Lock()
	active := client.active // last success promoted `first`
	client.mu.Unlock()
	if active != first.URL {
		t.Fatalf("active = %q, want %q", active, first.URL)
	}

	got := client.orderedAvailableRoutes(time.Now())
	if got[0].URL != first.URL || got[1].URL != second.URL {
		t.Errorf("order = %v, want active %q then %q", urlsOf(got), first.URL, second.URL)
	}
}

func TestOrderedAvailableRoutes_SkipsOpenCircuits(t *testing.T) {
	preferred := Route{ID: "p", Label: "p", Role: RolePreferred, URL: "https://p.example/"}
	fallback := Route{ID: "f", Label: "f", Role: RoleFallback, URL: "https://f.example/"}
	client := newPoolClient(t, preferred, fallback)

	client.recordTransportFailure(preferred, time.Minute, 1)

	got := client.orderedAvailableRoutes(time.Now())
	if len(got) != 1 || got[0].URL != fallback.URL {
		t.Errorf("order = %v, want only the healthy fallback", urlsOf(got))
	}

	// Once the cooldown lapses the preferred route returns to rotation.
	past := time.Now().Add(2 * time.Minute)
	got = client.orderedAvailableRoutes(past)
	if len(got) != 2 {
		t.Errorf("after cooldown order = %v, want both routes", urlsOf(got))
	}
}

func TestOrderedAvailableRoutes_HalfOpenRecovery(t *testing.T) {
	routeA := Route{ID: "a", Label: "a", Role: RolePreferred, URL: "https://a.example/"}
	routeB := Route{ID: "b", Label: "b", Role: RoleFallback, URL: "https://b.example/"}
	client := newPoolClient(t, routeA, routeB)

	client.recordTransportFailure(routeA, time.Hour, 1)
	time.Sleep(time.Millisecond)
	client.recordTransportFailure(routeB, time.Minute, 1)

	got := client.orderedAvailableRoutes(time.Now())
	if len(got) != 1 {
		t.Fatalf("half-open candidates = %d, want exactly one", len(got))
	}
	// routeB's circuit expires sooner; it must be the recovery candidate.
	if got[0].URL != routeB.URL {
		t.Errorf("recovery candidate = %q, want %q", got[0].URL, routeB.URL)
	}
}

func TestOrderedAvailableRoutes_EmptyPool(t *testing.T) {
	client := newPoolClient(t)
	if got := client.orderedAvailableRoutes(time.Now()); len(got) != 0 {
		t.Errorf("empty pool returned %v", urlsOf(got))
	}
}

func urlsOf(routes []Route) []string {
	urls := make([]string, len(routes))
	for i, r := range routes {
		urls[i] = r.URL
	}
	return urls
}
