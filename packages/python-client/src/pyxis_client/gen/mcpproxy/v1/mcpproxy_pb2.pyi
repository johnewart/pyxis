from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class McpTransportKind(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    MCP_TRANSPORT_KIND_UNSPECIFIED: _ClassVar[McpTransportKind]
    MCP_TRANSPORT_KIND_STDIO: _ClassVar[McpTransportKind]
    MCP_TRANSPORT_KIND_HTTP: _ClassVar[McpTransportKind]
    MCP_TRANSPORT_KIND_SSE: _ClassVar[McpTransportKind]

class McpProviderStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    MCP_PROVIDER_STATUS_UNSPECIFIED: _ClassVar[McpProviderStatus]
    MCP_PROVIDER_STATUS_CONNECTING: _ClassVar[McpProviderStatus]
    MCP_PROVIDER_STATUS_READY: _ClassVar[McpProviderStatus]
    MCP_PROVIDER_STATUS_ERROR: _ClassVar[McpProviderStatus]
    MCP_PROVIDER_STATUS_DISABLED: _ClassVar[McpProviderStatus]
MCP_TRANSPORT_KIND_UNSPECIFIED: McpTransportKind
MCP_TRANSPORT_KIND_STDIO: McpTransportKind
MCP_TRANSPORT_KIND_HTTP: McpTransportKind
MCP_TRANSPORT_KIND_SSE: McpTransportKind
MCP_PROVIDER_STATUS_UNSPECIFIED: McpProviderStatus
MCP_PROVIDER_STATUS_CONNECTING: McpProviderStatus
MCP_PROVIDER_STATUS_READY: McpProviderStatus
MCP_PROVIDER_STATUS_ERROR: McpProviderStatus
MCP_PROVIDER_STATUS_DISABLED: McpProviderStatus

class McpTransportConfig(_message.Message):
    __slots__ = ("type", "command", "args", "env", "cwd", "url", "headers")
    class EnvEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    class HeadersEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    TYPE_FIELD_NUMBER: _ClassVar[int]
    COMMAND_FIELD_NUMBER: _ClassVar[int]
    ARGS_FIELD_NUMBER: _ClassVar[int]
    ENV_FIELD_NUMBER: _ClassVar[int]
    CWD_FIELD_NUMBER: _ClassVar[int]
    URL_FIELD_NUMBER: _ClassVar[int]
    HEADERS_FIELD_NUMBER: _ClassVar[int]
    type: McpTransportKind
    command: str
    args: _containers.RepeatedScalarFieldContainer[str]
    env: _containers.ScalarMap[str, str]
    cwd: str
    url: str
    headers: _containers.ScalarMap[str, str]
    def __init__(self, type: _Optional[_Union[McpTransportKind, str]] = ..., command: _Optional[str] = ..., args: _Optional[_Iterable[str]] = ..., env: _Optional[_Mapping[str, str]] = ..., cwd: _Optional[str] = ..., url: _Optional[str] = ..., headers: _Optional[_Mapping[str, str]] = ...) -> None: ...

class McpServerConfig(_message.Message):
    __slots__ = ("enabled", "display_name", "attach_to_agents", "default_classification", "read_only_tool_allowlist", "tool_classifications", "transport", "call_timeout_ms", "scopes")
    class ToolClassificationsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    ATTACH_TO_AGENTS_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_CLASSIFICATION_FIELD_NUMBER: _ClassVar[int]
    READ_ONLY_TOOL_ALLOWLIST_FIELD_NUMBER: _ClassVar[int]
    TOOL_CLASSIFICATIONS_FIELD_NUMBER: _ClassVar[int]
    TRANSPORT_FIELD_NUMBER: _ClassVar[int]
    CALL_TIMEOUT_MS_FIELD_NUMBER: _ClassVar[int]
    SCOPES_FIELD_NUMBER: _ClassVar[int]
    enabled: bool
    display_name: str
    attach_to_agents: _containers.RepeatedScalarFieldContainer[str]
    default_classification: str
    read_only_tool_allowlist: _containers.RepeatedScalarFieldContainer[str]
    tool_classifications: _containers.ScalarMap[str, str]
    transport: McpTransportConfig
    call_timeout_ms: int
    scopes: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, enabled: _Optional[bool] = ..., display_name: _Optional[str] = ..., attach_to_agents: _Optional[_Iterable[str]] = ..., default_classification: _Optional[str] = ..., read_only_tool_allowlist: _Optional[_Iterable[str]] = ..., tool_classifications: _Optional[_Mapping[str, str]] = ..., transport: _Optional[_Union[McpTransportConfig, _Mapping]] = ..., call_timeout_ms: _Optional[int] = ..., scopes: _Optional[_Iterable[str]] = ...) -> None: ...

