package telebirr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultPrimaryURL is the official Telebirr transaction page prefix; the
// reference number is appended to it.
const DefaultPrimaryURL = "https://transactioninfo.ethiotelecom.et/receipt/"

// Defaults applied by New unless overridden with options. They mirror the
// reference implementation's environment fallbacks.
const (
	DefaultProxyTimeout     = 18 * time.Second
	DefaultHedgeDelay       = 1 * time.Second
	DefaultCooldown         = 60 * time.Second
	DefaultTotalTimeout     = 20 * time.Second
	DefaultPrimaryTimeout   = 30 * time.Second
	DefaultFailureThreshold = 2
	DefaultMaxParallel      = 2

	minMaxParallel = 1
	maxMaxParallel = 4
)

// maxResponseBytes bounds how much of an upstream response is read.
const maxResponseBytes = 4 << 20

var referencePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// Client verifies Telebirr receipts against the official source and an
// optional pool of fallback relays. It is safe for concurrent use; circuit
// breaker state is shared across all calls made through one client.
type Client struct {
	httpClient       *http.Client
	logger           *slog.Logger
	primaryURL       string
	skipPrimary      bool
	routes           []Route
	proxyKey         string
	proxyTimeout     time.Duration
	hedgeDelay       time.Duration
	cooldown         time.Duration
	totalTimeout     time.Duration
	primaryTimeout   time.Duration
	failureThreshold int
	maxParallel      int

	mu     sync.Mutex
	state  map[string]*routeState // keyed by Route.URL
	active string
}

// Option configures a [Client] at construction time.
type Option func(*Client) error

// WithHTTPClient replaces the underlying [http.Client], enabling custom
// proxies or TLS settings. A nil client is rejected.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) error {
		if hc == nil {
			return errors.New("telebirr: http client must not be nil")
		}
		c.httpClient = hc
		return nil
	}
}

// WithLogger attaches a structured logger receiving debug and warning events
// during verification. A nil logger disables logging.
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) error {
		c.logger = l
		return nil
	}
}

// WithPrimaryURL overrides the official receipt URL prefix.
func WithPrimaryURL(url string) Option {
	return func(c *Client) error {
		if url == "" {
			return errors.New("telebirr: primary url must not be empty")
		}
		c.primaryURL = url
		return nil
	}
}

// WithSkipPrimary makes Verify consult only the fallback relay pool.
func WithSkipPrimary(skip bool) Option {
	return func(c *Client) error {
		c.skipPrimary = skip
		return nil
	}
}

// WithRoutes sets the fallback relay pool. Entries with an empty URL are
// ignored; entries left with [RoleUnknown] are assigned roles by position
// (first preferred, the rest fallback).
func WithRoutes(routes []Route) Option {
	return func(c *Client) error {
		filtered := make([]Route, 0, len(routes))
		seen := make(map[string]bool, len(routes))
		for _, r := range routes {
			if r.URL == "" || seen[r.URL] {
				continue
			}
			if r.Role == RoleUnknown {
				r.Role = RolePreferred
				if len(filtered) > 0 {
					r.Role = RoleFallback
				}
			}
			seen[r.URL] = true
			filtered = append(filtered, r)
		}
		c.routes = filtered
		return nil
	}
}

// WithProxyKey appends "&key=<value>" to every relay URL.
func WithProxyKey(key string) Option {
	return func(c *Client) error {
		c.proxyKey = key
		return nil
	}
}

// WithProxyTimeout caps a single relay attempt.
func WithProxyTimeout(d time.Duration) Option {
	return func(c *Client) error {
		if d <= 0 {
			return errors.New("telebirr: proxy timeout must be positive")
		}
		c.proxyTimeout = d
		return nil
	}
}

// WithHedgeDelay sets how long to wait before launching an additional
// parallel relay attempt while earlier attempts are still running.
func WithHedgeDelay(d time.Duration) Option {
	return func(c *Client) error {
		if d <= 0 {
			return errors.New("telebirr: hedge delay must be positive")
		}
		c.hedgeDelay = d
		return nil
	}
}

// WithCooldown sets how long a tripped circuit breaker keeps a route out of
// rotation.
func WithCooldown(d time.Duration) Option {
	return func(c *Client) error {
		if d <= 0 {
			return errors.New("telebirr: cooldown must be positive")
		}
		c.cooldown = d
		return nil
	}
}

