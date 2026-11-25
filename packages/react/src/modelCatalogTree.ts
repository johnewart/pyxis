/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * Generic model-catalog tree: groups a flat model list into a browsable
 * hierarchy. Only the flat (no grouping) strategy ships here — hosts with
 * product-specific tiering (e.g. grouping Claude models by base model +
 * effort/thinking variant) build their own `CatalogTreeNode[]` and pass it
 * directly to `CatalogColumnBrowser`/`CatalogTreePickerOptions`, which don't
 * care how the tree was built.
 */

export type CatalogTreeNodeKind = 'group' | 'leaf';

export type CatalogTreeNode = {
  id: string;
  kind: CatalogTreeNodeKind;
  label: string;
  children?: CatalogTreeNode[];
  /** leaf only */
  modelId?: string;
};

export type CatalogTreeModelInput = {
  id: string;
  name: string;
};

export type CatalogTree = { roots: CatalogTreeNode[] };

/** Flat (ungrouped, alphabetical) catalog tree — the generic default. */
export function buildCatalogTree(models: CatalogTreeModelInput[]): CatalogTree {
  const sorted = [...models].sort((a, b) =>
    (a.name || a.id).localeCompare(b.name || b.id, undefined, {
      sensitivity: 'base',
    })
  );
  return {
    roots: sorted.map((model) => ({
      id: model.id,
      kind: 'leaf',
      label: model.name.trim() || model.id,
      modelId: model.id,
    })),
  };
}

const modelById = (
  models: CatalogTreeModelInput[]
): Map<string, CatalogTreeModelInput> => {
  const map = new Map<string, CatalogTreeModelInput>();
  for (const model of models) {
    map.set(model.id, model);
  }
  return map;
};

export function flattenCatalogTreeLeaves(
  tree: CatalogTree,
  models?: CatalogTreeModelInput[]
): CatalogTreeModelInput[] {
  const byId = models ? modelById(models) : null;
  const out: CatalogTreeModelInput[] = [];

  const walk = (nodes: CatalogTreeNode[]) => {
    for (const node of nodes) {
      if (node.kind === 'leaf' && node.modelId) {
        const row = byId?.get(node.modelId);
        out.push(
          row ?? {
            id: node.modelId,
            name: node.label,
          }
        );
      } else if (node.children?.length) {
        walk(node.children);
      }
    }
  };

  walk(tree.roots);
  return out;
}

export function catalogTreeBreadcrumb(
  tree: CatalogTree,
  modelId: string
): string[] | null {
  const path: string[] = [];

  const walk = (nodes: CatalogTreeNode[], ancestors: string[]): boolean => {
    for (const node of nodes) {
      const next = node.kind === 'group' ? [...ancestors, node.label] : ancestors;
      if (node.kind === 'leaf' && node.modelId === modelId) {
        path.push(...ancestors, node.label);
        return true;
      }
      if (node.children?.length && walk(node.children, next)) {
        return true;
      }
    }
    return false;
  };

  return walk(tree.roots, []) ? path : null;
}

/** True when every root node is a selectable leaf (no grouping). */
export function catalogTreeIsFlat(tree: CatalogTree): boolean {
  return (
    tree.roots.length > 0 &&
    tree.roots.every((node) => node.kind === 'leaf' && !node.children?.length)
  );
}

export function formatCatalogTreeBreadcrumb(parts: string[]): string {
  return parts.filter((p) => p.length > 0).join(' › ');
}
