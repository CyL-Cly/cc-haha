"""Real RunCommand initial/resume remote tasks, TaskStop, HTTP overlap and exit."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home,profile=sys.argv[1:];root=pathlib.Path(home)
assert root.joinpath('owned-remote-task-pty').read_text()=='private'
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',42,150,0,0));before=termios.tcgetattr(slave);captured=bytearray();screen=VTScreen(150,42);deadline=time.monotonic()+38
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTerminalRemoteTasksPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.03)[0]:
  try:data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def wait(text,label):
 while text not in screen.text():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-16000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.14);pump()
def file(name):
 while not root.joinpath(name).exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('missing '+name+' '+repr(bytes(captured[-16000:])))
  pump()
try:
 wait('Claude Code','ordinary CLI startup')
 if profile=='slash-resume':send(b'/resume 34343434-3434-4434-8434-343434343434\r')
 file('remote-held-started');file('remote-exit-started')
 send(b'PUBLIC_HUMAN_WHILE_REMOTE_HELD\r');wait('PUBLIC_REMOTE_TASK_NEXT_HUMAN_COMPLETE_1','same Harness HTTP while both actual remote polls held')
 send(b'/tasks\r');wait('Background tasks','actual task menu');wait('Owned held','restored task row')
 send(b'\x1b[H')
 for _ in range(8):
  if any(line.startswith('> ') and 'Owned held' in line for line in screen.text().splitlines()):break
  send(b'\x1b[B')
 else:raise AssertionError('restored real task row not selectable '+screen.text())
 send(b'x');file('remote-held-archived');assert root.joinpath('remote-held-archived').read_text()=='{}'
 wait('Owned held · killed','actual TaskStop without cancelling its in-flight poll');send(b'\x1b')
 send(b'PUBLIC_HUMAN_AFTER_REMOTE_TASK_STOP\r');wait('PUBLIC_REMOTE_TASK_NEXT_HUMAN_COMPLETE_2','next actual model after remote Stop')
 send(b'\x04\x04');file('remote-task-receipt.json')
 receipt=json.loads(root.joinpath('remote-task-receipt.json').read_text());assert receipt['models']==2 and receipt['archives']==1 and receipt['globalExitPreservesRemote'] and receipt['terminalRestored'] and receipt['exitCode']==0,receipt
 assert termios.tcgetattr(slave)==before,'physical slave termios not restored'
 root.joinpath('remote-task-pty-release').write_text('observed')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('ordinary CLI remained alive')
  pump()
 assert p.returncode==0,p.returncode
 print(json.dumps(receipt))
finally:
 root.joinpath('remote-task-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait(timeout=3)
 os.close(master);os.close(slave)
