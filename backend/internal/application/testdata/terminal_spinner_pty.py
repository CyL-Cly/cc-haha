"""Real ParseCommand/RunCommand stream, task verb, permission and next human."""
import fcntl,json,os,pathlib,re,select,signal,struct,subprocess,sys,termios,time
from vt_screen import VTScreen

binary,home=sys.argv[1:]
home=pathlib.Path(home)
master,slave=os.openpty()
fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,132,0,0))
before=termios.tcgetattr(slave)
screen=VTScreen(132,36);captured=bytearray();deadline=time.monotonic()+30;glyphs=set()
def setup():
 os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTerminalSpinnerPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:
   data=os.read(master,65536);captured.extend(data);screen.feed(data)
   line=header().lstrip()
   if line and line[0] in ('·','✢','✳','✶','✻','✽','*'):glyphs.add(line[0])
  except OSError:pass
def until(predicate,label,seconds=None):
 (home/'spinner-driver-stage.txt').write_text(label)
 stop=min(deadline,time.monotonic()+seconds) if seconds else deadline
 while not predicate():
  if time.monotonic()>stop or p.poll() is not None:
   raise AssertionError(label+' SCREEN '+repr(screen.text())+' RAW '+repr(bytes(captured[-6000:])))
  pump()
def send(value):os.write(master,value)
def ready():return any(line=='>' or line.startswith('> ') for line in screen.text().splitlines())
def header():
 return next((line for line in screen.text().splitlines() if 'PUBLIC_SPINNER_ACTIVE…' in line),'')
def release(stage):(home/('spinner-release-'+stage)).write_text('actual VT inspected')
try:
 until(ready,'actual public startup prompt')
 send(b'PUBLIC_SPINNER_HUMAN_1\r')
 until(lambda:(home/'spinner-stage-thinking').exists() and header(),'actual task + stream spinner')
 until(lambda:re.search(r'\d+(?:\.\d+)?[kmbt]? tokens',header()) is not None,'actual UTF16 response ref shown by animation')
 until(lambda:len(glyphs)>=2,'actual live spinner clock advances glyphs',2)
 release('thinking')
 until(lambda:(home/'spinner-stage-partial').exists(),'actual partial delta flushed')
 until(lambda:bool(header()),'partial line must retain original Spinner',2)
 release('partial')
 until(lambda:(home/'spinner-stage-newline').exists(),'actual newline delta flushed')
 until(lambda:not header(),'complete streaming line unmounts Spinner',2)
 release('newline')
 until(lambda:'spinner-physical.txt' in screen.text() and ('Allow once' in screen.text() or 'Yes' in screen.text() or 'Allow Write?' in screen.text()),'actual default permission dialog')
 assert not header(),'permission retained animated spinner'
 send(b'\r')
 until(lambda:'PUBLIC_SPINNER_WRITE_DONE' in screen.text() and ready() and not header(),'actual approved Write + query completion')
 assert not header(),'idle turn retained animated spinner'
 send(b'/help\r');until(lambda:'Claude understands your codebase' in screen.text(),'actual Help local JSX')
 assert not header(),'local JSX retained animated spinner'
 send(b'\x1b');until(lambda:'Claude understands your codebase' not in screen.text() and ready(),'actual Help onDone')
 send(b'PUBLIC_SPINNER_NEXT_HUMAN\r')
 until(lambda:'PUBLIC_SPINNER_NEXT_DONE' in screen.text() and ready(),'same Harness next human + canonical records')
 send(b'\x04\x04')
 receipt=home/'spinner-pty-receipt.json'
 until(receipt.exists,'global close joins actual runtime and returns terminal',3)
 assert json.loads(receipt.read_text())=={'code':0,'requests':6,'terminalRestored':True}
 assert termios.tcgetattr(slave)==before,'live slave terminal not restored'
 (home/'spinner-restoration-inspected').write_text('same live leader inspected')
 until(lambda:p.poll() is not None,'native helper normal exit');assert p.returncode==0,p.returncode
 print(json.dumps({'terminalRestored':True,'actualModelRequests':6,'actualLiveGlyphs':len(glyphs),'partialLineKeepsSpinner':True,'newlineUnmountsSpinner':True,'actualTaskVerb':True,'permissionUnmount':True,'actualWriteRead':True,'canonicalNextHuman':True,'exitCode':0}))
finally:
 (home/'spinner-terminal.txt').write_bytes(captured)
 (home/'spinner-final-screen.txt').write_text(screen.text())
 if p.poll() is None:
  p.send_signal(signal.SIGQUIT)
  stop=time.monotonic()+2
  while p.poll() is None and time.monotonic()<stop:pump()
  (home/'spinner-blocked-stacks.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
