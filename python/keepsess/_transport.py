"""Bounded, multiplexed MCP stdio transport; no command retry or replay."""
from __future__ import annotations

import asyncio
import json
import math
import os
from collections import deque
from collections.abc import Mapping, Sequence
from typing import Any

from .errors import CallTimeout, ClientBusy, ProtocolError, TransportError
from .models import Record, decode_record

MAX_FRAME = 16 * 1024 * 1024
MAX_INFLIGHT = 32


def finite_timeout(value: float, name: str) -> float:
    value = float(value)
    if not math.isfinite(value) or value <= 0:
        raise ValueError(f"{name} must be finite and positive")
    return value


class Transport:
    def __init__(self, binary: str | os.PathLike[str] | None = None, *,
                 rpc_timeout: float = 125, startup_timeout: float = 15,
                 command: Sequence[str] | None = None,
                 env: Mapping[str, str] | None = None):
        if command is not None and (binary is not None or not command or isinstance(command, str)):
            raise ValueError("command must be a non-empty sequence, exclusive with binary")
        self.command = list(command) if command is not None else [
            os.fspath(binary or os.environ.get("KEEPSESS_BINARY", "keepsess")), "serve"]
        self.env = None if env is None else {**os.environ, **env}
        self.rpc_timeout = finite_timeout(rpc_timeout, "rpc_timeout")
        self.startup_timeout = finite_timeout(startup_timeout, "startup_timeout")
        self.process: asyncio.subprocess.Process | None = None
        self.server_info = Record()
        self.meta = Record()
        self.notifications: deque[dict[str, Any]] = deque(maxlen=32)
        self._stderr = bytearray()
        self._pending: dict[int, asyncio.Future[Any]] = {}
        self._outbox: asyncio.Queue[bytes] = asyncio.Queue(maxsize=64)
        self._tasks: list[asyncio.Task[Any]] = []
        self._next_id = 0
        self._started = False
        self._closed = False
        self._failure: Exception | None = None
        self._start_lock = asyncio.Lock()
        self._close_lock = asyncio.Lock()

    @property
    def stderr_tail(self) -> str:
        """Explicit diagnostics only; may contain private data from the engine."""
        return self._stderr.decode("utf-8", "replace")

    async def start(self) -> None:
        async with self._start_lock:
            self._check()
            if self._started:
                return
            try:
                self.process = await asyncio.wait_for(asyncio.create_subprocess_exec(
                    *self.command, stdin=asyncio.subprocess.PIPE,
                    stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE,
                    limit=MAX_FRAME + 1, env=self.env), self.startup_timeout)
                self._check()  # close may have run while process creation yielded
                self._tasks = [asyncio.create_task(self._read()),
                               asyncio.create_task(self._write()),
                               asyncio.create_task(self._drain_stderr())]
                init = await self.request("initialize", {
                    "protocolVersion": "2025-11-25", "capabilities": {},
                    "clientInfo": {"name": "keepsess-python", "version": "0.1.0"},
                }, timeout=self.startup_timeout)
                info = init.get("serverInfo", {})
                version = info.get("version", "").split("+", 1)[0]
                if info.get("name") != "keepsess" or not version.startswith("0.3."):
                    raise ProtocolError("This SDK requires keepsess 0.3.x")
                if init.get("protocolVersion") != "2025-11-25":
                    raise ProtocolError("Unsupported MCP protocol version")
                self.server_info = Record.from_dict(info)
                self.meta = decode_record(Record, init.get("_meta", {}))
                self.notify("notifications/initialized", {})
                self._started = True
            except BaseException as error:
                await self.close()
                if isinstance(error, OSError):
                    raise TransportError("Could not start keepsess; check binary path and permissions") from error
                raise

    def _check(self) -> None:
        if self._closed:
            raise TransportError("Client is closed; create a new client and connect explicitly")
        if self._failure:
            raise TransportError(str(self._failure))

    def _fail(self, error: Exception) -> None:
        if self._failure is None:
            self._failure = error
        for future in self._pending.values():
            if not future.done():
                future.set_exception(error)
        self._pending.clear()
        if self.process is not None and self.process.returncode is None:
            try:
                self.process.terminate()
            except ProcessLookupError:
                pass

    def _enqueue(self, message: dict[str, Any]) -> None:
        self._check()
        data = json.dumps(message, ensure_ascii=True, allow_nan=False,
                          separators=(",", ":")).encode("utf-8") + b"\n"
        if len(data) > MAX_FRAME:
            raise ValueError("MCP input exceeds SDK's 16MiB frame limit; nothing sent")
        try:
            self._outbox.put_nowait(data)
        except asyncio.QueueFull:
            raise ClientBusy("Client output queue full; this call was not sent") from None

    def notify(self, method: str, params: dict[str, Any]) -> None:
        self._enqueue({"jsonrpc": "2.0", "method": method, "params": params})

    async def request(self, method: str, params: dict[str, Any], *,
                      timeout: float | None = None) -> Any:
        self._check()
        if len(self._pending) >= MAX_INFLIGHT:
            raise ClientBusy("Maximum 32 in-flight calls; this call was not sent")
        self._next_id += 1
        request_id = self._next_id
        future = asyncio.get_running_loop().create_future()
        self._pending[request_id] = future
        try:
            self._enqueue({"jsonrpc": "2.0", "id": request_id,
                           "method": method, "params": params})
            try:
                return await asyncio.wait_for(asyncio.shield(future),
                                              self.rpc_timeout if timeout is None else timeout)
            except (asyncio.TimeoutError, asyncio.CancelledError) as error:
                try:
                    self.notify("notifications/cancelled", {"requestId": request_id})
                except (TransportError, ClientBusy):
                    self._fail(TransportError("Could not enqueue cancellation; outcome may be unknown"))
                if isinstance(error, asyncio.CancelledError):
                    raise
                raise CallTimeout(request_id, params.get("name", method)) from None
        finally:
            self._pending.pop(request_id, None)
            # Late responses are drained by the reader, never attached to a new call.
            if not future.done():
                future.cancel()
            elif not future.cancelled():
                future.exception()  # retrieve errors if cancellation won a race

    async def _read(self) -> None:
        assert self.process is not None and self.process.stdout is not None
        try:
            while True:
                line = await self.process.stdout.readline()
                if not line:
                    raise TransportError("keepsess stdout closed; outcomes may be unknown; no replay")
                if len(line) > MAX_FRAME or not line.endswith(b"\n"):
                    raise ProtocolError("Invalid or oversized MCP frame")
                message = json.loads(line)
                if not isinstance(message, dict) or message.get("jsonrpc") != "2.0":
                    raise ProtocolError("Invalid MCP message")
                if "method" in message:
                    if "id" in message:
                        self._enqueue({"jsonrpc": "2.0", "id": message["id"],
                                       "error": {"code": -32601, "message": "Unsupported client method"}})
                    else:
                        self.notifications.append(message)
                    continue
                request_id = message.get("id")
                if not isinstance(request_id, int) or isinstance(request_id, bool):
                    raise ProtocolError("Invalid MCP response ID")
                future = self._pending.get(request_id)
                if future is None or future.done():
                    continue
                if "error" in message:
                    error = message["error"]
                    future.set_exception(ProtocolError(error.get("message", "JSON-RPC error"),
                                                       code=error.get("code")))
                elif "result" in message:
                    future.set_result(message["result"])
                else:
                    raise ProtocolError("MCP response missing result/error")
        except asyncio.CancelledError:
            raise
        except Exception as error:
            self._fail(error if isinstance(error, (ProtocolError, TransportError)) else
                       ProtocolError("Malformed MCP response"))

    async def _write(self) -> None:
        assert self.process is not None and self.process.stdin is not None
        try:
            while True:
                data = await self._outbox.get()
                self.process.stdin.write(data)
                await asyncio.wait_for(self.process.stdin.drain(), 5)
        except asyncio.CancelledError:
            raise
        except Exception:
            self._fail(TransportError("keepsess stdin failed/stalled; input outcome may be unknown"))

    async def _drain_stderr(self) -> None:
        assert self.process is not None and self.process.stderr is not None
        while True:
            data = await self.process.stderr.read(4096)
            if not data:
                return
            self._stderr.extend(data)
            del self._stderr[:-65536]

    async def close(self) -> None:
        async with self._close_lock:
            if self._closed and (self.process is None or self.process.returncode is not None):
                return
            self._closed = True
            for future in self._pending.values():
                if not future.done():
                    future.set_exception(TransportError("Client closed; remote outcomes may be unknown"))
            self._pending.clear()
            process = self.process
            try:
                if process is not None:
                    if process.stdin is not None:
                        process.stdin.close()
                    try:
                        await asyncio.wait_for(process.wait(), 3)
                    except asyncio.TimeoutError:
                        try:
                            process.terminate()
                        except ProcessLookupError:
                            pass
                        try:
                            await asyncio.wait_for(process.wait(), 2)
                        except asyncio.TimeoutError:
                            try:
                                process.kill()
                            except ProcessLookupError:
                                pass
                            await asyncio.wait_for(process.wait(), 2)
            finally:
                for task in self._tasks:
                    task.cancel()
                await asyncio.gather(*self._tasks, return_exceptions=True)
                while not self._outbox.empty():
                    self._outbox.get_nowait()
