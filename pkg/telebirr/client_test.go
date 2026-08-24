package telebirr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func mustClient(t *testing.T, opts ...Option) *Client {
	t.Helper()
	c, err := New(opts...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return c
}

// relayServer serves a successful JSON envelope for any reference.
func relayServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRelaySuccess(t, w)
	}))
}

func writeRelaySuccess(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	data := relayData{
		PayerName:              "ABEBE KEBEDE",
		PayerTelebirrNo:        "0711234567",
		CreditedPartyName:      "SOLOMON TESFAYE",
		CreditedPartyAccountNo: "0729876543",
		TransactionStatus:      "Successful",
		ReceiptNo:              "CBJ0H74269",
		PaymentDate:            "24-08-2026 14:35:12",
		SettledAmount:          "1,500.00 Birr",
		ServiceFee:             "15.00 Birr",
		ServiceFeeVAT:          "1.95 Birr",
		TotalPaidAmount:        "1,516.95 Birr",
		CustomerNote:           "August rent",
	}
	if err := json.NewEncoder(w).Encode(relayEnvelope{Success: boolPtr(true), Data: &data}); err != nil {
		t.Errorf("encode relay envelope: %v", err)
	}
}

func boolPtr(b bool) *bool { return &b }

const testReference = "CBJ0H74269"

func primaryServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(loadFixture(t, "receipt.html")))
	}))
}

func TestNew_OptionValidation(t *testing.T) {
	tests := []struct {
		name    string
		option  Option
		wantErr bool
	}{
		{"nil http client", WithHTTPClient(nil), true},
		{"empty primary url", WithPrimaryURL(""), true},
		{"zero proxy timeout", WithProxyTimeout(0), true},
		{"negative hedge delay", WithHedgeDelay(-time.Second), true},
		{"zero cooldown", WithCooldown(0), true},
		{"zero total timeout", WithTotalTimeout(0), true},
		{"zero primary timeout", WithPrimaryTimeout(0), true},
		{"zero failure threshold", WithFailureThreshold(0), true},
		{"zero max parallel", WithMaxParallelRoutes(0), true},
		{"valid options", WithProxyTimeout(time.Second), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(tt.option)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && client == nil {
				t.Error("New() returned nil client without an error")
			}
		})
	}
}

func TestNew_Defaults(t *testing.T) {
	client := mustClient(t)
	switch {
	case client.primaryURL != DefaultPrimaryURL:
		t.Errorf("primaryURL = %q", client.primaryURL)
	case client.proxyTimeout != DefaultProxyTimeout:
		t.Errorf("proxyTimeout = %v", client.proxyTimeout)
	case client.hedgeDelay != DefaultHedgeDelay:
		t.Errorf("hedgeDelay = %v", client.hedgeDelay)
	case client.totalTimeout != DefaultTotalTimeout:
		t.Errorf("totalTimeout = %v", client.totalTimeout)
	case client.failureThreshold != DefaultFailureThreshold:
		t.Errorf("failureThreshold = %d", client.failureThreshold)
	case client.maxParallel != DefaultMaxParallel:
		t.Errorf("maxParallel = %d", client.maxParallel)
	}
}

func TestWithMaxParallelRoutes_Clamped(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{"minimum kept", 1, 1},
		{"within range kept", 3, 3},
		{"above range clamps to four", 100, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := mustClient(t, WithMaxParallelRoutes(tt.input))
			if client.maxParallel != tt.expected {
				t.Errorf("maxParallel = %d, want %d", client.maxParallel, tt.expected)
			}
		})
	}
}

func TestVerify_InvalidReference(t *testing.T) {
	tests := []struct {
		name      string
		reference string
	}{
		{"empty reference", ""},
		{"spaces inside", "CBJ 042"},
		{"path traversal", "../../etc/passwd"},
		{"query injection", "abc?x=1"},
		{"unicode", "ቀረጻ"},
		{"too long", strings.Repeat("A", 65)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := mustClient(t)
			receipt, err := client.Verify(context.Background(), tt.reference)
			if !errors.Is(err, ErrInvalidReference) {
				t.Errorf("error = %v, want ErrInvalidReference", err)
			}
			if receipt != nil {
				t.Errorf("receipt = %+v, want nil", receipt)
			}
		})
	}
}

