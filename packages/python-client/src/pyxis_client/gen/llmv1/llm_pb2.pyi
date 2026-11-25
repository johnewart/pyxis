import datetime

from google.protobuf import duration_pb2 as _duration_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class AskCardType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ASK_CARD_TYPE_UNSPECIFIED: _ClassVar[AskCardType]
    ASK_CARD_TYPE_CHOICE: _ClassVar[AskCardType]
    ASK_CARD_TYPE_TEXT: _ClassVar[AskCardType]
    ASK_CARD_TYPE_FILE: _ClassVar[AskCardType]

class TextToolCallMode(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    TEXT_TOOL_CALL_MODE_UNSPECIFIED: _ClassVar[TextToolCallMode]
    TEXT_TOOL_CALL_MODE_STRIP: _ClassVar[TextToolCallMode]
    TEXT_TOOL_CALL_MODE_CONVERT: _ClassVar[TextToolCallMode]

class ToolClassification(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    TOOL_CLASSIFICATION_UNSPECIFIED: _ClassVar[ToolClassification]
    TOOL_CLASSIFICATION_QUERY: _ClassVar[ToolClassification]
    TOOL_CLASSIFICATION_MUTATION: _ClassVar[ToolClassification]

class ToolPolicy(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    TOOL_POLICY_UNSPECIFIED: _ClassVar[ToolPolicy]
    TOOL_POLICY_INHERIT_SERVER_WIDE: _ClassVar[ToolPolicy]
    TOOL_POLICY_PROFILE_ONLY: _ClassVar[ToolPolicy]

class LlmProviderType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    LLM_PROVIDER_TYPE_UNSPECIFIED: _ClassVar[LlmProviderType]
    LLM_PROVIDER_TYPE_OPENAI: _ClassVar[LlmProviderType]
    LLM_PROVIDER_TYPE_OLLAMA: _ClassVar[LlmProviderType]
    LLM_PROVIDER_TYPE_ANTHROPIC: _ClassVar[LlmProviderType]
    LLM_PROVIDER_TYPE_CURSOR_CLI: _ClassVar[LlmProviderType]
    LLM_PROVIDER_TYPE_CLAUDE_CODE: _ClassVar[LlmProviderType]
    LLM_PROVIDER_TYPE_BEDROCK: _ClassVar[LlmProviderType]
    LLM_PROVIDER_TYPE_CLAUDE_CODE_V2: _ClassVar[LlmProviderType]
    LLM_PROVIDER_TYPE_GEMINI: _ClassVar[LlmProviderType]
    LLM_PROVIDER_TYPE_OPENROUTER: _ClassVar[LlmProviderType]

class CursorCliMode(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CURSOR_CLI_MODE_UNSPECIFIED: _ClassVar[CursorCliMode]
    CURSOR_CLI_MODE_DEFAULT: _ClassVar[CursorCliMode]
    CURSOR_CLI_MODE_ASK: _ClassVar[CursorCliMode]
    CURSOR_CLI_MODE_PLAN: _ClassVar[CursorCliMode]

class ProviderConnectionStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    PROVIDER_CONNECTION_STATUS_UNSPECIFIED: _ClassVar[ProviderConnectionStatus]
    PROVIDER_CONNECTION_STATUS_OK: _ClassVar[ProviderConnectionStatus]
    PROVIDER_CONNECTION_STATUS_BINARY_NOT_FOUND: _ClassVar[ProviderConnectionStatus]
    PROVIDER_CONNECTION_STATUS_VERSION_TOO_OLD: _ClassVar[ProviderConnectionStatus]
    PROVIDER_CONNECTION_STATUS_NOT_AUTHENTICATED: _ClassVar[ProviderConnectionStatus]
    PROVIDER_CONNECTION_STATUS_RESUME_UNSUPPORTED: _ClassVar[ProviderConnectionStatus]
    PROVIDER_CONNECTION_STATUS_TIMED_OUT: _ClassVar[ProviderConnectionStatus]
    PROVIDER_CONNECTION_STATUS_FAILED: _ClassVar[ProviderConnectionStatus]

class CredentialSource(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CREDENTIAL_SOURCE_UNSPECIFIED: _ClassVar[CredentialSource]
    CREDENTIAL_SOURCE_DEFAULT_CHAIN: _ClassVar[CredentialSource]
    CREDENTIAL_SOURCE_ASSUMED_ROLE: _ClassVar[CredentialSource]
    CREDENTIAL_SOURCE_WEB_IDENTITY: _ClassVar[CredentialSource]
    CREDENTIAL_SOURCE_API_KEY: _ClassVar[CredentialSource]
    CREDENTIAL_SOURCE_PROFILE: _ClassVar[CredentialSource]
    CREDENTIAL_SOURCE_BEARER_TOKEN: _ClassVar[CredentialSource]
    CREDENTIAL_SOURCE_NONE: _ClassVar[CredentialSource]

class UsageGroupBy(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    USAGE_GROUP_BY_UNSPECIFIED: _ClassVar[UsageGroupBy]
    USAGE_GROUP_BY_ROWS: _ClassVar[UsageGroupBy]
    USAGE_GROUP_BY_AGGREGATE: _ClassVar[UsageGroupBy]

class SessionSortField(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    SESSION_SORT_FIELD_UNSPECIFIED: _ClassVar[SessionSortField]
    SESSION_SORT_FIELD_LAST_MESSAGE_AT: _ClassVar[SessionSortField]
    SESSION_SORT_FIELD_CREATED_AT: _ClassVar[SessionSortField]
    SESSION_SORT_FIELD_UPDATED_AT: _ClassVar[SessionSortField]
    SESSION_SORT_FIELD_TITLE: _ClassVar[SessionSortField]

class SessionSortOrder(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    SESSION_SORT_ORDER_UNSPECIFIED: _ClassVar[SessionSortOrder]
    SESSION_SORT_ORDER_ASC: _ClassVar[SessionSortOrder]
    SESSION_SORT_ORDER_DESC: _ClassVar[SessionSortOrder]

class ToolCallOrigin(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    TOOL_CALL_ORIGIN_UNSPECIFIED: _ClassVar[ToolCallOrigin]
    TOOL_CALL_ORIGIN_MODEL: _ClassVar[ToolCallOrigin]
    TOOL_CALL_ORIGIN_CLIENT: _ClassVar[ToolCallOrigin]
    TOOL_CALL_ORIGIN_SYSTEM: _ClassVar[ToolCallOrigin]

class LlmErrorKind(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    LLM_ERROR_KIND_UNSPECIFIED: _ClassVar[LlmErrorKind]
    LLM_ERROR_KIND_RATE_LIMITED: _ClassVar[LlmErrorKind]
    LLM_ERROR_KIND_AUTH: _ClassVar[LlmErrorKind]
    LLM_ERROR_KIND_PERMISSION_DENIED: _ClassVar[LlmErrorKind]
    LLM_ERROR_KIND_CONTEXT_LENGTH: _ClassVar[LlmErrorKind]
    LLM_ERROR_KIND_CONTENT_FILTER: _ClassVar[LlmErrorKind]
    LLM_ERROR_KIND_UNAVAILABLE: _ClassVar[LlmErrorKind]
    LLM_ERROR_KIND_TIMEOUT: _ClassVar[LlmErrorKind]
    LLM_ERROR_KIND_INVALID_REQUEST: _ClassVar[LlmErrorKind]
    LLM_ERROR_KIND_UNKNOWN: _ClassVar[LlmErrorKind]
ASK_CARD_TYPE_UNSPECIFIED: AskCardType
ASK_CARD_TYPE_CHOICE: AskCardType
ASK_CARD_TYPE_TEXT: AskCardType
ASK_CARD_TYPE_FILE: AskCardType
TEXT_TOOL_CALL_MODE_UNSPECIFIED: TextToolCallMode
TEXT_TOOL_CALL_MODE_STRIP: TextToolCallMode
TEXT_TOOL_CALL_MODE_CONVERT: TextToolCallMode
TOOL_CLASSIFICATION_UNSPECIFIED: ToolClassification
TOOL_CLASSIFICATION_QUERY: ToolClassification
TOOL_CLASSIFICATION_MUTATION: ToolClassification
TOOL_POLICY_UNSPECIFIED: ToolPolicy
TOOL_POLICY_INHERIT_SERVER_WIDE: ToolPolicy
TOOL_POLICY_PROFILE_ONLY: ToolPolicy
LLM_PROVIDER_TYPE_UNSPECIFIED: LlmProviderType
LLM_PROVIDER_TYPE_OPENAI: LlmProviderType
LLM_PROVIDER_TYPE_OLLAMA: LlmProviderType
LLM_PROVIDER_TYPE_ANTHROPIC: LlmProviderType
LLM_PROVIDER_TYPE_CURSOR_CLI: LlmProviderType
LLM_PROVIDER_TYPE_CLAUDE_CODE: LlmProviderType
LLM_PROVIDER_TYPE_BEDROCK: LlmProviderType
LLM_PROVIDER_TYPE_CLAUDE_CODE_V2: LlmProviderType
LLM_PROVIDER_TYPE_GEMINI: LlmProviderType
LLM_PROVIDER_TYPE_OPENROUTER: LlmProviderType
CURSOR_CLI_MODE_UNSPECIFIED: CursorCliMode
CURSOR_CLI_MODE_DEFAULT: CursorCliMode
CURSOR_CLI_MODE_ASK: CursorCliMode
CURSOR_CLI_MODE_PLAN: CursorCliMode
PROVIDER_CONNECTION_STATUS_UNSPECIFIED: ProviderConnectionStatus
PROVIDER_CONNECTION_STATUS_OK: ProviderConnectionStatus
PROVIDER_CONNECTION_STATUS_BINARY_NOT_FOUND: ProviderConnectionStatus
PROVIDER_CONNECTION_STATUS_VERSION_TOO_OLD: ProviderConnectionStatus
PROVIDER_CONNECTION_STATUS_NOT_AUTHENTICATED: ProviderConnectionStatus
PROVIDER_CONNECTION_STATUS_RESUME_UNSUPPORTED: ProviderConnectionStatus
PROVIDER_CONNECTION_STATUS_TIMED_OUT: ProviderConnectionStatus
PROVIDER_CONNECTION_STATUS_FAILED: ProviderConnectionStatus
CREDENTIAL_SOURCE_UNSPECIFIED: CredentialSource
CREDENTIAL_SOURCE_DEFAULT_CHAIN: CredentialSource
CREDENTIAL_SOURCE_ASSUMED_ROLE: CredentialSource
CREDENTIAL_SOURCE_WEB_IDENTITY: CredentialSource
CREDENTIAL_SOURCE_API_KEY: CredentialSource
CREDENTIAL_SOURCE_PROFILE: CredentialSource
CREDENTIAL_SOURCE_BEARER_TOKEN: CredentialSource
CREDENTIAL_SOURCE_NONE: CredentialSource
USAGE_GROUP_BY_UNSPECIFIED: UsageGroupBy
USAGE_GROUP_BY_ROWS: UsageGroupBy
USAGE_GROUP_BY_AGGREGATE: UsageGroupBy
SESSION_SORT_FIELD_UNSPECIFIED: SessionSortField
SESSION_SORT_FIELD_LAST_MESSAGE_AT: SessionSortField
SESSION_SORT_FIELD_CREATED_AT: SessionSortField
SESSION_SORT_FIELD_UPDATED_AT: SessionSortField
SESSION_SORT_FIELD_TITLE: SessionSortField
SESSION_SORT_ORDER_UNSPECIFIED: SessionSortOrder
SESSION_SORT_ORDER_ASC: SessionSortOrder
SESSION_SORT_ORDER_DESC: SessionSortOrder
TOOL_CALL_ORIGIN_UNSPECIFIED: ToolCallOrigin
TOOL_CALL_ORIGIN_MODEL: ToolCallOrigin
TOOL_CALL_ORIGIN_CLIENT: ToolCallOrigin
TOOL_CALL_ORIGIN_SYSTEM: ToolCallOrigin
LLM_ERROR_KIND_UNSPECIFIED: LlmErrorKind
LLM_ERROR_KIND_RATE_LIMITED: LlmErrorKind
LLM_ERROR_KIND_AUTH: LlmErrorKind
LLM_ERROR_KIND_PERMISSION_DENIED: LlmErrorKind
LLM_ERROR_KIND_CONTEXT_LENGTH: LlmErrorKind
LLM_ERROR_KIND_CONTENT_FILTER: LlmErrorKind
LLM_ERROR_KIND_UNAVAILABLE: LlmErrorKind
LLM_ERROR_KIND_TIMEOUT: LlmErrorKind
LLM_ERROR_KIND_INVALID_REQUEST: LlmErrorKind
LLM_ERROR_KIND_UNKNOWN: LlmErrorKind

class ToolCall(_message.Message):
    __slots__ = ("id", "name", "arguments_json", "thought_signature")
    ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    ARGUMENTS_JSON_FIELD_NUMBER: _ClassVar[int]
    THOUGHT_SIGNATURE_FIELD_NUMBER: _ClassVar[int]
    id: str
    name: str
    arguments_json: str
    thought_signature: bytes
    def __init__(self, id: _Optional[str] = ..., name: _Optional[str] = ..., arguments_json: _Optional[str] = ..., thought_signature: _Optional[bytes] = ...) -> None: ...

class MessageAttachment(_message.Message):
    __slots__ = ("id", "kind", "payload_json", "after_tool_call_id", "content")
    ID_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    PAYLOAD_JSON_FIELD_NUMBER: _ClassVar[int]
    AFTER_TOOL_CALL_ID_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    id: str
    kind: str
    payload_json: str
    after_tool_call_id: str
    content: str
    def __init__(self, id: _Optional[str] = ..., kind: _Optional[str] = ..., payload_json: _Optional[str] = ..., after_tool_call_id: _Optional[str] = ..., content: _Optional[str] = ...) -> None: ...

class AskChoiceCard(_message.Message):
    __slots__ = ("choices", "allow_multiple", "options")
    CHOICES_FIELD_NUMBER: _ClassVar[int]
    ALLOW_MULTIPLE_FIELD_NUMBER: _ClassVar[int]
    OPTIONS_FIELD_NUMBER: _ClassVar[int]
    choices: _containers.RepeatedScalarFieldContainer[str]
    allow_multiple: bool
    options: _containers.RepeatedCompositeFieldContainer[AskChoiceOption]
    def __init__(self, choices: _Optional[_Iterable[str]] = ..., allow_multiple: _Optional[bool] = ..., options: _Optional[_Iterable[_Union[AskChoiceOption, _Mapping]]] = ...) -> None: ...

class AskChoiceOption(_message.Message):
    __slots__ = ("label", "description", "recommendation")
    LABEL_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    RECOMMENDATION_FIELD_NUMBER: _ClassVar[int]
    label: str
    description: str
    recommendation: str
    def __init__(self, label: _Optional[str] = ..., description: _Optional[str] = ..., recommendation: _Optional[str] = ...) -> None: ...

class AskFileCard(_message.Message):
    __slots__ = ("accepted_mime_types",)
    ACCEPTED_MIME_TYPES_FIELD_NUMBER: _ClassVar[int]
    accepted_mime_types: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, accepted_mime_types: _Optional[_Iterable[str]] = ...) -> None: ...

class AskCard(_message.Message):
    __slots__ = ("id", "title", "text", "type", "allow_additional_text", "choice", "file")
    ID_FIELD_NUMBER: _ClassVar[int]
    TITLE_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    ALLOW_ADDITIONAL_TEXT_FIELD_NUMBER: _ClassVar[int]
    CHOICE_FIELD_NUMBER: _ClassVar[int]
    FILE_FIELD_NUMBER: _ClassVar[int]
    id: str
    title: str
    text: str
    type: AskCardType
    allow_additional_text: bool
    choice: AskChoiceCard
    file: AskFileCard
    def __init__(self, id: _Optional[str] = ..., title: _Optional[str] = ..., text: _Optional[str] = ..., type: _Optional[_Union[AskCardType, str]] = ..., allow_additional_text: _Optional[bool] = ..., choice: _Optional[_Union[AskChoiceCard, _Mapping]] = ..., file: _Optional[_Union[AskFileCard, _Mapping]] = ...) -> None: ...

class AskChoiceResponse(_message.Message):
    __slots__ = ("selected",)
    SELECTED_FIELD_NUMBER: _ClassVar[int]
    selected: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, selected: _Optional[_Iterable[str]] = ...) -> None: ...

class AskFileResponse(_message.Message):
    __slots__ = ("file_name", "mime_type", "size_bytes", "content")
    FILE_NAME_FIELD_NUMBER: _ClassVar[int]
    MIME_TYPE_FIELD_NUMBER: _ClassVar[int]
    SIZE_BYTES_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    file_name: str
    mime_type: str
    size_bytes: int
    content: bytes
    def __init__(self, file_name: _Optional[str] = ..., mime_type: _Optional[str] = ..., size_bytes: _Optional[int] = ..., content: _Optional[bytes] = ...) -> None: ...

class AskCardResponse(_message.Message):
    __slots__ = ("card_id", "type", "additional_text", "choice", "file")
    CARD_ID_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    ADDITIONAL_TEXT_FIELD_NUMBER: _ClassVar[int]
    CHOICE_FIELD_NUMBER: _ClassVar[int]
    FILE_FIELD_NUMBER: _ClassVar[int]
    card_id: str
    type: AskCardType
    additional_text: str
    choice: AskChoiceResponse
    file: AskFileResponse
    def __init__(self, card_id: _Optional[str] = ..., type: _Optional[_Union[AskCardType, str]] = ..., additional_text: _Optional[str] = ..., choice: _Optional[_Union[AskChoiceResponse, _Mapping]] = ..., file: _Optional[_Union[AskFileResponse, _Mapping]] = ...) -> None: ...

class TrackedDocument(_message.Message):
    __slots__ = ("document_id", "name", "content")
    DOCUMENT_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    document_id: str
    name: str
    content: str
    def __init__(self, document_id: _Optional[str] = ..., name: _Optional[str] = ..., content: _Optional[str] = ...) -> None: ...

class Message(_message.Message):
    __slots__ = ("role", "content", "id", "tool_calls", "tool_call_id", "thinking_content", "activity_headline", "attachments", "usage", "finish_reason", "model_duration_ms")
    ROLE_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALLS_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_ID_FIELD_NUMBER: _ClassVar[int]
    THINKING_CONTENT_FIELD_NUMBER: _ClassVar[int]
    ACTIVITY_HEADLINE_FIELD_NUMBER: _ClassVar[int]
    ATTACHMENTS_FIELD_NUMBER: _ClassVar[int]
    USAGE_FIELD_NUMBER: _ClassVar[int]
    FINISH_REASON_FIELD_NUMBER: _ClassVar[int]
    MODEL_DURATION_MS_FIELD_NUMBER: _ClassVar[int]
    role: str
    content: str
    id: str
    tool_calls: _containers.RepeatedCompositeFieldContainer[ToolCall]
    tool_call_id: str
    thinking_content: str
    activity_headline: str
    attachments: _containers.RepeatedCompositeFieldContainer[MessageAttachment]
    usage: TokenUsage
    finish_reason: str
    model_duration_ms: int
    def __init__(self, role: _Optional[str] = ..., content: _Optional[str] = ..., id: _Optional[str] = ..., tool_calls: _Optional[_Iterable[_Union[ToolCall, _Mapping]]] = ..., tool_call_id: _Optional[str] = ..., thinking_content: _Optional[str] = ..., activity_headline: _Optional[str] = ..., attachments: _Optional[_Iterable[_Union[MessageAttachment, _Mapping]]] = ..., usage: _Optional[_Union[TokenUsage, _Mapping]] = ..., finish_reason: _Optional[str] = ..., model_duration_ms: _Optional[int] = ...) -> None: ...

class CacheOptions(_message.Message):
    __slots__ = ("prompt_cache_mode",)
    class PromptCacheMode(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
        __slots__ = ()
        PROMPT_CACHE_MODE_UNSPECIFIED: _ClassVar[CacheOptions.PromptCacheMode]
        PROMPT_CACHE_MODE_DISABLED: _ClassVar[CacheOptions.PromptCacheMode]
        PROMPT_CACHE_MODE_SHORT_TTL: _ClassVar[CacheOptions.PromptCacheMode]
        PROMPT_CACHE_MODE_LONG_TTL: _ClassVar[CacheOptions.PromptCacheMode]
    PROMPT_CACHE_MODE_UNSPECIFIED: CacheOptions.PromptCacheMode
    PROMPT_CACHE_MODE_DISABLED: CacheOptions.PromptCacheMode
    PROMPT_CACHE_MODE_SHORT_TTL: CacheOptions.PromptCacheMode
    PROMPT_CACHE_MODE_LONG_TTL: CacheOptions.PromptCacheMode
    PROMPT_CACHE_MODE_FIELD_NUMBER: _ClassVar[int]
    prompt_cache_mode: CacheOptions.PromptCacheMode
    def __init__(self, prompt_cache_mode: _Optional[_Union[CacheOptions.PromptCacheMode, str]] = ...) -> None: ...

class ClaudeCodeCallOptions(_message.Message):
    __slots__ = ("effort", "max_budget_usd", "base_model", "thinking_type", "thinking_budget_tokens")
    EFFORT_FIELD_NUMBER: _ClassVar[int]
    MAX_BUDGET_USD_FIELD_NUMBER: _ClassVar[int]
    BASE_MODEL_FIELD_NUMBER: _ClassVar[int]
    THINKING_TYPE_FIELD_NUMBER: _ClassVar[int]
    THINKING_BUDGET_TOKENS_FIELD_NUMBER: _ClassVar[int]
    effort: str
    max_budget_usd: float
    base_model: str
    thinking_type: str
    thinking_budget_tokens: int
    def __init__(self, effort: _Optional[str] = ..., max_budget_usd: _Optional[float] = ..., base_model: _Optional[str] = ..., thinking_type: _Optional[str] = ..., thinking_budget_tokens: _Optional[int] = ...) -> None: ...

class OpenRouterCallOptions(_message.Message):
    __slots__ = ("sort", "order", "only", "ignore", "allow_fallbacks")
    class Sort(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
        __slots__ = ()
        SORT_UNSPECIFIED: _ClassVar[OpenRouterCallOptions.Sort]
        SORT_PRICE: _ClassVar[OpenRouterCallOptions.Sort]
        SORT_THROUGHPUT: _ClassVar[OpenRouterCallOptions.Sort]
        SORT_LATENCY: _ClassVar[OpenRouterCallOptions.Sort]
    SORT_UNSPECIFIED: OpenRouterCallOptions.Sort
    SORT_PRICE: OpenRouterCallOptions.Sort
    SORT_THROUGHPUT: OpenRouterCallOptions.Sort
    SORT_LATENCY: OpenRouterCallOptions.Sort
    SORT_FIELD_NUMBER: _ClassVar[int]
    ORDER_FIELD_NUMBER: _ClassVar[int]
    ONLY_FIELD_NUMBER: _ClassVar[int]
    IGNORE_FIELD_NUMBER: _ClassVar[int]
    ALLOW_FALLBACKS_FIELD_NUMBER: _ClassVar[int]
    sort: OpenRouterCallOptions.Sort
    order: _containers.RepeatedScalarFieldContainer[str]
    only: _containers.RepeatedScalarFieldContainer[str]
    ignore: _containers.RepeatedScalarFieldContainer[str]
    allow_fallbacks: bool
    def __init__(self, sort: _Optional[_Union[OpenRouterCallOptions.Sort, str]] = ..., order: _Optional[_Iterable[str]] = ..., only: _Optional[_Iterable[str]] = ..., ignore: _Optional[_Iterable[str]] = ..., allow_fallbacks: _Optional[bool] = ...) -> None: ...

class ResponseFormat(_message.Message):
    __slots__ = ("kind", "json_schema", "schema_name", "strict")
    class Kind(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
        __slots__ = ()
        RESPONSE_FORMAT_KIND_UNSPECIFIED: _ClassVar[ResponseFormat.Kind]
        RESPONSE_FORMAT_KIND_JSON_OBJECT: _ClassVar[ResponseFormat.Kind]
        RESPONSE_FORMAT_KIND_JSON_SCHEMA: _ClassVar[ResponseFormat.Kind]
    RESPONSE_FORMAT_KIND_UNSPECIFIED: ResponseFormat.Kind
    RESPONSE_FORMAT_KIND_JSON_OBJECT: ResponseFormat.Kind
    RESPONSE_FORMAT_KIND_JSON_SCHEMA: ResponseFormat.Kind
    KIND_FIELD_NUMBER: _ClassVar[int]
    JSON_SCHEMA_FIELD_NUMBER: _ClassVar[int]
    SCHEMA_NAME_FIELD_NUMBER: _ClassVar[int]
    STRICT_FIELD_NUMBER: _ClassVar[int]
    kind: ResponseFormat.Kind
    json_schema: str
    schema_name: str
    strict: bool
    def __init__(self, kind: _Optional[_Union[ResponseFormat.Kind, str]] = ..., json_schema: _Optional[str] = ..., schema_name: _Optional[str] = ..., strict: _Optional[bool] = ...) -> None: ...

class CallOptions(_message.Message):
    __slots__ = ("model", "max_tokens", "temperature", "top_p", "top_k", "cache_options", "max_tool_rounds", "read_only", "claude_code", "response_format", "provider_id", "tool_choice", "openrouter", "attempt_timeout_ms", "max_attempts", "final_round", "text_tool_call_mode")
    MODEL_FIELD_NUMBER: _ClassVar[int]
    MAX_TOKENS_FIELD_NUMBER: _ClassVar[int]
    TEMPERATURE_FIELD_NUMBER: _ClassVar[int]
    TOP_P_FIELD_NUMBER: _ClassVar[int]
    TOP_K_FIELD_NUMBER: _ClassVar[int]
    CACHE_OPTIONS_FIELD_NUMBER: _ClassVar[int]
    MAX_TOOL_ROUNDS_FIELD_NUMBER: _ClassVar[int]
    READ_ONLY_FIELD_NUMBER: _ClassVar[int]
    CLAUDE_CODE_FIELD_NUMBER: _ClassVar[int]
    RESPONSE_FORMAT_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    TOOL_CHOICE_FIELD_NUMBER: _ClassVar[int]
    OPENROUTER_FIELD_NUMBER: _ClassVar[int]
    ATTEMPT_TIMEOUT_MS_FIELD_NUMBER: _ClassVar[int]
    MAX_ATTEMPTS_FIELD_NUMBER: _ClassVar[int]
    FINAL_ROUND_FIELD_NUMBER: _ClassVar[int]
    TEXT_TOOL_CALL_MODE_FIELD_NUMBER: _ClassVar[int]
    model: str
    max_tokens: int
    temperature: float
    top_p: float
    top_k: int
    cache_options: CacheOptions
    max_tool_rounds: int
    read_only: bool
    claude_code: ClaudeCodeCallOptions
    response_format: ResponseFormat
    provider_id: str
    tool_choice: ToolChoice
    openrouter: OpenRouterCallOptions
    attempt_timeout_ms: int
    max_attempts: int
    final_round: FinalRoundOptions
    text_tool_call_mode: TextToolCallMode
    def __init__(self, model: _Optional[str] = ..., max_tokens: _Optional[int] = ..., temperature: _Optional[float] = ..., top_p: _Optional[float] = ..., top_k: _Optional[int] = ..., cache_options: _Optional[_Union[CacheOptions, _Mapping]] = ..., max_tool_rounds: _Optional[int] = ..., read_only: _Optional[bool] = ..., claude_code: _Optional[_Union[ClaudeCodeCallOptions, _Mapping]] = ..., response_format: _Optional[_Union[ResponseFormat, _Mapping]] = ..., provider_id: _Optional[str] = ..., tool_choice: _Optional[_Union[ToolChoice, _Mapping]] = ..., openrouter: _Optional[_Union[OpenRouterCallOptions, _Mapping]] = ..., attempt_timeout_ms: _Optional[int] = ..., max_attempts: _Optional[int] = ..., final_round: _Optional[_Union[FinalRoundOptions, _Mapping]] = ..., text_tool_call_mode: _Optional[_Union[TextToolCallMode, str]] = ...) -> None: ...

class FinalRoundOptions(_message.Message):
    __slots__ = ("enabled", "instruction")
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    INSTRUCTION_FIELD_NUMBER: _ClassVar[int]
    enabled: bool
    instruction: str
    def __init__(self, enabled: _Optional[bool] = ..., instruction: _Optional[str] = ...) -> None: ...

class ToolChoice(_message.Message):
    __slots__ = ("mode", "tool_name")
    class Mode(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
        __slots__ = ()
        MODE_UNSPECIFIED: _ClassVar[ToolChoice.Mode]
        MODE_AUTO: _ClassVar[ToolChoice.Mode]
        MODE_NONE: _ClassVar[ToolChoice.Mode]
        MODE_REQUIRED: _ClassVar[ToolChoice.Mode]
        MODE_NAMED: _ClassVar[ToolChoice.Mode]
    MODE_UNSPECIFIED: ToolChoice.Mode
    MODE_AUTO: ToolChoice.Mode
    MODE_NONE: ToolChoice.Mode
    MODE_REQUIRED: ToolChoice.Mode
    MODE_NAMED: ToolChoice.Mode
    MODE_FIELD_NUMBER: _ClassVar[int]
    TOOL_NAME_FIELD_NUMBER: _ClassVar[int]
    mode: ToolChoice.Mode
    tool_name: str
    def __init__(self, mode: _Optional[_Union[ToolChoice.Mode, str]] = ..., tool_name: _Optional[str] = ...) -> None: ...

class GenerateOnceRequest(_message.Message):
    __slots__ = ("system_prompt", "prompt", "options", "provider_id", "prompt_parts")
    SYSTEM_PROMPT_FIELD_NUMBER: _ClassVar[int]
    PROMPT_FIELD_NUMBER: _ClassVar[int]
    OPTIONS_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    PROMPT_PARTS_FIELD_NUMBER: _ClassVar[int]
    system_prompt: str
    prompt: str
    options: CallOptions
    provider_id: str
    prompt_parts: _containers.RepeatedCompositeFieldContainer[PromptPart]
    def __init__(self, system_prompt: _Optional[str] = ..., prompt: _Optional[str] = ..., options: _Optional[_Union[CallOptions, _Mapping]] = ..., provider_id: _Optional[str] = ..., prompt_parts: _Optional[_Iterable[_Union[PromptPart, _Mapping]]] = ...) -> None: ...

class PromptPart(_message.Message):
    __slots__ = ("text", "cache_breakpoint")
    TEXT_FIELD_NUMBER: _ClassVar[int]
    CACHE_BREAKPOINT_FIELD_NUMBER: _ClassVar[int]
    text: str
    cache_breakpoint: bool
    def __init__(self, text: _Optional[str] = ..., cache_breakpoint: _Optional[bool] = ...) -> None: ...

class CompleteRequest(_message.Message):
    __slots__ = ("messages", "options", "provider_id", "tools", "system")
    MESSAGES_FIELD_NUMBER: _ClassVar[int]
    OPTIONS_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    TOOLS_FIELD_NUMBER: _ClassVar[int]
    SYSTEM_FIELD_NUMBER: _ClassVar[int]
    messages: _containers.RepeatedCompositeFieldContainer[Message]
    options: CallOptions
    provider_id: str
    tools: _containers.RepeatedCompositeFieldContainer[ToolDefinition]
    system: str
    def __init__(self, messages: _Optional[_Iterable[_Union[Message, _Mapping]]] = ..., options: _Optional[_Union[CallOptions, _Mapping]] = ..., provider_id: _Optional[str] = ..., tools: _Optional[_Iterable[_Union[ToolDefinition, _Mapping]]] = ..., system: _Optional[str] = ...) -> None: ...

class CompleteResponse(_message.Message):
    __slots__ = ("reply",)
    REPLY_FIELD_NUMBER: _ClassVar[int]
    reply: AssistantReply
    def __init__(self, reply: _Optional[_Union[AssistantReply, _Mapping]] = ...) -> None: ...

class CompleteStreamResponse(_message.Message):
    __slots__ = ("content_delta", "thinking_delta", "tool_call", "final_reply")
    CONTENT_DELTA_FIELD_NUMBER: _ClassVar[int]
    THINKING_DELTA_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_FIELD_NUMBER: _ClassVar[int]
    FINAL_REPLY_FIELD_NUMBER: _ClassVar[int]
    content_delta: str
    thinking_delta: str
    tool_call: ToolCall
    final_reply: AssistantReply
    def __init__(self, content_delta: _Optional[str] = ..., thinking_delta: _Optional[str] = ..., tool_call: _Optional[_Union[ToolCall, _Mapping]] = ..., final_reply: _Optional[_Union[AssistantReply, _Mapping]] = ...) -> None: ...

class AssistantReply(_message.Message):
    __slots__ = ("content", "id", "tool_calls", "finish_reason", "thinking_content", "activity_headline", "usage", "served_by")
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALLS_FIELD_NUMBER: _ClassVar[int]
    FINISH_REASON_FIELD_NUMBER: _ClassVar[int]
    THINKING_CONTENT_FIELD_NUMBER: _ClassVar[int]
    ACTIVITY_HEADLINE_FIELD_NUMBER: _ClassVar[int]
    USAGE_FIELD_NUMBER: _ClassVar[int]
    SERVED_BY_FIELD_NUMBER: _ClassVar[int]
    content: str
    id: str
    tool_calls: _containers.RepeatedCompositeFieldContainer[ToolCall]
    finish_reason: str
    thinking_content: str
    activity_headline: str
    usage: TokenUsage
    served_by: str
    def __init__(self, content: _Optional[str] = ..., id: _Optional[str] = ..., tool_calls: _Optional[_Iterable[_Union[ToolCall, _Mapping]]] = ..., finish_reason: _Optional[str] = ..., thinking_content: _Optional[str] = ..., activity_headline: _Optional[str] = ..., usage: _Optional[_Union[TokenUsage, _Mapping]] = ..., served_by: _Optional[str] = ...) -> None: ...

class ToolExecutionInlineBuiltin(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ToolExecutionHttpWebhook(_message.Message):
    __slots__ = ("url", "timeout_seconds", "bearer_token", "hmac_secret")
    URL_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_SECONDS_FIELD_NUMBER: _ClassVar[int]
    BEARER_TOKEN_FIELD_NUMBER: _ClassVar[int]
    HMAC_SECRET_FIELD_NUMBER: _ClassVar[int]
    url: str
    timeout_seconds: int
    bearer_token: str
    hmac_secret: str
    def __init__(self, url: _Optional[str] = ..., timeout_seconds: _Optional[int] = ..., bearer_token: _Optional[str] = ..., hmac_secret: _Optional[str] = ...) -> None: ...

class ToolExecutionDelegateToClient(_message.Message):
    __slots__ = ("timeout_seconds",)
    TIMEOUT_SECONDS_FIELD_NUMBER: _ClassVar[int]
    timeout_seconds: int
    def __init__(self, timeout_seconds: _Optional[int] = ...) -> None: ...

class ToolDefinition(_message.Message):
    __slots__ = ("name", "description", "parameters_schema_json", "inline_builtin", "http_webhook", "delegate_to_client", "executor", "classification", "priority")
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    PARAMETERS_SCHEMA_JSON_FIELD_NUMBER: _ClassVar[int]
    INLINE_BUILTIN_FIELD_NUMBER: _ClassVar[int]
    HTTP_WEBHOOK_FIELD_NUMBER: _ClassVar[int]
    DELEGATE_TO_CLIENT_FIELD_NUMBER: _ClassVar[int]
    EXECUTOR_FIELD_NUMBER: _ClassVar[int]
    CLASSIFICATION_FIELD_NUMBER: _ClassVar[int]
    PRIORITY_FIELD_NUMBER: _ClassVar[int]
    name: str
    description: str
    parameters_schema_json: str
    inline_builtin: ToolExecutionInlineBuiltin
    http_webhook: ToolExecutionHttpWebhook
    delegate_to_client: ToolExecutionDelegateToClient
    executor: ToolExecutionExecutor
    classification: ToolClassification
    priority: int
    def __init__(self, name: _Optional[str] = ..., description: _Optional[str] = ..., parameters_schema_json: _Optional[str] = ..., inline_builtin: _Optional[_Union[ToolExecutionInlineBuiltin, _Mapping]] = ..., http_webhook: _Optional[_Union[ToolExecutionHttpWebhook, _Mapping]] = ..., delegate_to_client: _Optional[_Union[ToolExecutionDelegateToClient, _Mapping]] = ..., executor: _Optional[_Union[ToolExecutionExecutor, _Mapping]] = ..., classification: _Optional[_Union[ToolClassification, str]] = ..., priority: _Optional[int] = ...) -> None: ...

class ToolExecutionExecutor(_message.Message):
    __slots__ = ("executor_id", "max_duration_seconds")
    EXECUTOR_ID_FIELD_NUMBER: _ClassVar[int]
    MAX_DURATION_SECONDS_FIELD_NUMBER: _ClassVar[int]
    executor_id: str
    max_duration_seconds: int
    def __init__(self, executor_id: _Optional[str] = ..., max_duration_seconds: _Optional[int] = ...) -> None: ...

class EnsureSessionRequest(_message.Message):
    __slots__ = ("session_id", "display_name", "initial_tools", "llm_provider_id", "default_model", "system_prompt_sections", "chat_surface", "disabled_tools", "tool_policy", "replace_tools", "tool_context")
    class ToolContextEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    INITIAL_TOOLS_FIELD_NUMBER: _ClassVar[int]
    LLM_PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_MODEL_FIELD_NUMBER: _ClassVar[int]
    SYSTEM_PROMPT_SECTIONS_FIELD_NUMBER: _ClassVar[int]
    CHAT_SURFACE_FIELD_NUMBER: _ClassVar[int]
    DISABLED_TOOLS_FIELD_NUMBER: _ClassVar[int]
    TOOL_POLICY_FIELD_NUMBER: _ClassVar[int]
    REPLACE_TOOLS_FIELD_NUMBER: _ClassVar[int]
    TOOL_CONTEXT_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    display_name: str
    initial_tools: _containers.RepeatedCompositeFieldContainer[ToolDefinition]
    llm_provider_id: str
    default_model: str
    system_prompt_sections: _containers.RepeatedCompositeFieldContainer[SystemPromptSection]
    chat_surface: str
    disabled_tools: _containers.RepeatedScalarFieldContainer[str]
    tool_policy: ToolPolicy
    replace_tools: bool
    tool_context: _containers.ScalarMap[str, str]
    def __init__(self, session_id: _Optional[str] = ..., display_name: _Optional[str] = ..., initial_tools: _Optional[_Iterable[_Union[ToolDefinition, _Mapping]]] = ..., llm_provider_id: _Optional[str] = ..., default_model: _Optional[str] = ..., system_prompt_sections: _Optional[_Iterable[_Union[SystemPromptSection, _Mapping]]] = ..., chat_surface: _Optional[str] = ..., disabled_tools: _Optional[_Iterable[str]] = ..., tool_policy: _Optional[_Union[ToolPolicy, str]] = ..., replace_tools: _Optional[bool] = ..., tool_context: _Optional[_Mapping[str, str]] = ...) -> None: ...

class EnsureSessionResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ToolMapping(_message.Message):
    __slots__ = ("tool_name", "enabled")
    TOOL_NAME_FIELD_NUMBER: _ClassVar[int]
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    tool_name: str
    enabled: bool
    def __init__(self, tool_name: _Optional[str] = ..., enabled: _Optional[bool] = ...) -> None: ...

class AgentProfile(_message.Message):
    __slots__ = ("id", "name", "description", "system_prompt_sections", "llm_provider_id", "default_model", "memory_enabled", "tool_mapping", "created_at", "updated_at", "is_static", "tool_policy")
    ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    SYSTEM_PROMPT_SECTIONS_FIELD_NUMBER: _ClassVar[int]
    LLM_PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_MODEL_FIELD_NUMBER: _ClassVar[int]
    MEMORY_ENABLED_FIELD_NUMBER: _ClassVar[int]
    TOOL_MAPPING_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    IS_STATIC_FIELD_NUMBER: _ClassVar[int]
    TOOL_POLICY_FIELD_NUMBER: _ClassVar[int]
    id: str
    name: str
    description: str
    system_prompt_sections: _containers.RepeatedCompositeFieldContainer[SystemPromptSection]
    llm_provider_id: str
    default_model: str
    memory_enabled: bool
    tool_mapping: _containers.RepeatedCompositeFieldContainer[ToolMapping]
    created_at: str
    updated_at: str
    is_static: bool
    tool_policy: ToolPolicy
    def __init__(self, id: _Optional[str] = ..., name: _Optional[str] = ..., description: _Optional[str] = ..., system_prompt_sections: _Optional[_Iterable[_Union[SystemPromptSection, _Mapping]]] = ..., llm_provider_id: _Optional[str] = ..., default_model: _Optional[str] = ..., memory_enabled: _Optional[bool] = ..., tool_mapping: _Optional[_Iterable[_Union[ToolMapping, _Mapping]]] = ..., created_at: _Optional[str] = ..., updated_at: _Optional[str] = ..., is_static: _Optional[bool] = ..., tool_policy: _Optional[_Union[ToolPolicy, str]] = ...) -> None: ...

class CreateAgentProfileRequest(_message.Message):
    __slots__ = ("name", "description", "system_prompt_sections", "llm_provider_id", "default_model", "memory_enabled", "tool_mapping", "id", "tool_policy")
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    SYSTEM_PROMPT_SECTIONS_FIELD_NUMBER: _ClassVar[int]
    LLM_PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_MODEL_FIELD_NUMBER: _ClassVar[int]
    MEMORY_ENABLED_FIELD_NUMBER: _ClassVar[int]
    TOOL_MAPPING_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    TOOL_POLICY_FIELD_NUMBER: _ClassVar[int]
    name: str
    description: str
    system_prompt_sections: _containers.RepeatedCompositeFieldContainer[SystemPromptSection]
    llm_provider_id: str
    default_model: str
    memory_enabled: bool
    tool_mapping: _containers.RepeatedCompositeFieldContainer[ToolMapping]
    id: str
    tool_policy: ToolPolicy
    def __init__(self, name: _Optional[str] = ..., description: _Optional[str] = ..., system_prompt_sections: _Optional[_Iterable[_Union[SystemPromptSection, _Mapping]]] = ..., llm_provider_id: _Optional[str] = ..., default_model: _Optional[str] = ..., memory_enabled: _Optional[bool] = ..., tool_mapping: _Optional[_Iterable[_Union[ToolMapping, _Mapping]]] = ..., id: _Optional[str] = ..., tool_policy: _Optional[_Union[ToolPolicy, str]] = ...) -> None: ...

class CreateAgentProfileResponse(_message.Message):
    __slots__ = ("profile",)
    PROFILE_FIELD_NUMBER: _ClassVar[int]
    profile: AgentProfile
    def __init__(self, profile: _Optional[_Union[AgentProfile, _Mapping]] = ...) -> None: ...

class UpdateAgentProfileRequest(_message.Message):
    __slots__ = ("id", "name", "description", "system_prompt_sections", "llm_provider_id", "default_model", "memory_enabled", "tool_mapping", "tool_policy")
    ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    SYSTEM_PROMPT_SECTIONS_FIELD_NUMBER: _ClassVar[int]
    LLM_PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_MODEL_FIELD_NUMBER: _ClassVar[int]
    MEMORY_ENABLED_FIELD_NUMBER: _ClassVar[int]
    TOOL_MAPPING_FIELD_NUMBER: _ClassVar[int]
    TOOL_POLICY_FIELD_NUMBER: _ClassVar[int]
    id: str
    name: str
    description: str
    system_prompt_sections: _containers.RepeatedCompositeFieldContainer[SystemPromptSection]
    llm_provider_id: str
    default_model: str
    memory_enabled: bool
    tool_mapping: _containers.RepeatedCompositeFieldContainer[ToolMapping]
    tool_policy: ToolPolicy
    def __init__(self, id: _Optional[str] = ..., name: _Optional[str] = ..., description: _Optional[str] = ..., system_prompt_sections: _Optional[_Iterable[_Union[SystemPromptSection, _Mapping]]] = ..., llm_provider_id: _Optional[str] = ..., default_model: _Optional[str] = ..., memory_enabled: _Optional[bool] = ..., tool_mapping: _Optional[_Iterable[_Union[ToolMapping, _Mapping]]] = ..., tool_policy: _Optional[_Union[ToolPolicy, str]] = ...) -> None: ...

class UpdateAgentProfileResponse(_message.Message):
    __slots__ = ("profile",)
    PROFILE_FIELD_NUMBER: _ClassVar[int]
    profile: AgentProfile
    def __init__(self, profile: _Optional[_Union[AgentProfile, _Mapping]] = ...) -> None: ...

class DeleteAgentProfileRequest(_message.Message):
    __slots__ = ("id",)
    ID_FIELD_NUMBER: _ClassVar[int]
    id: str
    def __init__(self, id: _Optional[str] = ...) -> None: ...

class DeleteAgentProfileResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class GetAgentProfileRequest(_message.Message):
    __slots__ = ("id",)
    ID_FIELD_NUMBER: _ClassVar[int]
    id: str
    def __init__(self, id: _Optional[str] = ...) -> None: ...

class GetAgentProfileResponse(_message.Message):
    __slots__ = ("profile",)
    PROFILE_FIELD_NUMBER: _ClassVar[int]
    profile: AgentProfile
    def __init__(self, profile: _Optional[_Union[AgentProfile, _Mapping]] = ...) -> None: ...

class ListAgentProfilesRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListAgentProfilesResponse(_message.Message):
    __slots__ = ("profiles",)
    PROFILES_FIELD_NUMBER: _ClassVar[int]
    profiles: _containers.RepeatedCompositeFieldContainer[AgentProfile]
    def __init__(self, profiles: _Optional[_Iterable[_Union[AgentProfile, _Mapping]]] = ...) -> None: ...

class CreateSessionFromAgentProfileRequest(_message.Message):
    __slots__ = ("agent_profile_id", "session_id", "display_name", "llm_provider_id_override", "default_model_override", "additional_tools", "chat_surface")
    AGENT_PROFILE_ID_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    LLM_PROVIDER_ID_OVERRIDE_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_MODEL_OVERRIDE_FIELD_NUMBER: _ClassVar[int]
    ADDITIONAL_TOOLS_FIELD_NUMBER: _ClassVar[int]
    CHAT_SURFACE_FIELD_NUMBER: _ClassVar[int]
    agent_profile_id: str
    session_id: str
    display_name: str
    llm_provider_id_override: str
    default_model_override: str
    additional_tools: _containers.RepeatedCompositeFieldContainer[ToolDefinition]
    chat_surface: str
    def __init__(self, agent_profile_id: _Optional[str] = ..., session_id: _Optional[str] = ..., display_name: _Optional[str] = ..., llm_provider_id_override: _Optional[str] = ..., default_model_override: _Optional[str] = ..., additional_tools: _Optional[_Iterable[_Union[ToolDefinition, _Mapping]]] = ..., chat_surface: _Optional[str] = ...) -> None: ...

class CreateSessionFromAgentProfileResponse(_message.Message):
    __slots__ = ("session_id", "agent_profile_id")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    AGENT_PROFILE_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    agent_profile_id: str
    def __init__(self, session_id: _Optional[str] = ..., agent_profile_id: _Optional[str] = ...) -> None: ...

class UpdateSessionLlmRequest(_message.Message):
    __slots__ = ("session_id", "llm_provider_id", "default_model")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    LLM_PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_MODEL_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    llm_provider_id: str
    default_model: str
    def __init__(self, session_id: _Optional[str] = ..., llm_provider_id: _Optional[str] = ..., default_model: _Optional[str] = ...) -> None: ...

class UpdateSessionLlmResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class DestroySessionRequest(_message.Message):
    __slots__ = ("session_id",)
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    def __init__(self, session_id: _Optional[str] = ...) -> None: ...

class DestroySessionResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class RegisterSessionToolRequest(_message.Message):
    __slots__ = ("session_id", "tool")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    TOOL_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    tool: ToolDefinition
    def __init__(self, session_id: _Optional[str] = ..., tool: _Optional[_Union[ToolDefinition, _Mapping]] = ...) -> None: ...

class RegisterSessionToolResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class UnregisterSessionToolRequest(_message.Message):
    __slots__ = ("session_id", "name")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    name: str
    def __init__(self, session_id: _Optional[str] = ..., name: _Optional[str] = ...) -> None: ...

class UnregisterSessionToolResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListSessionToolsRequest(_message.Message):
    __slots__ = ("session_id",)
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    def __init__(self, session_id: _Optional[str] = ...) -> None: ...

class ListSessionToolsResponse(_message.Message):
    __slots__ = ("tools",)
    TOOLS_FIELD_NUMBER: _ClassVar[int]
    tools: _containers.RepeatedCompositeFieldContainer[ToolDefinition]
    def __init__(self, tools: _Optional[_Iterable[_Union[ToolDefinition, _Mapping]]] = ...) -> None: ...

class ListToolsRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListToolsResponse(_message.Message):
    __slots__ = ("tools",)
    TOOLS_FIELD_NUMBER: _ClassVar[int]
    tools: _containers.RepeatedCompositeFieldContainer[ToolDefinition]
    def __init__(self, tools: _Optional[_Iterable[_Union[ToolDefinition, _Mapping]]] = ...) -> None: ...

class GetSessionHistoryRequest(_message.Message):
    __slots__ = ("session_id", "max_messages", "status_only")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    MAX_MESSAGES_FIELD_NUMBER: _ClassVar[int]
    STATUS_ONLY_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    max_messages: int
    status_only: bool
    def __init__(self, session_id: _Optional[str] = ..., max_messages: _Optional[int] = ..., status_only: _Optional[bool] = ...) -> None: ...

class SessionAttachmentRow(_message.Message):
    __slots__ = ("content", "tool_call_id")
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_ID_FIELD_NUMBER: _ClassVar[int]
    content: str
    tool_call_id: str
    def __init__(self, content: _Optional[str] = ..., tool_call_id: _Optional[str] = ...) -> None: ...

class TrackedDocumentRef(_message.Message):
    __slots__ = ("document_id", "name", "version", "anchor_message_id", "change", "content")
    DOCUMENT_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    ANCHOR_MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    CHANGE_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    document_id: str
    name: str
    version: int
    anchor_message_id: str
    change: str
    content: str
    def __init__(self, document_id: _Optional[str] = ..., name: _Optional[str] = ..., version: _Optional[int] = ..., anchor_message_id: _Optional[str] = ..., change: _Optional[str] = ..., content: _Optional[str] = ...) -> None: ...

class GetSessionHistoryResponse(_message.Message):
    __slots__ = ("messages", "attachment_rows", "tracked_documents", "initial_seed", "turn_in_progress", "events_seq", "turn_start_seq", "status_only", "turn_started_at_ms", "last_turn_started_at_ms", "last_turn_ended_at_ms")
    MESSAGES_FIELD_NUMBER: _ClassVar[int]
    ATTACHMENT_ROWS_FIELD_NUMBER: _ClassVar[int]
    TRACKED_DOCUMENTS_FIELD_NUMBER: _ClassVar[int]
    INITIAL_SEED_FIELD_NUMBER: _ClassVar[int]
    TURN_IN_PROGRESS_FIELD_NUMBER: _ClassVar[int]
    EVENTS_SEQ_FIELD_NUMBER: _ClassVar[int]
    TURN_START_SEQ_FIELD_NUMBER: _ClassVar[int]
    STATUS_ONLY_FIELD_NUMBER: _ClassVar[int]
    TURN_STARTED_AT_MS_FIELD_NUMBER: _ClassVar[int]
    LAST_TURN_STARTED_AT_MS_FIELD_NUMBER: _ClassVar[int]
    LAST_TURN_ENDED_AT_MS_FIELD_NUMBER: _ClassVar[int]
    messages: _containers.RepeatedCompositeFieldContainer[Message]
    attachment_rows: _containers.RepeatedCompositeFieldContainer[SessionAttachmentRow]
    tracked_documents: _containers.RepeatedCompositeFieldContainer[TrackedDocumentRef]
    initial_seed: str
    turn_in_progress: bool
    events_seq: int
    turn_start_seq: int
    status_only: bool
    turn_started_at_ms: int
    last_turn_started_at_ms: int
    last_turn_ended_at_ms: int
    def __init__(self, messages: _Optional[_Iterable[_Union[Message, _Mapping]]] = ..., attachment_rows: _Optional[_Iterable[_Union[SessionAttachmentRow, _Mapping]]] = ..., tracked_documents: _Optional[_Iterable[_Union[TrackedDocumentRef, _Mapping]]] = ..., initial_seed: _Optional[str] = ..., turn_in_progress: _Optional[bool] = ..., events_seq: _Optional[int] = ..., turn_start_seq: _Optional[int] = ..., status_only: _Optional[bool] = ..., turn_started_at_ms: _Optional[int] = ..., last_turn_started_at_ms: _Optional[int] = ..., last_turn_ended_at_ms: _Optional[int] = ...) -> None: ...

class SubscribeSessionEventsRequest(_message.Message):
    __slots__ = ("session_id", "after_seq")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    AFTER_SEQ_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    after_seq: int
    def __init__(self, session_id: _Optional[str] = ..., after_seq: _Optional[int] = ...) -> None: ...

class CancelTurnRequest(_message.Message):
    __slots__ = ("session_id", "reason")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    reason: str
    def __init__(self, session_id: _Optional[str] = ..., reason: _Optional[str] = ...) -> None: ...

class CancelTurnResponse(_message.Message):
    __slots__ = ("cancelled",)
    CANCELLED_FIELD_NUMBER: _ClassVar[int]
    cancelled: bool
    def __init__(self, cancelled: _Optional[bool] = ...) -> None: ...

class ReplaceSessionHistoryRequest(_message.Message):
    __slots__ = ("session_id", "messages")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    MESSAGES_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    messages: _containers.RepeatedCompositeFieldContainer[Message]
    def __init__(self, session_id: _Optional[str] = ..., messages: _Optional[_Iterable[_Union[Message, _Mapping]]] = ...) -> None: ...

class ReplaceSessionHistoryResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class UpsertSessionAttachmentRequest(_message.Message):
    __slots__ = ("session_id", "anchor_message_id", "attachment")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    ANCHOR_MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    ATTACHMENT_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    anchor_message_id: str
    attachment: MessageAttachment
    def __init__(self, session_id: _Optional[str] = ..., anchor_message_id: _Optional[str] = ..., attachment: _Optional[_Union[MessageAttachment, _Mapping]] = ...) -> None: ...

class UpsertSessionAttachmentResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class DeleteSessionAttachmentRequest(_message.Message):
    __slots__ = ("session_id", "attachment_id")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    ATTACHMENT_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    attachment_id: str
    def __init__(self, session_id: _Optional[str] = ..., attachment_id: _Optional[str] = ...) -> None: ...

class DeleteSessionAttachmentResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class PatchAssistantMessageContentRequest(_message.Message):
    __slots__ = ("session_id", "message_id", "content", "turn_id")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    message_id: str
    content: str
    turn_id: str
    def __init__(self, session_id: _Optional[str] = ..., message_id: _Optional[str] = ..., content: _Optional[str] = ..., turn_id: _Optional[str] = ...) -> None: ...

class PatchAssistantMessageContentResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class LlmProviderInfo(_message.Message):
    __slots__ = ("id", "type", "default_model", "catalog_model_count", "models_refreshed_unix_ms", "user_registered", "stateful")
    ID_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_MODEL_FIELD_NUMBER: _ClassVar[int]
    CATALOG_MODEL_COUNT_FIELD_NUMBER: _ClassVar[int]
    MODELS_REFRESHED_UNIX_MS_FIELD_NUMBER: _ClassVar[int]
    USER_REGISTERED_FIELD_NUMBER: _ClassVar[int]
    STATEFUL_FIELD_NUMBER: _ClassVar[int]
    id: str
    type: LlmProviderType
    default_model: str
    catalog_model_count: int
    models_refreshed_unix_ms: int
    user_registered: bool
    stateful: bool
    def __init__(self, id: _Optional[str] = ..., type: _Optional[_Union[LlmProviderType, str]] = ..., default_model: _Optional[str] = ..., catalog_model_count: _Optional[int] = ..., models_refreshed_unix_ms: _Optional[int] = ..., user_registered: _Optional[bool] = ..., stateful: _Optional[bool] = ...) -> None: ...

class CatalogLlmModel(_message.Message):
    __slots__ = ("provider_id", "model_id", "display_name")
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    MODEL_ID_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    provider_id: str
    model_id: str
    display_name: str
    def __init__(self, provider_id: _Optional[str] = ..., model_id: _Optional[str] = ..., display_name: _Optional[str] = ...) -> None: ...

class ListLlmProvidersRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListLlmProvidersResponse(_message.Message):
    __slots__ = ("providers",)
    PROVIDERS_FIELD_NUMBER: _ClassVar[int]
    providers: _containers.RepeatedCompositeFieldContainer[LlmProviderInfo]
    def __init__(self, providers: _Optional[_Iterable[_Union[LlmProviderInfo, _Mapping]]] = ...) -> None: ...

class SupportedLlmProviderType(_message.Message):
    __slots__ = ("type", "short_code", "display_name", "requires_api_key", "is_cli_backed")
    TYPE_FIELD_NUMBER: _ClassVar[int]
    SHORT_CODE_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    REQUIRES_API_KEY_FIELD_NUMBER: _ClassVar[int]
    IS_CLI_BACKED_FIELD_NUMBER: _ClassVar[int]
    type: LlmProviderType
    short_code: str
    display_name: str
    requires_api_key: bool
    is_cli_backed: bool
    def __init__(self, type: _Optional[_Union[LlmProviderType, str]] = ..., short_code: _Optional[str] = ..., display_name: _Optional[str] = ..., requires_api_key: _Optional[bool] = ..., is_cli_backed: _Optional[bool] = ...) -> None: ...

class ListSupportedLlmProviderTypesRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListSupportedLlmProviderTypesResponse(_message.Message):
    __slots__ = ("types",)
    TYPES_FIELD_NUMBER: _ClassVar[int]
    types: _containers.RepeatedCompositeFieldContainer[SupportedLlmProviderType]
    def __init__(self, types: _Optional[_Iterable[_Union[SupportedLlmProviderType, _Mapping]]] = ...) -> None: ...

class RefreshLlmProviderModelsRequest(_message.Message):
    __slots__ = ("provider_id",)
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    provider_id: str
    def __init__(self, provider_id: _Optional[str] = ...) -> None: ...

class RefreshLlmProviderModelsResponse(_message.Message):
    __slots__ = ("models_discovered",)
    MODELS_DISCOVERED_FIELD_NUMBER: _ClassVar[int]
    models_discovered: int
    def __init__(self, models_discovered: _Optional[int] = ...) -> None: ...

class ListLlmCatalogModelsRequest(_message.Message):
    __slots__ = ("provider_id",)
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    provider_id: str
    def __init__(self, provider_id: _Optional[str] = ...) -> None: ...

class ListLlmCatalogModelsResponse(_message.Message):
    __slots__ = ("models",)
    MODELS_FIELD_NUMBER: _ClassVar[int]
    models: _containers.RepeatedCompositeFieldContainer[CatalogLlmModel]
    def __init__(self, models: _Optional[_Iterable[_Union[CatalogLlmModel, _Mapping]]] = ...) -> None: ...

class AnthropicLlmConfig(_message.Message):
    __slots__ = ("api_key", "base_url", "default_model", "max_output_tokens")
    API_KEY_FIELD_NUMBER: _ClassVar[int]
    BASE_URL_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_MODEL_FIELD_NUMBER: _ClassVar[int]
    MAX_OUTPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    api_key: str
    base_url: str
    default_model: str
    max_output_tokens: int
    def __init__(self, api_key: _Optional[str] = ..., base_url: _Optional[str] = ..., default_model: _Optional[str] = ..., max_output_tokens: _Optional[int] = ...) -> None: ...

class OpenAiLlmConfig(_message.Message):
    __slots__ = ("api_key", "base_url", "default_model", "max_tokens")
    API_KEY_FIELD_NUMBER: _ClassVar[int]
    BASE_URL_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_MODEL_FIELD_NUMBER: _ClassVar[int]
    MAX_TOKENS_FIELD_NUMBER: _ClassVar[int]
    api_key: str
    base_url: str
    default_model: str
    max_tokens: int
    def __init__(self, api_key: _Optional[str] = ..., base_url: _Optional[str] = ..., default_model: _Optional[str] = ..., max_tokens: _Optional[int] = ...) -> None: ...

class OllamaLlmConfig(_message.Message):
    __slots__ = ("base_url", "default_model", "max_tokens")
    BASE_URL_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_MODEL_FIELD_NUMBER: _ClassVar[int]
    MAX_TOKENS_FIELD_NUMBER: _ClassVar[int]
    base_url: str
    default_model: str
    max_tokens: int
    def __init__(self, base_url: _Optional[str] = ..., default_model: _Optional[str] = ..., max_tokens: _Optional[int] = ...) -> None: ...

class CursorCliLlmConfig(_message.Message):
    __slots__ = ("binary", "cwd", "workspace", "api_key", "default_model", "cli_mode", "trust", "approve_mcps", "force", "session_id", "max_output_tokens")
    BINARY_FIELD_NUMBER: _ClassVar[int]
    CWD_FIELD_NUMBER: _ClassVar[int]
    WORKSPACE_FIELD_NUMBER: _ClassVar[int]
    API_KEY_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_MODEL_FIELD_NUMBER: _ClassVar[int]
    CLI_MODE_FIELD_NUMBER: _ClassVar[int]
    TRUST_FIELD_NUMBER: _ClassVar[int]
    APPROVE_MCPS_FIELD_NUMBER: _ClassVar[int]
    FORCE_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    MAX_OUTPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    binary: str
    cwd: str
    workspace: str
    api_key: str
    default_model: str
    cli_mode: CursorCliMode
    trust: bool
    approve_mcps: bool
    force: bool
    session_id: str
    max_output_tokens: int
    def __init__(self, binary: _Optional[str] = ..., cwd: _Optional[str] = ..., workspace: _Optional[str] = ..., api_key: _Optional[str] = ..., default_model: _Optional[str] = ..., cli_mode: _Optional[_Union[CursorCliMode, str]] = ..., trust: _Optional[bool] = ..., approve_mcps: _Optional[bool] = ..., force: _Optional[bool] = ..., session_id: _Optional[str] = ..., max_output_tokens: _Optional[int] = ...) -> None: ...

class ClaudeCodeLlmConfig(_message.Message):
    __slots__ = ("binary", "cwd", "agent", "default_model", "system_prompt", "append_system_prompt", "effort", "permission_mode", "max_budget_usd", "add_dir", "allowed_tools", "disallowed_tools", "mcp_config_path", "timeout_ms", "max_turns", "setting_sources", "thinking_type", "thinking_budget_tokens", "hooks_json", "strict_mcp_config", "include_hook_events", "config_dir", "use_my_claude_config", "defer_tool_schemas", "use_claude_code_preset", "oauth_token")
    BINARY_FIELD_NUMBER: _ClassVar[int]
    CWD_FIELD_NUMBER: _ClassVar[int]
    AGENT_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_MODEL_FIELD_NUMBER: _ClassVar[int]
    SYSTEM_PROMPT_FIELD_NUMBER: _ClassVar[int]
    APPEND_SYSTEM_PROMPT_FIELD_NUMBER: _ClassVar[int]
    EFFORT_FIELD_NUMBER: _ClassVar[int]
    PERMISSION_MODE_FIELD_NUMBER: _ClassVar[int]
    MAX_BUDGET_USD_FIELD_NUMBER: _ClassVar[int]
    ADD_DIR_FIELD_NUMBER: _ClassVar[int]
    ALLOWED_TOOLS_FIELD_NUMBER: _ClassVar[int]
    DISALLOWED_TOOLS_FIELD_NUMBER: _ClassVar[int]
    MCP_CONFIG_PATH_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_MS_FIELD_NUMBER: _ClassVar[int]
    MAX_TURNS_FIELD_NUMBER: _ClassVar[int]
    SETTING_SOURCES_FIELD_NUMBER: _ClassVar[int]
    THINKING_TYPE_FIELD_NUMBER: _ClassVar[int]
    THINKING_BUDGET_TOKENS_FIELD_NUMBER: _ClassVar[int]
    HOOKS_JSON_FIELD_NUMBER: _ClassVar[int]
    STRICT_MCP_CONFIG_FIELD_NUMBER: _ClassVar[int]
    INCLUDE_HOOK_EVENTS_FIELD_NUMBER: _ClassVar[int]
    CONFIG_DIR_FIELD_NUMBER: _ClassVar[int]
    USE_MY_CLAUDE_CONFIG_FIELD_NUMBER: _ClassVar[int]
    DEFER_TOOL_SCHEMAS_FIELD_NUMBER: _ClassVar[int]
    USE_CLAUDE_CODE_PRESET_FIELD_NUMBER: _ClassVar[int]
    OAUTH_TOKEN_FIELD_NUMBER: _ClassVar[int]
    binary: str
    cwd: str
    agent: str
    default_model: str
    system_prompt: str
    append_system_prompt: str
    effort: str
    permission_mode: str
    max_budget_usd: float
    add_dir: _containers.RepeatedScalarFieldContainer[str]
    allowed_tools: _containers.RepeatedScalarFieldContainer[str]
    disallowed_tools: _containers.RepeatedScalarFieldContainer[str]
    mcp_config_path: str
    timeout_ms: int
    max_turns: int
    setting_sources: _containers.RepeatedScalarFieldContainer[str]
    thinking_type: str
    thinking_budget_tokens: int
    hooks_json: str
    strict_mcp_config: bool
    include_hook_events: bool
    config_dir: str
    use_my_claude_config: bool
    defer_tool_schemas: bool
    use_claude_code_preset: bool
    oauth_token: str
    def __init__(self, binary: _Optional[str] = ..., cwd: _Optional[str] = ..., agent: _Optional[str] = ..., default_model: _Optional[str] = ..., system_prompt: _Optional[str] = ..., append_system_prompt: _Optional[str] = ..., effort: _Optional[str] = ..., permission_mode: _Optional[str] = ..., max_budget_usd: _Optional[float] = ..., add_dir: _Optional[_Iterable[str]] = ..., allowed_tools: _Optional[_Iterable[str]] = ..., disallowed_tools: _Optional[_Iterable[str]] = ..., mcp_config_path: _Optional[str] = ..., timeout_ms: _Optional[int] = ..., max_turns: _Optional[int] = ..., setting_sources: _Optional[_Iterable[str]] = ..., thinking_type: _Optional[str] = ..., thinking_budget_tokens: _Optional[int] = ..., hooks_json: _Optional[str] = ..., strict_mcp_config: _Optional[bool] = ..., include_hook_events: _Optional[bool] = ..., config_dir: _Optional[str] = ..., use_my_claude_config: _Optional[bool] = ..., defer_tool_schemas: _Optional[bool] = ..., use_claude_code_preset: _Optional[bool] = ..., oauth_token: _Optional[str] = ...) -> None: ...

class GeminiLlmConfig(_message.Message):
    __slots__ = ("api_key", "base_url", "default_model", "max_output_tokens")
    API_KEY_FIELD_NUMBER: _ClassVar[int]
    BASE_URL_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_MODEL_FIELD_NUMBER: _ClassVar[int]
    MAX_OUTPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    api_key: str
    base_url: str
    default_model: str
    max_output_tokens: int
    def __init__(self, api_key: _Optional[str] = ..., base_url: _Optional[str] = ..., default_model: _Optional[str] = ..., max_output_tokens: _Optional[int] = ...) -> None: ...

class OpenRouterLlmConfig(_message.Message):
    __slots__ = ("api_key", "base_url", "default_model", "default_call_options", "avoid_unhealthy_endpoints", "endpoint_health_ttl_seconds")
    API_KEY_FIELD_NUMBER: _ClassVar[int]
    BASE_URL_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_MODEL_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_CALL_OPTIONS_FIELD_NUMBER: _ClassVar[int]
    AVOID_UNHEALTHY_ENDPOINTS_FIELD_NUMBER: _ClassVar[int]
    ENDPOINT_HEALTH_TTL_SECONDS_FIELD_NUMBER: _ClassVar[int]
    api_key: str
    base_url: str
    default_model: str
    default_call_options: OpenRouterCallOptions
    avoid_unhealthy_endpoints: bool
    endpoint_health_ttl_seconds: int
    def __init__(self, api_key: _Optional[str] = ..., base_url: _Optional[str] = ..., default_model: _Optional[str] = ..., default_call_options: _Optional[_Union[OpenRouterCallOptions, _Mapping]] = ..., avoid_unhealthy_endpoints: _Optional[bool] = ..., endpoint_health_ttl_seconds: _Optional[int] = ...) -> None: ...

class BedrockLlmConfig(_message.Message):
    __slots__ = ("region", "profile", "default_model", "max_output_tokens", "endpoint_url", "assume_role_arn", "role_session_name", "external_id")
    REGION_FIELD_NUMBER: _ClassVar[int]
    PROFILE_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_MODEL_FIELD_NUMBER: _ClassVar[int]
    MAX_OUTPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    ENDPOINT_URL_FIELD_NUMBER: _ClassVar[int]
    ASSUME_ROLE_ARN_FIELD_NUMBER: _ClassVar[int]
    ROLE_SESSION_NAME_FIELD_NUMBER: _ClassVar[int]
    EXTERNAL_ID_FIELD_NUMBER: _ClassVar[int]
    region: str
    profile: str
    default_model: str
    max_output_tokens: int
    endpoint_url: str
    assume_role_arn: str
    role_session_name: str
    external_id: str
    def __init__(self, region: _Optional[str] = ..., profile: _Optional[str] = ..., default_model: _Optional[str] = ..., max_output_tokens: _Optional[int] = ..., endpoint_url: _Optional[str] = ..., assume_role_arn: _Optional[str] = ..., role_session_name: _Optional[str] = ..., external_id: _Optional[str] = ...) -> None: ...

class RegisterLlmProviderRequest(_message.Message):
    __slots__ = ("provider_id", "type", "anthropic", "openai", "ollama", "cursor_cli", "claude_code", "bedrock", "gemini", "openrouter")
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    ANTHROPIC_FIELD_NUMBER: _ClassVar[int]
    OPENAI_FIELD_NUMBER: _ClassVar[int]
    OLLAMA_FIELD_NUMBER: _ClassVar[int]
    CURSOR_CLI_FIELD_NUMBER: _ClassVar[int]
    CLAUDE_CODE_FIELD_NUMBER: _ClassVar[int]
    BEDROCK_FIELD_NUMBER: _ClassVar[int]
    GEMINI_FIELD_NUMBER: _ClassVar[int]
    OPENROUTER_FIELD_NUMBER: _ClassVar[int]
    provider_id: str
    type: LlmProviderType
    anthropic: AnthropicLlmConfig
    openai: OpenAiLlmConfig
    ollama: OllamaLlmConfig
    cursor_cli: CursorCliLlmConfig
    claude_code: ClaudeCodeLlmConfig
    bedrock: BedrockLlmConfig
    gemini: GeminiLlmConfig
    openrouter: OpenRouterLlmConfig
    def __init__(self, provider_id: _Optional[str] = ..., type: _Optional[_Union[LlmProviderType, str]] = ..., anthropic: _Optional[_Union[AnthropicLlmConfig, _Mapping]] = ..., openai: _Optional[_Union[OpenAiLlmConfig, _Mapping]] = ..., ollama: _Optional[_Union[OllamaLlmConfig, _Mapping]] = ..., cursor_cli: _Optional[_Union[CursorCliLlmConfig, _Mapping]] = ..., claude_code: _Optional[_Union[ClaudeCodeLlmConfig, _Mapping]] = ..., bedrock: _Optional[_Union[BedrockLlmConfig, _Mapping]] = ..., gemini: _Optional[_Union[GeminiLlmConfig, _Mapping]] = ..., openrouter: _Optional[_Union[OpenRouterLlmConfig, _Mapping]] = ...) -> None: ...

class RegisterLlmProviderResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class UnregisterLlmProviderRequest(_message.Message):
    __slots__ = ("provider_id",)
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    provider_id: str
    def __init__(self, provider_id: _Optional[str] = ...) -> None: ...

class UnregisterLlmProviderResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class GetLlmProviderConfigRequest(_message.Message):
    __slots__ = ("provider_id",)
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    provider_id: str
    def __init__(self, provider_id: _Optional[str] = ...) -> None: ...

class GetLlmProviderConfigResponse(_message.Message):
    __slots__ = ("provider_id", "type", "anthropic", "openai", "ollama", "cursor_cli", "claude_code", "bedrock", "gemini", "openrouter")
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    ANTHROPIC_FIELD_NUMBER: _ClassVar[int]
    OPENAI_FIELD_NUMBER: _ClassVar[int]
    OLLAMA_FIELD_NUMBER: _ClassVar[int]
    CURSOR_CLI_FIELD_NUMBER: _ClassVar[int]
    CLAUDE_CODE_FIELD_NUMBER: _ClassVar[int]
    BEDROCK_FIELD_NUMBER: _ClassVar[int]
    GEMINI_FIELD_NUMBER: _ClassVar[int]
    OPENROUTER_FIELD_NUMBER: _ClassVar[int]
    provider_id: str
    type: LlmProviderType
    anthropic: AnthropicLlmConfig
    openai: OpenAiLlmConfig
    ollama: OllamaLlmConfig
    cursor_cli: CursorCliLlmConfig
    claude_code: ClaudeCodeLlmConfig
    bedrock: BedrockLlmConfig
    gemini: GeminiLlmConfig
    openrouter: OpenRouterLlmConfig
    def __init__(self, provider_id: _Optional[str] = ..., type: _Optional[_Union[LlmProviderType, str]] = ..., anthropic: _Optional[_Union[AnthropicLlmConfig, _Mapping]] = ..., openai: _Optional[_Union[OpenAiLlmConfig, _Mapping]] = ..., ollama: _Optional[_Union[OllamaLlmConfig, _Mapping]] = ..., cursor_cli: _Optional[_Union[CursorCliLlmConfig, _Mapping]] = ..., claude_code: _Optional[_Union[ClaudeCodeLlmConfig, _Mapping]] = ..., bedrock: _Optional[_Union[BedrockLlmConfig, _Mapping]] = ..., gemini: _Optional[_Union[GeminiLlmConfig, _Mapping]] = ..., openrouter: _Optional[_Union[OpenRouterLlmConfig, _Mapping]] = ...) -> None: ...

class UpdateLlmProviderConfigRequest(_message.Message):
    __slots__ = ("provider_id", "type", "anthropic", "openai", "ollama", "cursor_cli", "claude_code", "bedrock", "gemini", "openrouter")
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    ANTHROPIC_FIELD_NUMBER: _ClassVar[int]
    OPENAI_FIELD_NUMBER: _ClassVar[int]
    OLLAMA_FIELD_NUMBER: _ClassVar[int]
    CURSOR_CLI_FIELD_NUMBER: _ClassVar[int]
    CLAUDE_CODE_FIELD_NUMBER: _ClassVar[int]
    BEDROCK_FIELD_NUMBER: _ClassVar[int]
    GEMINI_FIELD_NUMBER: _ClassVar[int]
    OPENROUTER_FIELD_NUMBER: _ClassVar[int]
    provider_id: str
    type: LlmProviderType
    anthropic: AnthropicLlmConfig
    openai: OpenAiLlmConfig
    ollama: OllamaLlmConfig
    cursor_cli: CursorCliLlmConfig
    claude_code: ClaudeCodeLlmConfig
    bedrock: BedrockLlmConfig
    gemini: GeminiLlmConfig
    openrouter: OpenRouterLlmConfig
    def __init__(self, provider_id: _Optional[str] = ..., type: _Optional[_Union[LlmProviderType, str]] = ..., anthropic: _Optional[_Union[AnthropicLlmConfig, _Mapping]] = ..., openai: _Optional[_Union[OpenAiLlmConfig, _Mapping]] = ..., ollama: _Optional[_Union[OllamaLlmConfig, _Mapping]] = ..., cursor_cli: _Optional[_Union[CursorCliLlmConfig, _Mapping]] = ..., claude_code: _Optional[_Union[ClaudeCodeLlmConfig, _Mapping]] = ..., bedrock: _Optional[_Union[BedrockLlmConfig, _Mapping]] = ..., gemini: _Optional[_Union[GeminiLlmConfig, _Mapping]] = ..., openrouter: _Optional[_Union[OpenRouterLlmConfig, _Mapping]] = ...) -> None: ...

class UpdateLlmProviderConfigResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class DiscoverProviderBinariesRequest(_message.Message):
    __slots__ = ("provider_type", "binary")
    PROVIDER_TYPE_FIELD_NUMBER: _ClassVar[int]
    BINARY_FIELD_NUMBER: _ClassVar[int]
    provider_type: LlmProviderType
    binary: str
    def __init__(self, provider_type: _Optional[_Union[LlmProviderType, str]] = ..., binary: _Optional[str] = ...) -> None: ...

class DiscoveredProviderBinary(_message.Message):
    __slots__ = ("path", "version", "source", "origin", "usable", "error")
    PATH_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    ORIGIN_FIELD_NUMBER: _ClassVar[int]
    USABLE_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    path: str
    version: str
    source: str
    origin: str
    usable: bool
    error: str
    def __init__(self, path: _Optional[str] = ..., version: _Optional[str] = ..., source: _Optional[str] = ..., origin: _Optional[str] = ..., usable: _Optional[bool] = ..., error: _Optional[str] = ...) -> None: ...

class DiscoverProviderBinariesResponse(_message.Message):
    __slots__ = ("binaries", "searched")
    BINARIES_FIELD_NUMBER: _ClassVar[int]
    SEARCHED_FIELD_NUMBER: _ClassVar[int]
    binaries: _containers.RepeatedCompositeFieldContainer[DiscoveredProviderBinary]
    searched: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, binaries: _Optional[_Iterable[_Union[DiscoveredProviderBinary, _Mapping]]] = ..., searched: _Optional[_Iterable[str]] = ...) -> None: ...

class CheckProviderConnectionRequest(_message.Message):
    __slots__ = ("provider_type", "binary", "cwd", "config_dir", "timeout_ms", "oauth_token", "anthropic", "openai", "ollama", "cursor_cli", "claude_code", "bedrock", "gemini", "openrouter")
    PROVIDER_TYPE_FIELD_NUMBER: _ClassVar[int]
    BINARY_FIELD_NUMBER: _ClassVar[int]
    CWD_FIELD_NUMBER: _ClassVar[int]
    CONFIG_DIR_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_MS_FIELD_NUMBER: _ClassVar[int]
    OAUTH_TOKEN_FIELD_NUMBER: _ClassVar[int]
    ANTHROPIC_FIELD_NUMBER: _ClassVar[int]
    OPENAI_FIELD_NUMBER: _ClassVar[int]
    OLLAMA_FIELD_NUMBER: _ClassVar[int]
    CURSOR_CLI_FIELD_NUMBER: _ClassVar[int]
    CLAUDE_CODE_FIELD_NUMBER: _ClassVar[int]
    BEDROCK_FIELD_NUMBER: _ClassVar[int]
    GEMINI_FIELD_NUMBER: _ClassVar[int]
    OPENROUTER_FIELD_NUMBER: _ClassVar[int]
    provider_type: LlmProviderType
    binary: str
    cwd: str
    config_dir: str
    timeout_ms: int
    oauth_token: str
    anthropic: AnthropicLlmConfig
    openai: OpenAiLlmConfig
    ollama: OllamaLlmConfig
    cursor_cli: CursorCliLlmConfig
    claude_code: ClaudeCodeLlmConfig
    bedrock: BedrockLlmConfig
    gemini: GeminiLlmConfig
    openrouter: OpenRouterLlmConfig
    def __init__(self, provider_type: _Optional[_Union[LlmProviderType, str]] = ..., binary: _Optional[str] = ..., cwd: _Optional[str] = ..., config_dir: _Optional[str] = ..., timeout_ms: _Optional[int] = ..., oauth_token: _Optional[str] = ..., anthropic: _Optional[_Union[AnthropicLlmConfig, _Mapping]] = ..., openai: _Optional[_Union[OpenAiLlmConfig, _Mapping]] = ..., ollama: _Optional[_Union[OllamaLlmConfig, _Mapping]] = ..., cursor_cli: _Optional[_Union[CursorCliLlmConfig, _Mapping]] = ..., claude_code: _Optional[_Union[ClaudeCodeLlmConfig, _Mapping]] = ..., bedrock: _Optional[_Union[BedrockLlmConfig, _Mapping]] = ..., gemini: _Optional[_Union[GeminiLlmConfig, _Mapping]] = ..., openrouter: _Optional[_Union[OpenRouterLlmConfig, _Mapping]] = ...) -> None: ...

class CallerIdentity(_message.Message):
    __slots__ = ("account", "arn", "user_id")
    ACCOUNT_FIELD_NUMBER: _ClassVar[int]
    ARN_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    account: str
    arn: str
    user_id: str
    def __init__(self, account: _Optional[str] = ..., arn: _Optional[str] = ..., user_id: _Optional[str] = ...) -> None: ...

class CheckProviderConnectionResponse(_message.Message):
    __slots__ = ("status", "binary", "version", "message", "details", "needs_auth", "error", "credential_source", "warnings", "assumed_role_arn", "caller_identity")
    STATUS_FIELD_NUMBER: _ClassVar[int]
    BINARY_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    DETAILS_FIELD_NUMBER: _ClassVar[int]
    NEEDS_AUTH_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    CREDENTIAL_SOURCE_FIELD_NUMBER: _ClassVar[int]
    WARNINGS_FIELD_NUMBER: _ClassVar[int]
    ASSUMED_ROLE_ARN_FIELD_NUMBER: _ClassVar[int]
    CALLER_IDENTITY_FIELD_NUMBER: _ClassVar[int]
    status: ProviderConnectionStatus
    binary: str
    version: str
    message: str
    details: str
    needs_auth: bool
    error: LlmError
    credential_source: CredentialSource
    warnings: _containers.RepeatedScalarFieldContainer[str]
    assumed_role_arn: str
    caller_identity: CallerIdentity
    def __init__(self, status: _Optional[_Union[ProviderConnectionStatus, str]] = ..., binary: _Optional[str] = ..., version: _Optional[str] = ..., message: _Optional[str] = ..., details: _Optional[str] = ..., needs_auth: _Optional[bool] = ..., error: _Optional[_Union[LlmError, _Mapping]] = ..., credential_source: _Optional[_Union[CredentialSource, str]] = ..., warnings: _Optional[_Iterable[str]] = ..., assumed_role_arn: _Optional[str] = ..., caller_identity: _Optional[_Union[CallerIdentity, _Mapping]] = ...) -> None: ...

class ProviderAuthCommandStart(_message.Message):
    __slots__ = ("provider_type", "binary", "config_dir", "cols", "rows")
    PROVIDER_TYPE_FIELD_NUMBER: _ClassVar[int]
    BINARY_FIELD_NUMBER: _ClassVar[int]
    CONFIG_DIR_FIELD_NUMBER: _ClassVar[int]
    COLS_FIELD_NUMBER: _ClassVar[int]
    ROWS_FIELD_NUMBER: _ClassVar[int]
    provider_type: LlmProviderType
    binary: str
    config_dir: str
    cols: int
    rows: int
    def __init__(self, provider_type: _Optional[_Union[LlmProviderType, str]] = ..., binary: _Optional[str] = ..., config_dir: _Optional[str] = ..., cols: _Optional[int] = ..., rows: _Optional[int] = ...) -> None: ...

class ProviderAuthCommandResize(_message.Message):
    __slots__ = ("cols", "rows")
    COLS_FIELD_NUMBER: _ClassVar[int]
    ROWS_FIELD_NUMBER: _ClassVar[int]
    cols: int
    rows: int
    def __init__(self, cols: _Optional[int] = ..., rows: _Optional[int] = ...) -> None: ...

class StartProviderAuthSessionRequest(_message.Message):
    __slots__ = ("provider_type", "binary", "config_dir", "cols", "rows")
    PROVIDER_TYPE_FIELD_NUMBER: _ClassVar[int]
    BINARY_FIELD_NUMBER: _ClassVar[int]
    CONFIG_DIR_FIELD_NUMBER: _ClassVar[int]
    COLS_FIELD_NUMBER: _ClassVar[int]
    ROWS_FIELD_NUMBER: _ClassVar[int]
    provider_type: LlmProviderType
    binary: str
    config_dir: str
    cols: int
    rows: int
    def __init__(self, provider_type: _Optional[_Union[LlmProviderType, str]] = ..., binary: _Optional[str] = ..., config_dir: _Optional[str] = ..., cols: _Optional[int] = ..., rows: _Optional[int] = ...) -> None: ...

class StartProviderAuthSessionResponse(_message.Message):
    __slots__ = ("session_id",)
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    def __init__(self, session_id: _Optional[str] = ...) -> None: ...

class StreamProviderAuthOutputRequest(_message.Message):
    __slots__ = ("session_id", "replay")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    REPLAY_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    replay: bool
    def __init__(self, session_id: _Optional[str] = ..., replay: _Optional[bool] = ...) -> None: ...

class SendProviderAuthInputRequest(_message.Message):
    __slots__ = ("session_id", "stdin", "resize")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    STDIN_FIELD_NUMBER: _ClassVar[int]
    RESIZE_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    stdin: bytes
    resize: ProviderAuthCommandResize
    def __init__(self, session_id: _Optional[str] = ..., stdin: _Optional[bytes] = ..., resize: _Optional[_Union[ProviderAuthCommandResize, _Mapping]] = ...) -> None: ...

class SendProviderAuthInputResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class EndProviderAuthSessionRequest(_message.Message):
    __slots__ = ("session_id",)
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    def __init__(self, session_id: _Optional[str] = ...) -> None: ...

class EndProviderAuthSessionResponse(_message.Message):
    __slots__ = ("exit_code", "exited")
    EXIT_CODE_FIELD_NUMBER: _ClassVar[int]
    EXITED_FIELD_NUMBER: _ClassVar[int]
    exit_code: int
    exited: bool
    def __init__(self, exit_code: _Optional[int] = ..., exited: _Optional[bool] = ...) -> None: ...

class ProviderAuthCommandInput(_message.Message):
    __slots__ = ("start", "stdin", "resize")
    START_FIELD_NUMBER: _ClassVar[int]
    STDIN_FIELD_NUMBER: _ClassVar[int]
    RESIZE_FIELD_NUMBER: _ClassVar[int]
    start: ProviderAuthCommandStart
    stdin: bytes
    resize: ProviderAuthCommandResize
    def __init__(self, start: _Optional[_Union[ProviderAuthCommandStart, _Mapping]] = ..., stdin: _Optional[bytes] = ..., resize: _Optional[_Union[ProviderAuthCommandResize, _Mapping]] = ...) -> None: ...

class ProviderAuthCommandOutput(_message.Message):
    __slots__ = ("output", "exit_code", "started")
    OUTPUT_FIELD_NUMBER: _ClassVar[int]
    EXIT_CODE_FIELD_NUMBER: _ClassVar[int]
    STARTED_FIELD_NUMBER: _ClassVar[int]
    output: bytes
    exit_code: int
    started: bool
    def __init__(self, output: _Optional[bytes] = ..., exit_code: _Optional[int] = ..., started: _Optional[bool] = ...) -> None: ...

class UserOllamaEmbeddingConfig(_message.Message):
    __slots__ = ("base_url", "embed_model", "dimensions")
    BASE_URL_FIELD_NUMBER: _ClassVar[int]
    EMBED_MODEL_FIELD_NUMBER: _ClassVar[int]
    DIMENSIONS_FIELD_NUMBER: _ClassVar[int]
    base_url: str
    embed_model: str
    dimensions: int
    def __init__(self, base_url: _Optional[str] = ..., embed_model: _Optional[str] = ..., dimensions: _Optional[int] = ...) -> None: ...

class UserOpenAiCompatEmbeddingConfig(_message.Message):
    __slots__ = ("base_url", "api_key", "embed_model", "dimensions")
    BASE_URL_FIELD_NUMBER: _ClassVar[int]
    API_KEY_FIELD_NUMBER: _ClassVar[int]
    EMBED_MODEL_FIELD_NUMBER: _ClassVar[int]
    DIMENSIONS_FIELD_NUMBER: _ClassVar[int]
    base_url: str
    api_key: str
    embed_model: str
    dimensions: int
    def __init__(self, base_url: _Optional[str] = ..., api_key: _Optional[str] = ..., embed_model: _Optional[str] = ..., dimensions: _Optional[int] = ...) -> None: ...

class UserBedrockEmbeddingConfig(_message.Message):
    __slots__ = ("region", "profile", "embed_model", "dimensions", "endpoint_url")
    REGION_FIELD_NUMBER: _ClassVar[int]
    PROFILE_FIELD_NUMBER: _ClassVar[int]
    EMBED_MODEL_FIELD_NUMBER: _ClassVar[int]
    DIMENSIONS_FIELD_NUMBER: _ClassVar[int]
    ENDPOINT_URL_FIELD_NUMBER: _ClassVar[int]
    region: str
    profile: str
    embed_model: str
    dimensions: int
    endpoint_url: str
    def __init__(self, region: _Optional[str] = ..., profile: _Optional[str] = ..., embed_model: _Optional[str] = ..., dimensions: _Optional[int] = ..., endpoint_url: _Optional[str] = ...) -> None: ...

class RegisterUserEmbeddingProviderRequest(_message.Message):
    __slots__ = ("provider_id", "ollama", "openai_compat", "bedrock")
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    OLLAMA_FIELD_NUMBER: _ClassVar[int]
    OPENAI_COMPAT_FIELD_NUMBER: _ClassVar[int]
    BEDROCK_FIELD_NUMBER: _ClassVar[int]
    provider_id: str
    ollama: UserOllamaEmbeddingConfig
    openai_compat: UserOpenAiCompatEmbeddingConfig
    bedrock: UserBedrockEmbeddingConfig
    def __init__(self, provider_id: _Optional[str] = ..., ollama: _Optional[_Union[UserOllamaEmbeddingConfig, _Mapping]] = ..., openai_compat: _Optional[_Union[UserOpenAiCompatEmbeddingConfig, _Mapping]] = ..., bedrock: _Optional[_Union[UserBedrockEmbeddingConfig, _Mapping]] = ...) -> None: ...

class RegisterUserEmbeddingProviderResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class UnregisterUserEmbeddingProviderRequest(_message.Message):
    __slots__ = ("provider_id",)
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    provider_id: str
    def __init__(self, provider_id: _Optional[str] = ...) -> None: ...

class UnregisterUserEmbeddingProviderResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListEmbeddingProvidersRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListEmbeddingProvidersResponse(_message.Message):
    __slots__ = ("provider_ids",)
    PROVIDER_IDS_FIELD_NUMBER: _ClassVar[int]
    provider_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, provider_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class TokenUsage(_message.Message):
    __slots__ = ("input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "total_cost_usd", "reasoning_tokens")
    INPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    CACHE_READ_TOKENS_FIELD_NUMBER: _ClassVar[int]
    CACHE_WRITE_TOKENS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_COST_USD_FIELD_NUMBER: _ClassVar[int]
    REASONING_TOKENS_FIELD_NUMBER: _ClassVar[int]
    input_tokens: int
    output_tokens: int
    cache_read_tokens: int
    cache_write_tokens: int
    total_cost_usd: float
    reasoning_tokens: int
    def __init__(self, input_tokens: _Optional[int] = ..., output_tokens: _Optional[int] = ..., cache_read_tokens: _Optional[int] = ..., cache_write_tokens: _Optional[int] = ..., total_cost_usd: _Optional[float] = ..., reasoning_tokens: _Optional[int] = ...) -> None: ...

class QueryUsageRequest(_message.Message):
    __slots__ = ("tenant_id", "scope_id", "model", "since_unix", "until_unix", "group_by")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    SCOPE_ID_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    SINCE_UNIX_FIELD_NUMBER: _ClassVar[int]
    UNTIL_UNIX_FIELD_NUMBER: _ClassVar[int]
    GROUP_BY_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    scope_id: str
    model: str
    since_unix: int
    until_unix: int
    group_by: UsageGroupBy
    def __init__(self, tenant_id: _Optional[str] = ..., scope_id: _Optional[str] = ..., model: _Optional[str] = ..., since_unix: _Optional[int] = ..., until_unix: _Optional[int] = ..., group_by: _Optional[_Union[UsageGroupBy, str]] = ...) -> None: ...

class UsageRow(_message.Message):
    __slots__ = ("tenant_id", "scope_id", "model", "recorded_unix", "input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens", "cost_usd")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    SCOPE_ID_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    RECORDED_UNIX_FIELD_NUMBER: _ClassVar[int]
    INPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    CACHE_READ_TOKENS_FIELD_NUMBER: _ClassVar[int]
    CACHE_WRITE_TOKENS_FIELD_NUMBER: _ClassVar[int]
    REASONING_TOKENS_FIELD_NUMBER: _ClassVar[int]
    COST_USD_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    scope_id: str
    model: str
    recorded_unix: int
    input_tokens: int
    output_tokens: int
    cache_read_tokens: int
    cache_write_tokens: int
    reasoning_tokens: int
    cost_usd: float
    def __init__(self, tenant_id: _Optional[str] = ..., scope_id: _Optional[str] = ..., model: _Optional[str] = ..., recorded_unix: _Optional[int] = ..., input_tokens: _Optional[int] = ..., output_tokens: _Optional[int] = ..., cache_read_tokens: _Optional[int] = ..., cache_write_tokens: _Optional[int] = ..., reasoning_tokens: _Optional[int] = ..., cost_usd: _Optional[float] = ...) -> None: ...

class UsageTotal(_message.Message):
    __slots__ = ("tenant_id", "scope_id", "model", "input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens", "cost_usd", "call_count")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    SCOPE_ID_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    INPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    CACHE_READ_TOKENS_FIELD_NUMBER: _ClassVar[int]
    CACHE_WRITE_TOKENS_FIELD_NUMBER: _ClassVar[int]
    REASONING_TOKENS_FIELD_NUMBER: _ClassVar[int]
    COST_USD_FIELD_NUMBER: _ClassVar[int]
    CALL_COUNT_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    scope_id: str
    model: str
    input_tokens: int
    output_tokens: int
    cache_read_tokens: int
    cache_write_tokens: int
    reasoning_tokens: int
    cost_usd: float
    call_count: int
    def __init__(self, tenant_id: _Optional[str] = ..., scope_id: _Optional[str] = ..., model: _Optional[str] = ..., input_tokens: _Optional[int] = ..., output_tokens: _Optional[int] = ..., cache_read_tokens: _Optional[int] = ..., cache_write_tokens: _Optional[int] = ..., reasoning_tokens: _Optional[int] = ..., cost_usd: _Optional[float] = ..., call_count: _Optional[int] = ...) -> None: ...

class QueryUsageResponse(_message.Message):
    __slots__ = ("rows", "totals")
    ROWS_FIELD_NUMBER: _ClassVar[int]
    TOTALS_FIELD_NUMBER: _ClassVar[int]
    rows: _containers.RepeatedCompositeFieldContainer[UsageRow]
    totals: _containers.RepeatedCompositeFieldContainer[UsageTotal]
    def __init__(self, rows: _Optional[_Iterable[_Union[UsageRow, _Mapping]]] = ..., totals: _Optional[_Iterable[_Union[UsageTotal, _Mapping]]] = ...) -> None: ...

class QueryToolUsageRequest(_message.Message):
    __slots__ = ("since_unix", "until_unix", "scope_id", "tenant_id")
    SINCE_UNIX_FIELD_NUMBER: _ClassVar[int]
    UNTIL_UNIX_FIELD_NUMBER: _ClassVar[int]
    SCOPE_ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    since_unix: int
    until_unix: int
    scope_id: str
    tenant_id: str
    def __init__(self, since_unix: _Optional[int] = ..., until_unix: _Optional[int] = ..., scope_id: _Optional[str] = ..., tenant_id: _Optional[str] = ...) -> None: ...

class ToolUsageStat(_message.Message):
    __slots__ = ("tool_name", "calls", "errors", "min_ms", "median_ms", "mean_ms", "max_ms")
    TOOL_NAME_FIELD_NUMBER: _ClassVar[int]
    CALLS_FIELD_NUMBER: _ClassVar[int]
    ERRORS_FIELD_NUMBER: _ClassVar[int]
    MIN_MS_FIELD_NUMBER: _ClassVar[int]
    MEDIAN_MS_FIELD_NUMBER: _ClassVar[int]
    MEAN_MS_FIELD_NUMBER: _ClassVar[int]
    MAX_MS_FIELD_NUMBER: _ClassVar[int]
    tool_name: str
    calls: int
    errors: int
    min_ms: int
    median_ms: float
    mean_ms: float
    max_ms: int
    def __init__(self, tool_name: _Optional[str] = ..., calls: _Optional[int] = ..., errors: _Optional[int] = ..., min_ms: _Optional[int] = ..., median_ms: _Optional[float] = ..., mean_ms: _Optional[float] = ..., max_ms: _Optional[int] = ...) -> None: ...

class QueryToolUsageResponse(_message.Message):
    __slots__ = ("stats",)
    STATS_FIELD_NUMBER: _ClassVar[int]
    stats: _containers.RepeatedCompositeFieldContainer[ToolUsageStat]
    def __init__(self, stats: _Optional[_Iterable[_Union[ToolUsageStat, _Mapping]]] = ...) -> None: ...

class EvUserTurnCommitted(_message.Message):
    __slots__ = ("user_message_id", "text_preview", "committed_at_ms")
    USER_MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    TEXT_PREVIEW_FIELD_NUMBER: _ClassVar[int]
    COMMITTED_AT_MS_FIELD_NUMBER: _ClassVar[int]
    user_message_id: str
    text_preview: str
    committed_at_ms: int
    def __init__(self, user_message_id: _Optional[str] = ..., text_preview: _Optional[str] = ..., committed_at_ms: _Optional[int] = ...) -> None: ...

class EvAssistantTurnStarted(_message.Message):
    __slots__ = ("turn_id", "thinking_enabled", "streaming_tool_calls", "parent_turn_group_id", "turn_seq")
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    THINKING_ENABLED_FIELD_NUMBER: _ClassVar[int]
    STREAMING_TOOL_CALLS_FIELD_NUMBER: _ClassVar[int]
    PARENT_TURN_GROUP_ID_FIELD_NUMBER: _ClassVar[int]
    TURN_SEQ_FIELD_NUMBER: _ClassVar[int]
    turn_id: str
    thinking_enabled: bool
    streaming_tool_calls: bool
    parent_turn_group_id: str
    turn_seq: int
    def __init__(self, turn_id: _Optional[str] = ..., thinking_enabled: _Optional[bool] = ..., streaming_tool_calls: _Optional[bool] = ..., parent_turn_group_id: _Optional[str] = ..., turn_seq: _Optional[int] = ...) -> None: ...

class EvAssistantThinkingDelta(_message.Message):
    __slots__ = ("turn_id", "delta")
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    DELTA_FIELD_NUMBER: _ClassVar[int]
    turn_id: str
    delta: str
    def __init__(self, turn_id: _Optional[str] = ..., delta: _Optional[str] = ...) -> None: ...

class EvAssistantActivitySnapshot(_message.Message):
    __slots__ = ("turn_id", "headline")
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    HEADLINE_FIELD_NUMBER: _ClassVar[int]
    turn_id: str
    headline: str
    def __init__(self, turn_id: _Optional[str] = ..., headline: _Optional[str] = ...) -> None: ...

class EvAssistantTextDelta(_message.Message):
    __slots__ = ("turn_id", "delta")
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    DELTA_FIELD_NUMBER: _ClassVar[int]
    turn_id: str
    delta: str
    def __init__(self, turn_id: _Optional[str] = ..., delta: _Optional[str] = ...) -> None: ...

class EvToolCallStreamStarted(_message.Message):
    __slots__ = ("turn_id", "id", "name")
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    turn_id: str
    id: str
    name: str
    def __init__(self, turn_id: _Optional[str] = ..., id: _Optional[str] = ..., name: _Optional[str] = ...) -> None: ...

class EvToolCallArgsDelta(_message.Message):
    __slots__ = ("turn_id", "id", "args_delta")
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    ARGS_DELTA_FIELD_NUMBER: _ClassVar[int]
    turn_id: str
    id: str
    args_delta: str
    def __init__(self, turn_id: _Optional[str] = ..., id: _Optional[str] = ..., args_delta: _Optional[str] = ...) -> None: ...

class EvToolCallStreamFinished(_message.Message):
    __slots__ = ("turn_id", "id", "name", "arguments_json")
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    ARGUMENTS_JSON_FIELD_NUMBER: _ClassVar[int]
    turn_id: str
    id: str
    name: str
    arguments_json: str
    def __init__(self, turn_id: _Optional[str] = ..., id: _Optional[str] = ..., name: _Optional[str] = ..., arguments_json: _Optional[str] = ...) -> None: ...

class EvToolExecutionStarted(_message.Message):
    __slots__ = ("turn_id", "tool_call_id", "name", "client_wait_nonce", "arguments_json", "arguments_truncated")
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    CLIENT_WAIT_NONCE_FIELD_NUMBER: _ClassVar[int]
    ARGUMENTS_JSON_FIELD_NUMBER: _ClassVar[int]
    ARGUMENTS_TRUNCATED_FIELD_NUMBER: _ClassVar[int]
    turn_id: str
    tool_call_id: str
    name: str
    client_wait_nonce: str
    arguments_json: str
    arguments_truncated: bool
    def __init__(self, turn_id: _Optional[str] = ..., tool_call_id: _Optional[str] = ..., name: _Optional[str] = ..., client_wait_nonce: _Optional[str] = ..., arguments_json: _Optional[str] = ..., arguments_truncated: _Optional[bool] = ...) -> None: ...

class EvToolExecutionFinished(_message.Message):
    __slots__ = ("turn_id", "tool_call_id", "name", "success", "error_message")
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    ERROR_MESSAGE_FIELD_NUMBER: _ClassVar[int]
    turn_id: str
    tool_call_id: str
    name: str
    success: bool
    error_message: str
    def __init__(self, turn_id: _Optional[str] = ..., tool_call_id: _Optional[str] = ..., name: _Optional[str] = ..., success: _Optional[bool] = ..., error_message: _Optional[str] = ...) -> None: ...

class EvAssistantTurnFinished(_message.Message):
    __slots__ = ("turn_id", "assistant_message_id", "finish_reason", "usage", "model_duration_ms")
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    ASSISTANT_MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    FINISH_REASON_FIELD_NUMBER: _ClassVar[int]
    USAGE_FIELD_NUMBER: _ClassVar[int]
    MODEL_DURATION_MS_FIELD_NUMBER: _ClassVar[int]
    turn_id: str
    assistant_message_id: str
    finish_reason: str
    usage: TokenUsage
    model_duration_ms: int
    def __init__(self, turn_id: _Optional[str] = ..., assistant_message_id: _Optional[str] = ..., finish_reason: _Optional[str] = ..., usage: _Optional[_Union[TokenUsage, _Mapping]] = ..., model_duration_ms: _Optional[int] = ...) -> None: ...

class EvAssistantTurnFailed(_message.Message):
    __slots__ = ("turn_id", "error")
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    turn_id: str
    error: str
    def __init__(self, turn_id: _Optional[str] = ..., error: _Optional[str] = ...) -> None: ...

class EvConversationAborted(_message.Message):
    __slots__ = ("reason",)
    REASON_FIELD_NUMBER: _ClassVar[int]
    reason: str
    def __init__(self, reason: _Optional[str] = ...) -> None: ...

class EvSessionStatus(_message.Message):
    __slots__ = ("session_id", "headline")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    HEADLINE_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    headline: str
    def __init__(self, session_id: _Optional[str] = ..., headline: _Optional[str] = ...) -> None: ...

class EvSessionTitleChanged(_message.Message):
    __slots__ = ("session_id", "title")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    TITLE_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    title: str
    def __init__(self, session_id: _Optional[str] = ..., title: _Optional[str] = ...) -> None: ...

class EvTodosUpdated(_message.Message):
    __slots__ = ("session_id", "todos_json")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    TODOS_JSON_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    todos_json: str
    def __init__(self, session_id: _Optional[str] = ..., todos_json: _Optional[str] = ...) -> None: ...

class QueuedMessage(_message.Message):
    __slots__ = ("id", "content", "state", "claim_owner", "claim_expires_at_unix", "created_at_unix", "attachments", "turn_options")
    ID_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    CLAIM_OWNER_FIELD_NUMBER: _ClassVar[int]
    CLAIM_EXPIRES_AT_UNIX_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_UNIX_FIELD_NUMBER: _ClassVar[int]
    ATTACHMENTS_FIELD_NUMBER: _ClassVar[int]
    TURN_OPTIONS_FIELD_NUMBER: _ClassVar[int]
    id: str
    content: str
    state: str
    claim_owner: str
    claim_expires_at_unix: int
    created_at_unix: int
    attachments: _containers.RepeatedCompositeFieldContainer[MessageAttachment]
    turn_options: TurnOptions
    def __init__(self, id: _Optional[str] = ..., content: _Optional[str] = ..., state: _Optional[str] = ..., claim_owner: _Optional[str] = ..., claim_expires_at_unix: _Optional[int] = ..., created_at_unix: _Optional[int] = ..., attachments: _Optional[_Iterable[_Union[MessageAttachment, _Mapping]]] = ..., turn_options: _Optional[_Union[TurnOptions, _Mapping]] = ...) -> None: ...

class TurnOptions(_message.Message):
    __slots__ = ("disable_tools", "queue_if_busy")
    DISABLE_TOOLS_FIELD_NUMBER: _ClassVar[int]
    QUEUE_IF_BUSY_FIELD_NUMBER: _ClassVar[int]
    disable_tools: bool
    queue_if_busy: bool
    def __init__(self, disable_tools: _Optional[bool] = ..., queue_if_busy: _Optional[bool] = ...) -> None: ...

class EvTurnQueued(_message.Message):
    __slots__ = ("session_id", "entry")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    ENTRY_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    entry: QueuedMessage
    def __init__(self, session_id: _Optional[str] = ..., entry: _Optional[_Union[QueuedMessage, _Mapping]] = ...) -> None: ...

class EvQueueChanged(_message.Message):
    __slots__ = ("session_id", "queue", "paused")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    QUEUE_FIELD_NUMBER: _ClassVar[int]
    PAUSED_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    queue: _containers.RepeatedCompositeFieldContainer[QueuedMessage]
    paused: bool
    def __init__(self, session_id: _Optional[str] = ..., queue: _Optional[_Iterable[_Union[QueuedMessage, _Mapping]]] = ..., paused: _Optional[bool] = ...) -> None: ...

class EvAttachmentUpserted(_message.Message):
    __slots__ = ("session_id", "attachment_id", "kind", "message_id", "after_tool_call_id", "payload_json")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    ATTACHMENT_ID_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    AFTER_TOOL_CALL_ID_FIELD_NUMBER: _ClassVar[int]
    PAYLOAD_JSON_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    attachment_id: str
    kind: str
    message_id: str
    after_tool_call_id: str
    payload_json: str
    def __init__(self, session_id: _Optional[str] = ..., attachment_id: _Optional[str] = ..., kind: _Optional[str] = ..., message_id: _Optional[str] = ..., after_tool_call_id: _Optional[str] = ..., payload_json: _Optional[str] = ...) -> None: ...

class EvAttachmentDeleted(_message.Message):
    __slots__ = ("session_id", "attachment_id")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    ATTACHMENT_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    attachment_id: str
    def __init__(self, session_id: _Optional[str] = ..., attachment_id: _Optional[str] = ...) -> None: ...

class ConversationStreamFrame(_message.Message):
    __slots__ = ("schema_version", "user_turn_committed", "assistant_turn_started", "assistant_thinking_delta", "assistant_text_delta", "tool_call_stream_started", "tool_call_args_delta", "tool_call_stream_finished", "tool_execution_started", "tool_execution_finished", "assistant_turn_finished", "assistant_turn_failed", "conversation_aborted", "stream_complete", "assistant_activity_snapshot", "session_status", "session_title_changed", "todos_updated", "queue_changed", "attachment_upserted", "attachment_deleted", "turn_queued")
    SCHEMA_VERSION_FIELD_NUMBER: _ClassVar[int]
    USER_TURN_COMMITTED_FIELD_NUMBER: _ClassVar[int]
    ASSISTANT_TURN_STARTED_FIELD_NUMBER: _ClassVar[int]
    ASSISTANT_THINKING_DELTA_FIELD_NUMBER: _ClassVar[int]
    ASSISTANT_TEXT_DELTA_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_STREAM_STARTED_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_ARGS_DELTA_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_STREAM_FINISHED_FIELD_NUMBER: _ClassVar[int]
    TOOL_EXECUTION_STARTED_FIELD_NUMBER: _ClassVar[int]
    TOOL_EXECUTION_FINISHED_FIELD_NUMBER: _ClassVar[int]
    ASSISTANT_TURN_FINISHED_FIELD_NUMBER: _ClassVar[int]
    ASSISTANT_TURN_FAILED_FIELD_NUMBER: _ClassVar[int]
    CONVERSATION_ABORTED_FIELD_NUMBER: _ClassVar[int]
    STREAM_COMPLETE_FIELD_NUMBER: _ClassVar[int]
    ASSISTANT_ACTIVITY_SNAPSHOT_FIELD_NUMBER: _ClassVar[int]
    SESSION_STATUS_FIELD_NUMBER: _ClassVar[int]
    SESSION_TITLE_CHANGED_FIELD_NUMBER: _ClassVar[int]
    TODOS_UPDATED_FIELD_NUMBER: _ClassVar[int]
    QUEUE_CHANGED_FIELD_NUMBER: _ClassVar[int]
    ATTACHMENT_UPSERTED_FIELD_NUMBER: _ClassVar[int]
    ATTACHMENT_DELETED_FIELD_NUMBER: _ClassVar[int]
    TURN_QUEUED_FIELD_NUMBER: _ClassVar[int]
    schema_version: int
    user_turn_committed: EvUserTurnCommitted
    assistant_turn_started: EvAssistantTurnStarted
    assistant_thinking_delta: EvAssistantThinkingDelta
    assistant_text_delta: EvAssistantTextDelta
    tool_call_stream_started: EvToolCallStreamStarted
    tool_call_args_delta: EvToolCallArgsDelta
    tool_call_stream_finished: EvToolCallStreamFinished
    tool_execution_started: EvToolExecutionStarted
    tool_execution_finished: EvToolExecutionFinished
    assistant_turn_finished: EvAssistantTurnFinished
    assistant_turn_failed: EvAssistantTurnFailed
    conversation_aborted: EvConversationAborted
    stream_complete: AssistantReply
    assistant_activity_snapshot: EvAssistantActivitySnapshot
    session_status: EvSessionStatus
    session_title_changed: EvSessionTitleChanged
    todos_updated: EvTodosUpdated
    queue_changed: EvQueueChanged
    attachment_upserted: EvAttachmentUpserted
    attachment_deleted: EvAttachmentDeleted
    turn_queued: EvTurnQueued
    def __init__(self, schema_version: _Optional[int] = ..., user_turn_committed: _Optional[_Union[EvUserTurnCommitted, _Mapping]] = ..., assistant_turn_started: _Optional[_Union[EvAssistantTurnStarted, _Mapping]] = ..., assistant_thinking_delta: _Optional[_Union[EvAssistantThinkingDelta, _Mapping]] = ..., assistant_text_delta: _Optional[_Union[EvAssistantTextDelta, _Mapping]] = ..., tool_call_stream_started: _Optional[_Union[EvToolCallStreamStarted, _Mapping]] = ..., tool_call_args_delta: _Optional[_Union[EvToolCallArgsDelta, _Mapping]] = ..., tool_call_stream_finished: _Optional[_Union[EvToolCallStreamFinished, _Mapping]] = ..., tool_execution_started: _Optional[_Union[EvToolExecutionStarted, _Mapping]] = ..., tool_execution_finished: _Optional[_Union[EvToolExecutionFinished, _Mapping]] = ..., assistant_turn_finished: _Optional[_Union[EvAssistantTurnFinished, _Mapping]] = ..., assistant_turn_failed: _Optional[_Union[EvAssistantTurnFailed, _Mapping]] = ..., conversation_aborted: _Optional[_Union[EvConversationAborted, _Mapping]] = ..., stream_complete: _Optional[_Union[AssistantReply, _Mapping]] = ..., assistant_activity_snapshot: _Optional[_Union[EvAssistantActivitySnapshot, _Mapping]] = ..., session_status: _Optional[_Union[EvSessionStatus, _Mapping]] = ..., session_title_changed: _Optional[_Union[EvSessionTitleChanged, _Mapping]] = ..., todos_updated: _Optional[_Union[EvTodosUpdated, _Mapping]] = ..., queue_changed: _Optional[_Union[EvQueueChanged, _Mapping]] = ..., attachment_upserted: _Optional[_Union[EvAttachmentUpserted, _Mapping]] = ..., attachment_deleted: _Optional[_Union[EvAttachmentDeleted, _Mapping]] = ..., turn_queued: _Optional[_Union[EvTurnQueued, _Mapping]] = ...) -> None: ...

class UpsertSessionDraftRequest(_message.Message):
    __slots__ = ("session_id", "content")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    content: str
    def __init__(self, session_id: _Optional[str] = ..., content: _Optional[str] = ...) -> None: ...

class UpsertSessionDraftResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class GetSessionDraftRequest(_message.Message):
    __slots__ = ("session_id",)
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    def __init__(self, session_id: _Optional[str] = ...) -> None: ...

class GetSessionDraftResponse(_message.Message):
    __slots__ = ("content",)
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    content: str
    def __init__(self, content: _Optional[str] = ...) -> None: ...

class ClearSessionDraftRequest(_message.Message):
    __slots__ = ("session_id",)
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    def __init__(self, session_id: _Optional[str] = ...) -> None: ...

class ClearSessionDraftResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class EnqueueMessageRequest(_message.Message):
    __slots__ = ("session_id", "content", "attachments", "turn_options")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    ATTACHMENTS_FIELD_NUMBER: _ClassVar[int]
    TURN_OPTIONS_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    content: str
    attachments: _containers.RepeatedCompositeFieldContainer[MessageAttachment]
    turn_options: TurnOptions
    def __init__(self, session_id: _Optional[str] = ..., content: _Optional[str] = ..., attachments: _Optional[_Iterable[_Union[MessageAttachment, _Mapping]]] = ..., turn_options: _Optional[_Union[TurnOptions, _Mapping]] = ...) -> None: ...

class EnqueueMessageResponse(_message.Message):
    __slots__ = ("entry",)
    ENTRY_FIELD_NUMBER: _ClassVar[int]
    entry: QueuedMessage
    def __init__(self, entry: _Optional[_Union[QueuedMessage, _Mapping]] = ...) -> None: ...

class AnswerAskCardRequest(_message.Message):
    __slots__ = ("session_id", "card_id", "response_json", "continue_with_tools")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    CARD_ID_FIELD_NUMBER: _ClassVar[int]
    RESPONSE_JSON_FIELD_NUMBER: _ClassVar[int]
    CONTINUE_WITH_TOOLS_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    card_id: str
    response_json: str
    continue_with_tools: bool
    def __init__(self, session_id: _Optional[str] = ..., card_id: _Optional[str] = ..., response_json: _Optional[str] = ..., continue_with_tools: _Optional[bool] = ...) -> None: ...

class AnswerAskCardResponse(_message.Message):
    __slots__ = ("entry", "updated_existing")
    ENTRY_FIELD_NUMBER: _ClassVar[int]
    UPDATED_EXISTING_FIELD_NUMBER: _ClassVar[int]
    entry: QueuedMessage
    updated_existing: bool
    def __init__(self, entry: _Optional[_Union[QueuedMessage, _Mapping]] = ..., updated_existing: _Optional[bool] = ...) -> None: ...

class OpenAskCard(_message.Message):
    __slots__ = ("scope_id", "session_id", "card_id", "payload_json", "created_at_ms")
    SCOPE_ID_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    CARD_ID_FIELD_NUMBER: _ClassVar[int]
    PAYLOAD_JSON_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_MS_FIELD_NUMBER: _ClassVar[int]
    scope_id: str
    session_id: str
    card_id: str
    payload_json: str
    created_at_ms: int
    def __init__(self, scope_id: _Optional[str] = ..., session_id: _Optional[str] = ..., card_id: _Optional[str] = ..., payload_json: _Optional[str] = ..., created_at_ms: _Optional[int] = ...) -> None: ...

class ListOpenAskCardsRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListOpenAskCardsResponse(_message.Message):
    __slots__ = ("cards",)
    CARDS_FIELD_NUMBER: _ClassVar[int]
    cards: _containers.RepeatedCompositeFieldContainer[OpenAskCard]
    def __init__(self, cards: _Optional[_Iterable[_Union[OpenAskCard, _Mapping]]] = ...) -> None: ...

class SubscribeAskCardsRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class AskCardChange(_message.Message):
    __slots__ = ("card", "open")
    CARD_FIELD_NUMBER: _ClassVar[int]
    OPEN_FIELD_NUMBER: _ClassVar[int]
    card: OpenAskCard
    open: bool
    def __init__(self, card: _Optional[_Union[OpenAskCard, _Mapping]] = ..., open: _Optional[bool] = ...) -> None: ...

class ListQueueRequest(_message.Message):
    __slots__ = ("session_id",)
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    def __init__(self, session_id: _Optional[str] = ...) -> None: ...

class ListQueueResponse(_message.Message):
    __slots__ = ("queue", "paused")
    QUEUE_FIELD_NUMBER: _ClassVar[int]
    PAUSED_FIELD_NUMBER: _ClassVar[int]
    queue: _containers.RepeatedCompositeFieldContainer[QueuedMessage]
    paused: bool
    def __init__(self, queue: _Optional[_Iterable[_Union[QueuedMessage, _Mapping]]] = ..., paused: _Optional[bool] = ...) -> None: ...

class RemoveQueuedMessageRequest(_message.Message):
    __slots__ = ("session_id", "entry_id")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    ENTRY_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    entry_id: str
    def __init__(self, session_id: _Optional[str] = ..., entry_id: _Optional[str] = ...) -> None: ...

class RemoveQueuedMessageResponse(_message.Message):
    __slots__ = ("removed",)
    REMOVED_FIELD_NUMBER: _ClassVar[int]
    removed: bool
    def __init__(self, removed: _Optional[bool] = ...) -> None: ...

class ReorderQueueRequest(_message.Message):
    __slots__ = ("session_id", "entry_ids")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    ENTRY_IDS_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    entry_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, session_id: _Optional[str] = ..., entry_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class ReorderQueueResponse(_message.Message):
    __slots__ = ("queue",)
    QUEUE_FIELD_NUMBER: _ClassVar[int]
    queue: _containers.RepeatedCompositeFieldContainer[QueuedMessage]
    def __init__(self, queue: _Optional[_Iterable[_Union[QueuedMessage, _Mapping]]] = ...) -> None: ...

class SetQueuePausedRequest(_message.Message):
    __slots__ = ("session_id", "paused")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    PAUSED_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    paused: bool
    def __init__(self, session_id: _Optional[str] = ..., paused: _Optional[bool] = ...) -> None: ...

class SetQueuePausedResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ClaimNextQueuedMessageRequest(_message.Message):
    __slots__ = ("session_id", "owner")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    OWNER_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    owner: str
    def __init__(self, session_id: _Optional[str] = ..., owner: _Optional[str] = ...) -> None: ...

class ClaimNextQueuedMessageResponse(_message.Message):
    __slots__ = ("entry", "paused")
    ENTRY_FIELD_NUMBER: _ClassVar[int]
    PAUSED_FIELD_NUMBER: _ClassVar[int]
    entry: QueuedMessage
    paused: bool
    def __init__(self, entry: _Optional[_Union[QueuedMessage, _Mapping]] = ..., paused: _Optional[bool] = ...) -> None: ...

class HeartbeatQueuedMessageRequest(_message.Message):
    __slots__ = ("session_id", "entry_id", "owner")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    ENTRY_ID_FIELD_NUMBER: _ClassVar[int]
    OWNER_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    entry_id: str
    owner: str
    def __init__(self, session_id: _Optional[str] = ..., entry_id: _Optional[str] = ..., owner: _Optional[str] = ...) -> None: ...

class HeartbeatQueuedMessageResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class MarkQueuedMessageStartedRequest(_message.Message):
    __slots__ = ("session_id", "entry_id", "owner", "turn_id")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    ENTRY_ID_FIELD_NUMBER: _ClassVar[int]
    OWNER_FIELD_NUMBER: _ClassVar[int]
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    entry_id: str
    owner: str
    turn_id: str
    def __init__(self, session_id: _Optional[str] = ..., entry_id: _Optional[str] = ..., owner: _Optional[str] = ..., turn_id: _Optional[str] = ...) -> None: ...

class MarkQueuedMessageStartedResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class RetireQueuedMessageRequest(_message.Message):
    __slots__ = ("session_id", "entry_id", "owner")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    ENTRY_ID_FIELD_NUMBER: _ClassVar[int]
    OWNER_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    entry_id: str
    owner: str
    def __init__(self, session_id: _Optional[str] = ..., entry_id: _Optional[str] = ..., owner: _Optional[str] = ...) -> None: ...

class RetireQueuedMessageResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ReleaseQueuedMessageRequest(_message.Message):
    __slots__ = ("session_id", "entry_id", "owner")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    ENTRY_ID_FIELD_NUMBER: _ClassVar[int]
    OWNER_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    entry_id: str
    owner: str
    def __init__(self, session_id: _Optional[str] = ..., entry_id: _Optional[str] = ..., owner: _Optional[str] = ...) -> None: ...

class ReleaseQueuedMessageResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ChatStreamRequest(_message.Message):
    __slots__ = ("messages", "options", "session_id", "persist_session", "system_prompt_sections", "tracked_documents", "initial_seed", "initial_seed_graph_paths", "turn_options", "tool_context")
    class ToolContextEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    MESSAGES_FIELD_NUMBER: _ClassVar[int]
    OPTIONS_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    PERSIST_SESSION_FIELD_NUMBER: _ClassVar[int]
    SYSTEM_PROMPT_SECTIONS_FIELD_NUMBER: _ClassVar[int]
    TRACKED_DOCUMENTS_FIELD_NUMBER: _ClassVar[int]
    INITIAL_SEED_FIELD_NUMBER: _ClassVar[int]
    INITIAL_SEED_GRAPH_PATHS_FIELD_NUMBER: _ClassVar[int]
    TURN_OPTIONS_FIELD_NUMBER: _ClassVar[int]
    TOOL_CONTEXT_FIELD_NUMBER: _ClassVar[int]
    messages: _containers.RepeatedCompositeFieldContainer[Message]
    options: CallOptions
    session_id: str
    persist_session: bool
    system_prompt_sections: _containers.RepeatedCompositeFieldContainer[SystemPromptSection]
    tracked_documents: _containers.RepeatedCompositeFieldContainer[TrackedDocument]
    initial_seed: str
    initial_seed_graph_paths: _containers.RepeatedScalarFieldContainer[str]
    turn_options: TurnOptions
    tool_context: _containers.ScalarMap[str, str]
    def __init__(self, messages: _Optional[_Iterable[_Union[Message, _Mapping]]] = ..., options: _Optional[_Union[CallOptions, _Mapping]] = ..., session_id: _Optional[str] = ..., persist_session: _Optional[bool] = ..., system_prompt_sections: _Optional[_Iterable[_Union[SystemPromptSection, _Mapping]]] = ..., tracked_documents: _Optional[_Iterable[_Union[TrackedDocument, _Mapping]]] = ..., initial_seed: _Optional[str] = ..., initial_seed_graph_paths: _Optional[_Iterable[str]] = ..., turn_options: _Optional[_Union[TurnOptions, _Mapping]] = ..., tool_context: _Optional[_Mapping[str, str]] = ...) -> None: ...

class SystemPromptSection(_message.Message):
    __slots__ = ("id", "content", "tags")
    ID_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    id: str
    content: str
    tags: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, id: _Optional[str] = ..., content: _Optional[str] = ..., tags: _Optional[_Iterable[str]] = ...) -> None: ...

class SubmitToolResultsRequest(_message.Message):
    __slots__ = ("session_id", "client_wait_nonce", "tool_call_id", "content", "is_error", "ends_turn")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    CLIENT_WAIT_NONCE_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_ID_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    IS_ERROR_FIELD_NUMBER: _ClassVar[int]
    ENDS_TURN_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    client_wait_nonce: str
    tool_call_id: str
    content: str
    is_error: bool
    ends_turn: bool
    def __init__(self, session_id: _Optional[str] = ..., client_wait_nonce: _Optional[str] = ..., tool_call_id: _Optional[str] = ..., content: _Optional[str] = ..., is_error: _Optional[bool] = ..., ends_turn: _Optional[bool] = ...) -> None: ...

class SubmitToolResultsResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class GetSessionTokenUsageRequest(_message.Message):
    __slots__ = ("session_id",)
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    def __init__(self, session_id: _Optional[str] = ...) -> None: ...

class GetSessionTokenUsageResponse(_message.Message):
    __slots__ = ("total_provider_in_toks", "total_provider_out_toks", "total_original_tokens", "total_compressed_tokens")
    TOTAL_PROVIDER_IN_TOKS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_PROVIDER_OUT_TOKS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_ORIGINAL_TOKENS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_COMPRESSED_TOKENS_FIELD_NUMBER: _ClassVar[int]
    total_provider_in_toks: int
    total_provider_out_toks: int
    total_original_tokens: int
    total_compressed_tokens: int
    def __init__(self, total_provider_in_toks: _Optional[int] = ..., total_provider_out_toks: _Optional[int] = ..., total_original_tokens: _Optional[int] = ..., total_compressed_tokens: _Optional[int] = ...) -> None: ...

class TurnDecoration(_message.Message):
    __slots__ = ("turn_seq", "turn_id", "target", "tool_call_id", "kind", "uri", "created_at_unix")
    TURN_SEQ_FIELD_NUMBER: _ClassVar[int]
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    TARGET_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_ID_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    URI_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_UNIX_FIELD_NUMBER: _ClassVar[int]
    turn_seq: int
    turn_id: str
    target: str
    tool_call_id: str
    kind: str
    uri: str
    created_at_unix: int
    def __init__(self, turn_seq: _Optional[int] = ..., turn_id: _Optional[str] = ..., target: _Optional[str] = ..., tool_call_id: _Optional[str] = ..., kind: _Optional[str] = ..., uri: _Optional[str] = ..., created_at_unix: _Optional[int] = ...) -> None: ...

class ListTurnDecorationsRequest(_message.Message):
    __slots__ = ("session_id",)
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    def __init__(self, session_id: _Optional[str] = ...) -> None: ...

class ListTurnDecorationsResponse(_message.Message):
    __slots__ = ("decorations",)
    DECORATIONS_FIELD_NUMBER: _ClassVar[int]
    decorations: _containers.RepeatedCompositeFieldContainer[TurnDecoration]
    def __init__(self, decorations: _Optional[_Iterable[_Union[TurnDecoration, _Mapping]]] = ...) -> None: ...

class TurnUsageEntry(_message.Message):
    __slots__ = ("message_id", "input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "original_tokens", "compressed_tokens", "turn_seq", "model_duration_ms")
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    INPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    CACHE_READ_TOKENS_FIELD_NUMBER: _ClassVar[int]
    CACHE_WRITE_TOKENS_FIELD_NUMBER: _ClassVar[int]
    ORIGINAL_TOKENS_FIELD_NUMBER: _ClassVar[int]
    COMPRESSED_TOKENS_FIELD_NUMBER: _ClassVar[int]
    TURN_SEQ_FIELD_NUMBER: _ClassVar[int]
    MODEL_DURATION_MS_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    input_tokens: int
    output_tokens: int
    cache_read_tokens: int
    cache_write_tokens: int
    original_tokens: int
    compressed_tokens: int
    turn_seq: int
    model_duration_ms: int
    def __init__(self, message_id: _Optional[str] = ..., input_tokens: _Optional[int] = ..., output_tokens: _Optional[int] = ..., cache_read_tokens: _Optional[int] = ..., cache_write_tokens: _Optional[int] = ..., original_tokens: _Optional[int] = ..., compressed_tokens: _Optional[int] = ..., turn_seq: _Optional[int] = ..., model_duration_ms: _Optional[int] = ...) -> None: ...

class GetSessionTurnBreakdownRequest(_message.Message):
    __slots__ = ("session_id",)
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    def __init__(self, session_id: _Optional[str] = ...) -> None: ...

class GetSessionTurnBreakdownResponse(_message.Message):
    __slots__ = ("turns", "total_input_tokens", "total_output_tokens", "total_cache_read_tokens", "total_cache_write_tokens")
    TURNS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_INPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_OUTPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_CACHE_READ_TOKENS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_CACHE_WRITE_TOKENS_FIELD_NUMBER: _ClassVar[int]
    turns: _containers.RepeatedCompositeFieldContainer[TurnUsageEntry]
    total_input_tokens: int
    total_output_tokens: int
    total_cache_read_tokens: int
    total_cache_write_tokens: int
    def __init__(self, turns: _Optional[_Iterable[_Union[TurnUsageEntry, _Mapping]]] = ..., total_input_tokens: _Optional[int] = ..., total_output_tokens: _Optional[int] = ..., total_cache_read_tokens: _Optional[int] = ..., total_cache_write_tokens: _Optional[int] = ...) -> None: ...

class GetScopeTokenMetricsRequest(_message.Message):
    __slots__ = ("all_scopes",)
    ALL_SCOPES_FIELD_NUMBER: _ClassVar[int]
    all_scopes: bool
    def __init__(self, all_scopes: _Optional[bool] = ...) -> None: ...

class GetScopeTokenMetricsResponse(_message.Message):
    __slots__ = ("total_provider_in_toks", "total_provider_out_toks", "total_original_tokens", "total_compressed_tokens", "compressed_result_count", "total_cache_read_toks", "total_cache_write_toks")
    TOTAL_PROVIDER_IN_TOKS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_PROVIDER_OUT_TOKS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_ORIGINAL_TOKENS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_COMPRESSED_TOKENS_FIELD_NUMBER: _ClassVar[int]
    COMPRESSED_RESULT_COUNT_FIELD_NUMBER: _ClassVar[int]
    TOTAL_CACHE_READ_TOKS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_CACHE_WRITE_TOKS_FIELD_NUMBER: _ClassVar[int]
    total_provider_in_toks: int
    total_provider_out_toks: int
    total_original_tokens: int
    total_compressed_tokens: int
    compressed_result_count: int
    total_cache_read_toks: int
    total_cache_write_toks: int
    def __init__(self, total_provider_in_toks: _Optional[int] = ..., total_provider_out_toks: _Optional[int] = ..., total_original_tokens: _Optional[int] = ..., total_compressed_tokens: _Optional[int] = ..., compressed_result_count: _Optional[int] = ..., total_cache_read_toks: _Optional[int] = ..., total_cache_write_toks: _Optional[int] = ...) -> None: ...

class TokenUsageBucket(_message.Message):
    __slots__ = ("bucket_unix", "provider_in_toks", "provider_out_toks", "cache_read_toks", "cache_write_toks")
    BUCKET_UNIX_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_IN_TOKS_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_OUT_TOKS_FIELD_NUMBER: _ClassVar[int]
    CACHE_READ_TOKS_FIELD_NUMBER: _ClassVar[int]
    CACHE_WRITE_TOKS_FIELD_NUMBER: _ClassVar[int]
    bucket_unix: int
    provider_in_toks: int
    provider_out_toks: int
    cache_read_toks: int
    cache_write_toks: int
    def __init__(self, bucket_unix: _Optional[int] = ..., provider_in_toks: _Optional[int] = ..., provider_out_toks: _Optional[int] = ..., cache_read_toks: _Optional[int] = ..., cache_write_toks: _Optional[int] = ...) -> None: ...

class GetTokenUsageTimelineRequest(_message.Message):
    __slots__ = ("all_scopes",)
    ALL_SCOPES_FIELD_NUMBER: _ClassVar[int]
    all_scopes: bool
    def __init__(self, all_scopes: _Optional[bool] = ...) -> None: ...

class GetTokenUsageTimelineResponse(_message.Message):
    __slots__ = ("buckets",)
    BUCKETS_FIELD_NUMBER: _ClassVar[int]
    buckets: _containers.RepeatedCompositeFieldContainer[TokenUsageBucket]
    def __init__(self, buckets: _Optional[_Iterable[_Union[TokenUsageBucket, _Mapping]]] = ...) -> None: ...

class TokenSavingsBucket(_message.Message):
    __slots__ = ("day_unix", "original_tokens", "compressed_tokens")
    DAY_UNIX_FIELD_NUMBER: _ClassVar[int]
    ORIGINAL_TOKENS_FIELD_NUMBER: _ClassVar[int]
    COMPRESSED_TOKENS_FIELD_NUMBER: _ClassVar[int]
    day_unix: int
    original_tokens: int
    compressed_tokens: int
    def __init__(self, day_unix: _Optional[int] = ..., original_tokens: _Optional[int] = ..., compressed_tokens: _Optional[int] = ...) -> None: ...

class GetTokenSavingsTimelineRequest(_message.Message):
    __slots__ = ("all_scopes",)
    ALL_SCOPES_FIELD_NUMBER: _ClassVar[int]
    all_scopes: bool
    def __init__(self, all_scopes: _Optional[bool] = ...) -> None: ...

class GetTokenSavingsTimelineResponse(_message.Message):
    __slots__ = ("buckets",)
    BUCKETS_FIELD_NUMBER: _ClassVar[int]
    buckets: _containers.RepeatedCompositeFieldContainer[TokenSavingsBucket]
    def __init__(self, buckets: _Optional[_Iterable[_Union[TokenSavingsBucket, _Mapping]]] = ...) -> None: ...

class GetToolResultOriginalRequest(_message.Message):
    __slots__ = ("tool_call_id", "session_id")
    TOOL_CALL_ID_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    tool_call_id: str
    session_id: str
    def __init__(self, tool_call_id: _Optional[str] = ..., session_id: _Optional[str] = ...) -> None: ...

class GetToolResultOriginalResponse(_message.Message):
    __slots__ = ("original_content", "found")
    ORIGINAL_CONTENT_FIELD_NUMBER: _ClassVar[int]
    FOUND_FIELD_NUMBER: _ClassVar[int]
    original_content: str
    found: bool
    def __init__(self, original_content: _Optional[str] = ..., found: _Optional[bool] = ...) -> None: ...

class UpdateSessionCompressionSettingsRequest(_message.Message):
    __slots__ = ("session_id", "enabled", "threshold", "age_keep", "extractive_search", "minify_dedup")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    THRESHOLD_FIELD_NUMBER: _ClassVar[int]
    AGE_KEEP_FIELD_NUMBER: _ClassVar[int]
    EXTRACTIVE_SEARCH_FIELD_NUMBER: _ClassVar[int]
    MINIFY_DEDUP_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    enabled: bool
    threshold: int
    age_keep: int
    extractive_search: bool
    minify_dedup: bool
    def __init__(self, session_id: _Optional[str] = ..., enabled: _Optional[bool] = ..., threshold: _Optional[int] = ..., age_keep: _Optional[int] = ..., extractive_search: _Optional[bool] = ..., minify_dedup: _Optional[bool] = ...) -> None: ...

class UpdateSessionCompressionSettingsResponse(_message.Message):
    __slots__ = ("success", "enabled", "threshold", "age_keep", "extractive_search", "minify_dedup")
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    THRESHOLD_FIELD_NUMBER: _ClassVar[int]
    AGE_KEEP_FIELD_NUMBER: _ClassVar[int]
    EXTRACTIVE_SEARCH_FIELD_NUMBER: _ClassVar[int]
    MINIFY_DEDUP_FIELD_NUMBER: _ClassVar[int]
    success: bool
    enabled: bool
    threshold: int
    age_keep: int
    extractive_search: bool
    minify_dedup: bool
    def __init__(self, success: _Optional[bool] = ..., enabled: _Optional[bool] = ..., threshold: _Optional[int] = ..., age_keep: _Optional[int] = ..., extractive_search: _Optional[bool] = ..., minify_dedup: _Optional[bool] = ...) -> None: ...

class TodoItem(_message.Message):
    __slots__ = ("id", "title", "completed_at")
    ID_FIELD_NUMBER: _ClassVar[int]
    TITLE_FIELD_NUMBER: _ClassVar[int]
    COMPLETED_AT_FIELD_NUMBER: _ClassVar[int]
    id: str
    title: str
    completed_at: int
    def __init__(self, id: _Optional[str] = ..., title: _Optional[str] = ..., completed_at: _Optional[int] = ...) -> None: ...

class SessionMeta(_message.Message):
    __slots__ = ("title", "todos_json", "attributes_json", "cursor_pos", "session_id", "chat_surface", "created_at", "updated_at", "provider_id", "default_model", "tags", "forked_from_session_id", "forked_from_message_id", "status")
    class TagsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    TITLE_FIELD_NUMBER: _ClassVar[int]
    TODOS_JSON_FIELD_NUMBER: _ClassVar[int]
    ATTRIBUTES_JSON_FIELD_NUMBER: _ClassVar[int]
    CURSOR_POS_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    CHAT_SURFACE_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_MODEL_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    FORKED_FROM_SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    FORKED_FROM_MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    title: str
    todos_json: str
    attributes_json: str
    cursor_pos: int
    session_id: str
    chat_surface: str
    created_at: int
    updated_at: int
    provider_id: str
    default_model: str
    tags: _containers.ScalarMap[str, str]
    forked_from_session_id: str
    forked_from_message_id: str
    status: str
    def __init__(self, title: _Optional[str] = ..., todos_json: _Optional[str] = ..., attributes_json: _Optional[str] = ..., cursor_pos: _Optional[int] = ..., session_id: _Optional[str] = ..., chat_surface: _Optional[str] = ..., created_at: _Optional[int] = ..., updated_at: _Optional[int] = ..., provider_id: _Optional[str] = ..., default_model: _Optional[str] = ..., tags: _Optional[_Mapping[str, str]] = ..., forked_from_session_id: _Optional[str] = ..., forked_from_message_id: _Optional[str] = ..., status: _Optional[str] = ...) -> None: ...

class SessionSummary(_message.Message):
    __slots__ = ("session_id", "title", "chat_surface", "created_at", "updated_at", "cursor_pos", "last_message_at", "tags", "forked_from_session_id", "forked_from_message_id")
    class TagsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    TITLE_FIELD_NUMBER: _ClassVar[int]
    CHAT_SURFACE_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    CURSOR_POS_FIELD_NUMBER: _ClassVar[int]
    LAST_MESSAGE_AT_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    FORKED_FROM_SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    FORKED_FROM_MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    title: str
    chat_surface: str
    created_at: int
    updated_at: int
    cursor_pos: int
    last_message_at: int
    tags: _containers.ScalarMap[str, str]
    forked_from_session_id: str
    forked_from_message_id: str
    def __init__(self, session_id: _Optional[str] = ..., title: _Optional[str] = ..., chat_surface: _Optional[str] = ..., created_at: _Optional[int] = ..., updated_at: _Optional[int] = ..., cursor_pos: _Optional[int] = ..., last_message_at: _Optional[int] = ..., tags: _Optional[_Mapping[str, str]] = ..., forked_from_session_id: _Optional[str] = ..., forked_from_message_id: _Optional[str] = ...) -> None: ...

class GetSessionRequest(_message.Message):
    __slots__ = ("session_id",)
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    def __init__(self, session_id: _Optional[str] = ...) -> None: ...

class GetSessionResponse(_message.Message):
    __slots__ = ("meta",)
    META_FIELD_NUMBER: _ClassVar[int]
    meta: SessionMeta
    def __init__(self, meta: _Optional[_Union[SessionMeta, _Mapping]] = ...) -> None: ...

class SetSessionMetaRequest(_message.Message):
    __slots__ = ("session_id", "meta")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    META_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    meta: SessionMeta
    def __init__(self, session_id: _Optional[str] = ..., meta: _Optional[_Union[SessionMeta, _Mapping]] = ...) -> None: ...

class SetSessionMetaResponse(_message.Message):
    __slots__ = ("success",)
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    success: bool
    def __init__(self, success: _Optional[bool] = ...) -> None: ...

class ListSessionsRequest(_message.Message):
    __slots__ = ("sort_by", "order", "page_size", "offset", "chat_surface", "tag_filters")
    class TagFiltersEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    SORT_BY_FIELD_NUMBER: _ClassVar[int]
    ORDER_FIELD_NUMBER: _ClassVar[int]
    PAGE_SIZE_FIELD_NUMBER: _ClassVar[int]
    OFFSET_FIELD_NUMBER: _ClassVar[int]
    CHAT_SURFACE_FIELD_NUMBER: _ClassVar[int]
    TAG_FILTERS_FIELD_NUMBER: _ClassVar[int]
    sort_by: SessionSortField
    order: SessionSortOrder
    page_size: int
    offset: int
    chat_surface: str
    tag_filters: _containers.ScalarMap[str, str]
    def __init__(self, sort_by: _Optional[_Union[SessionSortField, str]] = ..., order: _Optional[_Union[SessionSortOrder, str]] = ..., page_size: _Optional[int] = ..., offset: _Optional[int] = ..., chat_surface: _Optional[str] = ..., tag_filters: _Optional[_Mapping[str, str]] = ...) -> None: ...

class ListSessionsResponse(_message.Message):
    __slots__ = ("sessions", "total_count", "next_offset", "has_more")
    SESSIONS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_COUNT_FIELD_NUMBER: _ClassVar[int]
    NEXT_OFFSET_FIELD_NUMBER: _ClassVar[int]
    HAS_MORE_FIELD_NUMBER: _ClassVar[int]
    sessions: _containers.RepeatedCompositeFieldContainer[SessionSummary]
    total_count: int
    next_offset: int
    has_more: bool
    def __init__(self, sessions: _Optional[_Iterable[_Union[SessionSummary, _Mapping]]] = ..., total_count: _Optional[int] = ..., next_offset: _Optional[int] = ..., has_more: _Optional[bool] = ...) -> None: ...

class UpdateSessionTagsRequest(_message.Message):
    __slots__ = ("session_id", "tags", "replace")
    class TagsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    REPLACE_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    tags: _containers.ScalarMap[str, str]
    replace: bool
    def __init__(self, session_id: _Optional[str] = ..., tags: _Optional[_Mapping[str, str]] = ..., replace: _Optional[bool] = ...) -> None: ...

class UpdateSessionTagsResponse(_message.Message):
    __slots__ = ("tags",)
    class TagsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    TAGS_FIELD_NUMBER: _ClassVar[int]
    tags: _containers.ScalarMap[str, str]
    def __init__(self, tags: _Optional[_Mapping[str, str]] = ...) -> None: ...

class DeleteSessionRequest(_message.Message):
    __slots__ = ("session_id",)
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    def __init__(self, session_id: _Optional[str] = ...) -> None: ...

class DeleteSessionResponse(_message.Message):
    __slots__ = ("success",)
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    success: bool
    def __init__(self, success: _Optional[bool] = ...) -> None: ...

class ForkSessionRequest(_message.Message):
    __slots__ = ("source_session_id", "up_to_message_id", "new_session_id", "title")
    SOURCE_SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    UP_TO_MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    NEW_SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    TITLE_FIELD_NUMBER: _ClassVar[int]
    source_session_id: str
    up_to_message_id: str
    new_session_id: str
    title: str
    def __init__(self, source_session_id: _Optional[str] = ..., up_to_message_id: _Optional[str] = ..., new_session_id: _Optional[str] = ..., title: _Optional[str] = ...) -> None: ...

class ForkSessionResponse(_message.Message):
    __slots__ = ("session_id", "copied_messages")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    COPIED_MESSAGES_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    copied_messages: int
    def __init__(self, session_id: _Optional[str] = ..., copied_messages: _Optional[int] = ...) -> None: ...

class SetSessionCursorRequest(_message.Message):
    __slots__ = ("session_id", "cursor_pos")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    CURSOR_POS_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    cursor_pos: int
    def __init__(self, session_id: _Optional[str] = ..., cursor_pos: _Optional[int] = ...) -> None: ...

class SetSessionCursorResponse(_message.Message):
    __slots__ = ("success",)
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    success: bool
    def __init__(self, success: _Optional[bool] = ...) -> None: ...

class AdvanceSessionCursorRequest(_message.Message):
    __slots__ = ("session_id", "delta")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    DELTA_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    delta: int
    def __init__(self, session_id: _Optional[str] = ..., delta: _Optional[int] = ...) -> None: ...

class AdvanceSessionCursorResponse(_message.Message):
    __slots__ = ("new_cursor_pos",)
    NEW_CURSOR_POS_FIELD_NUMBER: _ClassVar[int]
    new_cursor_pos: int
    def __init__(self, new_cursor_pos: _Optional[int] = ...) -> None: ...

class ToolProviderHello(_message.Message):
    __slots__ = ("provider_id", "shared_secret")
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    SHARED_SECRET_FIELD_NUMBER: _ClassVar[int]
    provider_id: str
    shared_secret: str
    def __init__(self, provider_id: _Optional[str] = ..., shared_secret: _Optional[str] = ...) -> None: ...

class ToolExecutionInvoke(_message.Message):
    __slots__ = ("execution_id", "session_id", "tool_id", "arguments_json", "client_session_id")
    EXECUTION_ID_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    TOOL_ID_FIELD_NUMBER: _ClassVar[int]
    ARGUMENTS_JSON_FIELD_NUMBER: _ClassVar[int]
    CLIENT_SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    execution_id: str
    session_id: str
    tool_id: str
    arguments_json: str
    client_session_id: str
    def __init__(self, execution_id: _Optional[str] = ..., session_id: _Optional[str] = ..., tool_id: _Optional[str] = ..., arguments_json: _Optional[str] = ..., client_session_id: _Optional[str] = ...) -> None: ...

class ToolResultAttachment(_message.Message):
    __slots__ = ("kind", "uri")
    KIND_FIELD_NUMBER: _ClassVar[int]
    URI_FIELD_NUMBER: _ClassVar[int]
    kind: str
    uri: str
    def __init__(self, kind: _Optional[str] = ..., uri: _Optional[str] = ...) -> None: ...

class ToolExecutionResult(_message.Message):
    __slots__ = ("execution_id", "tool_id", "content", "is_error", "dedup_key", "attachments", "ends_turn")
    EXECUTION_ID_FIELD_NUMBER: _ClassVar[int]
    TOOL_ID_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    IS_ERROR_FIELD_NUMBER: _ClassVar[int]
    DEDUP_KEY_FIELD_NUMBER: _ClassVar[int]
    ATTACHMENTS_FIELD_NUMBER: _ClassVar[int]
    ENDS_TURN_FIELD_NUMBER: _ClassVar[int]
    execution_id: str
    tool_id: str
    content: str
    is_error: bool
    dedup_key: str
    attachments: _containers.RepeatedCompositeFieldContainer[ToolResultAttachment]
    ends_turn: bool
    def __init__(self, execution_id: _Optional[str] = ..., tool_id: _Optional[str] = ..., content: _Optional[str] = ..., is_error: _Optional[bool] = ..., dedup_key: _Optional[str] = ..., attachments: _Optional[_Iterable[_Union[ToolResultAttachment, _Mapping]]] = ..., ends_turn: _Optional[bool] = ...) -> None: ...

class ExecutorHello(_message.Message):
    __slots__ = ("executor_id", "tools", "max_concurrency", "version", "instance_id")
    EXECUTOR_ID_FIELD_NUMBER: _ClassVar[int]
    TOOLS_FIELD_NUMBER: _ClassVar[int]
    MAX_CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    INSTANCE_ID_FIELD_NUMBER: _ClassVar[int]
    executor_id: str
    tools: _containers.RepeatedCompositeFieldContainer[ToolDefinition]
    max_concurrency: int
    version: str
    instance_id: str
    def __init__(self, executor_id: _Optional[str] = ..., tools: _Optional[_Iterable[_Union[ToolDefinition, _Mapping]]] = ..., max_concurrency: _Optional[int] = ..., version: _Optional[str] = ..., instance_id: _Optional[str] = ...) -> None: ...

class RejectedTool(_message.Message):
    __slots__ = ("name", "reason")
    NAME_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    name: str
    reason: str
    def __init__(self, name: _Optional[str] = ..., reason: _Optional[str] = ...) -> None: ...

class ExecutorHelloAck(_message.Message):
    __slots__ = ("executor_id", "accepted_tools", "rejected_tools", "max_result_bytes")
    EXECUTOR_ID_FIELD_NUMBER: _ClassVar[int]
    ACCEPTED_TOOLS_FIELD_NUMBER: _ClassVar[int]
    REJECTED_TOOLS_FIELD_NUMBER: _ClassVar[int]
    MAX_RESULT_BYTES_FIELD_NUMBER: _ClassVar[int]
    executor_id: str
    accepted_tools: _containers.RepeatedScalarFieldContainer[str]
    rejected_tools: _containers.RepeatedCompositeFieldContainer[RejectedTool]
    max_result_bytes: int
    def __init__(self, executor_id: _Optional[str] = ..., accepted_tools: _Optional[_Iterable[str]] = ..., rejected_tools: _Optional[_Iterable[_Union[RejectedTool, _Mapping]]] = ..., max_result_bytes: _Optional[int] = ...) -> None: ...

class ExecutorToolCall(_message.Message):
    __slots__ = ("call_id", "tool_name", "arguments_json", "session_id", "tenant_id", "origin", "max_duration_seconds", "scope_id", "turn_id", "tool_context")
    class ToolContextEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    CALL_ID_FIELD_NUMBER: _ClassVar[int]
    TOOL_NAME_FIELD_NUMBER: _ClassVar[int]
    ARGUMENTS_JSON_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    ORIGIN_FIELD_NUMBER: _ClassVar[int]
    MAX_DURATION_SECONDS_FIELD_NUMBER: _ClassVar[int]
    SCOPE_ID_FIELD_NUMBER: _ClassVar[int]
    TURN_ID_FIELD_NUMBER: _ClassVar[int]
    TOOL_CONTEXT_FIELD_NUMBER: _ClassVar[int]
    call_id: str
    tool_name: str
    arguments_json: str
    session_id: str
    tenant_id: str
    origin: ToolCallOrigin
    max_duration_seconds: int
    scope_id: str
    turn_id: str
    tool_context: _containers.ScalarMap[str, str]
    def __init__(self, call_id: _Optional[str] = ..., tool_name: _Optional[str] = ..., arguments_json: _Optional[str] = ..., session_id: _Optional[str] = ..., tenant_id: _Optional[str] = ..., origin: _Optional[_Union[ToolCallOrigin, str]] = ..., max_duration_seconds: _Optional[int] = ..., scope_id: _Optional[str] = ..., turn_id: _Optional[str] = ..., tool_context: _Optional[_Mapping[str, str]] = ...) -> None: ...

class ToolCallCancel(_message.Message):
    __slots__ = ("call_id", "reason")
    CALL_ID_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    call_id: str
    reason: str
    def __init__(self, call_id: _Optional[str] = ..., reason: _Optional[str] = ...) -> None: ...

class ToolCallOutput(_message.Message):
    __slots__ = ("content", "is_error", "dedup_key", "attachments", "ends_turn", "cards")
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    IS_ERROR_FIELD_NUMBER: _ClassVar[int]
    DEDUP_KEY_FIELD_NUMBER: _ClassVar[int]
    ATTACHMENTS_FIELD_NUMBER: _ClassVar[int]
    ENDS_TURN_FIELD_NUMBER: _ClassVar[int]
    CARDS_FIELD_NUMBER: _ClassVar[int]
    content: str
    is_error: bool
    dedup_key: str
    attachments: _containers.RepeatedCompositeFieldContainer[ToolResultAttachment]
    ends_turn: bool
    cards: _containers.RepeatedCompositeFieldContainer[MessageAttachment]
    def __init__(self, content: _Optional[str] = ..., is_error: _Optional[bool] = ..., dedup_key: _Optional[str] = ..., attachments: _Optional[_Iterable[_Union[ToolResultAttachment, _Mapping]]] = ..., ends_turn: _Optional[bool] = ..., cards: _Optional[_Iterable[_Union[MessageAttachment, _Mapping]]] = ...) -> None: ...

class ToolCallFailure(_message.Message):
    __slots__ = ("code", "message")
    CODE_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    code: str
    message: str
    def __init__(self, code: _Optional[str] = ..., message: _Optional[str] = ...) -> None: ...

class ToolCallResult(_message.Message):
    __slots__ = ("call_id", "output", "failure")
    CALL_ID_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_FIELD_NUMBER: _ClassVar[int]
    FAILURE_FIELD_NUMBER: _ClassVar[int]
    call_id: str
    output: ToolCallOutput
    failure: ToolCallFailure
    def __init__(self, call_id: _Optional[str] = ..., output: _Optional[_Union[ToolCallOutput, _Mapping]] = ..., failure: _Optional[_Union[ToolCallFailure, _Mapping]] = ...) -> None: ...

class ToolProviderToServer(_message.Message):
    __slots__ = ("hello", "result", "executor_hello", "tool_call_result")
    HELLO_FIELD_NUMBER: _ClassVar[int]
    RESULT_FIELD_NUMBER: _ClassVar[int]
    EXECUTOR_HELLO_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_RESULT_FIELD_NUMBER: _ClassVar[int]
    hello: ToolProviderHello
    result: ToolExecutionResult
    executor_hello: ExecutorHello
    tool_call_result: ToolCallResult
    def __init__(self, hello: _Optional[_Union[ToolProviderHello, _Mapping]] = ..., result: _Optional[_Union[ToolExecutionResult, _Mapping]] = ..., executor_hello: _Optional[_Union[ExecutorHello, _Mapping]] = ..., tool_call_result: _Optional[_Union[ToolCallResult, _Mapping]] = ...) -> None: ...

class ToolServerToProvider(_message.Message):
    __slots__ = ("invoke", "executor_hello_ack", "tool_call", "tool_call_cancel")
    INVOKE_FIELD_NUMBER: _ClassVar[int]
    EXECUTOR_HELLO_ACK_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_CANCEL_FIELD_NUMBER: _ClassVar[int]
    invoke: ToolExecutionInvoke
    executor_hello_ack: ExecutorHelloAck
    tool_call: ExecutorToolCall
    tool_call_cancel: ToolCallCancel
    def __init__(self, invoke: _Optional[_Union[ToolExecutionInvoke, _Mapping]] = ..., executor_hello_ack: _Optional[_Union[ExecutorHelloAck, _Mapping]] = ..., tool_call: _Optional[_Union[ExecutorToolCall, _Mapping]] = ..., tool_call_cancel: _Optional[_Union[ToolCallCancel, _Mapping]] = ...) -> None: ...

class InvokeToolRequest(_message.Message):
    __slots__ = ("tool_id", "arguments_json", "session_id")
    TOOL_ID_FIELD_NUMBER: _ClassVar[int]
    ARGUMENTS_JSON_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    tool_id: str
    arguments_json: str
    session_id: str
    def __init__(self, tool_id: _Optional[str] = ..., arguments_json: _Optional[str] = ..., session_id: _Optional[str] = ...) -> None: ...

class InvokeToolResponse(_message.Message):
    __slots__ = ("result_json", "is_error", "error_message")
    RESULT_JSON_FIELD_NUMBER: _ClassVar[int]
    IS_ERROR_FIELD_NUMBER: _ClassVar[int]
    ERROR_MESSAGE_FIELD_NUMBER: _ClassVar[int]
    result_json: str
    is_error: bool
    error_message: str
    def __init__(self, result_json: _Optional[str] = ..., is_error: _Optional[bool] = ..., error_message: _Optional[str] = ...) -> None: ...

class SessionCacheGetRequest(_message.Message):
    __slots__ = ("session_id", "namespace", "key")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    NAMESPACE_FIELD_NUMBER: _ClassVar[int]
    KEY_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    namespace: str
    key: str
    def __init__(self, session_id: _Optional[str] = ..., namespace: _Optional[str] = ..., key: _Optional[str] = ...) -> None: ...

class SessionCacheGetResponse(_message.Message):
    __slots__ = ("value", "found")
    VALUE_FIELD_NUMBER: _ClassVar[int]
    FOUND_FIELD_NUMBER: _ClassVar[int]
    value: bytes
    found: bool
    def __init__(self, value: _Optional[bytes] = ..., found: _Optional[bool] = ...) -> None: ...

class SessionCacheSetRequest(_message.Message):
    __slots__ = ("session_id", "namespace", "key", "value")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    NAMESPACE_FIELD_NUMBER: _ClassVar[int]
    KEY_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    namespace: str
    key: str
    value: bytes
    def __init__(self, session_id: _Optional[str] = ..., namespace: _Optional[str] = ..., key: _Optional[str] = ..., value: _Optional[bytes] = ...) -> None: ...

class SessionCacheSetResponse(_message.Message):
    __slots__ = ("success",)
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    success: bool
    def __init__(self, success: _Optional[bool] = ...) -> None: ...

class SessionCacheDeleteRequest(_message.Message):
    __slots__ = ("session_id", "namespace", "key")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    NAMESPACE_FIELD_NUMBER: _ClassVar[int]
    KEY_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    namespace: str
    key: str
    def __init__(self, session_id: _Optional[str] = ..., namespace: _Optional[str] = ..., key: _Optional[str] = ...) -> None: ...

class SessionCacheDeleteResponse(_message.Message):
    __slots__ = ("deleted",)
    DELETED_FIELD_NUMBER: _ClassVar[int]
    deleted: int
    def __init__(self, deleted: _Optional[int] = ...) -> None: ...

class LlmError(_message.Message):
    __slots__ = ("kind", "provider_type", "provider_status", "retryable", "retry_after")
    KIND_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_TYPE_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_STATUS_FIELD_NUMBER: _ClassVar[int]
    RETRYABLE_FIELD_NUMBER: _ClassVar[int]
    RETRY_AFTER_FIELD_NUMBER: _ClassVar[int]
    kind: LlmErrorKind
    provider_type: LlmProviderType
    provider_status: int
    retryable: bool
    retry_after: _duration_pb2.Duration
    def __init__(self, kind: _Optional[_Union[LlmErrorKind, str]] = ..., provider_type: _Optional[_Union[LlmProviderType, str]] = ..., provider_status: _Optional[int] = ..., retryable: _Optional[bool] = ..., retry_after: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ...) -> None: ...
