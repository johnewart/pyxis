/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * One "just works" panel for creating *and* editing any llmd LLM provider
 * config. Covers every field on every `llm.v1.*LlmConfig` proto message
 * (anthropic/openai/gemini/openrouter/ollama/bedrock/cursor_cli/claude_code_v2) via
 * conditional per-type sub-panels, sharing one field-value type
 * (`RegisterProviderConfig` from `@pyxis-llm/client`) with `registerProvider` /
 * `getProviderConfig` / `updateProviderConfig` on both TS clients.
 *
 * Per project rule: this component is hand-maintained, not reflection-driven.
 * If you add a field to a provider's proto message, add it here too, in the
 * same change.
 */

import { useEffect, useMemo, useState, type ReactNode } from 'react';
import {
  Alert,
  Button,
  NumberInput,
  PasswordInput,
  Select,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
} from '@mantine/core';
import type {
  LlmdClient,
  CursorCliModeName,
  OpenRouterSortName,
  RegisterProviderConfig,
} from '@pyxis-llm/client';

/**
 * The form edits an `@pyxis-llm/client` provider config directly.
 *
 * This relies on `RegisterProviderConfig` being a discriminated union with ONE
 * literal `type` per provider kind (llmd's short_code). The `update<Extract<…,
 * { type: 'cursor_cli' }>>()` calls below and the exhaustive
 * `Record<ProviderConnectConfigType, …>` / `switch` both break if a member's
 * `type` is ever widened to a union of spellings — `Extract` collapses to
 * `never` and every field editor starts erroring. See the note on
 * `RegisterProviderConfig`.
 */
export type ProviderConnectConfig = RegisterProviderConfig;
export type ProviderConnectConfigType = ProviderConnectConfig['type'];

const PROVIDER_TYPE_LABELS: Record<ProviderConnectConfigType, string> = {
  anthropic: 'Anthropic',
  openai: 'OpenAI',
  gemini: 'Google Gemini',
  openrouter: 'OpenRouter',
  ollama: 'Ollama',
  bedrock: 'Amazon Bedrock',
  cursor_cli: 'Cursor CLI',
  claude_code_v2: 'Claude Code',
};

const ALL_PROVIDER_TYPES: ProviderConnectConfigType[] = [
  'anthropic',
  'openai',
  'gemini',
  'openrouter',
  'ollama',
  'bedrock',
  'cursor_cli',
  'claude_code_v2',
];

function requiresApiKey(type: ProviderConnectConfigType): boolean {
  return type === 'anthropic' || type === 'openai' || type === 'gemini' || type === 'openrouter';
}

/** API-backed provider types (everything except the CLI-backed ones). */
export function isApiProviderType(type: ProviderConnectConfigType): boolean {
  return type !== 'cursor_cli' && type !== 'claude_code_v2';
}

function emptyConfigForType(type: ProviderConnectConfigType): ProviderConnectConfig {
  switch (type) {
    case 'anthropic':
      return { type, apiKey: '' };
    case 'openai':
      return { type, apiKey: '' };
    case 'gemini':
      return { type, apiKey: '' };
    case 'openrouter':
      return { type, apiKey: '' };
    case 'ollama':
      return { type };
    case 'bedrock':
      return { type, region: '' };
    case 'cursor_cli':
      return { type };
    case 'claude_code_v2':
      return { type };
  }
}

function str(v: string | undefined): string {
  return v ?? '';
}

function trimmedOrUndefined(v: string): string | undefined {
  const t = v.trim();
  return t.length > 0 ? t : undefined;
}

function multilineToArray(v: string): string[] | undefined {
  const lines = v
    .split('\n')
    .map((l) => l.trim())
    .filter((l) => l.length > 0);
  return lines.length > 0 ? lines : undefined;
}

function arrayToMultiline(v: string[] | undefined): string {
  return (v ?? []).join('\n');
}

