/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * The session's message queue, rendered above the composer.
 *
 * Designed to slot into `ChatPanel`'s `composerBefore`, so it composes rather
 * than adding another half-dozen props to ChatPanel:
 *
 *   <ChatPanel
 *     composerBefore={
 *       <QueueStrip
 *         queue={driver.queue}
 *         paused={driver.queuePaused}
 *         onRemove={driver.removeQueued}
 *         onReorder={driver.reorderQueue}
 *         onSetPaused={driver.setQueuePaused}
 *       />
 *     }
 *     ...
 *   />
 *
 * Renders nothing at all when the queue is empty and not paused: a queue that
 * is not in use should take up no space.
 */

import { ActionIcon, Tooltip } from '@mantine/core';
import {
  IconArrowDown,
  IconArrowUp,
  IconPlayerPause,
  IconBolt,
  IconPlayerPlay,
  IconX,
} from '@tabler/icons-react';

import type { QueuedMessage } from './vendor/llmd-types/index.js';

export type QueueStripProps = {
  queue: QueuedMessage[];
  paused: boolean;
  onRemove?: (entryId: string) => void | Promise<void>;
  /** Called with the full desired order (see the driver's `reorderQueue`). */
  onReorder?: (entryIds: string[]) => void | Promise<void>;
  onSetPaused?: (paused: boolean) => void | Promise<void>;
  /**
   * Send this entry now as an interrupt, taking it out of the queue (see the
   * driver's `interruptWithQueued`). Offered on every entry that has not started.
   */
  onInterruptWith?: (entryId: string) => void | Promise<void>;
  className?: string;
};

/** One-line preview, so a long queued message does not dominate the strip. */
function preview(content: string, max = 80): string {
  const flat = content.replace(/\s+/g, ' ').trim();
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat;
}

export function QueueStrip({
  queue,
  paused,
  onRemove,
  onReorder,
  onSetPaused,
  onInterruptWith,
  className,
}: QueueStripProps) {
  if (queue.length === 0 && !paused) return null;

  const ids = queue.map((q) => q.id);
  const move = (index: number, delta: number) => {
    const next = [...ids];
    const target = index + delta;
    if (target < 0 || target >= next.length) return;
    [next[index], next[target]] = [next[target]!, next[index]!];
    void onReorder?.(next);
  };

  return (
    <div
      className={['llmd-queue-strip', className].filter(Boolean).join(' ')}
      style={{ display: 'flex', flexDirection: 'column', gap: 4, width: '100%' }}
    >
      <div
        className="llmd-queue-strip__header"
        style={{ display: 'flex', alignItems: 'center', gap: 6 }}
      >
        <span
          className="llmd-queue-strip__count"
          style={{ fontSize: '0.7rem', textTransform: 'uppercase', letterSpacing: '0.05em', opacity: 0.7 }}
        >
          {queue.length === 0
            ? 'Queue paused'
            : `${queue.length} queued${paused ? ' · paused' : ''}`}
        </span>
        {onSetPaused && (
          <Tooltip
            label={
              paused
                ? 'Resume — queued messages will start running again'
                : 'Pause — queued messages stay put until resumed'
            }
            withArrow
          >
            <ActionIcon
              type="button"
              variant="subtle"
              size="xs"
              color="gray"
              aria-label={paused ? 'Resume queue' : 'Pause queue'}
              className="llmd-queue-strip__pause"
              onClick={() => void onSetPaused(!paused)}
            >
              {paused ? <IconPlayerPlay size={14} /> : <IconPlayerPause size={14} />}
            </ActionIcon>
          </Tooltip>
        )}
      </div>

      {queue.map((entry, index) => {
        const running = entry.state === 'running';
        return (
          <div
            key={entry.id}
            className={`llmd-queue-strip__item${running ? ' llmd-queue-strip__item--running' : ''}`}
            data-queue-entry-id={entry.id}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 6,
              fontSize: '0.78rem',
              opacity: running ? 1 : 0.85,
            }}
          >
            <span
              className="llmd-queue-strip__state"
              style={{ fontSize: '0.62rem', textTransform: 'uppercase', opacity: 0.6, minWidth: 52 }}
            >
              {running ? 'running' : `#${index + 1}`}
            </span>
            <span className="llmd-queue-strip__content" style={{ flex: 1, minWidth: 0 }}>
              {preview(entry.content)}
            </span>
            {/* A running entry is already being answered: reordering or removing
                it would not stop the turn, so those controls are only offered
                for entries that have not started. */}
            {!running && onInterruptWith && (
              <Tooltip label="Send now — interrupt the running turn" withArrow>
                <ActionIcon
                  type="button"
                  variant="subtle"
                  size="xs"
                  color="orange"
                  aria-label={`Interrupt with: ${preview(entry.content, 24)}`}
                  className="llmd-queue-strip__interrupt"
                  onClick={() => void onInterruptWith(entry.id)}
                >
                  <IconBolt size={13} />
                </ActionIcon>
              </Tooltip>
            )}
            {!running && onReorder && (
              <>
                <ActionIcon
                  type="button"
                  variant="subtle"
                  size="xs"
                  color="gray"
                  aria-label={`Move up: ${preview(entry.content, 24)}`}
                  disabled={index === 0}
                  onClick={() => move(index, -1)}
                >
                  <IconArrowUp size={13} />
                </ActionIcon>
                <ActionIcon
                  type="button"
                  variant="subtle"
                  size="xs"
                  color="gray"
                  aria-label={`Move down: ${preview(entry.content, 24)}`}
                  disabled={index === queue.length - 1}
                  onClick={() => move(index, 1)}
                >
                  <IconArrowDown size={13} />
                </ActionIcon>
              </>
            )}
            {!running && onRemove && (
              <ActionIcon
                type="button"
                variant="subtle"
                size="xs"
                color="gray"
                aria-label={`Remove from queue: ${preview(entry.content, 24)}`}
                className="llmd-queue-strip__remove"
                onClick={() => void onRemove(entry.id)}
              >
                <IconX size={13} />
              </ActionIcon>
            )}
          </div>
        );
      })}
    </div>
  );
}
