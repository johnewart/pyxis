# llmdtest: testing against llmd without a live provider

`llmdtest` runs a real llmd server wired to a scriptable fake OpenAI-compatible
provider. No API key or external network is needed.

## Go usage

```go
env, err := llmdtest.StartInProcess(ctx, llmdtest.InProcessOptions{})
if err != nil { t.Fatal(err) }
defer env.Stop(ctx)

env.FakeOpenAI.Enqueue(llmdtest.TextTurn("hello there"))
sessCtx, err := env.NewSession(ctx, tenantscope.Scope{TenantID: "t", ScopeID: "s"}, "sess")
// env.Client is an llmv1.LlmServiceClient; use sessCtx for calls.
```

`InProcessOptions` fields: `DataDir`, `RegisterTools` (host inline tools),
`ToolProviderSecret`, and `ListenAddr` (serve on real TCP instead of bufconn).
`llmdtest.Start` spawns an llmd binary instead of running in-process.

Turn builders (one consumed per model request):

| Builder | Effect |
| --- | --- |
| `TextTurn(deltas...)` | Streamed or JSON assistant text |
| `SlowTextTurn(delay, deltas...)` | Text with a delay between deltas |
| `ToolCallTurn(id, name, args)` | One tool call |
| `ToolCallsTurn(calls...)` | Several tool calls |
| `ErrorTurn(status, message)` | OpenAI-style error with that HTTP status |
| `HangTurn()` | Never responds (cancellation tests) |
| `Turn.WithUsage(p, c, r)` | Adds token usage |

## Binary usage: llmd-fake

Build and run:

```
go -C llmd build -o /tmp/llmd-fake ./llmdtest/cmd/llmd-fake
/tmp/llmd-fake [-listen 127.0.0.1:0] [-tool-secret SECRET]
```

On startup it prints one JSON line on stdout (the ready line):

```json
{"grpc_addr":"127.0.0.1:41234","provider_id":"fake-openai","tenant_id":"llmdtest-default","fake_url":"http://127.0.0.1:35000"}
```

Clients send `tenant_id` as gRPC metadata key `llmd-tenant-id`. The process
runs until SIGINT/SIGTERM or until its stdin is closed. Its data dir is a temp
dir removed on exit.

### Control JSON schema

- `POST {fake_url}/_script` with `{"turns":[ScriptTurn,...]}` returns
  `{"queued":N}`. An invalid script is rejected whole with 400.
- `GET {fake_url}/_requests` returns
  `{"requests":[{"body":{...},"headers":{...}}]}`: the model requests llmd made.

ScriptTurn forms:

```
{"kind":"text","deltas":["a","b"]}
{"kind":"slow_text","delay_ms":150,"deltas":["a","b"]}
{"kind":"tool_call","id":"c1","name":"n","arguments":"{}"}
{"kind":"tool_calls","calls":[{"id":"c1","name":"n","arguments":"{}"}]}
{"kind":"error","status":429,"message":"slow down"}
{"kind":"hang"}
```

Any turn may add `"usage":{"prompt":1,"completion":2,"reasoning":0}`.

### Python example

```python
import json, subprocess, urllib.request
from pyxis_client import create_channel, llm_pb2, llm_pb2_grpc

proc = subprocess.Popen(["/tmp/llmd-fake"], stdin=subprocess.PIPE,
                        stdout=subprocess.PIPE, text=True)
ready = json.loads(proc.stdout.readline())

req = urllib.request.Request(
    ready["fake_url"] + "/_script",
    data=json.dumps({"turns": [{"kind": "text", "deltas": ["hi"]}]}).encode(),
    headers={"Content-Type": "application/json"}, method="POST")
urllib.request.urlopen(req).read()

with create_channel(ready["grpc_addr"], tenant_id=ready["tenant_id"]) as ch:
    stub = llm_pb2_grpc.LlmServiceStub(ch)
    reply = stub.Complete(
        llm_pb2.CompleteRequest(
            provider_id=ready["provider_id"],
            options=llm_pb2.CallOptions(model="fake-model"),
            messages=[llm_pb2.Message(role="user", content="hello")])).reply
    print(reply.content)

proc.stdin.close()  # shuts the server down
proc.wait()
```

The pytest smoke test is
`packages/python-client/tests/test_fake_binary_smoke.py`; it skips unless
`LLMD_FAKE_BIN` points at the built binary.
