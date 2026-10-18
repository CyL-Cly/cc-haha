from pathlib import Path
import hashlib
import importlib.util
import json
import os
import stat
import signal
import sys
from unittest import mock
import credential_capability as credentials
import tempfile
import unittest

owner = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('parent_driver', owner / 'record_source_parent.py')
driver = importlib.util.module_from_spec(spec)
spec.loader.exec_module(driver)

class OfflinePrimitiveTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='cc-haha-parent-driver-offline-', dir=Path('/tmp').resolve(strict=True))
        self.root = Path(self.temp.name).resolve()
        os.chmod(self.root, 0o700)

    def tearDown(self):
        self.temp.cleanup()

    def test_allocation_requires_empty_owned_private_canonical_directory(self):
        self.assertEqual(driver.allocation_path(str(self.root)), self.root)
        # A controlled canonical /tmp namespace models the Linux root without
        # creating global directories or weakening the actual owner/mode checks.
        canonical_tmp = self.root / 'canonical-tmp'; canonical_tmp.mkdir(mode=0o700)
        inside = canonical_tmp / 'inside'; inside.mkdir(mode=0o700)
        outside = self.root / 'outside-current-tmp'; outside.mkdir(mode=0o700)
        original_path = driver.Path
        def current_os_path(value):
            return canonical_tmp if str(value) == '/tmp' else original_path(value)
        with mock.patch.object(driver, 'Path', side_effect=current_os_path):
            self.assertEqual(driver.allocation_path(str(inside)), inside)
            with self.assertRaisesRegex(AssertionError, 'Disposable canonical allocation'):
                driver.allocation_path(str(outside))
            with self.assertRaisesRegex(AssertionError, 'Disposable canonical allocation'):
                driver.allocation_path(str(canonical_tmp))
        (self.root / 'existing-evidence.txt').write_text('preserve')
        with self.assertRaisesRegex(AssertionError, 'Preserve existing'):
            driver.allocation_path(str(self.root))

    def test_allocation_rejects_group_permissions(self):
        os.chmod(self.root, 0o750)
        with self.assertRaisesRegex(AssertionError, 'Owned0700'):
            driver.allocation_path(str(self.root))

    def test_regular_rejects_symlink_and_nonprivate_mode(self):
        source = self.root / 'owned.bin'; source.write_bytes(b'offline-fixture-only')
        os.chmod(source, 0o600)
        self.assertEqual(driver.regular(source, 0o600), source)
        alias = self.root / 'alias.bin'; alias.symlink_to(source)
        with self.assertRaises(AssertionError): driver.regular(alias, 0o600)
        os.chmod(source, 0o644)
        with self.assertRaises(AssertionError): driver.regular(source, 0o600)

    def test_exclusive_copy_preserves_source_hash_mode_and_bytes(self):
        # Arbitrary primitive fixture bytes; never manufacture a parent JSONL.
        source = self.root / 'fixture.bin'; source.write_bytes(b'primitive-copy-fixture\x00\xff\n')
        os.chmod(source, 0o600)
        before = source.read_bytes(); target = self.root / 'copy.bin'
        proof = driver.copy_exclusive_bytes(source, target, b'fake-private-secret')
        self.assertEqual(source.read_bytes(), before)
        self.assertEqual(target.read_bytes(), before)
        self.assertEqual(proof['sha256'], hashlib.sha256(before).hexdigest())
        self.assertEqual(proof['bytes'], len(before))
        self.assertEqual(stat.S_IMODE(target.stat().st_mode), 0o600)
        with self.assertRaises(FileExistsError): driver.copy_exclusive_bytes(source, target, b'fake-private-secret')
        self.assertEqual(target.read_bytes(), before)

    def test_secret_bearing_bytes_are_rejected_before_destination_creation(self):
        source = self.root / 'fixture.bin'; source.write_bytes(b'before fake-private-secret after')
        target = self.root / 'copy.bin'
        with self.assertRaisesRegex(AssertionError, 'credential-bearing'):
            driver.copy_exclusive_bytes(source, target, b'fake-private-secret')
        self.assertFalse(target.exists())

    def test_changed_validated_bytes_are_rejected_before_destination_creation(self):
        source = self.root / 'fixture.bin'; source.write_bytes(b'changed-fixture')
        target = self.root / 'copy.bin'
        with self.assertRaisesRegex(AssertionError, 'Validated source bytes changed'):
            driver.copy_exclusive_bytes(source, target, b'fake-private-secret', hashlib.sha256(b'original-fixture').hexdigest())
        self.assertFalse(target.exists())

    def test_static_driver_never_logs_full_environment_or_embeds_secret_in_argv(self):
        text = (owner / 'record-source-parent.ts').read_text()
        argv = text[text.index('const argv ='):text.index('let result:')]
        self.assertNotIn('key', argv)
        self.assertNotIn('JSON.stringify(env', text)
        self.assertNotIn('keyFile', text)
        self.assertNotIn('--key-file', text)
        self.assertLess(text.index('verifyCandidateEvidence(candidateFile'), text.index("writeFileSync(readyFd, 'SOURCE_PARENT_VALIDATED_V2"))
        self.assertLess(text.index('freezeResolvedDependencies(source)'), text.index("writeFileSync(readyFd, 'SOURCE_PARENT_VALIDATED_V2"))
        self.assertLess(text.index("writeFileSync(readyFd, 'SOURCE_PARENT_VALIDATED_V2"), text.index("const key = readFileSync(credentialFd"))
        self.assertLess(text.index('closeSync(credentialFd)'), text.index('const transport = startParentTransport'))
        self.assertIn("const redact = (text: string) => text.replaceAll(key, '<redacted-key>')", text)
        self.assertIn('state.redactions || outputRedactions', text)

    def test_exact_original_command_and_transport_blocks_are_preserved(self):
        runner = (driver.ROOT / 'scripts/perf/go-main-paired-benchmark.ts').read_text()
        command = (owner / 'formal-command.ts').read_text()
        transport = (owner / 'formal-transport.ts').read_text()
        self.assertIn('export ' + driver.command_block(runner), command)
        self.assertIn(driver.transport_block(runner), transport)
        self.assertIn('https://api.deepseek.com', transport)
        self.assertIn('state.redactions++', transport)
        self.assertNotIn('writeFileSync(path, data', transport)

