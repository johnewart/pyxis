import grpc
from google.protobuf import any_pb2, duration_pb2
from google.rpc import status_pb2

from pyxis_client import (
    create_channel,
    llm_error_from_rpc_error,
    llm_pb2,
    scope_metadata,
)


def test_scope_metadata_trims_and_omits_empty():
    assert scope_metadata(" t1 ", "  ", None) == [("llmd-tenant-id", "t1")]
    assert scope_metadata("t", "s", "p", "tok") == [
        ("llmd-tenant-id", "t"),
        ("llmd-scope-id", "s"),
        ("llmd-principal-id", "p"),
        ("authorization", "Bearer tok"),
    ]
    assert scope_metadata("", None) == []


class _FakeErr(grpc.Call, grpc.RpcError):
    def __init__(self, md):
        self._md = md

    def code(self):
        return grpc.StatusCode.RESOURCE_EXHAUSTED

    def details(self):
        return ""

    def trailing_metadata(self):
        return self._md

    def initial_metadata(self):
        return ()

    def is_active(self):
        return False

    def time_remaining(self):
        return None

    def cancel(self):
        return False

    def add_callback(self, callback):
        return False


def _details(msg):
    a = any_pb2.Any()
    a.Pack(msg)
    return status_pb2.Status(code=grpc.StatusCode.RESOURCE_EXHAUSTED.value[0], details=[a]).SerializeToString()


def test_llm_error_parse():
    e = llm_pb2.LlmError(
        provider_status=429,
        retryable=True,
        retry_after=duration_pb2.Duration(seconds=2, nanos=500_000_000),
    )
    info = llm_error_from_rpc_error(
        _FakeErr([("x", "y"), ("grpc-status-details-bin", _details(e))])
    )
    assert info is not None
    assert info.provider_status == 429
    assert info.retryable is True
    assert info.retry_after_ms == 2500


def test_llm_error_absent():
    assert llm_error_from_rpc_error(ValueError("x")) is None
    assert llm_error_from_rpc_error(_FakeErr([])) is None
    assert llm_error_from_rpc_error(_FakeErr([("grpc-status-details-bin", b"\xff")])) is None
    other = _details(duration_pb2.Duration())
    assert llm_error_from_rpc_error(_FakeErr([("grpc-status-details-bin", other)])) is None


def test_create_channel_builds():
    ch = create_channel("localhost:1", tenant_id="t", bearer_token="b")
    ch.close()
