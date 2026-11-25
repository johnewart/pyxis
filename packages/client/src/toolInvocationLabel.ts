/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * Human-readable labels for tool invocations.
 *
 * `EvToolExecutionStarted` now carries `arguments_json` on every transport
 * (including MCP-dispatched CLI tools, which produce no
 * ToolCallStreamFinished), so a UI can say "Reading lines 15-200 of foo.go"
 * instead of "read_code". Turning those arguments into prose needs per-tool
 * knowledge of which fields identify the work, which is what this module is.
 *
 * Design notes:
 *
 *   - Pure and synchronous. No React, no transport; safe to call while
 *     rendering a frame as it streams in.
 *   - Total. Every input produces a non-empty string. An unknown tool, absent
 *     arguments, or a wrong-typed field falls back to the tool name plus
 *     whatever identifying scalar can be found — never a throw, never "".
 *     Arguments arrive from a model, so a field being the wrong type is normal
 *     input, not an exceptional case.
 *   - Truncation-aware. llmd elides oversized VALUES rather than clipping the
 *     payload, so identifying fields (path, line range) survive even when
 *     `argumentsTruncated` is set. Templates therefore need no special
 *     handling; they read the same fields either way.
 */

/** The fields of a ToolExecutionStarted event this module reads. */
export type ToolInvocationLabelInput = {
  name: string;
  arguments?: Record<string, unknown>;
};

/** A non-empty string, or undefined if the value is absent/blank/wrong-typed. */
function str(v: unknown): string | undefined {
  if (typeof v !== "string") return undefined;
  const t = v.trim();
  return t === "" ? undefined : t;
}

/** A finite number from a number or numeric string; undefined otherwise. */
function num(v: unknown): number | undefined {
  if (typeof v === "number") return Number.isFinite(v) ? v : undefined;
  if (typeof v === "string" && v.trim() !== "") {
    const n = Number(v);
    return Number.isFinite(n) ? n : undefined;
  }
  return undefined;
}

/** A plain object, or undefined for anything else (null, array, scalar). */
function obj(v: unknown): Record<string, unknown> | undefined {
  return v !== null && typeof v === "object" && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : undefined;
}

/** First present string among `keys`, in order. */
function pick(args: Record<string, unknown>, ...keys: string[]): string | undefined {
  for (const k of keys) {
    const v = str(args[k]);
    if (v !== undefined) return v;
  }
  return undefined;
}

/** Shows only the tail of a long path — the leading segments are rarely the point. */
function shortPath(p: string, maxLen = 48): string {
  if (p.length <= maxLen) return p;
  const segs = p.split("/");
  let out = segs[segs.length - 1] ?? p;
  for (let i = segs.length - 2; i >= 0; i--) {
    const next = `${segs[i]}/${out}`;
    if (next.length + 1 > maxLen) break;
    out = next;
  }
  return `…/${out}`;
}

/** "lines 15-200", "from line 15", "to line 200", or undefined. */
function lineRange(args: Record<string, unknown>): string | undefined {
  const start = num(args.start_line ?? args.startLine);
  const end = num(args.end_line ?? args.endLine);
  if (start !== undefined && end !== undefined) return `lines ${start}-${end}`;
  if (start !== undefined) return `from line ${start}`;
  if (end !== undefined) return `to line ${end}`;
  return undefined;
}

/** Collapses whitespace and clips, for echoing a query back in a label. */
function quoted(s: string, maxLen = 40): string {
  const flat = s.replace(/\s+/g, " ").trim();
  return `"${flat.length <= maxLen ? flat : `${flat.slice(0, maxLen - 1)}…`}"`;
}

/**
 * Extracts the search pattern from the `match` one-of
 * (`{semantic|literal|regex|glob: string}`), with its mode.
 */
function matchClause(args: Record<string, unknown>): string | undefined {
  const rec = obj(args.match);
  if (rec === undefined) return undefined;
  for (const mode of ["semantic", "literal", "regex", "glob"]) {
    const v = str(rec[mode]);
    if (v !== undefined) return `${mode === "semantic" ? "" : `${mode} `}${quoted(v)}`;
  }
  return undefined;
}

/** Per-tool templates. Returning undefined defers to the generic fallback. */
const TEMPLATES: Record<
  string,
  (args: Record<string, unknown>) => string | undefined
