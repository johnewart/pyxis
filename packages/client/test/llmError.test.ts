import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { describe, expect, it } from "vitest";
import { getLlmError, llmv1 } from "../src/index.js";

describe("getLlmError", () => {
  it("reads back the LlmError detail", () => {
    const detail = create(llmv1.LlmErrorSchema, {
      kind: llmv1.LlmErrorKind.RATE_LIMITED,
      providerType: llmv1.LlmProviderType.ANTHROPIC,
      providerStatus: 429,
      retryable: true,
      retryAfter: { seconds: 7n, nanos: 0 },
    });
    const err = new ConnectError("slow down", Code.ResourceExhausted, undefined, [
      { desc: llmv1.LlmErrorSchema, value: detail },
    ]);
    const got = getLlmError(err);
    expect(got?.kind).toBe(llmv1.LlmErrorKind.RATE_LIMITED);
    expect(got?.providerType).toBe(llmv1.LlmProviderType.ANTHROPIC);
    expect(got?.providerStatus).toBe(429);
    expect(got?.retryable).toBe(true);
    expect(got?.retryAfterMs).toBe(7000);
  });

  it("converts nanos to milliseconds", () => {
    const detail = create(llmv1.LlmErrorSchema, {
      retryAfter: { seconds: 1n, nanos: 500_000_000 },
    });
    const err = new ConnectError("x", Code.ResourceExhausted, undefined, [
      { desc: llmv1.LlmErrorSchema, value: detail },
    ]);
    expect(getLlmError(err)?.retryAfterMs).toBe(1500);
  });

  it("leaves retryAfterMs undefined when not sent", () => {
    const detail = create(llmv1.LlmErrorSchema, {});
    const err = new ConnectError("x", Code.Internal, undefined, [
      { desc: llmv1.LlmErrorSchema, value: detail },
    ]);
    expect(getLlmError(err)?.retryAfterMs).toBeUndefined();
  });

  it("returns undefined without detail", () => {
    expect(getLlmError(new ConnectError("x", Code.Internal))).toBeUndefined();
    expect(getLlmError(new Error("x"))).toBeUndefined();
  });
});
