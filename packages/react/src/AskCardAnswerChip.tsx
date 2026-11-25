/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * The trace an answered ask card leaves in the transcript: a small chip on the
 * "Answer to: …" user message, naming the question; click it to see the
 * question and the answer that was sent. The card itself stops rendering once
 * answered (see @pyxis-llm/core's appendAnchorlessCardMessages) — it is a prompt,
 * not content, and re-showing it under every new message was the bug.
 */

import { useState } from 'react';
import { Badge, Popover, Stack, Text } from '@mantine/core';
import { IconMessageQuestion } from '@tabler/icons-react';
import type { AskCard, AskCardResponse } from '@pyxis-llm/core';
import { answerSummary } from './AskCardView.js';

export type AskCardAnswerChipProps = {
  /** The card answered (when still in the index); the chip degrades to the answer alone without it. */
  card?: AskCard;
  response: AskCardResponse;
};

export function AskCardAnswerChip({ card, response }: AskCardAnswerChipProps) {
  const [open, setOpen] = useState(false);
  const title = card?.title?.trim() || 'Question';
  return (
    <Popover opened={open} onChange={setOpen} position="bottom-start" withArrow shadow="md" width={320}>
      <Popover.Target>
        <Badge
          component="button"
          type="button"
          variant="light"
          color="gray"
          radius="sm"
          size="sm"
          leftSection={<IconMessageQuestion size={12} stroke={1.75} />}
          onClick={() => setOpen((v) => !v)}
          style={{ cursor: 'pointer', textTransform: 'none', fontWeight: 500 }}
          className="ask-card-chip"
          aria-label={`Answered question: ${title}`}
          data-testid={`ask-card-chip-${response.cardId}`}
        >
          {title}
        </Badge>
      </Popover.Target>
      <Popover.Dropdown>
        <Stack gap={4} data-testid={`ask-card-chip-detail-${response.cardId}`}>
          <Text fw={600} size="sm">
            {title}
          </Text>
          {card?.text && (
            <Text size="sm" c="dimmed">
              {card.text}
            </Text>
          )}
          <Text size="sm" fw={500}>
            {answerSummary(response)}
          </Text>
          {response.type !== 'text' && response.additionalText?.trim() && (
            <Text size="sm" c="dimmed">
              {response.additionalText}
            </Text>
          )}
        </Stack>
      </Popover.Dropdown>
    </Popover>
  );
}
