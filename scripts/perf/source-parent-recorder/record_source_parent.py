from pathlib import Path
import argparse
import hashlib
import json
import os
import stat
import subprocess
import sys
from credential_capability import key_metadata, run_validated_worker

OWNER = Path(__file__).resolve().parent
ROOT = OWNER.parents[2]
MARKER = 'SOURCE_LIVE_FULL_READY'
MAIN = '6d21316ec4635f29ecbaa43e1a5574bfd31d7155'

def sha(path):
    h = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            h.update(block)
    return h.hexdigest()

def regular(path, mode=None):
    path = Path(path)
    assert path.is_absolute() and path.resolve(strict=True) == path, 'Canonical absolute file required'
    s = path.lstat()
    assert stat.S_ISREG(s.st_mode) and s.st_uid == os.getuid(), 'Owned physical regular file required'
    if mode is not None:
        assert stat.S_IMODE(s.st_mode) == mode, 'Private file mode required'
    return path

def allocation_path(value):
    path = Path(value)
    assert path.is_absolute() and path.resolve(strict=True) == path, 'Canonical absolute allocation required'
    temporary_root = Path('/tmp').resolve(strict=True)
    assert path.is_relative_to(temporary_root) and path != temporary_root, 'Disposable canonical allocation required'
    s = path.lstat()
    assert stat.S_ISDIR(s.st_mode) and s.st_uid == os.getuid() and stat.S_IMODE(s.st_mode) == 0o700, 'Owned0700 allocation required'
    assert not any(path.iterdir()), 'Preserve existing parent-pilot allocation'
    return path

HELPERS = ['record_source_parent.py', 'credential_capability.py', 'record-source-parent.ts', 'formal-command.ts', 'formal-transport.ts']
NORMAL_SOURCES = ['scripts/perf/go-main-paired-benchmark.ts', 'scripts/perf/go-main-source-live.ts', 'scripts/perf/go-main-paired-benchmark-core.ts', 'scripts/perf/go-main-paired-benchmark-evidence.ts', 'scripts/quality-gate/modes.ts', '.agents/plans/go-main-paired-benchmark-protocol.md']

def verify_helpers(candidate):
    value = json.loads(regular(candidate).read_text())
    assert Path(value['root']).resolve(strict=True) == ROOT, 'Producer must belong to the final candidate root'
    proof = value['inputs']; inputs_path = regular(proof['path'])
    assert sha(inputs_path) == proof['sha256'], 'Candidate input proof changed'
    inputs = json.loads(inputs_path.read_text())
    names = [*NORMAL_SOURCES, *['scripts/perf/source-parent-recorder/' + name for name in HELPERS]]
    actual = {}
    for name in names:
        file = regular(ROOT / name); row = inputs['files'].get(name)
        assert isinstance(row, dict) and row.get('kind') == 'file' and row.get('sha256') == sha(file) and row.get('mode') == stat.S_IMODE(file.stat().st_mode), 'Producer/interface is outside the final candidate: ' + name
        actual[name] = row['sha256']
    runner = (ROOT / 'scripts/perf/go-main-paired-benchmark.ts').read_text()
    assert 'export ' + command_block(runner) in (OWNER / 'formal-command.ts').read_text()
    assert transport_block(runner) in (OWNER / 'formal-transport.ts').read_text()
    return {'helpers': {name: actual['scripts/perf/source-parent-recorder/' + name] for name in HELPERS}, 'normalSources': {name: actual[name] for name in NORMAL_SOURCES}}

def command_block(runner):
    return runner[runner.index('async function command('):runner.index('\nasync function main()')].strip() + '\n'

def transport_block(runner):
    start = runner.index('  const upstream = Bun.serve(')
    return runner[start:runner.index('\n  const initialParentHash', start)].strip() + '\n'

