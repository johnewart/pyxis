"""Parse the structured LlmError detail from a failed gRPC call."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

import grpc
from grpc_status import rpc_status

from .gen.llmv1 import llm_pb2


@dataclass(frozen=True)
class LlmErrorInfo:
    kind: int
    provider_type: int
    provider_status: int
    retryable: bool
    #: Retry-after in milliseconds; None when the server sent none.
    retry_after_ms: Optional[float]


def llm_error_to_info(d: llm_pb2.LlmError) -> LlmErrorInfo:
    retry = None
    if d.HasField("retry_after"):
        retry = d.retry_after.seconds * 1000 + d.retry_after.nanos / 1e6
    return LlmErrorInfo(
        kind=d.kind,
        provider_type=d.provider_type,
        provider_status=d.provider_status,
        retryable=d.retryable,
        retry_after_ms=retry,
    )


def llm_error_from_rpc_error(err: object) -> Optional[LlmErrorInfo]:
    """Return the LlmError detail attached to a grpc.RpcError, or None."""
    if not isinstance(err, grpc.Call):
        return None
    try:
        st = rpc_status.from_call(err)
    except Exception:
        # Malformed grpc-status-details-bin or code mismatch: no usable detail.
        return None
    if st is None:
        return None
    for any_msg in st.details:
        detail = llm_pb2.LlmError()
        if any_msg.Unpack(detail):
            return llm_error_to_info(detail)
    return None
