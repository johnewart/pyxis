# llmd (CGO: mattn/go-sqlite3). Native go build on matching Linux hosts
# (needs libsqlite3-dev + gcc). Other hosts use Docker — see Dockerfile.linux-amd64 and
# Dockerfile.linux-arm64 (CGO cannot cross-compile linux from macOS without a Linux toolchain).
#
# Generate Go code from proto. Requires:
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
#   protoc on PATH, or set PROTOC (e.g. $(brew --prefix protobuf)/bin/protoc)
#
export CGO_ENABLED ?= 1

UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)
# Non-empty on Linux aarch64 — native linux/arm64 build (no Docker).
LINUX_AARCH64_HOST := $(and $(filter Linux,$(UNAME_S)),$(filter aarch64 arm64,$(UNAME_M)))
# Non-empty on Linux x86_64 — native linux/amd64 build (no Docker).
LINUX_AMD64_HOST := $(and $(filter Linux,$(UNAME_S)),$(filter x86_64 amd64,$(UNAME_M)))

PROTO_DIR := proto
PROTOC ?= protoc
# llmd owns models/chat/tools/knowledge/mcp only, and generates nothing else.
# Schemas for any other subsystem belong to whichever service owns them.
LLMD_PROTOS := \
	knowledge/v1/knowledge.proto \
	llamacpp/v1/llamacpp.proto \
	llmv1/llm.proto \
	llmv1/policy.proto \
	mcpproxy/v1/mcpproxy.proto

PROTOC_BUNDLED := $(firstword $(wildcard $(CURDIR)/packages/client/node_modules/@protobuf-ts/protoc/installed/protoc-*/bin/protoc))
# go install destination: prefer GOBIN (buildpacks), else GOPATH/bin.
GOBIN_DIR := $(shell go env GOBIN 2>/dev/null)
GO_TOOL_BIN := $(if $(strip $(GOBIN_DIR)),$(GOBIN_DIR),$(shell go env GOPATH)/bin)

.PHONY: proto-plugins
proto-plugins:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1

.PHONY: clean-stale-proto
clean-stale-proto:
	@rm -f gen/llmv1/embedding.pb.go
	@rm -rf gen/documents

.PHONY: generate
ifneq ($(PROTOC_BUNDLED),)
generate: proto-plugins clean-stale-proto
	PATH="$(GO_TOOL_BIN):$(PATH)" $(PROTOC_BUNDLED) -I $(PROTO_DIR) \
		--go_out=gen --go_opt=paths=source_relative \
		--go-grpc_out=gen --go-grpc_opt=paths=source_relative \
		$(LLMD_PROTOS)
else
generate: proto-plugins clean-stale-proto
	PATH="$(GO_TOOL_BIN):$(PATH)" $(PROTOC) -I $(PROTO_DIR) \
		--go_out=gen --go_opt=paths=source_relative \
		--go-grpc_out=gen --go-grpc_opt=paths=source_relative \
		$(LLMD_PROTOS)
endif

# workspace-daemon (formerly a separate `daemon` module) is not llmd's —
# llmd itself never imported it, so there is nothing to generate here for it.
.PHONY: deps
deps:
	cd $(CURDIR) && go mod tidy

.PHONY: build
build: generate deps
	cd $(CURDIR) && go build -trimpath -ldflags='-s -w' -o llmd ./cmd/llmd

# Linux amd64: native go build on Linux x86_64; Docker elsewhere.
.PHONY: build-linux-amd64-native
build-linux-amd64-native: generate deps
	cd $(CURDIR) && GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o llmd-linux-amd64 ./cmd/llmd

ifneq ($(LINUX_AMD64_HOST),)
.PHONY: build-linux-amd64-do
build-linux-amd64-do:
	@echo "Building llmd-linux-amd64 natively on Linux x86_64 (CGO on, no Docker)…"
	$(MAKE) build-linux-amd64-native
