from __future__ import annotations

import asyncio
from concurrent.futures import ThreadPoolExecutor
from dataclasses import is_dataclass
import inspect
import json
import math
from pathlib import Path
import sys
import unittest

from keepsess import (AsyncKeepsess, CallTimeout, ClientBusy, ExecResult, Keepsess,
                      ProtocolError, ToolError, TransportError)

COMMAND = [sys.executable, str(Path(__file__).with_name('fake_engine.py'))]


class ClientTests(unittest.TestCase):
    def client(self, **kwargs):
        client = Keepsess(command=COMMAND, **kwargs)
        self.addCleanup(client.close)
        return client

    def test_typed_results_and_omission(self):
        with self.client() as client:
            result = client.exec(command='echo', login=False, env={}, max_output_bytes=0)
            self.assertIsInstance(result, ExecResult)
            self.assertTrue(is_dataclass(result))
            self.assertEqual(result.peer.server, 'fixture')
            self.assertEqual(result.new_field, 'retained')
            self.assertEqual(result.exit_code, 8)  # not a tool/transport failure
            self.assertIsNone(result.handle)
            self.assertNotIn('handle', result.to_dict())
            self.assertEqual(json.loads(result.output), {
                'command': 'echo', 'login': False, 'env': {}, 'max_output_bytes': 0})
            self.assertEqual(result.to_dict()['peer']['server'], 'fixture')
            self.assertEqual(client.meta.instance_id, 'fixture')
            self.assertEqual(client.meta.web.url, 'http://localhost/')
            self.assertEqual(client.list().sessions[0].peer.server, 'fixture')

    def test_error_preserves_receipt_and_handle(self):
        client = self.client()
        with self.assertRaises(ToolError) as caught:
            client.exec(command='error')
        self.assertEqual(caught.exception.result.input_state, 'not_sent')
        self.assertEqual(caught.exception.result.handle, 'task1')
        self.assertTrue(caught.exception.envelope['isError'])

    def test_json_text_fallback(self):
        result = self.client().exec(command='text-fallback')
        self.assertEqual(result.exit_code, 8)
        self.assertEqual(result.peer.server, 'fixture')

    def test_incompatible_version_is_reaped(self):
        client = self.client(env={'FAKE_VERSION': '0.2.0'})
        with self.assertRaises(ProtocolError):
            client.start()
        self.assertIsNotNone(client._transport.process.returncode)

    def test_startup_timeout_is_reaped(self):
        client = self.client(env={'FAKE_INIT_DELAY': '1'}, startup_timeout=0.05)
        with self.assertRaises(CallTimeout):
            client.start()
        self.assertIsNotNone(client._transport.process.returncode)

    def test_sync_concurrent_out_of_order(self):
        client = self.client().start()
        with ThreadPoolExecutor(max_workers=3) as pool:
            slow = pool.submit(client.exec, command='sleep:0.2')
            fast = pool.submit(client.exec, command='fast')
            self.assertEqual(json.loads(fast.result().output)['command'], 'fast')
            self.assertFalse(slow.done())
            self.assertEqual(json.loads(slow.result().output)['command'], 'sleep:0.2')

    def test_timeout_cancellation_and_late_response(self):
        client = self.client(rpc_timeout=0.08).start()
        with self.assertRaises(CallTimeout):
            client.exec(command='sleep:0.2')
        # Give the late response time to arrive; it must never match another call.
        import time
        time.sleep(0.22)
        self.assertEqual(json.loads(client.exec(command='next').output)['command'], 'next')
        self.assertIn('cancelled', client.stderr_tail)

    def test_large_output_and_stderr_are_drained(self):
        client = self.client()
        self.assertGreater(len(client.exec(command='large').output), 300000)
        client.exec(command='stderr')
        self.assertLessEqual(len(client.stderr_tail.encode()), 65536)

    def test_failure_does_not_restart(self):
        client = self.client()
        with self.assertRaises(TransportError):
            client.exec(command='exit')
        with self.assertRaises(TransportError):
            client.list()

    def test_protocol_failure(self):
        client = self.client()
        with self.assertRaises(ProtocolError):
            client.exec(command='malformed')

    def test_close_session_preserves_client_process(self):
        with self.client() as client:
            receipt = client.close(session='s1')
            self.assertEqual(json.loads(receipt.output), {'session': 's1'})
            self.assertIsInstance(client.exec(command='still alive'), ExecResult)

    def test_close_is_idempotent_and_reaps_process(self):
        client = self.client().start()
        process = client._transport.process
        client.close()
        client.close()
        self.assertIsNotNone(process.returncode)
        self.assertFalse(client._thread.is_alive())
        with self.assertRaises(TransportError):
            client.list()

    def test_configuration_errors_and_lazy_close(self):
        for value in [0, -1, math.inf, math.nan]:
            with self.assertRaises(ValueError):
                Keepsess(rpc_timeout=value)
        client = Keepsess()
        client.close()
        self.assertIsNone(client._thread)
        with self.assertRaises(TypeError):
            self.client().exec(command='x', made_up=True)

    def test_missing_engine_fails_cleanly(self):
        client = Keepsess(binary='/does-not-exist/keepsess')
        self.addCleanup(client.close)
        with self.assertRaises(TransportError):
            client.start()
        self.assertFalse(client._thread.is_alive())

    def test_all_30_explicit_methods_have_matching_signatures(self):
        from keepsess._methods import SyncMethods
        methods = [name for name, method in vars(SyncMethods).items()
                   if not name.startswith('_') and callable(method)]
        self.assertEqual(len(methods), 30)
        for name in methods:
            sync = inspect.signature(getattr(Keepsess, name))
            async_sig = inspect.signature(getattr(AsyncKeepsess, name))
            self.assertEqual(sync, async_sig)
            self.assertFalse(any(p.kind == p.VAR_KEYWORD for p in sync.parameters.values()))


class AsyncTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.client = AsyncKeepsess(command=COMMAND)
        await self.client.start()

    async def test_close_session_preserves_client_process(self):
        receipt = await self.client.close(session='s1')
        self.assertEqual(json.loads(receipt.output), {'session': 's1'})
        self.assertIsInstance(await self.client.exec(command='still alive'), ExecResult)

    async def asyncTearDown(self):
        await self.client.close()

    async def test_cancel_one_call_keeps_other_calls_alive(self):
        task = asyncio.create_task(self.client.exec(command='sleep:0.2'))
        await asyncio.sleep(0.02)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        result = await self.client.exec(command='next')
        self.assertEqual(json.loads(result.output)['command'], 'next')
        await asyncio.sleep(0.22)
        self.assertIn('cancelled', self.client.stderr_tail)

    async def test_inflight_is_bounded_without_queueing(self):
        calls = [asyncio.create_task(self.client.exec(command='sleep:0.3')) for _ in range(32)]
        await asyncio.sleep(0.05)
        with self.assertRaises(ClientBusy):
            await self.client.list()
        await asyncio.gather(*calls)

    async def test_close_cancels_pending_calls(self):
        call = asyncio.create_task(self.client.exec(command='sleep:3'))
        await asyncio.sleep(0.02)
        await self.client.close()
        with self.assertRaises(TransportError):
            await call

    async def test_stalled_stdin_is_bounded(self):
        await self.client.exec(command='stop-reading')
        with self.assertRaises(TransportError):
            await asyncio.wait_for(self.client.exec(command='x' * 1_000_000), 8)
