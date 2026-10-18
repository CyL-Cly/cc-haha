from public_cli_fixture import PrivateLoopbackHTTPServer, PrivatePublicCLINetwork
from vt_screen import VTScreen
import fcntl, http.server, json, os, pathlib, select, signal, struct, subprocess, sys, termios, threading, time

binary, root = sys.argv[1:3]
preview_capture = len(sys.argv) > 3 and sys.argv[3] == 'preview-capture'
root = pathlib.Path(root).resolve()
assert (root / 'owned-resume-pty').read_text() == 'private'
home, config, temp, work = [root / name for name in ('home', 'config', 'temp', 'work')]
for path in (home, config, temp, work): path.mkdir(exist_ok=True)
env = {'HOME': str(home), 'USERPROFILE': str(home), 'CLAUDE_CONFIG_DIR': str(config), 'TMPDIR': str(temp), 'CC_HAHA_NATIVE_FIXTURE_ROOT': str(root), 'PATH': '/usr/bin:/bin', 'SHELL': '/bin/bash', 'TERM': 'xterm-256color', 'TZ': 'UTC', 'USER': 'private-resume-fixture', 'ANTHROPIC_API_KEY': 'fake-private-branch-key', 'DISABLE_TELEMETRY': '1', 'CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH': '1', 'CLAUDE_CODE_MAX_RETRIES': '0', 'CLAUDE_CODE_ATTRIBUTION_HEADER': '0', 'NO_PROXY': '127.0.0.1,localhost'}
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
captured = bytearray(); deadline = time.monotonic()+25; screen = VTScreen(120,36)
def pump():
 if select.select([master],[],[],.03)[0]:
  try:
   data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError: pass
def wait(needle,start=0):
 while needle not in captured[start:]:
  if p.poll() is not None or time.monotonic()>deadline:
   prior_tail=bytes(captured[-6500:])
   quit_start=len(captured)
   if p.poll() is None and (root/'actual-go-pid').exists():
    os.kill(int((root/'actual-go-pid').read_text()),signal.SIGQUIT)
    stop=time.monotonic()+2
    while p.poll() is None and time.monotonic()<stop: pump()
    for _ in range(4): pump()
   raise AssertionError('actual resume PTY expected '+repr(needle)+' tail '+repr(prior_tail)+' quit '+repr(bytes(captured[quit_start:quit_start+18000])))
  pump()
def send(data): os.write(master,data)
def wait_resumed(present, absent):
 while True:
  visible=screen.text()
  if present in visible and absent not in visible and 'Loading conversations' not in visible and 'Resume Session' not in visible and any(line.rstrip().endswith('>') for line in visible.splitlines()):return
  if p.poll() is not None or time.monotonic()>deadline:raise AssertionError('actual resumed screen did not replace old conversation '+repr(visible))
  pump()
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
  if time.monotonic()>deadline: raise AssertionError('actual physical transcript did not append '+str(path)+' files='+str([(str(x),x.read_text()[-1200:]) for x in transcripts()])+' screen='+repr(bytes(captured[-6500:])))
  pump()
