#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

PROTOC="$(ls packages/client/node_modules/@protobuf-ts/protoc/installed/protoc-*/bin/protoc 2>/dev/null | head -1)"
if [ -z "$PROTOC" ]; then
  echo "bundled protoc not found" >&2
  exit 1
fi

GOBIN_DIR="$(go env GOBIN 2>/dev/null)"
GO_TOOL_BIN="${GOBIN_DIR:-$(go env GOPATH)/bin}"

# Install grpc plugins if not present
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1

PATH="${GO_TOOL_BIN}:${PATH}" "$PROTOC" -I proto \
  --go_out=gen --go_opt=paths=source_relative \
  --go-grpc_out=gen --go-grpc_opt=paths=source_relative \
  llmv1/llm.proto

echo "Done."
