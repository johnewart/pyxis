/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import { memo, useMemo, type ReactNode, type RefObject } from 'react';
import { Combobox, Menu, Text } from '@mantine/core';
import {
  buildCatalogTree,
  catalogTreeBreadcrumb,
  catalogTreeIsFlat,
  formatCatalogTreeBreadcrumb,
  type CatalogTree,
  type CatalogTreeNode,
} from './modelCatalogTree.js';
import {
  encodeChatModelOptionValue,
  type ChatModelOption,
} from './chatModelOptions.js';
import { CatalogColumnBrowser, type CatalogColumnBrowserHandle } from './CatalogColumnBrowser.js';
import { CatalogPickerMenuItem } from './CatalogPickerMenuItem.js';
import { catalogPickerMenuLabelStyle } from './catalogPickerMenuStyles.js';

/**
 * Builds one provider's tree from its options (leaves' modelId = an option's
 * modelId). Hosts with their own grouping pass it; the default is a flat list.
 */
export type CatalogTreeBuilder = (options: ChatModelOption[]) => CatalogTree;

export type CatalogTreePickerOptionsProps = {
  options: ChatModelOption[];
  /** A provider's tree from its options (default: flat, alphabetical). */
  buildTree?: CatalogTreeBuilder;
  /** Keep the tree's own order in the column browser instead of sorting by label. */
  keepOrder?: boolean;
  selected?: { providerId: string; modelId: string } | null;
  onSelect: (providerId: string, modelId: string) => void;
  variant: 'combobox' | 'menu';
  /** When set, only matching leaves are shown with breadcrumb labels. */
  searchQuery?: string;
  /** Bumps column browser navigation when changed (e.g. menu open). */
  menuOpenKey?: string | number;
  /** Notifies parent when the column browser switches between one and two columns. */
  onColumnLayoutChange?: (twoColumns: boolean) => void;
  isOptionSelected?: (opt: ChatModelOption) => boolean;
  /**
   * Keyboard-nav active index into the flat filtered list (search mode only).
   * The item at this index receives the `active` highlight.
   */
  activeIndex?: number | null;
  /** Ref forwarded to the CatalogColumnBrowser for imperative keyboard navigation. */
  browserRef?: RefObject<CatalogColumnBrowserHandle | null>;
  /** Forwarded to CatalogColumnBrowser — see its doc comment. Menu variant only. */
  onReloadProvider?: (providerId: string) => void | Promise<void>;
  /**
   * How the menu variant shows an unfiltered catalog: 'columns' (default) is
   * the Finder-style column browser; 'flat' lists every provider's models at
   * once, under a label per provider, each row named for the model alone.
   */
  layout?: 'columns' | 'flat';
};

function groupOptionsByProvider(
  options: ChatModelOption[]
): Array<{ providerId: string; providerName: string; options: ChatModelOption[] }> {
  const groups: Array<{
    providerId: string;
    providerName: string;
    options: ChatModelOption[];
  }> = [];
  const indexById = new Map<string, number>();

  for (const opt of options) {
    const existing = indexById.get(opt.providerId);
    if (existing != null) {
      groups[existing]!.options.push(opt);
      continue;
    }
    indexById.set(opt.providerId, groups.length);
    groups.push({
      providerId: opt.providerId,
      providerName: opt.providerName,
      options: [opt],
    });
  }

  return groups;
}

function normalizeSearchQuery(raw?: string): string {
  return (raw ?? '').trim().toLowerCase();
}

function leafMatchesSearch(
  opt: ChatModelOption,
  breadcrumb: string[] | null,
  query: string
): boolean {
  if (!query) return true;
  const haystack = [
    opt.providerName,
    opt.modelName,
    opt.modelId,
    breadcrumb ? formatCatalogTreeBreadcrumb(breadcrumb) : '',
  ]
    .join(' ')
    .toLowerCase();
  return haystack.includes(query);
}

