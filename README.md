# telebirr-verifier

[![Go Version](https://img.shields.io/github/go-mod/go-version/Zahir-Seid/telebirr-verifier)](https://go.dev/)
[![Go Reference](https://pkg.go.dev/badge/github.com/Zahir-Seid/telebirr-verifier.svg)](https://pkg.go.dev/github.com/Zahir-Seid/telebirr-verifier/pkg/telebirr)
[![License](https://img.shields.io/github/license/Zahir-Seid/telebirr-verifier)](./LICENSE)

A Go library that verifies Ethiopian Telebirr payment receipts by reference number. It queries the official transaction page first and can fall back to a pool of relay endpoints, ported from the original TypeScript implementation.

## Features

- **Official source scraping** — receipt pages are parsed with regexes compiled once at package level; no HTML parser dependency.
- **Fallback relay pool** — races up to 4 relays per lookup, hedges extra attempts after a configurable delay.
- **Circuit breaker** — routes failing repeatedly at the transport level cool down; a half-open recovery attempt prevents permanent lock-out.
- **Route ordering** — active route first, then preferred role, recency of success, average latency (EWMA), configuration order.
- **Typed errors** — `*VerificationError` distinguishes domain rejections, transport failures and cancellation; `errors.Is/As` friendly.
- **Zero dependencies** — standard library only (`net/http`, `regexp`, `encoding/json`, `log/slog`).
- **Environment-driven config** — drop-in parity with the TypeScript service's `TELEBIRR_*` variables.

## Installation

```bash
go get github.com/Zahir-Seid/telebirr-verifier/pkg/telebirr
```

## Usage

### Quick start

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	telebirr "github.com/Zahir-Seid/telebirr-verifier/pkg/telebirr"
)

func main() {
	client, err := telebirr.New()
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	receipt, err := client.Verify(ctx, "CBJ0H74269")
	switch {
	case errors.Is(err, telebirr.ErrNotFound):
		fmt.Println("receipt not found")
	case err != nil:
		var verr *telebirr.VerificationError
		if errors.As(err, &verr) {
			fmt.Println("verification failed:", verr.Kind, verr.Message)
			return
		}
		panic(err)
	default:
		fmt.Printf("%s paid %s (%s)\n",
			receipt.PayerName, receipt.SettledAmount, receipt.TransactionStatus)
	}
}
```

### With fallback relays

```go
client, err := telebirr.New(
	telebirr.WithRoutes(telebirr.RoutesFromEnv(nil)), // reads FALLBACK_PROXIES
	telebirr.WithProxyKey(os.Getenv("TELEBIRR_PROXY_KEY")),
	telebirr.WithTotalTimeout(20*time.Second),
	telebirr.WithMaxParallelRoutes(2),
)
```

Or load everything from the environment in one call:

```go
client, err := telebirr.NewFromEnv(nil) // nil lookup = os.Getenv
```

### Probing relay health

```go
details := client.Probe(ctx, reference)
for _, r := range details.Routes {
	fmt.Println(r.ID, r.Status, r.LatencyMS)
}
```

### Parsing raw receipt HTML

Already have the HTML? Skip the network:

```go
receipt := telebirr.ScrapeReceipt(htmlBody)
if !receipt.IsValid() {
	// missing receipt number, payer name or status
}
```

## Configuration

Every option has a functional equivalent; environment variables are applied by `NewFromEnv`.

| Environment variable | Option | Default |
| --- | --- | --- |
| `FALLBACK_PROXIES` | `WithRoutes(RoutesFromEnv(...))` | none |
| `TELEBIRR_PROXY_LABELS` | labels on `Route` | derived |
| `TELEBIRR_PROXY_KEY` | `WithProxyKey` | empty |
| `TELEBIRR_PROXY_TIMEOUT_MS` | `WithProxyTimeout` | 18000 |
| `TELEBIRR_HEDGE_DELAY_MS` | `WithHedgeDelay` | 1000 |
| `TELEBIRR_PROXY_COOLDOWN_MS` | `WithCooldown` | 60000 |
| `TELEBIRR_TOTAL_TIMEOUT_MS` | `WithTotalTimeout` | 20000 |
| `TELEBIRR_PROXY_FAILURE_THRESHOLD` | `WithFailureThreshold` | 2 |
| `TELEBIRR_MAX_PARALLEL_PROXIES` | `WithMaxParallelRoutes` | 2 (clamped 1–4) |
| `SKIP_PRIMARY_VERIFICATION` | `WithSkipPrimary(true)` | false |

## Error model

```go
verr := new(telebirr.VerificationError)
switch {
case errors.Is(err, telebirr.ErrNotFound):        // every source answered; no receipt
case errors.Is(err, telebirr.ErrNoRoutes):        // skip-primary set without routes
case errors.Is(err, telebirr.ErrInvalidReference): // malformed reference
case errors.As(err, &verr):
	switch verr.Kind {
	case telebirr.ErrorDomain:    // relay rejected the lookup ("not found", ...)
	case telebirr.ErrorTransport: // timeout, refused, HTTP 5xx
	case telebirr.ErrorCancelled: // caller context cancelled
	}
}
```

## Development

```bash
make test-race   # go test -race ./...
make vet         # go vet ./...
make fmt         # gofmt -s -w .
make cover       # coverage report
```

## Contributing

Issues and pull requests are welcome. Run `make test-race vet fmt` before submitting.

## License

[MIT](./LICENSE)