class McpProviderSnapshot(_message.Message):
    __slots__ = ("provider_id", "status", "display_name", "tool_count", "resource_count", "prompt_count", "registered_tool_ids", "last_refresh_at", "last_error")
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    TOOL_COUNT_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_COUNT_FIELD_NUMBER: _ClassVar[int]
    PROMPT_COUNT_FIELD_NUMBER: _ClassVar[int]
    REGISTERED_TOOL_IDS_FIELD_NUMBER: _ClassVar[int]
    LAST_REFRESH_AT_FIELD_NUMBER: _ClassVar[int]
    LAST_ERROR_FIELD_NUMBER: _ClassVar[int]
    provider_id: str
    status: McpProviderStatus
    display_name: str
    tool_count: int
    resource_count: int
    prompt_count: int
    registered_tool_ids: _containers.RepeatedScalarFieldContainer[str]
    last_refresh_at: str
    last_error: str
    def __init__(self, provider_id: _Optional[str] = ..., status: _Optional[_Union[McpProviderStatus, str]] = ..., display_name: _Optional[str] = ..., tool_count: _Optional[int] = ..., resource_count: _Optional[int] = ..., prompt_count: _Optional[int] = ..., registered_tool_ids: _Optional[_Iterable[str]] = ..., last_refresh_at: _Optional[str] = ..., last_error: _Optional[str] = ...) -> None: ...

class ProxiedTool(_message.Message):
    __slots__ = ("tool_id", "provider_id", "upstream_name", "description", "input_schema_json", "read_only")
    TOOL_ID_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    UPSTREAM_NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    INPUT_SCHEMA_JSON_FIELD_NUMBER: _ClassVar[int]
    READ_ONLY_FIELD_NUMBER: _ClassVar[int]
    tool_id: str
    provider_id: str
    upstream_name: str
    description: str
    input_schema_json: str
    read_only: bool
    def __init__(self, tool_id: _Optional[str] = ..., provider_id: _Optional[str] = ..., upstream_name: _Optional[str] = ..., description: _Optional[str] = ..., input_schema_json: _Optional[str] = ..., read_only: _Optional[bool] = ...) -> None: ...

class McpResource(_message.Message):
    __slots__ = ("uri", "name", "description", "mime_type")
    URI_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    MIME_TYPE_FIELD_NUMBER: _ClassVar[int]
    uri: str
    name: str
    description: str
    mime_type: str
    def __init__(self, uri: _Optional[str] = ..., name: _Optional[str] = ..., description: _Optional[str] = ..., mime_type: _Optional[str] = ...) -> None: ...

class McpPromptArgument(_message.Message):
    __slots__ = ("name", "description", "required")
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    REQUIRED_FIELD_NUMBER: _ClassVar[int]
    name: str
    description: str
    required: bool
    def __init__(self, name: _Optional[str] = ..., description: _Optional[str] = ..., required: _Optional[bool] = ...) -> None: ...

class McpPrompt(_message.Message):
    __slots__ = ("name", "description", "arguments")
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    ARGUMENTS_FIELD_NUMBER: _ClassVar[int]
    name: str
    description: str
    arguments: _containers.RepeatedCompositeFieldContainer[McpPromptArgument]
    def __init__(self, name: _Optional[str] = ..., description: _Optional[str] = ..., arguments: _Optional[_Iterable[_Union[McpPromptArgument, _Mapping]]] = ...) -> None: ...

class SetProvidersRequest(_message.Message):
    __slots__ = ("servers",)
    class ServersEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: McpServerConfig
        def __init__(self, key: _Optional[str] = ..., value: _Optional[_Union[McpServerConfig, _Mapping]] = ...) -> None: ...
    SERVERS_FIELD_NUMBER: _ClassVar[int]
    servers: _containers.MessageMap[str, McpServerConfig]
    def __init__(self, servers: _Optional[_Mapping[str, McpServerConfig]] = ...) -> None: ...

