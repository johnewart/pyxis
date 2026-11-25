/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import type { CatalogTreeNode } from './modelCatalogTree.js';
import type { ChatModelOption } from './chatModelOptions.js';

export function optionLookupKey(
  providerId: string,
  modelId: string,
  multiProvider: boolean
): string {
  return multiProvider ? `${providerId}:${modelId}` : modelId;
}

/** Drop leaf nodes with no matching picker option; prune empty groups. */
export function filterSelectableTreeNodes(
  nodes: CatalogTreeNode[],
  resolveOption: (modelId: string) => ChatModelOption | undefined
): CatalogTreeNode[] {
  const out: CatalogTreeNode[] = [];
  for (const node of nodes) {
    if (node.kind === 'leaf') {
      if (node.modelId && resolveOption(node.modelId)) {
        out.push(node);
      }
      continue;
    }
    if (!node.children?.length) continue;
    const children = filterSelectableTreeNodes(node.children, resolveOption);
    if (children.length === 0) continue;
    out.push({ ...node, children });
  }
  return out;
}

export function makeResolveOption(
  providerId: string,
  optionByModelId: Map<string, ChatModelOption>,
  multiProvider: boolean
): (modelId: string) => ChatModelOption | undefined {
  return (modelId: string) => {
    const key = optionLookupKey(providerId, modelId, multiProvider);
    return optionByModelId.get(key) ?? optionByModelId.get(modelId);
  };
}

/** When multiple providers share one browser, filter each provider subtree with its own id. */
export function filterMultiProviderRoots(
  roots: CatalogTreeNode[],
  optionByModelId: Map<string, ChatModelOption>,
  multiProvider: boolean
): CatalogTreeNode[] {
  const out: CatalogTreeNode[] = [];
  for (const root of roots) {
    if (root.kind !== 'group') {
      out.push(root);
      continue;
    }
    const resolveOption = makeResolveOption(
      root.id,
      optionByModelId,
      multiProvider
    );
    const children = filterSelectableTreeNodes(root.children ?? [], resolveOption);
    if (children.length === 0) continue;
    out.push({ ...root, children });
  }
  return out;
}
