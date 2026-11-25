/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Unit-speed (no real llmd binary). Display cards are records, not prompts:
 * they must never be dropped just because the agent did not place an inline
 * tag. Pins where @pyxis-llm/core puts them (right after the tool result that
 * produced them, else at the end) and that ChatPanel / ChatMessage render
 * them with no host code: images are built in, charts come from importing
 * `@pyxis-llm/react/charts`.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MantineProvider } from '@mantine/core';
import {
  ANCHORLESS_CARD_MESSAGE_ID_PREFIX,
  DISPLAY_CARD_KIND,
  appendAnchorlessCardMessages,
  buildDisplayCardAttachment,
  chartDisplayCard,
  formatAttachmentTag,
  imageCardDataUrl,
  imageDisplayCard,
  llmdMessageAttachmentFromRowContent,
  llmdMessageAttachmentRowContent,
  mergeLiveAttachments,
  withLlmdAttachmentKindPlugins,
  type LlmdMessageAttachment,
} from '@pyxis-llm/core';
import { ChatMessage, ChatPanel, registerLlmdPlugin, type ChatMessageData } from '@pyxis-llm/react';
import { chartsPlugin, unregisterCharts } from '@pyxis-llm/react/charts';

const PNG_B64 =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=';

const image = (id: string, afterToolCallId?: string): LlmdMessageAttachment =>
  buildDisplayCardAttachment(
    imageDisplayCard(id, { src: imageCardDataUrl('image/png', PNG_B64), mimeType: 'image/png' }, { title: `Plot ${id}` }),
    afterToolCallId ? { afterToolCallId } : {}
  );

const BAR_SPEC = {
  data: { values: [{ a: 'A', b: 3 }, { a: 'B', b: 5 }] },
  mark: 'bar',
  encoding: { x: { field: 'a', type: 'nominal' }, y: { field: 'b', type: 'quantitative' } },
};

const chart = (id: string, afterToolCallId?: string): LlmdMessageAttachment =>
  buildDisplayCardAttachment(
    chartDisplayCard(id, { format: 'vega-lite', spec: BAR_SPEC }, { title: 'Spend' }),
    afterToolCallId ? { afterToolCallId } : {}
  );

/** user → assistant(call_1) → tool(call_1) → assistant(final). */
const transcript = (): ChatMessageData[] => [
  { role: 'user', content: 'plot it', id: 'u1' },
  { role: 'assistant', content: 'Running the script.', id: 'a1', toolCalls: [{ id: 'call_1', name: 'run' }] },
  { role: 'tool', content: 'ok', id: 't1', toolCallId: 'call_1' },
  { role: 'assistant', content: 'Here is the plot.', id: 'a2' },
];

const index = (...atts: LlmdMessageAttachment[]) => new Map(atts.map((a) => [a.id, a]));

const place = (
  messages: ChatMessageData[],
  atts: Map<string, LlmdMessageAttachment>,
  plugins = withLlmdAttachmentKindPlugins()
) => appendAnchorlessCardMessages(messages as never, atts, plugins) as unknown as ChatMessageData[];

const ids = (messages: ChatMessageData[]) => messages.map((m) => m.id);
const row = (id: string) => `${ANCHORLESS_CARD_MESSAGE_ID_PREFIX}${id}`;