// WithTotalTimeout bounds one full Verify call across all attempts.
func WithTotalTimeout(d time.Duration) Option {
	return func(c *Client) error {
		if d <= 0 {
			return errors.New("telebirr: total timeout must be positive")
		}
		c.totalTimeout = d
		return nil
	}
}

// WithPrimaryTimeout bounds the single request against the official source.
func WithPrimaryTimeout(d time.Duration) Option {
	return func(c *Client) error {
		if d <= 0 {
			return errors.New("telebirr: primary timeout must be positive")
		}
		c.primaryTimeout = d
		return nil
	}
}

// WithFailureThreshold sets how many consecutive transport failures open a
// route's circuit breaker.
func WithFailureThreshold(n int) Option {
	return func(c *Client) error {
		if n < 1 {
			return errors.New("telebirr: failure threshold must be at least 1")
		}
		c.failureThreshold = n
		return nil
	}
}

// WithMaxParallelRoutes caps concurrent relay attempts per verification.
// Values above 4 are clamped to 4, matching the reference implementation;
// values below 1 are rejected.
func WithMaxParallelRoutes(n int) Option {
	return func(c *Client) error {
		if n < 1 {
			return errors.New("telebirr: max parallel routes must be at least 1")
		}
		c.maxParallel = n
		return nil
	}
}

// New builds a client with package defaults; no environment variables are
// read. Use [NewFromEnv] for environment-driven configuration.
func New(opts ...Option) (*Client, error) {
	c := &Client{
		httpClient:       &http.Client{},
		logger:           slog.New(slog.DiscardHandler),
		primaryURL:       DefaultPrimaryURL,
		proxyTimeout:     DefaultProxyTimeout,
		hedgeDelay:       DefaultHedgeDelay,
		cooldown:         DefaultCooldown,
		totalTimeout:     DefaultTotalTimeout,
		primaryTimeout:   DefaultPrimaryTimeout,
		failureThreshold: DefaultFailureThreshold,
		maxParallel:      DefaultMaxParallel,
		state:            map[string]*routeState{},
	}
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	c.maxParallel = clampInt(c.maxParallel, minMaxParallel, maxMaxParallel)
	return c, nil
}

// NewFromEnv builds a client from the recognised environment variables (see
// the Env* constants), then applies opts on top so explicit options always
// win. A nil lookup falls back to [OSLookup].
func NewFromEnv(lookup LookupFunc, opts ...Option) (*Client, error) {
	if lookup == nil {
		lookup = OSLookup
	}
	envOpts := []Option{
		WithRoutes(RoutesFromEnv(lookup)),
		WithProxyKey(lookup(EnvProxyKey)),
		WithSkipPrimary(envBool(lookup, EnvSkipPrimaryVerification)),
		WithProxyTimeout(envDurationMs(lookup, EnvProxyTimeoutMs, DefaultProxyTimeout)),
		WithHedgeDelay(envDurationMs(lookup, EnvHedgeDelayMs, DefaultHedgeDelay)),
		WithCooldown(envDurationMs(lookup, EnvProxyCooldownMs, DefaultCooldown)),
		WithTotalTimeout(envDurationMs(lookup, EnvTotalTimeoutMs, DefaultTotalTimeout)),
		WithFailureThreshold(envInt(lookup, EnvProxyFailureThreshold, DefaultFailureThreshold)),
		WithMaxParallelRoutes(envInt(lookup, EnvMaxParallelProxies, DefaultMaxParallel)),
	}
	return New(append(envOpts, opts...)...)
}

// Verify resolves a Telebirr receipt by reference number. It first queries
// the official source (unless skipped), then races the configured fallback
// relays with hedging and circuit breaking.
//
// Outcomes:
//   - (*Receipt, nil): verified receipt.
//   - (nil, ErrNotFound): sources answered normally but none knows the receipt.
//   - (nil, *VerificationError): transport failure, deadline expiry, a relay
//     domain rejection surfaced on its own, or cancellation.
//   - (nil, error wrapping ErrInvalidReference): malformed reference.
func (c *Client) Verify(ctx context.Context, reference string) (*Receipt, error) {
	if err := validateReference(reference); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, newCancelledError(err)
	}

	if !c.skipPrimary {
		receipt, err := c.fetchPrimary(ctx, reference)
		switch {
		case err == nil && receipt.IsValid():
			return receipt, nil
		case err != nil:
			c.logger.Debug("primary verification failed; falling back to relay pool", "error", err)
		default:
			c.logger.Debug("primary source returned an incomplete receipt; falling back to relay pool")
		}
	} else if len(c.routes) == 0 {
		return nil, ErrNoRoutes
	}

	receipt, err := c.verifyViaRoutes(ctx, reference)
	if err != nil {
		return nil, err
	}
	if receipt == nil {
		return nil, ErrNotFound
	}
	return receipt, nil
}

