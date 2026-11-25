import { describe, expect, it } from "vitest";

import { LlmdClient } from "../src/llm.js";
import { CredentialSource, ProviderConnectionStatus } from "../src/gen/llmv1/llm_pb.js";

function harness(response: Record<string, unknown>) {
  const requests: Array<Record<string, unknown>> = [];
  const client = new LlmdClient({
    transport: {} as never,
    tenantId: "tenant_test",
    scopeId: "scope_test",
    sessionId: "sess_test",
  });
  (client as unknown as { client: unknown }).client = {
    checkProviderConnection: async (req: Record<string, unknown>) => {
      requests.push(req);
      return {
        status: ProviderConnectionStatus.OK,
        binary: "",
        version: "",
        message: "",
        details: "",
        needsAuth: false,
        error: undefined,
        credentialSource: CredentialSource.UNSPECIFIED,
        warnings: [],
        assumedRoleArn: "",
        callerIdentity: undefined,
        ...response,
      };
    },
  };
  return { client, requests };
}

describe("LlmdClient.checkProviderConnection", () => {
  it("sends the unsaved config through the oneof and maps diagnostics", async () => {
    const h = harness({
      credentialSource: CredentialSource.ASSUMED_ROLE,
      assumedRoleArn: "arn:aws:iam::123456789012:role/r",
      warnings: ["clock skew"],
      callerIdentity: { account: "123456789012", arn: "arn:x", userId: "AID" },
      status: ProviderConnectionStatus.FAILED,
      error: { kind: 1, providerType: 0, providerStatus: 403, retryable: false },
    });

    const res = await h.client.checkProviderConnection("bedrock", {
      config: { type: "bedrock", region: "us-east-1" },
    });

    const sent = h.requests[0].config as { case?: string; value?: { region?: string } };
    expect(sent.case).toBe("bedrock");
    expect(sent.value?.region).toBe("us-east-1");
    expect(res.status).toBe("failed");
    expect(res.credentialSource).toBe("assumed_role");
    expect(res.assumedRoleArn).toBe("arn:aws:iam::123456789012:role/r");
    expect(res.warnings).toEqual(["clock skew"]);
    expect(res.callerIdentity).toEqual({
      account: "123456789012",
      arn: "arn:x",
      userId: "AID",
    });
    expect(res.error?.providerStatus).toBe(403);
    expect(res.error?.retryAfterMs).toBeUndefined();
  });

  it("leaves the oneof unset when no config is given (CLI behavior)", async () => {
    const h = harness({ binary: "/usr/bin/claude", version: "1.0.0" });

    const res = await h.client.checkProviderConnection("claude_code_v2", { timeoutMs: 1 });

    expect((h.requests[0].config as { case?: string }).case).toBeUndefined();
    expect(h.requests[0].timeoutMs).toBe(1);
    expect(res.status).toBe("ok");
    expect(res.binary).toBe("/usr/bin/claude");
    expect(res.error).toBeUndefined();
    expect(res.callerIdentity).toBeUndefined();
    expect(res.warnings).toEqual([]);
  });

  it("maps every credentialSource value", async () => {
    const cases: Array<[CredentialSource, string]> = [
      [CredentialSource.UNSPECIFIED, "unspecified"],
      [CredentialSource.DEFAULT_CHAIN, "default_chain"],
      [CredentialSource.ASSUMED_ROLE, "assumed_role"],
      [CredentialSource.WEB_IDENTITY, "web_identity"],
      [CredentialSource.API_KEY, "api_key"],
      [CredentialSource.PROFILE, "profile"],
      [CredentialSource.BEARER_TOKEN, "bearer_token"],
      [CredentialSource.NONE, "none"],
    ];
    for (const [wire, expected] of cases) {
      const h = harness({ credentialSource: wire });
      const res = await h.client.checkProviderConnection("bedrock");
      expect(res.credentialSource).toBe(expected);
    }
  });
});
