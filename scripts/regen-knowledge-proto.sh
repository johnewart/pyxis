#!/usr/bin/env bash
# Regenerate Go protobuf stubs for knowledge/v1 (fixes corrupted manual rawDesc patches).
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="${PATH}:$(go env GOPATH)/bin"
if ! command -v protoc-gen-go >/dev/null 2>&1; then
  go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
fi
if ! command -v protoc-gen-go-grpc >/dev/null 2>&1; then
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
fi
protoc -I proto \
  --go_out=gen --go_opt=paths=source_relative \
  --go-grpc_out=gen --go-grpc_opt=paths=source_relative \
  knowledge/v1/knowledge.proto
echo "regenerated gen/knowledge/v1/knowledge.pb.go and knowledge_grpc.pb.go"
