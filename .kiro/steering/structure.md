# Project Structure

Clean Architecture in Go. Dependency rule: outer layers know inner layers, never the reverse.

```
cmd (composition root) -> adapter -> usecase -> domain
                          infra  ->  implements ports declared by usecase
```

```
cmd/lanshare/main.go        Composition root: builds every adapter, use case and wires them. Only place that knows concrete types.
internal/
  domain/                   Entities and rules only: Room, Device, File, Chat/Message/Clip, Network, errors. No HTTP, JSON, SQL or disk.
  usecase/
    port/                   Interfaces the use cases need (RoomRepository, FileStore, TokenService, EventPublisher, Clock, ...)
    auth/ room/ device/     PIN login, lockout, tokens; room management; presence
    transfer/ chat/         Streaming upload/download, thumbnails, zip; messages and clipboard
    network/                Picks LAN interface and builds the join URL
    fake/                   In-memory test doubles for ports
  adapter/
    httpapi/                Router (server.go), handlers, middleware, DTOs
    ws/                     WebSocket hub (implements EventPublisher)
    filestore/              File contents on disk, per-room folders (FileStore, ThumbStore)
    jsonstore/              state.json repositories (rooms, files)
    memory/                 In-memory devices, tokens, rate limiter, chat
    netinfo/ qr/ thumb/     Network interfaces, QR PNG/terminal, image thumbnails
  infra/
    config/                 CLI flags -> Config
    system/                 Real Clock and IDGenerator
web/                        Browser UI (index.html, app.js, style.css) embedded via embed.go
docs/project-scope.md       Thai scope, decisions, phases, status
mock/                       HTML UI mocks (ui-mock-v2.html is current; ui-mock.html is obsolete)
```

## Layer rules
- `domain` imports nothing from this project. `usecase` imports only `domain` and `usecase/port`.
- `usecase` must not import `adapter` or `infra`, and must not use `net/http`, SQL or the filesystem directly.
- Ports are declared in `usecase/port` (consumer side), not next to the implementation.
- Convert to and from JSON/DTOs in `adapter`; keep `domain` types free of JSON/SQL concerns.
- File content ports take `io.Reader` / `io.ReadSeeker`; never `[]byte` for whole files.
- Every repository/store method that touches files or messages takes a `RoomID` so lookups are room-scoped.
- New external behavior: add the port in `usecase/port`, implement it in `adapter` or `infra`, add a fake in `usecase/fake`, wire it in `main.go`.
- Don't create interfaces ahead of a real consumer.

## Naming
- Packages are lowercase single words, matching the folder name.
- Tests sit next to the code as `*_test.go`.
- Event type names are constants in `port` (`EventFileAdded`, `EventTransfer`, ...); the browser handles the same strings in `web/app.js`.

## Version control
- Commit messages are written in Thai, subject line at most 72 chars, starting with a verb. An optional English Conventional Commits prefix (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`) is allowed. Keep file names, identifiers and technical terms in English.
