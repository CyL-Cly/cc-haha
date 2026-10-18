from public_cli_fixture import PrivateLoopbackHTTPServer, PrivatePublicCLINetwork
import fcntl, http.server, json, os, pathlib, select, signal, struct, subprocess, sys, termios, threading, time

binary, root = sys.argv[1:]
root = pathlib.Path(root).resolve()
assert (root / 'owned-branch-pty').read_text() == 'private'
home, config, temp, work = [root / name for name in ('home', 'config', 'temp', 'work')]
for path in (home, config, temp, work): path.mkdir(exist_ok=True)
env = {'HOME': str(home), 'USERPROFILE': str(home), 'CLAUDE_CONFIG_DIR': str(config), 'TMPDIR': str(temp), 'CC_HAHA_NATIVE_FIXTURE_ROOT': str(root), 'PATH': '/usr/bin:/bin', 'SHELL': '/bin/bash', 'TERM': 'xterm-256color', 'TZ': 'UTC', 'USER': 'private-branch-fixture', 'ANTHROPIC_API_KEY': 'fake-private-branch-key', 'DISABLE_TELEMETRY': '1', 'CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH': '1', 'CLAUDE_CODE_MAX_RETRIES': '0', 'CLAUDE_CODE_ATTRIBUTION_HEADER': '0', 'NO_PROXY': '127.0.0.1,localhost'}
requests = []
class Model(http.server.BaseHTTPRequestHandler):
 def do_POST(self):
  body = self.rfile.read(int(self.headers.get('Content-Length', 0))); requests.append(body.decode())
  self.send_response(200); self.send_header('Content-Type', 'text/event-stream'); self.end_headers()
  def event(kind, value): self.wfile.write(('event: ' + kind + '\ndata: ' + json.dumps(value) + '\n\n').encode())
  event('message_start', {'type':'message_start','message':{'id':'branch-response-'+str(len(requests)),'type':'message','role':'assistant','content':[],'model':'fixture-model','stop_reason':None,'stop_sequence':None,'usage':{'input_tokens':12,'output_tokens':0}}})
  event('content_block_start', {'type':'content_block_start','index':0,'content_block':{'type':'text','text':''}})
  event('content_block_delta', {'type':'content_block_delta','index':0,'delta':{'type':'text_delta','text':'BRANCH_HUMAN_COMPLETED_'+str(len(requests))}})
  event('content_block_stop', {'type':'content_block_stop','index':0})
  event('message_delta', {'type':'message_delta','delta':{'stop_reason':'end_turn','stop_sequence':None},'usage':{'output_tokens':1}})
  event('message_stop', {'type':'message_stop'}); self.wfile.flush()
 def log_message(self, *args): pass
peer = PrivateLoopbackHTTPServer(('127.0.0.1',0),Model); worker = threading.Thread(target=peer.serve_forever,daemon=True); worker.start()
env['ANTHROPIC_BASE_URL'] = 'http://127.0.0.1:' + str(peer.server_address[1])
network = PrivatePublicCLINetwork(env); network.seed_startup(binary,env,work,config)
master, slave = os.openpty(); fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,120,0,0))
def setup(): os.setsid(); fcntl.ioctl(slave,termios.TIOCSCTTY,0)
guardian = pathlib.Path(__file__).with_name('tui_exit_public_pty.py')
p = subprocess.Popen(['/usr/bin/python3',str(guardian),'_guardian',str(root),binary,'cli','--app-root',str(root),'--model','fixture-model','--thinking','disabled','--tools','','--system-prompt','actual public branch fixture','--no-computer-use'],cwd=work,env=env,stdin=slave,stdout=slave,stderr=slave,preexec_fn=setup,close_fds=True)
captured = bytearray(); deadline = time.monotonic()+25
def pump():
 if select.select([master],[],[],.03)[0]:
  try: captured.extend(os.read(master,65536))
  except OSError: pass
