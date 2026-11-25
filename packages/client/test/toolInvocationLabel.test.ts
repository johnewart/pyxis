/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * Unit tests for toolInvocationLabel — the helper a UI uses to turn a
 * ToolExecutionStarted event's arguments into "Reading lines 15-200 of foo.go"
 * instead of "read_code".
 *
 * The invariant worth defending: the function is TOTAL. Arguments come from a
 * model, so missing keys and wrong types are ordinary input, and a render must
 * never crash or show an empty label.
 */

import { describe, expect, it } from 'vitest';
import { toolInvocationLabel } from '../src/toolInvocationLabel.js';

describe('toolInvocationLabel', () => {
  it('describes a read with a line range', () => {
    expect(
      toolInvocationLabel({
        name: 'read_code',
        arguments: { path: 'internal/serve/foo.go', start_line: 15, end_line: 200 },
      })
    ).toBe('Reading lines 15-200 of internal/serve/foo.go');
  });

  it('describes a read with no range, and one-sided ranges', () => {
    expect(toolInvocationLabel({ name: 'read_code', arguments: { path: 'a/b.go' } })).toBe(
      'Reading a/b.go'
    );
    expect(
      toolInvocationLabel({ name: 'read_code', arguments: { path: 'a/b.go', start_line: 15 } })
    ).toBe('Reading from line 15 of a/b.go');
  });

  it('accepts numeric strings for line numbers (models emit both)', () => {
    expect(
      toolInvocationLabel({
        name: 'read_code',
        arguments: { path: 'a/b.go', start_line: '15', end_line: '20' },
      })
    ).toBe('Reading lines 15-20 of a/b.go');
  });

  it('counts batch reads instead of naming one file', () => {
    expect(
      toolInvocationLabel({
        name: 'read_code',
        arguments: { reads: [{ path: 'a.go' }, { path: 'b.go' }, { path: 'c.go' }] },
      })
    ).toBe('Reading 3 files');
  });

  it('distinguishes creating from editing by base_revision', () => {
    expect(
      toolInvocationLabel({ name: 'write_code', arguments: { path: 'a.ts', content: 'x' } })
    ).toBe('Creating a.ts');
    expect(
      toolInvocationLabel({
        name: 'write_code',
        arguments: { path: 'a.ts', content: 'x', base_revision: 'deadbeef' },
      })
    ).toBe('Editing a.ts');
  });

  it('labels a search by match mode, and names the scoped path', () => {
    expect(
      toolInvocationLabel({ name: 'search', arguments: { match: { literal: 'NewBrowserSurface' } } })
    ).toBe('Searching for literal "NewBrowserSurface"');
    // semantic is the default reading, so its mode is left implicit
    expect(
      toolInvocationLabel({ name: 'search', arguments: { match: { semantic: 'how tools merge' } } })
    ).toBe('Searching for "how tools merge"');
    expect(
      toolInvocationLabel({
        name: 'search',
        arguments: { match: { regex: 'foo.*bar' }, scope: { code: { path: 'internal/serve' } } },
      })
    ).toBe('Searching internal/serve for regex "foo.*bar"');
  });

  it('labels listing a directory', () => {
    expect(
      toolInvocationLabel({ name: 'enumerate', arguments: { filter: { path: 'tmp/foo' } } })
    ).toBe('Listing files in tmp/foo');
    expect(toolInvocationLabel({ name: 'enumerate', arguments: {} })).toBe('Listing files');
  });

  it('quotes and collapses a multi-line command', () => {
    expect(
      toolInvocationLabel({ name: 'run_command', arguments: { command: 'go test\n  ./...' } })
    ).toBe('Running "go test ./..."');
  });

  it('shortens a long path from the tail, keeping the filename', () => {
    const label = toolInvocationLabel({
      name: 'read_code',
      arguments: {
        path: 'packages/react/src/components/deeply/nested/directory/tree/Component.tsx',
      },
    });
    expect(label).toContain('Component.tsx');
    expect(label).toContain('…/');
    expect(label.length).toBeLessThan(70);
  });

  it('still finds identifying fields when llmd elided oversized values', () => {
    // llmd elides per VALUE, so path/start_line survive a huge `content`.
    expect(
      toolInvocationLabel({
        name: 'read_code',
        arguments: { path: 'big.go', start_line: 15, end_line: 200, content: 'xxx…' },
      })
    ).toBe('Reading lines 15-200 of big.go');
  });

  it('falls back to a humanized name plus the best identifying field', () => {
    expect(
      toolInvocationLabel({ name: 'some_host_tool', arguments: { path: 'a/b.txt' } })
    ).toBe('Some host tool: a/b.txt');
    expect(toolInvocationLabel({ name: 'some_host_tool', arguments: {} })).toBe('Some host tool');
  });

  it('never returns empty, throws, or leaks a wrong-typed field', () => {
    const cases: Array<{ name: string; arguments?: Record<string, unknown> }> = [
      { name: 'read_code' },
      { name: 'read_code', arguments: {} },
      // wrong types everywhere: path as number, ranges as objects, match as array
      { name: 'read_code', arguments: { path: 42, start_line: {}, end_line: [] } },
      { name: 'search', arguments: { match: ['literal', 'x'] } },
      { name: 'search', arguments: { match: null } },
      { name: 'enumerate', arguments: { filter: 'not-an-object' } },
      { name: 'traverse_graph', arguments: { mode: 'impact', seeds: { paths: [7] } } },
      { name: 'write_code', arguments: { operations: [] } },
      { name: '', arguments: { path: 'x' } },
      { name: '   ' },
    ];
    for (const c of cases) {
      const label = toolInvocationLabel(c);
      expect(typeof label, JSON.stringify(c)).toBe('string');
      expect(label.trim().length, JSON.stringify(c)).toBeGreaterThan(0);
    }
  });

  it('labels the core session tools, which every agent has', () => {
    expect(toolInvocationLabel({ name: 'list_todos' })).toBe('Listing todos');
    expect(
      toolInvocationLabel({ name: 'add_todo', arguments: { description: 'Wire the thing' } })
    ).toBe('Adding todo "Wire the thing"');
    expect(
      toolInvocationLabel({ name: 'set_status', arguments: { description: 'Running tests' } })
    ).toBe('Status: Running tests');
    expect(
      toolInvocationLabel({ name: 'update_todo', arguments: { id: 'x', status: 'completed' } })
    ).toBe('Marking todo completed');
  });
});
