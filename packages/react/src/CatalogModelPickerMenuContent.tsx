/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import { useEffect, useRef, useState, type ReactNode, type KeyboardEvent as ReactKeyboardEvent } from 'react';
import { Box, TextInput } from '@mantine/core';
import { IconSearch } from '@tabler/icons-react';
import {
  CatalogTreePickerOptions,
  getFilteredFlatOptions,
  type CatalogTreePickerOptionsProps,
} from './CatalogTreePickerOptions.js';
import type { CatalogColumnBrowserHandle } from './CatalogColumnBrowser.js';
import { catalogPickerInsetDividerStyle, catalogPickerSearchResultsScrollStyle } from './catalogPickerMenuStyles.js';

export type CatalogModelPickerMenuContentProps = Omit<
  CatalogTreePickerOptionsProps,
  'variant' | 'searchQuery' | 'activeIndex'
> & {
  /** Renders below the filter box (e.g. "Use default model", labels). */
  header?: ReactNode;
  /** Bump when the menu opens to reset the filter field. */
  menuOpenKey?: string | number;
  /** Resizes the menu dropdown when the column browser drills into a second column. */
  onDropdownTwoColumns?: (twoColumns: boolean) => void;
  /** Called when a selection is made via keyboard (Enter key). */
  onCloseMenuAfterKeyboardSelection?: () => void;
  /** What the filter box filters, as people read it (default "models"). */
  filterNoun?: string;
};

/** Model picker dropdown body: quick-filter input + optional header + tree browser. */
export function CatalogModelPickerMenuContent({
  header,
  menuOpenKey,
  onDropdownTwoColumns,
  options,
  onSelect,
  isOptionSelected,
  onCloseMenuAfterKeyboardSelection,
  onReloadProvider,
  filterNoun = 'models',
  buildTree,
  keepOrder,
  layout,
}: CatalogModelPickerMenuContentProps) {
  const [filter, setFilter] = useState('');
  const [activeIndex, setActiveIndex] = useState<number | null>(null);
  const filterInputRef = useRef<HTMLInputElement>(null);
  const browserRef = useRef<CatalogColumnBrowserHandle>(null);
  const listRef = useRef<HTMLDivElement>(null);

  // Reset on menu open
  useEffect(() => {
    setFilter('');
    setActiveIndex(null);
    onDropdownTwoColumns?.(false);
    const frame = requestAnimationFrame(() => {
      filterInputRef.current?.focus({ preventScroll: true });
    });
    return () => cancelAnimationFrame(frame);
  }, [menuOpenKey, onDropdownTwoColumns]);

  // When filter changes to non-empty, auto-select first match
  useEffect(() => {
    if (filter.trim()) {
      setActiveIndex(0);
    } else {
      setActiveIndex(null);
    }
    onDropdownTwoColumns?.(false);
  }, [filter, onDropdownTwoColumns]);

  // Flat option list for the current filter — mirrors CatalogTreePickerOptions render order
  const flatOptions = filter.trim()
    ? getFilteredFlatOptions(options, filter, buildTree, keepOrder)
    : [];

  // Scroll the active item into view when activeIndex changes
  useEffect(() => {
    if (activeIndex == null || !listRef.current) return;
    const el = listRef.current.querySelector('[data-picker-active="true"]');
    el?.scrollIntoView({ block: 'nearest' });
  }, [activeIndex]);

  const handleKeyDown = (e: ReactKeyboardEvent<HTMLInputElement>) => {
    // Always prevent Mantine Menu from intercepting arrow navigation
    if (['ArrowDown', 'ArrowUp', 'ArrowLeft', 'ArrowRight'].includes(e.key)) {
      e.stopPropagation();
    }

    const hasFilter = Boolean(filter.trim());

    if (hasFilter) {
      // ── Search-mode navigation (flat list) ──────────────────────────────
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setActiveIndex((prev) =>
          prev == null ? 0 : Math.min(prev + 1, flatOptions.length - 1)
        );
        return;
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault();
        setActiveIndex((prev) =>
          prev == null ? Math.max(flatOptions.length - 1, 0) : Math.max(prev - 1, 0)
        );
        return;
      }
      if (e.key === 'Enter') {
        const opt =
          activeIndex != null ? flatOptions[activeIndex] : flatOptions[0];
        if (opt) {
          e.preventDefault();
          e.stopPropagation();
          onSelect(opt.providerId, opt.modelId);
          // Close menu after keyboard selection
          onCloseMenuAfterKeyboardSelection?.();
        }
        return;
      }
    } else {
      // ── Browse-mode navigation (column browser) ─────────────────────────
      if (
        e.key === 'ArrowDown' ||
        e.key === 'ArrowUp' ||
        e.key === 'ArrowRight' ||
        e.key === 'ArrowLeft' ||
        e.key === 'Enter'
      ) {
        const consumed = browserRef.current?.handleKeyDown(e.key) ?? false;
        if (consumed) {
          e.preventDefault();
          e.stopPropagation();
          // If it was Enter in browse mode, also close the menu
          if (e.key === 'Enter') {
            onCloseMenuAfterKeyboardSelection?.();
          }
        }
        return;
      }
    }
  };

  return (
    <>
      <Box px="xs" pt={6} pb={6}>
        <TextInput
          ref={filterInputRef}
          size="xs"
          placeholder={`Filter ${filterNoun}…`}
          value={filter}
          onChange={(event) => setFilter(event.currentTarget.value)}
          onMouseDown={(event) => event.stopPropagation()}
          onClick={(event) => event.stopPropagation()}
          onKeyDown={handleKeyDown}
          leftSection={<IconSearch size={14} stroke={1.5} aria-hidden />}
          aria-label={`Filter ${filterNoun}`}
        />
      </Box>
      <Box style={catalogPickerInsetDividerStyle()} />
      {header}
      <Box
        ref={listRef}
        style={{
          width: '100%',
          ...(filter.trim() ? catalogPickerSearchResultsScrollStyle() : undefined),
        }}
      >
        <CatalogTreePickerOptions
          options={options}
          buildTree={buildTree}
          keepOrder={keepOrder}
          layout={layout}
          variant="menu"
          searchQuery={filter}
          isOptionSelected={isOptionSelected}
          onSelect={onSelect}
          menuOpenKey={menuOpenKey}
          onColumnLayoutChange={onDropdownTwoColumns}
          activeIndex={filter.trim() ? activeIndex : null}
          browserRef={browserRef}
          onReloadProvider={onReloadProvider}
        />
      </Box>
    </>
  );
}
