/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import { forwardRef, memo, useCallback, useEffect, useImperativeHandle, useMemo, useState, type ReactNode } from 'react';
import { Box, Menu, Text, UnstyledButton, Loader } from '@mantine/core';
import { IconChevronRight, IconRefresh } from '@tabler/icons-react';
import type { CatalogTreeNode } from './modelCatalogTree.js';
import type { ChatModelOption } from './chatModelOptions.js';
import {
  CATALOG_PICKER_ACTIVE_BACKGROUND,
  CATALOG_PICKER_ACTIVE_COLOR,
  catalogPickerActiveColumnHeaderStyle,
  catalogPickerActiveColumnStyle,
  catalogPickerBrowserFrameStyle,
  catalogPickerColumnFooterStyle,
  catalogPickerColumnScrollStyle,
  catalogPickerInsetDividerStyle,
  catalogPickerMenuItemStylesForState,
} from './catalogPickerMenuStyles.js';
import {
  filterMultiProviderRoots,
  filterSelectableTreeNodes,
  makeResolveOption,
} from './catalogColumnBrowserTree.js';

export type CatalogColumnBrowserHandle = {
  /**
   * Forward a keyboard event from the parent (e.g. from the filter TextInput).
   * Handles ArrowUp / ArrowDown / ArrowRight / ArrowLeft / Enter.
   * Returns true if the event was consumed (caller should preventDefault).
   */
  handleKeyDown(key: string): boolean;
};

export type CatalogColumnBrowserProps = {
  roots: CatalogTreeNode[];
  optionByModelId: Map<string, ChatModelOption>;
  providerId: string;
  multiProvider?: boolean;
  onSelect: (providerId: string, modelId: string) => void;
  isOptionSelected?: (opt: ChatModelOption) => boolean;
  /** Bumps navigation back to the top level when changed (e.g. menu open). */
  resetKey?: string | number;
  /** Notifies parent when the browser switches between one and two columns. */
  onColumnLayoutChange?: (twoColumns: boolean) => void;
  /**
   * When provided, renders a "Reload models" row fixed at the bottom of the
   * pane showing a specific provider's models (regardless of how many models
   * it currently has) — a manual, per-provider escape hatch instead of
   * auto-refreshing every provider on every picker open.
   */
  onReloadProvider?: (providerId: string) => void | Promise<void>;
  /** Show nodes in the order given instead of sorting them by label (a host that orders its own tree). */
  keepOrder?: boolean;
};

function ReloadModelsButton({
  providerId,
  onReload,
}: {
  providerId: string;
  onReload: (providerId: string) => void | Promise<void>;
}) {
  const [reloading, setReloading] = useState(false);

  const handleClick = useCallback(() => {
    if (reloading) return;
    setReloading(true);
    void Promise.resolve(onReload(providerId)).finally(() => setReloading(false));
  }, [onReload, providerId, reloading]);

  return (
    <Box style={catalogPickerColumnFooterStyle()}>
      <UnstyledButton
        type="button"
        onClick={handleClick}
        disabled={reloading}
        style={{
          width: '100%',
          display: 'flex',
          alignItems: 'center',
          gap: 6,
          padding: '7px 12px',
          fontSize: 'var(--mantine-font-size-sm)',
          color: 'var(--mantine-color-dimmed)',
          opacity: reloading ? 0.6 : 1,
        }}
      >
        {reloading ? <Loader size={12} /> : <IconRefresh size={14} stroke={1.5} aria-hidden />}
        <Text component="span" size="sm">
          {reloading ? 'Reloading…' : 'Reload models'}
        </Text>
      </UnstyledButton>
    </Box>
  );
}

/** Known tier keywords in logical order (low → max). Anything else sorts alphabetically after. */
const TIER_RANK: Record<string, number> = { mini: 0, low: 1, medium: 2, med: 2, high: 3, max: 4, ultra: 5 };