func TestVerify_ValidReferencesAccepted(t *testing.T) {
	tests := []struct {
		name      string
		reference string
	}{
		{"alphanumeric", "CBJ0H74269"},
		{"punctuation mix", "a.b_c-9"},
		{"maximum length", strings.Repeat("A", 64)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := primaryServer(t, http.StatusOK)
			defer server.Close()

			client := mustClient(t, WithPrimaryURL(server.URL+"/"))
			if _, err := client.Verify(context.Background(), tt.reference); err != nil {
				t.Errorf("Verify(%q) error = %v, want nil", tt.reference, err)
			}
		})
	}
}

func TestVerify_PrimarySuccess(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(loadFixture(t, "receipt.html")))
	}))
	defer server.Close()

	client := mustClient(t, WithPrimaryURL(server.URL+"/receipt/"))
	receipt, err := client.Verify(context.Background(), testReference)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if gotPath != "/receipt/"+testReference {
		t.Errorf("upstream path = %q, want %q", gotPath, "/receipt/"+testReference)
	}
	expected := ScrapeReceipt(loadFixture(t, "receipt.html"))
	if *receipt != expected {
		t.Errorf("receipt =\n%+v\nwant\n%+v", *receipt, expected)
	}
}

func TestVerify_PrimaryFails_RelayJSONSucceeds(t *testing.T) {
	primary := primaryServer(t, http.StatusInternalServerError)
	defer primary.Close()
	relay := relayServer(t)
	defer relay.Close()

	client := mustClient(t,
		WithPrimaryURL(primary.URL+"/"),
		WithRoutes([]Route{{URL: relay.URL + "?ref=", Label: "test relay"}}),
	)
	receipt, err := client.Verify(context.Background(), testReference)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if receipt.ReceiptNo != "CBJ0H74269" || receipt.TransactionStatus != "Successful" {
		t.Errorf("unexpected receipt: %+v", receipt)
	}
}

func TestVerify_AppendsProxyKey(t *testing.T) {
	var gotRawQuery string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		writeRelaySuccess(t, w)
	}))
	defer relay.Close()

	client := mustClient(t,
		WithSkipPrimary(true),
		WithProxyKey("sekrit"),
		WithRoutes([]Route{{URL: relay.URL + "/verify?ref="}}),
	)
	if _, err := client.Verify(context.Background(), testReference); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !strings.Contains(gotRawQuery, "key=sekrit") || !strings.Contains(gotRawQuery, "ref="+testReference) {
		t.Errorf("raw query = %q, want ref=%s and key=sekrit", gotRawQuery, testReference)
	}
}

// Regression: path-style relay prefixes (no existing "?") must receive the
// key with a "?" separator, and key values need URL escaping.
func TestVerify_AppendsProxyKeyToPathStyleRoute(t *testing.T) {
	var gotURI string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.RequestURI
		writeRelaySuccess(t, w)
	}))
	defer relay.Close()

	client := mustClient(t,
		WithSkipPrimary(true),
		WithProxyKey("abc 123/x+y"),
		WithRoutes([]Route{{URL: relay.URL + "/receipt/"}}),
	)
	if _, err := client.Verify(context.Background(), testReference); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	want := "/receipt/" + testReference + "?key=" + url.QueryEscape("abc 123/x+y")
	if gotURI != want {
		t.Errorf("RequestURI = %q, want %q", gotURI, want)
	}
}

func TestVerify_DomainRejectionOnly_ReturnsNotFound(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"error":   "receipt not found",
		})
	}))
	defer relay.Close()

	client := mustClient(t,
		WithSkipPrimary(true),
		WithRoutes([]Route{{URL: relay.URL + "?"}}),
	)
	receipt, err := client.Verify(context.Background(), testReference)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
	if receipt != nil {
		t.Errorf("receipt = %+v, want nil", receipt)
	}
}

func TestVerify_TransportFailureOnly_Propagates(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	dead.Close() // guaranteed refused connections

	client := mustClient(t,
		WithSkipPrimary(true),
		WithRoutes([]Route{{URL: dead.URL + "?"}}),
		WithTotalTimeout(2*time.Second),
	)
	_, err := client.Verify(context.Background(), testReference)

	var verificationErr *VerificationError
	if !errors.As(err, &verificationErr) {
		t.Fatalf("error = %v, want *VerificationError", err)
	}
	if verificationErr.Kind != ErrorTransport {
		t.Errorf("Kind = %v, want %v", verificationErr.Kind, ErrorTransport)
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("transport failure must surface itself, not ErrNotFound")
	}
}

