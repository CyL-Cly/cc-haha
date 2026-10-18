#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys
from urllib.parse import unquote, urlparse

receipt = Path(sys.argv[1])
private_root = Path(sys.argv[2]).resolve()
if not receipt.resolve().is_relative_to(private_root):
    raise RuntimeError('LSP receipt must be private')


def send(value):
    body = json.dumps(value, separators=(',', ':')).encode('utf-8')
    sys.stdout.buffer.write(b'Content-Length: ' + str(len(body)).encode() + b'\r\n\r\n' + body)
    sys.stdout.buffer.flush()


def held(path):
    if path:
        target = Path(path).resolve()
        if not target.is_relative_to(private_root):
            raise RuntimeError('LSP hold pipe must be private')
        with target.open('rb', buffering=0) as pipe:
            pipe.read(1)


while True:
    length = None
    while True:
        line = sys.stdin.buffer.readline()
        if not line:
            sys.exit(0)
        if line in (b'\r\n', b'\n'):
            break
        name, value = line.decode('ascii').strip().split(':', 1)
        if name.lower() == 'content-length':
            length = int(value.strip())
    if length is None or not 0 <= length <= 16 * 1024 * 1024:
        raise RuntimeError('Invalid private LSP frame')
    frame = json.loads(sys.stdin.buffer.read(length))
    method = frame.get('method')
    params = frame.get('params') or {}
    entry = dict(pid=os.getpid(), method=method, params=params)
    uri = params.get('textDocument', {}).get('uri') if isinstance(params, dict) else None
    if uri:
        document = Path(unquote(urlparse(uri).path)).resolve()
        if not document.is_relative_to(private_root):
            raise RuntimeError('LSP document must be private')
        entry['disk'] = document.read_bytes().decode('utf-8', errors='replace') if document.exists() else None
    with receipt.open('a', encoding='utf-8') as output:
        output.write(json.dumps(entry, separators=(',', ':')) + '\n')
    if method == os.environ.get('CC_HAHA_LSP_HOLD_METHOD'):
        held(os.environ.get('CC_HAHA_LSP_REQUEST_RELEASE'))
    if method == 'exit':
        break
    if method == 'initialize':
        held(os.environ.get('CC_HAHA_LSP_INITIALIZE_RELEASE'))
        result = {'capabilities': {'textDocumentSync': 1, 'definitionProvider': True, 'referencesProvider': True, 'hoverProvider': True, 'documentSymbolProvider': True, 'workspaceSymbolProvider': True, 'implementationProvider': True, 'callHierarchyProvider': True}}
    elif method == 'shutdown':
        held(os.environ.get('CC_HAHA_LSP_SHUTDOWN_RELEASE'))
        result = None
    elif method == '$/fixture/barrier':
        result = {'ready': True}
    else:
        result = json.loads(os.environ.get('CC_HAHA_LSP_RESULTS', '{}')).get(method)
    if method in ('textDocument/didOpen', 'textDocument/didChange') and os.environ.get('CC_HAHA_LSP_DIAGNOSTIC'):
        send({'jsonrpc': '2.0', 'method': 'textDocument/publishDiagnostics', 'params': {'uri': uri, 'diagnostics': json.loads(os.environ['CC_HAHA_LSP_DIAGNOSTIC'])}})
    if 'id' in frame:
        if method == os.environ.get('CC_HAHA_LSP_FAIL_METHOD'):
            send({'jsonrpc': '2.0', 'id': frame['id'], 'error': {'code': -32603, 'message': 'private actual LSP failure'}})
        else:
            send({'jsonrpc': '2.0', 'id': frame['id'], 'result': result})
