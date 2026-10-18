"""One credential capability, admitted only after the worker's validated READY.

No credential bytes, hashes or descriptor numbers are serialized in evidence.
The worker receives bytes through an anonymous pipe, never a key pathname.
"""
from pathlib import Path
import os
import selectors
import signal
import stat
import subprocess
import time

READY = b'SOURCE_PARENT_VALIDATED_V2\n'
MAX_KEY_BYTES = 4096


def identity(info):
    return (info.st_dev, info.st_ino, info.st_mode, info.st_uid, info.st_gid,
            info.st_size, info.st_mtime_ns, info.st_ctime_ns)


def key_metadata(path):
    path = Path(path)
    assert path.is_absolute() and path.resolve(strict=True) == path, 'Canonical key file required'
    info = path.lstat()
    assert stat.S_ISREG(info.st_mode) and info.st_uid == os.getuid() and stat.S_IMODE(info.st_mode) == 0o600, 'Owned regular0600 key file required'
    assert 0 < info.st_size <= MAX_KEY_BYTES, 'Raw token file size invalid'
    return identity(info)


class Credential:
    def __init__(self, path, expected_identity):
        self.path = Path(path)
        self.fd = None
        self.secret = None
        self.raw = None
        self.expected_identity = expected_identity
        assert key_metadata(self.path) == expected_identity, 'Credential file changed before admission'
        # A regular file can be replaced by a FIFO between lstat and open.
        # Nonblocking open reaches fstat's identity rejection without hanging
        # the preflight supervisor inside a read-only FIFO open.
        descriptor = os.open(self.path, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK)
        try:
            assert identity(os.fstat(descriptor)) == expected_identity, 'Credential file changed during admission'
            raw = os.read(descriptor, MAX_KEY_BYTES + 1)
            assert identity(os.fstat(descriptor)) == expected_identity, 'Credential file changed during read'
            secret = raw.strip()
            assert len(raw) <= MAX_KEY_BYTES and secret and all(33 <= c <= 126 for c in secret) and not secret.startswith((b'{', b'[')), 'Raw token required'
            self.fd, self.raw, self.secret = descriptor, raw, secret
            self.assert_unchanged()
        except BaseException:
            os.close(descriptor)
            self.fd = None
            raise

    def assert_unchanged(self):
        assert self.fd is not None and self.secret, 'Credential capability unavailable'
        assert key_metadata(self.path) == self.expected_identity, 'Credential pathname identity changed'
        assert identity(os.fstat(self.fd)) == self.expected_identity, 'Credential descriptor identity changed'
        os.lseek(self.fd, 0, os.SEEK_SET)
        raw = os.read(self.fd, MAX_KEY_BYTES + 1)
        assert raw == self.raw, 'Credential bytes changed'
        assert identity(os.fstat(self.fd)) == self.expected_identity and key_metadata(self.path) == self.expected_identity, 'Credential changed during post audit'

    def redact(self, value):
        return value.replace(self.secret, b'<redacted-key>')

    def close(self):
        if self.fd is not None:
            os.close(self.fd)
            self.fd = None


def run_validated_worker(argv, cwd, env, key_path, expected_identity, preflight_timeout=300):
    """Hold output in memory; never read key bytes before exact READY + EOF.

    Returns only sanitized output. Before credential admission, child output is
    deliberately discarded even on failure. The production worker has its own
    exact formal command's 600s joined process-group/pipe lifetime after READY.
    """
    ready_read, ready_write = os.pipe()
    secret_read, secret_write = os.pipe()
    child = None
    selector = selectors.DefaultSelector()
    capability = None
    admitted = False
    chunks = []
    failure = None
    try:
        child = subprocess.Popen([*argv, '--ready-fd', str(ready_write), '--credential-fd', str(secret_read)],
                                 cwd=cwd, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                 pass_fds=(ready_write, secret_read), start_new_session=True)
        os.close(ready_write); ready_write = None
        os.close(secret_read); secret_read = None
        selector.register(ready_read, selectors.EVENT_READ, 'ready')
        selector.register(child.stdout, selectors.EVENT_READ, 'stdout')
        ready = bytearray()
        started = time.monotonic()
        while selector.get_map():
            if not admitted and time.monotonic() - started > preflight_timeout:
                raise RuntimeError('Worker preflight deadline expired before credential admission')
            for key, _ in selector.select(timeout=0.25):
                data = os.read(key.fd, 65536)
                if key.data == 'stdout':
                    if data: chunks.append(data)
                    else: selector.unregister(key.fileobj)
                    continue
                if data:
                    ready.extend(data)
                    assert len(ready) <= len(READY), 'Invalid worker preflight signal'
                    continue
                selector.unregister(key.fileobj)
                os.close(ready_read); ready_read = None
                assert bytes(ready) == READY and child.poll() is None, 'Worker failed before validated readiness; credential not read'
                capability = Credential(key_path, expected_identity)
                # Below this actual pipe's PIPE_BUF: one atomic write, then EOF. The worker
                # closes both inherited descriptors before launching Source.
                assert len(capability.secret) <= os.fpathconf(secret_write, 'PC_PIPE_BUF'), 'Credential exceeds atomic pipe capacity'
                # Once a write is attempted, conservatively treat Source as
                # admitted even if delivery raises. Close EOF before joining.
                admitted = True
                assert os.write(secret_write, capability.secret) == len(capability.secret), 'Credential pipe write incomplete'
                os.close(secret_write); secret_write = None
        code = child.wait()
        credential_stable = None
        if capability is not None:
            try:
                capability.assert_unchanged()
                credential_stable = True
            except BaseException:
                credential_stable = False
                failure = 'Credential file changed during worker; freezing forbidden'
        raw = b''.join(chunks)
        redactions = raw.count(capability.secret) if capability else 0
        output = capability.redact(raw) if capability else None
        if redactions:
            failure = (failure + '; ' if failure else '') + 'Worker output contained credential bytes; sanitized evidence only, freezing forbidden'
        return {'code': code, 'output': output, 'capability': capability,
                'credentialAdmitted': capability is not None, 'credentialStable': credential_stable,
                'outputRedactions': redactions, 'failure': failure}
    except BaseException:
        # Before admission Source cannot have started, so this group is ours.
        # After admission the exact formal command owns a detached Source
        # group. Do not kill its supervisor and abandon that child: drain and
        # join the worker's existing bounded command/transport cleanup first.
        if admitted and child is not None:
            if secret_write is not None:
                os.close(secret_write); secret_write = None
            remainder, _ = child.communicate()
            chunks.append(remainder or b'')
            raw = b''.join(chunks)
            output = capability.redact(raw)
            return {'code': child.returncode, 'output': output, 'capability': capability,
                    'credentialAdmitted': True, 'credentialStable': None, 'outputRedactions': raw.count(capability.secret),
                    'failure': 'Supervisor interrupted; worker drained and joined; freezing forbidden'}
        if child is not None and child.poll() is None:
            os.killpg(child.pid, signal.SIGTERM)
            try: child.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(child.pid, signal.SIGKILL); child.wait(timeout=10)
        if capability is not None: capability.close()
        raise RuntimeError('Parent worker supervision failed; no unsanitized output retained') from None
    finally:
        selector.close()
        if child is not None and child.stdout is not None: child.stdout.close()
        for descriptor in [ready_read, ready_write, secret_read, secret_write]:
            if descriptor is not None: os.close(descriptor)
