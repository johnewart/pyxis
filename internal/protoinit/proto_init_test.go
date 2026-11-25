// Package protoinit holds smoke checks that llmd's generated protobuf packages
// register themselves at init.
//
// This was a main package under scripts/test-knowledge-proto-init, which meant
// two problems: it sat in llmd's PUBLIC import-path namespace, and nothing ever
// ran it — no Makefile, justfile or CI step referenced it, so the check it
// performed had silently stopped happening. As a test it runs with everything
// else.
package protoinit

import (
	"testing"

	knowledgev1 "github.com/johnewart/pyxis/gen/knowledge/v1"
	llamacppv1 "github.com/johnewart/pyxis/gen/llamacpp/v1"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	mcpproxyv1 "github.com/johnewart/pyxis/gen/mcpproxy/v1"
)

// TestGeneratedProtoFilesRegister asserts each generated package's file
// descriptor is non-nil after init.
//
// A nil descriptor means that package's init did not run or the generated code
// is malformed, which surfaces far away from the cause: reflection-based paths
// (grpc-web/Connect, protojson, any registry lookup by name) fail at request
// time with an error about an unknown message rather than at startup.
func TestGeneratedProtoFilesRegister(t *testing.T) {
	cases := []struct {
		name string
		file any
	}{
		{"knowledge.v1", knowledgev1.File_knowledge_v1_knowledge_proto},
		{"llm.v1", llmv1.File_llmv1_llm_proto},
		{"llamacpp.v1", llamacppv1.File_llamacpp_v1_llamacpp_proto},
		{"mcpproxy.v1", mcpproxyv1.File_mcpproxy_v1_mcpproxy_proto},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.file == nil {
				t.Fatalf("%s: generated file descriptor is nil after init", tc.name)
			}
		})
	}
}