func (c *Client) fetchPrimary(ctx context.Context, reference string) (*Receipt, error) {
	ctx, cancel := context.WithTimeout(ctx, c.primaryTimeout)
	defer cancel()

	body, err := c.get(ctx, c.primaryURL+reference, nil)
	if err != nil {
		return nil, fmt.Errorf("primary source request: %w", err)
	}
	receipt := ScrapeReceipt(body)
	return &receipt, nil
}

// fetchRoute performs one relay attempt, following the reference
// implementation's outcome mapping:
//   - (*Receipt, nil): relay produced a receipt (JSON or scraped HTML);
//   - (nil, errRejected): relay refused the request without a domain reason
//     (e.g. HTTP 404); the pool simply moves on;
//   - (nil, *VerificationError with kind ErrorDomain): relay explicitly
//     reported a domain failure such as "receipt not found";
//   - (nil, *VerificationError with kind ErrorTransport): network-level failure.
func (c *Client) fetchRoute(ctx context.Context, route Route, reference string) (*Receipt, error) {
	url := route.URL + reference
	if c.proxyKey != "" {
		url += "&key=" + c.proxyKey
	}

	headers := map[string]string{
		"Accept":     "application/json",
		"User-Agent": "VerifierAPI/1.0",
	}
	body, err := c.get(ctx, url, headers)
	if err != nil {
		var rejected errRejected
		switch {
		case errors.As(err, &rejected):
			// Relay refused the lookup without a domain reason; the pool
			// simply moves on.
			return nil, err
		case errors.Is(err, context.Canceled):
			return nil, newCancelledError(err)
		default:
			return nil, newTransportError(route.Label, err)
		}
	}

	receipt, domainMessage, parseFailed := parseRelayBody(body)
	switch {
	case domainMessage != "":
		return nil, &VerificationError{
			Kind:    ErrorDomain,
			Message: domainMessage,
			Details: route.Label,
		}
	case parseFailed:
		// Not the expected JSON envelope: scrape the response as HTML.
		scraped := ScrapeReceipt(body)
		receipt = &scraped
	}
	c.logger.Debug("relay attempt finished", "relay", route.Label, "parsed", !parseFailed)
	return receipt, nil
}

// routeAttemptResult carries the outcome of one relay attempt back to the
// pool loop.
type routeAttemptResult struct {
	route     Route
	receipt   *Receipt // non-nil on success
	failure   *VerificationError
	latencyMS int64
}

// startAttempt launches one relay attempt bound to attemptCtx. The result is
// delivered to results; the goroutine always terminates.
func (c *Client) startAttempt(attemptCtx context.Context, route Route, reference string, startedAt time.Time, results chan<- routeAttemptResult) {
	go func() {
		receipt, err := c.fetchRoute(attemptCtx, route, reference)
		result := routeAttemptResult{
			route:     route,
			latencyMS: time.Since(startedAt).Milliseconds(),
		}
		var (
			rejected  errRejected
			domainErr *VerificationError
		)
		switch {
		case err == nil:
			result.receipt = receipt
		case errors.As(err, &rejected):
			// Relay refused the lookup without a domain reason; move on.
		case errors.As(err, &domainErr):
			result.failure = domainErr
		case errors.Is(err, context.Canceled):
			result.failure = newCancelledError(err)
		default:
			result.failure = newTransportError(route.Label, err)
		}
		results <- result
	}()
}

