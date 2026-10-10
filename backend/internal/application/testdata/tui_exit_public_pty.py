"""Exercise the actual public Go CLI, with only external native fixture seams."""
import fcntl, http.server, json, os, pathlib, select, struct, subprocess, sys, termios, threading, time
if len(sys.argv) > 1 and sys.argv[1] == '_guardian':
 root, *command = sys.argv[2:]
 before = termios.tcgetattr(0)
 child = subprocess.Popen(command, close_fds=True)
 pathlib.Path(root, 'actual-go-pid').write_text(str(child.pid))
 code = child.wait()
 after_raw = termios.tcgetattr(0)
 pending_input = None
 if sys.platform == 'darwin':
  # Same existing native observation primitive as pty_native_darwin_test.go.
  # FIONREAD settles XNU PENDIN without consuming bytes or setting termios.
  pending_input = struct.unpack('I', fcntl.ioctl(0, 0x4004667f, struct.pack('I', 0)))[0]
 after = termios.tcgetattr(0)
 pathlib.Path(root, 'terminal-state.json').write_text(json.dumps({'terminalRestored': before == after, 'childExitCode': code, 'childPID': child.pid, 'before': before, 'afterRaw': after_raw, 'after': after, 'pendingInput': pending_input}, default=lambda v: list(v)))
 sys.exit(code)
binary, root, mode = sys.argv[1:]
root = pathlib.Path(root).resolve()
assert root.is_absolute() and (root / 'owned-exit-pty').read_text() == 'private'
home, config, temp, work = [root / name for name in ('home', 'config', 'temp', 'work')]
for path in (home, config, temp, work): path.mkdir(exist_ok=True)
hook = work / 'exit-hook.py'
hook.write_text('import json,sys\nfrom pathlib import Path\np=Path(' + repr(str(root / 'session-end.jsonl')) + ')\nwith p.open("a") as f: f.write(json.dumps(json.load(sys.stdin))+"\\n")\n')
settings = {'agentTeamsEnabled': False, 'alwaysThinkingEnabled': False, 'hooks': {'SessionEnd': [{'hooks': [{'type': 'command', 'command': '/usr/bin/python3 ' + str(hook), 'timeout': 3}]}]}}
(config / 'settings.json').write_text(json.dumps(settings))
requests = []
read_observed = []
read_marker = work / 'read-before-exit.txt'
read_marker.write_text('EXIT_PHYSICAL_READ_BEFORE_QUIT')
class ModelPeer(http.server.BaseHTTPRequestHandler):
 def do_POST(self):
  requests.append({'method': self.command, 'path': self.path})
  body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
  if mode != 'after-read': self.send_response(500); self.end_headers(); return
  number = len(requests)
  if number == 2: read_observed.append(b'EXIT_PHYSICAL_READ_BEFORE_QUIT' in body)
  self.send_response(200); self.send_header('Content-Type', 'text/event-stream'); self.end_headers()
  def event(kind, data): self.wfile.write(('event: ' + kind + '\ndata: ' + json.dumps(data, separators=(',', ':')) + '\n\n').encode())
  event('message_start', {'type': 'message_start', 'message': {'id': 'exit-physical-' + str(number), 'type': 'message', 'role': 'assistant', 'model': 'fixture-model', 'content': [], 'stop_reason': None, 'stop_sequence': None, 'usage': {'input_tokens': 10, 'output_tokens': 0}}})
  if number == 1:
   event('content_block_start', {'type': 'content_block_start', 'index': 0, 'content_block': {'type': 'tool_use', 'id': 'exit-actual-read', 'name': 'Read', 'input': {}}})
   event('content_block_delta', {'type': 'content_block_delta', 'index': 0, 'delta': {'type': 'input_json_delta', 'partial_json': json.dumps({'file_path': str(read_marker)})}})
  else:
   event('content_block_start', {'type': 'content_block_start', 'index': 0, 'content_block': {'type': 'text', 'text': ''}})
   event('content_block_delta', {'type': 'content_block_delta', 'index': 0, 'delta': {'type': 'text_delta', 'text': 'EXIT_ACTUAL_READ_FINISHED'}})
  event('content_block_stop', {'type': 'content_block_stop', 'index': 0})
  event('message_delta', {'type': 'message_delta', 'delta': {'stop_reason': 'tool_use' if number == 1 else 'end_turn', 'stop_sequence': None}, 'usage': {'output_tokens': 1}})
  event('message_stop', {'type': 'message_stop'})
  self.wfile.flush()
 def do_GET(self):
  requests.append({'method': self.command, 'path': self.path})
  self.send_response(500); self.end_headers()
 def log_message(self, *args): pass