try:
 wait(b'Claude Code'); send(b'BRANCH_PARENT_HUMAN\r'); wait(b'BRANCH_HUMAN_COMPLETED_1'); wait(b'Worked for')
 while len(transcripts())!=1:
  if time.monotonic()>deadline: raise AssertionError('no original transcript')
  pump()
 parent = next(iter(transcripts())); persisted(parent,'BRANCH_HUMAN_COMPLETED_1')
 rename_start=len(captured);send(b'/rename owned resume parent\r');wait(b'Session renamed to: owned resume parent',rename_start);persisted(parent,'owned resume parent');original = parent.read_bytes()
 start=len(captured);before_branch=transcripts();send(b'/branch public named branch\r');materialized_branch_file(before_branch)
 # Source branch awaits context.resume before its canonical onDone stdout.
 # A materialized file alone can precede that callback's Editor reset.
 wait(b'Branched conversation "public named branch". You are now in the branch.',start)
 assert len(requests)==1, requests
 forks=transcripts()-{parent}; assert len(forks)==1, forks
 firstfork=next(iter(forks)); assert '"forkedFrom"' in firstfork.read_text()
 assert parent.read_bytes()==original
 start=len(captured); send(b'BRANCH_SECOND_HUMAN\r'); wait(b'BRANCH_HUMAN_COMPLETED_2',start); persisted(firstfork,'BRANCH_HUMAN_COMPLETED_2')
 assert len(requests)==2 and all(value in requests[1] for value in ('BRANCH_PARENT_HUMAN','BRANCH_SECOND_HUMAN')), requests
 assert json.loads(json.loads(requests[1])['metadata']['user_id'])['session_id']==firstfork.stem,requests
 if preview_capture:
  start=len(captured);send(b'/resume\r');wait(b'Resume Session',start)
  send(b'owned resume parent');send(b'\r');start=len(captured);send(b'\x16');wait(b'BRANCH_HUMAN_COMPLETED_1',start)
  original_rows=[json.loads(line) for line in parent.read_text().splitlines() if line.strip()]
  tip=next(row['uuid'] for row in reversed(original_rows) if row.get('type') in ('user','assistant'))
  late={'type':'user','uuid':'external-after-preview','parentUuid':tip,'sessionId':parent.stem,'timestamp':'2099-10-01T00:00:02.000Z','cwd':str(work),'message':{'role':'user','content':'EXTERNAL_AFTER_PREVIEW_UNSEEN'}}
  with parent.open('a') as target: target.write(json.dumps(late,separators=(',',':'))+'\n')
  assert 'EXTERNAL_AFTER_PREVIEW_UNSEEN' in parent.read_text()
  # The old history is already visible. Observe the actual prompt after
  # modal unmount rather than requiring an incremental repaint of that text.
  start=len(captured);send(b'\r');wait_resumed('BRANCH_HUMAN_COMPLETED_1','BRANCH_SECOND_HUMAN')
  start=len(captured);send(b'PREVIEW_CONFIRMED_NEXT_HUMAN\r');wait(b'BRANCH_HUMAN_COMPLETED_3',start)
  assert len(requests)==3 and 'BRANCH_PARENT_HUMAN' in requests[2] and 'PREVIEW_CONFIRMED_NEXT_HUMAN' in requests[2],requests
  assert 'EXTERNAL_AFTER_PREVIEW_UNSEEN' not in requests[2],requests[2]
  assert json.loads(json.loads(requests[2])['metadata']['user_id'])['session_id']==parent.stem,requests[2]
  send(b'\x04\x04')
  while p.poll() is None:
   if time.monotonic()>deadline: raise AssertionError('preview resume exit did not join')
   pump()
  state=json.loads((root/'terminal-state.json').read_text());network.assert_private()
  assert p.returncode==0 and state['terminalRestored'],state
  print(json.dumps({'actualPublicCLI':True,'previewCapturedFullLog':True,'modelRequests':len(requests),'terminalRestored':True,'exitCode':p.returncode,'externalRequests':network.attempts}))
  sys.exit(0)
 before_alias=firstfork.read_bytes();start=len(captured);before_branch=transcripts();send(b'/fork alias child\r');materialized_branch_file(before_branch)
 wait(b'Branched conversation "alias child". You are now in the branch.',start)
 assert len(requests)==2 and len(transcripts())==3
 start=len(captured); send(b'BRANCH_THIRD_HUMAN\r'); wait(b'BRANCH_HUMAN_COMPLETED_3',start)
 assert len(requests)==3 and all(value in requests[2] for value in ('BRANCH_PARENT_HUMAN','BRANCH_SECOND_HUMAN','BRANCH_THIRD_HUMAN')), requests
 assert json.loads(json.loads(requests[2])['metadata']['user_id'])['session_id']==next(iter(transcripts()-{parent,firstfork})).stem,requests
 assert parent.read_bytes()==original and firstfork.read_bytes()==before_alias
 # Resume UUID and title selectors consume each physical file in the same CLI.
 parent_sid=parent.stem
 # Loading repaints the previous history, including BRANCH_PARENT_HUMAN.
 # Observe current terminal cells replacing the unique child history, rather
 # than requiring incremental output to redraw a session-name prompt.
 start=len(captured);send(('/resume '+parent_sid+'\r').encode());wait_resumed('BRANCH_HUMAN_COMPLETED_1','BRANCH_THIRD_HUMAN')
 start=len(captured);send(b'RESUMED_PARENT_NEXT_HUMAN\r');wait(b'BRANCH_HUMAN_COMPLETED_4',start);wait(b'Worked for',start)
 assert len(requests)==4 and 'BRANCH_PARENT_HUMAN' in requests[3] and 'RESUMED_PARENT_NEXT_HUMAN' in requests[3]
 assert json.loads(json.loads(requests[3])['metadata']['user_id'])['session_id']==parent.stem,requests
 assert 'BRANCH_SECOND_HUMAN' not in requests[3] and 'BRANCH_THIRD_HUMAN' not in requests[3],requests
 persisted(parent,'RESUMED_PARENT_NEXT_HUMAN')
 assert firstfork.read_bytes()==before_alias
 start=len(captured);send(b'/continue PUBLIC NAMED BRANCH (BRANCH)\r');wait_resumed('BRANCH_SECOND_HUMAN','RESUMED_PARENT_NEXT_HUMAN')
 start=len(captured);send(b'RESUMED_BRANCH_TITLE_NEXT_HUMAN\r');wait(b'BRANCH_HUMAN_COMPLETED_5',start);wait(b'Worked for',start)
 assert len(requests)==5 and 'BRANCH_SECOND_HUMAN' in requests[4] and 'RESUMED_BRANCH_TITLE_NEXT_HUMAN' in requests[4]
 assert json.loads(json.loads(requests[4])['metadata']['user_id'])['session_id']==firstfork.stem,requests
 assert 'BRANCH_THIRD_HUMAN' not in requests[4] and 'RESUMED_PARENT_NEXT_HUMAN' not in requests[4],requests
 start=len(captured);send(b'/resume\r');wait(b'Resume Session',start)
 send(b'owned resume parent');send(b'\r');preview_start=len(captured);send(b'\x16');wait(b'BRANCH_HUMAN_COMPLETED_4',preview_start)
 back_start=len(captured);send(b'\x1b');wait(b'Resume Session',back_start);rename_start=len(captured);send(b'\x12');wait(b'Rename session:',rename_start)
 rename_start=len(captured);send(b'picker renamed parent\r');persisted(parent,'picker renamed parent');wait(b'1. picker renamed parent',rename_start)
 start=len(captured);send(b'\r');wait_resumed('RESUMED_PARENT_NEXT_HUMAN','RESUMED_BRANCH_TITLE_NEXT_HUMAN')
 start=len(captured);send(b'RESUMED_PICKER_NEXT_HUMAN\r');wait(b'BRANCH_HUMAN_COMPLETED_6',start)
 assert len(requests)==6 and 'RESUMED_PARENT_NEXT_HUMAN' in requests[5] and 'RESUMED_PICKER_NEXT_HUMAN' in requests[5]
 assert 'BRANCH_SECOND_HUMAN' not in requests[5] and 'RESUMED_BRANCH_TITLE_NEXT_HUMAN' not in requests[5],requests
 assert json.loads(json.loads(requests[5])['metadata']['user_id'])['session_id']==parent.stem,requests
 send(b'\x04\x04')
 while p.poll() is None:
  if time.monotonic()>deadline: raise AssertionError('branch exit did not join')
  pump()
 state=json.loads((root/'terminal-state.json').read_text()); network.assert_private()
 assert p.returncode==0 and state['terminalRestored'], state
 print(json.dumps({'actualPublicCLI':True,'branchAndAlias':True,'parentsImmutable':True,'ordinaryResumeUUIDTitlePicker':True,'previewCapturedFullLog':preview_capture,'modelRequests':len(requests),'terminalRestored':True,'exitCode':p.returncode,'externalRequests':network.attempts}))
finally:
 if p.poll() is None: p.kill();p.wait()
 (root/'terminal.raw').write_bytes(captured); (root/'model-requests.json').write_text(json.dumps(requests))
 os.close(master);os.close(slave);network.close();peer.shutdown();peer.server_close();worker.join()
