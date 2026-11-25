from llmv1 import llm_pb2 as _llm_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ContentScope(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CONTENT_SCOPE_FULL: _ClassVar[ContentScope]
    CONTENT_SCOPE_LATEST: _ClassVar[ContentScope]
    CONTENT_SCOPE_LATEST_N: _ClassVar[ContentScope]

class Cadence(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CADENCE_EVERY_ROUND: _ClassVar[Cadence]
    CADENCE_FIRST_ROUND: _ClassVar[Cadence]
    CADENCE_EVERY_N_ROUNDS: _ClassVar[Cadence]

class Decision(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    DECISION_ALLOW: _ClassVar[Decision]
    DECISION_DENY: _ClassVar[Decision]

class PolicyPurpose(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    POLICY_PURPOSE_UNSPECIFIED: _ClassVar[PolicyPurpose]
    POLICY_PURPOSE_CHAT_STREAM: _ClassVar[PolicyPurpose]
    POLICY_PURPOSE_COMPLETE: _ClassVar[PolicyPurpose]

class PolicyEntryPath(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    POLICY_ENTRY_PATH_UNSPECIFIED: _ClassVar[PolicyEntryPath]
    POLICY_ENTRY_PATH_AGENT_LOOP: _ClassVar[PolicyEntryPath]
    POLICY_ENTRY_PATH_MCP_SERVER: _ClassVar[PolicyEntryPath]
CONTENT_SCOPE_FULL: ContentScope
CONTENT_SCOPE_LATEST: ContentScope
CONTENT_SCOPE_LATEST_N: ContentScope
CADENCE_EVERY_ROUND: Cadence
CADENCE_FIRST_ROUND: Cadence
CADENCE_EVERY_N_ROUNDS: Cadence
DECISION_ALLOW: Decision
DECISION_DENY: Decision
POLICY_PURPOSE_UNSPECIFIED: PolicyPurpose
POLICY_PURPOSE_CHAT_STREAM: PolicyPurpose
POLICY_PURPOSE_COMPLETE: PolicyPurpose
POLICY_ENTRY_PATH_UNSPECIFIED: PolicyEntryPath
POLICY_ENTRY_PATH_AGENT_LOOP: PolicyEntryPath
POLICY_ENTRY_PATH_MCP_SERVER: PolicyEntryPath

class CheckPolicy(_message.Message):
    __slots__ = ("content", "latest_n", "cadence", "every_n_rounds", "include_system_prompt", "include_tool_results")
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    LATEST_N_FIELD_NUMBER: _ClassVar[int]
    CADENCE_FIELD_NUMBER: _ClassVar[int]
    EVERY_N_ROUNDS_FIELD_NUMBER: _ClassVar[int]
    INCLUDE_SYSTEM_PROMPT_FIELD_NUMBER: _ClassVar[int]
    INCLUDE_TOOL_RESULTS_FIELD_NUMBER: _ClassVar[int]
    content: ContentScope
    latest_n: int
    cadence: Cadence
    every_n_rounds: int
    include_system_prompt: bool
    include_tool_results: bool
    def __init__(self, content: _Optional[_Union[ContentScope, str]] = ..., latest_n: _Optional[int] = ..., cadence: _Optional[_Union[Cadence, str]] = ..., every_n_rounds: _Optional[int] = ..., include_system_prompt: _Optional[bool] = ..., include_tool_results: _Optional[bool] = ...) -> None: ...

class PolicyScope(_message.Message):
    __slots__ = ("tenant_id", "workspace_id", "principal_id")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    WORKSPACE_ID_FIELD_NUMBER: _ClassVar[int]
    PRINCIPAL_ID_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    workspace_id: str
    principal_id: str
    def __init__(self, tenant_id: _Optional[str] = ..., workspace_id: _Optional[str] = ..., principal_id: _Optional[str] = ...) -> None: ...

class GetCheckPolicyRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class GetCheckPolicyResponse(_message.Message):
    __slots__ = ("llm_call", "filter_tools", "tool_call")
    LLM_CALL_FIELD_NUMBER: _ClassVar[int]
    FILTER_TOOLS_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_FIELD_NUMBER: _ClassVar[int]
    llm_call: CheckPolicy
    filter_tools: CheckPolicy
    tool_call: CheckPolicy
    def __init__(self, llm_call: _Optional[_Union[CheckPolicy, _Mapping]] = ..., filter_tools: _Optional[_Union[CheckPolicy, _Mapping]] = ..., tool_call: _Optional[_Union[CheckPolicy, _Mapping]] = ...) -> None: ...

class CheckLLMCallRequest(_message.Message):
    __slots__ = ("scope", "agent_id", "session_id", "turn_id", "round", "provider_id", "provider_type", "model", "purpose", "read_only", "messages")
    SCOPE_FIELD_NUMBER: _ClassVar[int]
    AGENT_ID_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    ROUND_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_TYPE_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    PURPOSE_FIELD_NUMBER: _ClassVar[int]
    READ_ONLY_FIELD_NUMBER: _ClassVar[int]
    MESSAGES_FIELD_NUMBER: _ClassVar[int]
    scope: PolicyScope
    agent_id: str
    session_id: str
    turn_id: str
    round: int
    provider_id: str
    provider_type: str
    model: str
    purpose: PolicyPurpose
    read_only: bool
    messages: _containers.RepeatedCompositeFieldContainer[_llm_pb2.Message]
    def __init__(self, scope: _Optional[_Union[PolicyScope, _Mapping]] = ..., agent_id: _Optional[str] = ..., session_id: _Optional[str] = ..., turn_id: _Optional[str] = ..., round: _Optional[int] = ..., provider_id: _Optional[str] = ..., provider_type: _Optional[str] = ..., model: _Optional[str] = ..., purpose: _Optional[_Union[PolicyPurpose, str]] = ..., read_only: _Optional[bool] = ..., messages: _Optional[_Iterable[_Union[_llm_pb2.Message, _Mapping]]] = ...) -> None: ...

class CheckLLMCallResponse(_message.Message):
    __slots__ = ("decision", "reason", "user_message", "policy_override")
    DECISION_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    USER_MESSAGE_FIELD_NUMBER: _ClassVar[int]
    POLICY_OVERRIDE_FIELD_NUMBER: _ClassVar[int]
    decision: Decision
    reason: str
    user_message: str
    policy_override: CheckPolicy
    def __init__(self, decision: _Optional[_Union[Decision, str]] = ..., reason: _Optional[str] = ..., user_message: _Optional[str] = ..., policy_override: _Optional[_Union[CheckPolicy, _Mapping]] = ...) -> None: ...

class FilterToolsRequest(_message.Message):
    __slots__ = ("scope", "agent_id", "session_id", "turn_id", "round", "provider_id", "provider_type", "model", "purpose", "read_only", "messages", "tools")
    SCOPE_FIELD_NUMBER: _ClassVar[int]
    AGENT_ID_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    ROUND_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_TYPE_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    PURPOSE_FIELD_NUMBER: _ClassVar[int]
    READ_ONLY_FIELD_NUMBER: _ClassVar[int]
    MESSAGES_FIELD_NUMBER: _ClassVar[int]
    TOOLS_FIELD_NUMBER: _ClassVar[int]
    scope: PolicyScope
    agent_id: str
    session_id: str
    turn_id: str
    round: int
    provider_id: str
    provider_type: str
    model: str
    purpose: PolicyPurpose
    read_only: bool
    messages: _containers.RepeatedCompositeFieldContainer[_llm_pb2.Message]
    tools: _containers.RepeatedCompositeFieldContainer[_llm_pb2.ToolDefinition]
    def __init__(self, scope: _Optional[_Union[PolicyScope, _Mapping]] = ..., agent_id: _Optional[str] = ..., session_id: _Optional[str] = ..., turn_id: _Optional[str] = ..., round: _Optional[int] = ..., provider_id: _Optional[str] = ..., provider_type: _Optional[str] = ..., model: _Optional[str] = ..., purpose: _Optional[_Union[PolicyPurpose, str]] = ..., read_only: _Optional[bool] = ..., messages: _Optional[_Iterable[_Union[_llm_pb2.Message, _Mapping]]] = ..., tools: _Optional[_Iterable[_Union[_llm_pb2.ToolDefinition, _Mapping]]] = ...) -> None: ...

class FilterToolsResponse(_message.Message):
    __slots__ = ("decision", "allowed_tool_names", "reason", "policy_override")
    DECISION_FIELD_NUMBER: _ClassVar[int]
    ALLOWED_TOOL_NAMES_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    POLICY_OVERRIDE_FIELD_NUMBER: _ClassVar[int]
    decision: Decision
    allowed_tool_names: _containers.RepeatedScalarFieldContainer[str]
    reason: str
    policy_override: CheckPolicy
    def __init__(self, decision: _Optional[_Union[Decision, str]] = ..., allowed_tool_names: _Optional[_Iterable[str]] = ..., reason: _Optional[str] = ..., policy_override: _Optional[_Union[CheckPolicy, _Mapping]] = ...) -> None: ...

class CheckToolCallRequest(_message.Message):
    __slots__ = ("scope", "agent_id", "session_id", "turn_id", "round", "provider_id", "provider_type", "model", "purpose", "read_only", "tool_call_id", "name", "arguments_json", "tool", "mutation", "delegated", "assistant_text", "entry_path", "messages")
    SCOPE_FIELD_NUMBER: _ClassVar[int]
    AGENT_ID_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    ROUND_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_TYPE_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    PURPOSE_FIELD_NUMBER: _ClassVar[int]
    READ_ONLY_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    ARGUMENTS_JSON_FIELD_NUMBER: _ClassVar[int]
    TOOL_FIELD_NUMBER: _ClassVar[int]
    MUTATION_FIELD_NUMBER: _ClassVar[int]
    DELEGATED_FIELD_NUMBER: _ClassVar[int]
    ASSISTANT_TEXT_FIELD_NUMBER: _ClassVar[int]
    ENTRY_PATH_FIELD_NUMBER: _ClassVar[int]
    MESSAGES_FIELD_NUMBER: _ClassVar[int]
    scope: PolicyScope
    agent_id: str
    session_id: str
    turn_id: str
    round: int
    provider_id: str
    provider_type: str
    model: str
    purpose: PolicyPurpose
    read_only: bool
    tool_call_id: str
    name: str
    arguments_json: str
    tool: _llm_pb2.ToolDefinition
    mutation: bool
    delegated: bool
    assistant_text: str
    entry_path: PolicyEntryPath
    messages: _containers.RepeatedCompositeFieldContainer[_llm_pb2.Message]
    def __init__(self, scope: _Optional[_Union[PolicyScope, _Mapping]] = ..., agent_id: _Optional[str] = ..., session_id: _Optional[str] = ..., turn_id: _Optional[str] = ..., round: _Optional[int] = ..., provider_id: _Optional[str] = ..., provider_type: _Optional[str] = ..., model: _Optional[str] = ..., purpose: _Optional[_Union[PolicyPurpose, str]] = ..., read_only: _Optional[bool] = ..., tool_call_id: _Optional[str] = ..., name: _Optional[str] = ..., arguments_json: _Optional[str] = ..., tool: _Optional[_Union[_llm_pb2.ToolDefinition, _Mapping]] = ..., mutation: _Optional[bool] = ..., delegated: _Optional[bool] = ..., assistant_text: _Optional[str] = ..., entry_path: _Optional[_Union[PolicyEntryPath, str]] = ..., messages: _Optional[_Iterable[_Union[_llm_pb2.Message, _Mapping]]] = ...) -> None: ...

class CheckToolCallResponse(_message.Message):
    __slots__ = ("decision", "reason", "user_message", "replacement_tool_result", "policy_override")
    DECISION_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    USER_MESSAGE_FIELD_NUMBER: _ClassVar[int]
    REPLACEMENT_TOOL_RESULT_FIELD_NUMBER: _ClassVar[int]
    POLICY_OVERRIDE_FIELD_NUMBER: _ClassVar[int]
    decision: Decision
    reason: str
    user_message: str
    replacement_tool_result: str
    policy_override: CheckPolicy
    def __init__(self, decision: _Optional[_Union[Decision, str]] = ..., reason: _Optional[str] = ..., user_message: _Optional[str] = ..., replacement_tool_result: _Optional[str] = ..., policy_override: _Optional[_Union[CheckPolicy, _Mapping]] = ...) -> None: ...
