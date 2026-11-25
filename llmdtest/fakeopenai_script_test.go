// Copyright 2026 John Ewart <john@johnewart.net>
// SPDX-License-Identifier: Apache-2.0

package llmdtest

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func postFake(t *testing.T, f *FakeOpenAI, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(f.URL+"/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return resp.StatusCode, string(b)
}

func TestFakeOpenAIErrorTurn(t *testing.T) {
	f, err := StartFakeOpenAI()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.Enqueue(ErrorTurn(429, "slow down"), ErrorTurn(500, "boom"))
	code, b := postFake(t, f, `{"stream":true}`)
	if code != 429 || !strings.Contains(b, `"message":"slow down"`) {
		t.Fatalf("got %d %s", code, b)
	}
	code, _ = postFake(t, f, `{}`)
	if code != 500 {
		t.Fatalf("got %d", code)
	}
}

func TestFakeOpenAIUsageAndMultiToolCalls(t *testing.T) {
	f, err := StartFakeOpenAI()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	calls := ToolCallsTurn(ToolCall{"a", "f", "{}"}, ToolCall{"b", "g", "{}"}).WithUsage(3, 4, 1)
	f.Enqueue(calls, calls, TextTurn("hi").WithUsage(1, 2, 0))

	_, b := postFake(t, f, `{"stream":true,"stream_options":{"include_usage":true}}`)
	if !strings.Contains(b, `"index":1`) || !strings.Contains(b, `"prompt_tokens":3`) {
		t.Fatalf("stream: %s", b)
	}
	_, b = postFake(t, f, `{}`)
	if !strings.Contains(b, `"id":"b"`) || !strings.Contains(b, `"finish_reason":"tool_calls"`) || !strings.Contains(b, `"completion_tokens":4`) {
		t.Fatalf("json: %s", b)
	}
	_, b = postFake(t, f, `{"stream":true}`)
	if strings.Contains(b, `"usage"`) {
		t.Fatalf("usage without include_usage: %s", b)
	}
}