def verify_source(source):
    source = Path(source)
    assert source.is_absolute() and source.resolve(strict=True) == source and source.is_dir()
    listing = subprocess.check_output(['git', 'ls-tree', '-rz', MAIN], cwd=ROOT)
    proofs = []
    for entry in listing.split(b'\0'):
        if not entry:
            continue
        header, name = entry.decode().split('\t', 1)
        mode, kind, expected = header.split()
        assert kind == 'blob' and mode in ['100644', '100755', '120000']
        path = source / name
        assert path.parent.resolve().is_relative_to(source), 'Source path escapes'
        if mode == '120000':
            assert path.is_symlink() and path.resolve().is_relative_to(source)
            data = os.readlink(path).encode()
        else:
            assert stat.S_ISREG(path.lstat().st_mode)
            data = path.read_bytes()
        actual = hashlib.sha1(('blob ' + str(len(data)) + '\0').encode() + data).hexdigest()
        assert actual == expected, 'Frozen MAIN blob mismatch: ' + name
        proofs.append({'path': name, 'gitBlob': expected, 'sha256': hashlib.sha256(data).hexdigest()})
    expected_production = {p['path'] for p in proofs if p['path'].startswith(('src/', 'adapters/', 'stubs/')) or p['path'] in ['preload.ts', 'package.json', 'bun.lock', 'tsconfig.json']}
    actual_production = set()
    for prefix in ['src', 'adapters', 'stubs']:
        for directory, dirs, files in os.walk(source / prefix, followlinks=False):
            dirs[:] = [d for d in dirs if d not in ['node_modules', '.git']]
            for name in dirs + files:
                p = Path(directory) / name
                assert not p.is_symlink(), 'Unexpected production Source symlink'
            actual_production.update(str((Path(directory) / name).relative_to(source)) for name in files)
    actual_production.update(['preload.ts', 'package.json', 'bun.lock', 'tsconfig.json'])
    assert expected_production == actual_production, 'Missing or extra frozen MAIN production inputs'
    return {'main': MAIN, 'root': str(source), 'files': proofs, 'hash': hashlib.sha256(json.dumps(proofs, separators=(',', ':')).encode()).hexdigest()}

def copy_exclusive_bytes(source, target, secret, expected_sha256=None):
    regular(source)
    data = source.read_bytes()
    assert secret and secret not in data, 'Never freeze credential-bearing bytes'
    if expected_sha256 is not None:
        assert hashlib.sha256(data).hexdigest() == expected_sha256, 'Validated source bytes changed before freeze'
    with target.open('xb') as stream:
        os.chmod(target, 0o600)
        stream.write(data)
        stream.flush(); os.fsync(stream.fileno())
    assert source.read_bytes() == data and target.read_bytes() == data
    return {'sha256': sha(target), 'bytes': len(data), 'originalAndCopyEqual': True}

def audit_transport(allocation, secret, state):
    evidence = allocation / 'transport'
    rows = [json.loads(line) for line in (evidence / 'transport.jsonl').read_text().splitlines() if line.strip()]
    requests = [json.loads(line) for line in (evidence / 'requests.jsonl').read_text().splitlines() if line.strip()]
    assert len(rows) == state['calls'] and [r['number'] for r in rows] == list(range(1, len(rows) + 1))
    assert len(requests) == state['modelCalls'] and len({r['number'] for r in requests}) == len(requests)
    head_errors = list(state.get('transportErrors', []))
    assert all(isinstance(error, str) for error in head_errors)
    for row in rows:
        number = row['number']; response_path = evidence / ('response-' + str(number) + '.json')
        response_bytes = response_path.read_bytes()
        assert secret not in response_bytes
        response = json.loads(response_bytes)
        assert row.get('upstreamURL') == 'https://api.deepseek.com' + row['pathname'] + row['query'], 'Actual transport must target the official endpoint'
        assert response['method'] == row['method']
        if row['method'] == 'HEAD':
            assert row['pathname'] == '/anthropic' and response['bytes'] == 0 and response['sha256'] == hashlib.sha256(b'').hexdigest(), 'Actual preconnect evidence mismatch'
            if response.get('complete') is True:
                assert not response.get('error') and isinstance(response.get('status'), int) and 100 <= response['status'] <= 599
            else:
                error = response.get('error')
                assert response.get('complete') is False and response.get('status') == 0 and isinstance(error, str) and error and error in head_errors, 'Failed preconnect diagnostic is unbound'
                head_errors.remove(error)
            continue
        assert response.get('complete') is True and not response.get('error')
        assert row['method'] == 'POST' and row['pathname'] == '/anthropic/v1/messages', 'Unexpected parent-pilot auxiliary transport requires diagnosis'
        assert 200 <= response['status'] < 300, 'Real model transport must succeed'
        body = (evidence / ('response-' + str(number) + '.txt')).read_bytes()
        assert secret not in body and response['bytes'] == len(body) and response['sha256'] == hashlib.sha256(body).hexdigest()
        matched = [r for r in requests if r['number'] == number]
        assert len(matched) == 1
        request = matched[0]; raw = (evidence / ('request-' + str(number) + '.txt')).read_bytes()
        assert secret not in raw and request['bodyHash'] == hashlib.sha256(raw).hexdigest()
        assert json.loads(raw) == request['body'] and request['body']['model'] == 'deepseek-flash'
    return {'calls': len(rows), 'modelCalls': len(requests), 'completeNonHeadResponsesVerified': True, 'rawBodyHashesVerified': True}