def wait(needle,start=0):
 while needle not in captured[start:]:
  if p.poll() is not None or time.monotonic()>deadline:
   if p.poll() is None and (root/'actual-go-pid').exists():
    os.kill(int((root/'actual-go-pid').read_text()),signal.SIGQUIT)
    stop=time.monotonic()+2
    while p.poll() is None and time.monotonic()<stop: pump()
    for _ in range(4): pump()
   raise AssertionError('actual branch PTY '+repr(bytes(captured[-65000:])))
  pump()
def send(data): os.write(master,data)
def transcripts(): return set((config/'projects').rglob('*.jsonl'))
def materialized_branch_file(previous):
 while True:
  added=transcripts()-previous
  if len(added)==1:
   path=next(iter(added))
   if '"forkedFrom"' in path.read_text(): return path
  if time.monotonic()>deadline: raise AssertionError('actual physical branch file not materialized')
  pump()
def persisted(path, text):
 while not path.exists() or text not in path.read_text():
  if time.monotonic()>deadline: raise AssertionError('actual physical transcript did not append '+str(path))
  pump()
try:
 wait(b'Claude Code'); send(b'BRANCH_PARENT_HUMAN\r'); wait(b'BRANCH_HUMAN_COMPLETED_1'); wait(b'Worked for')
 while len(transcripts())!=1:
  if time.monotonic()>deadline: raise AssertionError('no original transcript')
  pump()
 parent = next(iter(transcripts())); persisted(parent,'BRANCH_HUMAN_COMPLETED_1'); original = parent.read_bytes()
 start=len(captured);before_branch=transcripts();send(b'/branch public named branch\r');materialized_branch_file(before_branch)
 # The actual Source call awaits context.resume before its onDone callback;
 # creating the fork file alone does not establish that the input has resumed.
 wait(b'Branched conversation "public named branch". You are now in the branch.',start)
 assert len(requests)==1, requests
 forks=transcripts()-{parent}; assert len(forks)==1, forks
 firstfork=next(iter(forks)); assert '"forkedFrom"' in firstfork.read_text()
 assert parent.read_bytes()==original
 start=len(captured); send(b'BRANCH_SECOND_HUMAN\r'); wait(b'BRANCH_HUMAN_COMPLETED_2',start); persisted(firstfork,'BRANCH_HUMAN_COMPLETED_2')
 assert len(requests)==2 and all(value in requests[1] for value in ('BRANCH_PARENT_HUMAN','BRANCH_SECOND_HUMAN')), requests
 assert json.loads(json.loads(requests[1])['metadata']['user_id'])['session_id']==firstfork.stem,requests
 before_alias=firstfork.read_bytes();start=len(captured);before_branch=transcripts();send(b'/fork alias child\r');materialized_branch_file(before_branch)
 wait(b'Branched conversation "alias child". You are now in the branch.',start)
 assert len(requests)==2 and len(transcripts())==3
 start=len(captured); send(b'BRANCH_THIRD_HUMAN\r'); wait(b'BRANCH_HUMAN_COMPLETED_3',start)
 assert len(requests)==3 and all(value in requests[2] for value in ('BRANCH_PARENT_HUMAN','BRANCH_SECOND_HUMAN','BRANCH_THIRD_HUMAN')), requests
 assert json.loads(json.loads(requests[2])['metadata']['user_id'])['session_id']==next(iter(transcripts()-{parent,firstfork})).stem,requests
 assert parent.read_bytes()==original and firstfork.read_bytes()==before_alias
 send(b'\x04\x04')
 while p.poll() is None:
  if time.monotonic()>deadline: raise AssertionError('branch exit did not join')
  pump()
 state=json.loads((root/'terminal-state.json').read_text()); network.assert_private()
 assert p.returncode==0 and state['terminalRestored'], state
 print(json.dumps({'actualPublicCLI':True,'branchAndAlias':True,'parentsImmutable':True,'modelRequests':len(requests),'terminalRestored':True,'exitCode':p.returncode,'externalRequests':network.attempts}))
finally:
 if p.poll() is None: p.kill();p.wait()
 (root/'terminal.raw').write_bytes(captured); (root/'model-requests.json').write_text(json.dumps(requests))
 os.close(master);os.close(slave);network.close();peer.shutdown();peer.server_close();worker.join()
