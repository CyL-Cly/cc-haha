"""Real RunCommand: saved setting env, task directories, next human and cleanup."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,100,0,0))
before=termios.tcgetattr(slave);screen=VTScreen(100,36);captured=bytearray();deadline=time.monotonic()+30
def setup():
 os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTaskListEnvironmentPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def until(predicate,label):
 while not predicate():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' SCREEN '+repr(screen.text())+' RAW '+repr(bytes(captured[-5000:])))
  pump()
def send(value):os.write(master,value)
def ready():return any(line.startswith('> ') or line=='>' for line in screen.text().splitlines())
def task_rows():
 lines=screen.text().splitlines();indices=[i for i,line in enumerate(lines) if '1 tasks (' in line]
 return '\n'.join(lines[indices[-1]:indices[-1]+3]) if indices else ''
try:
 until(ready,'actual startup prompt');assert 'No tasks' not in screen.text()
 send(b'TASK_ENV_HUMAN_1\r');until(lambda:'PUBLIC_TASK_ENV_DONE_1' in screen.text() and 'ORIGINAL_LIST_TASK' in task_rows() and ready(),'actual first task/store')
 until(lambda:root.joinpath('task-env-paths.json').exists(),'actual private paths')
 paths=json.loads(root.joinpath('task-env-paths.json').read_text());original=pathlib.Path(paths['original']);next_list=pathlib.Path(paths['next']);original_file=original/'1.json';old_bytes=original_file.read_bytes()
 settings=pathlib.Path(paths['settings']);value=json.loads(settings.read_text());assert value['futureTaskSettings']=={'keep':True}
 value['env']={'CLAUDE_CODE_TASK_LIST_ID':'actual-next-list'}
 # An actual owned StatusLine shell supplies the committed environment receipt.
 # No private map/getter, artificial state event or delay signals this transition.
 value['statusLine']={'type':'command','command':'printf \'TASK_ENV_APPLIED_%s\\n\' "$CLAUDE_CODE_TASK_LIST_ID"'}
 settings.write_text(json.dumps(value))
 until(lambda:'TASK_ENV_APPLIED_actual-next-list' in screen.text(),'actual settings watcher/env application/StatusLine shell')
 send(b'TASK_ENV_HUMAN_2\r');until(lambda:'PUBLIC_TASK_ENV_DONE_2' in screen.text() and 'NEXT_LIST_TASK' in task_rows() and ready(),'actual next TaskCreate/rewatched list')
 assert 'ORIGINAL_LIST_TASK' not in task_rows(),task_rows()
 assert original_file.read_bytes()==old_bytes and len(list(original.glob('[0-9]*.json')))==1
 assert json.loads((next_list/'1.json').read_text())['subject']=='NEXT_LIST_TASK' and len(list(next_list.glob('[0-9]*.json')))==1
 state=json.loads(pathlib.Path(paths['global']).read_text());assert state['showExpandedTodos'] is True and state['taskListFuture']=={'keep':True}
 assert json.loads(settings.read_text())['futureTaskSettings']=={'keep':True}
 send(b'/help\r');until(lambda:'Claude understands your codebase' in screen.text(),'actual local JSX Help');assert not task_rows()
 send(b'\x1b');until(lambda:'Claude understands your codebase' not in screen.text() and 'NEXT_LIST_TASK' in task_rows() and ready(),'same task component after actual onDone')
 send(b'TASK_ENV_HUMAN_3\r');until(lambda:'PUBLIC_TASK_ENV_DONE_3' in screen.text() and ready(),'next same-Harness human with canonical Help')
 send(b'\x04\x04');receipt=root/'task-env-receipt.json';until(receipt.exists,'owned watcher/shell/native-read join and terminal restoration')
 assert json.loads(receipt.read_text())=={'exitCode':0,'requests':5,'terminalRestored':True}
 assert termios.tcgetattr(slave)==before,'physical termios not restored'
 root.joinpath('task-env-restoration-inspected').write_text('same live leader inspected')
 until(lambda:p.poll() is not None,'normal exit');assert p.returncode==0
 print(json.dumps({'requests':5,'actualSettingsEnvironment':True,'oldListPreserved':True,'actualToolAndWatcher':True,'canonicalNextHuman':True,'terminalRestored':True,'exitCode':0}))
finally:
 root.joinpath('task-env-terminal.txt').write_bytes(captured);root.joinpath('task-env-final-screen.txt').write_text(screen.text())
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
