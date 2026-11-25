package server

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	knowledgev1 "github.com/johnewart/pyxis/gen/knowledge/v1"
	llamacppv1 "github.com/johnewart/pyxis/gen/llamacpp/v1"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	mcpproxyv1 "github.com/johnewart/pyxis/gen/mcpproxy/v1"
)

// TestProtoNamingHasNoWorkspace guards the tenant/scope cut: no proto name in
// llmd's wire surface may say "workspace". The single exception is
// CursorCliLlmConfig.workspace, a Cursor CLI provider setting (a directory
// path passed to that CLI), not llmd's own identity concept.
func TestProtoNamingHasNoWorkspace(t *testing.T) {
	files := []protoreflect.FileDescriptor{
		llmv1.File_llmv1_llm_proto,
		mcpproxyv1.File_mcpproxy_v1_mcpproxy_proto,
		llamacppv1.File_llamacpp_v1_llamacpp_proto,
		knowledgev1.File_knowledge_v1_knowledge_proto,
	}
	allowed := map[string]bool{}
	for _, fd := range files {
		allowed[string(fd.Package())+".CursorCliLlmConfig.workspace"] = true
	}
	var bad []string
	check := func(d protoreflect.Descriptor) {
		full := string(d.FullName())
		if strings.Contains(strings.ToLower(string(d.Name())), "workspace") && !allowed[full] {
			bad = append(bad, full)
		}
	}
	var walkEnum func(e protoreflect.EnumDescriptor)
	walkEnum = func(e protoreflect.EnumDescriptor) {
		check(e)
		for i := 0; i < e.Values().Len(); i++ {
			check(e.Values().Get(i))
		}
	}
	var walkMsg func(m protoreflect.MessageDescriptor)
	walkMsg = func(m protoreflect.MessageDescriptor) {
		check(m)
		for i := 0; i < m.Fields().Len(); i++ {
			check(m.Fields().Get(i))
		}
		for i := 0; i < m.Oneofs().Len(); i++ {
			// Synthetic oneofs (proto3 `optional`) mirror their field's name.
			if o := m.Oneofs().Get(i); !o.IsSynthetic() {
				check(o)
			}
		}
		for i := 0; i < m.Enums().Len(); i++ {
			walkEnum(m.Enums().Get(i))
		}
		for i := 0; i < m.Messages().Len(); i++ {
			walkMsg(m.Messages().Get(i))
		}
	}
	for _, fd := range files {
		if fd == nil {
			t.Fatal("nil file descriptor")
		}
		for i := 0; i < fd.Messages().Len(); i++ {
			walkMsg(fd.Messages().Get(i))
		}
		for i := 0; i < fd.Enums().Len(); i++ {
			walkEnum(fd.Enums().Get(i))
		}
		for i := 0; i < fd.Services().Len(); i++ {
			s := fd.Services().Get(i)
			check(s)
			for j := 0; j < s.Methods().Len(); j++ {
				check(s.Methods().Get(j))
			}
		}
	}
	if len(bad) > 0 {
		t.Fatalf("proto names must not contain \"workspace\" (use tenant/scope):\n  %s", strings.Join(bad, "\n  "))
	}
}
