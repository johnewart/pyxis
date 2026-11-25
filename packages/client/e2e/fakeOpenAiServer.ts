/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Minimal, scriptable fake of OpenAI's `/v1/chat/completions` endpoint
 * (streaming SSE + non-streaming), standing in for a real provider so the
 * e2e suite exercises llmd's real `openai` backend code path
 * (`internal/llm/openai`) without real API calls or nondeterminism. This is
 * exactly how a host would register a real OpenAI-compatible endpoint —
 * `RegisterLlmProvider(type=openai, base_url=...)` — just pointed at a
 * server we control.
 */

import http from 'node:http';
import type { AddressInfo } from 'node:net';

/** One turn's scripted behavior. Consumed in order, one per incoming request. */
export type FakeOpenAiTurn =
  | { kind: 'text'; deltas: string[] }
  | {
      kind: 'tool_call';
      id: string;
      name: string;
      argumentsJson: string;
      /**
       * Hold the response this long before emitting the tool call (default 0).
       * For tests that need the tool to run at a controlled moment — e.g. while
       * the client is disconnected, or only after it has reconnected.
       */
      delayMs?: number;
    }
  /** Holds the connection open and never responds, until the server is closed. For cancellation tests. */
  | { kind: 'hang' }
  /**
   * Like `text`, but paced with a delay between each delta (default 150ms) —
   * for tests that need to observe/act on PARTIAL streamed output (mid-stream
   * cancellation, interrupting with a new message) with a reliable window to
   * do so, instead of racing a stream that completes near-instantly.
   */
  | { kind: 'slow_text'; deltas: string[]; delayMs?: number };

export type FakeOpenAiRequestRecord = {
  body: any;
  headers: http.IncomingHttpHeaders;
};

export type FakeOpenAiServer = {
  /** `http://127.0.0.1:PORT` (no trailing slash / no `/v1`) — pass as the provider's base_url. */
  readonly url: string;
  /** Every request received so far, in order (for asserting tool visibility etc.). */
  readonly requests: FakeOpenAiRequestRecord[];
  /** Push more turns onto the queue (consumed FIFO, one per request). */
  enqueue(...turns: FakeOpenAiTurn[]): void;
  close(): Promise<void>;
};

function sseChunk(res: http.ServerResponse, payload: unknown): void {
  res.write(`data: ${JSON.stringify(payload)}\n\n`);
}

