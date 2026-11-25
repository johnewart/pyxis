/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/** Unit separator — not used in UUIDs or typical vendor model ids (unlike `:`). */
const MODEL_OPTION_VALUE_SEP = '';

export type ChatModelOption = {
  providerId: string;
  providerName: string;
  modelId: string;
  modelName: string;
};

export function encodeChatModelOptionValue(opt: ChatModelOption): string {
  return `${opt.providerId}${MODEL_OPTION_VALUE_SEP}${opt.modelId}`;
}

export function decodeChatModelOptionValue(
  value: string
): { providerId: string; modelId: string } | null {
  const i = value.indexOf(MODEL_OPTION_VALUE_SEP);
  if (i <= 0 || i >= value.length - 1) return null;
  return {
    providerId: value.slice(0, i),
    modelId: value.slice(i + MODEL_OPTION_VALUE_SEP.length),
  };
}

type ProviderRow = {
  id: string;
  name: string;
  defaultModel?: string;
  models?: { id: string; name: string }[];
  useDefaultModelOnly?: boolean;
};

/**
 * Flat list of picker rows for configured providers only, deduped by provider+model
 * and skipping empty ids (avoids Mantine Combobox duplicate-value errors and malformed data).
 */
export function buildConfiguredModelOptions(
  configuredProviders: ProviderRow[]
): ChatModelOption[] {
  const list: ChatModelOption[] = [];
  const seen = new Set<string>();
  for (const p of configuredProviders) {
    const models = p.models?.length
      ? p.models
      : p.defaultModel?.trim()
        ? [{ id: p.defaultModel.trim(), name: p.defaultModel.trim() }]
        : [];
    // A provider reporting no models still gets one selectable row, but its
    // model id must never be the PROVIDER id: the picker's choice is typically
    // persisted downstream, and a provider id fails model lookups there.
    // `default` is the sentinel meaning "whatever this provider is set to".
    const modelList =
      models.length > 0
        ? models
        : p.useDefaultModelOnly
          ? [{ id: 'default', name: 'Default' }]
          : [{ id: 'default', name: p.name }];
    for (const m of modelList) {
      const pid = (p.id ?? '').trim();
      const mid = (m.id ?? '').trim();
      if (!pid || !mid) continue;
      const key = `${pid}\0${mid}`;
      if (seen.has(key)) continue;
      seen.add(key);
      list.push({
        providerId: pid,
        providerName: (p.name ?? '').trim() || pid,
        modelId: mid,
        modelName: (m.name ?? '').trim() || mid,
      });
    }
  }
  return list;
}