> = {
  read_code: (a) => {
    // Batch form: { reads: [{path}, ...] } — no single path to name.
    const reads = a.reads;
    if (Array.isArray(reads) && reads.length > 0) {
      return reads.length === 1
        ? undefined
        : `Reading ${reads.length} files`;
    }
    const path = pick(a, "path");
    if (path === undefined) return undefined;
    const range = lineRange(a);
    return range ? `Reading ${range} of ${shortPath(path)}` : `Reading ${shortPath(path)}`;
  },

  write_code: (a) => {
    const ops = a.operations;
    if (Array.isArray(ops) && ops.length > 0) return `Editing ${ops.length} file(s)`;
    const path = pick(a, "path");
    if (path === undefined) return undefined;
    // `content` present without base_revision is a create, not an edit.
    const creating = "content" in a && str(a.base_revision) === undefined;
    return `${creating ? "Creating" : "Editing"} ${shortPath(path)}`;
  },

  delete_code: (a) => {
    const paths = a.paths;
    if (Array.isArray(paths) && paths.length > 0) return `Deleting ${paths.length} file(s)`;
    const path = pick(a, "path");
    return path === undefined ? undefined : `Deleting ${shortPath(path)}`;
  },

  search: (a) => {
    const clause = matchClause(a);
    if (clause === undefined) return undefined;
    const scope = str(obj(obj(a.scope)?.code)?.path);
    return scope ? `Searching ${shortPath(scope)} for ${clause}` : `Searching for ${clause}`;
  },

  enumerate: (a) => {
    const filter = obj(a.filter);
    const what = str(filter?.path) ?? str(filter?.glob);
    return what === undefined ? "Listing files" : `Listing files in ${shortPath(what)}`;
  },

  run_command: (a) => {
    const cmd = pick(a, "command");
    return cmd === undefined ? undefined : `Running ${quoted(cmd, 56)}`;
  },

  find_symbol: (a) => {
    const n = pick(a, "name");
    return n === undefined ? undefined : `Finding symbol ${quoted(n)}`;
  },

  traverse_graph: (a) => {
    const mode = pick(a, "mode");
    const seeds = obj(a.seeds);
    const paths = seeds?.paths;
    const seed =
      str(seeds?.path) ??
      str(seeds?.symbol) ??
      (Array.isArray(paths) ? str(paths[0]) : undefined);
    if (mode === undefined) return undefined;
    return seed ? `Analyzing ${mode} of ${shortPath(seed)}` : `Analyzing ${mode}`;
  },

  add_todo: (a) => {
    const d = pick(a, "description");
    return d === undefined ? undefined : `Adding todo ${quoted(d)}`;
  },
  update_todo: (a) => {
    const s = pick(a, "status");
    return s === undefined ? undefined : `Marking todo ${s}`;
  },
  list_todos: () => "Listing todos",
  set_status: (a) => {
    const d = pick(a, "description");
    return d === undefined ? undefined : `Status: ${d}`;
  },
  set_session_title: (a) => {
    const t = pick(a, "title");
    return t === undefined ? undefined : `Retitling session to ${quoted(t)}`;
  },
  update_session_title: (a) => {
    const t = pick(a, "title");
    return t === undefined ? undefined : `Retitling session to ${quoted(t)}`;
  },
  ask_user: (a) => {
    const t = pick(a, "title");
    return t === undefined ? undefined : `Asking: ${t}`;
  },
};

/** `read_code` -> `Read code`, for a readable fallback from a snake_case name. */
function humanizeToolName(name: string): string {
  const words = name.trim().replace(/[_-]+/g, " ").replace(/\s+/g, " ").trim();
  if (words === "") return "Tool";
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/**
 * Generic label for a tool with no template: the humanized name plus the most
 * identifying scalar argument available, so a new or host-registered tool still
 * reads better than a bare name.
 */
function genericLabel(name: string, args: Record<string, unknown>): string {
  const base = humanizeToolName(name);
  const hint =
    pick(args, "path", "file", "name", "query", "command", "title", "description", "id") ??
    matchClause(args);
  if (hint === undefined) return base;
  return `${base}: ${hint.startsWith('"') ? hint : shortPath(hint)}`;
}

/**
 * A short human-readable description of a tool invocation, for display in place
 * of a bare tool name.
 *
 * Always returns a non-empty string. Known tools get a purpose-written phrase
 * ("Reading lines 15-200 of foo.go"); anything else gets its humanized name
 * plus the most identifying argument found.
 */
export function toolInvocationLabel(ev: ToolInvocationLabelInput): string {
  const name = ev.name?.trim() ?? "";
  if (name === "") return "Tool";
  const args = ev.arguments ?? {};
  try {
    return TEMPLATES[name]?.(args) ?? genericLabel(name, args);
  } catch {
    // A template must never take down a render; the name alone is still useful.
    return humanizeToolName(name);
  }
}