function streamText(res: http.ServerResponse, model: string, deltas: string[]): void {
  const id = `chatcmpl-fake-${Date.now()}`;
  for (const delta of deltas) {
    sseChunk(res, {
      id,
      object: 'chat.completion.chunk',
      created: Math.floor(Date.now() / 1000),
      model,
      choices: [{ index: 0, delta: { content: delta }, finish_reason: null }],
    });
  }
  sseChunk(res, {
    id,
    object: 'chat.completion.chunk',
    created: Math.floor(Date.now() / 1000),
    model,
    choices: [{ index: 0, delta: {}, finish_reason: 'stop' }],
  });
  res.write('data: [DONE]\n\n');
  res.end();
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/**
 * Like `streamText`, but writes one delta at a time with a delay between
 * them, and stops early (leaving the connection open, never finishing) if
 * the client disconnects mid-stream (checked via `res.writableEnded` /
 * `res.destroyed`) — mirrors how a real upstream provider's stream reacts
 * to the downstream client (llmd) going away on cancellation.
 */
async function streamSlowText(
  res: http.ServerResponse,
  model: string,
  deltas: string[],
  delayMs: number
): Promise<void> {
  const id = `chatcmpl-fake-${Date.now()}`;
  for (const delta of deltas) {
    if (res.destroyed || res.writableEnded) return;
    sseChunk(res, {
      id,
      object: 'chat.completion.chunk',
      created: Math.floor(Date.now() / 1000),
      model,
      choices: [{ index: 0, delta: { content: delta }, finish_reason: null }],
    });
    await sleep(delayMs);
  }
  if (res.destroyed || res.writableEnded) return;
  sseChunk(res, {
    id,
    object: 'chat.completion.chunk',
    created: Math.floor(Date.now() / 1000),
    model,
    choices: [{ index: 0, delta: {}, finish_reason: 'stop' }],
  });
  res.write('data: [DONE]\n\n');
  res.end();
}

/**
 * Non-streaming (`stream: false` or omitted) response: a single JSON
 * `chat.completion` object built from the scripted turn, instead of SSE
 * chunks. Real OpenAI never streams when `stream` isn't truthy, so the fake
 * must not either.
 */
function respondNonStreaming(
  res: http.ServerResponse,
  model: string,
  turn: Extract<FakeOpenAiTurn, { kind: 'text' | 'slow_text' | 'tool_call' }>
): void {
  const id = `chatcmpl-fake-${Date.now()}`;
  const created = Math.floor(Date.now() / 1000);

  const message =
    turn.kind === 'tool_call'
      ? {
          role: 'assistant',
          content: null,
          tool_calls: [
            {
              id: turn.id,
              type: 'function',
              function: { name: turn.name, arguments: turn.argumentsJson },
            },
          ],
        }
      : { role: 'assistant', content: turn.deltas.join('') };

  const finishReason = turn.kind === 'tool_call' ? 'tool_calls' : 'stop';

  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end(
    JSON.stringify({
      id,
      object: 'chat.completion',
      created,
      model,
      choices: [{ index: 0, message, finish_reason: finishReason }],
    })
  );
}

function streamToolCall(
  res: http.ServerResponse,
  model: string,
  toolCallId: string,
  name: string,
  argumentsJson: string
): void {
  const id = `chatcmpl-fake-${Date.now()}`;
  sseChunk(res, {
    id,
    object: 'chat.completion.chunk',
    created: Math.floor(Date.now() / 1000),
    model,
    choices: [
      {
        index: 0,
        delta: {
          tool_calls: [
            {
              index: 0,
              id: toolCallId,
              type: 'function',
              function: { name, arguments: argumentsJson },
            },
          ],
        },
        finish_reason: null,
      },
    ],
  });
  sseChunk(res, {
    id,
    object: 'chat.completion.chunk',
    created: Math.floor(Date.now() / 1000),
    model,
    choices: [{ index: 0, delta: {}, finish_reason: 'tool_calls' }],
  });
  res.write('data: [DONE]\n\n');
  res.end();
}

/** Start a fake OpenAI-compatible server on an ephemeral loopback port. */
export function startFakeOpenAiServer(): Promise<FakeOpenAiServer> {
  const queue: FakeOpenAiTurn[] = [];
  const requests: FakeOpenAiRequestRecord[] = [];

  const server = http.createServer((req, res) => {
    if (req.method !== 'POST' || !req.url?.startsWith('/chat/completions')) {
      res.writeHead(404).end();
      return;
    }
    const chunks: Buffer[] = [];
    req.on('data', (c) => chunks.push(c));
    req.on('end', () => {
      let body: any = {};
      try {
        body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      } catch {
        // leave body = {}
      }
      requests.push({ body, headers: req.headers });

      const turn = queue.shift();
      const model = typeof body?.model === 'string' ? body.model : 'fake-model';

      if (!turn) {
        res.writeHead(500, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: { message: 'fake-openai-server: no scripted turn queued' } }));
        return;
      }

      if (turn.kind === 'hang') {
        // Deliberately never respond; the test drives cancellation and closes
        // the server itself in teardown.
        return;
      }

      // OpenAI only streams SSE when `stream: true` is explicitly set; any
      // other value (false / omitted) gets a single JSON completion object.
      if (body?.stream !== true) {
        respondNonStreaming(res, model, turn);
        return;
      }

      res.writeHead(200, {
        'Content-Type': 'text/event-stream',
        'Cache-Control': 'no-cache',
        Connection: 'keep-alive',
      });

      if (turn.kind === 'text') {
        streamText(res, model, turn.deltas);
      } else if (turn.kind === 'slow_text') {
        void streamSlowText(res, model, turn.deltas, turn.delayMs ?? 150);
      } else if (turn.delayMs) {
        setTimeout(() => streamToolCall(res, model, turn.id, turn.name, turn.argumentsJson), turn.delayMs);
      } else {
        streamToolCall(res, model, turn.id, turn.name, turn.argumentsJson);
      }
    });
  });

  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address() as AddressInfo;
      resolve({
        url: `http://127.0.0.1:${addr.port}`,
        requests,
        enqueue: (...turns: FakeOpenAiTurn[]) => {
          queue.push(...turns);
        },
        close: () =>
          new Promise<void>((res) => {
            server.close(() => res());
            server.closeAllConnections?.();
          }),
      });
    });
  });
}
