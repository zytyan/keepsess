"""Public clients: a client owns exactly one persistent keepsess serve process."""
from __future__ import annotations

import asyncio
from collections.abc import Mapping, Sequence
from concurrent.futures import Future
import json
import os
import threading
from typing import Any, Coroutine, TypeVar

from ._methods import AsyncMethods, SyncMethods
from ._transport import Transport
from .errors import ProtocolError, ToolError, TransportError
from .models import Record, Result, decode_result

T = TypeVar("T")


async def _invoke(transport: Transport, tool: str, arguments: dict[str, Any]) -> Any:
    await transport.start()
    # None means omitted; false, zero, empty strings and empty maps are preserved.
    envelope = await transport.request("tools/call", {
        "name": tool, "arguments": {k: v for k, v in arguments.items() if v is not None},
    })
    try:
        if "structuredContent" in envelope:
            data = envelope["structuredContent"]
        else:
            content = envelope["content"]
            if len(content) != 1 or content[0].get("type") != "text":
                raise ValueError("Expected one JSON text result")
            data = json.loads(content[0]["text"])
        result = decode_result(tool, data)
    except (KeyError, TypeError, ValueError, AttributeError) as error:
        raise ProtocolError("Invalid keepsess tool result") from error
    if envelope.get("isError") or (isinstance(data, dict) and data.get("error")):
        raise ToolError(tool, result, envelope)
    return result


class AsyncKeepsess(AsyncMethods):
    """Async context manager; use on one asyncio loop. No automatic retries.

    binary / KEEPSESS_BINARY selects the engine executable (default PATH).
    rpc_timeout and startup_timeout are finite seconds, unlike tool *_ms values.
    command overrides the entire argv for wrappers/tests; env overlays process env.
    """

    def __init__(self, binary: str | os.PathLike[str] | None = None, *,
                 rpc_timeout: float = 125, startup_timeout: float = 15,
                 command: Sequence[str] | None = None,
                 env: Mapping[str, str] | None = None):
        self._transport = Transport(binary, rpc_timeout=rpc_timeout,
                                    startup_timeout=startup_timeout, command=command, env=env)

    @property
    def server_info(self) -> Record:
        return self._transport.server_info

    @property
    def meta(self) -> Record:
        """initialize._meta, including instance_id and Web information."""
        return self._transport.meta

    @property
    def stderr_tail(self) -> str:
        return self._transport.stderr_tail

    async def start(self) -> AsyncKeepsess:
        await self._transport.start()
        return self

    async def _invoke(self, tool: str, arguments: dict[str, Any]) -> Any:
        return await _invoke(self._transport, tool, arguments)

    async def close(self, *, session: str | None = None) -> Result | None:
        """With session, close only that connection; without it, stop this client."""
        if session is not None:
            return await self._invoke('close', {'session': session})
        await self._transport.close()

    async def __aenter__(self) -> AsyncKeepsess:
        return await self.start()

    async def __aexit__(self, *_: Any) -> None:
        await self.close()


class Keepsess(SyncMethods):
    """Synchronous, thread-safe client backed by one private asyncio loop.

    Prefer `with Keepsess(...) as ks`. For asyncio programs use AsyncKeepsess.
    Methods share the engine's concurrency/busy semantics, without a retry queue.
    """

    def __init__(self, binary: str | os.PathLike[str] | None = None, *,
                 rpc_timeout: float = 125, startup_timeout: float = 15,
                 command: Sequence[str] | None = None,
                 env: Mapping[str, str] | None = None):
        self._transport = Transport(binary, rpc_timeout=rpc_timeout,
                                    startup_timeout=startup_timeout, command=command, env=env)
        self._lock = threading.Lock()
        self._loop: asyncio.AbstractEventLoop | None = None
        self._thread: threading.Thread | None = None
        self._closed = False

    @property
    def server_info(self) -> Record:
        return self._transport.server_info

    @property
    def meta(self) -> Record:
        return self._transport.meta

    @property
    def stderr_tail(self) -> str:
        return self._transport.stderr_tail

    def _submit(self, coroutine: Coroutine[Any, Any, T]) -> Future[T]:
        with self._lock:
            if self._closed:
                coroutine.close()
                raise TransportError("Client is closed; create a new client and connect explicitly")
            if self._loop is None:
                self._loop = asyncio.new_event_loop()
                self._thread = threading.Thread(target=self._serve_loop, name="keepsess-stdio", daemon=True)
                self._thread.start()
            return asyncio.run_coroutine_threadsafe(coroutine, self._loop)

    def _serve_loop(self) -> None:
        assert self._loop is not None
        asyncio.set_event_loop(self._loop)
        try:
            self._loop.run_forever()
        finally:
            tasks = asyncio.all_tasks(self._loop)
            for task in tasks:
                task.cancel()
            self._loop.run_until_complete(asyncio.gather(*tasks, return_exceptions=True))
            self._loop.close()

    def _run(self, coroutine: Coroutine[Any, Any, T]) -> T:
        future = self._submit(coroutine)
        try:
            return future.result()
        except KeyboardInterrupt:
            future.cancel()
            raise

    def start(self) -> Keepsess:
        try:
            self._run(self._transport.start())
        except BaseException:
            self.close()
            raise
        return self

    def _invoke(self, tool: str, arguments: dict[str, Any]) -> Any:
        return self._run(_invoke(self._transport, tool, arguments))

    def close(self, *, session: str | None = None) -> Result | None:
        """With session, close only that connection; without it, stop this client."""
        if session is not None:
            return self._invoke('close', {'session': session})
        with self._lock:
            if self._closed:
                return
            self._closed = True
            loop, thread = self._loop, self._thread
        if loop is not None:
            try:
                asyncio.run_coroutine_threadsafe(self._transport.close(), loop).result(timeout=10)
            finally:
                loop.call_soon_threadsafe(loop.stop)
                assert thread is not None
                thread.join(timeout=2)

    def __enter__(self) -> Keepsess:
        return self.start()

    def __exit__(self, *_: Any) -> None:
        self.close()
