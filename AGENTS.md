# AGENTS.md

## Required Go skills

The `.agents/skills` directory vendors [samber/cc-skills-golang](https://github.com/samber/cc-skills-golang)
so the standards travel with the repository; update it by re-running that
project's installer and committing the diff.

Always load `samber/cc-skills-golang@golang-how-to` before any Go task in this repository. It routes to the relevant secondary skills.

For this project, additionally load when applicable:

- Writing or reviewing library code: `golang-naming`, `golang-code-style`, `golang-error-handling`, `golang-design-patterns`
- Concurrency work (pool, hedging, circuit breaker): `golang-concurrency`, `golang-context`
- Tests: `golang-testing`
- Docs: `golang-documentation`

## Project conventions

- Module: `github.com/Zahir-Seid/telebirr-verifier`; package name: `telebirr`, living under `pkg/telebirr/`.
- Zero external dependencies: standard library only (regex-based HTML extraction, net/http, log/slog).
- Public API lives in `pkg/telebirr/`; nothing under internal/.
- All regexes compiled once at package level.
- Every exported symbol has a doc comment; run `gofmt -s -w .` and `go vet ./...` and `go test -race ./...` before committing.
