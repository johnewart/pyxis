import { useState } from 'react';
import { ProviderConnectForm, type ProviderConnectConfig } from './ProviderConnectForm.js';

export type SettingsScreenProps = {
  /** Any llmd client exposing `registerProvider` (e.g. from `createLlmdClient`). */
  client: {
    registerProvider: (providerId: string, config: ProviderConnectConfig) => Promise<void>;
  };
  onRegistered?: (providerId: string) => void;
};

export function SettingsScreen({ client, onRegistered }: SettingsScreenProps) {
  const [providerId, setProviderId] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [saved, setSaved] = useState(false);

  const handleSubmit = async (config: ProviderConnectConfig) => {
    setSubmitting(true);
    setError(null);
    setSaved(false);
    try {
      await client.registerProvider(providerId, config);
      setSaved(true);
      onRegistered?.(providerId);
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div>
      <h2>Settings</h2>
      <ProviderConnectForm
        providerId={providerId}
        onProviderIdChange={setProviderId}
        onSubmit={handleSubmit}
        submitting={submitting}
        error={error}
        successMessage={saved ? <span>Provider registered.</span> : undefined}
      />
    </div>
  );
}
