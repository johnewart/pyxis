/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Attachment-kind behavior for the kinds llmd itself defines. Kept out of
 * messageAttachments.ts, which only knows the generic mechanics and never
 * special-cases a kind by name: llmd's own kinds plug in exactly like a
 * host's do.
 */

import { DISPLAY_CARD_KIND } from './displayCard.js';
import type { AttachmentKindPlugin } from './messageAttachments.js';

/** Display cards are records of what a tool showed: always visible. */
export const LLMD_ATTACHMENT_KIND_PLUGINS: readonly AttachmentKindPlugin[] = [
  { kind: DISPLAY_CARD_KIND, persistent: true },
];

/**
 * A host's plugins plus llmd's own. Host plugins come first, so a host that
 * registers one of llmd's kinds itself overrides llmd's behavior for it.
 */
export function withLlmdAttachmentKindPlugins(
  hostPlugins: readonly AttachmentKindPlugin[] = []
): readonly AttachmentKindPlugin[] {
  return hostPlugins.length === 0 ? LLMD_ATTACHMENT_KIND_PLUGINS : [...hostPlugins, ...LLMD_ATTACHMENT_KIND_PLUGINS];
}