def freeze_parent(allocation, secret):
    receipt = json.loads((allocation / 'pilot-process.json').read_text())
    p = receipt['process']; transport = receipt['transport']
    assert receipt['scope'] == 'real-Source-unscored-parent-pilot-not-120-benchmark'
    assert p['code'] == 0 and not p['timedOut'] and p['joined'] and p['stdoutComplete'] and p['stderrComplete'] and not p['recordingErrors']
    assert not receipt.get('failure') and not receipt['outputRedactions']
    assert transport['modelCalls'] >= 1 and not transport['errors'] and not transport['recordingErrors'] and not transport['redactions']
    audit_transport(allocation, secret, transport)
    stdout = (allocation / 'stdout.jsonl').read_bytes()
    assert secret not in stdout and secret not in (allocation / 'stderr.log').read_bytes()
    events = [json.loads(line) for line in stdout.splitlines() if line.strip()]
    terminals = [row for row in events if row.get('type') == 'result']
    assert len(terminals) == 1 and events[-1] == terminals[0], 'Unique final public result required'
    result = terminals[0]
    assert result.get('subtype') == 'success' and result.get('is_error') is False
    sid = result.get('session_id')
    assert isinstance(sid, str) and sid and '/' not in sid and '\\' not in sid and '\x00' not in sid
    assert result.get('result', '').strip() == MARKER, 'Actual complete Source reply must match frozen history judge'
    home = allocation / 'source-home'
    projects = home / '.claude/projects'
    candidates = []
    for directory, dirs, files in os.walk(projects, followlinks=False):
        dirs[:] = [d for d in dirs if not (Path(directory) / d).is_symlink()]
        if sid + '.jsonl' in files:
            path = Path(directory) / (sid + '.jsonl')
            regular(path)
            assert path.resolve().is_relative_to(projects)
            candidates.append(path)
    assert len(candidates) == 1, 'Public SID must identify exactly one physical Source transcript'
    parent = candidates[0]; data = parent.read_bytes()
    assert secret not in data, 'Never freeze a credential-bearing transcript'
    records = [json.loads(line) for line in data.splitlines() if line.strip()]
    assert any(r.get('type') == 'user' for r in records)
    assert all(r.get('sessionId') == sid for r in records if r.get('type') in ['user', 'assistant']), 'Physical Source history SID mismatch'
    assistants = [r for r in records if r.get('type') == 'assistant']
    assert assistants, 'Real assistant history required'
    content = assistants[-1].get('message', {}).get('content')
    text = content if isinstance(content, str) else ''.join(x.get('text', '') for x in content if x.get('type') == 'text') if isinstance(content, list) else ''
    assert text.strip() == MARKER, 'Real transcript assistant must match frozen judge'
    frozen = allocation / 'frozen-parent.jsonl'
    copied = copy_exclusive_bytes(parent, frozen, secret, hashlib.sha256(data).hexdigest())
    return {'sourcePath': str(parent), 'frozenPath': str(frozen), **copied, 'sessionId': sid, 'records': len(records), 'realSourceHistory': True}

