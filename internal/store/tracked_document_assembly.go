package store

import (
	"sort"
	"strconv"
	"strings"
)

// Send-time assembly for tracked documents and the session seed.
//
// These functions turn immutable stored state (TrackedDocumentVersionRow snapshots,
// the session InitialSeed) into the model-facing text injected into outbound
// messages. They are PURE and deterministic: given the same rows and turn id they
// always produce the same bytes. That determinism is what guarantees prompt-cache
// prefix stability — a historical turn's injected form never shifts because it is a
// function of frozen rows, not of "latest" state.
//
// Representation choice: a changed document re-sends its FULL current body (labelled
// "updated"), it is not diffed. This mirrors the "mark up the PDF and re-send it"
// model and favors reliability over token savings — the model always sees complete
// current content rather than reconstructing it from a patch. A diff-based form can
// be layered on later as a pure token optimization without changing storage.

// TrackedDocChangeInitial / ...Updated label a rendered block so the model knows
// whether it is seeing a document for the first time or a revision.
const (
	trackedDocChangeInitial = "initial"
	trackedDocChangeUpdated = "updated"
)

// RenderTrackedDocInjection renders one document version as a self-describing,
// parseable block for the model. seq 0 is the first sighting ("initial"); any later
// seq is a revision ("updated") carrying the full new body.
func RenderTrackedDocInjection(row TrackedDocumentVersionRow) string {
	change := trackedDocChangeUpdated
	if row.Seq == 0 {
		change = trackedDocChangeInitial
	}
	var b strings.Builder
	b.WriteString(`<tracked_document id="`)
	b.WriteString(xmlAttrEscape(row.DocumentID))
	b.WriteString(`" name="`)
	b.WriteString(xmlAttrEscape(row.Name))
	b.WriteString(`" version="`)
	b.WriteString(strconv.Itoa(row.Seq))
	b.WriteString(`" change="`)
	b.WriteString(change)
	b.WriteString("\">\n<content>\n")
	b.WriteString(row.Content)
	b.WriteString("\n</content>\n</tracked_document>")
	return b.String()
}

// TrackedDocInjectionsForTurn returns the concatenated document blocks that should be
// injected at the outbound message anchored to turnID: exactly the versions whose
// AnchorTurnID equals turnID (a version is injected once, at the turn it was attached).
// Ordered by document id then version for stable output. Empty string when none.
func TrackedDocInjectionsForTurn(versions []TrackedDocumentVersionRow, turnID string) string {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return ""
	}
	var matched []TrackedDocumentVersionRow
	for _, v := range versions {
		if strings.TrimSpace(v.AnchorTurnID) == turnID {
			matched = append(matched, v)
		}
	}
	if len(matched) == 0 {
		return ""
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].DocumentID != matched[j].DocumentID {
			return matched[i].DocumentID < matched[j].DocumentID
		}
		return matched[i].Seq < matched[j].Seq
	})
	blocks := make([]string, 0, len(matched))
	for _, v := range matched {
		blocks = append(blocks, RenderTrackedDocInjection(v))
	}
	return strings.Join(blocks, "\n\n")
}

// TrackedDocInjectionsByTurn buckets a session's versions by anchor turn, rendering
// each turn's injected block once. The returned map is keyed by turn id; callers splice
// each value into the outbound user message for that turn. Turns with no attached
// versions are absent from the map.
func TrackedDocInjectionsByTurn(versions []TrackedDocumentVersionRow) map[string]string {
	byTurn := map[string][]TrackedDocumentVersionRow{}
	order := map[string]struct{}{}
	for _, v := range versions {
		turn := strings.TrimSpace(v.AnchorTurnID)
		if turn == "" {
			continue
		}
		byTurn[turn] = append(byTurn[turn], v)
		order[turn] = struct{}{}
	}
	out := make(map[string]string, len(byTurn))
	for turn, vs := range byTurn {
		sort.SliceStable(vs, func(i, j int) bool {
			if vs[i].DocumentID != vs[j].DocumentID {
				return vs[i].DocumentID < vs[j].DocumentID
			}
			return vs[i].Seq < vs[j].Seq
		})
		blocks := make([]string, 0, len(vs))
		for _, v := range vs {
			blocks = append(blocks, RenderTrackedDocInjection(v))
		}
		out[turn] = strings.Join(blocks, "\n\n")
	}
	return out
}

// TrackedDocInjectionsByMessage buckets a session's versions by the user message they
// were attached to (AnchorMessageID), rendering each message's injected block once.
// Keyed by user message id; callers splice each value ahead of that user message's
// content at send time. Versions with no anchor message are skipped.
func TrackedDocInjectionsByMessage(versions []TrackedDocumentVersionRow) map[string]string {
	byMsg := map[string][]TrackedDocumentVersionRow{}
	for _, v := range versions {
		msg := strings.TrimSpace(v.AnchorMessageID)
		if msg == "" {
			continue
		}
		byMsg[msg] = append(byMsg[msg], v)
	}
	out := make(map[string]string, len(byMsg))
	for msg, vs := range byMsg {
		sort.SliceStable(vs, func(i, j int) bool {
			if vs[i].DocumentID != vs[j].DocumentID {
				return vs[i].DocumentID < vs[j].DocumentID
			}
			return vs[i].Seq < vs[j].Seq
		})
		blocks := make([]string, 0, len(vs))
		for _, v := range vs {
			blocks = append(blocks, RenderTrackedDocInjection(v))
		}
		out[msg] = strings.Join(blocks, "\n\n")
	}
	return out
}

// PrependContext joins a leading context block (seed and/or tracked-doc injections)
// ahead of the user's own text with a stable separator, matching the convention the
// client display layer strips. Empty context returns userText unchanged. This is used
// at send time only; the persisted user content stays exactly what the user typed.
func PrependContext(context, userText string) string {
	ctx := strings.TrimSpace(context)
	if ctx == "" {
		return userText
	}
	return ctx + "\n\n---\n\n" + userText
}

// BuildFirstTurnContext composes the lead context for a session's FIRST outbound user
// message: the session seed first (if any), then any tracked-document blocks anchored
// to that turn. Later turns pass an empty seed.
func BuildFirstTurnContext(seed, trackedBlocks string) string {
	parts := make([]string, 0, 2)
	if s := strings.TrimSpace(seed); s != "" {
		parts = append(parts, s)
	}
	if t := strings.TrimSpace(trackedBlocks); t != "" {
		parts = append(parts, t)
	}
	return strings.Join(parts, "\n\n")
}

func xmlAttrEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"\"", "&quot;",
		"<", "&lt;",
		">", "&gt;",
	)
	return r.Replace(s)
}
