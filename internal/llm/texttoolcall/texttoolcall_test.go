package texttoolcall

import (
	"strings"
	"testing"
)

var offered = []string{"search", "read"}

func TestNoMarkupUnchanged(t *testing.T) {
	in := "  hello <b>world</b> a < b > c \n"
	for _, m := range []Mode{ModeUnspecified, ModeStrip, ModeConvert} {
		out, calls, dets := Process(in, offered, m)
		if out != in || len(calls) != 0 || len(dets) != 0 {
			t.Fatalf("mode %d: got %q %v %v", m, out, calls, dets)
		}
	}
}

func TestUnknownNameLeftAlone(t *testing.T) {
	in := ` x <tool_call>{"name":"nope","arguments":{}}</tool_call> <invoke name="nope"><parameter name="a">1</parameter></invoke> `
	out, calls, dets := Process(in, offered, ModeConvert)
	if out != in || len(calls) != 0 || len(dets) != 0 {
		t.Fatalf("got %q %v %v", out, calls, dets)
	}
}

func TestEmptyOfferedLeavesAll(t *testing.T) {
	in := `<tool_call>{"name":"search"}</tool_call>`
	out, _, dets := Process(in, nil, ModeStrip)
	if out != in || len(dets) != 0 {
		t.Fatalf("got %q %v", out, dets)
	}
}

func TestStripPreservesSurroundingBytes(t *testing.T) {
	in := "  before \n<tool_call>{\"name\":\"search\",\"arguments\":{\"q\":\"a\"}}</tool_call>\n after  "
	out, calls, dets := Process(in, offered, ModeStrip)
	if out != "  before \n\n after  " {
		t.Fatalf("got %q", out)
	}
	if len(calls) != 0 || len(dets) != 1 || dets[0].Name != "search" || dets[0].Converted || !strings.HasPrefix(dets[0].Raw, "<tool_call>") {
		t.Fatalf("calls=%v dets=%+v", calls, dets)
	}
}

func TestUnspecifiedActsLikeStrip(t *testing.T) {
	in := `a<tool_use>{"name":"read"}</tool_use>b`
	out, calls, dets := Process(in, offered, ModeUnspecified)
	if out != "ab" || len(calls) != 0 || len(dets) != 1 {
		t.Fatalf("got %q %v %v", out, calls, dets)
	}
}

func TestConvertJSON(t *testing.T) {
	in := `x<function_call>{"name":"search","arguments":{"q":"a"}}</function_call>y`
	out, calls, dets := Process(in, offered, ModeConvert)
	if out != "xy" || len(calls) != 1 || calls[0].Name != "search" || calls[0].Arguments != `{"q":"a"}` {
		t.Fatalf("got %q %+v", out, calls)
	}
	if !dets[0].Converted || dets[0].Error != "" {
		t.Fatalf("det %+v", dets[0])
	}
}

func TestConvertInvokeParameters(t *testing.T) {
	in := `<invoke name="search"><parameter name="q">hello world</parameter><parameter name="n">5</parameter><parameter name="o">{"a":true}</parameter></invoke>`
	out, calls, dets := Process(in, offered, ModeConvert)
	if out != "" || len(calls) != 1 || len(dets) != 1 {
		t.Fatalf("got %q %v %v", out, calls, dets)
	}
	want := `{"n":5,"o":{"a":true},"q":"hello world"}`
	if calls[0].Arguments != want {
		t.Fatalf("args %s want %s", calls[0].Arguments, want)
	}
}

func TestFunctionCallsWrapper(t *testing.T) {
	in := "hi\n<function_calls>\n<invoke name=\"read\"><parameter name=\"p\">/a</parameter></invoke>\n<invoke name=\"search\"><parameter name=\"q\">z</parameter></invoke>\n</function_calls>\nbye"
	out, calls, dets := Process(in, offered, ModeConvert)
	if out != "hi\n\nbye" || len(calls) != 2 || len(dets) != 2 {
		t.Fatalf("got %q %v %v", out, calls, dets)
	}
	if calls[0].Name != "read" || calls[1].Name != "search" {
		t.Fatalf("calls %+v", calls)
	}
}

func TestWrapperWithUnknownInvokeKept(t *testing.T) {
	in := `<function_calls><invoke name="nope"></invoke><invoke name="read"></invoke></function_calls>`
	out, calls, _ := Process(in, offered, ModeConvert)
	want := `<function_calls><invoke name="nope"></invoke></function_calls>`
	if out != want || len(calls) != 1 || calls[0].Arguments != "{}" {
		t.Fatalf("got %q %v", out, calls)
	}
}

func TestFunctionCallNameAttribute(t *testing.T) {
	in := `<function_call name="read"><parameter name="p">x</parameter></function_call>`
	out, calls, _ := Process(in, offered, ModeConvert)
	if out != "" || len(calls) != 1 || calls[0].Arguments != `{"p":"x"}` {
		t.Fatalf("got %q %v", out, calls)
	}
}

func TestConvertParseFailureStripsAndRecords(t *testing.T) {
	in := `a<tool_call>{"name":"search","arguments":"not json"}</tool_call>b`
	out, calls, dets := Process(in, offered, ModeConvert)
	if out != "ab" || len(calls) != 0 || len(dets) != 1 {
		t.Fatalf("got %q %v %v", out, calls, dets)
	}
	if dets[0].Converted || dets[0].Error == "" {
		t.Fatalf("det %+v", dets[0])
	}
}

func TestUnterminatedLeftAlone(t *testing.T) {
	in := `<tool_call>{"name":"search"}`
	out, _, dets := Process(in, offered, ModeStrip)
	if out != in || len(dets) != 0 {
		t.Fatalf("got %q %v", out, dets)
	}
}

func TestMultipleBlocks(t *testing.T) {
	in := `<tool_call>{"name":"search"}</tool_call>-<tool_call>{"name":"read"}</tool_call>`
	out, calls, dets := Process(in, offered, ModeConvert)
	if out != "-" || len(calls) != 2 || len(dets) != 2 {
		t.Fatalf("got %q %v %v", out, calls, dets)
	}
}
