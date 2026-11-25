---
title: Never swallow errors in Go — always log or propagate
summary: Project rule requiring every Go error branch to log with context or
  propagate, prompted by silently swallowed websocket errors that hid a
  diagnosable failure.
---
Every `err != nil` (or ignored-error) branch in Go code must either propagate the error to the caller or log it with enough context (module, relevant IDs, the error itself) to diagnose from server logs alone. Never silently `return`/`continue`/drop on an error with no trace.

This bit us concretely in `internal/serve/session_events_ws.go`: `websocket.Accept`'s error was silently swallowed (`if err != nil { return }`), and `SessionEventRepo.GetAllEvents`/`GetEventsSince` errors were ignored via `if err == nil { ... }` with no `else`. The result: a real, diagnosable server-side rejection (wrong origin pattern) presented to the client as an unexplained "handshake then premature close" — cost multiple rounds of blind guessing (client version checks, proxy config, wrapper-object shape) before anyone thought to look at the Go side, because there was nothing in the logs to look at.

Concretely:
- `if err != nil { return }` with no log line is a bug, not a valid "best effort" pattern — even in a hot path or a supplementary/non-critical feature, log at `llmdlog.LevelWarn` (or higher) before bailing.
- `if err == nil { ...use result... }` without an `else` branch logging the error is the same bug in a different shape.
- This applies doubly to any handler serving an external client (HTTP/WS/gRPC) where the failure is otherwise invisible outside the server process — the whole value of Go's explicit error returns is defeated if nobody ever sees them.