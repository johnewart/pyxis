import { describe, expect, it } from 'vitest';
import {
  LLMD_SCOPE_METADATA_KEY,
  LLMD_TENANT_METADATA_KEY,
  scopeHeaders,
} from '../src/transport.js';

describe('scopeHeaders', () => {
  it('sets tenant and scope headers (trimmed)', () => {
    const h = scopeHeaders({ tenantId: ' t1 ', scopeId: ' s1 ' });
    expect(LLMD_TENANT_METADATA_KEY).toBe('llmd-tenant-id');
    expect(LLMD_SCOPE_METADATA_KEY).toBe('llmd-scope-id');
    expect(h.get('llmd-tenant-id')).toBe('t1');
    expect(h.get('llmd-scope-id')).toBe('s1');
    expect(h.has('llmd-workspace-id')).toBe(false);
  });

  it('omits scope header for undefined scopeId', () => {
    const h = scopeHeaders({ tenantId: 't1' });
    expect(h.get('llmd-tenant-id')).toBe('t1');
    expect(h.has('llmd-scope-id')).toBe(false);
  });

  it('omits scope header for empty/whitespace scopeId', () => {
    expect(scopeHeaders({ tenantId: 't1', scopeId: '' }).has('llmd-scope-id')).toBe(false);
    expect(scopeHeaders({ tenantId: 't1', scopeId: '   ' }).has('llmd-scope-id')).toBe(false);
  });
});
