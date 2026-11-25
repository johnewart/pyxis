/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * How ChatMessage renders every non-ask attachment: display cards
 * (@pyxis-llm/core DISPLAY_CARD_KIND) through the renderer a plugin registered
 * for their `type`, inside llmd's shared frame; host kinds through the
 * plugin registered for their `kind`. Nothing here knows a type or kind by
 * name. Anything without a renderer degrades to a muted placeholder that
 * says what is missing — never to nothing.
 */

import { Component, useMemo, type ErrorInfo, type ReactNode } from 'react';
import { Box, Text } from '@mantine/core';
import { DISPLAY_CARD_KIND, decodeDisplayCard, type LlmdMessageAttachment } from '@pyxis-llm/core';
import { useLlmdPlugins, type AttachmentRenderProps } from './plugins.js';

function Unavailable({ id, label, detail }: { id: string; label: string; detail?: string }) {
  return (
    <span
      title={detail ?? `Attachment ${id}`}
      data-testid={`card-${id}-unavailable`}
      className="llmd-card-unavailable"
      style={{ opacity: 0.6, fontStyle: 'italic' }}
    >
      {label}
    </span>
  );
}

/** One broken card must not take down the transcript. */
class CardErrorBoundary extends Component<{ id: string; label: string; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError(): { failed: boolean } {
    return { failed: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // eslint-disable-next-line no-console
    console.error(`llmd card: attachment ${this.props.id} failed to render`, error, info.componentStack);
  }

  render(): ReactNode {
    if (this.state.failed) return <Unavailable id={this.props.id} label={this.props.label} />;
    return this.props.children;
  }
}

/**
 * A display card. Decoding (a JSON parse of up to a few MB, plus the type's
 * validation) is memoized on the payload, so re-rendering the message does
 * not repeat it.
 */
export function DisplayCardAttachment({ attachment }: { attachment: LlmdMessageAttachment }) {
  const { displayCards } = useLlmdPlugins();
  const envelope = useMemo(() => decodeDisplayCard(attachment.payloadJson), [attachment.payloadJson]);
  const renderer = envelope ? displayCards[envelope.type] : undefined;
  const body = useMemo(
    () => (envelope && renderer ? (renderer.decode ? renderer.decode(envelope.body) : envelope.body) : undefined),
    [envelope, renderer]
  );
  if (!envelope) return <Unavailable id={attachment.id} label="[card unavailable]" />;
  if (!renderer) {
    return (
      <Box className="llmd-display-card llmd-display-card--no-renderer" data-testid={`display-card-${envelope.id}`}>
        {envelope.title?.trim() ? (
          <Text size="sm" fw={600}>
            {envelope.title}
          </Text>
        ) : null}
        <Text size="xs" c="dimmed">
          This {envelope.type} can&apos;t be shown here: no renderer for &quot;{envelope.type}&quot; cards is installed.
        </Text>
      </Box>
    );
  }
  if (body === undefined) return <Unavailable id={attachment.id} label={`[${envelope.type} unavailable]`} />;
  const card = { ...envelope, body };
  const Render = renderer.render;
  return (
    <CardErrorBoundary id={attachment.id} label={`[this ${envelope.type} failed to render]`}>
      <div
        className={`llmd-display-card llmd-display-card--${envelope.type}`}
        data-testid={`display-card-${envelope.id}`}
        data-card-type={envelope.type}
      >
        {renderer.ownsChrome ? (
          <Render card={card} />
        ) : (
          <Box style={{ display: 'flex', flexDirection: 'column', gap: 6, minWidth: 0 }}>
            {envelope.title?.trim() ? (
              <Text size="sm" fw={600} className="llmd-display-card__title">
                {envelope.title}
              </Text>
            ) : null}
            <Render card={card} />
            {envelope.caption?.trim() ? (
              <Text size="xs" c="dimmed" className="llmd-display-card__caption">
                {envelope.caption}
              </Text>
            ) : null}
          </Box>
        )}
      </div>
    </CardErrorBoundary>
  );
}

/**
 * Any non-ask attachment: a display card, or a host kind with a registered
 * renderer. Returns the muted placeholder for anything else.
 */
export function PluginAttachment(props: Omit<AttachmentRenderProps, 'attachmentId'> & { attachmentId: string }) {
  const { attachments } = useLlmdPlugins();
  const { attachment } = props;
  if (attachment.kind === DISPLAY_CARD_KIND) return <DisplayCardAttachment attachment={attachment} />;
  const Render = attachments[attachment.kind]?.render;
  if (!Render) {
    return <Unavailable id={attachment.id} label="[card unavailable]" detail={`No renderer for ${attachment.kind}`} />;
  }
  return (
    <CardErrorBoundary id={attachment.id} label="[this card failed to render]">
      <Render {...props} />
    </CardErrorBoundary>
  );
}
