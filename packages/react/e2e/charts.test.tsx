/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Unit-speed (no real llmd binary). `@pyxis-llm/react/charts`: specs are untrusted,
 * so the tests pin what is locked down — no network fetch for a remote data
 * URL, spec-supplied embed options stripped, tooltips that never emit <img> —
 * plus the happy path: a ```vega-lite fence in a chat message renders an SVG
 * chart (which also pins the hyphenated-fence-language lookup). Importing
 * `@pyxis-llm/react/charts` registers the plugin; no provider is needed.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { ChatMessage } from '@pyxis-llm/react';
import {
  VEGA_EXTERNAL_RESOURCE_ERROR,
  VegaChart,
  formatTooltip,
  prepareVegaSpec,
  renderVegaChart,
  renderVegaSvg,
  resolveChartFont,
} from '@pyxis-llm/react/charts';

const BAR_SPEC = {
  data: { values: [{ a: 'A', b: 3 }, { a: 'B', b: 5 }] },
  mark: 'bar',
  encoding: { x: { field: 'a', type: 'nominal' }, y: { field: 'b', type: 'quantitative' } },
};

const escape = (v: unknown) =>
  String(v).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

let consoleError: ReturnType<typeof vi.spyOn>;
beforeEach(() => {
  // jsdom has no canvas; Vega falls back to estimated text metrics and says so.
  consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(() => {
  consoleError.mockRestore();
  vi.restoreAllMocks();
});

describe('renderVegaSvg', () => {
  it('renders a bar spec to a static svg string', async () => {
    const out = await renderVegaSvg(BAR_SPEC, { mode: 'vega-lite' });
    if ('error' in out) throw new Error(out.error);
    expect(out.svg).toContain('<svg');
  });

  it('refuses a remote data url', async () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal('fetch', fetchSpy);
    let out: Awaited<ReturnType<typeof renderVegaSvg>>;
    try {
      out = await renderVegaSvg(
        { ...BAR_SPEC, data: { url: 'https://evil.test/data.json' } },
        { mode: 'vega-lite' }
      );
    } finally {
      vi.unstubAllGlobals();
    }
    expect(fetchSpy).not.toHaveBeenCalled();
    const text = 'error' in out ? out.error : out.problems.join(' ');
    expect(text).toContain(VEGA_EXTERNAL_RESOURCE_ERROR);
  });

  it('ignores usermeta.embedOptions', async () => {
    const spec = {
      ...BAR_SPEC,
      usermeta: { embedOptions: { config: { background: '#ff0000' }, theme: 'dark' } },
    };
    const out = await renderVegaSvg(spec, { mode: 'vega-lite' });
    if ('error' in out) throw new Error(out.error);
    expect(out.svg).toContain('<svg');
    expect(out.svg).not.toContain('#ff0000');
    expect(out.svg).not.toContain('rgb(255, 0, 0)');
  });
});

describe('prepareVegaSpec', () => {
  it('strips spec-supplied embed options and never mutates the input', () => {
    const spec = {
      ...BAR_SPEC,
      usermeta: { embedOptions: { loader: { baseURL: 'https://evil.test/' }, actions: true }, note: 'kept' },
    };
    const snapshot = JSON.stringify(spec);
    const out = prepareVegaSpec(spec, 'vega-lite');
    expect((out.usermeta as Record<string, unknown>).embedOptions).toBeUndefined();
    expect((out.usermeta as Record<string, unknown>).note).toBe('kept');
    expect(JSON.stringify(spec)).toBe(snapshot);
  });

  it('sizes single-view vega-lite charts to the container, but not facets, concats or explicit widths', () => {
    expect(prepareVegaSpec(BAR_SPEC, 'vega-lite').width).toBe('container');
    expect(prepareVegaSpec({ ...BAR_SPEC, width: 300 }, 'vega-lite').width).toBe(300);
    const faceted = { ...BAR_SPEC, encoding: { ...BAR_SPEC.encoding, column: { field: 'a' } } };
    expect(prepareVegaSpec(faceted, 'vega-lite').width).toBeUndefined();
    expect(prepareVegaSpec({ hconcat: [BAR_SPEC] }, 'vega-lite').width).toBeUndefined();
    expect(prepareVegaSpec(BAR_SPEC, 'vega').width).toBeUndefined();
  });
});

describe('chart font', () => {
  it('drops every font choice a spec makes', () => {
    const spec = {
      ...BAR_SPEC,
      mark: { type: 'text', font: 'Courier' },
      title: { text: 'T', font: 'monospace', subtitleFont: 'monospace' },
      config: { font: 'monospace', axis: { labelFont: 'Courier', titleFont: 'Courier', labelColor: 'red' } },
    };
    const out = prepareVegaSpec(spec, 'vega-lite') as Record<string, Record<string, unknown>>;
    expect(out.mark.font).toBeUndefined();
    expect(out.title.font).toBeUndefined();
    expect(out.config.font).toBeUndefined();
    expect(out.config.axis).toEqual({ labelColor: 'red' });
  });

  it('scales every font size a spec sets, but never touches data', () => {
    const spec = {
      ...BAR_SPEC,
      data: { values: [{ fontSize: 10 }] },
      config: { axis: { labelFontSize: 10 } },
      mark: { type: 'text', fontSize: 20 },
    };
    const out = prepareVegaSpec(spec, 'vega-lite', 1.5) as Record<string, Record<string, unknown>>;
    expect((out.config.axis as Record<string, number>).labelFontSize).toBe(15);
    expect(out.mark.fontSize).toBe(30);
    expect((out.data.values as Array<Record<string, number>>)[0].fontSize).toBe(10);
  });

  it('defaults to the page body font, and an explicit font wins', () => {
    document.body.style.fontFamily = "'Reddit Sans', sans-serif";
    expect(resolveChartFont()).toContain('Reddit Sans');
    expect(resolveChartFont('Inter')).toBe('Inter');
    document.body.style.fontFamily = '';
  });

  it('renders labels in the app font, not the one the spec asks for', async () => {
    document.body.style.fontFamily = "'Reddit Sans', sans-serif";
    const el = document.createElement('div');
    document.body.appendChild(el);
    const spec = { ...BAR_SPEC, config: { font: 'monospace' } };
    const result = await renderVegaChart(el, spec, { mode: 'vega-lite', isDark: false });
    const fonts = [...el.querySelectorAll('svg text')].map((t) => t.getAttribute('font-family') ?? '');
    expect(fonts.length).toBeGreaterThan(0);
    for (const f of fonts) expect(f).toContain('Reddit Sans');
    if ('finalize' in result) result.finalize();
    el.remove();
    document.body.style.fontFamily = '';
  });
});

describe('formatTooltip', () => {
  it('renders an image field as escaped text, never as <img>', () => {
    const html = formatTooltip({ title: 'T', image: 'https://evil.test/p.png?x=1', v: '<b>' }, escape);
    expect(html).not.toContain('<img');
    expect(html).toContain('https://evil.test/p.png?x=1');
    expect(html).toContain('&lt;b&gt;');
  });
});

describe('renderVegaChart', () => {
  it('renders an inline-data chart to SVG', async () => {
    const el = document.createElement('div');
    document.body.appendChild(el);
    const result = await renderVegaChart(el, BAR_SPEC, { mode: 'vega-lite', isDark: false });
    expect('finalize' in result && result.problems).toEqual([]);
    expect(el.querySelector('svg')).not.toBeNull();
    if ('finalize' in result) result.finalize();
    el.remove();
  });

  it('refuses a remote data URL without making any request, and reports why', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    const el = document.createElement('div');
    document.body.appendChild(el);
    const spec = { ...BAR_SPEC, data: { url: 'https://example.test/data.csv' } };
    const result = await renderVegaChart(el, spec, { mode: 'vega-lite', isDark: false });
    expect(fetchSpy).not.toHaveBeenCalled();
    const reported = 'error' in result ? [result.error] : result.problems;
    expect(reported).toContain(VEGA_EXTERNAL_RESOURCE_ERROR);
    if ('finalize' in result) result.finalize();
    el.remove();
  });

  it('returns an error (does not throw) for a spec Vega cannot compile', async () => {
    const el = document.createElement('div');
    const result = await renderVegaChart(el, { mark: { type: 'no-such-mark' } }, { mode: 'vega-lite', isDark: false });
    expect('error' in result).toBe(true);
  });
});

describe('renderVegaChart host loader', () => {
  const CSV = 'a,b\nA,3\nB,5\n';
  const csvSpec = { ...BAR_SPEC, data: { url: 'doc/sales.csv', format: { type: 'csv' } } };
  const IMAGE_SPEC = {
    data: { values: [{ x: 1, y: 1, u: 'https://img.test/p.png' }] },
    mark: { type: 'image', width: 20, height: 20 },
    encoding: {
      x: { field: 'x', type: 'quantitative' },
      y: { field: 'y', type: 'quantitative' },
      url: { field: 'u', type: 'nominal' },
    },
  };
  const LINK_SPEC = {
    data: { values: [{ a: 'A', b: 3, h: 'https://link.test/' }] },
    mark: 'bar',
    encoding: {
      x: { field: 'a', type: 'nominal' },
      y: { field: 'b', type: 'quantitative' },
      href: { field: 'h', type: 'nominal' },
    },
  };

  // `inspect` runs while the chart is still mounted, before finalize.
  async function run(
    spec: Record<string, unknown>,
    loader?: (url: string) => Promise<string>,
    inspect?: (el: HTMLElement, problems: string[]) => Promise<void> | void
  ) {
    const el = document.createElement('div');
    document.body.appendChild(el);
    const result = await renderVegaChart(el, spec, { mode: 'vega-lite', isDark: false, loader });
    const problems = 'error' in result ? [result.error] : result.problems;
    if (inspect) await inspect(el, problems);
    const reported = [...problems];
    if ('finalize' in result) result.finalize();
    el.remove();
    return { reported, el };
  }

  const IMG_URL = 'https://img.test/p.png';
  async function expectImageRefused(loader?: (url: string) => Promise<string>) {
    let images = 0;
    await run(IMAGE_SPEC, loader, async (el) => {
      await new Promise((r) => setTimeout(r, 20));
      for (const img of el.querySelectorAll('image')) {
        images++;
        const refs = [img.getAttribute('href'), img.getAttribute('xlink:href')];
        for (const ref of refs) expect(ref ?? '').not.toContain(IMG_URL);
      }
    });
    expect(images).toBeGreaterThan(0);
  }
  async function expectLinkRefused(loader?: (url: string) => Promise<string>) {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null);
    await run(LINK_SPEC, loader, async (el, problems) => {
      const bar = el.querySelector('svg .mark-rect path');
      expect(bar).not.toBeNull();
      bar!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      await new Promise((r) => setTimeout(r, 20));
      expect(open).not.toHaveBeenCalled();
      expect(problems).toContain(VEGA_EXTERNAL_RESOURCE_ERROR);
    });
  }

  it('without a loader refuses data, image and link loads', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    const { reported } = await run(csvSpec);
    expect(reported).toContain(VEGA_EXTERNAL_RESOURCE_ERROR);
    await expectImageRefused();
    await expectLinkRefused();
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it('with a loader, loads a data url through it', async () => {
    const loader = vi.fn(async () => CSV);
    const { reported } = await run(csvSpec, loader);
    expect(loader).toHaveBeenCalledWith('doc/sales.csv');
    expect(reported).toEqual([]);
  });

  it('with a loader, still refuses image marks and links', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    const loader = vi.fn(async () => CSV);
    await expectImageRefused(loader);
    await expectLinkRefused(loader);
    expect(loader).not.toHaveBeenCalled();
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it('a spec cannot override the loader through usermeta.embedOptions', async () => {
    const evil = vi.fn(async () => CSV);
    (globalThis as Record<string, unknown>).__evilLoader = evil;
    const loader = vi.fn(async () => CSV);
    const spec = {
      ...csvSpec,
      usermeta: { embedOptions: { loader: { load: evil, sanitize: evil, http: evil, file: evil } } },
    };
    await run(spec, loader);
    expect(loader).toHaveBeenCalled();
    expect(evil).not.toHaveBeenCalled();
    delete (globalThis as Record<string, unknown>).__evilLoader;
  });
});

describe('VegaChart source editing', () => {
  let container: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(() => {
    act(() => root.unmount());
    container.remove();
  });
  const src = JSON.stringify(BAR_SPEC);
  function show(props: Partial<React.ComponentProps<typeof VegaChart>>) {
    act(() => {
      root.render(
        <MantineProvider>
          <VegaChart source={src} defaultView="source" {...props} />
        </MantineProvider>
      );
    });
  }

  it('is a read-only <pre> by default', () => {
    show({});
    expect(container.querySelector('pre.vega-chart-embed__source')).not.toBeNull();
    expect(container.querySelector('textarea')).toBeNull();
  });

  it('is an editable textarea that reports edits and focus', () => {
    const onSourceChange = vi.fn();
    const onSourceFocus = vi.fn();
    show({ onSourceChange, onSourceFocus });
    const ta = container.querySelector('textarea') as HTMLTextAreaElement;
    expect(ta).not.toBeNull();
    expect(ta.value).toBe(src);
    expect(container.querySelector('pre')).toBeNull();
    act(() => ta.focus());
    expect(onSourceFocus).toHaveBeenCalled();
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
    act(() => {
      setter.call(ta, '{"mark":"line"}');
      ta.dispatchEvent(new Event('input', { bubbles: true }));
    });
    expect(onSourceChange).toHaveBeenCalledWith('{"mark":"line"}');
  });

  it('disables the textarea when readOnly', () => {
    show({ onSourceChange: vi.fn(), readOnly: true });
    expect((container.querySelector('textarea') as HTMLTextAreaElement).disabled).toBe(true);
  });

  it('renders footerAction in the footer next to the toggle', () => {
    show({ footerAction: <button data-testid="extra">Extra</button> });
    const footer = container.querySelector('.vega-chart-embed__footer')!;
    expect(footer.querySelector('[data-testid="extra"]')).not.toBeNull();
    expect(footer.querySelector('.vega-chart-embed__toggle')).not.toBeNull();
  });
});

describe('vega-lite fence in a chat message', () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(() => {
    act(() => root.unmount());
    container.remove();
  });

  function renderMessage(content: string) {
    act(() => {
      root.render(
        <MantineProvider>
          <ChatMessage message={{ role: 'assistant', content, id: 'm1' }} />
        </MantineProvider>
      );
    });
  }

  it('renders the fence as an interactive chart', async () => {
    renderMessage('Here you go:\n\n```vega-lite\n' + JSON.stringify(BAR_SPEC) + '\n```\n');
    await vi.waitFor(() => {
      expect(container.querySelector('.vega-chart-embed__host svg')).not.toBeNull();
    }, { timeout: 10_000 });
    expect(container.querySelector('pre code.language-vega-lite')).toBeNull();
  });

  it('shows a titled spec\'s title once (in the chart), not again in the header', async () => {
    renderMessage('```vega-lite\n' + JSON.stringify({ ...BAR_SPEC, title: 'Monthly spend' }) + '\n```\n');
    await vi.waitFor(() => {
      expect(container.querySelector('.vega-chart-embed__host svg')).not.toBeNull();
    }, { timeout: 10_000 });
    expect(container.querySelector('.vega-chart-embed__label')).toBeNull();
    const svgText = [...container.querySelectorAll('.vega-chart-embed__host svg text')].map((t) => t.textContent);
    expect(svgText).toContain('Monthly spend');
  });

  it('A+ re-renders the chart with larger text, and the controls sit below the chart', async () => {
    localStorage.removeItem('llmd.vegaChart.fontScale');
    renderMessage('```vega-lite\n' + JSON.stringify(BAR_SPEC) + '\n```\n');
    const axisLabelSize = () =>
      Number(container.querySelector('.vega-chart-embed__host svg .role-axis-label text')?.getAttribute('font-size')?.replace('px', ''));
    await vi.waitFor(() => expect(axisLabelSize()).toBeGreaterThan(0), { timeout: 10_000 });
    const before = axisLabelSize();

    const footer = container.querySelector('.vega-chart-embed__footer')!;
    const host = container.querySelector('.vega-chart-embed__host')!;
    // DOCUMENT_POSITION_FOLLOWING: the footer comes after the chart.
    expect(host.compareDocumentPosition(footer) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(footer.querySelector('.vega-chart-embed__toggle')).not.toBeNull();
    // The … button: vega-embed pins it top-right of the host; our CSS must
    // actually apply (vega-embed puts .vega-embed ON the host, so a
    // descendant selector silently matches nothing).
    expect(host.classList.contains('vega-embed')).toBe(true);
    expect(getComputedStyle(host).position).toBe('static');
    const summary = host.querySelector('summary')!;
    expect(summary).not.toBeNull();
    expect(getComputedStyle(summary).top).toBe('auto');
    expect(getComputedStyle(summary).bottom).toBe('0px');

    act(() => {
      (container.querySelector('[aria-label="Larger chart text"]') as HTMLButtonElement).click();
    });
    await vi.waitFor(() => expect(axisLabelSize()).toBeGreaterThan(before), { timeout: 10_000 });
    expect(localStorage.getItem('llmd.vegaChart.fontScale')).toBe('1.15');
    localStorage.removeItem('llmd.vegaChart.fontScale');
  });

  it('shows a quiet notice, not an error, for an incomplete (still streaming) spec', async () => {
    renderMessage('```vega-lite\n{"data": {"values": [\n```\n');
    expect(container.querySelector('.vega-chart-embed__invalid')).not.toBeNull();
    expect(container.querySelector('.vega-chart-embed__error')).toBeNull();
  });
});