function nodeSortKey(label: string): string {
  const lower = label.toLowerCase();
  for (const [word, rank] of Object.entries(TIER_RANK)) {
    if (lower === word || lower.startsWith(word + ' ') || lower.endsWith(' ' + word)) {
      return `\x00${rank}`;
    }
  }
  return label;
}

function nodeHasVisibleChildren(node: CatalogTreeNode): boolean {
  return node.kind === 'group' && (node.children?.length ?? 0) > 0;
}

function sortedNodes(nodes: CatalogTreeNode[], keepOrder = false): CatalogTreeNode[] {
  if (keepOrder) return nodes;
  return [...nodes].sort((a, b) => nodeSortKey(a.label).localeCompare(nodeSortKey(b.label)));
}

function ColumnRow({
  node,
  selected,
  showChevron,
  onClick,
  closeOnClick,
  isOptionSelected,
  resolveOption,
  showDivider,
  keyboardActive,
}: {
  node: CatalogTreeNode;
  selected: boolean;
  showChevron: boolean;
  onClick: () => void;
  closeOnClick: boolean;
  isOptionSelected?: (opt: ChatModelOption) => boolean;
  resolveOption: (modelId: string) => ChatModelOption | undefined;
  showDivider: boolean;
  keyboardActive: boolean;
}) {
  const leafSelected =
    node.kind === 'leaf' &&
    !!node.modelId &&
    (() => {
      const opt = resolveOption(node.modelId);
      return opt ? (isOptionSelected?.(opt) ?? false) : false;
    })();

  return (
    <Box
      component="div"
      style={{ width: '100%' }}
      data-picker-active={keyboardActive ? 'true' : undefined}
    >
      <Menu.Item
        closeMenuOnClick={closeOnClick}
        onClick={onClick}
        fw={selected || leafSelected ? 600 : undefined}
        rightSection={
          showChevron ? (
            <IconChevronRight size={12} stroke={1.5} aria-hidden />
          ) : undefined
        }
        styles={{
          ...catalogPickerMenuItemStylesForState({ selected, leafSelected }),
          item: {
            ...catalogPickerMenuItemStylesForState({ selected, leafSelected }).item,
            ...(keyboardActive && !selected
              ? { background: CATALOG_PICKER_ACTIVE_BACKGROUND, color: CATALOG_PICKER_ACTIVE_COLOR }
              : {}),
          },
        }}
      >
        <Text component="span" size="sm" fw={500} lineClamp={2}>
          {node.label}
        </Text>
      </Menu.Item>
      {showDivider ? <Box style={catalogPickerInsetDividerStyle()} /> : null}
    </Box>
  );
}

function Column({
  nodes,
  selectedId,
  keyboardCursorId,
  onNodeClick,
  side,
  isOptionSelected,
  resolveOption,
  activePane = false,
  paneLabel,
  footer,
  keepOrder = false,
}: {
  keepOrder?: boolean;
  nodes: CatalogTreeNode[];
  selectedId: string | null;
  keyboardCursorId: string | null;
  onNodeClick: (node: CatalogTreeNode, side: 'left' | 'right') => void;
  side: 'left' | 'right';
  isOptionSelected?: (opt: ChatModelOption) => boolean;
  resolveOption: (modelId: string) => ChatModelOption | undefined;
  /** Tint the pane when showing a selected parent's children. */
  activePane?: boolean;
  paneLabel?: string | null;
  /** Rendered fixed at the bottom of the column, below the scrollable node list. */
  footer?: ReactNode;
}) {
  return (
    <Box
      style={{
        flex: '1 1 0',
        minWidth: 0,
        width: '100%',
        height: '100%',
        minHeight: 0,
        display: 'flex',
        flexDirection: 'column',
        overflow: 'hidden',
        borderRight:
          side === 'left' ? '1px solid var(--mantine-color-default-border)' : undefined,
        ...(activePane ? catalogPickerActiveColumnStyle() : {}),
      }}
    >
      {activePane && paneLabel ? (
        <Box style={catalogPickerActiveColumnHeaderStyle()}>
          <Text size="sm" fw={600} lineClamp={1}>
            {paneLabel}
          </Text>
        </Box>
      ) : null}
      <Box style={catalogPickerColumnScrollStyle()}>
        {sortedNodes(nodes, keepOrder).map((node, index) => {
          const isGroup = nodeHasVisibleChildren(node);
          const selected = selectedId === node.id;
          const keyboardActive = side === 'right' && keyboardCursorId === node.id;
          return (
            <ColumnRow
              key={node.id}
              node={node}
              selected={selected}
              showChevron={isGroup}
              closeOnClick={!isGroup}
              onClick={() => onNodeClick(node, side)}
              isOptionSelected={isOptionSelected}
              resolveOption={resolveOption}
              showDivider={index < nodes.length - 1}
              keyboardActive={keyboardActive}
            />
          );
        })}
      </Box>
      {footer}
    </Box>
  );
}