describe('placement of display cards', () => {
  it('goes right after the tool result that produced it', () => {
    expect(ids(place(transcript(), index(image('img1', 'call_1'))))).toEqual(['u1', 'a1', 't1', row('img1'), 'a2']);
  });

  it('keeps several cards from one tool call in order', () => {
    const out = place(transcript(), index(image('img1', 'call_1'), chart('ch1', 'call_1')));
    expect(ids(out)).toEqual(['u1', 'a1', 't1', row('img1'), row('ch1'), 'a2']);
  });

  it('falls back to after the calling assistant turn when the tool row is absent', () => {
    const noToolRow = transcript().filter((m) => m.role !== 'tool');
    expect(ids(place(noToolRow, index(image('img1', 'call_1'))))).toEqual(['u1', 'a1', row('img1'), 'a2']);
  });

  it('goes at the end when the tool call is not in the transcript (live turn)', () => {
    expect(ids(place(transcript(), index(image('img1', 'call_live'))))).toEqual(['u1', 'a1', 't1', 'a2', row('img1')]);
    expect(ids(place(transcript(), index(image('img2'))))).toEqual(['u1', 'a1', 't1', 'a2', row('img2')]);
  });

  it('goes at the end of the turn it was made in when its tool call is not in the transcript (CLI tools over MCP)', () => {
    const twoTurns: ChatMessageData[] = [
      { role: 'user', content: 'plot it', id: 'u1' },
      { role: 'assistant', content: 'Here is the plot.', id: 'a1' },
      { role: 'user', content: 'thanks', id: 'u2' },
      { role: 'assistant', content: 'Sure.', id: 'a2' },
    ];
    const made = (id: string) => ({ ...chart(id, `mcp-${id}`), turnMessageId: 'u1' });
    expect(ids(place(twoTurns, index(made('ch1'), made('ch2'))))).toEqual([
      'u1',
      'a1',
      row('ch1'),
      row('ch2'),
      'u2',
      'a2',
    ]);
    // An unknown turn (not loaded, or recorded before llmd kept it) still gets a row.
    const lost = { ...chart('ch3', 'mcp-ch3'), turnMessageId: 'u-gone' };
    expect(ids(place(twoTurns, index(lost)))).toEqual(['u1', 'a1', 'u2', 'a2', row('ch3')]);
  });

  it('reads the recorded turn from a stored row and keeps it over a live update', () => {
    const stored = llmdMessageAttachmentFromRowContent(
      JSON.stringify({ ...JSON.parse(llmdMessageAttachmentRowContent(chart('ch1', 'mcp-1'), 'mcp-1')), turn_message_id: 'u1' })
    )!;
    expect(stored.turnMessageId).toBe('u1');
    const live = new Map([['ch1', { ...chart('ch1', 'mcp-1'), turnMessageId: 'u9' }]]);
    expect(mergeLiveAttachments(index(stored), live).get('ch1')?.turnMessageId).toBe('u1');
    const fresh = new Map([['ch2', { ...chart('ch2', 'mcp-2'), turnMessageId: 'u9' }]]);
    expect(mergeLiveAttachments(index(stored), fresh).get('ch2')?.turnMessageId).toBe('u9');
  });

  it('gets no row when the agent placed its tag inline', () => {
    const msgs = transcript();
    msgs[3] = { ...msgs[3]!, content: `Here is the plot.${formatAttachmentTag('img1')}` };
    expect(ids(place(msgs, index(image('img1', 'call_1'))))).toEqual(['u1', 'a1', 't1', 'a2']);
  });

  it('is idempotent', () => {
    const atts = index(image('img1', 'call_1'));
    const once = place(transcript(), atts);
    expect(ids(place(once, atts))).toEqual(ids(once));
  });

  it('leaves non-persistent, non-pending kinds alone (unchanged behavior)', () => {
    const other: LlmdMessageAttachment = { id: 'x', kind: 'host.thing', payloadJson: '{"status":"done"}' };
    expect(ids(place(transcript(), index(other)))).toEqual(['u1', 'a1', 't1', 'a2']);
  });

  it('lets a host plugin override llmd behavior for its kinds', () => {
    const hidden = withLlmdAttachmentKindPlugins([{ kind: DISPLAY_CARD_KIND, rendersInline: false }]);
    expect(ids(place(transcript(), index(image('img1', 'call_1')), hidden))).toEqual(['u1', 'a1', 't1', 'a2']);
  });
});

