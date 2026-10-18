"""Actual public RunCommand prompt-editor PTY; no model or user editor/clipboard.
This file is copied to a private path containing spaces as the physical editor.
"""
import fcntl,json,os,pathlib,select,shlex,signal,stat,struct,subprocess,sys,termios,time

def tty(fd):
 value=termios.tcgetattr(fd)
 return value[:6]+[[x.hex() if isinstance(x,bytes) else x for x in value[6]]]

if len(sys.argv)>1 and sys.argv[1]=='--owned-editor':
 mode,quoted,path=sys.argv[2:]
 home=pathlib.Path(os.environ['CC_HAHA_EDITOR_EXPECTED_HOME']).resolve()
 target=pathlib.Path(path);st=target.lstat()
 assert quoted=='quoted argument' and target.resolve()==target and target.parent==home and target.name.startswith('claude-prompt-') and stat.S_ISREG(st.st_mode) and st.st_uid==os.getuid()
 assert target.read_text()=='PUBLIC_ORIGINAL_DRAFT'
 assert os.isatty(0) and os.isatty(1)
 row={'pid':os.getpid(),'ppid':os.getppid(),'pgid':os.getpgid(0),'mode':mode,'quotedArgument':quoted,'path':str(target),'initialText':target.read_text(),'terminal':tty(0),'canonical':bool(termios.tcgetattr(0)[3]&termios.ICANON),'echo':bool(termios.tcgetattr(0)[3]&termios.ECHO)}
 with (home/'editor-started.json').open('x') as f:json.dump(row,f)
 print('OWNED_PROMPT_EDITOR_RUNNING',flush=True)
 if mode=='sentinel':raise AssertionError('lower-priority EDITOR must not execute')
 if mode=='error':sys.exit(7)
 deadline=time.monotonic()+20
 while not (home/'editor-release').exists():
  assert time.monotonic()<deadline,'editor release deadline'
  time.sleep(.01)
 assert (home/'editor-release').read_text()=='release-owned-editor'
 target.write_text('PUBLIC_EDITED_DRAFT\n')
 with (home/'editor-finished.json').open('x') as f:json.dump({'pid':os.getpid(),'writtenText':target.read_text()},f)
 sys.exit(0)

from vt_screen import VTScreen
binary,home,scenario=sys.argv[1:];home=pathlib.Path(home).resolve()
editor_dir=home/'owned editor';editor_dir.mkdir(mode=0o700)
owned_editor=editor_dir/'physical editor.py';owned_editor.write_bytes(pathlib.Path(__file__).read_bytes());owned_editor.chmod(0o600)
chosen='exec '+shlex.join([sys.executable,str(owned_editor),'--owned-editor',scenario,'quoted argument'])
sentinel='exec '+shlex.join([sys.executable,str(owned_editor),'--owned-editor','sentinel','quoted argument'])
env=os.environ.copy();env.update(CC_HAHA_EDITOR_EXPECTED_HOME=str(home),VISUAL=chosen if scenario=='visual' else ' \t',EDITOR=sentinel if scenario=='visual' else chosen)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',40,132,0,0));before=tty(master);screen=VTScreen(132,40);raw=bytearray();deadline=time.monotonic()+28;cleanup=[];old_business=False;editor=None

def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestPublicRunCommandPromptEditorFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=env,preexec_fn=setup,close_fds=True,cwd=home)
def group():
 rows=subprocess.check_output(['/bin/ps','-axo','pid=,ppid=,pgid=,stat=,comm='],text=True)
 return [r.strip() for r in rows.splitlines() if len(r.split(None,4))==5 and int(r.split(None,4)[2])==p.pid]
def pump():
 if select.select([master],[],[],.04)[0]:
  try:block=os.read(master,65536)
  except OSError as exc:
   if exc.errno!=5:raise
   block=b''
  if block:raw.extend(block);screen.feed(block)
def line(text):return any(''.join(r).rstrip()==text for r in screen.lines)
def wait(predicate,label):
 while not predicate():
  assert time.monotonic()<deadline,label+' '+repr(bytes(raw[-4000:]))
  assert p.poll() is None,label+' process exited '+repr(bytes(raw[-4000:]))
  pump()
