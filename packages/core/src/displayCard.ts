/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Display cards: non-interactive content embedded in the chat transcript —
 * an image a script wrote, an interactive chart, a host's budget table. One
 * attachment kind (DISPLAY_CARD_KIND) for all of them; what a card shows is
 * its `type`, and `body` is that type's data. The shared parts (title,
 * caption, placement after the tool call that made it, error fallback) are the
 * same for every type, so renderers only draw the body.
 *
 * (Interactive cards are a different thing: ask cards — askCard.ts — carry a
 * response back to the model and llmd's server handles them specially.)
 *
 * The payload is display-only: llmd never sends payloadJson to a provider, so
 * a card costs the model no context, whatever its body holds. A producer that
 * wants the model to know what was shown sets the attachment's `content`
 * (see synthesizeDisplayCardContent).
 *
 * Which types exist is open: llmd ships `image` (imageCard.ts) and `chart`
 * (chartCard.ts); a host may define its own. A renderer for a type comes from
 * a `@pyxis-llm/react` plugin; a card whose type has no renderer says so instead
 * of disappearing.
 */

import type { LlmdMessageAttachment } from './messageAttachments.js';

export const DISPLAY_CARD_KIND = 'llmd.display';

/**
 * Largest payload accepted (3 MiB of JSON). Each type caps its body lower;
 * this bounds the envelope so the frame carrying it stays inside gRPC's
 * default 4 MiB message limit.
 */
export const DISPLAY_CARD_MAX_PAYLOAD_BYTES = 3 * 1024 * 1024;

export interface DisplayCard<Body = Record<string, unknown>> {
  id: string;
  /** What the card shows: "image", "chart", or a host-defined type. */
  type: string;
  title?: string;
  caption?: string;
  body: Body;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function optionalString(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value : undefined;
}

/** UTF-8 byte length without needing TextEncoder. */
export function utf8Length(text: string): number {
  let bytes = 0;
  for (let i = 0; i < text.length; i++) {
    const code = text.charCodeAt(i);
    if (code < 0x80) bytes += 1;
    else if (code < 0x800) bytes += 2;
    else if (code >= 0xd800 && code <= 0xdbff) {
      bytes += 4;
      i++;
    } else bytes += 3;
  }
  return bytes;
}

export function encodeDisplayCard(card: DisplayCard<unknown>): string {
  return JSON.stringify(card);
}

/**
 * The envelope of a display card, or undefined when the payload is not one
 * (or is over DISPLAY_CARD_MAX_PAYLOAD_BYTES). The body is NOT validated
 * here: that is the type's job (e.g. decodeImageBody), done by its renderer.
 */
export function decodeDisplayCard(payloadJson: string): DisplayCard | undefined {
  if (utf8Length(payloadJson) > DISPLAY_CARD_MAX_PAYLOAD_BYTES) return undefined;
  let parsed: unknown;
  try {
    parsed = JSON.parse(payloadJson);
  } catch {
    return undefined;
  }
  if (!isPlainObject(parsed) || typeof parsed.id !== 'string') return undefined;
  const type = typeof parsed.type === 'string' ? parsed.type.trim() : '';
  if (!type || !isPlainObject(parsed.body)) return undefined;
  const title = optionalString(parsed.title);
  const caption = optionalString(parsed.caption);
  return {
    id: parsed.id,
    type,
    body: parsed.body,
    ...(title ? { title } : {}),
    ...(caption ? { caption } : {}),
  };
}

/**
 * One-line, provider-facing note that the user was shown this card. Never
 * includes the body. `label` overrides the title (types pass a better one,
 * e.g. a chart's own title).
 */
export function synthesizeDisplayCardContent(card: DisplayCard<unknown>, label?: string): string {
  const name = card.title?.trim() || label?.trim() || `${/^[aeiou]/i.test(card.type) ? 'an' : 'a'} ${card.type}`;
  const caption = card.caption?.trim();
  return `[Shown to the user: ${card.type} "${name}"${caption ? ` — ${caption}` : ''}]`;
}

export type BuildDisplayCardAttachmentOptions = {
  /** Place the card right after this tool call's result. */
  afterToolCallId?: string;
  /** Provider-facing note (e.g. synthesizeDisplayCardContent). Omit for display-only. */
  content?: string;
};

export function buildDisplayCardAttachment(
  card: DisplayCard<unknown>,
  options: BuildDisplayCardAttachmentOptions = {}
): LlmdMessageAttachment {
  return {
    id: card.id,
    kind: DISPLAY_CARD_KIND,
    payloadJson: encodeDisplayCard(card),
    ...(options.afterToolCallId ? { afterToolCallId: options.afterToolCallId } : {}),
    ...(options.content ? { content: options.content } : {}),
  };
}
