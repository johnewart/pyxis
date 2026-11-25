package server

import (
	"context"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	llmdlog "github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/protobuf/proto"
)

// injectTrackedContextForSend persists this turn's tracked-document snapshots and the
// session seed, then splices their model-facing blocks into the OUTBOUND user messages.
//
// Invariants:
//   - The persisted store is never mutated: histMsgs/incoming stay raw. We clone any
//     user message we augment and return a new slice, so persisted content is exactly
//     what the user typed and display needs no redaction.
//   - Message COUNT is unchanged (only user-message content is prefixed), so stateful
//     provider watermarks and history bookkeeping are unaffected.
//   - Injection is derived from immutable, append-only version rows + the write-once
//     session seed, so a historical turn's injected form is deterministic (frozen) and
//     prompt-cache prefixes stay stable across sends.
//
// A no-op when tracked documents are disabled, the session is ephemeral, or the turn
// carries no tracked documents / seed (backward-compatible with clients that don't send
// them yet).
func (s *LlmServer) injectTrackedContextForSend(
	ctx context.Context,
	sc serverutil.Scope,
	sid string,
	req *llmv1.ChatStreamRequest,
	full []*llmv1.Message,
) []*llmv1.Message {
	if sid == "" || strings.TrimSpace(sc.TenantID) == "" || (s.TrackedDocs == nil && s.Attachments == nil) {
		return full
	}

	// The current turn's user message anchors this turn's document versions.
	curUserID := ""
	msgs := req.GetMessages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].GetRole() == "user" {
			curUserID = strings.TrimSpace(msgs[i].GetId())
			break
		}
	}

	// 1. Persist this turn's snapshots. AppendVersion hash-dedups, so re-sending an
	//    unchanged document is a no-op and a changed one appends a new version.
	if s.TrackedDocs != nil {
		for _, td := range req.GetTrackedDocuments() {
			docID := strings.TrimSpace(td.GetDocumentId())
			if docID == "" {
				continue
			}
			if _, _, err := s.TrackedDocs.AppendVersion(ctx, sc, sid, store.TrackedDocInput{
				DocumentID:      docID,
				Name:            td.GetName(),
				Content:         td.GetContent(),
				AnchorMessageID: curUserID,
			}); err != nil {
				llmdlog.Stdout("server", llmdlog.LevelWarn,
					"tracked document persist failed: session=%q doc=%q err=%v", sid, docID, err)
			}
		}
	}

	// 2. Persist the session seed once (first time provided); read the effective seed.
	seed := ""
	if s.SessionRepo != nil {
		if sess, _ := s.SessionRepo.GetSession(ctx, sc, sid); sess != nil {
			seed = sess.InitialSeed
		}
		if reqSeed := strings.TrimSpace(req.GetInitialSeed()); reqSeed != "" && strings.TrimSpace(seed) == "" {
			if err := s.SessionRepo.UpdateSession(ctx, sc, sid, map[string]any{"initial_seed": reqSeed}); err != nil {
				llmdlog.Stdout("server", llmdlog.LevelWarn, "session seed persist failed: session=%q err=%v", sid, err)
			}
			seed = reqSeed
		}
	}

	// 3. Load all versions and bucket the rendered blocks by anchor user message.
	byMsg := map[string]string{}
	if s.TrackedDocs != nil {
		versions, err := s.TrackedDocs.ListSessionVersions(ctx, sc, sid)
		if err != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn, "tracked document load failed: session=%q err=%v", sid, err)
		} else {
			byMsg = store.TrackedDocInjectionsByMessage(versions)
		}
	}

	// 3b. Fold in provider-facing attachment content (ask_card_response summaries,
	// etc.), keyed to the same anchor message, appended after any tracked-doc
	// block for that message.
	addBlock := func(msg, content string) {
		if existing := byMsg[msg]; strings.TrimSpace(existing) != "" {
			byMsg[msg] = existing + "\n\n" + content
		} else {
			byMsg[msg] = content
		}
	}
	seenAttachment := map[string]bool{}
	if s.Attachments != nil {
		injections, err := s.Attachments.ListInjectableContent(ctx, sc, sid)
		if err != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn, "attachment content load failed: session=%q err=%v", sid, err)
		} else {
			for _, inj := range injections {
				msg := strings.TrimSpace(inj.AnchorMessageID)
				if msg == "" {
					continue
				}
				if id := strings.TrimSpace(inj.ID); id != "" {
					seenAttachment[id] = true
				}
				addBlock(msg, inj.Content)
			}
		}
	}

	// 3c. Attachments carried on THIS send's user messages. They are persisted
	// (persistIncomingAttachments) only once the user message itself is stored,
	// which happens after this runs, so read them from the request here. Rows
	// already persisted (a re-sent message) were folded in above; skip them.
	for _, m := range msgs {
		if m.GetRole() != "user" {
			continue
		}
		msgID := strings.TrimSpace(m.GetId())
		if msgID == "" {
			continue
		}
		for _, att := range m.GetAttachments() {
			if strings.TrimSpace(att.GetContent()) == "" || seenAttachment[strings.TrimSpace(att.GetId())] {
				continue
			}
			addBlock(msgID, att.GetContent())
		}
	}

	if len(byMsg) == 0 && strings.TrimSpace(seed) == "" {
		return full
	}

	// 4. Splice blocks into cloned user messages. The seed leads the FIRST user message.
	out := make([]*llmv1.Message, len(full))
	firstUserSeen := false
	for i, m := range full {
		if m.GetRole() != "user" {
			out[i] = m
			continue
		}
		block := byMsg[strings.TrimSpace(m.GetId())]
		lead := block
		if !firstUserSeen {
			lead = store.BuildFirstTurnContext(seed, block)
			firstUserSeen = true
		}
		if strings.TrimSpace(lead) == "" {
			out[i] = m
			continue
		}
		clone, _ := proto.Clone(m).(*llmv1.Message)
		if clone == nil {
			out[i] = m
			continue
		}
		clone.Content = store.PrependContext(lead, m.GetContent())
		out[i] = clone
	}
	return out
}
