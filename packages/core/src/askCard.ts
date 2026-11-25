/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * AskCard / AskCardResponse: first-class llmd conversational vocabulary — a
 * generic structured-question primitive, distinct from app-owned domain data
 * (which stays a TurnDecoration pointer). llmd itself never interprets these;
 * this module is the ONE canonical JS shape + JSON codec every llmd-based
 * client shares, so a host never reinvents the question/answer contract.
 *
 * Wire shape: an AskCard rides as `LlmdMessageAttachment.payloadJson` with
 * kind = ASK_CARD_KIND, inline-tagged into an assistant message the same way
 * any other attachment is. The answer is a SEPARATE attachment
 * (kind = ASK_CARD_RESPONSE_KIND) whose payloadJson carries the structured
 * AskCardResponse and whose `content` carries a synthesized natural-language
 * summary for the model — never the raw structured answer, and never file
 * bytes (see synthesizeAskCardResponseContent).
 */

import type { LlmdMessageAttachment } from './messageAttachments.js';

export const ASK_CARD_KIND = 'ask_card';
export const ASK_CARD_RESPONSE_KIND = 'ask_card_response';

export type AskCardType = 'choice' | 'text' | 'file';

/**
 * One explained choice. `label` is what the answer reports as selected;
 * `description` is a one-line "what happens if you pick this"; a non-empty
 * `recommendation` marks the option as recommended and says why (at most one
 * option per card carries it).
 */
export interface AskChoiceOption {
  label: string;
  description?: string;
  recommendation?: string;
}

export interface AskChoiceCard {
  /** Option labels. Always present, also when `options` is set. */
  choices: string[];
  /** false = single-select, true = multi-select. */
  allowMultiple: boolean;
  /** Optional per-option explanations, same order as `choices`. */
  options?: AskChoiceOption[];
}

export interface AskFileCard {
  /** Advisory only; empty = any file type accepted. */
  acceptedMimeTypes: string[];
}

export interface AskCard {
  id: string;
  title: string;
  text: string;
  type: AskCardType;
  /** When true, the client also offers a free-text field alongside the primary answer. */
  allowAdditionalText: boolean;
  choice?: AskChoiceCard;
  file?: AskFileCard;
}

export interface AskChoiceResponse {
  selected: string[];
}

export interface AskFileResponse {
  fileName: string;
  mimeType: string;
  sizeBytes: number;
  /** Base64-encoded raw file bytes. */
  contentBase64: string;
}

export interface AskCardResponse {
  /** Correlator: the AskCard.id being answered. */
  cardId: string;
  type: AskCardType;
  additionalText?: string;
  choice?: AskChoiceResponse;
  file?: AskFileResponse;
}

export function encodeAskCard(card: AskCard): string {
  return JSON.stringify(card);
}

export function decodeAskCard(payloadJson: string): AskCard | undefined {
  try {
    const parsed = JSON.parse(payloadJson) as Partial<AskCard> | null;
    if (!parsed || typeof parsed.id !== 'string' || typeof parsed.type !== 'string') {
      return undefined;
    }
    return {
      id: parsed.id,
      title: typeof parsed.title === 'string' ? parsed.title : '',
      text: typeof parsed.text === 'string' ? parsed.text : '',
      type: parsed.type as AskCardType,
      allowAdditionalText: parsed.allowAdditionalText === true,
      ...(parsed.choice ? { choice: decodeAskChoiceCard(parsed.choice) } : {}),
      ...(parsed.file ? { file: parsed.file as AskFileCard } : {}),
    };
  } catch {
    return undefined;
  }
}

/**
 * Normalizes a stored choice body. `options` is kept only if it is a list of
 * objects with string labels; otherwise the card falls back to plain
 * `choices`. When options are kept, `choices` is derived from their labels so
 * the two can never disagree.
 */