class ReproducibleProducerTests(unittest.TestCase):
    setUp = OfflinePrimitiveTests.setUp
    tearDown = OfflinePrimitiveTests.tearDown

    def test_normal_candidate_snapshot_authorizes_helper_bytes_without_preparation_json(self):
        root = self.root / 'candidate'; producer = root / 'scripts/perf/source-parent-recorder'
        producer.mkdir(parents=True)
        files = {}
        names = [*driver.NORMAL_SOURCES, *['scripts/perf/source-parent-recorder/' + name for name in driver.HELPERS]]
        for name in names:
            target = root / name; target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes((driver.ROOT / name).read_bytes()); target.chmod(0o600)
            files[name] = {'kind': 'file', 'mode': 0o600, 'sha256': driver.sha(target)}
        inputs = self.root / 'candidate-inputs.json'; inputs.write_text(json.dumps({'files': files})); inputs.chmod(0o600)
        candidate = self.root / 'candidate.json'; candidate.write_text(json.dumps({'root': str(root), 'inputs': {'path': str(inputs), 'sha256': driver.sha(inputs)}})); candidate.chmod(0o600)
        with mock.patch.object(driver, 'ROOT', root), mock.patch.object(driver, 'OWNER', producer):
            observed = driver.verify_helpers(candidate)
            self.assertEqual(set(observed['helpers']), set(driver.HELPERS))
            self.assertFalse((producer / 'preparation.json').exists())
            (producer / 'record-source-parent.ts').write_text('changed producer byte')
            with self.assertRaisesRegex(AssertionError, 'outside the final candidate'):
                driver.verify_helpers(candidate)

    def synthetic_parent(self, wrong_sid=False):
        # Synthetic protocol fixture only. Never used as real Source or benchmark evidence.
        import json
        sid = '46464646-4646-4646-8646-464646464646'
        evidence = self.root / 'transport'; evidence.mkdir(mode=0o700)
        body = json.dumps({'model': 'deepseek-flash'})
        request = {'number': 1, 'method': 'POST', 'pathname': '/anthropic/v1/messages', 'query': '', 'bodyHash': hashlib.sha256(body.encode()).hexdigest(), 'body': json.loads(body)}
        raw = b'synthetic complete response bytes, no live provider'
        (evidence / 'requests.jsonl').write_text(json.dumps(request) + '\n')
        (evidence / 'transport.jsonl').write_text(json.dumps({'number': 1, 'method': 'POST', 'pathname': '/anthropic/v1/messages', 'query': '', 'upstreamURL': 'https://api.deepseek.com/anthropic/v1/messages'}) + '\n')
        (evidence / 'request-1.txt').write_text(body); (evidence / 'response-1.txt').write_bytes(raw)
        (evidence / 'response-1.json').write_text(json.dumps({'method': 'POST', 'complete': True, 'status': 200, 'bytes': len(raw), 'sha256': hashlib.sha256(raw).hexdigest()}))
        process = {'code': 0, 'timedOut': False, 'joined': True, 'stdoutComplete': True, 'stderrComplete': True, 'recordingErrors': []}
        (self.root / 'pilot-process.json').write_text(json.dumps({'scope': 'real-Source-unscored-parent-pilot-not-120-benchmark', 'process': process, 'transport': {'calls': 1, 'modelCalls': 1, 'errors': [], 'recordingErrors': [], 'redactions': 0}, 'outputRedactions': 0}))
        (self.root / 'stdout.jsonl').write_text(json.dumps({'type': 'result', 'subtype': 'success', 'is_error': False, 'session_id': sid, 'result': driver.MARKER}) + '\n')
        (self.root / 'stderr.log').write_text('')
        source = self.root / 'source-home/.claude/projects/synthetic-project' / (sid + '.jsonl'); source.parent.mkdir(parents=True)
        source.write_text(json.dumps({'type': 'user', 'sessionId': 'wrong-sid' if wrong_sid else sid, 'message': {'role': 'user', 'content': 'synthetic'}}) + '\n' + json.dumps({'type': 'assistant', 'sessionId': sid, 'message': {'content': [{'type': 'text', 'text': driver.MARKER}]}}) + '\n')
        return source

    def test_successful_fixture_preserves_full_original_bytes_and_exclusive_frozen_copy(self):
        source = self.synthetic_parent(); before = source.read_bytes()
        actual = driver.freeze_parent(self.root, b'explicit-fake-token')
        self.assertEqual(actual['sessionId'], '46464646-4646-4646-8646-464646464646')
        self.assertEqual(source.read_bytes(), before)
        self.assertEqual(Path(actual['frozenPath']).read_bytes(), before)
        self.assertEqual(stat.S_IMODE(Path(actual['frozenPath']).stat().st_mode), 0o600)

    def test_same_marker_and_file_name_cannot_hide_a_different_physical_user_sid(self):
        source = self.synthetic_parent(wrong_sid=True); before = source.read_bytes()
        with self.assertRaisesRegex(AssertionError, 'history SID mismatch'):
            driver.freeze_parent(self.root, b'explicit-fake-token')
        self.assertEqual(source.read_bytes(), before)
        self.assertFalse((self.root / 'frozen-parent.jsonl').exists())

    def test_actual_failed_head_fetch_keeps_successful_post_and_original_failure(self):
        self.synthetic_parent()
        error = 'Upstream fetch failed: synthetic HEAD connection refused'
        transport = self.root / 'transport'
        with (transport / 'transport.jsonl').open('a') as stream:
            stream.write(json.dumps({'number': 2, 'method': 'HEAD', 'pathname': '/anthropic', 'query': '', 'upstreamURL': 'https://api.deepseek.com/anthropic'}) + '\n')
        failure = {'method': 'HEAD', 'status': 0, 'complete': False, 'error': error, 'bytes': 0, 'sha256': hashlib.sha256(b'').hexdigest()}
        physical = transport / 'response-2.json'; physical.write_text(json.dumps(failure)); before = physical.read_bytes()
        pilot_path = self.root / 'pilot-process.json'; pilot = json.loads(pilot_path.read_text())
        pilot['transport']['calls'] = 2; pilot['transport']['transportErrors'] = [error]
        pilot_path.write_text(json.dumps(pilot))
        self.assertTrue(driver.freeze_parent(self.root, b'explicit-fake-token')['realSourceHistory'])
        self.assertEqual(physical.read_bytes(), before)
        failure['error'] = 'unbound different failure'; physical.write_text(json.dumps(failure))
        with self.assertRaisesRegex(AssertionError, 'diagnostic is unbound'):
            driver.audit_transport(self.root, b'explicit-fake-token', pilot['transport'])

    def test_parent_freeze_rejects_redirected_actual_transport_url(self):
        self.synthetic_parent()
        (self.root / 'transport/transport.jsonl').write_text(json.dumps({'number': 1, 'method': 'POST', 'pathname': '/anthropic/v1/messages', 'query': '', 'upstreamURL': 'https://not-deepseek.invalid/anthropic/v1/messages'}) + '\n')
        with self.assertRaisesRegex(AssertionError, 'official endpoint'):
            driver.freeze_parent(self.root, b'explicit-fake-token')
        self.assertFalse((self.root / 'frozen-parent.jsonl').exists())