// verifyViaRoutes races the configured relays: at most maxParallel attempts
// run concurrently, further candidates hedge in after hedgeDelay, a tripped
// circuit breaker skips unhealthy routes, and everything answers before the
// total timeout.
//
// Returns (receipt, nil) on success; (nil, nil) when every candidate answered
// without transport failures yet none produced a receipt; (nil, err) when the
// deadline expired or only transport failures remain.
func (c *Client) verifyViaRoutes(ctx context.Context, reference string) (*Receipt, error) {
	if err := ctx.Err(); err != nil {
		return nil, newCancelledError(err)
	}

	now := time.Now()
	candidates := c.orderedAvailableRoutes(now)

	if len(candidates) == 0 {
		return nil, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan routeAttemptResult, len(candidates))
	deadline := time.Now().Add(c.totalTimeout)
	deadlineTimer := time.NewTimer(c.totalTimeout)
	defer deadlineTimer.Stop()

	var (
		inFlight           []Route
		nextCandidate      int
		lastTransportError *VerificationError
		sawDomainFailure   bool
		hedgeTimer         *time.Timer
		hedgeC             <-chan time.Time
		attemptCancels     []context.CancelFunc
	)
	defer func() {
		for _, attemptCancel := range attemptCancels {
			attemptCancel()
		}
	}()

	startNextCandidate := func() bool {
		if nextCandidate >= len(candidates) || len(inFlight) >= c.maxParallel {
			return false
		}
		route := candidates[nextCandidate]
		nextCandidate++

		attemptTimeout := minDuration(c.proxyTimeout, time.Until(deadline))
		if attemptTimeout <= 0 {
			return false
		}
		attemptCtx, attemptCancel := context.WithTimeout(ctx, attemptTimeout)
		attemptCancels = append(attemptCancels, attemptCancel)

		inFlight = append(inFlight, route)
		c.startAttempt(attemptCtx, route, reference, time.Now(), results)
		return true
	}

	startNextCandidate()

	for len(inFlight) > 0 {
		if hedgeTimer == nil && nextCandidate < len(candidates) && len(inFlight) < c.maxParallel {
			hedgeTimer = time.NewTimer(minDuration(c.hedgeDelay, maxDuration(time.Millisecond, time.Until(deadline))))
			hedgeC = hedgeTimer.C
		}

		select {
		case <-ctx.Done():
			stopTimer(hedgeTimer)
			return nil, newCancelledError(ctx.Err())

		case <-deadlineTimer.C:
			stopTimer(hedgeTimer)
			for _, route := range inFlight {
				c.recordTransportFailure(route, c.cooldown, c.failureThreshold)
			}
			c.logger.Warn("relay pool reached its total verification deadline")
			return nil, &VerificationError{
				Kind:    ErrorTransport,
				Message: "the fallback relays did not respond before the verification deadline",
			}

		case <-hedgeC:
			hedgeTimer.Stop()
			hedgeTimer, hedgeC = nil, nil
			startNextCandidate()

		case event := <-results:
			stopTimer(hedgeTimer)
			hedgeTimer, hedgeC = nil, nil
			inFlight = removeFromSlice(inFlight, event.route.URL)

			if event.receipt != nil && event.receipt.IsValid() {
				c.recordSuccess(event.route, event.latencyMS)
				cancel() // stop hedged siblings still in flight
				return event.receipt, nil
			}

			switch {
			case event.failure != nil && event.failure.Kind == ErrorTransport:
				c.recordTransportFailure(event.route, c.cooldown, c.failureThreshold)
				lastTransportError = event.failure
			case event.failure != nil && event.failure.Kind == ErrorDomain:
				sawDomainFailure = true
				c.logger.Info("relay rejected the receipt lookup", "relay", event.route.Label, "error", event.failure.Message)
			}
			startNextCandidate()
		}
	}

	if lastTransportError != nil && !sawDomainFailure {
		return nil, lastTransportError
	}
	return nil, nil
}

// Probe verifies the reference against every configured relay in parallel and
// reports per-route operational status, mirroring a status-page health probe.
// The primary source is never consulted and circuit state is untouched.
func (c *Client) Probe(ctx context.Context, reference string) ProbeDetails {
	routes := c.routes
	results := make([]RouteStatus, len(routes))

	var wg sync.WaitGroup
	for i, route := range routes {
		wg.Add(1)
		go func(i int, route Route) {
			defer wg.Done()
			startedAt := time.Now()
			receipt, _ := c.fetchRoute(ctx, route, reference)

			status := RouteStatus{
				ID:        route.ID,
				Label:     route.Label,
				Role:      route.Role,
				LatencyMS: time.Since(startedAt).Milliseconds(),
				Status:    RouteHealthUnavailable,
			}
			if receipt != nil && receipt.IsValid() {
				status.Status = RouteHealthOperational
			}
			results[i] = status
		}(i, route)
	}
	wg.Wait()

	details := ProbeDetails{Routes: make([]RouteStatus, 0, len(results))}
	for _, status := range results {
		if details.ActiveRouteID == "" && status.Status == RouteHealthOperational {
			details.ActiveRouteID = status.ID
		}
		if status.Role == RolePreferred && status.Status == RouteHealthOperational {
			details.PreferredAvailable = true
		}
		details.Routes = append(details.Routes, status)
	}
	return details
}

