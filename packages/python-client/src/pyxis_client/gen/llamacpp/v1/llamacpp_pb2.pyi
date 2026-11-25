from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class LlamaPhase(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    LLAMA_PHASE_UNSPECIFIED: _ClassVar[LlamaPhase]
    LLAMA_PHASE_IDLE: _ClassVar[LlamaPhase]
    LLAMA_PHASE_STARTING: _ClassVar[LlamaPhase]
    LLAMA_PHASE_WAITING_HEALTH: _ClassVar[LlamaPhase]
    LLAMA_PHASE_LOADING_MODEL: _ClassVar[LlamaPhase]
    LLAMA_PHASE_READY: _ClassVar[LlamaPhase]
    LLAMA_PHASE_STOPPING: _ClassVar[LlamaPhase]
    LLAMA_PHASE_ERROR: _ClassVar[LlamaPhase]

class DownloadPhase(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    DOWNLOAD_PHASE_UNSPECIFIED: _ClassVar[DownloadPhase]
    DOWNLOAD_PHASE_DOWNLOADING: _ClassVar[DownloadPhase]
    DOWNLOAD_PHASE_COMPLETE: _ClassVar[DownloadPhase]
    DOWNLOAD_PHASE_ERROR: _ClassVar[DownloadPhase]
LLAMA_PHASE_UNSPECIFIED: LlamaPhase
LLAMA_PHASE_IDLE: LlamaPhase
LLAMA_PHASE_STARTING: LlamaPhase
LLAMA_PHASE_WAITING_HEALTH: LlamaPhase
LLAMA_PHASE_LOADING_MODEL: LlamaPhase
LLAMA_PHASE_READY: LlamaPhase
LLAMA_PHASE_STOPPING: LlamaPhase
LLAMA_PHASE_ERROR: LlamaPhase
DOWNLOAD_PHASE_UNSPECIFIED: DownloadPhase
DOWNLOAD_PHASE_DOWNLOADING: DownloadPhase
DOWNLOAD_PHASE_COMPLETE: DownloadPhase
DOWNLOAD_PHASE_ERROR: DownloadPhase

class EnsureBinaryRequest(_message.Message):
    __slots__ = ("force_upgrade", "custom_binary_path")
    FORCE_UPGRADE_FIELD_NUMBER: _ClassVar[int]
    CUSTOM_BINARY_PATH_FIELD_NUMBER: _ClassVar[int]
    force_upgrade: bool
    custom_binary_path: str
    def __init__(self, force_upgrade: _Optional[bool] = ..., custom_binary_path: _Optional[str] = ...) -> None: ...

class EnsureBinaryResponse(_message.Message):
    __slots__ = ("binary_path", "version", "available_version", "managed", "error")
    BINARY_PATH_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    AVAILABLE_VERSION_FIELD_NUMBER: _ClassVar[int]
    MANAGED_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    binary_path: str
    version: str
    available_version: str
    managed: bool
    error: str
    def __init__(self, binary_path: _Optional[str] = ..., version: _Optional[str] = ..., available_version: _Optional[str] = ..., managed: _Optional[bool] = ..., error: _Optional[str] = ...) -> None: ...

class LocalModel(_message.Message):
    __slots__ = ("model_id", "model_name", "path", "size_bytes", "download_partial", "hf_repo")
    MODEL_ID_FIELD_NUMBER: _ClassVar[int]
    MODEL_NAME_FIELD_NUMBER: _ClassVar[int]
    PATH_FIELD_NUMBER: _ClassVar[int]
    SIZE_BYTES_FIELD_NUMBER: _ClassVar[int]
    DOWNLOAD_PARTIAL_FIELD_NUMBER: _ClassVar[int]
    HF_REPO_FIELD_NUMBER: _ClassVar[int]
    model_id: str
    model_name: str
    path: str
    size_bytes: int
    download_partial: bool
    hf_repo: str
    def __init__(self, model_id: _Optional[str] = ..., model_name: _Optional[str] = ..., path: _Optional[str] = ..., size_bytes: _Optional[int] = ..., download_partial: _Optional[bool] = ..., hf_repo: _Optional[str] = ...) -> None: ...

class ListModelsRequest(_message.Message):
    __slots__ = ("model_dir",)
    MODEL_DIR_FIELD_NUMBER: _ClassVar[int]
    model_dir: str
    def __init__(self, model_dir: _Optional[str] = ...) -> None: ...

class ListModelsResponse(_message.Message):
    __slots__ = ("models", "model_dir")
    MODELS_FIELD_NUMBER: _ClassVar[int]
    MODEL_DIR_FIELD_NUMBER: _ClassVar[int]
    models: _containers.RepeatedCompositeFieldContainer[LocalModel]
    model_dir: str
    def __init__(self, models: _Optional[_Iterable[_Union[LocalModel, _Mapping]]] = ..., model_dir: _Optional[str] = ...) -> None: ...

class DownloadRequest(_message.Message):
    __slots__ = ("hf_repo", "hf_file", "hf_revision", "model_dir", "catalog_id")
    HF_REPO_FIELD_NUMBER: _ClassVar[int]
    HF_FILE_FIELD_NUMBER: _ClassVar[int]
    HF_REVISION_FIELD_NUMBER: _ClassVar[int]
    MODEL_DIR_FIELD_NUMBER: _ClassVar[int]
    CATALOG_ID_FIELD_NUMBER: _ClassVar[int]
    hf_repo: str
    hf_file: str
    hf_revision: str
    model_dir: str
    catalog_id: str
    def __init__(self, hf_repo: _Optional[str] = ..., hf_file: _Optional[str] = ..., hf_revision: _Optional[str] = ..., model_dir: _Optional[str] = ..., catalog_id: _Optional[str] = ...) -> None: ...

class DownloadProgress(_message.Message):
    __slots__ = ("phase", "downloaded_bytes", "total_bytes", "file_label", "model_path", "model_id", "error")
    PHASE_FIELD_NUMBER: _ClassVar[int]
    DOWNLOADED_BYTES_FIELD_NUMBER: _ClassVar[int]
    TOTAL_BYTES_FIELD_NUMBER: _ClassVar[int]
    FILE_LABEL_FIELD_NUMBER: _ClassVar[int]
    MODEL_PATH_FIELD_NUMBER: _ClassVar[int]
    MODEL_ID_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    phase: DownloadPhase
    downloaded_bytes: int
    total_bytes: int
    file_label: str
    model_path: str
    model_id: str
    error: str
    def __init__(self, phase: _Optional[_Union[DownloadPhase, str]] = ..., downloaded_bytes: _Optional[int] = ..., total_bytes: _Optional[int] = ..., file_label: _Optional[str] = ..., model_path: _Optional[str] = ..., model_id: _Optional[str] = ..., error: _Optional[str] = ...) -> None: ...

class StartRequest(_message.Message):
    __slots__ = ("model_id", "model_dir", "binary_path", "port", "extra_args")
    MODEL_ID_FIELD_NUMBER: _ClassVar[int]
    MODEL_DIR_FIELD_NUMBER: _ClassVar[int]
    BINARY_PATH_FIELD_NUMBER: _ClassVar[int]
    PORT_FIELD_NUMBER: _ClassVar[int]
    EXTRA_ARGS_FIELD_NUMBER: _ClassVar[int]
    model_id: str
    model_dir: str
    binary_path: str
    port: int
    extra_args: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, model_id: _Optional[str] = ..., model_dir: _Optional[str] = ..., binary_path: _Optional[str] = ..., port: _Optional[int] = ..., extra_args: _Optional[_Iterable[str]] = ...) -> None: ...

class SetManagedStateRequest(_message.Message):
    __slots__ = ("enabled", "active_model_id")
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    ACTIVE_MODEL_ID_FIELD_NUMBER: _ClassVar[int]
    enabled: bool
    active_model_id: str
    def __init__(self, enabled: _Optional[bool] = ..., active_model_id: _Optional[str] = ...) -> None: ...

class StopRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class StatusRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class StatusStreamRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class LlamaStatus(_message.Message):
    __slots__ = ("phase", "message", "model_id", "model_dir", "binary_path", "port", "base_url", "pid", "tenant_id", "enabled", "active_model_id", "error", "scope_id")
    PHASE_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    MODEL_ID_FIELD_NUMBER: _ClassVar[int]
    MODEL_DIR_FIELD_NUMBER: _ClassVar[int]
    BINARY_PATH_FIELD_NUMBER: _ClassVar[int]
    PORT_FIELD_NUMBER: _ClassVar[int]
    BASE_URL_FIELD_NUMBER: _ClassVar[int]
    PID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    ACTIVE_MODEL_ID_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    SCOPE_ID_FIELD_NUMBER: _ClassVar[int]
    phase: LlamaPhase
    message: str
    model_id: str
    model_dir: str
    binary_path: str
    port: int
    base_url: str
    pid: int
    tenant_id: str
    enabled: bool
    active_model_id: str
    error: str
    scope_id: str
    def __init__(self, phase: _Optional[_Union[LlamaPhase, str]] = ..., message: _Optional[str] = ..., model_id: _Optional[str] = ..., model_dir: _Optional[str] = ..., binary_path: _Optional[str] = ..., port: _Optional[int] = ..., base_url: _Optional[str] = ..., pid: _Optional[int] = ..., tenant_id: _Optional[str] = ..., enabled: _Optional[bool] = ..., active_model_id: _Optional[str] = ..., error: _Optional[str] = ..., scope_id: _Optional[str] = ...) -> None: ...
