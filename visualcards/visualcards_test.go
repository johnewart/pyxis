package visualcards

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

var pngHeader = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

// TestPayloadShapesMatchCore pins the JSON @pyxis-llm/core decodes
// (displayCard.ts envelope; imageCard.ts / chartCard.ts bodies). A rename
// here breaks the renderer silently.
func TestPayloadShapesMatchCore(t *testing.T) {
	if Kind != "llmd.display" || TypeImage != "image" || TypeChart != "chart" {
		t.Fatal("kind/type names drifted from @pyxis-llm/core")
	}
	img, err := NewImageCard("i1", "image/png", pngHeader)
	if err != nil {
		t.Fatal(err)
	}
	img.Title = "Plot"
	b := img.Body.(*ImageBody)
	b.FileName, b.Width = "plot.png", 10
	raw, err := img.PayloadJSON()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(raw), &m)
	if m["id"] != "i1" || m["type"] != "image" || m["title"] != "Plot" {
		t.Errorf("envelope = %s", raw)
	}
	body := m["body"].(map[string]any)
	for _, k := range []string{"src", "mimeType", "fileName", "width"} {
		if _, ok := body[k]; !ok {
			t.Errorf("image body missing %q: %s", k, raw)
		}
	}
	if !strings.HasPrefix(body["src"].(string), "data:image/png;base64,") {
		t.Errorf("src = %v", body["src"])
	}

	ch, err := NewChartCard("c1", "", map[string]any{"mark": "bar"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = ch.PayloadJSON()
	m = nil
	_ = json.Unmarshal([]byte(raw), &m)
	cb := m["body"].(map[string]any)
	if m["type"] != "chart" || cb["format"] != "vega-lite" || cb["spec"] == nil {
		t.Errorf("chart payload = %s", raw)
	}
}

func TestHostDefinedType(t *testing.T) {
	raw, err := New("b1", "budget-table", map[string]any{"total": 12}).PayloadJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"type":"budget-table"`) || !strings.Contains(raw, `"total":12`) {
		t.Errorf("payload = %s", raw)
	}
	if _, err := New("b1", "x", []int{1}).PayloadJSON(); !errors.Is(err, ErrInvalid) {
		t.Errorf("array body: %v", err)
	}
	if _, err := New("", "x", map[string]any{}).PayloadJSON(); !errors.Is(err, ErrInvalid) {
		t.Errorf("no id: %v", err)
	}
	if _, err := New("b", "", map[string]any{}).PayloadJSON(); !errors.Is(err, ErrInvalid) {
		t.Errorf("no type: %v", err)
	}
	huge := map[string]any{"s": strings.Repeat("x", MaxPayloadBytes)}
	if _, err := New("b", "x", huge).PayloadJSON(); !errors.Is(err, ErrInvalid) {
		t.Errorf("oversize: %v", err)
	}
}

func TestLimitsAndValidation(t *testing.T) {
	if _, err := NewImageCard("i", "image/png", make([]byte, ImageMaxBytes+1)); !errors.Is(err, ErrInvalid) {
		t.Errorf("oversize image: %v", err)
	}
	if _, err := NewImageCard("i", "image/bmp", pngHeader); !errors.Is(err, ErrInvalid) {
		t.Errorf("bmp: %v", err)
	}
	if _, err := NewChartCard("c", "", nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty spec: %v", err)
	}
	if _, err := NewChartCard("c", "plotly", map[string]any{"a": 1}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad format: %v", err)
	}
	big := map[string]any{"data": map[string]any{"values": strings.Repeat("x", ChartMaxSpecBytes)}}
	if _, err := NewChartCard("c", "", big); !errors.Is(err, ErrInvalid) {
		t.Errorf("oversize spec: %v", err)
	}
}

func TestSniffImageMIME(t *testing.T) {
	if got := SniffImageMIME(pngHeader, "image/jpeg"); got != "image/png" {
		t.Errorf("png bytes named .jpg: %q", got)
	}
	if got := SniffImageMIME([]byte("hello"), "image/png"); got != "" {
		t.Errorf("text as png: %q", got)
	}
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	if got := SniffImageMIME(svg, "image/svg+xml"); got != "image/svg+xml" {
		t.Errorf("svg: %q", got)
	}
	if got := SniffImageMIME(svg, ""); got != "" {
		t.Errorf("unnamed svg: %q", got)
	}
	if got := SniffImageMIME([]byte("not svg"), "image/svg+xml"); got != "" {
		t.Errorf("fake svg: %q", got)
	}
}

func TestSummariesNeverCarryData(t *testing.T) {
	img, _ := NewImageCard("i", "image/png", pngHeader)
	if s := img.Summary(); strings.Contains(s, "base64") || s != `[Shown to the user: image "an image"]` {
		t.Errorf("image summary = %q", s)
	}
	ch, _ := NewChartCard("c", "", map[string]any{"title": "Spend", "data": map[string]any{"values": []any{map[string]any{"secret": 42}}}})
	ch.Caption = "Q3"
	if s := ch.Summary(); s != `[Shown to the user: chart "Spend" — Q3]` || strings.Contains(s, "secret") {
		t.Errorf("chart summary = %q", s)
	}
}
