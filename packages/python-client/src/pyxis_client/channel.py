"""Channel construction that attaches scope metadata to every call."""

from __future__ import annotations

import collections
from typing import Optional

import grpc

from .metadata import scope_metadata


class _Details(
    collections.namedtuple(
        "_Details",
        ("method", "timeout", "metadata", "credentials", "wait_for_ready", "compression"),
    ),
    grpc.ClientCallDetails,
):
    pass


class _MetadataInterceptor(
    grpc.UnaryUnaryClientInterceptor,
    grpc.UnaryStreamClientInterceptor,
    grpc.StreamUnaryClientInterceptor,
    grpc.StreamStreamClientInterceptor,
):
    def __init__(self, metadata):
        self._metadata = list(metadata)

    def _details(self, d):
        md = list(d.metadata or []) + self._metadata
        return _Details(
            d.method, d.timeout, md, d.credentials, d.wait_for_ready, d.compression
        )

    def intercept_unary_unary(self, continuation, details, request):
        return continuation(self._details(details), request)

    def intercept_unary_stream(self, continuation, details, request):
        return continuation(self._details(details), request)

    def intercept_stream_unary(self, continuation, details, request_iterator):
        return continuation(self._details(details), request_iterator)

    def intercept_stream_stream(self, continuation, details, request_iterator):
        return continuation(self._details(details), request_iterator)


def create_channel(
    target: str,
    *,
    tenant_id: Optional[str] = None,
    scope_id: Optional[str] = None,
    principal_id: Optional[str] = None,
    bearer_token: Optional[str] = None,
    secure: bool = False,
    credentials: Optional[grpc.ChannelCredentials] = None,
) -> grpc.Channel:
    """Open a channel that sends scope/principal/bearer metadata on every RPC."""
    if credentials is not None or secure:
        channel = grpc.secure_channel(target, credentials or grpc.ssl_channel_credentials())
    else:
        channel = grpc.insecure_channel(target)
    md = scope_metadata(tenant_id, scope_id, principal_id, bearer_token)
    if md:
        channel = grpc.intercept_channel(channel, _MetadataInterceptor(md))
    return channel
