/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Built-in renderer for llmd's first-class AskCard/AskCardResponse
 * conversational primitive. Unlike host-defined attachment kinds (which
 * always go through `renderAttachmentCard`), ask cards are rendered by
 * @pyxis-llm/react automatically — the whole point of making them first-class
 * llmd vocabulary is that a host never has to re-implement this UI.
 *
 * Two render modes, chosen purely by whether a response is present:
 *   - Unanswered: an interactive form (choice/text/file) with two submit
 *     buttons: "Answer & continue" (primary; the agent's next turn may run
 *     tools) and "Answer & stop" (the agent replies without running tools).
 *   - Answered: a read-only summary of what was submitted.
 *
 * `onSubmit` is host-supplied because ANSWERING requires persisting the
 * response (an UpsertSessionAttachment call) — this component only builds
 * the typed AskCardResponse and hands it off; it never talks to llmd itself.
 */

import { Component, useState, type ErrorInfo, type ReactNode } from 'react';
import {
  Button,
  Checkbox,
  CloseButton,
  FileInput,
  Group,
  Radio,
  Stack,
  Text,
  Textarea,
} from '@mantine/core';
import type { AskCard, AskCardResponse, AskChoiceOption } from '@pyxis-llm/core';

/**
 * The explanation under one choice: its one-line description, then (for the
 * recommended option) the reason it is recommended, so neither has to be dug
 * out of the card's text. Spans, not divs: Mantine renders `description`
 * inside a <p>.
 */
function optionExplanation(cardId: string, option: AskChoiceOption | undefined): ReactNode {
  if (!option || (!option.description && !option.recommendation)) return undefined;
  return (
    <>
      {option.description && (
        <span style={{ display: 'block' }} data-testid={`ask-card-${cardId}-desc-${option.label}`}>
          {option.description}
        </span>
      )}
      {option.recommendation && (
        <Text
          component="span"
          size="xs"
          fw={500}
          c="teal"
          style={{ display: 'block' }}
          data-testid={`ask-card-${cardId}-rec-${option.label}`}
        >
          ★ Recommended: {option.recommendation}
        </Text>
      )}
    </>
  );
}

/**
 * How the agent should proceed after an ask-card answer. Passed alongside the
 * response by `AskCardView` and threaded to llmd's AnswerAskCard.
 */
export type AskCardSubmitOptions = {
  /**
   * true ("Answer & continue"): the answer's turn may run tools, so the agent
   * carries on working. false ("Answer & stop"): it replies without tools.
   */
  continueWithTools: boolean;
};

export type AskCardViewProps = {
  card: AskCard;
  /** The persisted response, if this card has already been answered. */
  response?: AskCardResponse | null;
  /** Called with the built response when the user submits. Omit to render read-only. */
  onSubmit?: (response: AskCardResponse, options: AskCardSubmitOptions) => void;
  /**
   * Dismisses the card as though it was never asked (shown as an ✕ on an
   * unanswered card). Omit to hide the ✕.
   */
  onDismiss?: () => void;
  disabled?: boolean;
};

/** The ✕ that dismisses an ask card. Shared by the card and its fallbacks. */
export function AskCardDismissButton({ cardId, onDismiss }: { cardId: string; onDismiss: () => void }) {
  return (
    <CloseButton
      size="sm"
      onClick={onDismiss}
      title="Dismiss this question"
      aria-label="Dismiss this question"
      data-testid={`ask-card-${cardId}-dismiss`}
    />
  );
}

/**
 * A muted one-liner for a card that cannot be shown (undecodable payload, or
 * it crashed while rendering), with the ✕ when dismissing is wired, so a
 * broken card can still be cleared away.
 */
