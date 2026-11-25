/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * The built-in `image` display card (@pyxis-llm/core IMAGE_CARD_TYPE): an inline
 * image (capped height, click to enlarge) with a download button. Always an
 * `<img>`: SVG scripts do not run there, and `src` is a data: URL (enforced by
 * decodeImageBody), so showing it fetches nothing.
 *
 * Imports only types from plugins.tsx, which imports this module: keep it
 * that way, or the two form a runtime import cycle.
 */

import { useState } from 'react';
import { ActionIcon, Box, Group, Modal, Text, Tooltip, UnstyledButton } from '@mantine/core';
import { IconDownload } from '@tabler/icons-react';
import { IMAGE_CARD_TYPE, decodeImageBody, type ImageBody, type ImageCard } from '@pyxis-llm/core';
import type { DisplayCardRenderProps, LlmdPlugin } from './plugins.js';

const IMAGE_EXTENSIONS: Record<ImageBody['mimeType'], string> = {
  'image/png': 'png',
  'image/jpeg': 'jpg',
  'image/gif': 'gif',
  'image/webp': 'webp',
  'image/svg+xml': 'svg',
};

function downloadNameFor(card: ImageCard): string {
  if (card.body.fileName?.trim()) return card.body.fileName.trim();
  const base = (card.title ?? '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
  return `${base || 'image'}.${IMAGE_EXTENSIONS[card.body.mimeType]}`;
}

export function ImageCardView({ card }: DisplayCardRenderProps<ImageBody>) {
  const [enlarged, setEnlarged] = useState(false);
  const { body } = card;
  const label = card.title?.trim() || body.fileName?.trim() || 'Image';
  const alt = body.alt?.trim() || card.title?.trim() || card.caption?.trim() || 'Image';
  const downloadName = downloadNameFor(card);
  return (
    <Box
      className="llmd-image-card"
      data-testid={`image-card-${card.id}`}
      style={{ display: 'flex', flexDirection: 'column', gap: 6, minWidth: 0 }}
    >
      <Group justify="space-between" gap="xs" wrap="nowrap">
        <Text size="sm" fw={600} className="llmd-image-card__label" truncate>
          {label}
        </Text>
        <Tooltip label={`Download ${downloadName}`} withArrow>
          <ActionIcon
            component="a"
            href={body.src}
            download={downloadName}
            variant="subtle"
            color="gray"
            size="sm"
            aria-label={`Download ${downloadName}`}
          >
            <IconDownload size={14} stroke={1.6} />
          </ActionIcon>
        </Tooltip>
      </Group>
      <UnstyledButton
        onClick={() => setEnlarged(true)}
        aria-label={`Enlarge: ${alt}`}
        style={{ alignSelf: 'flex-start', maxWidth: '100%', cursor: 'zoom-in' }}
      >
        <img
          src={body.src}
          alt={alt}
          width={body.width}
          height={body.height}
          loading="lazy"
          className="llmd-image-card__img"
          style={{
            display: 'block',
            maxWidth: '100%',
            maxHeight: 480,
            height: 'auto',
            objectFit: 'contain',
            borderRadius: 6,
          }}
        />
      </UnstyledButton>
      {card.caption?.trim() ? (
        <Text size="xs" c="dimmed" className="llmd-image-card__caption">
          {card.caption}
        </Text>
      ) : null}
      <Modal opened={enlarged} onClose={() => setEnlarged(false)} title={label} size="auto" centered>
        <img
          src={body.src}
          alt={alt}
          style={{ display: 'block', maxWidth: '90vw', maxHeight: '80vh', objectFit: 'contain' }}
        />
      </Modal>
    </Box>
  );
}

/** Built in: every chat gets image cards without registering anything. */
export const imageCardPlugin: LlmdPlugin = {
  id: 'llmd.image',
  displayCards: {
    [IMAGE_CARD_TYPE]: { decode: decodeImageBody, render: ImageCardView, ownsChrome: true },
  },
};
