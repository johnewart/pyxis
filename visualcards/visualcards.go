/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

// Package visualcards is the Go side of llmd's display cards: non-interactive
// content embedded in the chat transcript (an image, a chart, a host-defined
// table). It mirrors @pyxis-llm/core's displayCard.ts, imageCard.ts and
// chartCard.ts — the one attachment kind, the envelope, the built-in body
// types and their limits — so a Go producer cannot drift from what
// @pyxis-llm/react renders. TestPayloadShapesMatchCore pins the JSON.
//
// A card is one attachment of kind Kind whose payload is
// {id, type, title?, caption?, body}. What it shows is its Type; Body is that
// type's data. llmd ships two types (TypeImage, TypeChart); a host may define
// its own, rendered by its own @pyxis-llm/react plugin.
//
// The payload is display-only: llmd never sends payload_json to a provider,
// so a card costs the model no context whatever its body holds. Built-in
// bodies are inline — images as base64 data: URLs, chart data as
// `data.values` — because renderers refuse to fetch anything, and because a
// snapshot survives its source file going away.
//
// This package only builds and validates payloads; it does not deliver them.
package visualcards

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const (
	// Kind is the attachment kind of every display card.
	Kind = "llmd.display"

	// TypeImage and TypeChart are the built-in card types.
	TypeImage = "image"
	TypeChart = "chart"

	// MaxPayloadBytes bounds a whole card (3 MiB of JSON), so the frame
	// carrying it stays inside gRPC's default 4 MiB message limit.
	MaxPayloadBytes = 3 * 1024 * 1024
	// ImageMaxBytes is the largest decoded image accepted (2 MiB).
	ImageMaxBytes = 2 * 1024 * 1024
	// ChartMaxSpecBytes is the largest serialized chart spec accepted (1 MiB
	// of JSON, data included).
	ChartMaxSpecBytes = 1024 * 1024

	// FormatVegaLite and FormatVega are the chart spec formats.
	FormatVegaLite = "vega-lite"
	FormatVega     = "vega"
)

// ImageMIMETypes are the accepted image types. SVG is shown through <img>,
// where its scripts do not run.
var ImageMIMETypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/svg+xml"}

// ErrInvalid wraps every validation failure, so a tool can report it to the
// model as a usage error.
var ErrInvalid = errors.New("invalid card")

// Card is a display card: the envelope plus its type's body. Body is any
// JSON-serializable value; ImageBody and ChartBody are the built-in ones.
type Card struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Title   string `json:"title,omitempty"`
	Caption string `json:"caption,omitempty"`
	Body    any    `json:"body"`
}

