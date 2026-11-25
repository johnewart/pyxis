"""Request metadata helpers; mirrors scopeHeaders in packages/client/src/transport.ts."""

from __future__ import annotations

from typing import List, Optional, Tuple

LLMD_TENANT_METADATA_KEY = "llmd-tenant-id"
LLMD_SCOPE_METADATA_KEY = "llmd-scope-id"
LLMD_PRINCIPAL_METADATA_KEY = "llmd-principal-id"
AUTHORIZATION_METADATA_KEY = "authorization"


def scope_metadata(
    tenant_id: Optional[str],
    scope_id: Optional[str] = None,
    principal_id: Optional[str] = None,
    bearer_token: Optional[str] = None,
) -> List[Tuple[str, str]]:
    """Build gRPC metadata for tenant, scope, principal and bearer token.

    Values are trimmed and omitted when empty, like the TS client.
    """
    out: List[Tuple[str, str]] = []
    for key, value in (
        (LLMD_TENANT_METADATA_KEY, tenant_id),
        (LLMD_SCOPE_METADATA_KEY, scope_id),
        (LLMD_PRINCIPAL_METADATA_KEY, principal_id),
    ):
        v = (value or "").strip()
        if v:
            out.append((key, v))
    token = (bearer_token or "").strip()
    if token:
        out.append((AUTHORIZATION_METADATA_KEY, "Bearer " + token))
    return out