func TestVerify_DomainPlusTransport_ReturnsNotFound(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	dead.Close()

	domainRejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "nope"})
	}))
	defer domainRejecting.Close()

	client := mustClient(t,
		WithSkipPrimary(true),
		WithRoutes([]Route{
			{ID: "preferred", Role: RolePreferred, URL: dead.URL + "?ref="},
			{ID: "relay-1", Role: RoleFallback, URL: domainRejecting.URL + "?ref="},
		}),
		WithTotalTimeout(2*time.Second),
	)
	receipt, err := client.Verify(context.Background(), testReference)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound (mixed failures collapse to not-found)", err)
	}
	if receipt != nil {
		t.Errorf("receipt = %+v, want nil", receipt)
	}
}

func TestVerify_SkipPrimaryWithoutRoutes(t *testing.T) {
	client := mustClient(t, WithSkipPrimary(true))
	receipt, err := client.Verify(context.Background(), testReference)
	if !errors.Is(err, ErrNoRoutes) {
		t.Errorf("error = %v, want ErrNoRoutes", err)
	}
	if receipt != nil {
		t.Errorf("receipt = %+v, want nil", receipt)
	}
}

func TestVerify_CancelledBeforeCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := mustClient(t)
	_, err := client.Verify(ctx, testReference)

	var verificationErr *VerificationError
	if !errors.As(err, &verificationErr) {
		t.Fatalf("error = %v, want *VerificationError", err)
	}
	if verificationErr.Kind != ErrorCancelled {
		t.Errorf("Kind = %v, want %v", verificationErr.Kind, ErrorCancelled)
	}
}

func TestVerify_CancelledDuringPrimary(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slow.Close()

	client := mustClient(t, WithPrimaryURL(slow.URL+"/"))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	startedAt := time.Now()
	_, err := client.Verify(ctx, testReference)
	if time.Since(startedAt) > 5*time.Second {
		t.Error("Verify() ignored cancellation and blocked")
	}

	var verificationErr *VerificationError
	if !errors.As(err, &verificationErr) || verificationErr.Kind != ErrorCancelled {
		t.Errorf("error = %v, want cancelled verification error", err)
	}
}

func TestVerify_FastRouteWinsWhileSlowSiblingCancelled(t *testing.T) {
	siblingDone := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		close(siblingDone)
	}))
	defer slow.Close()

	fast := relayServer(t)
	defer fast.Close()

	client := mustClient(t,
		WithSkipPrimary(true),
		WithRoutes([]Route{
			{ID: "preferred", Role: RolePreferred, URL: slow.URL + "?ref="},
			{ID: "relay-1", Role: RoleFallback, URL: fast.URL + "?ref="},
		}),
		WithTotalTimeout(5*time.Second),
		WithProxyTimeout(9*time.Second),
	)

	startedAt := time.Now()
	receipt, err := client.Verify(context.Background(), testReference)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 5*time.Second {
		t.Errorf("Verify() took %v; the fast route should win immediately", elapsed)
	}
	if receipt.ReceiptNo != "CBJ0H74269" {
		t.Errorf("receipt from wrong route: %+v", receipt)
	}

	select {
	case <-siblingDone:
		t.Log("slow sibling observed cancellation")
	case <-time.After(2 * time.Second):
		t.Error("slow sibling was never cancelled after success")
	}
}

func TestVerify_ConcurrentCallsShareStateSafely(t *testing.T) {
	relay := relayServer(t)
	defer relay.Close()

	client := mustClient(t,
		WithSkipPrimary(true),
		WithRoutes(RoutesFromEnv(lookupWith(map[string]string{
			EnvFallbackProxies: strings.Join([]string{relay.URL + "?ref=", relay.URL + "&ref="}, ","),
		}))),
	)

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range cap(errs) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.Verify(context.Background(), testReference)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Verify() error = %v", err)
		}
	}
}

func TestProbe_RouteStatuses(t *testing.T) {
	good := relayServer(t)
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer bad.Close()

	client := mustClient(t,
		WithSkipPrimary(true),
		WithRoutes([]Route{
			{ID: "relay-1", Label: "bad", Role: RolePreferred, URL: bad.URL + "?ref="},
			{ID: "relay-2", Label: "good", Role: RoleFallback, URL: good.URL + "?ref="},
		}),
	)

	details := client.Probe(context.Background(), testReference)
	switch {
	case details.ActiveRouteID != "relay-2":
		t.Errorf("ActiveRouteID = %q, want relay-2", details.ActiveRouteID)
	case details.PreferredAvailable:
		t.Error("PreferredAvailable = true for a failing preferred route")
	}
	if len(details.Routes) != 2 {
		t.Fatalf("got %d route statuses, want 2", len(details.Routes))
	}
	for _, status := range details.Routes {
		expectedHealth := RouteHealthUnavailable
		if status.ID == "relay-2" {
			expectedHealth = RouteHealthOperational
		}
		if status.Status != expectedHealth {
			t.Errorf("route %s status = %v, want %v", status.ID, status.Status, expectedHealth)
		}
		if status.LatencyMS < 0 {
			t.Errorf("route %s latency = %dms, want >= 0", status.ID, status.LatencyMS)
		}
	}
}