describe('end-of-turn placement', () => {
  const OFFER_KIND = 'host.offer';
  const offer = (id: string, extra: Partial<LlmdMessageAttachment> = {}): LlmdMessageAttachment => ({
    id,
    kind: OFFER_KIND,
    payloadJson: JSON.stringify({ status: 'pending' }),
    ...extra,
  });
  const plugins = withLlmdAttachmentKindPlugins([{ kind: OFFER_KIND, endOfTurn: true }]);

  it('puts a mid-turn tag at the end of its turn, before the next user message', () => {
    const msgs: ChatMessageData[] = [
      { role: 'user', content: 'build it', id: 'u1' },
      { role: 'assistant', content: `Offer${formatAttachmentTag('o1')} then more.`, id: 'a1', toolCalls: [{ id: 'call_1', name: 'offer' }] },
      { role: 'tool', content: 'ok', id: 't1', toolCallId: 'call_1' },
      { role: 'assistant', content: 'Done.', id: 'a2' },
      { role: 'user', content: 'thanks', id: 'u2' },
      { role: 'assistant', content: 'Sure.', id: 'a3' },
    ];
    expect(ids(place(msgs, index(offer('o1', { afterToolCallId: 'call_1' })), plugins))).toEqual([
      'u1', 'a1', 't1', 'a2', row('o1'), 'u2', 'a3',
    ]);
    expect(ids(place(msgs, index(offer('o1', { turnMessageId: 'u1' })), plugins))).toEqual([
      'u1', 'a1', 't1', 'a2', row('o1'), 'u2', 'a3',
    ]);
  });

  it('goes at the end of the transcript while the turn is still running', () => {
    const msgs: ChatMessageData[] = [
      { role: 'user', content: 'build it', id: 'u1' },
      { role: 'assistant', content: 'Working.', id: 'a1' },
    ];
    expect(ids(place(msgs, index(offer('o1', { turnMessageId: 'u1' })), plugins))).toEqual(['u1', 'a1', row('o1')]);
  });

  it('is placed once and idempotent', () => {
    const atts = index(offer('o1', { turnMessageId: 'u1' }));
    const msgs: ChatMessageData[] = [
      { role: 'user', content: 'x', id: 'u1' },
      { role: 'assistant', content: 'ok', id: 'a1' },
      { role: 'user', content: 'y', id: 'u2' },
    ];
    const once = place(msgs, atts, plugins);
    expect(ids(place(once, atts, plugins))).toEqual(ids(once));
    expect(ids(once).filter((i) => i === row('o1'))).toHaveLength(1);
  });

  const twoTurns = (): ChatMessageData[] => [
    { role: 'user', content: 'build it', id: 'u1' },
    { role: 'assistant', content: `Offer${formatAttachmentTag('o1')} text.`, id: 'a1' },
    { role: 'assistant', content: 'Final.', id: 'a2' },
    { role: 'user', content: 'next', id: 'u2' },
    { role: 'assistant', content: 'Sure.', id: 'a3' },
  ];

  it('places a card with no turnMessageId or a stale one at the end of the turn holding its tag', () => {
    const expected = ['u1', 'a1', 'a2', row('o1'), 'u2', 'a3'];
    expect(ids(place(twoTurns(), index(offer('o1')), plugins))).toEqual(expected);
    expect(ids(place(twoTurns(), index(offer('o1', { turnMessageId: 'u-stale' })), plugins))).toEqual(expected);
  });

  it('renders such cards once in a turn older than fullRenderTail, with no placeholder', () => {
    const container = document.createElement('div');
    document.body.appendChild(container);
    const root = createRoot(container);
    const unregister = registerLlmdPlugin({
      id: 'test-offer-old',
      attachments: {
        [OFFER_KIND]: {
          placement: { endOfTurn: true },
          render: ({ attachmentId }) => <div data-testid={`offer-${attachmentId}`}>offer</div>,
        },
      },
    });
    try {
      const msgs = twoTurns();
      for (let i = 0; i < 15; i++) {
        msgs.push({ role: 'user', content: `q${i}`, id: `uq${i}` }, { role: 'assistant', content: `r${i}`, id: `ar${i}` });
      }
      act(() => {
        root.render(
          <MantineProvider>
            <ChatPanel messages={msgs} onSend={() => {}} attachmentIndex={index(offer('o1', { turnMessageId: 'u-stale' }))} />
          </MantineProvider>
        );
      });
      expect(container.querySelectorAll('[data-testid="offer-o1"]')).toHaveLength(1);
      expect(container.textContent).not.toContain('[card unavailable]');
      const rows = Array.from(container.querySelectorAll('[data-message-id]')).map((el) => el.getAttribute('data-message-id'));
      expect(rows.slice(0, 6)).toEqual(['u1', 'a1', 'a2', row('o1'), 'u2', 'a3']);
    } finally {
      act(() => root.unmount());
      container.remove();
      unregister();
    }
  });

  it('does not scroll back to the bottom when an older turn card resolves while the reader is scrolled up', () => {
    const container = document.createElement('div');
    document.body.appendChild(container);
    const root = createRoot(container);
    const unregister = registerLlmdPlugin({
      id: 'test-offer-scroll',
      attachments: {
        [OFFER_KIND]: {
          placement: { endOfTurn: true },
          render: ({ attachmentId }) => <div data-testid={`offer-${attachmentId}`}>offer</div>,
        },
      },
    });
    try {
      const panelRef = React.createRef<{ getViewport: () => HTMLElement | null }>();
      const renderPanel = (msgs: ChatMessageData[], atts?: Map<string, LlmdMessageAttachment>) =>
        act(() => {
          root.render(
            <MantineProvider>
              <ChatPanel ref={panelRef as never} messages={msgs} onSend={() => {}} attachmentIndex={atts} />
            </MantineProvider>
          );
        });
      const rowIds = () =>
        Array.from(container.querySelectorAll('[data-message-id]')).map((el) => el.getAttribute('data-message-id'));
      const scrollTo = (viewport: HTMLElement, top: number) =>
        act(() => {
          viewport.scrollTop = top;
          viewport.dispatchEvent(new Event('scroll'));
        });

      renderPanel(twoTurns());
      const viewport = panelRef.current!.getViewport()!;
      // jsdom does no layout: give the viewport a size, so scrollTop is meaningful.
      Object.defineProperty(viewport, 'scrollHeight', { configurable: true, value: 2000 });
      Object.defineProperty(viewport, 'clientHeight', { configurable: true, value: 400 });

      // Control: the harness does see a jump. A reader at the bottom is taken to a new bottom row.
      scrollTo(viewport, 1600);
      renderPanel([...twoTurns(), { role: 'user', content: 'more', id: 'u3' }]);
      expect(viewport.scrollTop).toBe(2000);

      // The reader scrolls up into the older turn.
      renderPanel(twoTurns());
      scrollTo(viewport, 100);
      expect(viewport.scrollTop).toBe(100);
      const before = rowIds();
      expect(before[before.length - 1]).toBe('a3');

      // The card's attachment index entry arrives after the transcript was shown,
      // with no usable turn id (as from history).
      renderPanel(twoTurns(), index(offer('o1')));
      expect(container.querySelectorAll('[data-testid="offer-o1"]')).toHaveLength(1);
      // It goes in its own turn, not as a new row at the bottom of the chat
      // (the old `messages.length` fallback), so the bottom row is unchanged...
      const after = rowIds();
      expect(after).toEqual(['u1', 'a1', 'a2', row('o1'), 'u2', 'a3']);
      expect(after[after.length - 1]).toBe('a3');
      // ...and the reader is left where they were.
      expect(viewport.scrollTop).toBe(100);
    } finally {
      act(() => root.unmount());
      container.remove();
      unregister();
    }
  });

  it('renders the card once in ChatPanel, with no placeholder at the inline tag', () => {
    const container = document.createElement('div');
    document.body.appendChild(container);
    const root = createRoot(container);
    const unregister = registerLlmdPlugin({
      id: 'test-offer',
      attachments: {
        [OFFER_KIND]: {
          placement: { endOfTurn: true },
          render: ({ attachmentId }) => <div data-testid={`offer-${attachmentId}`}>offer</div>,
        },
      },
    });
    try {
      const msgs: ChatMessageData[] = [
        { role: 'user', content: 'build it', id: 'u1' },
        { role: 'assistant', content: `Offer${formatAttachmentTag('o1')} text.`, id: 'a1' },
        { role: 'assistant', content: 'Final.', id: 'a2' },
        { role: 'user', content: 'next', id: 'u2' },
      ];
      act(() => {
        root.render(
          <MantineProvider>
            <ChatPanel messages={msgs} onSend={() => {}} attachmentIndex={index(offer('o1', { turnMessageId: 'u1' }))} />
          </MantineProvider>
        );
      });
      expect(container.querySelectorAll('[data-testid="offer-o1"]')).toHaveLength(1);
      expect(container.textContent).not.toContain('[card unavailable]');
      const rows = Array.from(container.querySelectorAll('[data-message-id]')).map((el) => el.getAttribute('data-message-id'));
      expect(rows).toEqual(['u1', 'a1', 'a2', row('o1'), 'u2']);
    } finally {
      act(() => root.unmount());
      container.remove();
      unregister();
    }
  });
});