export function AskCardUnavailable({
  cardId,
  label,
  onDismiss,
}: {
  cardId: string;
  label: string;
  onDismiss?: () => void;
}) {
  return (
    <Group gap={4} wrap="nowrap" data-testid={`ask-card-${cardId}-unavailable`}>
      <span title={`Ask card: ${cardId}`} style={{ opacity: 0.6, fontStyle: 'italic' }}>
        {label}
      </span>
      {onDismiss && <AskCardDismissButton cardId={cardId} onDismiss={onDismiss} />}
    </Group>
  );
}

type AskCardErrorBoundaryProps = { cardId: string; onDismiss?: () => void; children: ReactNode };

/**
 * Keeps one broken card from taking down the whole transcript: if the card
 * throws while rendering, it is replaced by a dismissable "failed to render"
 * line and the rest of the chat carries on.
 */
export class AskCardErrorBoundary extends Component<AskCardErrorBoundaryProps, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError(): { failed: boolean } {
    return { failed: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // eslint-disable-next-line no-console
    console.error(`AskCardView: card ${this.props.cardId} failed to render`, error, info.componentStack);
  }

  render(): ReactNode {
    if (this.state.failed) {
      return (
        <AskCardUnavailable
          cardId={this.props.cardId}
          label="[this question failed to render]"
          onDismiss={this.props.onDismiss}
        />
      );
    }
    return this.props.children;
  }
}

async function fileToBase64(file: File): Promise<string> {
  const buf = await file.arrayBuffer();
  const bytes = new Uint8Array(buf);
  let binary = '';
  for (let i = 0; i < bytes.byteLength; i++) binary += String.fromCharCode(bytes[i]!);
  return btoa(binary);
}

function isAnswerable(
  card: AskCard,
  selected: string[],
  text: string,
  file: File | null
): boolean {
  if (card.type === 'choice') return selected.length > 0;
  if (card.type === 'file') return file != null;
  return text.trim().length > 0;
}

/** One-line, human-readable summary of an answer ("Selected: A", "Uploaded: f.pdf", or the text). */
export function answerSummary(response: AskCardResponse): string {
  if (response.type === 'choice' && response.choice) {
    return response.choice.selected.length > 0
      ? `Selected: ${response.choice.selected.join(', ')}`
      : 'No option selected';
  }
  if (response.type === 'file' && response.file) {
    return `Uploaded: ${response.file.fileName}`;
  }
  return response.additionalText?.trim() || '(no answer)';
}

