"""Public RunCommand: actual task tools, local JSX mount and durable reset."""
import fcntl,json,os,pathlib,select,signal,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,100,0,0))
before=termios.tcgetattr(slave);screen=VTScreen(100,36);captured=bytearray();deadline=time.monotonic()+30
def setup():
 os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTaskListPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def until(predicate,label):
 while not predicate():
  if time.monotonic()>deadline or p.poll() is not None:
   # The process can exit between the predicate and poll. Recheck the
   # requested terminal condition before treating an exit as premature.
   if predicate():return
   raise AssertionError(label+' SCREEN '+repr(screen.text())+' RAW '+repr(bytes(captured[-4000:])))
  pump()
def send(value):os.write(master,value)
def ready():return any(line.startswith('> ') or line=='>' for line in screen.text().splitlines())
def tasks():return '2 tasks (' in screen.text()
def global_state(value):
 path=pathlib.Path(pathlib.Path(home,'task-list-global-path').read_text())
 data=json.loads(path.read_text())
 return data.get('showExpandedTodos',False) is value and data.get('taskListFuture')=={'keep':True}
def cleared():
 path=pathlib.Path(pathlib.Path(home,'task-list-directory').read_text())
 return path.exists() and not list(path.glob('[0-9]*.json')) and path.joinpath('.highwatermark').read_text()=='2'
try:
 until(ready,'actual startup prompt');assert 'No tasks' not in screen.text(),'empty task component painted'
 send(b'TASK_LIST_HUMAN_1\r');until(lambda:'TASK_LIST_DONE_1' in screen.text() and ready() and tasks(),'actual TaskCreate autoexpand')
 assert 'PUBLIC_TASK_SUBJECT_1' in screen.text() and 'PUBLIC_TASK_SUBJECT_2' in screen.text()
 until(lambda:global_state(True),'actual tool AppState global callback')
 send(b'\x14');until(lambda:not tasks() and global_state(False),'committed Ctrl-T collapse')
 send(b'\x14');until(lambda:tasks() and global_state(True),'committed Ctrl-T expand')
 send(b'/help\r');until(lambda:'Claude understands your codebase' in screen.text(),'real local JSX Help')
 assert not tasks(),'local JSX retained standalone TaskList'
 send(b'\x14');until(lambda:global_state(False),'root binding during local JSX')
 send(b'\x14');until(lambda:global_state(True),'root binding preserves local JSX completion')
 assert not tasks(),'expanded TaskList painted in local JSX'
 send(b'\x1b');until(lambda:'Claude understands your codebase' not in screen.text() and tasks() and ready(),'onDone restores actual task component')
 send(b'/model\r');until(lambda:'Select model' in screen.text(),'actual slash model consumer')
 assert not tasks(),'slash model local JSX retained TaskList'
 send(b'\x1b');until(lambda:'Select model' not in screen.text() and tasks() and ready(),'slash model onDone restores TaskList')
 send(b'\x1bp');until(lambda:'Select model' in screen.text(),'actual PromptInput model shortcut')
 assert tasks(),'PromptInput model shortcut removed the standalone TaskList'
 send(b'\x1b');until(lambda:'Select model' not in screen.text() and tasks() and ready(),'input model callback restores prompt')
 send(b'TASK_LIST_HUMAN_2\r');until(lambda:'TASK_LIST_DONE_2' in screen.text() and '2 tasks (2 done, 0 open)' in screen.text(),'actual TaskUpdate completes both')
 until(lambda:cleared() and not tasks() and global_state(False),'real five-second file reset/collapse')
 assert 'No tasks' not in screen.text(),'hidden task component painted empty text'
 send(b'TASK_LIST_HUMAN_3\r');until(lambda:'TASK_LIST_DONE_3' in screen.text() and ready(),'next same Harness human + canonical Help')
 send(b'\x04\x04');receipt=pathlib.Path(home,'task-list-receipt.json');until(receipt.exists,'global close joins actual task watcher/native reads')
 assert json.loads(receipt.read_text())=={'code':0,'requests':7,'terminalRestored':True}
 assert termios.tcgetattr(slave)==before,'physical termios not restored'
 pathlib.Path(home,'task-list-restoration-inspected').write_text('same live leader inspected')
 until(lambda:p.poll() is not None,'actual fixture normal exit');assert p.returncode==0
 print(json.dumps({'terminalRestored':True,'actualModelRequests':7,'actualTaskCreateUpdateReset':True,'canonicalHelp':True,'exitCode':0}))
finally:
 if p.poll() is None:
  # Preserve the actual blocked Go stacks after a failed original deadline.
  # This diagnostic does not extend any successful behavior wait.
  p.send_signal(signal.SIGQUIT)
  diagnostic=time.monotonic()+2
  while p.poll() is None and time.monotonic()<diagnostic:pump()
 pathlib.Path(home,'task-list-terminal.txt').write_bytes(captured);pathlib.Path(home,'task-list-final-screen.txt').write_text(screen.text())
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