// ImageBody is the body of a TypeImage card.
type ImageBody struct {
	Src      string `json:"src"` // data:<mime>;base64,<bytes>
	MimeType string `json:"mimeType"`
	Alt      string `json:"alt,omitempty"`
	FileName string `json:"fileName,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
}

// ChartBody is the body of a TypeChart card.
type ChartBody struct {
	Format string         `json:"format"`
	Spec   map[string]any `json:"spec"`
}

// ImageMIMEForExt returns the image type for a file extension (with or
// without the dot), or "" when it is not an accepted image.
func ImageMIMEForExt(ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "svg":
		return "image/svg+xml"
	}
	return ""
}

func allowedMIME(m string) bool {
	for _, a := range ImageMIMETypes {
		if a == m {
			return true
		}
	}
	return false
}

// SniffImageMIME returns the accepted image type of data, judged from its
// bytes, or "" when it is none of them. claimed is what the file name says;
// it is only trusted for SVG, which is text and has no magic number, and
// then only when the bytes look like an SVG document.
func SniffImageMIME(data []byte, claimed string) string {
	if claimed == "image/svg+xml" {
		head := data
		if len(head) > 4096 {
			head = head[:4096]
		}
		if bytes.Contains(bytes.ToLower(head), []byte("<svg")) {
			return claimed
		}
		return ""
	}
	m := http.DetectContentType(data)
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = m[:i]
	}
	if m == "image/svg+xml" || !allowedMIME(m) {
		return ""
	}
	return m
}

// New builds a card of any type. Validate is applied by PayloadJSON.
func New(id, typ string, body any) Card {
	return Card{ID: id, Type: typ, Body: body}
}

// NewImageCard builds an image card from raw bytes. mime must be one of
// ImageMIMETypes; the size limit applies to data.
func NewImageCard(id, mime string, data []byte) (Card, error) {
	if !allowedMIME(mime) {
		return Card{}, fmt.Errorf("%w: unsupported image type %q (allowed: %s)", ErrInvalid, mime, strings.Join(ImageMIMETypes, ", "))
	}
	if len(data) == 0 {
		return Card{}, fmt.Errorf("%w: image data is empty", ErrInvalid)
	}
	if len(data) > ImageMaxBytes {
		return Card{}, fmt.Errorf("%w: image is %d bytes; the limit is %d", ErrInvalid, len(data), ImageMaxBytes)
	}
	c := New(id, TypeImage, &ImageBody{
		Src:      "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data),
		MimeType: mime,
	})
	return c, c.validateEnvelope()
}

// NewChartCard builds a chart card. format "" means Vega-Lite.
func NewChartCard(id, format string, spec map[string]any) (Card, error) {
	if format == "" {
		format = FormatVegaLite
	}
	if format != FormatVegaLite && format != FormatVega {
		return Card{}, fmt.Errorf("%w: unsupported chart format %q (allowed: %s, %s)", ErrInvalid, format, FormatVegaLite, FormatVega)
	}
	if len(spec) == 0 {
		return Card{}, fmt.Errorf("%w: chart spec is empty", ErrInvalid)
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return Card{}, fmt.Errorf("%w: chart spec is not serializable: %v", ErrInvalid, err)
	}
	if len(raw) > ChartMaxSpecBytes {
		return Card{}, fmt.Errorf("%w: chart spec is %d bytes; the limit is %d", ErrInvalid, len(raw), ChartMaxSpecBytes)
	}
	c := New(id, TypeChart, &ChartBody{Format: format, Spec: spec})
	return c, c.validateEnvelope()
}

func (c Card) validateEnvelope() error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("%w: card needs an id", ErrInvalid)
	}
	if strings.TrimSpace(c.Type) == "" {
		return fmt.Errorf("%w: card needs a type", ErrInvalid)
	}
	if c.Body == nil {
		return fmt.Errorf("%w: card needs a body", ErrInvalid)
	}
	return nil
}

// PayloadJSON is the attachment payload for the card, checked against the
// envelope rules and MaxPayloadBytes. The body must serialize to a JSON object.
func (c Card) PayloadJSON() (string, error) {
	if err := c.validateEnvelope(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("%w: encode card: %v", ErrInvalid, err)
	}
	if len(raw) > MaxPayloadBytes {
		return "", fmt.Errorf("%w: card is %d bytes; the limit is %d", ErrInvalid, len(raw), MaxPayloadBytes)
	}
	var probe struct {
		Body json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil || len(probe.Body) == 0 || probe.Body[0] != '{' {
		return "", fmt.Errorf("%w: card body must be a JSON object", ErrInvalid)
	}
	return string(raw), nil
}

func article(word string) string {
	if word != "" && strings.ContainsRune("aeiouAEIOU", rune(word[0])) {
		return "an"
	}
	return "a"
}

// Summary is a one-line, model-facing note that the user was shown the card
// (synthesizeDisplayCardContent in core). It never includes the body.
func (c Card) Summary() string {
	label := strings.TrimSpace(c.Title)
	if label == "" {
		switch b := c.Body.(type) {
		case *ImageBody:
			label = strings.TrimSpace(b.FileName)
		case *ChartBody:
			if t, ok := b.Spec["title"].(string); ok {
				label = strings.TrimSpace(t)
			} else if t, ok := b.Spec["title"].(map[string]any); ok {
				label, _ = t["text"].(string)
			}
			if label == "" {
				label, _ = b.Spec["description"].(string)
			}
		}
	}
	if label = strings.TrimSpace(label); label == "" {
		label = article(c.Type) + " " + c.Type
	}
	caption := ""
	if s := strings.TrimSpace(c.Caption); s != "" {
		caption = " — " + s
	}
	return fmt.Sprintf("[Shown to the user: %s %q%s]", c.Type, label, caption)
}