func TestFetchRoute_OutcomeClassification(t *testing.T) {
	htmlBody := loadFixture(t, "receipt.html")

	tests := []struct {
		name         string
		handler      http.HandlerFunc
		wantReceipt  bool
		wantValid    bool
		wantRejected bool
		wantKind     ErrorKind
	}{
		{
			name: "json envelope parsed",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				writeRelaySuccess(t, w)
			},
			wantReceipt: true,
			wantValid:   true,
		},
		{
			name: "domain rejection",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "not found", "details": "db"})
			},
			wantKind: ErrorDomain,
		},
		{
			name: "html fallback scraped",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(htmlBody))
			},
			wantReceipt: true,
			wantValid:   true,
		},
		{
			name: "malformed json scraped as html",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("{not json"))
			},
			wantReceipt: true,
		},
		{
			name: "status not found rejected silently",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.NotFound(w, r)
			},
			wantRejected: true,
		},
		{
			name: "server error classified transport",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			wantKind: ErrorTransport,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			client := mustClient(t)
			route := Route{ID: "t", Label: "test", Role: RolePreferred, URL: server.URL + "?ref="}
			receipt, err := client.fetchRoute(context.Background(), route, testReference)

			switch {
			case tt.wantRejected:
				var rejected requestRejectedError
				if !errors.As(err, &rejected) {
					t.Fatalf("error = %v, want requestRejectedError", err)
				}
			case tt.wantKind != 0:
				var verificationErr *VerificationError
				if !errors.As(err, &verificationErr) {
					t.Fatalf("error = %v, want *VerificationError", err)
				}
				if verificationErr.Kind != tt.wantKind {
					t.Errorf("Kind = %v, want %v", verificationErr.Kind, tt.wantKind)
				}
			default:
				if err != nil {
					t.Fatalf("fetchRoute() error = %v", err)
				}
				if (tt.wantValid && !receipt.IsValid()) || (!tt.wantValid && receipt.IsValid()) {
					t.Errorf("receipt validity = %v, want %v (receipt: %+v)", receipt.IsValid(), tt.wantValid, receipt)
				}
			}
		})
	}
}

func TestParseRelayBody(t *testing.T) {
	validEnvelope := func(data relayData) string {
		raw, _ := json.Marshal(relayEnvelope{Success: boolPtr(true), Data: &data})
		return string(raw)
	}

	tests := []struct {
		name       string
		body       string
		wantScrape bool
		wantErr    string
	}{
		{name: "html body scrapes", body: "<html></html>", wantScrape: true},
		{name: "array body scrapes", body: `[{"a":1}]`, wantScrape: true},
		{name: "malformed object scrapes", body: "{oops", wantScrape: true},
		{name: "missing success flag scrapes", body: `{"data":{}}`, wantScrape: true},
		{name: "success without data scrapes", body: `{"success":true}`, wantScrape: true},
		{
			name:    "domain rejection surfaces message",
			body:    `{"success":false,"error":"receipt does not exist","details":"x"}`,
			wantErr: "receipt does not exist",
		},
		{
			name:       "success false without error scrapes",
			body:       `{"success":false}`,
			wantScrape: true,
		},
		{
			name: "full envelope parses",
			body: validEnvelope(relayData{ReceiptNo: "R1", PayerName: "P", TransactionStatus: "Successful"}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt, relayErr, parseFailed := parseRelayBody(tt.body)

			if relayErr != tt.wantErr {
				t.Errorf("relayErr = %q, want %q", relayErr, tt.wantErr)
			}
			if parseFailed != tt.wantScrape {
				t.Errorf("parseFailed = %v, want %v", parseFailed, tt.wantScrape)
			}
			if !tt.wantScrape && tt.wantErr == "" && receipt == nil {
				t.Error("receipt = nil for a parseable envelope")
			}
			if tt.wantScrape && receipt != nil {
				t.Error("receipt set although scraping was requested")
			}
		})
	}
}

