"""Real /keybindings WX create/editor/reload/reopen/next-Harness terminal flow."""
import fcntl, json, os, pathlib, re, select, struct, subprocess, sys, termios, time

binary, home = sys.argv[1:]
master, slave = os.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 36, 140, 0, 0))
before = termios.tcgetattr(slave)
captured = bytearray()
deadline = time.monotonic() + 33
def setup():
    os.setsid()
    fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
p = subprocess.Popen([binary, '-test.run=^TestNativeKeybindingsCommandPTYFixtureProcess$', '-test.count=1'], stdin=slave, stdout=slave, stderr=slave, env=os.environ.copy(), preexec_fn=setup, close_fds=True)
def pump():
    if select.select([master], [], [], .05)[0]:
        try: captured.extend(os.read(master, 65536))
        except OSError: pass
def wrapped_message(text):
    # PTY wrapping may split any word. Preserve every message character.
    return re.compile(rb'[\r\n]*'.join(rb'[ \t\r\n]+' if value == 32 else re.escape(bytes([value])) for value in text))
def wait(needle, label, start=0):
    while not (needle in captured[start:] if isinstance(needle, bytes) else needle.search(captured[start:])):
        if time.monotonic() > deadline or p.poll() is not None:
            raise AssertionError(label + ' ' + repr(bytes(captured[-7000:])))
        pump()
def send(value):
    start = len(captured)
    os.write(master, value)
    return start
try:
    wait(b'Claude Code', 'normal terminal entry')
    start = send(b'/keybindings\r')
    wait(b'KEYBINDINGS_EDITOR_ACTUAL_TTY_19AC_1', 'new template handed to actual editor', start)
    file = pathlib.Path(pathlib.Path(home, 'keybindings-command-path.txt').read_text())
    counter = pathlib.Path(home, 'keybindings-command-gate-calls.txt')
    count_before = int(counter.read_text())
    send(b'save-keybindings\r')
    wait(wrapped_message(b'Opened in your editor.'), 'same TTY resumed after first editor', start)
    # Source watcher checks stability for 500 ms using its 200 ms polling interval.
    # Observe the real trusted loader rather than substituting a new binding set.
    while int(counter.read_text() or '0') <= count_before:
        if time.monotonic() > deadline or p.poll() is not None:
            raise AssertionError('actual edited bindings were not reloaded')
        pump()
    start = send(b'\x0b\x10')
    wait(b'Claude understands your codebase', 'newly edited chord opens real Help', start)
    send(b'\x1b')
    time.sleep(.18)
    pump()
    edited = file.read_bytes()
    start = send(b'/keybindings\r')
    wait(b'KEYBINDINGS_EDITOR_ACTUAL_TTY_19AC_2', 'existing file handed to actual editor', start)
    assert file.read_bytes() == edited, 'exclusive create replaced existing user bytes'
    start = send(b'cancel-edit\r')
    wait(b'Claude: Opened ', 'same TTY resumed after editor quit without save', start)
    assert file.read_bytes() == edited, 'editor quit changed existing file'
    start = send(b'KEYBINDINGS_COMMAND_NEXT_HUMAN\r')
    wait(b'KEYBINDINGS_COMMAND_NEXT_COMPLETE_19AC', 'same actual Harness next human', start)
    wait(b'Worked for', 'next Harness finally', start)
    send(b'\x04\x04')
    receipt_file = pathlib.Path(home, 'keybindings-command-receipt.json')
    while not receipt_file.exists():
        if time.monotonic() > deadline or p.poll() is not None:
            raise AssertionError('normal exit restoration receipt missing ' + repr(bytes(captured[-7000:])))
        pump()
    receipt = json.loads(receipt_file.read_text())
    editors = json.loads(pathlib.Path(home, 'keybindings-editor-receipt.json').read_text())
    assert receipt['terminalRestored'] and termios.tcgetattr(slave) == before, receipt
    assert len(editors) == 2 and all(row['terminalReleased'] for row in editors), editors
    assert [row['stdin'] for row in editors] == ['save-keybindings', 'cancel-edit'], editors
    assert editors[1]['before'].encode() == edited, 'reopen replaced existing unknown fields'
    pathlib.Path(home, 'keybindings-command-termios-release').write_text('physical slave restored before leader exit')
    while p.poll() is None:
        if time.monotonic() > deadline: raise AssertionError('actual normal exit timeout')
        pump()
    assert p.returncode == 0 and receipt['requests'] == 1 and receipt['editedFilePreserved'], (receipt, p.returncode)
    print(json.dumps({'terminalRestored': True, 'actualExclusiveCreate': True, 'actualEditorSaveAndQuitWithoutSave': True, 'editedChordOpenedHelp': True, 'existingUnknownFieldsPreserved': True, 'localCommandsZeroModelRequests': True, 'nextHumanRequests': 1, 'editorChildren': 2, 'exitCode': p.returncode}))
finally:
    pathlib.Path(home, 'keybindings-command-terminal.txt').write_bytes(captured)
    if p.poll() is None: p.kill(); p.wait()
    os.close(master)
    os.close(slave)