def main():
    parser = argparse.ArgumentParser()
    for name in ['allocation', 'source-root', 'go-binary', 'candidate-evidence', 'bun', 'key-file']:
        parser.add_argument('--' + name, required=True)
    parser.add_argument('--allow-live', action='store_true')
    args = parser.parse_args()
    assert args.allow_live, 'Explicit --allow-live required; no credential read or model call performed'
    allocation = allocation_path(args.allocation)
    key_file = regular(args.key_file, 0o600)
    key_identity = key_metadata(key_file)
    assert not key_file.is_relative_to(allocation), 'Keep credential outside model allocation'
    binary = regular(args.go_binary); bun = regular(args.bun); candidate = regular(args.candidate_evidence)
    plan = verify_helpers(candidate)
    source = verify_source(args.source_root)
    manifest = {'scope': 'unscored-real-source-parent-preparation', 'source': source, 'helpers': plan['helpers'], 'binarySHA256': sha(binary), 'bunSHA256': sha(bun), 'candidateEvidenceSHA256': sha(candidate)}
    with (allocation / 'source-inputs.json').open('x') as stream:
        os.chmod(stream.name, 0o600); json.dump(manifest, stream, indent=2); stream.write('\n')
    bootstrap = allocation / 'driver-home'; bootstrap.mkdir(mode=0o700)
    for name in ['tmp', 'config', 'cache']: (bootstrap / name).mkdir(mode=0o700)
    env = {'HOME': str(bootstrap), 'USERPROFILE': str(bootstrap), 'CLAUDE_CONFIG_DIR': str(bootstrap / '.claude'), 'TMPDIR': str(bootstrap / 'tmp'), 'CLAUDE_CODE_TMPDIR': str(bootstrap / 'tmp'), 'TMP': str(bootstrap / 'tmp'), 'TEMP': str(bootstrap / 'tmp'), 'XDG_CONFIG_HOME': str(bootstrap / 'config'), 'XDG_CACHE_HOME': str(bootstrap / 'cache'), 'CFFIXED_USER_HOME': str(bootstrap), 'PATH': str(bun.parent) + ':/opt/homebrew/bin:/usr/bin:/bin', 'LANG': 'C.UTF-8', 'LC_ALL': 'C.UTF-8', 'TZ': 'UTC', 'BUN_OPTIONS': '--no-env-file'}
    command = [str(bun), '--no-env-file', str(OWNER / 'record-source-parent.ts'), '--allocation', str(allocation), '--source-root', args.source_root, '--go-binary', str(binary), '--candidate-evidence', str(candidate), '--allow-live']
    # One worker validates the candidate, then signals READY through an
    # anonymous pipe. Only then may Python read one credential capability.
    child = run_validated_worker(command, allocation, env, key_file, key_identity)
    capability = child['capability']
    try:
        with (allocation / 'worker-supervision.json').open('x') as stream:
            os.chmod(stream.name, 0o600)
            json.dump({key: child[key] for key in ['code', 'credentialAdmitted', 'credentialStable', 'outputRedactions', 'failure']}, stream, indent=2)
            stream.write('\n')
        if child['output'] is not None:
            with (allocation / 'driver.log').open('xb') as stream:
                os.chmod(stream.name, 0o600); stream.write(child['output'])
        assert child['credentialAdmitted'], 'Candidate preflight failed; credential not read and child output not persisted'
        assert child['code'] == 0 and not child['failure'], 'Source pilot or credential audit failed; sanitized evidence retained, do not reuse allocation'
        capability.assert_unchanged()
        secret = capability.secret
        assert verify_helpers(candidate) == plan, 'Final candidate producer/interface changed during pilot'
        assert verify_source(args.source_root) == source, 'Frozen Source changed during pilot'
        assert sha(binary) == manifest['binarySHA256'] and sha(bun) == manifest['bunSHA256'] and sha(candidate) == manifest['candidateEvidenceSHA256']
        capability.assert_unchanged()
        frozen = freeze_parent(allocation, secret)
        capability.assert_unchanged()
        def file_proof(path):
            regular(path, 0o600)
            assert secret not in path.read_bytes(), 'Never seal credential-bearing evidence'
            return {'path': str(path), 'sha256': sha(path)}
        transport_files = [file_proof(path) for path in sorted((allocation / 'transport').iterdir())]
        proof = {'version': 2, 'status': 'frozen-real-source-parent-not-formal120', 'parent': frozen,
                 'inputs': file_proof(allocation / 'source-inputs.json'), 'pilot': file_proof(allocation / 'pilot-process.json'),
                 'supervision': file_proof(allocation / 'worker-supervision.json'), 'stdout': file_proof(allocation / 'stdout.jsonl'),
                 'stderr': file_proof(allocation / 'stderr.log'), 'driverLog': file_proof(allocation / 'driver.log'),
                 'helpers': [{'name': name, 'path': str(OWNER / name), 'sha256': sha(OWNER / name)} for name in HELPERS],
                 'transportFiles': transport_files,
                 'qualification': 'Unscored Source history only; final runner revalidates current SID/raw/FD/candidate identities. Not a signed attestation or formal pair.'}
        with (allocation / 'frozen-parent-receipt.json').open('x') as stream:
            os.chmod(stream.name, 0o600); json.dump(proof, stream, indent=2); stream.write('\n')
        print(json.dumps({'status': proof['status'], 'parent': frozen['frozenPath'], 'sha256': frozen['sha256']}))
    finally:
        if capability is not None:
            capability.close()

if __name__ == '__main__':
    main()
