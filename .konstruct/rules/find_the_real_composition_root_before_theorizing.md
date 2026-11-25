---
title: Find the real composition root before theorizing
summary: A project rule urging engineers to verify which composition root
  actually runs (konstructd, not cmd/llmd) by adding log lines before
  theorizing.
---
## Find the real composition root before theorizing

**This repo has more than one composition root, and the obvious one is usually not the one running.**

- `cmd/llmd` is its own Go module with its own `go.mod`. It is invisible to `./...` from the repo root (`go list ./cmd/...` → "matched no packages"; build it with `go build -C cmd/llmd ./...`).
- **konstructd links the core module `github.com/johnewart/pyxis` via a local `replace` and composes/inits llmd's gRPC services in its own process.** It has its own startup sequence and its own log lines, some of them older copies of text that also exists in `cmd/llmd/main.go`.
- So `cmd/llmd/main.go` is llmd's standalone binary, **not** what executes in the product. Reading it to learn "how llmd starts up" tells you how *a* llmd starts up.

### The rule

**If your log line doesn't appear, your code didn't run. Make that the first hypothesis, not the last.**

I had that backwards for four turns. The startup lines in the user's log didn't match this tree, so I invented reasons the *logs* were wrong: a stale binary, a second checkout, a build step skipping a nested module. Each theory was plausible and each one required the user to disprove it. The simple reading — different code is printing these lines — was available from the first paste.

### How to actually check (one round trip, not four theories)

Add a log line to the code you believe is executing, have it rebuilt, and look. That is the whole experiment. When I finally put a `WARN` in `internal/serve/connect_listener.go` it showed up in konstructd's log **seconds later**, which established in one shot that:

1. the host rebuilds and picks up current core-module source immediately (nothing was stale),
2. the host calls that constructor directly, and
3. `cmd/llmd/main.go` never executes there.

All three had been argued about at length. None of it needed arguing.

### Corollaries

- **Believe the user's description of their architecture over your inference from file layout.** I was told "konstruct composes itself of llmd's gRPC service and inits it" and kept re-deriving something else from `go.mod` files.
- **Fix things in the core library, not in `cmd/llmd/main.go`.** A change to the standalone binary's startup does nothing for the product. Anything that must hold for every host belongs where every host links it.
- **Don't ship a surface that can be wired half-way in silence.** The bug underneath all this: a host could build the Connect/grpc-web handler and get no `/ws/session-events` route, so browsers failed the handshake while every RPC worked and llmd logged nothing — the code that would log the rejection was the code that was never wired. The fix is `serve.NewBrowserSurface` (both halves, one call) plus a warning at construction on the incomplete path. Prefer "correct wiring is the only one-call option" over a doc comment asking people to remember.