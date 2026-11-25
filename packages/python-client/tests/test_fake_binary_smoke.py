"""Smoke test: drive the llmd-fake binary (in-process llmd + fake OpenAI) over gRPC.

Skipped unless LLMD_FAKE_BIN points at an executable built from
llmdtest/cmd/llmd-fake, e.g.:

    go -C llmd build -o /tmp/llmd-fake ./llmdtest/cmd/llmd-fake
    LLMD_FAKE_BIN=/tmp/llmd-fake pytest

No real provider or external network is used; everything stays on loopback.
"""

import json
import os
import subprocess
import urllib.request

import pytest

from pyxis_client import create_channel, llm_pb2, llm_pb2_grpc

BIN = os.environ.get("LLMD_FAKE_BIN", "")

pytestmark = pytest.mark.skipif(
    not BIN or not os.path.isfile(BIN) or not os.access(BIN, os.X_OK),
    reason="LLMD_FAKE_BIN not set to an executable llmd-fake binary",
)


@pytest.fixture(scope="module")
def fake():
    proc = subprocess.Popen(
        [BIN],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    try:
        line = proc.stdout.readline()
        if not line:
            raise RuntimeError("llmd-fake exited before ready line: " + proc.stderr.read())
        yield json.loads(line)
    finally:
        # Closing stdin makes llmd-fake shut down; terminate as a fallback.
        if proc.stdin:
            proc.stdin.close()
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()
        proc.stdout.close()
        proc.stderr.close()


def _post_json(url, body):
    req = urllib.request.Request(
        url,
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.loads(resp.read())


def test_scripted_turn_reply(fake):
    queued = _post_json(
        fake["fake_url"] + "/_script",
        {"turns": [{"kind": "text", "deltas": ["hello ", "from fake"]}]},
    )
    assert queued == {"queued": 1}

    with create_channel(fake["grpc_addr"], tenant_id=fake["tenant_id"]) as channel:
        stub = llm_pb2_grpc.LlmServiceStub(channel)
        resp = stub.Complete(
            llm_pb2.CompleteRequest(
                provider_id=fake["provider_id"],
                options=llm_pb2.CallOptions(model="fake-model"),
                messages=[llm_pb2.Message(role="user", content="hi")],
            ),
            timeout=30,
        )
    assert resp.reply.content == "hello from fake"
