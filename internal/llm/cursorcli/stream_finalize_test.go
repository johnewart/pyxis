package cursorclillm

import "testing"

func TestFinalizeCursorCLIStreamBodies(t *testing.T) {
	t.Parallel()

	t.Run("empty_result_visible_uses_emitted_stream_fold", func(t *testing.T) {
		t.Parallel()
		jr := jsonResult{Result: ""}
		content, thinking, err := finalizeCursorCLIStreamBodies(jr, "hello from stream", "", "", nil, false)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if content != "hello from stream" {
			t.Fatalf("content=%q want stream fold", content)
		}
		if thinking != "" {
			t.Fatalf("thinking=%q want empty", thinking)
		}
	})

	t.Run("empty_result_thinking_uses_emitted_thinking_fold", func(t *testing.T) {
		t.Parallel()
		jr := jsonResult{Result: "visible"}
		content, thinking, err := finalizeCursorCLIStreamBodies(jr, "", "think-fold", "", nil, false)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if content != "visible" {
			t.Fatalf("content=%q", content)
		}
		if thinking != "think-fold" {
			t.Fatalf("thinking=%q want emitted fold", thinking)
		}
	})

	t.Run("both_empty_without_tools_errors", func(t *testing.T) {
		t.Parallel()
		jr := jsonResult{Result: ""}
		_, _, err := finalizeCursorCLIStreamBodies(jr, "", "", "", nil, false)
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("both_empty_with_tools_ok", func(t *testing.T) {
		t.Parallel()
		jr := jsonResult{Result: ""}
		content, thinking, err := finalizeCursorCLIStreamBodies(jr, "", "", "", nil, true)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if content != "" || thinking != "" {
			t.Fatalf("content=%q thinking=%q want empty", content, thinking)
		}
	})
}
