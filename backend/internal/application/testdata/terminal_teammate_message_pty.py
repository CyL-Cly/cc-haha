"""Actual external canonical mailbox XML, same RunCommand next human, live termios."""
import fcntl,json,os,pathlib,select,signal,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:];home=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,132,0,0))
before=termios.tcgetattr(slave);screen=VTScreen(132,36);captured=bytearray();deadline=time.monotonic()+30

def setup():
 os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTerminalTeammateMessagePTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:
   data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def until(predicate,label,seconds=None):
 (home/'teammate-origin-stage.txt').write_text(label)
 stop=min(deadline,time.monotonic()+seconds) if seconds else deadline
 while not predicate():
  if time.monotonic()>stop or p.poll() is not None:
   # The process may exit between the predicate's poll and this poll.
   if predicate():return
   raise AssertionError(label+' SCREEN '+repr(screen.text())+' RAW '+repr(bytes(captured[-6000:])))
  pump()
def send(value):os.write(master,value)
def ready():return any(line=='>' or line.startswith('> ') for line in screen.text().splitlines())
try:
 until(ready,'actual public startup')
 send(b'PUBLIC_ORIGIN_FIRST_HUMAN\r')
 until(lambda:'PUBLIC_ORIGIN_ATTACHMENT_DONE' in screen.text() and ready(),'actual TeamCreate, mailbox attachment and Read')
 until(lambda:'PUBLIC_ORIGIN_MAILBOX_DONE' in screen.text() and ready(),'actual external autonomous XML query and Read')
 until(lambda:'Task #12 assigned by team-lead' in screen.text() and 'PUBLIC_ORIGIN_ASSIGNED_TASK' in screen.text(),'actual UserTeammateMessage assignment branch')
 assert 'Task assigned: #12' not in screen.text(),'external canonical XML incorrectly rendered as an ant attachment'
 send(b'PUBLIC_ORIGIN_NEXT_HUMAN\r')
 until(lambda:'PUBLIC_ORIGIN_NEXT_DONE' in screen.text() and ready(),'same Harness next human and actual Read')
 send(b'\x04\x04')
 receipt=home/'teammate-origin-receipt.json';until(receipt.exists,'actual close and restoration',3)
 assert json.loads(receipt.read_text())=={'code':0,'requests':7,'terminalRestored':True}
 assert termios.tcgetattr(slave)==before,'live slave terminal changed'
 (home/'teammate-origin-restoration-inspected').write_text('actual still-live leader inspected')
 until(lambda:p.poll() is not None,'normal helper exit');assert p.returncode==0,p.returncode
 print(json.dumps({'terminalRestored':True,'actualModelRequests':7,'realExternalMailboxXML':True,'nextHumanActualRead':True,'normalExit':0}))
finally:
 (home/'teammate-origin-screen.txt').write_text(screen.text());(home/'teammate-origin-terminal.txt').write_bytes(captured)
 if p.poll() is None:
  p.send_signal(signal.SIGQUIT);stop=time.monotonic()+2
  while p.poll() is None and time.monotonic()<stop:pump()
  (home/'teammate-origin-stacks.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
