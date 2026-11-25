/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * The `image` display card type (see displayCard.ts): a plot a script
 * rendered, a screenshot, a generated diagram.
 *
 * The image travels INLINE as a base64 `data:` URL. That is deliberate:
 *  - the card is a snapshot — it survives the source file being deleted, a
 *    worktree going away, or a reload, with no host-side resolver;
 *  - nothing is fetched at render time, so a card can never be turned into a
 *    tracking pixel or an exfiltration beacon by pointing it at a URL.
 * Only raster formats and SVG are accepted, and the renderer shows them via
 * `<img>`, where SVG scripts do not run. Size is capped (IMAGE_CARD_MAX_BYTES)
 * because the payload is stored in the session DB and pushed over the live
 * attachment stream.
 */

import type { DisplayCard } from './displayCard.js';

export const IMAGE_CARD_TYPE = 'image';

export const IMAGE_CARD_MIME_TYPES = [
  'image/png',
  'image/jpeg',
  'image/gif',
  'image/webp',
  'image/svg+xml',
] as const;

export type ImageCardMimeType = (typeof IMAGE_CARD_MIME_TYPES)[number];

/**
 * Largest decoded image accepted (2 MiB). Base64 inflates by 4/3, so the
 * stored payload stays under ~2.7 MiB — inside DISPLAY_CARD_MAX_PAYLOAD_BYTES.
 */
export const IMAGE_CARD_MAX_BYTES = 2 * 1024 * 1024;

export interface ImageBody {
  /** `data:<mimeType>;base64,<bytes>`. Nothing else is accepted. */
  src: string;
  /** Derived from `src` on decode; always agrees with it. */
  mimeType: ImageCardMimeType;
  /** Accessible description; falls back to the card title. */
  alt?: string;
  /** Suggested name when the user downloads the image. */
  fileName?: string;
  /** Intrinsic size in px, when the producer knows it (lets the UI reserve space). */
  width?: number;
  height?: number;
}

export type ImageCard = DisplayCard<ImageBody>;

const DATA_URL_PREFIX = /^data:([a-z0-9.+/-]+);base64,/i;
const BASE64_BODY = /^[A-Za-z0-9+/]*={0,2}$/;

function isImageCardMimeType(value: string): value is ImageCardMimeType {
  return (IMAGE_CARD_MIME_TYPES as readonly string[]).includes(value);
}

/** Decoded byte length of a base64 body (no allocation). */
export function base64DecodedLength(body: string): number {
  const padding = body.endsWith('==') ? 2 : body.endsWith('=') ? 1 : 0;
  return Math.floor((body.length * 3) / 4) - padding;
}

/** Builds the `data:` URL an image body carries. */
export function imageCardDataUrl(mimeType: ImageCardMimeType, base64: string): string {
  return `data:${mimeType};base64,${base64}`;
}

/**
 * Why `src` is not an acceptable image, or undefined when it is. Producers
 * can call this before persisting to fail with a clear message.
 */
export function imageCardSrcProblem(src: unknown): string | undefined {
  if (typeof src !== 'string' || !src) return 'image src is empty';
  const prefix = DATA_URL_PREFIX.exec(src);
  if (!prefix) return 'image src must be a base64 data: URL';
  const mime = prefix[1].toLowerCase();
  if (!isImageCardMimeType(mime)) {
    return `unsupported image type ${mime} (allowed: ${IMAGE_CARD_MIME_TYPES.join(', ')})`;
  }
  const body = src.slice(prefix[0].length);
  if (!body) return 'image data is empty';
  if (!BASE64_BODY.test(body)) return 'image data is not valid base64';
  const bytes = base64DecodedLength(body);
  if (bytes > IMAGE_CARD_MAX_BYTES) {
    return `image is ${bytes} bytes; the limit is ${IMAGE_CARD_MAX_BYTES}`;
  }
  return undefined;
}

function optionalString(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value : undefined;
}

function optionalDimension(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : undefined;
}

/** A display card body as an image, or undefined when it is not a valid, in-limits one. */
export function decodeImageBody(body: Record<string, unknown>): ImageBody | undefined {
  if (imageCardSrcProblem(body.src) !== undefined) return undefined;
  const src = body.src as string;
  const mimeType = DATA_URL_PREFIX.exec(src)![1].toLowerCase() as ImageCardMimeType;
  const alt = optionalString(body.alt);
  const fileName = optionalString(body.fileName);
  const width = optionalDimension(body.width);
  const height = optionalDimension(body.height);
  return {
    src,
    mimeType,
    ...(alt ? { alt } : {}),
    ...(fileName ? { fileName } : {}),
    ...(width ? { width } : {}),
    ...(height ? { height } : {}),
  };
}

/** An image display card (type IMAGE_CARD_TYPE). */
export function imageDisplayCard(
  id: string,
  body: ImageBody,
  meta: { title?: string; caption?: string } = {}
): ImageCard {
  return { id, type: IMAGE_CARD_TYPE, body, ...meta };
}
