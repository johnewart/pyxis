# Contributing to Pyxis

Thanks for helping improve Pyxis. By submitting a contribution you agree that it is licensed under the Apache License 2.0 (see [LICENSE](LICENSE)).

## Building

The root Go module needs Go 1.26 or newer and CGO (the SQLite driver uses it). See [docs/DEVELOPING.md](docs/DEVELOPING.md) for platform toolchains.

```bash
go build ./...
```

Two directories are separate Go modules and are not compiled by `go build ./...`:

```bash
go build -C cmd/llmd -o /dev/null .        # the llmd binary
go build -C examples/embed -o /dev/null .  # public-API boundary check
```

The TypeScript packages (`packages/client`, `packages/core`, `packages/react`) build with npm:

```bash
npm --prefix packages/client install
npm --prefix packages/client run build
npm --prefix packages/core install
npm --prefix packages/core run build
npm --prefix packages/react install
npm --prefix packages/react run build
```

## Running tests

```bash
go test ./...                              # root module
go test -C cmd/llmd ./...                  # nested module: the binary
go test -C examples/embed ./...            # nested module: embed example
npm --prefix packages/client test          # TypeScript client
npm --prefix packages/core test            # TypeScript core
npm --prefix packages/react/e2e install
npm --prefix packages/react/e2e test       # React components (build the packages first)
python3 -m pip install ./packages/python-client[test]
python3 -m pytest packages/python-client/tests
```

## The embed example must keep building

`examples/embed` is compiled from outside Pyxis's module path using only public packages. If a change breaks it, widen Pyxis's public API instead of importing `internal` packages from the example. Run the `examples/embed` build above before sending a change.

## Regenerating protos

The protobuf sources are in `proto/`. Generated Go code is in `gen/`; generated TypeScript is in `packages/client/src/gen`. Do not edit generated files by hand.

```bash
make generate                              # Go stubs (needs protoc on PATH or PROTOC set)
npm --prefix packages/client run proto:gen # TypeScript stubs
```

Commit the regenerated files with the `.proto` change.

## Pull requests

- Keep a change focused on one topic and include tests for new behavior.
- Run the Go tests, the nested-module builds and, if you touched them, the TypeScript or Python tests.
- Files that carry a copyright header also carry `SPDX-License-Identifier: Apache-2.0`.
- Report security problems privately, as described in [SECURITY.md](SECURITY.md), not in a public issue.