type RenderCtx = {
  providerId: string;
  providerName: string;
  tree: CatalogTree;
  optionByModelId: Map<string, ChatModelOption>;
  searchQuery: string;
  isOptionSelected?: (opt: ChatModelOption) => boolean;
  onSelect: (providerId: string, modelId: string) => void;
  variant: 'combobox' | 'menu';
  /** Shared mutable counter tracking the linear index of each rendered leaf. */
  flatIndexRef: { current: number };
  activeIndex?: number | null;
  /** Prefix each row with the provider's name (off in the flat layout, which labels providers separately). */
  showProviderPrefix?: boolean;
};

function buildProviderTreeRoots(
  group: {
    providerId: string;
    providerName: string;
    options: ChatModelOption[];
  },
  buildTree?: CatalogTreeBuilder
): { tree: CatalogTree; optionByModelId: Map<string, ChatModelOption> } {
  const tree = buildTree
    ? buildTree(group.options)
    : buildCatalogTree(
        group.options.map((opt) => ({
          id: opt.modelId,
          name: opt.modelName,
        }))
      );
  const optionByModelId = new Map(
    group.options.map((opt) => [opt.modelId, opt] as const)
  );
  return { tree, optionByModelId };
}

function wrapProviderRoots(
  providerGroups: Array<{
    providerId: string;
    providerName: string;
    options: ChatModelOption[];
  }>,
  buildTree?: CatalogTreeBuilder
): {
  roots: CatalogTreeNode[];
  optionByModelId: Map<string, ChatModelOption>;
  multiProvider: boolean;
} {
  const multiProvider = providerGroups.length > 1;
  const optionByModelId = new Map<string, ChatModelOption>();
  const roots: CatalogTreeNode[] = [];

  for (const group of providerGroups) {
    for (const opt of group.options) {
      const key = multiProvider
        ? `${group.providerId}:${opt.modelId}`
        : opt.modelId;
      optionByModelId.set(key, opt);
    }

    const { tree } = buildProviderTreeRoots(group, buildTree);

    if (!multiProvider) {
      return { roots: tree.roots, optionByModelId, multiProvider: false };
    }

    roots.push({
      id: group.providerId,
      kind: 'group',
      label: group.providerName,
      children: tree.roots,
    });
  }

  return { roots, optionByModelId, multiProvider: true };
}

/** Flat filtered list with breadcrumb labels (used for combobox and menu search). */
function renderTreeNodesFlat(
  nodes: CatalogTreeNode[],
  depth: number,
  ctx: RenderCtx
): ReactNode[] {
  const out: ReactNode[] = [];

  for (const node of nodes) {
    if (node.kind === 'leaf' && node.modelId) {
      const opt = ctx.optionByModelId.get(node.modelId);
      if (!opt) continue;
      const breadcrumb = catalogTreeBreadcrumb(ctx.tree, node.modelId);
      if (!leafMatchesSearch(opt, breadcrumb, ctx.searchQuery)) continue;

      const itemIndex = ctx.flatIndexRef.current++;
      const isActive = ctx.activeIndex != null && itemIndex === ctx.activeIndex;
      const selected = ctx.isOptionSelected?.(opt) ?? false;
      const label =
        ctx.searchQuery && breadcrumb?.length
          ? formatCatalogTreeBreadcrumb(breadcrumb)
          : ctx.showProviderPrefix === false
            ? node.label
            : `${ctx.providerName}: ${node.label}`;

      if (ctx.variant === 'combobox') {
        out.push(
          <Combobox.Option
            key={encodeChatModelOptionValue(opt)}
            value={encodeChatModelOptionValue(opt)}
            style={{
              fontWeight: selected ? 600 : undefined,
              paddingLeft: 12 + depth * 12,
            }}
          >
            {label}
          </Combobox.Option>
        );
      } else {
        out.push(
          <CatalogPickerMenuItem
            key={`${opt.providerId}:${opt.modelId}`}
            onClick={() => ctx.onSelect(opt.providerId, opt.modelId)}
            fw={selected ? 600 : undefined}
            paddingLeft={12 + depth * 12}
            active={isActive}
          >
            {label}
          </CatalogPickerMenuItem>
        );
      }
      continue;
    }

    if (node.kind !== 'group' || !node.children?.length) continue;

    const childNodes = renderTreeNodesFlat(node.children, depth + 1, ctx);
    if (childNodes.length === 0) continue;

    if (ctx.variant === 'combobox') {
      out.push(
        <Combobox.Group
          key={node.id}
          label={
            <Text size="xs" c="dimmed" pl={depth * 12}>
              {node.label}
            </Text>
          }
        >
          {childNodes}
        </Combobox.Group>
      );
    } else {
      out.push(
        <Menu.Label
          key={node.id}
          style={{ paddingLeft: 8 + depth * 12, ...catalogPickerMenuLabelStyle }}
        >
          {node.label}
        </Menu.Label>
      );
      out.push(...childNodes);
    }
  }

  return out;
}

