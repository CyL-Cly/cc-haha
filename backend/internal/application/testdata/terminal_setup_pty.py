"""Normal RunCommand: install real editor shortcut, back up, continue, exit."""
import fcntl, json, os, pathlib, select, struct, subprocess, sys, termios, time

binary, home = sys.argv[1:]
master, slave = os.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 35, 140, 0, 0))
before = termios.tcgetattr(slave)
captured = bytearray()
deadline = time.monotonic() + 25
def setup():
    os.setsid()
    fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
p = subprocess.Popen([binary, '-test.run=^TestTerminalSetupPTYFixtureProcess$', '-test.count=1'], stdin=slave, stdout=slave, stderr=slave, env=os.environ.copy(), preexec_fn=setup, close_fds=True)
def pump():
    if select.select([master], [], [], .05)[0]:
        try: captured.extend(os.read(master, 65536))
        except OSError: pass
def wait(needle, label, start=0):
    while needle not in captured[start:]:
        if time.monotonic() > deadline or p.poll() is not None:
            raise AssertionError(label + ' ' + repr(bytes(captured[-6000:])))
        pump()
def send(value):
    start = len(captured)
    os.write(master, value)
    return start
try:
    wait(b'Claude Code', 'normal CLI entry')
    start = send(b'/terminal-setup\r')
    wait(b'Installed VSCode terminal Shift+Enter key binding', 'actual install', start)
    start = send(b'/terminal-setup\r')
    wait(b'Found existing VSCode terminal Shift+Enter key binding.', 'actual existing-file backup', start)
    start = send(b'Continue after the editor shortcut was installed.\r')
    wait(b'TERMINAL_SETUP_NEXT_PUBLIC_379B', 'next real Harness request', start)
    wait(b'Worked for', 'real Harness finally', start)
    os.write(master, b'\x04\x04')
    receipt_path = pathlib.Path(home, 'terminal-setup-pty-receipt.json')
    while not receipt_path.exists():
        if time.monotonic() > deadline or p.poll() is not None:
            raise AssertionError('restoration receipt missing ' + repr(bytes(captured[-6000:])))
        pump()
    receipt = json.loads(receipt_path.read_text())
    actual = termios.tcgetattr(slave)
    assert receipt['terminalRestored'] and actual == before, (receipt, {'before': before, 'after': actual})
    pathlib.Path(home, 'terminal-setup-termios-release').write_text('actual slave restored before leader exit')
    while p.poll() is None:
        if time.monotonic() > deadline: raise AssertionError('normal exit timeout')
        pump()
    assert p.returncode == 0 and receipt['requests'] == 1 and receipt['canonical'] and receipt['configurationWritten'] and receipt['backupRetained'], (p.returncode, receipt)
    print(json.dumps(receipt))
finally:
    pathlib.Path(home, 'terminal-setup-pty-terminal.txt').write_bytes(captured)
    if p.poll() is None: p.kill(); p.wait()
    os.close(master)
    os.close(slave)
