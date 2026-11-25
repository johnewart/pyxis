/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * Generic catalog model picker. Unlike the original it was extracted from, this component
 * does not fetch data itself: hosts pass `options` (+ optional `loading`/
 * `error`/`emptyState`), typically sourced from a `useLlmdModelCatalog()`
 * hook (see ./node.js / ./browser.js) or any other source of ChatModelOption[].
 */

import { useCallback, useMemo, useState, type ReactNode } from 'react';
import {
  Badge,
  Menu,
  Stack,
  Text,
  Tooltip,
  UnstyledButton,
} from '@mantine/core';
import { IconChevronDown } from '@tabler/icons-react';
import { resolveCanonicalModelOption } from './modelCatalogHelpers.js';
import type { ChatModelOption } from './chatModelOptions.js';
import { CatalogModelPickerMenuContent } from './CatalogModelPickerMenuContent.js';
import type { CatalogTreeBuilder } from './CatalogTreePickerOptions.js';
import {
  catalogPickerMenuBehaviorProps,
  catalogPickerMenuDropdownStyles,
} from './catalogPickerMenuStyles.js';

export type CatalogModelPickerProps = {
  options: ChatModelOption[];
  loading?: boolean;
  error?: Error | null;
  /** Rendered instead of the "no models" text when options is empty and not loading. */
  emptyState?: ReactNode;
  selectedProviderId: string | null;
  selectedModelId: string | null;
  onSelect: (providerId: string, modelId: string) => void;
  /** When omitted, derived from the selected pair and picker catalog. */
  displayLabel?: string;
  /** Optional badge node rendered after the label (e.g. scope indicator). */
  displayBadge?: ReactNode;
  /** Used when no model is selected and displayLabel is omitted. */
  emptyLabel?: string;
  header?: ReactNode;
  tooltip?: string;
  /** Shown under the trigger when the selection is unavailable in the catalog. */
  invalidHint?: string;
  /** Controlled state for the menu (optional). */
  menuOpened?: boolean;
  /** Callback when menu state changes. */
  onMenuOpenedChange?: (opened: boolean) => void;
  /** Callback fired when a selection is made via keyboard (Enter). Useful for closing the parent menu. */
  onCloseMenuAfterSelection?: () => void;
  /** Bump when the menu opens to reset the filter field. */
  menuOpenKey?: string | number;
  /** Callback for when the dropdown resizes to two columns. */
  onDropdownTwoColumns?: (twoColumns: boolean) => void;
  /** Callback after a model is selected (via click or keyboard). Useful for refocusing the input. */
  onAfterSelect?: () => void;
  /**
   * When provided, shows a "Reload models" row fixed at the bottom of a
   * provider's model column (regardless of how many models it has) that
   * calls this with the provider id — wire to e.g. `useLlmdModelCatalog()`'s
   * `refreshProvider`. A manual escape hatch instead of auto-refreshing every
   * provider on every picker open (which is slow).
   */
  onReloadProviderModels?: (providerId: string) => void | Promise<void>;
  /** What the filter box filters, as people read it (default "models"), for hosts picking something else from a catalog (e.g. branches). */
  filterNoun?: string;
  /** The trigger's accessible name (default: its label). */
  ariaLabel?: string;
  /** Put on the trigger button as data-testid, for host tests. */
  testId?: string;
  /**
   * Builds a provider's tree from its options, for hosts with their own
   * grouping (default: a flat list). Leaves and groups may share a level.
   */
  buildTree?: CatalogTreeBuilder;
  /** Keep the tree's own order instead of sorting each column by label. */
  keepOrder?: boolean;
  /**
   * Dropdown layout: 'columns' (default) browses providers then models in
   * columns; 'flat' lists every provider's models at once.
   */
  layout?: 'columns' | 'flat';
};

export function formatCatalogModelPickerLabel(
  value: { providerId: string; modelId: string } | null,
  options: ChatModelOption[],
  emptyLabel: string
): string {
  if (!value?.providerId?.trim() || !value?.modelId?.trim()) {
    return emptyLabel;
  }
  const canonical = resolveCanonicalModelOption(options, value) ?? value;
  const hit = options.find(
    (o) =>
      o.providerId === canonical.providerId && o.modelId === canonical.modelId
  );
  return hit
    ? `${hit.providerName}: ${hit.modelName}`
    : `${canonical.providerId}: ${canonical.modelId}`;
}