describe('rendering', () => {
  let container: HTMLDivElement;
  let root: Root;
  let consoleError: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
    root = createRoot(container);
    // jsdom has no canvas; Vega falls back to estimated text metrics and says so.
    consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
  });
  afterEach(() => {
    act(() => root.unmount());
    container.remove();
    consoleError.mockRestore();
  });

  function render(node: React.ReactNode) {
    act(() => {
      root.render(<MantineProvider>{node}</MantineProvider>);
    });
  }

  it('ChatPanel shows an untagged image card after its tool result, with no host code', () => {
    render(<ChatPanel messages={transcript()} onSend={() => {}} attachmentIndex={index(image('img1', 'call_1'))} />);
    const rows = Array.from(container.querySelectorAll('[data-message-id]')).map((el) => el.getAttribute('data-message-id'));
    expect(rows).toEqual(['u1', 'a1', row('img1'), 'a2']);
    const img = container.querySelector('[data-testid="image-card-img1"] img') as HTMLImageElement | null;
    expect(img?.getAttribute('src')).toBe(imageCardDataUrl('image/png', PNG_B64));
    const download = container.querySelector('[data-testid="image-card-img1"] a[download]');
    expect(download?.getAttribute('download')).toBe('plot-img1.png');
  });

  it('renders a chart card through the charts plugin (registered by importing it)', async () => {
    render(
      <ChatMessage
        message={{ role: 'assistant', content: formatAttachmentTag('ch1'), id: 'm1' }}
        attachmentIndex={index(chart('ch1'))}
      />
    );
    const card = () => container.querySelector('[data-testid="display-card-ch1"]');
    expect(card()?.getAttribute('data-card-type')).toBe('chart');
    expect(card()?.querySelector('.vega-chart-embed__label')?.textContent).toBe('Spend');
    await vi.waitFor(() => expect(card()?.querySelector('svg')).not.toBeNull(), { timeout: 10_000 });
  });

  it('says so, instead of rendering nothing, when the charts plugin is not installed', () => {
    unregisterCharts();
    try {
      render(
        <ChatMessage
          message={{ role: 'assistant', content: formatAttachmentTag('ch1'), id: 'm1' }}
          attachmentIndex={index(chart('ch1'))}
        />
      );
      expect(container.querySelector('[data-testid="display-card-ch1"]')?.textContent).toMatch(
        /no renderer for "chart" cards/
      );
    } finally {
      registerLlmdPlugin(chartsPlugin);
    }
  });

  it('degrades an invalid card to a visible placeholder', () => {
    const bad: LlmdMessageAttachment = {
      id: 'bad',
      kind: DISPLAY_CARD_KIND,
      payloadJson: JSON.stringify({ id: 'bad', type: 'image', body: { src: 'https://evil.test/x.png' } }),
    };
    render(
      <ChatMessage message={{ role: 'assistant', content: formatAttachmentTag('bad'), id: 'm1' }} attachmentIndex={index(bad)} />
    );
    expect(container.querySelector('[data-testid="card-bad-unavailable"]')?.textContent).toBe('[image unavailable]');
    expect(container.querySelector('img')).toBeNull();
  });
});
