/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * The message-entry surface: textarea (Enter to send, ⌥/Ctrl/⌘+Enter for the
 * alternate send, Shift+Enter for a newline), a send button labelled with what
 * it will do (see `sendMode`) plus a stop button while streaming, and two
 * slots (`beforeInput`, `afterInput`) for host-specific rows — an
 * attachments strip, a model switcher, paste pills, whatever. This
 * component owns none of that; it only owns "type text, hit send".
 */

import { useCallback, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { ActionIcon, Button, Textarea } from '@mantine/core';
import { IconPlayerStop, IconSend } from '@tabler/icons-react';
import { isAlternateSend, type SendMode } from './sendMode.js';

export type ComposerProps = {
  /**
   * Called with the trimmed text on submit. Empty text never submits.
   * `alternate` is true when ⌥, Ctrl or ⌘ is held (Enter or the button) — the
   * "other" send (e.g. interrupt instead of queue).
   */
  onSend: (text: string, alternate: boolean) => void;
  /**
   * Live send mode (`useSendMode`): labels the send button with what it will do
   * (Send / Queue / Interrupt) and flips it while a modifier is held in the
   * input. Omitted = a plain "Send" button.
   */
  sendMode?: SendMode;
  /** True while an assistant reply is streaming — swaps the send button for a stop button. */
  streaming?: boolean;
  /** Called when the stop button is clicked while `streaming`. */
  onStop?: () => void;
  placeholder?: string;
  disabled?: boolean;
  autoFocus?: boolean;
  /** Controlled value; omit to let the composer manage its own text state. */
  value?: string;
  onValueChange?: (text: string) => void;
  /** Row rendered above the textarea (e.g. attachment pills, paste chips). */
  beforeInput?: ReactNode;
  /** Row rendered below the textarea, to the left of the send/stop button (e.g. model switcher). */
  afterInput?: ReactNode;
  minRows?: number;
  maxRows?: number;
  className?: string;
};

export function Composer({
  onSend,
  sendMode,
  streaming = false,
  onStop,
  placeholder = 'Message…',
  disabled = false,
  autoFocus = false,
  value,
  onValueChange,
  beforeInput,
  afterInput,
  minRows = 1,
  maxRows = 10,
  className,
}: ComposerProps) {
  const [internalValue, setInternalValue] = useState('');
  const isControlled = value !== undefined;
  const text = isControlled ? value : internalValue;
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const setText = useCallback(
    (next: string) => {
      if (!isControlled) setInternalValue(next);
      onValueChange?.(next);
    },
    [isControlled, onValueChange]
  );

  const submit = useCallback(
    (alternate = false) => {
      const trimmed = text.trim();
      if (!trimmed || disabled) return;
      onSend(trimmed, alternate);
      setText('');
    },
    [text, disabled, onSend, setText]
  );

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    sendMode?.keyHandlers.onKeyDown(e);
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      submit(isAlternateSend(e));
    }
  };
  const label = sendMode?.label ?? 'Send';
  const hasText = !!text.trim();

  return (
    <div className={['llmd-composer', className].filter(Boolean).join(' ')}>
      {beforeInput ? <div className="llmd-composer__before">{beforeInput}</div> : null}
      <div className="llmd-composer__row" style={{ display: 'flex', alignItems: 'flex-end', gap: 8 }}>
        <Textarea
          ref={textareaRef}
          className="llmd-composer__input"
          // Stable hook for hosts (e.g. paste/mention capture). Lands on the
          // <textarea> itself; part of the public contract — do not rename.
          data-llmd-composer-input=""
          style={{ flex: 1 }}
          value={text}
          onChange={(e) => setText(e.currentTarget.value)}
          onKeyDown={handleKeyDown}
          onKeyUp={sendMode?.keyHandlers.onKeyUp}
          onBlur={sendMode?.keyHandlers.onBlur}
          placeholder={placeholder}
          disabled={disabled}
          autoFocus={autoFocus}
          autosize
          minRows={minRows}
          maxRows={maxRows}
        />
        {streaming ? (
          <ActionIcon
            type="button"
            variant="filled"
            color="red"
            size="lg"
            aria-label="Stop"
            onClick={onStop}
          >
            <IconPlayerStop size={16} stroke={1.75} />
          </ActionIcon>
        ) : null}
        {/* While a turn runs the send button only appears once there is text
            (Stop is the main action then); it says whether this queues or
            interrupts. */}
        {!streaming || hasText ? (
          <Button
            type="button"
            variant="filled"
            size="sm"
            className="llmd-composer__send"
            aria-label={label}
            data-send-behavior={sendMode?.behavior}
            leftSection={<IconSend size={14} stroke={1.75} />}
            disabled={disabled || !hasText}
            onClick={(e) => submit(isAlternateSend(e))}
          >
            {label}
          </Button>
        ) : null}
      </div>
      {afterInput ? <div className="llmd-composer__after">{afterInput}</div> : null}
    </div>
  );
}
