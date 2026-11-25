/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

// jsdom does not implement window.matchMedia; Mantine's color-scheme hooks
// (used by MermaidDiagram's useComputedColorScheme) call it unconditionally.
// This is the standard minimal polyfill for that gap in a test environment.
if (typeof window !== 'undefined' && !window.matchMedia) {
  window.matchMedia = (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }) as unknown as MediaQueryList;
}

// jsdom does not implement ResizeObserver; Mantine's SegmentedControl (used
// by MermaidDiagram's chart/source toggle) observes element size to animate
// its indicator. A no-op stub is sufficient for rendering assertions.
if (typeof window !== 'undefined' && !window.ResizeObserver) {
  window.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
}
