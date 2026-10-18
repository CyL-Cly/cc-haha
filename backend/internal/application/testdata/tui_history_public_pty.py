"""Cold resume and main-screen presentation of the actual public Go CLI."""
from public_cli_fixture import PrivateLoopbackHTTPServer, PrivatePublicCLINetwork
import fcntl, http.server, json, os, pathlib, re, select, struct, subprocess, sys, termios, threading, time

binary, root, mode = sys.argv[1:]
root = pathlib.Path(root).resolve()
assert root.is_absolute() and (root / 'owned-history-pty').read_text() == 'private'
home, config, temp, work = [root / name for name in ('home', 'config', 'temp', 'work')]
for path in (home, config, temp, work): path.mkdir()
private_bin = home / '.local' / 'bin'; private_bin.mkdir(parents=True)
launcher = private_bin / 'claude-haha'; launcher.symlink_to(binary)
(config / 'settings.json').write_text(json.dumps({'agentTeamsEnabled': False, 'alwaysThinkingEnabled': False}))
read_path = work / 'history-read.txt'
read_path.write_text('HISTORY_PHYSICAL_READ_MARKER')
requests, read_observed = [], []
class ModelPeer(http.server.BaseHTTPRequestHandler):
 def do_POST(self):
  body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
  requests.append(body)
  number = len(requests)
  if number == 3: read_observed.append(b'HISTORY_PHYSICAL_READ_MARKER' in body)
  self.send_response(200); self.send_header('Content-Type', 'text/event-stream'); self.end_headers()
  def event(kind, data): self.wfile.write(('event: ' + kind + '\ndata: ' + json.dumps(data, separators=(',', ':')) + '\n\n').encode())
  event('message_start', {'type': 'message_start', 'message': {'id': 'history-actual-' + str(number), 'type': 'message', 'role': 'assistant', 'model': 'fixture-model', 'content': [], 'stop_reason': None, 'stop_sequence': None, 'usage': {'input_tokens': 10, 'output_tokens': 0}}})
  if number == 2:
   event('content_block_start', {'type': 'content_block_start', 'index': 0, 'content_block': {'type': 'tool_use', 'id': 'history-read', 'name': 'Read', 'input': {}}})
   event('content_block_delta', {'type': 'content_block_delta', 'index': 0, 'delta': {'type': 'input_json_delta', 'partial_json': json.dumps({'file_path': str(read_path)})}})
  else:
   event('content_block_start', {'type': 'content_block_start', 'index': 0, 'content_block': {'type': 'text', 'text': ''}})
   event('content_block_delta', {'type': 'content_block_delta', 'index': 0, 'delta': {'type': 'text_delta', 'text': 'COLD_HISTORY_ASSISTANT_MARKER' if number == 1 else 'NEW_HISTORY_ASSISTANT_MARKER'}})
  event('content_block_stop', {'type': 'content_block_stop', 'index': 0})
  event('message_delta', {'type': 'message_delta', 'delta': {'stop_reason': 'tool_use' if number == 2 else 'end_turn', 'stop_sequence': None}, 'usage': {'output_tokens': 1}})
  event('message_stop', {'type': 'message_stop'}); self.wfile.flush()
 def do_GET(self): self.send_response(500); self.end_headers()
 def log_message(self, *args): pass
