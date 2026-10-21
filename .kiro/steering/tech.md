# Tech Stack

## Core
- Language: Go (module `lanshare`, `go 1.27.0`)
- HTTP: standard library `net/http`
- WebSocket: `github.com/coder/websocket` (hidden behind the `EventPublisher` port)
- QR: `github.com/skip2/go-qrcode`
- Images: `golang.org/x/image` (thumbnail decoding)
- Logging: `log/slog`
- Persistence: JSON file `state.json` (`jsonstore`) behind repository ports. SQLite (`modernc.org/sqlite`, pure Go, no cgo) is planned as a drop-in replacement.
- Frontend: vanilla HTML/CSS/JS in `web/`, embedded in the binary via `//go:embed` (`web/embed.go`). No build step, no npm, no framework.
- Planned: Wails v3 menu bar app (Phase G)

## Commands
```bash
go build ./...                      # compile everything
go run ./cmd/lanshare               # run server (default :8080, data in ~/LANShare)
go run ./cmd/lanshare -port 9000 -data ./tmp -iface en0
go test ./...                       # all tests
go test ./internal/usecase/...      # use case tests only
go test -race ./...                 # race detector (concurrency-heavy code)
go vet ./...
gofmt -l .                          # list unformatted files
```

Config flags (`internal/infra/config`): `-port`, `-data`, `-iface`, `-room-name`, `-max-pin-failures`, `-lockout`.

If you add a new file to `web/`, add it to the `//go:embed` line in `web/embed.go`.

## Code conventions
- Follow standard Go style; run `gofmt`. Wrap errors with `fmt.Errorf("...: %w", err)`.
- Pass `context.Context` as the first argument to port methods and use case methods.
- Domain errors live in `internal/domain/errors.go` (e.g. `ErrNotFound`, `ErrUnsupported`). Match with `errors.Is`; map to HTTP status only in `httpapi`.
- Constructors are `NewService(...)` with explicit dependencies. No globals, no `init()` wiring.
- Inject `Clock` and `IDGenerator` ports instead of calling `time.Now()` or generating IDs directly in use cases.
- Doc comments on packages and exported types explain intent and invariants (not restating the code).
- Frontend: insert user-controlled text (file names, device names, messages) with `textContent`/text nodes, never `innerHTML`. Only build links for http(s) URLs. Keep `'use strict'` and the existing `$`/`$$` helpers.
- Do not set overall `ReadTimeout`/`WriteTimeout` on `http.Server`; they break large transfers.

## Testing
- Use cases: unit tests with fakes from `internal/usecase/fake` (in-memory port implementations, manual `Clock`). No HTTP or disk.
- Adapters: `httptest` and integration style tests (`package httpapi_test`, real filestore/jsonstore in `t.TempDir()`).
- Cross-room isolation and PIN lockout must stay covered by tests.
- Only add tests when asked, but do not break existing ones. Run `go test ./...` after changes.

## Security notes
- Never log or echo PINs or tokens beyond the startup banner.
- Do not weaken PIN lockout, token checks or room scoping. Auth middleware lives in `internal/adapter/httpapi/middleware.go`.
- Data dir is created with `0o700`.
