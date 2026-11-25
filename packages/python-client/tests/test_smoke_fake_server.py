"""Smoke test: GenerateOnce and Complete against the llmdtest fake model server.

Builds and launches ``./llmdtest/cmd/fakeserver`` (in-process llmd + fake OpenAI
provider), reads its ``LISTENING host:port`` line, and calls both RPCs through
the generated stubs. Fails when the ``go`` toolchain is not on PATH.
"""

import os
import shutil
import subprocess
import threading

import pytest

from pyxis_client import create_channel, llm_pb2, llm_pb2_grpc

REPO_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
TENANT = "llmdtest-default"
REPLY = "hello from fake model"


@pytest.fixture(scope="module")
def server_addr(tmp_path_factory):
    go = shutil.which("go")
    if go is None:
        pytest.fail("go toolchain not available on PATH; the smoke test must call llmd")
    binary = str(tmp_path_factory.mktemp("fakeserver") / "fakeserver")
    subprocess.run(
        [go, "build", "-o", binary, "./llmdtest/cmd/fakeserver"],
        cwd=REPO_ROOT,
        check=True,
        timeout=600,
    )
    proc = subprocess.Popen(
        [binary], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True
    )
    result = {"seen": []}
    found = threading.Event()

    def read_lines():
        # llmd logs JSON lines too; keep draining so the pipe never fills.
        for raw in proc.stdout:
            if raw.startswith("LISTENING ") and "addr" not in result:
                result["addr"] = raw.strip()[len("LISTENING "):]
                found.set()
            elif "addr" not in result:
                result["seen"].append(raw)
        found.set()  # stream closed

    t = threading.Thread(target=read_lines, daemon=True)
    t.start()
    found.wait(60)
    addr = result.get("addr")
    if not addr:
        proc.kill()
        pytest.fail("fakeserver did not report an address; output: %r" % result["seen"])
    try:
        yield addr
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()


@pytest.fixture()
def stub(server_addr):
    channel = create_channel(server_addr, tenant_id=TENANT)
    try:
        yield llm_pb2_grpc.LlmServiceStub(channel)
    finally:
        channel.close()


def test_generate_once(stub):
    reply = stub.GenerateOnce(
        llm_pb2.GenerateOnceRequest(
            system_prompt="You are a test.",
            prompt="say hi",
            provider_id="fake-openai",
        ),
        timeout=30,
    )
    assert reply.content == REPLY


def test_complete(stub):
    # Complete rejects models missing from the catalog; discover fake-model.
    stub.RefreshLlmProviderModels(
        llm_pb2.RefreshLlmProviderModelsRequest(provider_id="fake-openai"),
        timeout=30,
    )
    resp = stub.Complete(
        llm_pb2.CompleteRequest(
            messages=[llm_pb2.Message(role="user", content="say hi")],
            provider_id="fake-openai",
        ),
        timeout=30,
    )
    assert resp.reply.content == REPLY
