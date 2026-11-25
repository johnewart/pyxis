/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * Node-capable hook driving llmd's RunProviderAuthCommand RPC (see
 * ./node.js). Runs an interactive CLI auth command (e.g. `claude login`) in
 * a server-side PTY, streamed over gRPC via @pyxis-llm/client's
 * ProviderAuthCommandSession — useful when the CLI is only reachable on the
 * machine running llmd (a remote/server deployment), not the operator's own
 * machine. Lives here (not the main entry) for the same reason
 * useLlmdChatDriver does: @pyxis-llm/client pulls in @grpc/grpc-js.
 *
 * Pairs with ProviderAuthTerminal (in the main entry) for a minimal
 * terminal UI; hosts wanting full xterm.js fidelity (cursor movement, ANSI
 * color, resize-aware reflow) can wire their own xterm.js instance to
 * `write`/`resize` here instead — this hook's job is state management, not
 * terminal rendering.
 */

import { useCallback, useRef, useState } from 'react';
import type {
  LlmdClient,
  LlmProviderType,
  ProviderAuthCommandSession,
  ProviderAuthCommandStartOptions,
} from '@pyxis-llm/client';

export type ProviderAuthCommandState = {
  /** Accumulated PTY output, decoded as UTF-8 text (best-effort across chunk boundaries). */
  output: string;
  running: boolean;
  exitCode: number | null;
  error: Error | null;
  start: (providerType: LlmProviderType, options?: ProviderAuthCommandStartOptions) => void;
  /** Sends keystrokes to the running command's stdin. */
  write: (data: string) => void;
  resize: (cols: number, rows: number) => void;
  /** Closes the client's send side; the command keeps streaming output until it exits. */
  end: () => void;
  /** Aborts immediately, tearing down the underlying gRPC call. */
  cancel: () => void;
  /** Clears output/exitCode/error without starting a new run. */
  reset: () => void;
};

/**
 * Drives an interactive provider auth command (e.g. `claude login`) via
 * `client.runProviderAuthCommand`. `client` may be `null`/`undefined` while
 * a host is still setting up its connection — `start` is a no-op until it's
 * provided.
 */
export function useProviderAuthCommand(
  client: LlmdClient | null | undefined
): ProviderAuthCommandState {
  const [output, setOutput] = useState('');
  const [running, setRunning] = useState(false);
  const [exitCode, setExitCode] = useState<number | null>(null);
  const [error, setError] = useState<Error | null>(null);

  const sessionRef = useRef<ProviderAuthCommandSession | null>(null);
  const decoderRef = useRef<TextDecoder>(new TextDecoder());

  const reset = useCallback(() => {
    setOutput('');
    setExitCode(null);
    setError(null);
  }, []);

  const start = useCallback(
    (providerType: LlmProviderType, options?: ProviderAuthCommandStartOptions) => {
      if (!client || running) return;
      reset();
      setRunning(true);
      decoderRef.current = new TextDecoder();

      const session = client.runProviderAuthCommand(providerType, options);
      sessionRef.current = session;

      void (async () => {
        try {
          for await (const ev of session) {
            if (ev.kind === 'output') {
              const text = decoderRef.current.decode(ev.data, { stream: true });
              setOutput((prev) => prev + text);
            } else if (ev.kind === 'exit') {
              setExitCode(ev.code);
            }
          }
        } catch (err) {
          setError(err instanceof Error ? err : new Error(String(err)));
        } finally {
          setRunning(false);
          sessionRef.current = null;
        }
      })();
    },
    [client, running, reset]
  );

  const write = useCallback((data: string) => {
    sessionRef.current?.write(data);
  }, []);

  const resize = useCallback((cols: number, rows: number) => {
    sessionRef.current?.resize(cols, rows);
  }, []);

  const end = useCallback(() => {
    sessionRef.current?.end();
  }, []);

  const cancel = useCallback(() => {
    sessionRef.current?.cancel();
  }, []);

  return { output, running, exitCode, error, start, write, resize, end, cancel, reset };
}