function renderFlatProviderOptions(
  providerName: string,
  providerOptions: ChatModelOption[],
  ctx: Omit<RenderCtx, 'tree' | 'optionByModelId' | 'providerId' | 'providerName'>
): ReactNode[] {
  const matched = providerOptions.filter(
    (opt) =>
      leafMatchesSearch(opt, null, ctx.searchQuery) || ctx.searchQuery.length === 0
  );
  const out: ReactNode[] = [];
  for (let index = 0; index < matched.length; index += 1) {
    const opt = matched[index]!;
    const itemIndex = ctx.flatIndexRef.current++;
    const isActive = ctx.activeIndex != null && itemIndex === ctx.activeIndex;
    const selected = ctx.isOptionSelected?.(opt) ?? false;
    const label =
      ctx.showProviderPrefix === false
        ? opt.modelName
        : `${providerName}: ${opt.modelName}`;

    if (ctx.variant === 'combobox') {
      out.push(
        <Combobox.Option
          key={encodeChatModelOptionValue(opt)}
          value={encodeChatModelOptionValue(opt)}
          style={{ fontWeight: selected ? 600 : undefined }}
        >
          {label}
        </Combobox.Option>
      );
    } else {
      out.push(
        <CatalogPickerMenuItem
          key={`${opt.providerId}:${opt.modelId}`}
          onClick={() => ctx.onSelect(opt.providerId, opt.modelId)}
          fw={selected ? 600 : undefined}
          showDivider={index < matched.length - 1}
          active={isActive}
        >
          {label}
        </CatalogPickerMenuItem>
      );
    }
  }
  return out;
}

/**
 * Returns the flat ordered list of ChatModelOption leaves that match `rawQuery`,
 * in the same order they are rendered by CatalogTreePickerOptions (search mode).
 * Used by CatalogModelPickerMenuContent to drive keyboard activeIndex.
 */
export function getFilteredFlatOptions(
  options: ChatModelOption[],
  rawQuery: string,
  buildTree?: CatalogTreeBuilder
): ChatModelOption[] {
  const query = normalizeSearchQuery(rawQuery);
  if (!query) return [];

  const providerGroups = groupOptionsByProvider(options);
  const result: ChatModelOption[] = [];

  for (const group of providerGroups) {
    const { tree, optionByModelId } = buildProviderTreeRoots(group, buildTree);

    if (catalogTreeIsFlat(tree)) {
      for (const opt of group.options) {
        if (leafMatchesSearch(opt, null, query)) result.push(opt);
      }
    } else {
      collectMatchedLeaves(tree.roots, tree, query, optionByModelId, result);
    }
  }

  return result;
}

