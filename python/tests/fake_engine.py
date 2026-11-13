"""Adversarial stdio peer used only by SDK transport tests."""
import json
import os
import sys
import threading
import time

lock = threading.Lock()


def respond(request):
    method = request['method']
    if 'id' not in request:
        return
    if method == 'initialize':
        time.sleep(float(os.environ.get('FAKE_INIT_DELAY', '0')))
        result = {'protocolVersion': '2025-11-25',
                  'serverInfo': {'name': 'keepsess', 'version': os.environ.get('FAKE_VERSION', '0.3.2+fixture')},
                  '_meta': {'instance_id': 'fixture', 'web': {'enabled': True, 'url': 'http://localhost/'}}}
    else:
        args = request['params']['arguments']
        command = args.get('command', '')
        if command == 'exit':
            os._exit(7)
        if command == 'malformed':
            with lock:
                sys.stdout.write('invalid-json\n')
                sys.stdout.flush()
            return
        if command.startswith('sleep:'):
            time.sleep(float(command.split(':')[1]))
        if command == 'stderr':
            sys.stderr.write('diagnostic' * 20000)
            sys.stderr.flush()
        if command == 'large':
            output = '\u4e2d\x1b[31m' * 60000
        else:
            output = json.dumps(args)
        data = {'session': 's1', 'output': output, 'exit_code': 8,
                'new_field': 'retained', 'peer': {'server': 'fixture', 'host_key': 'SHA256:fixture'}}
        if command == 'error':
            data.update(error='busy', input_state='not_sent', handle='task1')
        if request['params']['name'] == 'list':
            data = {'instance_id': 'fixture', 'sessions': [data], 'web': {'enabled': True}}
        result = {'structuredContent': data, 'isError': command == 'error'}
        if command == 'text-fallback':
            result = {'content': [{'type': 'text', 'text': json.dumps(data)}]}
    with lock:
        sys.stdout.write(json.dumps({'jsonrpc': '2.0', 'id': request['id'], 'result': result}) + '\n')
        sys.stdout.flush()


for line in sys.stdin:
    request = json.loads(line)
    if request['method'] == 'notifications/cancelled':
        sys.stderr.write('cancelled\n')
        sys.stderr.flush()
    else:
        if request.get('params', {}).get('arguments', {}).get('command') == 'stop-reading':
            respond(request)
            time.sleep(30)
        else:
            threading.Thread(target=respond, args=(request,), daemon=True).start()
