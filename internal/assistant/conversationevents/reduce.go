package conversationevents

import (
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// SchemaVersion matches ai-library CONVERSATION_EVENT_SCHEMA_VERSION.
const SchemaVersion int32 = 2

type TurnStatus string

const (
	TurnIdle      TurnStatus = "idle"
	TurnActive    TurnStatus = "active"
	TurnCompleted TurnStatus = "completed"
	TurnFailed    TurnStatus = "failed"
)

// TurnState is the folded active assistant turn (TS TurnState).
type TurnState struct {
	TurnID           string
	TurnGroupRootID  string
	Thinking         string
	ActivityHeadline string
	Assistant        string
	ToolCalls        map[string]struct{ Name, ArgumentsJSON string }
	PendingToolArgs  map[string]struct{ Name, ArgsJSON string }
	Invocations      []struct {
		ToolCallID, Name string
		Success          bool
	}
	Status         TurnStatus
	Error          string
	AssistantMsgID string
	FinishReason   string
	InputTokens    int64
	OutputTokens   int64
}

// ConversationState is the folded conversation (TS ConversationState).
type ConversationState struct {
	ActiveAssistantTurn        TurnState
	LastCommittedUserMessageID string
	Aborted                    bool
}

func IdleTurnState() TurnState {
	return TurnState{
		Status:          TurnIdle,
		ToolCalls:       map[string]struct{ Name, ArgumentsJSON string }{},
		PendingToolArgs: map[string]struct{ Name, ArgsJSON string }{},
	}
}

func InitialConversationState() ConversationState {
	return ConversationState{ActiveAssistantTurn: IdleTurnState()}
}

func initialTurnState(turnID string) TurnState {
	return TurnState{
		TurnID:          turnID,
		Status:          TurnActive,
		ToolCalls:       map[string]struct{ Name, ArgumentsJSON string }{},
		PendingToolArgs: map[string]struct{ Name, ArgsJSON string }{},
	}
}

func baseTurnStateForStreamContent(state TurnState, turnID string) TurnState {
	if state.TurnID == "" {
		return initialTurnState(turnID)
	}
	if state.TurnID == turnID {
		return state
	}
	state.TurnID = turnID
	return state
}

func baseTurnStateForTools(state TurnState, turnID string) TurnState {
	if state.TurnID == "" {
		return initialTurnState(turnID)
	}
	return state
}

// ReduceActiveAssistantTurn folds streaming / tool events into the active turn.
func ReduceActiveAssistantTurn(state TurnState, frame *llmv1.ConversationStreamFrame) TurnState {
	if frame == nil {
		return state
	}
	switch {
	case frame.GetAssistantTurnStarted() != nil:
		ev := frame.GetAssistantTurnStarted()
		parent := ev.GetParentTurnGroupId()
		isCont := parent != "" && state.TurnGroupRootID != "" && parent == state.TurnGroupRootID
		if isCont {
			next := initialTurnState(ev.GetTurnId())
			next.Assistant = state.Assistant
			next.Thinking = state.Thinking
			next.ActivityHeadline = state.ActivityHeadline
			next.TurnGroupRootID = state.TurnGroupRootID
			return next
		}
		root := ev.GetTurnId()
		if parent != "" {
			root = parent
		}
		next := initialTurnState(ev.GetTurnId())
		next.TurnGroupRootID = root
		return next

	case frame.GetAssistantThinkingDelta() != nil:
		ev := frame.GetAssistantThinkingDelta()
		base := baseTurnStateForStreamContent(state, ev.GetTurnId())
		base.Thinking += ev.GetDelta()
		if base.Status == TurnIdle {
			base.Status = TurnActive
		}
		return base

	case frame.GetAssistantActivitySnapshot() != nil:
		ev := frame.GetAssistantActivitySnapshot()
		base := baseTurnStateForStreamContent(state, ev.GetTurnId())
		base.ActivityHeadline = ev.GetHeadline()
		if base.Status == TurnIdle {
			base.Status = TurnActive
		}
		return base

	case frame.GetAssistantTextDelta() != nil:
		ev := frame.GetAssistantTextDelta()
		base := baseTurnStateForStreamContent(state, ev.GetTurnId())
		base.Assistant += ev.GetDelta()
		if base.Status == TurnIdle {
			base.Status = TurnActive
		}
		return base

	case frame.GetToolCallStreamStarted() != nil:
		ev := frame.GetToolCallStreamStarted()
		base := baseTurnStateForTools(state, ev.GetTurnId())
		id := ev.GetId()
		pending := clonePending(base.PendingToolArgs)
		cur, ok := pending[id]
		if !ok {
			cur.Name = ev.GetName()
		} else if ev.GetName() != "" {
			cur.Name = ev.GetName()
		}
		pending[id] = cur
		base.PendingToolArgs = pending
		base.Status = TurnActive
		return base

	case frame.GetToolCallArgsDelta() != nil:
		ev := frame.GetToolCallArgsDelta()
		base := baseTurnStateForTools(state, ev.GetTurnId())
		id := ev.GetId()
		pending := clonePending(base.PendingToolArgs)
		prev := pending[id]
		prev.ArgsJSON += ev.GetArgsDelta()
		pending[id] = prev
		base.PendingToolArgs = pending
		base.Status = TurnActive
		return base

	case frame.GetToolCallStreamFinished() != nil:
		ev := frame.GetToolCallStreamFinished()
		base := baseTurnStateForTools(state, ev.GetTurnId())
		id := ev.GetId()
		pending := clonePending(base.PendingToolArgs)
		delete(pending, id)
		tools := cloneToolCalls(base.ToolCalls)
		tools[id] = struct{ Name, ArgumentsJSON string }{Name: ev.GetName(), ArgumentsJSON: ev.GetArgumentsJson()}
		base.PendingToolArgs = pending
		base.ToolCalls = tools
		base.Status = TurnActive
		return base

	case frame.GetToolExecutionStarted() != nil:
		base := baseTurnStateForTools(state, frame.GetToolExecutionStarted().GetTurnId())
		base.Status = TurnActive
		return base

	case frame.GetToolExecutionFinished() != nil:
		ev := frame.GetToolExecutionFinished()
		base := baseTurnStateForTools(state, ev.GetTurnId())
		inv := append([]struct {
			ToolCallID, Name string
			Success          bool
		}{}, base.Invocations...)
		inv = append(inv, struct {
			ToolCallID, Name string
			Success          bool
		}{ToolCallID: ev.GetToolCallId(), Name: ev.GetName(), Success: ev.GetSuccess()})
		base.Invocations = inv
		base.Status = TurnActive
		return base

	case frame.GetAssistantTurnFinished() != nil:
		ev := frame.GetAssistantTurnFinished()
		base := baseTurnStateForStreamContent(state, ev.GetTurnId())
		base.Status = TurnCompleted
		if ev.GetAssistantMessageId() != "" {
			base.AssistantMsgID = ev.GetAssistantMessageId()
		}
		if ev.GetFinishReason() != "" {
			base.FinishReason = ev.GetFinishReason()
		}
		if u := ev.GetUsage(); u != nil {
			base.InputTokens = u.GetInputTokens()
			base.OutputTokens = u.GetOutputTokens()
		}
		return base

	case frame.GetAssistantTurnFailed() != nil:
		ev := frame.GetAssistantTurnFailed()
		base := baseTurnStateForStreamContent(state, ev.GetTurnId())
		base.Status = TurnFailed
		base.Error = ev.GetError()
		return base

	default:
		return state
	}
}

// ReduceConversation folds one wire frame into conversation state.
func ReduceConversation(state ConversationState, frame *llmv1.ConversationStreamFrame) ConversationState {
	if frame == nil {
		return state
	}
	switch {
	case frame.GetUserTurnCommitted() != nil:
		ev := frame.GetUserTurnCommitted()
		return ConversationState{
			ActiveAssistantTurn:        IdleTurnState(),
			LastCommittedUserMessageID: ev.GetUserMessageId(),
			Aborted:                    state.Aborted,
		}
	case frame.GetConversationAborted() != nil:
		return ConversationState{
			ActiveAssistantTurn: IdleTurnState(),
			Aborted:             true,
		}
	case frame.GetStreamComplete() != nil:
		return state
	default:
		state.ActiveAssistantTurn = ReduceActiveAssistantTurn(state.ActiveAssistantTurn, frame)
		return state
	}
}

func clonePending(in map[string]struct{ Name, ArgsJSON string }) map[string]struct{ Name, ArgsJSON string } {
	out := make(map[string]struct{ Name, ArgsJSON string }, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneToolCalls(in map[string]struct{ Name, ArgumentsJSON string }) map[string]struct{ Name, ArgumentsJSON string } {
	out := make(map[string]struct{ Name, ArgumentsJSON string }, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}