func TestNewFromEnv_LayersOptionsOverEnvironment(t *testing.T) {
	env := map[string]string{
		EnvFallbackProxies:         "https://leul.et/api?ref=",
		EnvProxyKey:                "envkey",
		EnvSkipPrimaryVerification: "true",
		EnvProxyTimeoutMs:          "5000",
		EnvTotalTimeoutMs:          "9000",
		EnvMaxParallelProxies:      "99",
	}
	override := mustClient(t, WithProxyTimeout(3*time.Second))

	fromEnv, err := NewFromEnv(lookupWith(env))
	if err != nil {
		t.Fatalf("NewFromEnv() error = %v", err)
	}
	if len(fromEnv.routes) != 1 || fromEnv.routes[0].Label != "leul.et" {
		t.Errorf("routes = %+v", fromEnv.routes)
	}
	if fromEnv.proxyKey != "envkey" {
		t.Errorf("proxyKey = %q", fromEnv.proxyKey)
	}
	if !fromEnv.skipPrimary {
		t.Error("skipPrimary = false, want true")
	}
	if fromEnv.proxyTimeout != 5*time.Second {
		t.Errorf("proxyTimeout = %v, want 5s", fromEnv.proxyTimeout)
	}
	if fromEnv.totalTimeout != 9*time.Second {
		t.Errorf("totalTimeout = %v, want 9s", fromEnv.totalTimeout)
	}
	if fromEnv.maxParallel != maxMaxParallel {
		t.Errorf("maxParallel = %d, want clamp to %d", fromEnv.maxParallel, maxMaxParallel)
	}
	if override.proxyTimeout != 3*time.Second {
		t.Errorf("explicit option lost: proxyTimeout = %v", override.proxyTimeout)
	}
}

func TestVerify_ProxyTimeoutBoundsAttempt(t *testing.T) {
	hung := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-hung
	}))
	defer slow.Close()
	defer close(hung) // runs before slow.Close() so handlers can finish

	client := mustClient(t,
		WithSkipPrimary(true),
		WithRoutes([]Route{{URL: slow.URL + "?ref="}}),
		WithProxyTimeout(100*time.Millisecond),
		WithTotalTimeout(5*time.Second),
	)

	startedAt := time.Now()
	_, err := client.Verify(context.Background(), testReference)
	if elapsed := time.Since(startedAt); elapsed > 2*time.Second {
		t.Errorf("attempt outlived its proxy timeout: %v", elapsed)
	}

	var verificationErr *VerificationError
	if !errors.As(err, &verificationErr) || verificationErr.Kind != ErrorTransport {
		t.Errorf("error = %v, want transport verification error", err)
	}
}

func ExampleNew() {
	client, err := New(
		WithRoutes(RoutesFromEnv(nil)),
		WithTotalTimeout(20*time.Second),
	)
	if err != nil {
		fmt.Println("client:", err)
		return
	}
	receipt, err := client.Verify(context.Background(), "CBJ0H74269")
	switch {
	case errors.Is(err, ErrNotFound):
		fmt.Println("unknown receipt")
	case err != nil:
		fmt.Println("verification failed:", err)
	default:
		fmt.Println(receipt.PayerName, receipt.SettledAmount)
	}
}

// Regression: HTTP 429 is transport-classifiable so the circuit breaker
// learns about rate limiting instead of silently moving on.
func TestFetchRoute_RateLimitedIsTransport(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := mustClient(t)
	_, err := client.fetchRoute(context.Background(),
		Route{ID: "r", Label: "rate-limited relay", URL: server.URL + "/"}, testReference)

	var verr *VerificationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v, want *VerificationError", err)
	}
	if verr.Kind != ErrorTransport {
		t.Errorf("Kind = %s, want transport", verr.Kind)
	}
}

// Regression: Probe bounds each attempt by the proxy timeout; a hung relay
// cannot stall the whole probe.
func TestProbe_BoundedByProxyTimeout(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer func() { close(release); server.Close() }()

	client := mustClient(t,
		WithSkipPrimary(true),
		WithProxyTimeout(150*time.Millisecond),
		WithRoutes([]Route{{URL: server.URL + "/"}}),
	)

	done := make(chan ProbeDetails, 1)
	go func() { done <- client.Probe(context.Background(), testReference) }()

	select {
	case details := <-done:
		if len(details.Routes) != 1 || details.Routes[0].Status != RouteHealthUnavailable {
			t.Fatalf("details = %+v, want one unavailable route", details.Routes)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Probe did not return within the proxy timeout bound")
	}
}