FAKE_WORKER = r'''
import os, sys, time
from pathlib import Path
args = sys.argv
fd = lambda name: int(args[args.index(name) + 1])
ready, credential = fd('--ready-fd'), fd('--credential-fd')
mode, path = args[1], Path(args[2])
if mode == 'fail':
    os.write(1, b'preflight output deliberately not persisted\n')
    raise SystemExit(7)
if mode == 'spoof':
    os.write(1, b'SOURCE_PARENT_VALIDATED_V2\n')
    raise SystemExit(7)
if mode == 'invalid-ready':
    os.write(ready, b'INVALID\n'); os.close(ready)
    raise SystemExit(7)
if mode == 'timeout':
    time.sleep(10)
    raise SystemExit(7)
if mode == 'close-credential':
    os.close(credential)
os.write(ready, b'SOURCE_PARENT_')
time.sleep(0.01)
os.write(ready, b'VALIDATED_V2\n'); os.close(ready)
if mode == 'close-credential':
    time.sleep(0.1)
    raise SystemExit(9)
key = bytearray()
while True:
    data = os.read(credential, 100)
    if not data: break
    key.extend(data)
os.close(credential)
if not key: raise SystemExit(8)
action = mode.removesuffix('-echo')
if action == 'replace':
    other = path.with_name('replacement.key')
    other.write_bytes(b'fake-new-key-only-for-fixture'); other.chmod(0o600)
    other.replace(path)
if action == 'rewrite':
    path.write_bytes(b'fake-new-key-only-for-fixture')
if action == 'chmod':
    path.chmod(0o644)
if mode == 'runtime-interrupt': time.sleep(0.1)
if mode in ['success-clean', 'replace', 'rewrite', 'chmod']:
    os.write(1, b'fixture worker complete\n')
    raise SystemExit(0)
# Echo the old actual capability in separate writes, proving redaction uses K1.
os.write(1, b'before ' + key[:5]); os.write(1, key[5:] + b' after\n')
'''


class CredentialCapabilityTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='cc-haha-parent-v2-credential-', dir=Path('/tmp').resolve(strict=True))
        self.root = Path(self.temp.name).resolve()
        os.chmod(self.root, 0o700)
        self.path = self.root / 'fixture.key'
        self.key = b'fake-old-key-only-for-fixture'
        self.path.write_bytes(self.key + b'\n'); self.path.chmod(0o600)
        self.identity = credentials.key_metadata(self.path)
        self.env = {'HOME': str(self.root), 'USERPROFILE': str(self.root), 'TMPDIR': str(self.root), 'PATH': '/usr/bin:/bin', 'PYTHONDONTWRITEBYTECODE': '1'}
        self.results = []

    def tearDown(self):
        for result in self.results:
            if result['capability'] is not None: result['capability'].close()
        self.temp.cleanup()

    def run_worker(self, mode, **options):
        result = credentials.run_validated_worker([sys.executable, '-B', '-c', FAKE_WORKER, mode, str(self.path)], self.root, self.env, self.path, self.identity, **options)
        self.results.append(result)
        return result

    def test_preflight_failure_invalid_frame_and_stdout_spoof_never_read_key_or_return_raw(self):
        for mode in ['fail', 'spoof', 'invalid-ready']:
            with self.subTest(mode=mode), mock.patch.object(credentials, 'Credential', side_effect=AssertionError('Key bytes must remain unread')) as opener:
                with self.assertRaisesRegex(RuntimeError, 'no unsanitized output'):
                    self.run_worker(mode)
                opener.assert_not_called()
        self.assertEqual(list(self.root.iterdir()), [self.path])

    def test_preflight_timeout_joins_worker_without_reading_key(self):
        with mock.patch.object(credentials, 'Credential', side_effect=AssertionError('Key bytes must remain unread')) as opener:
            with self.assertRaisesRegex(RuntimeError, 'no unsanitized output'):
                self.run_worker('timeout', preflight_timeout=0.05)
            opener.assert_not_called()

    def test_split_ready_frame_reads_one_capability_and_secret_pipe_reaches_eof(self):
        with mock.patch.object(credentials, 'Credential', wraps=credentials.Credential) as opener:
            result = self.run_worker('success-clean')
            self.assertEqual(opener.call_count, 1)
        self.assertEqual(result['code'], 0)
        self.assertTrue(result['credentialAdmitted'])
        self.assertIsNone(result['failure'])
        self.assertIs(result['credentialStable'], True)
        self.assertEqual(result['output'], b'fixture worker complete\n')
        self.assertEqual(result['outputRedactions'], 0)
        self.assertEqual(result['capability'].secret, self.key)
        self.assertEqual(list(self.root.iterdir()), [self.path])

    def test_split_k1_driver_output_is_redacted_and_never_qualifies_for_freeze(self):
        result = self.run_worker('echo-key')
        self.assertEqual(result['code'], 0)
        self.assertEqual(result['output'], b'before <redacted-key> after\n')
        self.assertEqual(result['outputRedactions'], 1)
        self.assertIn('freezing forbidden', result['failure'])

    def test_key_rotations_fail_closed_and_old_k1_is_still_redacted(self):
        for mode in ['replace', 'rewrite', 'chmod']:
            with self.subTest(mode=mode):
                # Each subcase gets a new preflight identity; never use its new
                # credential to redact an earlier worker's output.
                self.path.write_bytes(self.key + b'\n'); self.path.chmod(0o600)
                self.identity = credentials.key_metadata(self.path)
                result = self.run_worker(mode)
                self.assertEqual(result['code'], 0)
                self.assertIn('Credential file changed', result['failure'])
                self.assertIs(result['credentialStable'], False)
                self.assertEqual(result['outputRedactions'], 0)
                self.assertEqual(result['output'], b'fixture worker complete\n')
                self.assertNotIn(self.key, result['output'])
                self.assertNotIn(b'fake-new-key', result['output'])
                self.assertEqual(result['capability'].secret, self.key)

    def test_rotation_with_echo_checks_drift_and_still_redacts_old_k1(self):
        result = self.run_worker('replace-echo')
        self.assertIs(result['credentialStable'], False)
        self.assertEqual(result['outputRedactions'], 1)
        self.assertIn('Credential file changed', result['failure'])
        self.assertIn('Worker output contained credential', result['failure'])
        self.assertEqual(result['output'], b'before <redacted-key> after\n')
        self.assertEqual(result['capability'].secret, self.key)

    def test_drift_before_ready_reads_no_capability_bytes(self):
        self.path.write_bytes(b'fake-changed-before-admission'); self.path.chmod(0o600)
        with self.assertRaisesRegex(RuntimeError, 'no unsanitized output'):
            self.run_worker('success')

    def test_symlink_fifo_and_world_readable_files_rejected_without_open(self):
        self.path.unlink()
        other = self.root / 'other'; other.write_bytes(b'fake-only'); other.chmod(0o600)
        self.path.symlink_to(other)
        with self.assertRaises(AssertionError): credentials.key_metadata(self.path)
        self.path.unlink(); os.mkfifo(self.path, 0o600)
        with self.assertRaises(AssertionError): credentials.key_metadata(self.path)
        self.path.unlink(); self.path.write_bytes(self.key); self.path.chmod(0o644)
        with self.assertRaises(AssertionError): credentials.key_metadata(self.path)

    def test_regular_to_fifo_race_is_rejected_without_blocking_or_reading_bytes(self):
        real_open = os.open
        def raced_open(path, flags):
            self.path.unlink(); os.mkfifo(self.path, 0o600)
            return real_open(path, flags)
        def timeout(*_): raise TimeoutError('FIFO open blocked before descriptor identity check')
        previous = signal.signal(signal.SIGALRM, timeout)
        signal.setitimer(signal.ITIMER_REAL, 0.5)
        try:
            with mock.patch.object(credentials.os, 'open', side_effect=raced_open):
                with self.assertRaisesRegex(AssertionError, 'changed during admission'):
                    credentials.Credential(self.path, self.identity)
        finally:
            signal.setitimer(signal.ITIMER_REAL, 0)
            signal.signal(signal.SIGALRM, previous)

    def test_closed_delivery_pipe_exits_without_freeze_or_unsanitized_output(self):
        result = self.run_worker('close-credential')
        self.assertNotEqual(result['code'], 0)
        self.assertIn('freezing forbidden', result['failure'])
        self.assertNotIn(self.key, result['output'])

    def test_post_admission_supervisor_error_drains_instead_of_killing_detached_owner(self):
        real_select = credentials.selectors.DefaultSelector
        admitted = False
        original_credential = credentials.Credential
        def acquire(*args):
            nonlocal admitted
            value = original_credential(*args)
            admitted = True
            return value
        class OneInterruption:
            def __init__(self): self.inner = real_select(); self.raised = False
            def register(self, *args): return self.inner.register(*args)
            def unregister(self, *args): return self.inner.unregister(*args)
            def get_map(self): return self.inner.get_map()
            def close(self): self.inner.close()
            def select(self, *args, **kwargs):
                if admitted and not self.raised:
                    self.raised = True
                    raise RuntimeError('fixture supervisor interruption')
                return self.inner.select(*args, **kwargs)
        with mock.patch.object(credentials.selectors, 'DefaultSelector', OneInterruption), mock.patch.object(credentials, 'Credential', acquire), mock.patch.object(credentials.os, 'killpg', side_effect=AssertionError('Do not kill admitted owner')) as killer:
            result = self.run_worker('runtime-interrupt')
            killer.assert_not_called()
        self.assertEqual(result['code'], 0)
        self.assertIn('freezing forbidden', result['failure'])
        self.assertEqual(result['output'], b'before <redacted-key> after\n')


if __name__ == '__main__':
    unittest.main()