else
.PHONY: build-linux-amd64-do
build-linux-amd64-do:
	@echo "Building llmd-linux-amd64 via Docker (linux/amd64, CGO on)…"
	cd $(CURDIR) && docker buildx build --platform linux/amd64 \
		-f Dockerfile.linux-amd64 \
		-o type=local,dest=. \
		.
endif

.PHONY: build-linux-amd64
build-linux-amd64: build-linux-amd64-do
	@test -f $(CURDIR)/llmd-linux-amd64 || (echo "missing $(CURDIR)/llmd-linux-amd64 after build" && exit 1)

# Linux arm64: native go build on Linux aarch64 (e.g. CI ubuntu-24.04-arm); Docker elsewhere.
.PHONY: build-linux-arm64-native
build-linux-arm64-native: generate deps
	cd $(CURDIR) && GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o llmd-linux-arm64 ./cmd/llmd

ifneq ($(LINUX_AARCH64_HOST),)
.PHONY: build-linux-arm64-do
build-linux-arm64-do:
	@echo "Building llmd-linux-arm64 natively on Linux aarch64 (CGO on, no Docker)…"
	$(MAKE) build-linux-arm64-native
else
.PHONY: build-linux-arm64-do
build-linux-arm64-do:
	@echo "Building llmd-linux-arm64 via Docker (linux/arm64, CGO on)…"
	cd $(CURDIR) && docker buildx build --platform linux/arm64 \
		-f Dockerfile.linux-arm64 \
		-o type=local,dest=. \
		.
endif

.PHONY: build-linux-arm64
build-linux-arm64: build-linux-arm64-do
	@test -f $(CURDIR)/llmd-linux-arm64 || (echo "missing $(CURDIR)/llmd-linux-arm64 after build" && exit 1)

.PHONY: build-linux-all
build-linux-all: build build-linux-arm64 build-linux-amd64

# CI artifact staging (→ artifacts/llmd).
ARTIFACTS_LLMD := $(CURDIR)/artifacts/llmd

.PHONY: collect-artifacts-linux-amd64-ci
collect-artifacts-linux-amd64-ci: build-linux-amd64
	mkdir -p $(ARTIFACTS_LLMD)
	cp -f $(CURDIR)/llmd-linux-amd64 $(ARTIFACTS_LLMD)/llmd-linux-amd64

.PHONY: collect-artifacts-linux-arm64-ci
collect-artifacts-linux-arm64-ci: build-linux-arm64
	mkdir -p $(ARTIFACTS_LLMD)
	cp -f $(CURDIR)/llmd-linux-arm64 $(ARTIFACTS_LLMD)/llmd-linux-arm64

.PHONY: collect-artifacts-darwin-ci
# Stages llmd-darwin-arm64 from a native Apple Silicon build (CI + local M-series).
# Recipe lines must use $ so Make does not treat $uarch as $(u)arch.
collect-artifacts-darwin-ci: build
	mkdir -p $(ARTIFACTS_LLMD)
	@uarch=$(uname -m); \
	if [ "$uarch" = arm64 ] || [ "$uarch" = aarch64 ] || [ "$uarch" = arch ]; then \
	  cp -f $(CURDIR)/llmd $(ARTIFACTS_LLMD)/llmd-darwin-arm64; \
	else \
	  echo "collect-artifacts-darwin-ci: expected Apple Silicon (uname -m arm64/aarch64/arch), got: $uarch" >&2; \
	  exit 1; \
	fi

# Convenience alias for local/just recipes (delegates to collect-artifacts-darwin-ci).
.PHONY: collect-artifacts-darwin-arm64
collect-artifacts-darwin-arm64: collect-artifacts-darwin-ci

# Both Linux helper binaries (container/remote paths).
.PHONY: collect-artifacts-linux
collect-artifacts-linux: collect-artifacts-linux-amd64-ci collect-artifacts-linux-arm64-ci
