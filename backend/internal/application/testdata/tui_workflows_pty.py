"""Actual public CLI /workflows shared runtime tree, pause, save and command."""
import fcntl,json,os,pathlib,re,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,140,0,0));captured=bytearray();deadline=time.monotonic()+32;screen=VTScreen(140,36)
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIWorkflowPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-5000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.22);pump()
def until(predicate,label):
 while not predicate():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' SCREEN '+repr(screen.text())+' RAW '+repr(bytes(captured[-5000:])))
  pump()
def idle():
 lines=screen.text().splitlines()
 return any(line.strip()=='>' and i+1<len(lines) and 'shift+tab to cycle' in lines[i+1] for i,line in enumerate(lines)) and not re.search(r'(?m)^\s*[·✢✳✶✻✽*]\s+.*…',screen.text())
def screen_words():return ' '.join(screen.text().split())
def menu():
 until(idle,'actual prompt before workflow menu');send(b'/workflows\r');until(lambda:'Dynamic workflow runs in this session' in screen.text(),'actual mounted workflow menu')
try:
 wait(b'Claude Code','startup');send(b'Launch the public workflow.\r');wait(b'WORKFLOW_PUBLIC_READY_564','actual Workflow tool')
 menu();until(lambda:'PUBLIC_PTY_WORKFLOW_RUN' in screen.text(),'actual live row');send(b'\r');until(lambda:'Ungrouped' in screen.text() and 'enter open phase' in screen_words(),'actual mounted run tree');send(b'\r');until(lambda:'enter inspect agent' in screen_words(),'actual mounted phase tree');send(b'\r');until(lambda:'Prompt: PRIVATE_PTY_WORKFLOW_AGENT_PROMPT' in screen_words(),'actual mounted child prompt detail');send(b'p');until(lambda:'resumeFromRunId' in ''.join(screen.text().split()),'actual pause resume result')
 menu();until(lambda:'paused' in screen.text(),'retained paused task');send(b'\r');until(lambda:'Workflow · pty-saved-workflow' in screen.text(),'actual retained paused run');send(b's');until(lambda:'Saved /pty-saved-workflow' in screen.text(),'actual dynamic save')
 work=pathlib.Path(pathlib.Path(home,'workflows-workdir.txt').read_text());saved=work/'.claude/workflows/pty-saved-workflow.js';assert saved.exists() and 'PRIVATE_PTY_WORKFLOW_AGENT_PROMPT' in saved.read_text()
 until(idle,'actual prompt after saved workflow');send(b'/pty-saved-workflow supplied arguments\r');wait(b'WORKFLOW_SAVED_COMMAND_DONE_564','same native saved command next round');until(idle,'actual idle next round before exit');send(b'\x04\x04')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout')
  pump()
 receipt=json.loads(pathlib.Path(home,'workflows-receipt.json').read_text());assert p.returncode==0 and receipt['terminalRestored'] and receipt['parentRequests']==3 and receipt['childRequests']==1 and receipt['canceledRequests']==1,(receipt,p.returncode,repr(bytes(captured[-5000:])))
 print(json.dumps({'terminalRestored':True,'actualWorkflowTree':True,'physicalWorkerCancelled':True,'pausedStateKept':True,'actualScriptSavedAndRegistered':True,'sameHarnessDynamicCommandNextRound':True,**receipt}))
finally:
 pathlib.Path(home,'workflows-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