function collectMatchedLeaves(
  nodes: CatalogTreeNode[],
  tree: CatalogTree,
  query: string,
  optionByModelId: Map<string, ChatModelOption>,
  result: ChatModelOption[]
): void {
  for (const node of nodes) {
    if (node.kind === 'leaf' && node.modelId) {
      const opt = optionByModelId.get(node.modelId);
      if (!opt) continue;
      const breadcrumb = catalogTreeBreadcrumb(tree, node.modelId);
      if (leafMatchesSearch(opt, breadcrumb, query)) result.push(opt);
    } else if (node.kind === 'group' && node.children?.length) {
      collectMatchedLeaves(node.children, tree, query, optionByModelId, result);
    }
  }
}

const CatalogTreePickerOptionsComponent = ({
  options,
  buildTree,
  keepOrder,
  onSelect,
  variant,
  searchQuery,
  menuOpenKey,
  onColumnLayoutChange,
  isOptionSelected,
  activeIndex,
  browserRef,
  onReloadProvider,
  layout = 'columns',
}: CatalogTreePickerOptionsProps) => {
  const normalizedSearch = normalizeSearchQuery(searchQuery);
  const providerGroups = useMemo(
    () => groupOptionsByProvider(options),
    [options]
  );

  const browserData = useMemo(
    () => wrapProviderRoots(providerGroups, buildTree),
    [providerGroups, buildTree]
  );

  const fallbackProviderId = providerGroups[0]?.providerId ?? '';

  if (variant === 'menu' && normalizedSearch.length === 0 && layout !== 'flat') {
    return (
      <CatalogColumnBrowser
        ref={browserRef as RefObject<CatalogColumnBrowserHandle> | undefined}
        roots={browserData.roots}
        optionByModelId={browserData.optionByModelId}
        providerId={fallbackProviderId}
        multiProvider={browserData.multiProvider}
        onSelect={onSelect}
        isOptionSelected={isOptionSelected}
        resetKey={menuOpenKey ?? options.map((o) => encodeChatModelOptionValue(o)).join('|')}
        onColumnLayoutChange={onColumnLayoutChange}
        onReloadProvider={onReloadProvider}
        keepOrder={keepOrder}
      />
    );
  }

  // Shared mutable counter — must be reset each render so indices are stable.
  const flatIndexRef = { current: 0 };
  // In the flat layout each provider gets its own label row, so the model rows
  // themselves are named for the model alone.
  const showProviderPrefix = !(layout === 'flat' && normalizedSearch.length === 0);
  const providerLabel = (group: { providerId: string; providerName: string }) =>
    layout === 'flat' &&
    normalizedSearch.length === 0 &&
    variant === 'menu' &&
    providerGroups.length > 1
      ? [
          <Menu.Label key={`label-${group.providerId}`} style={catalogPickerMenuLabelStyle}>
            {group.providerName}
          </Menu.Label>,
        ]
      : [];

  const rendered = providerGroups.flatMap((group): ReactNode[] => {
    const { tree, optionByModelId } = buildProviderTreeRoots(group, buildTree);

    if (catalogTreeIsFlat(tree)) {
      return [
        ...providerLabel(group),
        ...renderFlatProviderOptions(group.providerName, group.options, {
          searchQuery: normalizedSearch,
          isOptionSelected,
          onSelect,
          variant,
          flatIndexRef,
          activeIndex,
          showProviderPrefix,
        }),
      ];
    }

    const ctx: RenderCtx = {
      providerId: group.providerId,
      providerName: group.providerName,
      tree,
      optionByModelId,
      searchQuery: normalizedSearch,
      isOptionSelected,
      onSelect,
      variant,
      flatIndexRef,
      activeIndex,
      showProviderPrefix,
    };

    const nodes = renderTreeNodesFlat(tree.roots, 0, ctx);
    if (nodes.length === 0) return [];

    if (variant === 'combobox' && providerGroups.length > 1) {
      return [
        <Combobox.Group key={group.providerId} label={group.providerName}>
          {nodes}
        </Combobox.Group>,
      ];
    }

    return [...providerLabel(group), ...nodes];
  });

  return <>{rendered}</>;
};

export const CatalogTreePickerOptions = memo(CatalogTreePickerOptionsComponent);