const OPENROUTER_SORT_OPTIONS: { value: OpenRouterSortName; label: string }[] = [
  { value: 'price', label: 'Price' },
  { value: 'throughput', label: 'Throughput' },
  { value: 'latency', label: 'Latency' },
];

const CURSOR_CLI_MODE_OPTIONS: { value: CursorCliModeName; label: string }[] = [
  { value: 'default', label: 'Default' },
  { value: 'ask', label: 'Ask' },
  { value: 'plan', label: 'Plan' },
];

const CLAUDE_PERMISSION_MODE_OPTIONS = [
  { value: 'default', label: 'Default' },
  { value: 'acceptEdits', label: 'Accept edits' },
  { value: 'bypassPermissions', label: 'Bypass permissions' },
  { value: 'dontAsk', label: "Don't ask" },
  { value: 'plan', label: 'Plan' },
];

const CLAUDE_EFFORT_OPTIONS = [
  { value: 'low', label: 'Low' },
  { value: 'medium', label: 'Medium' },
  { value: 'high', label: 'High' },
  { value: 'xhigh', label: 'X-high' },
  { value: 'max', label: 'Max' },
];

const CLAUDE_THINKING_TYPE_OPTIONS = [
  { value: 'enabled', label: 'Enabled' },
  { value: 'disabled', label: 'Disabled' },
  { value: 'adaptive', label: 'Adaptive' },
];

/**
 * Result of testing an unsaved config; the same shape `LlmdClient.checkProviderConnection`
 * returns, so a host can pass that result straight through.
 */
export type ProviderConnectionTestResult = Awaited<
  ReturnType<LlmdClient['checkProviderConnection']>
>;

/** Renders diagnostics from a connection test. Shows only server-reported fields, never the config. */
function ConnectionTestResult({ result }: { result: ProviderConnectionTestResult }) {
  const ok = result.status === 'ok';
  return (
    <Alert
      color={ok ? 'green' : 'red'}
      title={ok ? 'Connection OK' : `Connection failed (${result.status})`}
      data-testid="connection-test-result"
    >
      <Stack gap={2}>
        {result.message ? <Text size="sm">{result.message}</Text> : null}
        <Text size="sm">Credential source: {result.credentialSource}</Text>
        {result.assumedRoleArn ? (
          <Text size="sm">Assumed role: {result.assumedRoleArn}</Text>
        ) : null}
        {result.callerIdentity ? (
          <Text size="sm">
            Identity: {result.callerIdentity.arn} (account {result.callerIdentity.account}, user{' '}
            {result.callerIdentity.userId})
          </Text>
        ) : null}
        {result.warnings.map((w, i) => (
          <Text key={i} size="sm" c="yellow.8">
            Warning: {w}
          </Text>
        ))}
        {result.error ? (
          <Text size="sm">
            Error: kind {result.error.kind}
            {result.error.providerStatus ? `, status ${result.error.providerStatus}` : ''}
            {result.error.retryable ? ', retryable' : ''}
          </Text>
        ) : null}
      </Stack>
    </Alert>
  );
}

export type ProviderConnectFormProps = {
  /**
   * Enables the "Test connection" button. Called with the current, unsaved config; wire it to
   * `client.checkProviderConnection(config.type, { config })`. May make real model calls, so
   * it only runs on button click.
   */
  onTestConnection?: (config: ProviderConnectConfig) => Promise<ProviderConnectionTestResult>;
  /** 'create' registers a brand-new provider; 'edit' updates an existing one. Defaults to 'create'. */
  mode?: 'create' | 'edit';
  providerId: string;
  onProviderIdChange: (id: string) => void;
  /**
   * Called with the assembled config on submit. In 'create' mode wire this to
   * `registerProvider`; in 'edit' mode wire it to `updateProviderConfig`.
   */
  onSubmit: (config: ProviderConnectConfig) => Promise<void> | void;
  /**
   * 'edit' mode only: the config loaded via `getProviderConfig`, used to seed
   * the form's initial field values. Also fixes the provider type (can't
   * change a provider's type once created).
   */
  initialConfig?: ProviderConnectConfig;
  submitting?: boolean;
  error?: Error | null;
  /** Provider types to offer; defaults to all types this form supports. Ignored in 'edit' mode. */
  allowedTypes?: ProviderConnectConfigType[];
  /** Rendered after a successful submit, e.g. a "saved!" banner slot. */
  successMessage?: ReactNode;
  /**
   * @deprecated use {@link onSubmit}. Kept as an alias so existing 'create'-only
   * callers keep working; if both are provided, `onSubmit` wins.
   */
  onRegister?: (config: ProviderConnectConfig) => Promise<void> | void;
};

