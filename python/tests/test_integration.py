"""Real keepsess process tests. Set KEEPSESS_TEST_BINARY and optionally
KEEPSESS_TEST_SSH_FIXTURE to the built fixture to enable protocol integration.
"""
from __future__ import annotations

import asyncio
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import time
import unittest
import urllib.request

from keepsess import AsyncKeepsess, Keepsess, ToolError

BINARY = os.environ.get('KEEPSESS_TEST_BINARY')
SSH_FIXTURE = os.environ.get('KEEPSESS_TEST_SSH_FIXTURE')


@unittest.skipUnless(BINARY, 'set KEEPSESS_TEST_BINARY for real engine tests')
class EngineTests(unittest.TestCase):
    def setUp(self):
        self.home = tempfile.TemporaryDirectory()
        self.addCleanup(self.home.cleanup)
        Path(self.home.name, '.keepsess.toml').write_text('[web]\nenabled = false\n')
        self.env = {'HOME': self.home.name, 'USERPROFILE': self.home.name}

    def client(self):
        client = Keepsess(BINARY, env=self.env)
        self.addCleanup(client.close)
        return client.start()

    def test_help_naming_and_typed_tool_errors(self):
        client = self.client()
        self.assertEqual(client.list().sessions, [])
        self.assertTrue(client.meta.instance_id)
        self.assertEqual(client.meta.api_contract, "2")
        self.assertTrue(client.help().topics)
        result = client.configure(instance_name='Python巡检')
        self.assertEqual(result.instance_name, 'Python巡检')
        self.assertEqual(client.list().instance_name, 'Python巡检')
        with self.assertRaises(ToolError) as caught:
            client.read_available(session='s999')
        self.assertEqual(caught.exception.result.instance_id, client.meta.instance_id)
        self.assertTrue(caught.exception.result.next_action)

    def test_named_profile_creates_new_connection_after_process_exit(self):
        listener = socket.socket()
        self.addCleanup(listener.close)
        listener.bind(('127.0.0.1', 0))
        listener.listen()
        listener.settimeout(5)
        config = Path(self.home.name, '.keepsess.toml')
        config.write_text('[web]\nenabled=false\n[profiles.fixture]\n'
                          'protocol="telnet"\nmode="stream"\nhost="127.0.0.1"\n'
                          f'port={listener.getsockname()[1]}\n')
        config.chmod(0o600)
        instances = []
        for _ in range(2):
            with Keepsess(BINARY, env=self.env) as client:
                self.assertEqual(client.meta.available_profiles, ['fixture'])
                conn = client.connect(profile='fixture', name='console')
                peer, _ = listener.accept()
                self.addCleanup(peer.close)
                self.assertTrue(conn.connected)
                self.assertEqual(client.list().available_profiles, ['fixture'])
                instances.append(client.meta.instance_id)
        self.assertNotEqual(*instances)

    def test_telnet_raw_escaped_and_read_only(self):
        listener = socket.socket()
        self.addCleanup(listener.close)
        listener.bind(('127.0.0.1', 0))
        listener.listen()
        received = bytearray()
        errors = []

        def endpoint():
            try:
                conn, _ = listener.accept()
                with conn:
                    conn.settimeout(5)
                    replies = 0
                    # Internal Telnet codec must handle split negotiation.
                    for part in (b'\xff', b'\xfb', b'\x01', b'fixture> '):
                        conn.sendall(part)
                    while True:
                        data = conn.recv(4096)
                        if not data:
                            return
                        received.extend(data)
                        commands = received.count(b'show\r') + received.count(b'plain\r')
                        if commands > replies:
                            replies = commands
                            conn.sendall(b'OK\r\nfixture> ')
            except Exception as error:
                errors.append(error)
        thread = threading.Thread(target=endpoint, daemon=True)
        thread.start()
        client = self.client()
        conn = client.connect(protocol='telnet', host='127.0.0.1',
                                   port=listener.getsockname()[1], mode='stream', line_end=r'\r')
        self.assertEqual(client.get_config(session=conn.session).config.line_end, r'\r')
        prompt = client.read_until(session=conn.session, end_pattern='fixture> ',
                                       read_idle_timeout_ms=0, read_total_timeout_ms=2000)
        self.assertIn('fixture>', prompt.output)
        result = client.send_escaped(session=conn.session, data=r'show\r',
                                         end_pattern='fixture> ', read_idle_timeout_ms=0,
                                         read_total_timeout_ms=2000)
        self.assertEqual(result.input_bytes, 5)
        self.assertEqual(result.written_bytes, 5)
        self.assertEqual(result.input_state, 'sent')
        self.assertIn('OK', result.output)
        # Ordinary Python escapes already produce the intended control bytes.
        raw = client.send_text(session=conn.session, data='plain\r',
                                   end_pattern='fixture> ', read_idle_timeout_ms=0,
                                   read_total_timeout_ms=2000)
        self.assertEqual(raw.input_bytes, 6)
        self.assertIn('OK', raw.output)
        client.send_text(session=conn.session, data='\x03', collect_output=False)
        client.send_text(session=conn.session, data=r'\x03', collect_output=False)
        client.configure(session=conn.session, read_only=True)
        with self.assertRaises(ToolError) as caught:
            client.sendline_text(session=conn.session, data='danger')
        self.assertEqual(caught.exception.result.input_state, 'not_sent')
        client.close(session=conn.session)
        thread.join(2)
        self.assertNotIn(b'danger', received)
        self.assertIn(b'plain\r', received)
        self.assertIn(b'\x03\\x03', received)
        self.assertEqual(errors, [])

    @unittest.skipUnless(SSH_FIXTURE, 'set KEEPSESS_TEST_SSH_FIXTURE')
    def test_ssh_exit_dual_stream_interactive_and_unknown(self):
        fixture = subprocess.Popen([SSH_FIXTURE], stdout=subprocess.PIPE, text=True)
        self.addCleanup(fixture.stdout.close)
        def stop():
            fixture.terminate()
            fixture.wait(timeout=3)
        self.addCleanup(stop)
        host, port = fixture.stdout.readline().strip().split(':')
        client = self.client()
        conn = client.connect(protocol='ssh', host=host, port=int(port),
                                   username='fixture', password='sdk-fixture')
        result = client.exec(session_id=conn.session, cmd='SDK_DUAL', execution_timeout_ms=2000)
        self.assertEqual(result.exit_code, 7)
        self.assertIn('stdout-中文', result.output)
        self.assertIn('stderr-fixture', result.output)
        self.assertIsNone(result.handle)
        task = client.exec(session=conn.session, command='SDK_SLOW', collect_output=False)
        self.assertTrue(task.handle)
        result = client.task_wait(task_id=task.handle, exec_timeout_ms=2000)
        self.assertEqual(result.exit_code, 7)
        self.assertTrue(client.task_list().tasks)
        interactive = client.exec(session=conn.session, command='SDK_INTERACTIVE',
                                       interactive=True, collect_output=False)
        client.send_text(handle=interactive.handle, data='stdin-fixture', collect_output=False)
        client.task_close_stdin(handle=interactive.handle)
        result = client.task_wait(handle=interactive.handle, timeout_ms=2000)
        self.assertEqual(result.output, 'stdin-fixture')
        unknown = client.exec(session=conn.session, command='SDK_UNKNOWN')
        self.assertIsNone(unknown.exit_code)
        self.assertTrue(unknown.warning)
        self.assertTrue(unknown.connected)

    def test_async_real_engine(self):
        async def run():
            async with AsyncKeepsess(BINARY, env=self.env) as client:
                results = await asyncio.gather(client.list(), client.help(query='timeout'))
                self.assertEqual(results[0].sessions, [])
                self.assertTrue(results[1].matched)
        asyncio.run(run())

    def test_web_sees_named_python_instance_and_archive(self):
        Path(self.home.name, '.keepsess.toml').write_text(
            '[web]\nenabled = true\nlisten = "127.0.0.1:0"\n'
            '[history]\ndir = "~/.keepsess/history"\n')
        # Stop only this test's isolated per-home daemon, after the client closes.
        def stop_daemon():
            subprocess.run([BINARY, 'daemon', 'stop'], env={**os.environ, **self.env},
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=10)
        self.addCleanup(stop_daemon)
        # Empty instances are intentionally hidden and removed on clean exit.
        # Retain a real session so this test exercises a visible archive.
        listener = socket.socket()
        self.addCleanup(listener.close)
        listener.bind(('127.0.0.1', 0))
        listener.listen()
        client = self.client()
        conn = client.connect(protocol='telnet', host='127.0.0.1',
                                   port=listener.getsockname()[1], mode='stream')
        self.assertTrue(conn.connected)
        client.configure(instance_name='Python-visible')
        self.assertTrue(client.meta.web.enabled, client.meta.web.error)
        url = client.meta.web.url + 'api/instances'
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        def find_instance():
            with opener.open(url, timeout=3) as response:
                records = json.load(response)
            return next((r for r in records if r['id'] == client.meta.instance_id), None)
        deadline = time.monotonic() + 5
        while True:
            record = find_instance()
            if record and record.get('name') == 'Python-visible':
                break
            self.assertLess(time.monotonic(), deadline)
            time.sleep(0.05)
        self.assertTrue(record['current'])
        client.close()
        deadline = time.monotonic() + 5
        while True:
            record = find_instance()
            if record and not record['current']:
                break
            self.assertLess(time.monotonic(), deadline)
            time.sleep(0.05)
        self.assertEqual(record['name'], 'Python-visible')
