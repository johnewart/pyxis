/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * `@pyxis-llm/react/charts` — interactive Vega-Lite / Vega charts in chat:
 * ```vega-lite (alias ```vegalite) and ```vega fences, and `chart` display
 * cards (@pyxis-llm/core CHART_CARD_TYPE).
 *
 *   import '@pyxis-llm/react/charts';     // that's all: it registers itself
 *
 * Importing this entry point registers `chartsPlugin` globally (see
 * registerLlmdPlugin), so a host opts in by importing it. It is also
 * exported, for a host that would rather scope it with LlmdPluginProvider —
 * call `unregisterCharts()` first in that case.
 *
 * Requires the optional peers `vega`, `vega-lite` and `vega-embed`; they are
 * loaded lazily, on the first chart, so importing this costs nothing until a
 * chart appears. Specs are treated as untrusted: see vegaRuntime.ts for what
 * is locked down.
 */

import { createElement, useMemo } from 'react';
import { CHART_CARD_TYPE, decodeChartBody, type ChartBody } from '@pyxis-llm/core';
import { registerLlmdPlugin, type DisplayCardRenderProps, type LlmdPlugin } from './plugins.js';
import { VegaChart, VegaLiteMarkdownCodeBlock, VegaMarkdownCodeBlock } from './VegaChart.js';

export type { VegaChartProps } from './VegaChart.js';
export { VegaChart, VegaLiteMarkdownCodeBlock, VegaMarkdownCodeBlock, VEGA_FONT_SCALES } from './VegaChart.js';
export type { RenderVegaOptions, RenderVegaResult, RenderVegaSvgOptions, RenderVegaSvgResult, VegaDataLoader, VegaMode } from './vegaRuntime.js';
export {
  VEGA_EXTERNAL_RESOURCE_ERROR,
  chartFontConfig,
  formatTooltip,
  resolveChartFont,
  prepareVegaSpec,
  renderVegaChart,
  renderVegaSvg,
  resetVegaRuntimeForTests,
} from './vegaRuntime.js';

/** A `chart` display card: the spec rendered by VegaChart, which draws its own title and caption. */
export function ChartCardView({ card }: DisplayCardRenderProps<ChartBody>) {
  const source = useMemo(() => JSON.stringify(card.body.spec), [card.body.spec]);
  return createElement(VegaChart, { source, mode: card.body.format, title: card.title, caption: card.caption });
}

export const chartsPlugin: LlmdPlugin = {
  id: 'llmd.charts',
  languages: {
    'vega-lite': VegaLiteMarkdownCodeBlock,
    vegalite: VegaLiteMarkdownCodeBlock,
    vega: VegaMarkdownCodeBlock,
  },
  displayCards: {
    [CHART_CARD_TYPE]: { decode: decodeChartBody, render: ChartCardView, ownsChrome: true },
  },
};

/** Registered on import; this undoes it. */
export const unregisterCharts = registerLlmdPlugin(chartsPlugin);