/**
 * A single config form covering every llmd provider type, for both creating
 * a new provider and editing an existing one's config.
 */
export function ProviderConnectForm({
  mode = 'create',
  providerId,
  onProviderIdChange,
  onSubmit,
  onRegister,
  onTestConnection,
  initialConfig,
  submitting = false,
  error = null,
  allowedTypes = ALL_PROVIDER_TYPES,
  successMessage,
}: ProviderConnectFormProps) {
  const isEdit = mode === 'edit';
  const submit = onSubmit ?? onRegister;

  const [type, setType] = useState<ProviderConnectConfigType>(
    initialConfig?.type ?? allowedTypes[0] ?? 'anthropic'
  );
  const [config, setConfig] = useState<ProviderConnectConfig>(
    initialConfig ?? emptyConfigForType(type)
  );
  const [submitted, setSubmitted] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<ProviderConnectionTestResult | null>(null);
  const [testError, setTestError] = useState<string | null>(null);

  // Re-seed when switching to a genuinely different initial config (e.g. host
  // finished loading getProviderConfig, or switched which provider is being edited).
  useEffect(() => {
    if (initialConfig) {
      setType(initialConfig.type);
      setConfig(initialConfig);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialConfig]);

  const typeOptions = useMemo(
    () => allowedTypes.map((t) => ({ value: t, label: PROVIDER_TYPE_LABELS[t] })),
    [allowedTypes]
  );

  const handleTypeChange = (next: ProviderConnectConfigType) => {
    setType(next);
    setConfig(emptyConfigForType(next));
    setTestResult(null);
    setTestError(null);
  };

  const handleTest = async () => {
    if (!onTestConnection) return;
    setTesting(true);
    setTestResult(null);
    setTestError(null);
    try {
      setTestResult(await onTestConnection(config));
    } catch (e) {
      // Message only: the config (which holds secrets) is never included.
      setTestError(e instanceof Error ? e.message : String(e));
    } finally {
      setTesting(false);
    }
  };

  const update = <T extends ProviderConnectConfig>(patch: Partial<T>) => {
    setConfig((prev) => ({ ...prev, ...patch }) as ProviderConnectConfig);
  };

  const canSubmit =
    (isEdit || providerId.trim().length > 0) &&
    (!requiresApiKey(type) ||
      (config.type === 'anthropic' ||
      config.type === 'openai' ||
      config.type === 'gemini' ||
      config.type === 'openrouter'
        ? config.apiKey.trim().length > 0
        : false)) &&
    (type !== 'bedrock' || (config.type === 'bedrock' && config.region.trim().length > 0)) &&
    !submitting;

  const handleSubmit = async () => {
    if (!canSubmit || !submit) return;
    setSubmitted(false);
    await submit(config);
    setSubmitted(true);
  };

  return (
    <Stack gap="sm" style={{ maxWidth: 480 }}>
      <TextInput
        label="Provider id"
        placeholder="e.g. my-anthropic"
        value={providerId}
        onChange={(e) => onProviderIdChange(e.currentTarget.value)}
        disabled={isEdit}
      />
      <Select
        label="Provider type"
        data={typeOptions}
        value={type}
        onChange={(v) => v && handleTypeChange(v as ProviderConnectConfigType)}
        allowDeselect={false}
        disabled={isEdit}
      />

      {config.type === 'anthropic' || config.type === 'openai' || config.type === 'gemini' ? (
        <ApiKeyShapedFields
          config={config as Extract<ProviderConnectConfig, { type: 'anthropic' | 'openai' | 'gemini' }>}
          onChange={update}
          maxTokensLabel="Max output tokens (optional)"
          maxTokensKey="maxOutputTokens"
        />
      ) : null}

      {config.type === 'openrouter' ? <OpenRouterFields config={config} onChange={update} /> : null}

      {config.type === 'ollama' ? (
        <>
          <TextInput
            label="Base URL (optional)"
            placeholder="http://127.0.0.1:11434"
            value={str(config.baseUrl)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'ollama' }>>({ baseUrl: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <TextInput
            label="Default model (optional)"
            value={str(config.defaultModel)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'ollama' }>>({ defaultModel: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <NumberInput
            label="Max tokens (optional)"
            value={config.maxTokens}
            onChange={(v) => update<Extract<ProviderConnectConfig, { type: 'ollama' }>>({ maxTokens: typeof v === 'number' ? v : undefined })}
          />
        </>
      ) : null}

      {config.type === 'bedrock' ? (
        <>
          <TextInput
            label="AWS region"
            placeholder="us-east-1"
            value={config.region}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'bedrock' }>>({ region: e.currentTarget.value })}
          />
          <TextInput
            label="AWS profile (optional)"
            value={str(config.profile)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'bedrock' }>>({ profile: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <TextInput
            label="Default model (optional)"
            value={str(config.defaultModel)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'bedrock' }>>({ defaultModel: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <TextInput
            label="Endpoint URL (optional)"
            value={str(config.endpointUrl)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'bedrock' }>>({ endpointUrl: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <TextInput
            label="Assume role ARN (optional)"
            placeholder="arn:aws:iam::123456789012:role/name"
            value={str(config.assumeRoleArn)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'bedrock' }>>({ assumeRoleArn: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <TextInput
            label="Role session name (optional)"
            description="Leave empty to let llmd use its default session name."
            value={str(config.roleSessionName)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'bedrock' }>>({ roleSessionName: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <TextInput
            type="password"
            autoComplete="off"
            label="External ID (optional)"
            description="Write-only; never shown after saving."
            value={str(config.externalId)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'bedrock' }>>({ externalId: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <NumberInput
            label="Max output tokens (optional)"
            value={config.maxOutputTokens}
            onChange={(v) => update<Extract<ProviderConnectConfig, { type: 'bedrock' }>>({ maxOutputTokens: typeof v === 'number' ? v : undefined })}
          />
        </>
      ) : null}

      {config.type === 'cursor_cli' ? (
        <>
          <TextInput
            label="CLI binary path (optional)"
            value={str(config.binary)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'cursor_cli' }>>({ binary: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <TextInput
            label="Working directory (optional)"
            value={str(config.cwd)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'cursor_cli' }>>({ cwd: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <TextInput
            label="Workspace (optional)"
            value={str(config.workspace)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'cursor_cli' }>>({ workspace: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <TextInput
            label="API key (optional)"
            type="password"
            value={str(config.apiKey)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'cursor_cli' }>>({ apiKey: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <TextInput
            label="Default model (optional)"
            value={str(config.defaultModel)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'cursor_cli' }>>({ defaultModel: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <Select
            label="CLI mode (optional)"
            placeholder="Default"
            clearable
            data={CURSOR_CLI_MODE_OPTIONS}
            value={config.cliMode ?? null}
            onChange={(v) => update<Extract<ProviderConnectConfig, { type: 'cursor_cli' }>>({ cliMode: (v as CursorCliModeName) ?? undefined })}
          />
          <TextInput
            label="Session id (optional)"
            value={str(config.sessionId)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'cursor_cli' }>>({ sessionId: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <NumberInput
            label="Max output tokens (optional)"
            value={config.maxOutputTokens}
            onChange={(v) => update<Extract<ProviderConnectConfig, { type: 'cursor_cli' }>>({ maxOutputTokens: typeof v === 'number' ? v : undefined })}
          />
          <Switch
            label="Trust workspace"
            checked={config.trust ?? false}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'cursor_cli' }>>({ trust: e.currentTarget.checked })}
          />
          <Switch
            label="Auto-approve MCP servers"
            checked={config.approveMcps ?? false}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'cursor_cli' }>>({ approveMcps: e.currentTarget.checked })}
          />
          <Switch
            label="Force"
            checked={config.force ?? false}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'cursor_cli' }>>({ force: e.currentTarget.checked })}
          />
        </>
      ) : null}

      {config.type === 'claude_code_v2' ? (
        <>
          <TextInput
            label="CLI binary path (optional)"
            value={str(config.binary)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ binary: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <TextInput
            label="Working directory (optional)"
            value={str(config.cwd)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ cwd: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <TextInput
            label="Agent (optional)"
            value={str(config.agent)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ agent: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <TextInput
            label="Default model (optional)"
            value={str(config.defaultModel)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ defaultModel: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <Textarea
            label="System prompt (optional)"
            autosize
            minRows={2}
            value={str(config.systemPrompt)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ systemPrompt: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <Textarea
            label="Append to system prompt (optional)"
            autosize
            minRows={2}
            value={str(config.appendSystemPrompt)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ appendSystemPrompt: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <Select
            label="Effort (optional)"
            placeholder="Default"
            clearable
            data={CLAUDE_EFFORT_OPTIONS}
            value={config.effort ?? null}
            onChange={(v) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ effort: v ?? undefined })}
          />
          <Select
            label="Permission mode (optional)"
            placeholder="Default"
            clearable
            data={CLAUDE_PERMISSION_MODE_OPTIONS}
            value={config.permissionMode ?? null}
            onChange={(v) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ permissionMode: v ?? undefined })}
          />
          <NumberInput
            label="Max budget (USD, optional)"
            value={config.maxBudgetUsd}
            onChange={(v) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ maxBudgetUsd: typeof v === 'number' ? v : undefined })}
          />
          <NumberInput
            label="Max turns (optional)"
            value={config.maxTurns}
            onChange={(v) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ maxTurns: typeof v === 'number' ? v : undefined })}
          />
          <NumberInput
            label="Timeout (ms, optional)"
            value={config.timeoutMs}
            onChange={(v) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ timeoutMs: typeof v === 'number' ? v : undefined })}
          />
          <Textarea
            label="Additional directories (optional, one per line)"
            autosize
            minRows={2}
            value={arrayToMultiline(config.addDir)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ addDir: multilineToArray(e.currentTarget.value) })}
          />
          <Textarea
            label="Allowed tools (optional, one per line)"
            autosize
            minRows={2}
            value={arrayToMultiline(config.allowedTools)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ allowedTools: multilineToArray(e.currentTarget.value) })}
          />
          <Textarea
            label="Disallowed tools (optional, one per line)"
            autosize
            minRows={2}
            value={arrayToMultiline(config.disallowedTools)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ disallowedTools: multilineToArray(e.currentTarget.value) })}
          />
          <TextInput
            label="MCP config path (optional)"
            value={str(config.mcpConfigPath)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ mcpConfigPath: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <Textarea
            label="Setting sources (optional, one per line: user, project, local)"
            autosize
            minRows={2}
            value={arrayToMultiline(config.settingSources)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ settingSources: multilineToArray(e.currentTarget.value) })}
          />
          <Select
            label="Thinking (optional)"
            placeholder="Default"
            clearable
            data={CLAUDE_THINKING_TYPE_OPTIONS}
            value={config.thinkingType ?? null}
            onChange={(v) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ thinkingType: v ?? undefined })}
          />
          <NumberInput
            label="Thinking budget (tokens, optional)"
            value={config.thinkingBudgetTokens}
            onChange={(v) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ thinkingBudgetTokens: typeof v === 'number' ? v : undefined })}
          />
          <Textarea
            label="Hooks (JSON, optional)"
            autosize
            minRows={3}
            value={str(config.hooksJson)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ hooksJson: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <TextInput
            label="Custom config directory (optional)"
            value={str(config.configDir)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ configDir: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <PasswordInput
            label="OAuth token (optional)"
            description="From `claude setup-token`. Used instead of the login stored in the config directory."
            value={str(config.oauthToken)}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ oauthToken: trimmedOrUndefined(e.currentTarget.value) })}
          />
          <Switch
            label="Strict MCP config"
            checked={config.strictMcpConfig ?? false}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ strictMcpConfig: e.currentTarget.checked })}
          />
          <Switch
            label="Include hook events"
            checked={config.includeHookEvents ?? false}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ includeHookEvents: e.currentTarget.checked })}
          />
          <Switch
            label="Use my Claude config"
            checked={config.useMyClaudeConfig ?? false}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ useMyClaudeConfig: e.currentTarget.checked })}
          />
          <Switch
            label="Defer tool schemas"
            checked={config.deferToolSchemas ?? false}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ deferToolSchemas: e.currentTarget.checked })}
          />
          <Switch
            label="Use Claude Code preset"
            checked={config.useClaudeCodePreset ?? false}
            onChange={(e) => update<Extract<ProviderConnectConfig, { type: 'claude_code_v2' }>>({ useClaudeCodePreset: e.currentTarget.checked })}
          />
        </>
      ) : null}

      {error ? (
        <Alert color="red" title={isEdit ? "Couldn't save provider config" : "Couldn't connect provider"}>
          {error.message}
        </Alert>
      ) : null}
      {onTestConnection && isApiProviderType(config.type) ? (
        <Button variant="default" onClick={() => void handleTest()} loading={testing}>
          Test connection
        </Button>
      ) : null}
      {testResult ? <ConnectionTestResult result={testResult} /> : null}
      {testError ? (
        <Alert color="red" title="Couldn't test connection">
          {testError}
        </Alert>
      ) : null}
      {submitted && !error && successMessage ? successMessage : null}
      <Button onClick={() => void handleSubmit()} loading={submitting} disabled={!canSubmit}>
        {isEdit ? 'Save' : 'Connect'}
      </Button>
    </Stack>
  );
}

