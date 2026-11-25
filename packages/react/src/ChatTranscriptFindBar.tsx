/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import { ActionIcon, Group, Text, TextInput } from '@mantine/core';
import { IconChevronDown, IconChevronUp, IconX } from '@tabler/icons-react';
import type { RefObject } from 'react';

export type ChatTranscriptFindBarProps = {
  query: string;
  onQueryChange: (value: string) => void;
  inputRef: RefObject<HTMLInputElement | null>;
  matchLabel: string;
  onFindNext: () => void;
  onFindPrevious: () => void;
  onClose: () => void;
  placeholder?: string;
  ariaLabel?: string;
  className?: string;
};

export function ChatTranscriptFindBar({
  query,
  onQueryChange,
  inputRef,
  matchLabel,
  onFindNext,
  onFindPrevious,
  onClose,
  placeholder = 'Find in chat…',
  ariaLabel = 'Find in chat',
  className = 'chat-transcript-find-bar',
}: ChatTranscriptFindBarProps) {
  return (
    <div
      className={className}
      data-chat-find-ignore
      data-code-find-ignore
    >
      <Group gap={6} wrap="nowrap">
        <TextInput
          ref={inputRef as RefObject<HTMLInputElement>}
          size="xs"
          placeholder={placeholder}
          value={query}
          onChange={(e) => onQueryChange(e.currentTarget.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              if (e.shiftKey) onFindPrevious();
              else onFindNext();
            }
            if (e.key === 'Escape') {
              e.preventDefault();
              onClose();
            }
          }}
          aria-label={ariaLabel}
          styles={{ input: { width: 200 } }}
        />
        <Text size="xs" c="dimmed" style={{ minWidth: 52, textAlign: 'center' }}>
          {matchLabel}
        </Text>
        <ActionIcon
          variant="subtle"
          size="sm"
          aria-label="Previous match"
          onClick={onFindPrevious}
        >
          <IconChevronUp size={16} stroke={1.5} />
        </ActionIcon>
        <ActionIcon
          variant="subtle"
          size="sm"
          aria-label="Next match"
          onClick={onFindNext}
        >
          <IconChevronDown size={16} stroke={1.5} />
        </ActionIcon>
        <ActionIcon
          variant="subtle"
          size="sm"
          aria-label="Close find"
          onClick={onClose}
        >
          <IconX size={16} stroke={1.5} />
        </ActionIcon>
      </Group>
    </div>
  );
}