/** macOS Finder-style two-column model browser (max two visible columns). */
const CatalogColumnBrowserComponent = forwardRef<
  CatalogColumnBrowserHandle,
  CatalogColumnBrowserProps
>(function CatalogColumnBrowserComponent(
  {
    roots,
    optionByModelId,
    providerId,
    multiProvider = false,
    onSelect,
    isOptionSelected,
    resetKey,
    onColumnLayoutChange,
    onReloadProvider,
    keepOrder = false,
  },
  ref
) {
  const [path, setPath] = useState<CatalogTreeNode[]>([]);
  /** Keyboard cursor in the right (active) column. */
  const [rightColCursorId, setRightColCursorId] = useState<string | null>(null);

  useEffect(() => {
    setPath([]);
    setRightColCursorId(null);
  }, [resetKey, roots]);

  const showTwoColumns = path.length > 0;

  useEffect(() => {
    onColumnLayoutChange?.(showTwoColumns);
  }, [showTwoColumns, onColumnLayoutChange]);

  const activeProviderId =
    multiProvider && path.length > 0 ? path[0]!.id : providerId;

  const resolveOption = useMemo(
    () => makeResolveOption(activeProviderId, optionByModelId, multiProvider),
    [activeProviderId, multiProvider, optionByModelId]
  );

  const browsableRoots = useMemo(() => {
    if (multiProvider && path.length === 0) {
      return filterMultiProviderRoots(roots, optionByModelId, multiProvider);
    }
    return filterSelectableTreeNodes(roots, resolveOption);
  }, [roots, path.length, multiProvider, optionByModelId, resolveOption]);

  const leftColumn = useMemo(() => {
    if (path.length === 0) return null;
    if (path.length === 1) {
      return { nodes: browsableRoots, selectedId: path[0]!.id };
    }
    const parent = path[path.length - 2]!;
    return {
      nodes: filterSelectableTreeNodes(parent.children ?? [], resolveOption),
      selectedId: path[path.length - 1]!.id,
    };
  }, [path, browsableRoots, resolveOption]);

  const rightColumn = useMemo(() => {
    if (path.length === 0) {
      return { nodes: browsableRoots, selectedId: null as string | null };
    }
    const current = path[path.length - 1]!;
    return {
      nodes: filterSelectableTreeNodes(current.children ?? [], resolveOption),
      selectedId: null as string | null,
    };
  }, [path, browsableRoots, resolveOption]);

  const activeParentLabel =
    showTwoColumns && path.length > 0 ? path[path.length - 1]!.label : null;

  // "Second-level nav for a provider": once we're browsing a specific provider's
  // own models (either the only provider in a single-provider picker, or one
  // level into a multi-provider tree), a reload affordance belongs at the
  // bottom of that pane — not on the top-level provider-picker column.
  const showReloadFooter =
    Boolean(onReloadProvider) && (multiProvider ? path.length > 0 : true);
  const rightColumnFooter =
    showReloadFooter && onReloadProvider ? (
      <ReloadModelsButton providerId={activeProviderId} onReload={onReloadProvider} />
    ) : undefined;

  const handleNodeClick = useCallback((node: CatalogTreeNode, side: 'left' | 'right') => {
    if (node.kind === 'leaf' && node.modelId) {
      const opt = resolveOption(node.modelId);
      if (opt) onSelect(opt.providerId, node.modelId);
      return;
    }

    if (!nodeHasVisibleChildren(node)) return;

    if (side === 'right') {
      setPath((prev) => [...prev, node]);
      setRightColCursorId(null);
      return;
    }

    // Left column: re-select sibling or pop when clicking the active row.
    setPath((prev) => {
      if (prev.length === 0) return [node];
      const active = prev[prev.length - 1]!;
      if (active.id === node.id) {
        return prev.slice(0, -1);
      }
      return [...prev.slice(0, -1), node];
    });
    setRightColCursorId(null);
  }, [resolveOption, onSelect]);

  useImperativeHandle(ref, () => ({
    handleKeyDown(key: string): boolean {
      const sorted = sortedNodes(rightColumn.nodes, keepOrder);
      if (sorted.length === 0) return false;

      if (key === 'ArrowDown') {
        setRightColCursorId((prev) => {
          if (prev === null) return sorted[0]?.id ?? null;
          const idx = sorted.findIndex((n) => n.id === prev);
          return sorted[Math.min(idx + 1, sorted.length - 1)]?.id ?? prev;
        });
        return true;
      }

      if (key === 'ArrowUp') {
        setRightColCursorId((prev) => {
          if (prev === null) return sorted[sorted.length - 1]?.id ?? null;
          const idx = sorted.findIndex((n) => n.id === prev);
          return sorted[Math.max(idx - 1, 0)]?.id ?? prev;
        });
        return true;
      }

      if (key === 'ArrowRight' || key === 'Enter') {
        if (rightColCursorId === null) return false;
        const node = sorted.find((n) => n.id === rightColCursorId);
        if (!node) return false;
        if (node.kind === 'leaf' && node.modelId) {
          const opt = resolveOption(node.modelId);
          if (opt) onSelect(opt.providerId, node.modelId);
          return true;
        }
        if (nodeHasVisibleChildren(node)) {
          setPath((prev) => [...prev, node]);
          setRightColCursorId(null);
          return true;
        }
        return false;
      }

      if (key === 'ArrowLeft') {
        if (path.length === 0) return false;
        const leavingNode = path[path.length - 1]!;
        setPath((prev) => prev.slice(0, -1));
        setRightColCursorId(leavingNode.id);
        return true;
      }

      return false;
    },
  }), [rightColumn.nodes, rightColCursorId, path, resolveOption, onSelect, keepOrder]);

  if (browsableRoots.length === 0) {
    return (
      <Menu.Item disabled>
        <Text size="sm" c="dimmed" fw={500}>
          No models
        </Text>
      </Menu.Item>
    );
  }

  return (
    <Box style={catalogPickerBrowserFrameStyle()}>
      {showTwoColumns && leftColumn ? (
        <Column
          nodes={leftColumn.nodes}
          selectedId={leftColumn.selectedId}
          keyboardCursorId={null}
          onNodeClick={handleNodeClick}
          side="left"
          isOptionSelected={isOptionSelected}
          resolveOption={resolveOption}
          keepOrder={keepOrder}
        />
      ) : null}
      <Column
        nodes={rightColumn.nodes}
        selectedId={rightColumn.selectedId}
        keyboardCursorId={rightColCursorId}
        onNodeClick={handleNodeClick}
        side="right"
        isOptionSelected={isOptionSelected}
        resolveOption={resolveOption}
        activePane={showTwoColumns}
        paneLabel={activeParentLabel}
        footer={rightColumnFooter}
        keepOrder={keepOrder}
      />
    </Box>
  );
});

export const CatalogColumnBrowser = memo(CatalogColumnBrowserComponent);