function OpenRouterFields({
  config,
  onChange,
}: {
  config: Extract<ProviderConnectConfig, { type: 'openrouter' }>;
  onChange: <T extends ProviderConnectConfig>(patch: Partial<T>) => void;
}) {
  type Cfg = Extract<ProviderConnectConfig, { type: 'openrouter' }>;
  const opts = config.defaultCallOptions ?? {};
  const setOpts = (patch: Partial<NonNullable<Cfg['defaultCallOptions']>>) => {
    const next = { ...opts, ...patch };
    const empty =
      next.sort === undefined &&
      next.order === undefined &&
      next.only === undefined &&
      next.ignore === undefined &&
      next.allowFallbacks === undefined;
    onChange<Cfg>({ defaultCallOptions: empty ? undefined : next });
  };
  return (
    <>
      <PasswordInput
        label="API key"
        description="Shown redacted when editing; only sent if you change it."
        value={config.apiKey}
        onChange={(e) => onChange<Cfg>({ apiKey: e.currentTarget.value })}
      />
      <TextInput
        label="Base URL (optional)"
        value={str(config.baseUrl)}
        onChange={(e) => onChange<Cfg>({ baseUrl: trimmedOrUndefined(e.currentTarget.value) })}
      />
      <TextInput
        label="Default model (optional)"
        value={str(config.defaultModel)}
        onChange={(e) => onChange<Cfg>({ defaultModel: trimmedOrUndefined(e.currentTarget.value) })}
      />
      <Select
        label="Routing sort (optional)"
        placeholder="Default"
        clearable
        data={OPENROUTER_SORT_OPTIONS}
        value={opts.sort ?? null}
        onChange={(v) => setOpts({ sort: (v as OpenRouterSortName | null) ?? undefined })}
      />
      <Textarea
        label="Preferred upstream providers (optional, one per line, in order)"
        autosize
        minRows={2}
        value={arrayToMultiline(opts.order)}
        onChange={(e) => setOpts({ order: multilineToArray(e.currentTarget.value) })}
      />
      <Textarea
        label="Only these upstream providers (optional, one per line)"
        autosize
        minRows={2}
        value={arrayToMultiline(opts.only)}
        onChange={(e) => setOpts({ only: multilineToArray(e.currentTarget.value) })}
      />
      <Textarea
        label="Ignore upstream providers (optional, one per line)"
        autosize
        minRows={2}
        value={arrayToMultiline(opts.ignore)}
        onChange={(e) => setOpts({ ignore: multilineToArray(e.currentTarget.value) })}
      />
      <Switch
        label="Allow fallbacks to other upstream providers"
        checked={opts.allowFallbacks ?? true}
        onChange={(e) => setOpts({ allowFallbacks: e.currentTarget.checked })}
      />
      <Switch
        label="Avoid unhealthy upstream endpoints"
        checked={config.avoidUnhealthyEndpoints ?? true}
        onChange={(e) => onChange<Cfg>({ avoidUnhealthyEndpoints: e.currentTarget.checked })}
      />
      <TextInput
        label="Endpoint health cache TTL in seconds (optional, default 60)"
        type="number"
        min={1}
        value={config.endpointHealthTtlSeconds ?? ''}
        onChange={(e) => {
          const n = Number.parseInt(e.currentTarget.value, 10);
          onChange<Cfg>({ endpointHealthTtlSeconds: Number.isFinite(n) && n > 0 ? n : undefined });
        }}
      />
    </>
  );
}

