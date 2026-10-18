"""Public RunCommand aliases, next model history/SID and real PTY restoration."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',32,132,0,0))
before=termios.tcgetattr(slave);captured=bytearray();deadline=time.monotonic()+32;screen=VTScreen(132,32)
def setup():
 os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTerminalClearPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:
   data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-5000:])))
  pump()
def send(value):
 os.write(master,value);time.sleep(.12);pump()
def wait_clear(index,alias):
 while True:
  visible=screen.text()
  # Clear remounts the original logo, but the terminal renderer can retain
  # unchanged title cells and emit only a history delta. Consume the actual
  # screen: the original full Message renderer displays the canonical command
  # and empty stdout as /clear and (no content), while storage retains tags.
  if 'Claude Code' in visible and '/clear' in visible and '(no content)' in visible and 'PUBLIC_CLEAR_HUMAN_'+str(index-1) not in visible and 'PUBLIC_CLEAR_HUMAN_COMPLETE_'+str(index-1) not in visible:return
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('original current screen after '+alias+' SCREEN '+repr(visible)+' RAW '+repr(bytes(captured[-5000:])))
  pump()
try:
 wait(b'Claude Code','startup')
 for index,alias in enumerate([None,'clear','reset','new'],1):
  if alias:
   send(('/'+alias+'\r').encode());wait_clear(index,alias)
   pathlib.Path(home,'clear-'+alias+'-screen.txt').write_text(screen.text())
  start=len(captured);send(('PUBLIC_CLEAR_HUMAN_'+str(index)+'\r').encode());wait(('PUBLIC_CLEAR_HUMAN_COMPLETE_'+str(index)).encode(),'same Harness after '+str(alias),start)
 send(b'\x04\x04')
 receipt_file=pathlib.Path(home,'clear-receipt.json')
 while not receipt_file.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('normal clear exit receipt missing '+repr(bytes(captured[-5000:])))
  pump()
 receipt=json.loads(receipt_file.read_text());assert receipt['terminalRestored'] and receipt['code']==0,receipt
 assert len(receipt['sids'])==4 and len(set(receipt['sids']))==4,receipt
 assert termios.tcgetattr(slave)==before,'real slave termios not restored'
 pathlib.Path(home,'clear-pty-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('actual public CLI remained pending')
  pump()
 assert p.returncode==0,p.returncode
 print(json.dumps({'terminalRestored':True,'aliases':['clear','reset','new'],'actualModelRequests':4,'uniqueCurrentSIDs':4,'exitCode':p.returncode}))
finally:
 pathlib.Path(home,'clear-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
