"""Normal RunCommand PTY, actual plan Write/editor/Read and next human."""
import fcntl, json, os, pathlib, select, struct, subprocess, sys, termios, time

binary, home = sys.argv[1:]
master, slave = os.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 36, 140, 0, 0))
before = termios.tcgetattr(slave)
captured = bytearray()
deadline = time.monotonic() + 28
def setup():
    os.setsid()
    fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
p = subprocess.Popen([binary, '-test.run=^TestNativePlanPTYFixtureProcess$', '-test.count=1'], stdin=slave, stdout=slave, stderr=slave, env=os.environ.copy(), preexec_fn=setup, close_fds=True)
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
    wait(b'Claude Code', 'normal entry')
    start = send(b'/plan\r')
    wait(b'Enabled plan mode', 'actual mode transition', start)
    start = send(b'/plan\r')
    wait(b'No plan written yet.', 'actual missing plan', start)
    start = send(b'Write an actual plan in the provided plan file.\r')
    wait(b'PLAN_FILE_CREATED_89EF', 'actual Write result', start)
    wait(b'Worked for', 'first Harness finally', start)
    start = send(b'/plan\r')
    wait(b'INITIAL_PLAN_FROM_REAL_WRITE', 'actual plan display', start)
    start = send(b'/plan open\r')
    wait(b'PLAN_EDITOR_READING_ACTUAL_TTY_89EF', 'editor got released TTY', start)
    os.write(master, b'save-plan\r')
    wait(b'Opened plan in editor:', 'editor finished and same terminal resumed', start)
    start = send(b'/plan\r')
    wait(b'EDITED_PLAN_FROM_EXTERNAL_EDITOR', 'actual edited plan display', start)
    start = send(b'Read the edited plan through the actual Read tool, then finish.\r')
    wait(b'PLAN_NEXT_HUMAN_ACTUAL_READ_89EF', 'same Harness actual Read', start)
    wait(b'Worked for', 'next Harness finally', start)
    os.write(master, b'\x04\x04')
    receipt_file = pathlib.Path(home, 'plan-pty-receipt.json')
    while not receipt_file.exists():
        if time.monotonic() > deadline or p.poll() is not None:
            raise AssertionError('restoration receipt missing ' + repr(bytes(captured[-6000:])))
        pump()
    receipt = json.loads(receipt_file.read_text())
    editor = json.loads(pathlib.Path(home, 'editor-receipt.json').read_text())
    assert receipt['terminalRestored'] and before == termios.tcgetattr(slave), receipt
    pathlib.Path(home, 'plan-termios-release').write_text('actual slave restored before leader exit')
    while p.poll() is None:
        if time.monotonic() > deadline: raise AssertionError('normal exit timeout')
        pump()
    assert p.returncode == 0 and receipt['requests'] == 4 and receipt['readEdited'] and receipt['canonicalPlan'], (p.returncode, receipt)
    assert receipt['terminalRestored'] and editor['terminalReleased'] and editor['stdin'] == 'save-plan', (receipt, editor)
    print(json.dumps({'terminalRestored': True, 'originalPlanFileEdited': True, 'actualReadNextHuman': True, 'requests': 4, 'exitCode': p.returncode}))
finally:
    pathlib.Path(home, 'plan-pty-terminal.txt').write_bytes(captured)
    if p.poll() is None: p.kill(); p.wait()
    os.close(master)
    os.close(slave)