class SetProvidersResponse(_message.Message):
    __slots__ = ("providers",)
    PROVIDERS_FIELD_NUMBER: _ClassVar[int]
    providers: _containers.RepeatedCompositeFieldContainer[McpProviderSnapshot]
    def __init__(self, providers: _Optional[_Iterable[_Union[McpProviderSnapshot, _Mapping]]] = ...) -> None: ...

class ListProvidersRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListProvidersResponse(_message.Message):
    __slots__ = ("providers", "enabled")
    PROVIDERS_FIELD_NUMBER: _ClassVar[int]
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    providers: _containers.RepeatedCompositeFieldContainer[McpProviderSnapshot]
    enabled: bool
    def __init__(self, providers: _Optional[_Iterable[_Union[McpProviderSnapshot, _Mapping]]] = ..., enabled: _Optional[bool] = ...) -> None: ...

class ListProxiedToolsRequest(_message.Message):
    __slots__ = ("agent_id",)
    AGENT_ID_FIELD_NUMBER: _ClassVar[int]
    agent_id: str
    def __init__(self, agent_id: _Optional[str] = ...) -> None: ...

class ListProxiedToolsResponse(_message.Message):
    __slots__ = ("tools", "agent_tool_ids", "scope_tool_ids")
    TOOLS_FIELD_NUMBER: _ClassVar[int]
    AGENT_TOOL_IDS_FIELD_NUMBER: _ClassVar[int]
    SCOPE_TOOL_IDS_FIELD_NUMBER: _ClassVar[int]
    tools: _containers.RepeatedCompositeFieldContainer[ProxiedTool]
    agent_tool_ids: _containers.RepeatedScalarFieldContainer[str]
    scope_tool_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, tools: _Optional[_Iterable[_Union[ProxiedTool, _Mapping]]] = ..., agent_tool_ids: _Optional[_Iterable[str]] = ..., scope_tool_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class CallProxiedToolRequest(_message.Message):
    __slots__ = ("tool_id", "args_json")
    TOOL_ID_FIELD_NUMBER: _ClassVar[int]
    ARGS_JSON_FIELD_NUMBER: _ClassVar[int]
    tool_id: str
    args_json: str
    def __init__(self, tool_id: _Optional[str] = ..., args_json: _Optional[str] = ...) -> None: ...

class CallProxiedToolResponse(_message.Message):
    __slots__ = ("text",)
    TEXT_FIELD_NUMBER: _ClassVar[int]
    text: str
    def __init__(self, text: _Optional[str] = ...) -> None: ...

class GetProviderCatalogRequest(_message.Message):
    __slots__ = ("provider_id",)
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    provider_id: str
    def __init__(self, provider_id: _Optional[str] = ...) -> None: ...

class GetProviderCatalogResponse(_message.Message):
    __slots__ = ("provider_id", "resources", "prompts")
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    RESOURCES_FIELD_NUMBER: _ClassVar[int]
    PROMPTS_FIELD_NUMBER: _ClassVar[int]
    provider_id: str
    resources: _containers.RepeatedCompositeFieldContainer[McpResource]
    prompts: _containers.RepeatedCompositeFieldContainer[McpPrompt]
    def __init__(self, provider_id: _Optional[str] = ..., resources: _Optional[_Iterable[_Union[McpResource, _Mapping]]] = ..., prompts: _Optional[_Iterable[_Union[McpPrompt, _Mapping]]] = ...) -> None: ...

class ProbeProviderRequest(_message.Message):
    __slots__ = ("provider_id", "config")
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    provider_id: str
    config: McpServerConfig
    def __init__(self, provider_id: _Optional[str] = ..., config: _Optional[_Union[McpServerConfig, _Mapping]] = ...) -> None: ...

class ProbeProviderResponse(_message.Message):
    __slots__ = ("ok", "error", "tool_count", "tools")
    OK_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    TOOL_COUNT_FIELD_NUMBER: _ClassVar[int]
    TOOLS_FIELD_NUMBER: _ClassVar[int]
    ok: bool
    error: str
    tool_count: int
    tools: _containers.RepeatedCompositeFieldContainer[ProbedTool]
    def __init__(self, ok: _Optional[bool] = ..., error: _Optional[str] = ..., tool_count: _Optional[int] = ..., tools: _Optional[_Iterable[_Union[ProbedTool, _Mapping]]] = ...) -> None: ...

class ProbedTool(_message.Message):
    __slots__ = ("name", "description")
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    name: str
    description: str
    def __init__(self, name: _Optional[str] = ..., description: _Optional[str] = ...) -> None: ...
