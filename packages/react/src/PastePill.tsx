/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import type { KeyboardEvent, ReactNode } from 'react';

export function pastePillLabel(number: number, lineCount: number): string {
  const lines =
    lineCount === 1 ? '1 line' : `${Math.max(lineCount, 1)} lines`;
  return `Paste #${number} · ${lines}`;
}

function pastePillClassName(className?: string, extra?: string): string {
  return ['paste-pill', extra, className].filter(Boolean).join(' ');
}

function activateOnEnterOrSpace(
  event: KeyboardEvent<HTMLElement>,
  action?: () => void
) {
  if (!action) return;
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault();
    action();
  }
}

/**
 * A small chip representing one captured multi-line clipboard paste, used
 * both in the composer (removable, optionally clickable to edit) and inline
 * in a rendered message (static or clickable). Purely presentational —
 * hosts own the paste content/state; this just renders the label + controls.
 */
export function PastePill({
  number,
  lineCount,
  onClick,
  onRemove,
  className,
  title,
}: {
  number: number;
  lineCount: number;
  onClick?: () => void;
  onRemove?: () => void;
  className?: string;
  title?: string;
}) {
  const label = pastePillLabel(number, lineCount);
  const resolvedTitle = title ?? (onClick ? `Edit ${label}` : label);

  const removeControl: ReactNode = onRemove ? (
    <span
      role="button"
      tabIndex={0}
      className="paste-pill__remove"
      aria-label={`Remove ${label}`}
      onClick={(e) => {
        e.stopPropagation();
        e.preventDefault();
        onRemove();
      }}
      onKeyDown={(e) => activateOnEnterOrSpace(e, onRemove)}
    >
      ×
    </span>
  ) : null;

  if (onRemove) {
    return (
      <span
        className={pastePillClassName(className, 'paste-pill--chip')}
        title={resolvedTitle}
      >
        <span
          className={[
            'paste-pill__label',
            onClick ? 'paste-pill__label--clickable' : null,
          ]
            .filter(Boolean)
            .join(' ')}
          role={onClick ? 'button' : undefined}
          tabIndex={onClick ? 0 : undefined}
          title={resolvedTitle}
          onClick={onClick}
          onKeyDown={(e) => activateOnEnterOrSpace(e, onClick)}
        >
          {label}
        </span>
        {removeControl}
      </span>
    );
  }

  if (onClick) {
    return (
      <button
        type="button"
        className={pastePillClassName(className)}
        title={resolvedTitle}
        onClick={onClick}
      >
        <span className="paste-pill__label">{label}</span>
      </button>
    );
  }

  return (
    <span
      className={pastePillClassName(className, 'paste-pill--static')}
      title={resolvedTitle}
    >
      <span className="paste-pill__label">{label}</span>
    </span>
  );
}