function ApiKeyShapedFields({
  config,
  onChange,
  maxTokensLabel,
}: {
  config: Extract<ProviderConnectConfig, { type: 'anthropic' | 'openai' | 'gemini' }>;
  onChange: <T extends ProviderConnectConfig>(patch: Partial<T>) => void;
  maxTokensLabel: string;
  maxTokensKey: 'maxOutputTokens';
}) {
  type Cfg = Extract<ProviderConnectConfig, { type: 'anthropic' | 'openai' | 'gemini' }>;
  const maxTokens = config.type === 'openai' ? config.maxTokens : config.maxOutputTokens;
  return (
    <>
      <TextInput
        label="API key"
        type="password"
        value={config.apiKey}
        onChange={(e) => onChange<Cfg>({ apiKey: e.currentTarget.value } as Partial<Cfg>)}
      />
      <TextInput
        label="Base URL (optional)"
        value={str(config.baseUrl)}
        onChange={(e) => onChange<Cfg>({ baseUrl: trimmedOrUndefined(e.currentTarget.value) } as Partial<Cfg>)}
      />
      <TextInput
        label="Default model (optional)"
        value={str(config.defaultModel)}
        onChange={(e) => onChange<Cfg>({ defaultModel: trimmedOrUndefined(e.currentTarget.value) } as Partial<Cfg>)}
      />
      <NumberInput
        label={config.type === 'openai' ? 'Max tokens (optional)' : maxTokensLabel}
        value={maxTokens}
        onChange={(v) =>
          onChange<Cfg>(
            (config.type === 'openai'
              ? { maxTokens: typeof v === 'number' ? v : undefined }
              : { maxOutputTokens: typeof v === 'number' ? v : undefined }) as Partial<Cfg>
          )
        }
      />
    </>
  );
}