function decodeAskChoiceCard(raw: unknown): AskChoiceCard {
  const c = (raw ?? {}) as Partial<AskChoiceCard> & { options?: unknown };
  const choices = Array.isArray(c.choices)
    ? c.choices.filter((x): x is string => typeof x === 'string')
    : [];
  const allowMultiple = c.allowMultiple === true;
  if (!Array.isArray(c.options) || c.options.length === 0) {
    return { choices, allowMultiple };
  }
  const options: AskChoiceOption[] = [];
  for (const o of c.options as unknown[]) {
    const opt = o as Partial<AskChoiceOption> | null;
    if (!opt || typeof opt.label !== 'string' || !opt.label.trim()) {
      return { choices, allowMultiple };
    }
    const description = typeof opt.description === 'string' ? opt.description.trim() : '';
    const recommendation = typeof opt.recommendation === 'string' ? opt.recommendation.trim() : '';
    options.push({
      label: opt.label,
      ...(description ? { description } : {}),
      ...(recommendation ? { recommendation } : {}),
    });
  }
  return { choices: options.map((o) => o.label), allowMultiple, options };
}

export function encodeAskCardResponse(response: AskCardResponse): string {
  return JSON.stringify(response);
}

export function decodeAskCardResponse(payloadJson: string): AskCardResponse | undefined {
  try {
    const parsed = JSON.parse(payloadJson) as Partial<AskCardResponse> | null;
    if (!parsed || typeof parsed.cardId !== 'string' || typeof parsed.type !== 'string') {
      return undefined;
    }
    return {
      cardId: parsed.cardId,
      type: parsed.type as AskCardType,
      ...(typeof parsed.additionalText === 'string'
        ? { additionalText: parsed.additionalText }
        : {}),
      ...(parsed.choice ? { choice: parsed.choice as AskChoiceResponse } : {}),
      ...(parsed.file ? { file: parsed.file as AskFileResponse } : {}),
    };
  } catch {
    return undefined;
  }
}

/**
 * Provider-facing summary for an AskCardResponse — a synthesized natural-
 * language sentence, NEVER the raw structured answer and NEVER file bytes.
 * This is what rides in the response attachment's `content` field and gets
 * spliced into the outbound prompt; the structured answer (and any file
 * bytes) lives only in payloadJson, which is client-facing and never sent
 * to the provider.
 */
export function synthesizeAskCardResponseContent(
  card: AskCard,
  response: AskCardResponse
): string {
  const parts: string[] = [];
  if (response.type === 'choice' && response.choice) {
    parts.push(
      response.choice.selected.length > 0
        ? `Selected: ${response.choice.selected.join(', ')}.`
        : 'No option selected.'
    );
  } else if (response.type === 'file' && response.file) {
    const kb = Math.max(1, Math.round(response.file.sizeBytes / 1024));
    parts.push(`Uploaded file: ${response.file.fileName} (${kb} KB, ${response.file.mimeType || 'unknown type'}).`);
  } else {
    parts.push(response.additionalText?.trim() ? response.additionalText.trim() : 'No answer provided.');
  }
  if (response.type !== 'text' && response.additionalText?.trim()) {
    parts.push(`Additional note: ${response.additionalText.trim()}`);
  }
  const title = card.title.trim() || 'a question';
  return `In response to "${title}": ${parts.join(' ')}`;
}

/** Builds the attachment for an AskCard itself (display-only, no provider content). */
export function buildAskCardAttachment(card: AskCard): LlmdMessageAttachment {
  return { id: card.id, kind: ASK_CARD_KIND, payloadJson: encodeAskCard(card) };
}

/**
 * Builds the attachment for an answer: payloadJson carries the full
 * structured response (client-facing), content carries the synthesized
 * summary (provider-facing). The id is derived from the card id so it's
 * stable/idempotent across re-submits of the same card.
 */
export function buildAskCardResponseAttachment(
  card: AskCard,
  response: AskCardResponse
): LlmdMessageAttachment {
  return {
    id: `${card.id}:response`,
    kind: ASK_CARD_RESPONSE_KIND,
    payloadJson: encodeAskCardResponse(response),
    content: synthesizeAskCardResponseContent(card, response),
  };
}

/** Indexes a session's attachments' ask-card responses by the card id they answer. */
export function indexAskCardResponses(
  attachments: Iterable<LlmdMessageAttachment>
): Map<string, AskCardResponse> {
  const out = new Map<string, AskCardResponse>();
  for (const attachment of attachments) {
    if (attachment.kind !== ASK_CARD_RESPONSE_KIND) continue;
    const response = decodeAskCardResponse(attachment.payloadJson);
    if (response) out.set(response.cardId, response);
  }
  return out;
}
