"""Local CLI acceptance: actual Worker JS, WebSocket, QUIC, TCP and log output."""
import asyncio
import contextlib
import json
import os
import re
import signal
import socket
import tempfile
from pathlib import Path
from urllib.parse import parse_qs, urlparse

from websockets.asyncio.server import serve
from websockets.exceptions import ConnectionClosed

ROOT = Path(__file__).resolve().parent
BINARY = Path(os.environ.get("HOLE_CLI_BINARY", str(ROOT / "hole")))
DEBUG = os.environ.get("HOLE_CLI_DEBUG") == "1"


async def main():
    node = await asyncio.create_subprocess_exec("node", str(ROOT / "worker" / "worker_fixture.test.mjs"), stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
    peers, clients, readers, logs, turn_requests = {}, [], [], {}, []
    lock = asyncio.Lock()

    async def send(value):
        async with lock:
            node.stdin.write((json.dumps(value) + "\n").encode())
            await node.stdin.drain()

    async def handler(ws):
        ident = id(ws)
        peers[ident] = ws
        await send(dict(kind="open", id=ident, room=parse_qs(urlparse(ws.request.path).query).get("room", ["fixture"])[0]))
        try:
            async for message in ws:
                if json.loads(message).get("type") == "turn_request":
                    turn_requests.append(ident)
                await send(dict(kind="message", id=ident, data=message))
        except ConnectionClosed:
            pass
        finally:
            peers.pop(ident, None)
            with contextlib.suppress(Exception):
                await send(dict(kind="close", id=ident))

    async def pump():
        async for line in node.stdout:
            item = json.loads(line)
            ws = peers.get(item["id"])
            if ws is not None:
                with contextlib.suppress(ConnectionClosed):
                    if "close" in item:
                        await ws.close(item["close"]["code"], item["close"]["reason"])
                    else:
                        await ws.send(item["data"])

    async def echo(reader, writer):
        try:
            while data := await reader.read(16384):
                writer.write(data)
                await writer.drain()
        finally:
            writer.close()
            await writer.wait_closed()

    async def collect(name, proc):
        async for line in proc.stdout:
            logs[name].append(line.decode().rstrip())

    service = await asyncio.start_server(echo, "127.0.0.1", 0)
    service_port = service.sockets[0].getsockname()[1]
    server = await serve(handler, "127.0.0.1", 0)
    worker = asyncio.create_task(pump())
    try:
        base = dict(server_url=f"ws://127.0.0.1:{server.sockets[0].getsockname()[1]}/ws", room="fixture", password="PRIVATE_PASSWORD", token="PRIVATE_ROOM_TOKEN", session_timeout="10m", transport=dict(preferred="ice", allow_legacy=True, allow_insecure_signal=True), ice=dict(stun_urls=[], include_loopback=True, interface_allowlist=["lo"], direct_probe_timeout="5s"))
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            local_port = sock.getsockname()[1]
        with tempfile.TemporaryDirectory(prefix="hole-cli-smoke-") as tmp:
            missing = Path(tmp) / "missing-server.json"
            missing.write_text("{}")
            check = await asyncio.create_subprocess_exec(str(BINARY), "-config", str(missing), stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.STDOUT)
            result, _ = await asyncio.wait_for(check.communicate(), 5)
            assert check.returncode != 0 and b"server_url" in result and "【启动】".encode() not in result
            for name in ("alpha", "beta"):
                config = dict(base, device_name=name, provide=[], consume=[])
                if name == "alpha":
                    config["provide"] = [dict(id="echo", service=f"tcp://127.0.0.1:{service_port}")]
                else:
                    config["consume"] = [dict(id="echo", expose=f"127.0.0.1:{local_port}")]
                path = Path(tmp) / f"{name}.json"
                path.write_text(json.dumps(config))
                args = [str(BINARY), "-config", str(path)]
                if DEBUG:
                    args.append("-debug")
                proc = await asyncio.create_subprocess_exec(*args, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.STDOUT)
                clients.append(proc)
                logs[name] = []
                readers.append(asyncio.create_task(collect(name, proc)))

            async def ready():
                while not all(any("映射已就绪" in line for line in lines) for lines in logs.values()):
                    assert all(p.returncode is None for p in clients), logs
                    await asyncio.sleep(0.1)

            await asyncio.wait_for(ready(), 18)
            reader, writer = await asyncio.open_connection("127.0.0.1", local_port)
            payload = b"CLI_CONFIGURED_SIGNAL_AND_DEFAULT_WORKER_TURN" * 256
            writer.write(payload)
            await writer.drain()
            assert await asyncio.wait_for(reader.readexactly(len(payload)), 5) == payload
            await asyncio.sleep(0.2)
            idle_counts = {name: len(lines) for name, lines in logs.items()}
            await asyncio.sleep(2)
            assert idle_counts == {name: len(lines) for name, lines in logs.items()}, "idle connection generated log noise"
            writer.close()
            await writer.wait_closed()

            # A failed upstream service must not be confused with a failed path.
            service.close()
            await service.wait_closed()
            for _ in range(6):
                reader, writer = await asyncio.open_connection("127.0.0.1", local_port)
                try:
                    writer.write(b"SERVICE_SHOULD_BE_CLOSED")
                    await writer.drain()
                    try:
                        assert await asyncio.wait_for(reader.read(1), 5) == b""
                    except ConnectionResetError:
                        pass
                finally:
                    writer.close()
                    with contextlib.suppress(ConnectionError):
                        await writer.wait_closed()

            async def wait_log(expected):
                while not all(any(expected in line for line in lines) for lines in logs.values()):
                    assert all(p.returncode is None for p in clients), logs
                    await asyncio.sleep(0.02)

            await asyncio.wait_for(wait_log(" x5"), 5)
            for name, lines in logs.items():
                assert sum("ERROR" in line and "拒绝连接" in line for line in lines) == 2, logs
                assert not any("的连接中断" in line or "映射未就绪" in line for line in lines), logs

            service = await asyncio.start_server(echo, "127.0.0.1", service_port)
            reader, writer = await asyncio.open_connection("127.0.0.1", local_port)
            writer.write(payload)
            await writer.drain()
            assert await asyncio.wait_for(reader.readexactly(len(payload)), 5) == payload
            await asyncio.wait_for(wait_log("目标服务已恢复"), 5)
            writer.close()
            await writer.wait_closed()
    finally:
        for proc in clients:
            if proc.returncode is None:
                proc.send_signal(signal.SIGINT)
        for proc in clients:
            try:
                await asyncio.wait_for(proc.wait(), 5)
            except asyncio.TimeoutError:
                proc.kill()
                await proc.wait()
        await asyncio.gather(*readers)
        server.close()
        await server.wait_closed()
        service.close()
        await service.wait_closed()
        node.stdin.close()
        await asyncio.wait_for(node.wait(), 5)
        await worker
    stderr = (await node.stderr.read()).decode()
    assert not stderr, stderr
    output = ROOT / "android/dist" / ("cli-smoke-debug" if DEBUG else "cli-smoke")
    output.mkdir(parents=True, exist_ok=True)
    for name, lines in logs.items():
        text = "\n".join(lines) + "\n"
        (output / f"{name}.log").write_text(text)
        assert "PRIVATE_" not in text, text
        assert all(re.match(r"^\d{2}:\d{2}:\d{2} (INFO |WARN |ERROR|DEBUG) ", line) for line in lines), f"unformatted library output:\n{text}"
        for expected in ["已连接 直连/IPv4", "映射已就绪", "hole 已停止"]:
            assert expected in text, f"{name}: missing {expected}\n{text}"
        if DEBUG:
            for expected in ["DEBUG", "turn mode=worker", "gen=", "path=直连/IPv4"]:
                assert expected in text, f"{name}: missing debug detail {expected}\n{text}"
        else:
            for hidden in ["DEBUG", "gen=", "session=", "STUN", "turn mode=", "【", "「", "·"]:
                assert hidden not in text, f"{name}: default log contains {hidden}\n{text}"
        assert "traffic peer=" not in text and "status engine=" not in text, "periodic sampling remains"
        path_line = next(i for i, line in enumerate(lines) if "已连接 直连/IPv4" in line)
        mapping_line = next(i for i, line in enumerate(lines) if "映射已就绪" in line)
        assert path_line < mapping_line, f"{name}: mapping readiness appeared before connection"
    assert not turn_requests, "healthy direct path requested TURN"
    assert sum("映射已就绪" in line for line in logs["beta"]) == 1
    assert any(f"本地入口 127.0.0.1:{local_port}" in line for line in logs["beta"])
    print(f"PASS ({'debug' if DEBUG else 'default'}): Worker/QUIC TCP echo, Chinese redacted logs, no idle noise/TURN requests, path before mapping, service failures summarized, recovery and clean stop")
    print("\n".join(logs["beta"]))


if __name__ == "__main__":
    asyncio.run(main())
