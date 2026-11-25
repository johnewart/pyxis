/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import { useEffect, useId, useRef, useState } from 'react';
import {
  Box,
  Group,
  Loader,
  SegmentedControl,
  Text,
  Textarea,
  useComputedColorScheme,
} from '@mantine/core';
import { useDebouncedValue } from '@mantine/hooks';
import { renderMermaidDiagram } from './mermaidRuntime.js';

const MERMAID_DEBOUNCE_MS = 320;

export type MermaidDiagramProps = {
  code: string;
  /** When true, source view uses a textarea (MDXEditor block). */
  editable?: boolean;
  onCodeChange?: (next: string) => void;
  readOnly?: boolean;
  onSourceFocus?: () => void;
  sourceTextareaRef?: React.RefObject<HTMLTextAreaElement | null>;
  /** Optional trailing control (e.g. delete block in the editor). */
  trailingControl?: React.ReactNode;
  defaultView?: 'chart' | 'source';
};

export function MermaidDiagram({
  code,
  editable = false,
  onCodeChange,
  readOnly = false,
  onSourceFocus,
  sourceTextareaRef,
  trailingControl,
  defaultView = 'chart',
}: MermaidDiagramProps) {
  const reactId = useId().replace(/:/g, '');
  const colorScheme = useComputedColorScheme('light');
  const isDark = colorScheme === 'dark';
  const chartHostRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);

  const [view, setView] = useState<'chart' | 'source'>(defaultView);
  const [renderError, setRenderError] = useState<string | null>(null);
  const [rendering, setRendering] = useState(false);
  const [debouncedCode] = useDebouncedValue(code, MERMAID_DEBOUNCE_MS);

  useEffect(() => {
    if (view !== 'chart') return;

    let cancelled = false;
    const host = chartHostRef.current;

    const run = async () => {
      setRendering(true);
      setRenderError(null);
      if (host) host.innerHTML = '';

      const result = await renderMermaidDiagram(debouncedCode, isDark);
      if (cancelled) return;

      if ('svg' in result) {
        if (host) {
          host.innerHTML = result.svg;
          result.bindFunctions?.(host);
        }
        setRenderError(null);
      } else {
        setRenderError(result.error);
        if (host) host.innerHTML = '';
      }
      setRendering(false);
    };

    void run();

    return () => {
      cancelled = true;
    };
  }, [debouncedCode, isDark, view, reactId]);

  return (
    <Box
      className="mermaid-diagram-embed"
      data-mermaid-code-block={editable ? '' : undefined}
      style={{ display: 'flex', flexDirection: 'column', gap: 8, minWidth: 0 }}
    >
      <Group justify="space-between" gap="xs" wrap="wrap">
        <Text size="sm" fw={600} className="mermaid-diagram-embed__label">
          Diagram
        </Text>
        <Group gap="xs" wrap="nowrap">
          <SegmentedControl
            size="xs"
            value={view}
            onChange={(v) => setView(v as 'chart' | 'source')}
            data={[
              { label: 'Chart', value: 'chart' },
              { label: 'Source', value: 'source' },
            ]}
            classNames={{ root: 'mermaid-diagram-embed__toggle' }}
          />
          {trailingControl}
        </Group>
      </Group>

      {view === 'chart' ? (
        <div className="mermaid-container mermaid-container--chart">
          {rendering ? (
            <Group gap="xs" className="mermaid-diagram-embed__loading">
              <Loader size="xs" />
              <Text size="xs" c="dimmed">
                Rendering…
              </Text>
            </Group>
          ) : null}
          <div
            ref={chartHostRef}
            className="mermaid-diagram-embed__chart-host"
            aria-label="Mermaid diagram"
          />
          {!rendering && !renderError && !debouncedCode.trim() ? (
            <Text size="sm" c="dimmed" className="mermaid-diagram-embed__empty">
              Switch to Source to add diagram code.
            </Text>
          ) : null}
          {renderError ? (
            <Text size="xs" c="red" className="mermaid-diagram-embed__error">
              {renderError}
            </Text>
          ) : null}
        </div>
      ) : editable ? (
        <Textarea
          ref={(el) => {
            textareaRef.current = el;
            if (sourceTextareaRef) {
              (
                sourceTextareaRef as React.MutableRefObject<HTMLTextAreaElement | null>
              ).current = el;
            }
          }}
          value={code}
          readOnly={readOnly}
          onChange={(ev) => onCodeChange?.(ev.currentTarget.value)}
          onFocus={onSourceFocus}
          minRows={5}
          maxRows={18}
          autosize
          spellCheck={false}
          classNames={{ input: 'mermaid-diagram-embed__source-input' }}
          placeholder={'flowchart LR\n  A[Start] --> B[End]'}
        />
      ) : (
        <pre className="mermaid-diagram-embed__source-readonly">
          <code>{code.trim().length ? code : '# No diagram source yet'}</code>
        </pre>
      )}
    </Box>
  );
}

/** ReactMarkdown helper: render ```mermaid fences with the shared embed. */
export function MermaidMarkdownCodeBlock({ code }: { code: string }) {
  return <MermaidDiagram code={code} defaultView="chart" />;
}
