/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * Minimal terminal UI for an interactive provider auth command (e.g.
 * `claude login`) driven server-side over gRPC — see useProviderAuthCommand
 * (./node.js). Deliberately simple (a scrollback <pre> + a line input, not a
 * full ANSI/cursor-aware terminal): most auth flows only print a URL and
 * wait for confirmation, so a full xterm.js integration is unnecessary for
 * the common case. Hosts that need real terminal fidelity (cursor movement,
 * color, in-place redraws) can build their own xterm.js view against the
 * same `write`/`resize` primitives useProviderAuthCommand exposes — this
 * component is a slot-free reference implementation, not the only option.
 */

import { useEffect, useRef, useState } from 'react';
import { Box, Button, Group, Loader, Text, TextInput } from '@mantine/core';

export type ProviderAuthTerminalProps = {
  output: string;
  running: boolean;
  exitCode: number | null;
  error?: Error | null;
  onSendLine: (line: string) => void;
  onCancel?: () => void;
  /** Fixed height for the scrollback pane. */
  height?: number;
};

export function ProviderAuthTerminal({
  output,
  running,
  exitCode,
  error,
  onSendLine,
  onCancel,
  height = 320,
}: ProviderAuthTerminalProps) {
  const [line, setLine] = useState('');
  const scrollRef = useRef<HTMLPreElement>(null);

  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [output]);

  const submitLine = () => {
    if (!running) return;
    onSendLine(line + '\n');
    setLine('');
  };

  return (
    <Box>
      <Box
        component="pre"
        ref={scrollRef}
        style={{
          height,
          overflowY: 'auto',
          overflowX: 'hidden',
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
          background: 'var(--mantine-color-dark-8)',
          color: 'var(--mantine-color-gray-2)',
          padding: 8,
          borderRadius: 4,
          fontSize: 'var(--mantine-font-size-xs)',
          margin: 0,
        }}
      >
        {output || (running ? '' : 'Not started.')}
      </Box>
      <Group gap="xs" mt="xs" align="center">
        {running ? <Loader size="xs" /> : null}
        <TextInput
          flex={1}
          size="xs"
          placeholder="Type a response and press Enter…"
          disabled={!running}
          value={line}
          onChange={(e) => setLine(e.currentTarget.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              submitLine();
            }
          }}
        />
        {running && onCancel ? (
          <Button size="xs" color="red" variant="light" onClick={onCancel}>
            Cancel
          </Button>
        ) : null}
      </Group>
      {!running && exitCode !== null ? (
        <Text size="xs" c={exitCode === 0 ? 'dimmed' : 'red'} mt={4}>
          Exited with code {exitCode}.
        </Text>
      ) : null}
      {error ? (
        <Text size="xs" c="red" mt={4}>
          {error.message}
        </Text>
      ) : null}
    </Box>
  );
}
