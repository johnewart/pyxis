import * as z from "zod";

/**
 * Instance shape for tools created with {@link defineClientTool}.
 * Matches the fields a host and llmd use: id, Zod input/output, and exec(input, context).
 */
export type ClientDefinedTool = {
  readonly id: string;
  readonly purpose: string;
  /** Short human-facing blurb for settings UI and docs (not sent to the model). */
  readonly friendlySummary: string;
  readonly description: string;
  readonly parameters: z.ZodType;
  readonly results: z.ZodType;
  exec(input: unknown, context: unknown): Promise<unknown> | unknown;
};

export type DefineClientToolConfig = {
  id: string;
  /** Defaults to {@link DefineClientToolConfig.id} when omitted. */
  purpose?: string;
  /** Short human-facing blurb for settings UI and docs (not sent to the model). */
  friendlySummary?: string;
  description: string;
  parameters: z.ZodType;
  /** Defaults to `z.string()` when omitted (plain text / JSON string tool output). */
  results?: z.ZodType;
  exec: (input: unknown, context: unknown) => Promise<unknown> | unknown;
};

/**
 * Define a tool class compatible with a host's tool registry (`new (defineClientTool(...))()`)
 * and with OpenAI-style conversion via {@link convertClientToolToOpenAiFormat}.
 */
export function defineClientTool(
  config: DefineClientToolConfig
): new () => ClientDefinedTool {
  const results = config.results ?? z.string();
  const purpose = config.purpose ?? config.id;
  const friendlySummary = config.friendlySummary ?? "";
  class DefinedClientTool implements ClientDefinedTool {
    readonly id = config.id;
    readonly purpose = purpose;
    readonly friendlySummary = friendlySummary;
    readonly description = config.description;
    readonly parameters = config.parameters;
    readonly results = results;
    exec(input: unknown, context: unknown): Promise<unknown> | unknown {
      return config.exec(input, context);
    }
  }
  return DefinedClientTool as unknown as new () => ClientDefinedTool;
}

/** OpenAI Chat Completions tool definition shape (single function). */
export type OpenAiFunctionTool = {
  type: "function";
  function: {
    name: string;
    description: string;
    parameters: Record<string, unknown>;
  };
};

/**
 * Convert a client-defined tool (or any object with id, description, Zod parameters) to OpenAI tool format.
 */
export function convertClientToolToOpenAiFormat(tool: {
  id: string;
  description: string;
  parameters: z.ZodType;
}): OpenAiFunctionTool {
  const parameters = z.toJSONSchema(tool.parameters) as Record<string, unknown>;
  return {
    type: "function",
    function: {
      name: tool.id,
      description: tool.description,
      parameters,
    },
  };
}