peer = PrivateLoopbackHTTPServer(('127.0.0.1', 0), ModelPeer)
worker = threading.Thread(target=peer.serve_forever, daemon=True); worker.start()
env = {'HOME': str(home), 'USERPROFILE': str(home), 'CLAUDE_CONFIG_DIR': str(config), 'TMPDIR': str(temp), 'CC_HAHA_NATIVE_FIXTURE_ROOT': str(root), 'PATH': '/usr/bin:/bin', 'SHELL': '/bin/bash', 'TERM': 'xterm-256color', 'TZ': 'UTC', 'USER': 'private-history-fixture', 'ANTHROPIC_API_KEY': 'fake-history-key', 'ANTHROPIC_BASE_URL': 'http://127.0.0.1:' + str(peer.server_address[1]), 'DISABLE_TELEMETRY': '1', 'CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH': '1', 'CLAUDE_CODE_MAX_RETRIES': '0', 'CLAUDE_CODE_ATTRIBUTION_HEADER': '0'}
if mode == 'fullscreen': env['CLAUDE_CODE_NO_FLICKER'] = '1'
env['PATH'] = str(private_bin) + ':/usr/bin:/bin'
args = [str(launcher), 'cli', '--app-root', str(root), '--system-prompt', 'private history presentation', '--model', 'fixture-model', '--tools', 'Read', '--thinking', 'disabled', '--no-computer-use']
network = PrivatePublicCLINetwork(env)
fixture_version = network.seed_startup(binary, env, work, config)
master = slave = None; process = None
try:
 seed = subprocess.run(args + ['--print', '--output-format', 'json', 'COLD_HISTORY_USER_MARKER'], cwd=work, env=env, capture_output=True, timeout=15)
 assert seed.returncode == 0, (seed.returncode, seed.stderr.decode(), seed.stdout.decode())
 sid = json.loads(seed.stdout)['session_id']
 transcript = next(config.glob('projects/*/' + sid + '.jsonl'))
 original = transcript.read_bytes()
 assert len(requests) == 1 and b'COLD_HISTORY_ASSISTANT_MARKER' in original
 master, slave = os.openpty(); fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 30, 110, 0, 0))
 def setup(): os.setsid(); fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
 # Reuse the existing guardian solely to observe exact restored termios while
 # the controlling session remains alive. It doesn't own the Go session.
 guardian = pathlib.Path(__file__).with_name('tui_exit_public_pty.py')
 process = subprocess.Popen(['/usr/bin/python3', str(guardian), '_guardian', str(root)] + args + ['--resume', sid], cwd=work, env=env, stdin=slave, stdout=slave, stderr=slave, preexec_fn=setup, close_fds=True)
 captured = bytearray(); deadline = time.monotonic() + 25
 def pump():
  if select.select([master], [], [], .05)[0]:
   try: captured.extend(os.read(master, 65536))
   except OSError: pass
 def wait(needle, label):
  while needle not in captured:
   if process.poll() is not None or time.monotonic() > deadline: raise AssertionError(label + ' ' + repr(bytes(captured[-4500:])))
   pump()
 wait(b'Claude Code', 'actual TUI initialization')
 wait(b'COLD_HISTORY_ASSISTANT_MARKER', 'initial actual resumed assistant missing')
 assert b'COLD_HISTORY_USER_MARKER' in captured, 'initial actual user missing'
 assert len(requests) == 1, 'display started another model request'
 assert transcript.read_bytes() == original, 'display changed canonical transcript'
 os.write(master, b'Read history-read.txt then finish NEW_HISTORY_USER_MARKER\r')
 wait(b'NEW_HISTORY_ASSISTANT_MARKER', 'actual continued Read turn')
 os.write(master, b'/exit\r')
 while process.poll() is None:
  if time.monotonic() > deadline: raise AssertionError('exit deadline')
  pump()
 for _ in range(4): pump()
 (root / 'terminal.raw').write_bytes(captured)
 state = json.loads((root / 'terminal-state.json').read_text())
 network.assert_private()
 assert process.returncode == 0 and state['terminalRestored'], state
 assert len(requests) == 3 and read_observed == [True], (len(requests), read_observed)
 if mode == 'ordinary':
  assert b'\x1b[?1049h' not in captured and b'\x1b[?1047h' not in captured, 'ordinary external CLI incorrectly used alt screen'
 else:
  assert b'\x1b[?1049h' in captured and b'\x1b[?1049l' in captured, 'explicit Source fullscreen lost'
 assert any(goodbye in captured for goodbye in (b'Goodbye!', b'See ya!', b'Bye!', b'Catch you later!')), 'goodbye missing from main screen'
 # Main-screen output is retained by the terminal; no alternate-buffer restore
 # or full-screen clear may erase the completed frame after its final marker.
 final = bytes(captured).rfind(b'NEW_HISTORY_ASSISTANT_MARKER')
 assert b'\x1b[2J' not in captured[final:], 'shutdown erased completed transcript'
 hint = ('Resume this session with:\r\nclaude-haha --resume ' + sid).encode()
 assert hint in captured, 'Source resume hint absent'
 if mode == 'fullscreen':
  main = bytes(captured)[bytes(captured).rfind(b'\x1b[?1049l'):]
  assert hint in main and b'COLD_HISTORY_ASSISTANT_MARKER' not in main and b'NEW_HISTORY_ASSISTANT_MARKER' not in main, 'fullscreen shutdown invented a main-buffer transcript dump'
 print(json.dumps({'mode': mode, 'initialHistoryVisible': True, 'startupModelRequests': 0, 'startupCanonicalUnchanged': True, 'exitCode': process.returncode, 'terminalRestored': state['terminalRestored'], 'screenContractRetained': True, 'resumeHintOnMainBuffer': True, 'physicalRead': True, 'unownedNetworkAttempts': len(network.attempts), 'fixtureVersion': fixture_version, 'modelRequests': len(requests), 'keychainPrimitive': 'native_process_fixture unavailable', 'pidGone': process.poll() is not None, 'sessionID': sid}))
finally:
 if process is not None and process.poll() is None: process.kill(); process.wait()
 if master is not None: os.close(master); os.close(slave)
 peer.shutdown(); peer.server_close(); worker.join()
 network.close()
