"""Actual public Go CLI resume picker, owned PTY and loopback provider only."""
import fcntl,json,os,pathlib,select,signal,struct,subprocess,sys,termios,time
binary,home,profile,flags_json=sys.argv[1:]
flags=json.loads(flags_json)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',28,100,0,0));before=termios.tcgetattr(slave)
buffer=bytearray();deadline=time.monotonic()+25

def setup():
 os.setsid()

p=subprocess.Popen([binary,*flags],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),cwd=home,preexec_fn=setup,close_fds=True)
def pump(t=.02):
 readable,_,_=select.select([master],[],[],t)
 if readable:
  try: buffer.extend(os.read(master,65536))
  except OSError: pass

def wait(predicate,label):
 while not predicate():
  if time.monotonic()>deadline: raise AssertionError('deadline '+label+' '+buffer.decode(errors='replace'))
  if p.poll() is not None: raise AssertionError('premature exit '+label+' '+str(p.returncode)+' '+buffer.decode(errors='replace'))
  pump()

try:
 wait(lambda:b'Resume session' in buffer or b'No conversations found' in buffer,'real picker')
 assert not pathlib.Path(home,'pr-resume-produced.txt').exists(), 'model side effect happened before selection'
 if profile=='cancel':
  os.write(master,b'\x1b')
  while p.poll() is None:
   if time.monotonic()>deadline: raise AssertionError('cancel did not exit')
   pump()
  assert p.returncode==1, p.returncode
 else:
  if profile=='all-search':
   os.write(master,b'BetaTitle');wait(lambda:b'Search: BetaTitle' in buffer,'real search filter')
  os.write(master,b'\r')
  wait(lambda:b'Claude Code' in buffer,'same Go runtime resumed TUI')
  os.write(master,b'Continue and read the requested physical input.\r')
  wait(lambda:b'PR_PICKER_TOOL_AND_HISTORY_OK' in buffer,'actual resumed tool turn')
  os.write(master,b'\x04\x04')
  while p.poll() is None:
   if time.monotonic()>deadline:raise AssertionError('resumed TUI did not exit')
   pump()
  assert p.returncode==0,(p.returncode,buffer.decode(errors='replace'))
 pump(0)
 raw_after=termios.tcgetattr(slave)
 if sys.platform=='darwin':
  # Ask XNU to settle its PENDIN state, preserving both raw observations.
  # FIONREAD observes the queue without consuming input or changing flags.
  fcntl.ioctl(slave,0x4004667f,struct.pack('i',0))
 observed_after=termios.tcgetattr(slave)
 restored=observed_after==before
 assert restored,('native terminal flags were not restored',before,raw_after,observed_after)
 pathlib.Path(home,'picker-terminal.raw').write_bytes(buffer)
 print(json.dumps({'exitCode':p.returncode,'restored':restored,'profile':profile,'rawBefore':before,'rawAfter':raw_after,'observedAfter':observed_after},default=lambda value:list(value)))
finally:
 if p.poll() is None:
  os.killpg(p.pid,signal.SIGKILL);p.wait()
 os.close(master);os.close(slave)
