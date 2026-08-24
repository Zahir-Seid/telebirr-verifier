// Package telebirr verifies Ethiopian Telebirr payment receipts.
//
// A receipt is looked up by its reference number from the official
// transactioninfo.ethiotelecom.et page (primary source) and, when configured,
// from a pool of fallback relays that return JSON. The primary source returns
// an HTML page; it is parsed with compiled regular expressions only, so the
// package has zero external dependencies.
//
// # Basic usage
//
//	client, err := telebirr.New()
//	if err != nil {
//		return err
//	}
//	receipt, err := client.Verify(ctx, "CBJ0H74269")
//	if err != nil {
//		return err
//	}
//	fmt.Println(receipt.PayerName, receipt.SettledAmount)
//
// # Fallback relay pool
//
// Relays are described by [Route] values, typically discovered from the
// FALLBACK_PROXIES environment variable via [RoutesFromEnv]. The client races
// up to MaxParallelRoutes relays per lookup, hedges after HedgeDelay, opens a
// circuit breaker around routes that keep failing at the transport level, and
// enforces an overall deadline for every verification:
//
//	client, err := telebirr.New(
//		telebirr.WithRoutes(telebirr.RoutesFromEnv(nil)),
//		telebirr.WithTotalTimeout(20*time.Second),
//	)
//
// # Errors
//
// Verify returns a [*VerificationError] whose [ErrorKind] distinguishes relay
// domain rejections (ErrorDomain, e.g. a relay reporting "receipt not found"),
// network-level failures (ErrorTransport) and caller cancellation
// (ErrorCancelled). When every route is exhausted without transport trouble
// the sentinel error [ErrNotFound] is returned instead.
package telebirr
