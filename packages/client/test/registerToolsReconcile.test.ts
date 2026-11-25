import { describe, expect, it } from "vitest";

import { LlmdClient } from "../src/llm.js";

/**
 * registerTools runs before every turn. It must only ever remove tools THIS
 * client registered — never tools it merely observed on the session, which
 * belong to the agent profile / server builtins.
 */
function harness() {
  const unregistered: string[] = [];
  const registered: string[] = [];
  let listCalls = 0;

  const client = new LlmdClient({
    transport: {} as never,
    tenantId: "tenant_test",
    scopeId: "scope_test",
    sessionId: "sess_test",
  });

  (client as unknown as { client: unknown }).client = {
    listSessionTools: async () => {
      listCalls += 1;
      return { tools: [{ name: "profile_search" }, { name: "profile_edit" }] };
    },
    unregisterSessionTool: async (req: { name: string }) => {
      unregistered.push(req.name);
      return {};
    },
    registerSessionTool: async (req: { tool: { name: string } }) => {
      registered.push(req.tool.name);
      return {};
    },
  };

  return {
    client,
    unregistered,
    registered,
    listCalls: () => listCalls,
  };
}

describe("LlmdClient.registerTools", () => {
  it("never unregisters tools installed server-side", async () => {
    const h = harness();

    // A turn that passes no tools at all (the host-panel case).
    await h.client.registerTools([]);

    expect(h.unregistered).toEqual([]);
    expect(h.listCalls()).toBe(0);
  });

  it("unregisters a previously client-registered tool once it leaves the set", async () => {
    const h = harness();

    await h.client.registerTools([
      { name: "client_echo", description: "", parameters: {} } as never,
    ]);
    expect(h.registered).toEqual(["client_echo"]);
    expect(h.unregistered).toEqual([]);

    // Next turn: the client no longer wants its own tool.
    await h.client.registerTools([]);
    expect(h.unregistered).toEqual(["client_echo"]);

    // And still nothing server-side was touched.
    expect(h.unregistered).not.toContain("profile_search");
    expect(h.unregistered).not.toContain("profile_edit");
  });
});
