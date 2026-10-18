from public_cli_fixture import PrivateLoopbackHTTPServer, PrivatePublicCLINetwork
import fcntl, http.server, json, os, pathlib, select, struct, subprocess, sys, termios, threading, time

binary, root = sys.argv[1:]
root = pathlib.Path(root).resolve()
assert (root / 'owned-diff-pty').read_text() == 'private'
home, config, temp, work, bins = [root / name for name in ('home', 'config', 'temp', 'work', 'bin')]
for path in (home, config, temp, work, bins): path.mkdir(exist_ok=True)
env = {'HOME': str(home), 'USERPROFILE': str(home), 'CLAUDE_CONFIG_DIR': str(config), 'TMPDIR': str(temp), 'CC_HAHA_NATIVE_FIXTURE_ROOT': str(root), 'PATH': str(bins) + ':/usr/bin:/bin', 'SHELL': '/bin/bash', 'TERM': 'xterm-256color', 'TZ': 'UTC', 'USER': 'private-diff-fixture', 'ANTHROPIC_API_KEY': 'fake-private-diff-key', 'DISABLE_TELEMETRY': '1', 'CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH': '1', 'CLAUDE_CODE_MAX_RETRIES': '0', 'CLAUDE_CODE_ATTRIBUTION_HEADER': '0', 'NO_PROXY': '127.0.0.1,localhost'}
def git(*args): subprocess.run(['/usr/bin/git', *args], cwd=work, env=env, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
git('init', '-q'); git('config', 'user.name', 'Private fixture'); git('config', 'user.email', 'fixture@example.invalid')
(work / 'tracked.txt').write_text('DIFF_ORIGINAL_LINE\n'); git('add', 'tracked.txt'); git('commit', '-qm', 'private fixture')
(work / 'tracked.txt').write_text('DIFF_CHANGED_LINE\n')
(work / 'untracked.txt').write_text('not staged\n')
# A real native process owns the held git invocation. It cannot return synthetic
# diff data; ordinary requests exec the real system git. Esc must kill and join
# the held child, and closing the terminal must kill a later held child too.
wrapper = bins / 'git'
wrapper.write_text('#!/usr/bin/python3\nimport os,pathlib,sys,time\nr=pathlib.Path(' + repr(str(root)) + ')\nif "diff" in sys.argv and (r/"hold-git").exists():\n p=r/("held-git-"+str(os.getpid()));p.write_text(str(os.getpid()))\n while (r/"hold-git").exists():time.sleep(.02)\nos.execv("/usr/bin/git",["git",*sys.argv[1:]])\n')
wrapper.chmod(0o700)
requests = []
class Model(http.server.BaseHTTPRequestHandler):
 def do_POST(self):
  body = self.rfile.read(int(self.headers.get('Content-Length', 0))); requests.append(body.decode())
  self.send_response(200); self.send_header('Content-Type', 'text/event-stream'); self.end_headers()
  def event(kind, value): self.wfile.write(('event: ' + kind + '\ndata: ' + json.dumps(value) + '\n\n').encode())
  event('message_start', {'type':'message_start','message':{'id':'diff-human','type':'message','role':'assistant','content':[],'model':'fixture-model','stop_reason':None,'stop_sequence':None,'usage':{'input_tokens':12,'output_tokens':0}}})
  event('content_block_start', {'type':'content_block_start','index':0,'content_block':{'type':'text','text':''}})
  event('content_block_delta', {'type':'content_block_delta','index':0,'delta':{'type':'text_delta','text':'DIFF_NEXT_HUMAN_COMPLETED'}})
  event('content_block_stop', {'type':'content_block_stop','index':0})
  event('message_delta', {'type':'message_delta','delta':{'stop_reason':'end_turn','stop_sequence':None},'usage':{'output_tokens':1}})
  event('message_stop', {'type':'message_stop'}); self.wfile.flush()
 def log_message(self, *args): pass
peer = PrivateLoopbackHTTPServer(('127.0.0.1',0),Model); worker = threading.Thread(target=peer.serve_forever,daemon=True); worker.start()
env['ANTHROPIC_BASE_URL'] = 'http://127.0.0.1:' + str(peer.server_address[1])
network = PrivatePublicCLINetwork(env); network.seed_startup(binary,env,work,config)
master, slave = os.openpty(); fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',32,110,0,0)); before = termios.tcgetattr(slave)
def setup(): os.setsid(); fcntl.ioctl(slave,termios.TIOCSCTTY,0)
guardian = pathlib.Path(__file__).with_name('tui_exit_public_pty.py')
p = subprocess.Popen(['/usr/bin/python3',str(guardian),'_guardian',str(root),binary,'cli','--app-root',str(root),'--model','fixture-model','--thinking','disabled','--tools','','--system-prompt','actual public diff fixture','--no-computer-use'],cwd=work,env=env,stdin=slave,stdout=slave,stderr=slave,preexec_fn=setup,close_fds=True)
captured = bytearray(); deadline = time.monotonic()+25
def pump():
 if select.select([master],[],[],.03)[0]:
  try: captured.extend(os.read(master,65536))
  except OSError: pass
def wait(needle,start=0):
 while needle not in captured[start:]:
  if p.poll() is not None or time.monotonic()>deadline: raise AssertionError('actual diff PTY '+repr(bytes(captured[-7000:])))
  pump()
def send(data): os.write(master,data)
def alive(pid):
 try: os.kill(pid,0); return True
 except ProcessLookupError: return False
def wait_held(previous):
 while True:
  current = set(root.glob('held-git-*'))-previous
  if current: return current
  if p.poll() is not None or time.monotonic()>deadline: raise AssertionError('real git not held '+repr(bytes(captured[-4000:])))
  pump()
try:
 wait(b'Claude Code'); send(b'/diff\r'); wait(b'tracked.txt'); send(b'\r'); wait(b'DIFF_CHANGED_LINE'); start=len(captured); send(b'\x1b'); wait(b'Enter view',start)
 start=len(captured); send(b'\x1b'); wait(b'Diff dialog dismissed',start)
 (root/'hold-git').write_text('owned'); previous=set(root.glob('held-git-*')); start=len(captured); send(b'/diff\r'); wait(b'Loading diff',start); held=wait_held(previous)
 start=len(captured); send(b'\x1b'); wait(b'Diff dialog dismissed',start)
 stop=time.monotonic()+2
 while any(alive(int(path.read_text())) for path in held):
  if time.monotonic()>stop: raise AssertionError('Esc left real git worker alive')
  pump()
 (root/'hold-git').unlink(); send(b'human after actual diff\r'); wait(b'DIFF_NEXT_HUMAN_COMPLETED')
 assert len(requests)==1 and 'human after actual diff' in requests[0], requests
 (root/'hold-git').write_text('owned'); previous=set(root.glob('held-git-*')); start=len(captured); send(b'/diff\r'); wait(b'Loading diff',start); held2=wait_held(previous)
 send(b'\x04\x04')
 while p.poll() is None:
  if time.monotonic()>deadline: raise AssertionError('global close held git')
  pump()
 state=json.loads((root/'terminal-state.json').read_text()); network.assert_private()
 assert p.returncode==0 and state['terminalRestored'], state
 assert not any(alive(int(path.read_text())) for path in held2), 'global close left real git worker'
 (root/'terminal.raw').write_bytes(captured)
 print(json.dumps({'actualPublicCLI':True,'currentGitDetail':True,'escCanceledJoined':True,'globalCloseJoined':True,'nextHumanRequests':len(requests),'terminalRestored':True,'exitCode':p.returncode,'externalRequests':network.attempts}))
finally:
 if p.poll() is None: p.kill();p.wait()
 (root/'terminal.raw').write_bytes(captured);os.close(master);os.close(slave);network.close();peer.shutdown();peer.server_close();worker.join()
