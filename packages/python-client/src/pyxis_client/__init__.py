"""llmd Python client. Generated protobuf code lives in pyxis_client.gen."""

from .gen.knowledge.v1 import knowledge_pb2, knowledge_pb2_grpc
from .gen.llamacpp.v1 import llamacpp_pb2, llamacpp_pb2_grpc
from .gen.llmv1 import llm_pb2, llm_pb2_grpc
from .gen.mcpproxy.v1 import mcpproxy_pb2, mcpproxy_pb2_grpc
from .channel import create_channel
from .errors import LlmErrorInfo, llm_error_from_rpc_error
from .metadata import (
    AUTHORIZATION_METADATA_KEY,
    LLMD_PRINCIPAL_METADATA_KEY,
    LLMD_SCOPE_METADATA_KEY,
    LLMD_TENANT_METADATA_KEY,
    scope_metadata,
)

__all__ = [
    "knowledge_pb2",
    "knowledge_pb2_grpc",
    "llamacpp_pb2",
    "llamacpp_pb2_grpc",
    "llm_pb2",
    "llm_pb2_grpc",
    "mcpproxy_pb2",
    "mcpproxy_pb2_grpc",
    "create_channel",
    "scope_metadata",
    "llm_error_from_rpc_error",
    "LlmErrorInfo",
    "AUTHORIZATION_METADATA_KEY",
    "LLMD_PRINCIPAL_METADATA_KEY",
    "LLMD_SCOPE_METADATA_KEY",
    "LLMD_TENANT_METADATA_KEY",
]
