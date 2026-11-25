/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

type MermaidInstance = typeof import('mermaid').default;

let mermaidPromise: Promise<MermaidInstance> | null = null;
let mermaidTheme: 'light' | 'dark' | null = null;
let elkLayoutAvailable = false;
let diagramCounter = 0;

function buildMermaidConfig(isDark: boolean) {
  return {
    startOnLoad: false,
    theme: isDark ? 'dark' : 'default',
    // Never let mermaid paint its own "Syntax error in text" diagram. We surface
    // parse failures inline via RenderMermaidResult.error instead; without this
    // mermaid draws the error SVG into a temp node it appends to <body>.
    suppressErrorRendering: true,
    securityLevel: 'strict' as const,
    fontFamily: 'inherit',
    flowchart: {
      useMaxWidth: true,
      htmlLabels: true,
      padding: 18,
      nodeSpacing: 50,
      rankSpacing: 55,
      curve: 'basis' as const,
      wrappingWidth: 220,
    },
    sequence: {
      useMaxWidth: true,
      wrap: true,
      messageFontSize: 14,
    },
    er: {
      useMaxWidth: true,
    },
    ...(elkLayoutAvailable ? { layout: 'elk' as const } : {}),
  };
}

async function loadMermaid(isDark: boolean): Promise<MermaidInstance> {
  if (mermaidPromise && mermaidTheme === (isDark ? 'dark' : 'light')) {
    return mermaidPromise;
  }

  mermaidTheme = isDark ? 'dark' : 'light';
  mermaidPromise = (async () => {
    const mermaidMod = await import('mermaid');
    const mermaid = mermaidMod.default;

    let elkRegistered = false;
    try {
      // @vite-ignore: this is an OPTIONAL peer of mermaid, not a hard
      // dependency of @pyxis-llm/react (see mermaid-layout-elk.d.ts). Without the
      // ignore hint, bundlers that statically analyze dynamic imports (Vite)
      // fail the build/dev-server transform when it isn't installed, even
      // though this try/catch already handles that at runtime.
      const elkMod = await import(/* @vite-ignore */ '@mermaid-js/layout-elk');
      mermaid.registerLayoutLoaders(elkMod.default);
      elkRegistered = true;
    } catch {
      elkRegistered = false;
    }
    elkLayoutAvailable = elkRegistered;

    mermaid.initialize(
      buildMermaidConfig(isDark) as Parameters<typeof mermaid.initialize>[0]
    );

    return mermaid;
  })();

  return mermaidPromise;
}

/**
 * Normalize diagram source before handing it to mermaid.
 *
 * Historically this rewrote `flowchart` / `graph TD` to the `flowchart-elk`
 * diagram keyword. In mermaid v11 ELK is a *layout* selected via
 * `layout: 'elk'` (set in buildMermaidConfig); the `flowchart-elk` keyword is
 * legacy and does nothing more than set that same layout flag. The rewrite was
 * therefore redundant, and its multiline regexes could rewrite a `flowchart`
 * line appearing inside a diagram body. Source is now passed through as-is.
 */
export function normalizeMermaidSource(code: string): string {
  return code.trim();
}

export type RenderMermaidResult =
  | { svg: string; bindFunctions?: (element: Element) => void }
  | { error: string };

export async function renderMermaidDiagram(
  code: string,
  isDark: boolean
): Promise<RenderMermaidResult> {
  const trimmed = code.trim();
  if (!trimmed) {
    return { error: 'Diagram source is empty.' };
  }

  const id = `mermaid-diagram-${++diagramCounter}-${Date.now()}`;
  // Own the scratch node ourselves so mermaid never appends to <body>.
  const container = createOffscreenContainer();

  try {
    const mermaid = await loadMermaid(isDark);
    const normalized = normalizeMermaidSource(trimmed);
    const result = await mermaid.render(id, normalized, container ?? undefined);
    return {
      svg: result.svg,
      bindFunctions: result.bindFunctions,
    };
  } catch (e) {
    return {
      error: e instanceof Error ? e.message : String(e),
    };
  } finally {
    cleanupMermaidScratchNodes(id, container);
  }
}

function createOffscreenContainer(): HTMLElement | null {
  if (typeof document === 'undefined') return null;
  const el = document.createElement('div');
  el.setAttribute('aria-hidden', 'true');
  el.style.position = 'absolute';
  el.style.left = '-10000px';
  el.style.top = '0';
  el.style.width = '0';
  el.style.height = '0';
  el.style.overflow = 'hidden';
  el.style.pointerEvents = 'none';
  document.body.appendChild(el);
  return el;
}

/**
 * Remove every node mermaid may have left behind for `id`. Even with
 * suppressErrorRendering and a caller-owned container, older/edge paths inside
 * mermaid append temp nodes keyed on `#id` and `#d{id}` directly to <body>;
 * if render throws they are never reclaimed and leak visible stray text.
 */
function cleanupMermaidScratchNodes(id: string, container: HTMLElement | null): void {
  if (typeof document === 'undefined') return;
  document.getElementById(id)?.remove();
  document.getElementById(`d${id}`)?.remove();
  container?.remove();
}

/** Reset cached mermaid instance (tests / hot reload). */
export function resetMermaidRuntimeForTests(): void {
  mermaidPromise = null;
  mermaidTheme = null;
  elkLayoutAvailable = false;
  diagramCounter = 0;
}
