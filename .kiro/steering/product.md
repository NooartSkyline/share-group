# Product: LAN Share

LAN Share is a file-sharing server for a local network. It runs on the owner's Mac (single Go binary). Other devices (Android, iOS, Windows, macOS) join through a browser by scanning a QR code or opening a URL and entering a PIN. No app install and no internet are needed.

## Core concepts
- **Room**: isolated share space with its own 6-digit PIN, files and device list. One room can never see another room's files.
- **Device**: a browser session identified by a token issued after a correct PIN.
- **Roles**: Owner, Member, Uploader (guest, upload only), Viewer (download only).
- **Real-time**: WebSocket pushes device presence, file events, transfer progress, chat and clipboard.

## Features
- Two-way streaming upload/download with progress and speed
- Shared clipboard and short chat messages (in memory, last 200 per room)
- Multi-file zip download (max 500 files), folder upload, thumbnails, paste-image upload, duplicate-name handling
- PIN lockout after repeated wrong attempts

## Status (see `docs/project-scope.md` section 11)
- Phase A (MVP) and B (convenience) are done
- Not started: C (resumable upload, SHA-256), D (multi-room, device approval, HTTPS), E (UX), F (advanced), G (Wails v3 menu bar app)
- Do not expose to untrusted users until Phase D (HTTPS and device approval) is done. Current transport is plain HTTP.

## Product rules
- Large files (multi-GB) must stream; never load a whole file into RAM.
- Every operation is scoped by `RoomID` and checks role permissions, even while only one default room exists.
- The user-facing UI and the startup banner are in Thai (with English planned in Phase E). Keep new user-facing strings consistent with the existing language.
- `docs/project-scope.md` (Thai) is the source of truth for scope and phases. UI mocks live in `mock/` (`ui-mock-v2.html` is current).