export function AskCardView({ card, response, onSubmit, onDismiss, disabled }: AskCardViewProps) {
  const [selected, setSelected] = useState<string[]>([]);
  const [text, setText] = useState('');
  const [additional, setAdditional] = useState('');
  const [file, setFile] = useState<File | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const answered = response != null;
  // Explanations by label (choices and options share labels, same order).
  const optionByLabel = new Map<string, AskChoiceOption>(
    (card.choice?.options ?? []).map((o) => [o.label, o])
  );

  async function handleSubmit(continueWithTools: boolean) {
    if (!onSubmit || submitting) return;
    setSubmitting(true);
    try {
      let built: AskCardResponse;
      if (card.type === 'choice') {
        built = {
          cardId: card.id,
          type: 'choice',
          choice: { selected },
          ...(card.allowAdditionalText && additional.trim()
            ? { additionalText: additional.trim() }
            : {}),
        };
      } else if (card.type === 'file') {
        if (!file) return;
        built = {
          cardId: card.id,
          type: 'file',
          file: {
            fileName: file.name,
            mimeType: file.type,
            sizeBytes: file.size,
            contentBase64: await fileToBase64(file),
          },
          ...(card.allowAdditionalText && additional.trim()
            ? { additionalText: additional.trim() }
            : {}),
        };
      } else {
        built = { cardId: card.id, type: 'text', additionalText: text.trim() };
      }
      onSubmit(built, { continueWithTools });
    } finally {
      setSubmitting(false);
    }
  }

  if (answered) {
    return (
      <Stack gap={4} className="ask-card ask-card--answered" data-testid={`ask-card-${card.id}`}>
        <Text fw={600} size="sm">
          {card.title}
        </Text>
        {card.text && (
          <Text size="sm" c="dimmed">
            {card.text}
          </Text>
        )}
        <Text size="sm" fw={500} data-testid={`ask-card-answer-${card.id}`}>
          {answerSummary(response!)}
        </Text>
        {response!.type !== 'text' && response!.additionalText?.trim() && (
          <Text size="sm" c="dimmed">
            {response!.additionalText}
          </Text>
        )}
      </Stack>
    );
  }

  return (
    <Stack gap={6} className="ask-card" data-testid={`ask-card-${card.id}`}>
      <Group justify="space-between" wrap="nowrap" align="flex-start" gap="xs">
        <Text fw={600} size="sm">
          {card.title}
        </Text>
        {onDismiss && <AskCardDismissButton cardId={card.id} onDismiss={onDismiss} />}
      </Group>
      {card.text && (
        <Text size="sm" c="dimmed">
          {card.text}
        </Text>
      )}

      {card.type === 'choice' &&
        card.choice &&
        (card.choice.allowMultiple ? (
          <Stack gap={6}>
            {card.choice.choices.map((choice) => (
              <Checkbox
                key={choice}
                label={choice}
                description={optionExplanation(card.id, optionByLabel.get(choice))}
                checked={selected.includes(choice)}
                disabled={disabled || submitting}
                onChange={(e) => {
                  // Read now: the updater runs later, after React has nulled
                  // the event's currentTarget.
                  const checked = e.currentTarget.checked;
                  setSelected((prev) =>
                    checked ? [...prev, choice] : prev.filter((c) => c !== choice)
                  );
                }}
                data-testid={`ask-card-${card.id}-choice-${choice}`}
              />
            ))}
          </Stack>
        ) : (
          <Radio.Group value={selected[0] ?? ''} onChange={(v) => setSelected([v])}>
            <Stack gap={6}>
              {card.choice.choices.map((choice) => (
                <Radio
                  key={choice}
                  value={choice}
                  label={choice}
                  description={optionExplanation(card.id, optionByLabel.get(choice))}
                  disabled={disabled || submitting}
                  data-testid={`ask-card-${card.id}-choice-${choice}`}
                />
              ))}
            </Stack>
          </Radio.Group>
        ))}

      {card.type === 'text' && (
        <Textarea
          value={text}
          onChange={(e) => setText(e.currentTarget.value)}
          disabled={disabled || submitting}
          data-testid={`ask-card-${card.id}-text`}
        />
      )}

      {card.type === 'file' && (
        <FileInput
          value={file}
          onChange={setFile}
          disabled={disabled || submitting}
          accept={card.file?.acceptedMimeTypes?.join(',') || undefined}
          data-testid={`ask-card-${card.id}-file`}
        />
      )}

      {card.allowAdditionalText && card.type !== 'text' && (
        <Textarea
          placeholder="Additional notes (optional)"
          value={additional}
          onChange={(e) => setAdditional(e.currentTarget.value)}
          disabled={disabled || submitting}
          data-testid={`ask-card-${card.id}-additional`}
        />
      )}

      <Group justify="flex-end" gap="xs">
        <Button
          size="xs"
          variant="light"
          color="orange"
          onClick={() => void handleSubmit(false)}
          disabled={disabled || submitting || !isAnswerable(card, selected, text, file)}
          title="Send the answer; the agent replies without running any tools"
          data-testid={`ask-card-${card.id}-submit-stop`}
        >
          Answer &amp; stop
        </Button>
        <Button
          size="xs"
          onClick={() => void handleSubmit(true)}
          disabled={disabled || submitting || !isAnswerable(card, selected, text, file)}
          title="Send the answer and let the agent keep working"
          data-testid={`ask-card-${card.id}-submit`}
        >
          Answer &amp; continue
        </Button>
      </Group>
    </Stack>
  );
}