def send(data):assert b'\r' not in data and b'\n' not in data;os.write(master,data)
def exit_clean():
 send(b'\x03');wait(lambda:line('>'),'empty ordinary draft after cancel')
 send(b'\x04');wait(lambda:line('Press Ctrl-D again to exit'),'real Ctrl-D confirmation')
 send(b'\x04')
def no_pid(pid):
 try:os.kill(pid,0)
 except ProcessLookupError:return True
 return False
try:
 wait(lambda:line('>'),'ordinary RunCommand empty prompt')
 send(b'PUBLIC_ORIGINAL_DRAFT');wait(lambda:line('> PUBLIC_ORIGINAL_DRAFT'),'ordinary unsubmitted draft')
 send(b'\x07')
 wait(lambda:(home/'editor-started.json').exists() or 'External editor is not configured' in screen.text(),'real Ctrl-G host handoff')
 if 'External editor is not configured' in screen.text():
  old_business=True;assert not (home/'editor-started.json').exists();assert line('> PUBLIC_ORIGINAL_DRAFT');exit_clean()
 else:
  editor=json.loads((home/'editor-started.json').read_text());assert editor['mode']==scenario and editor['quotedArgument']=='quoted argument' and editor['pgid']==p.pid and editor['ppid']==p.pid and editor['canonical'] and editor['echo'] and editor['terminal']==before
  if scenario=='cancel':(home/'cancel-editor').write_text('actual caller context cancellation')
  elif scenario=='error':
   wait(lambda:'exited with code 7' in screen.text() and line('> PUBLIC_ORIGINAL_DRAFT'),'error retains original draft and resumes TUI');exit_clean()
  else:
   assert not (home/'editor-finished.json').exists();(home/'editor-release').write_text('release-owned-editor')
   wait(lambda:line('> PUBLIC_EDITED_DRAFT'),'real file readback visible in same ordinary TUI');assert not pathlib.Path(editor['path']).exists();exit_clean()
 while p.poll() is None:
  assert time.monotonic()<deadline,'natural RunCommand completion';pump()
 until=time.monotonic()+3
 while group() and time.monotonic()<until:time.sleep(.01)
 assert p.returncode==0 and not group() and tty(master)==before,(p.returncode,group(),tty(master),before)
 result=json.loads((home/'runcommand-result.json').read_text());assert result['modelRequests']==0
 assert result['contextCanceled']==(scenario=='cancel' and not old_business)
 if editor:
  assert no_pid(editor['pid']) and not pathlib.Path(editor['path']).exists()
  if scenario=='cancel':(home/'editor-release').write_text('release-owned-editor');assert not (home/'editor-finished.json').exists()
 assert not list(home.glob('claude-prompt-*'))
 outcome={'qualified':not old_business,'oldBusinessError':'PUBLIC_RUNCOMMAND_EXTERNAL_EDITOR_NOT_CONFIGURED' if old_business else None,'scenario':scenario,'terminalRestored':True,'modelRequests':0,'naturalChildExitCode':p.returncode,'ownedGroupRemaining':group(),'editor':editor,'RunCommand':result}
 print(json.dumps(outcome),flush=True)
 assert not old_business,'PUBLIC_RUNCOMMAND_EXTERNAL_EDITOR_NOT_CONFIGURED'
finally:
 (home/'prompt-editor-pty.raw').write_bytes(raw)
 for sig in [signal.SIGTERM,signal.SIGKILL]:
  if p.poll() is not None and not group():break
  cleanup.append(signal.Signals(sig).name)
  try:os.killpg(p.pid,sig)
  except ProcessLookupError:pass
  until=time.monotonic()+3
  while (p.poll() is None or group()) and time.monotonic()<until:
   try:p.wait(timeout=.05)
   except subprocess.TimeoutExpired:pass
 (home/'prompt-editor-cleanup.json').write_text(json.dumps({'signals':cleanup,'remaining':group()}))
 os.close(master);os.close(slave)