model_peer = http.server.ThreadingHTTPServer(('127.0.0.1', 0), ModelPeer)
model_worker = threading.Thread(target=model_peer.serve_forever, daemon=True); model_worker.start()
env = {'HOME': str(home), 'USERPROFILE': str(home), 'CLAUDE_CONFIG_DIR': str(config), 'TMPDIR': str(temp), 'CC_HAHA_NATIVE_FIXTURE_ROOT': str(root), 'PATH': '/usr/bin:/bin', 'SHELL': '/bin/bash', 'TERM': 'xterm-256color', 'TZ': 'UTC', 'USER': 'private-exit-fixture', 'ANTHROPIC_API_KEY': 'fake-private-exit-key', 'ANTHROPIC_BASE_URL': 'http://127.0.0.1:' + str(model_peer.server_address[1]), 'DISABLE_TELEMETRY': '1', 'CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH': '1', 'CLAUDE_CODE_MAX_RETRIES': '0', 'CLAUDE_CODE_ATTRIBUTION_HEADER': '0'}
master, slave = os.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 30, 110, 0, 0))
before = termios.tcgetattr(slave)
def setup():
 os.setsid()
 fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
p = subprocess.Popen(['/usr/bin/python3', str(pathlib.Path(__file__).resolve()), '_guardian', str(root), binary, 'cli', '--app-root', str(root), '--system-prompt', 'private actual exit lifecycle', '--model', 'fixture-model', '--tools', 'Read', '--thinking', 'disabled', '--no-computer-use'], cwd=work, env=env, stdin=slave, stdout=slave, stderr=slave, preexec_fn=setup, close_fds=True)
captured = bytearray()
deadline = time.monotonic() + 25
def pump():
 if select.select([master], [], [], .05)[0]:
  try: captured.extend(os.read(master, 65536))
  except OSError: pass
def wait(needle):
 while needle not in captured:
  if p.poll() is not None or time.monotonic() > deadline: raise AssertionError('initialization/exit ' + repr(bytes(captured[-3500:])))
  pump()
try:
 wait(b'Claude Code')
 if mode == 'after-read':
  os.write(master, ('Read ' + str(read_marker) + ' then finish.\r').encode())
  wait(b'EXIT_ACTUAL_READ_FINISHED')
  os.write(master, b'/exit\r')
 elif mode == 'exit': os.write(master, b'/exit\r')
 elif mode == 'quit': os.write(master, b'/quit ignored\r')
 elif mode == 'ctrl-d': os.write(master, b'\x04\x04')
 else: raise AssertionError(mode)
 while p.poll() is None:
  if time.monotonic() > deadline: raise AssertionError('exit timeout ' + repr(bytes(captured[-5000:])))
  pump()
 for _ in range(4): pump()
 state = json.loads((root / 'terminal-state.json').read_text())
 (root / 'terminal.raw').write_bytes(captured)
 after = before if state['terminalRestored'] else []
 hooks = [json.loads(line) for line in (root / 'session-end.jsonl').read_text().splitlines()]
 assert p.returncode == 0, (p.returncode, repr(bytes(captured[-3500:])))
 assert state['terminalRestored'], 'terminal attributes not restored'
 assert len(requests) == (2 if mode == 'after-read' else 0), requests
 assert mode != 'after-read' or read_observed == [True], read_observed
 assert read_marker.read_text() == 'EXIT_PHYSICAL_READ_BEFORE_QUIT'
 assert len(hooks) == 1 and hooks[0]['reason'] == 'prompt_input_exit', hooks
 assert b'unknown actual prompt command' not in captured and b'Go public CLI capability' not in captured, repr(bytes(captured[-3500:]))
 (root / 'terminal.raw').write_bytes(captured)
 print(json.dumps({'mode': mode, 'exitCode': p.returncode, 'sessionEndCount': len(hooks), 'reason': hooks[0]['reason'], 'terminalRestored': before == after, 'pidGone': p.poll() is not None, 'credentialFixture': 'fake/private', 'keychainPrimitive': 'native_process_fixture unavailable', 'modelRequests': len(requests), 'actualGoPID': state['childPID'], 'physicalRead': mode == 'after-read' and read_observed == [True]}))
finally:
 if p.poll() is None: p.kill(); p.wait()
 os.close(master); os.close(slave)
 model_peer.shutdown(); model_peer.server_close(); model_worker.join()
