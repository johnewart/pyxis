from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class KnowledgeRemediation(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    KNOWLEDGE_REMEDIATION_UNSPECIFIED: _ClassVar[KnowledgeRemediation]
    REINDEX_REQUIRED: _ClassVar[KnowledgeRemediation]
    NEW_SCOPE_REQUIRED: _ClassVar[KnowledgeRemediation]
KNOWLEDGE_REMEDIATION_UNSPECIFIED: KnowledgeRemediation
REINDEX_REQUIRED: KnowledgeRemediation
NEW_SCOPE_REQUIRED: KnowledgeRemediation

class KnowledgeError(_message.Message):
    __slots__ = ("stored_model_id", "requested_model_id", "stored_dimension", "requested_dimension", "remediation")
    STORED_MODEL_ID_FIELD_NUMBER: _ClassVar[int]
    REQUESTED_MODEL_ID_FIELD_NUMBER: _ClassVar[int]
    STORED_DIMENSION_FIELD_NUMBER: _ClassVar[int]
    REQUESTED_DIMENSION_FIELD_NUMBER: _ClassVar[int]
    REMEDIATION_FIELD_NUMBER: _ClassVar[int]
    stored_model_id: str
    requested_model_id: str
    stored_dimension: int
    requested_dimension: int
    remediation: KnowledgeRemediation
    def __init__(self, stored_model_id: _Optional[str] = ..., requested_model_id: _Optional[str] = ..., stored_dimension: _Optional[int] = ..., requested_dimension: _Optional[int] = ..., remediation: _Optional[_Union[KnowledgeRemediation, str]] = ...) -> None: ...

class EmbeddingConfig(_message.Message):
    __slots__ = ("provider_id", "model", "dimensions", "index_model_id")
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    DIMENSIONS_FIELD_NUMBER: _ClassVar[int]
    INDEX_MODEL_ID_FIELD_NUMBER: _ClassVar[int]
    provider_id: str
    model: str
    dimensions: int
    index_model_id: str
    def __init__(self, provider_id: _Optional[str] = ..., model: _Optional[str] = ..., dimensions: _Optional[int] = ..., index_model_id: _Optional[str] = ...) -> None: ...

class DocumentInput(_message.Message):
    __slots__ = ("logical_document_id", "source_id", "name", "content", "metadata")
    class MetadataEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    LOGICAL_DOCUMENT_ID_FIELD_NUMBER: _ClassVar[int]
    SOURCE_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    logical_document_id: str
    source_id: str
    name: str
    content: str
    metadata: _containers.ScalarMap[str, str]
    def __init__(self, logical_document_id: _Optional[str] = ..., source_id: _Optional[str] = ..., name: _Optional[str] = ..., content: _Optional[str] = ..., metadata: _Optional[_Mapping[str, str]] = ...) -> None: ...

class UpsertDocumentsRequest(_message.Message):
    __slots__ = ("embedding_model_id", "chunk_size_chars", "chunk_overlap_chars", "documents", "default_source_id", "chunk_strategy", "embedding")
    EMBEDDING_MODEL_ID_FIELD_NUMBER: _ClassVar[int]
    CHUNK_SIZE_CHARS_FIELD_NUMBER: _ClassVar[int]
    CHUNK_OVERLAP_CHARS_FIELD_NUMBER: _ClassVar[int]
    DOCUMENTS_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_SOURCE_ID_FIELD_NUMBER: _ClassVar[int]
    CHUNK_STRATEGY_FIELD_NUMBER: _ClassVar[int]
    EMBEDDING_FIELD_NUMBER: _ClassVar[int]
    embedding_model_id: str
    chunk_size_chars: int
    chunk_overlap_chars: int
    documents: _containers.RepeatedCompositeFieldContainer[DocumentInput]
    default_source_id: str
    chunk_strategy: str
    embedding: EmbeddingConfig
    def __init__(self, embedding_model_id: _Optional[str] = ..., chunk_size_chars: _Optional[int] = ..., chunk_overlap_chars: _Optional[int] = ..., documents: _Optional[_Iterable[_Union[DocumentInput, _Mapping]]] = ..., default_source_id: _Optional[str] = ..., chunk_strategy: _Optional[str] = ..., embedding: _Optional[_Union[EmbeddingConfig, _Mapping]] = ...) -> None: ...

class UpsertDocumentsResponse(_message.Message):
    __slots__ = ("chunks_written",)
    CHUNKS_WRITTEN_FIELD_NUMBER: _ClassVar[int]
    chunks_written: int
    def __init__(self, chunks_written: _Optional[int] = ...) -> None: ...

class DocumentRef(_message.Message):
    __slots__ = ("source_id", "logical_document_id")
    SOURCE_ID_FIELD_NUMBER: _ClassVar[int]
    LOGICAL_DOCUMENT_ID_FIELD_NUMBER: _ClassVar[int]
    source_id: str
    logical_document_id: str
    def __init__(self, source_id: _Optional[str] = ..., logical_document_id: _Optional[str] = ...) -> None: ...

class DeleteDocumentsRequest(_message.Message):
    __slots__ = ("documents", "delete_all_for_source_id", "delete_for_file_path", "delete_for_file_source_id")
    DOCUMENTS_FIELD_NUMBER: _ClassVar[int]
    DELETE_ALL_FOR_SOURCE_ID_FIELD_NUMBER: _ClassVar[int]
    DELETE_FOR_FILE_PATH_FIELD_NUMBER: _ClassVar[int]
    DELETE_FOR_FILE_SOURCE_ID_FIELD_NUMBER: _ClassVar[int]
    documents: _containers.RepeatedCompositeFieldContainer[DocumentRef]
    delete_all_for_source_id: str
    delete_for_file_path: str
    delete_for_file_source_id: str
    def __init__(self, documents: _Optional[_Iterable[_Union[DocumentRef, _Mapping]]] = ..., delete_all_for_source_id: _Optional[str] = ..., delete_for_file_path: _Optional[str] = ..., delete_for_file_source_id: _Optional[str] = ...) -> None: ...

class DeleteDocumentsResponse(_message.Message):
    __slots__ = ("chunks_deleted",)
    CHUNKS_DELETED_FIELD_NUMBER: _ClassVar[int]
    chunks_deleted: int
    def __init__(self, chunks_deleted: _Optional[int] = ...) -> None: ...

class QueryRequest(_message.Message):
    __slots__ = ("query", "limit", "embedding_model_id", "enable_rerank", "source_id", "source_ids", "embedding", "partition_id")
    QUERY_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    EMBEDDING_MODEL_ID_FIELD_NUMBER: _ClassVar[int]
    ENABLE_RERANK_FIELD_NUMBER: _ClassVar[int]
    SOURCE_ID_FIELD_NUMBER: _ClassVar[int]
    SOURCE_IDS_FIELD_NUMBER: _ClassVar[int]
    EMBEDDING_FIELD_NUMBER: _ClassVar[int]
    PARTITION_ID_FIELD_NUMBER: _ClassVar[int]
    query: str
    limit: int
    embedding_model_id: str
    enable_rerank: bool
    source_id: str
    source_ids: _containers.RepeatedScalarFieldContainer[str]
    embedding: EmbeddingConfig
    partition_id: str
    def __init__(self, query: _Optional[str] = ..., limit: _Optional[int] = ..., embedding_model_id: _Optional[str] = ..., enable_rerank: _Optional[bool] = ..., source_id: _Optional[str] = ..., source_ids: _Optional[_Iterable[str]] = ..., embedding: _Optional[_Union[EmbeddingConfig, _Mapping]] = ..., partition_id: _Optional[str] = ...) -> None: ...

class QueryHit(_message.Message):
    __slots__ = ("chunk_id", "logical_document_id", "source_id", "name", "content", "score", "metadata", "partition_id")
    class MetadataEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    CHUNK_ID_FIELD_NUMBER: _ClassVar[int]
    LOGICAL_DOCUMENT_ID_FIELD_NUMBER: _ClassVar[int]
    SOURCE_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    SCORE_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    PARTITION_ID_FIELD_NUMBER: _ClassVar[int]
    chunk_id: str
    logical_document_id: str
    source_id: str
    name: str
    content: str
    score: float
    metadata: _containers.ScalarMap[str, str]
    partition_id: str
    def __init__(self, chunk_id: _Optional[str] = ..., logical_document_id: _Optional[str] = ..., source_id: _Optional[str] = ..., name: _Optional[str] = ..., content: _Optional[str] = ..., score: _Optional[float] = ..., metadata: _Optional[_Mapping[str, str]] = ..., partition_id: _Optional[str] = ...) -> None: ...

class QueryResponse(_message.Message):
    __slots__ = ("hits",)
    HITS_FIELD_NUMBER: _ClassVar[int]
    hits: _containers.RepeatedCompositeFieldContainer[QueryHit]
    def __init__(self, hits: _Optional[_Iterable[_Union[QueryHit, _Mapping]]] = ...) -> None: ...

class CountDocumentsRequest(_message.Message):
    __slots__ = ("source_id",)
    SOURCE_ID_FIELD_NUMBER: _ClassVar[int]
    source_id: str
    def __init__(self, source_id: _Optional[str] = ...) -> None: ...

class CountDocumentsResponse(_message.Message):
    __slots__ = ("file_count", "chunk_count")
    FILE_COUNT_FIELD_NUMBER: _ClassVar[int]
    CHUNK_COUNT_FIELD_NUMBER: _ClassVar[int]
    file_count: int
    chunk_count: int
    def __init__(self, file_count: _Optional[int] = ..., chunk_count: _Optional[int] = ...) -> None: ...
