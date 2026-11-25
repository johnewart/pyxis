#!/usr/bin/env bash
# Smoke test: importing knowledgev1 must not panic during proto init.
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="${PATH}:$(go env GOPATH)/bin"
go mod tidy
go run ./scripts/test-knowledge-proto-init/main.go