/** Generic model picker (chat header, default model settings, etc.). */
export function CatalogModelPicker({
  options,
  loading = false,
  error = null,
  emptyState,
  displayLabel: displayLabelProp,
  displayBadge,
  emptyLabel = 'Select a model',
  selectedProviderId,
  selectedModelId,
  onSelect,
  header,
  tooltip = 'Model for this session — click to change',
  invalidHint: invalidHintProp,
  menuOpened: menuOpenedProp,
  onMenuOpenedChange,
  onCloseMenuAfterSelection,
  menuOpenKey: menuOpenKeyProp,
  onDropdownTwoColumns,
  onAfterSelect,
  onReloadProviderModels,
  filterNoun,
  ariaLabel,
  testId,
  buildTree,
  keepOrder,
  layout,
}: CatalogModelPickerProps) {
  const allModelOptions = options;

  const [menuOpenKeyInternal, setMenuOpenKeyInternal] = useState(0);
  const [pickerTwoColumns, setPickerTwoColumns] = useState(false);
  const [menuOpenedInternal, setMenuOpenedInternal] = useState(false);

  const modelsListLoading = allModelOptions.length === 0 && !error && loading;

  const triggerRootStyle = {
    minWidth: 0,
    maxWidth: '100%',
    width: '100%',
    flexShrink: 0 as const,
  };

  const labelFontSize = '0.875rem';

  const selectedValue =
    selectedProviderId?.trim() && selectedModelId?.trim()
      ? { providerId: selectedProviderId, modelId: selectedModelId }
      : null;

  const displayLabel =
    displayLabelProp ??
    formatCatalogModelPickerLabel(selectedValue, allModelOptions, emptyLabel);

  const selectionInCatalog =
    selectedValue == null ||
    resolveCanonicalModelOption(allModelOptions, selectedValue) != null;

  const invalidHint =
    invalidHintProp ??
    (selectedValue && !selectionInCatalog
      ? 'Selected model is no longer available.'
      : undefined);

  const isInvalid = Boolean(invalidHint);

  // Use controlled state if provided, otherwise use internal state
  const menuOpened = menuOpenedProp ?? menuOpenedInternal;
  const setMenuOpened = (opened: boolean) => {
    if (menuOpenedProp === undefined) {
      setMenuOpenedInternal(opened);
    }
    onMenuOpenedChange?.(opened);
  };

  // Use controlled menuOpenKey if provided, otherwise use internal state
  const menuOpenKey = menuOpenKeyProp ?? menuOpenKeyInternal;
  const setMenuOpenKey = (fn: (key: number) => number) => {
    if (menuOpenKeyProp === undefined) {
      setMenuOpenKeyInternal((prevKey) => fn(prevKey));
    }
  };

  // Wrapper around onSelect to also call onAfterSelect
  const handleSelect = (providerId: string, modelId: string) => {
    onSelect(providerId, modelId);
    onAfterSelect?.();
  };

  // Stable callback so CatalogModelPickerMenuContent's reset effect doesn't
  // fire on every render and clear the filter input.
  const handleDropdownTwoColumns = useCallback(
    (twoColumns: boolean) => {
      setPickerTwoColumns(twoColumns);
      onDropdownTwoColumns?.(twoColumns);
    },
    [onDropdownTwoColumns]
  );

  const isOptionSelected = useMemo(
    () => (opt: ChatModelOption) =>
      selectedProviderId === opt.providerId && selectedModelId === opt.modelId,
    [selectedProviderId, selectedModelId]
  );

  if (allModelOptions.length === 0) {
    return (
      <Stack gap={4} style={{ maxWidth: '100%' }}>
        <Text size="sm">
          {modelsListLoading ? 'Loading models…' : 'No model available'}
        </Text>
        {error ? (
          <Text size="xs" c="red">
            {error.message}
          </Text>
        ) : null}
        {!modelsListLoading ? emptyState : null}
      </Stack>
    );
  }

  const trigger = (
    <UnstyledButton
      type="button"
      className={[
        'chat-form__model-pick',
        isInvalid ? 'chat-form__model-pick--invalid' : '',
      ]
        .filter(Boolean)
        .join(' ')}
      aria-haspopup="menu"
      aria-label={ariaLabel}
      data-testid={testId}
      aria-invalid={isInvalid || undefined}
      styles={{
        root: {
          ...triggerRootStyle,
          display: 'flex',
          alignItems: 'center',
          gap: 4,
          textAlign: 'left' as const,
          fontSize: labelFontSize,
          fontWeight: 400,
        },
      }}
    >
      <Badge
        size="sm"
        variant="light"
        color="violet"
        radius="sm"
        style={{
          flexShrink: 1,
          minWidth: 0,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          maxWidth: 200,
          pointerEvents: 'none',
          fontWeight: 500,
        }}
      >
        {displayLabel}
      </Badge>
      {displayBadge}
      <IconChevronDown
        size={18}
        stroke={2.5}
        aria-hidden
        className="chat-form__model-pick-chevron"
      />
    </UnstyledButton>
  );

  return (
    <Stack gap={2} style={{ maxWidth: '100%' }}>
      <Menu
        position="bottom-start"
        shadow="md"
        closeOnItemClick
        opened={menuOpened}
        {...catalogPickerMenuBehaviorProps}
        onChange={(opened) => {
          setMenuOpened(opened);
          if (opened) {
            setMenuOpenKey((key) => key + 1);
          } else {
            setPickerTwoColumns(false);
          }
        }}
        styles={catalogPickerMenuDropdownStyles(pickerTwoColumns)}
      >
        <Menu.Target>
          {tooltip ? <Tooltip label={tooltip}>{trigger}</Tooltip> : trigger}
        </Menu.Target>
        <Menu.Dropdown>
          <CatalogModelPickerMenuContent
            menuOpenKey={menuOpenKey}
            onDropdownTwoColumns={handleDropdownTwoColumns}
            options={allModelOptions}
            isOptionSelected={isOptionSelected}
            onSelect={handleSelect}
            header={header}
            onCloseMenuAfterKeyboardSelection={() => {
              setMenuOpened(false);
              onCloseMenuAfterSelection?.();
            }}
            onReloadProvider={onReloadProviderModels}
            filterNoun={filterNoun}
            buildTree={buildTree}
            keepOrder={keepOrder}
            layout={layout}
          />
        </Menu.Dropdown>
      </Menu>
      {invalidHint ? (
        <Text size="xs" c="red">
          {invalidHint}
        </Text>
      ) : null}
    </Stack>
  );
}