// get issues a GET and returns the body. Non-2xx statuses become errors:
// 5xx maps to a transport-classifiable error, other statuses to
// [errRejected].
func (c *Client) get(ctx context.Context, url string, headers map[string]string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("read response body: %w", err)
	}
	switch {
	case resp.StatusCode >= http.StatusInternalServerError:
		return "", fmt.Errorf("upstream returned status %d", resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return "", errRejected{status: resp.StatusCode}
	}
	return string(body), nil
}

// errRejected marks a relay answer that refused the request without being a
// transport problem (e.g. HTTP 404 or 403).
type errRejected struct{ status int }

func (e errRejected) Error() string {
	return "request rejected with status " + strconv.Itoa(e.status)
}

func validateReference(reference string) error {
	if !referencePattern.MatchString(reference) {
		return fmt.Errorf("%w: %q must match %s", ErrInvalidReference, reference, referencePattern.String())
	}
	return nil
}

func envDurationMs(lookup LookupFunc, key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(lookup(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return time.Duration(n) * time.Millisecond
}

func envInt(lookup LookupFunc, key string, fallback int) int {
	raw := strings.TrimSpace(lookup(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func envBool(lookup LookupFunc, key string) bool {
	return strings.EqualFold(strings.TrimSpace(lookup(key)), "true")
}

func clampInt(n, low, high int) int {
	if n < low {
		return low
	}
	if n > high {
		return high
	}
	return n
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func stopTimer(t *time.Timer) {
	if t != nil {
		t.Stop()
	}
}

func removeFromSlice(routes []Route, url string) []Route {
	for i, r := range routes {
		if r.URL == url {
			return append(routes[:i], routes[i+1:]...)
		}
	}
	return routes
}

// relayEnvelope mirrors the JSON contract of the fallback relays.
type relayEnvelope struct {
	Success *bool      `json:"success"`
	Data    *relayData `json:"data"`
	Error   string     `json:"error"`
	Details string     `json:"details"`
}

type relayData struct {
	PayerName              string `json:"payerName"`
	PayerTelebirrNo        string `json:"payerTelebirrNo"`
	CreditedPartyName      string `json:"creditedPartyName"`
	CreditedPartyAccountNo string `json:"creditedPartyAccountNo"`
	TransactionStatus      string `json:"transactionStatus"`
	ReceiptNo              string `json:"receiptNo"`
	PaymentDate            string `json:"paymentDate"`
	SettledAmount          string `json:"settledAmount"`
	ServiceFee             string `json:"serviceFee"`
	ServiceFeeVAT          string `json:"serviceFeeVAT"`
	TotalPaidAmount        string `json:"totalPaidAmount"`
	BankName               string `json:"bankName"`
	CustomerNote           string `json:"customerNote"`
}

func (d *relayData) toReceipt() Receipt {
	return Receipt{
		PayerName:              d.PayerName,
		PayerTelebirrNo:        d.PayerTelebirrNo,
		CreditedPartyName:      d.CreditedPartyName,
		CreditedPartyAccountNo: d.CreditedPartyAccountNo,
		TransactionStatus:      d.TransactionStatus,
		ReceiptNo:              d.ReceiptNo,
		PaymentDate:            d.PaymentDate,
		SettledAmount:          d.SettledAmount,
		ServiceFee:             d.ServiceFee,
		ServiceFeeVAT:          d.ServiceFeeVAT,
		TotalPaidAmount:        d.TotalPaidAmount,
		BankName:               d.BankName,
		CustomerNote:           d.CustomerNote,
	}
}

// parseRelayBody interprets a successful relay response:
//   - relayErr non-empty: domain-level rejection reported by the relay;
//   - parseErr true: body was not the expected JSON envelope (caller retries
//     as HTML);
//   - otherwise: parsed receipt.
func parseRelayBody(body string) (receipt *Receipt, relayErr string, parseErr bool) {
	trimmed := strings.TrimSpace(body)
	if !strings.HasPrefix(trimmed, "{") {
		return nil, "", true
	}

	var envelope relayEnvelope
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return nil, "", true
	}
	if envelope.Success != nil && !*envelope.Success && envelope.Error != "" {
		return nil, envelope.Error, false
	}
	if envelope.Success == nil || envelope.Data == nil {
		return nil, "", true
	}
	parsed := envelope.Data.toReceipt()
	return &parsed, "", false
}
