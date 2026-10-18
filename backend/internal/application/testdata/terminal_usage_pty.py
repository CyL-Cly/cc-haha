"""Actual RunCommand Usage HTTP, retry, cancel, next-human and restored PTY."""
import fcntl,json,os,pathlib,re,select,struct,subprocess,sys,termios,time
binary,home,profile=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,132,0,0))
before=termios.tcgetattr(slave);captured=bytearray();deadline=time.monotonic()+38
def setup():
 os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTerminalUsagePTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-7000:])))
  pump()
def send(value):
 os.write(master,value);time.sleep(.14);pump()
def command(value,needle,label):
 start=len(captured);send(value.encode()+b'\r');wait(needle,label,start)
def wait_background(parameters,label,start):
 while True:
  for sequence,text in re.findall(rb'\x1b\[([0-9;]+)m([^\x1b]*)',bytes(captured[start:])):
   codes=[int(value) for value in sequence.split(b';')]
   if any(codes[i:i+len(parameters)]==parameters for i in range(len(codes))) and b'\xe2\x96\x88' in text:return
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-7000:])))
  pump()
try:
 wait(b'Claude Code','startup')
 if profile=='subscriber':
  command('/usage',b'owned-usage-error','actual subscriber Usage HTTP error')
  start=len(captured);send(b'r');wait(b'23% used','real retry utilization',start)
  for label in [b'Current session',b'Current week (all models)',b'Current week (Sonnet only)',b'Extra usage',b'$23.90 / $100.00 spent']:
   wait(label,'actual utilization field '+repr(label),start)
  wait_background([48,2,80,83,112],'actual Source dark bar background',start)
  send(b'\x1b')
  command('PTY_USAGE_NEXT_HUMAN',b'PUBLIC_USAGE_NEXT_HUMAN_COMPLETE_1','same Harness next human')
  command('/usage',b'Loading usage data','real held third HTTP')
  held=pathlib.Path(home,'usage-held-http.started')
  while not held.exists():
   if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('actual third HTTP did not start '+repr(bytes(captured[-7000:])))
   pump()
  assert held.read_text()=='/api/oauth/usage','held request consumer differs'
  send(b'\x1b')
  command('PTY_USAGE_AFTER_CANCEL',b'PUBLIC_USAGE_NEXT_HUMAN_COMPLETE_2','next human after physical HTTP cancel')
 else:
  command('/status',b'Usage','actual settings tabs')
  start=len(captured);send(b'\x1b[D');wait(b'[Usage]','actual previous settings tab',start);wait(b'/usage is only available for subscription plans.','normal API-key Usage tab negative gate',start)
  send(b'\x1b')
  command('PTY_API_KEY_AFTER_USAGE',b'PUBLIC_USAGE_NEXT_HUMAN_COMPLETE_1','API-key next human')
 send(b'\x04\x04')
 receipt_file=pathlib.Path(home,'usage-receipt.json')
 while not receipt_file.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('normal exit receipt missing '+repr(bytes(captured[-5000:])))
  pump()
 receipt=json.loads(receipt_file.read_text());assert receipt['terminalRestored'] and receipt['code']==0,receipt
 assert receipt['api']==(3 if profile=='subscriber' else 0),receipt
 assert receipt['models']==(2 if profile=='subscriber' else 1),receipt
 assert termios.tcgetattr(slave)==before,'real slave termios not restored'
 pathlib.Path(home,'usage-pty-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('physical CLI remained pending')
  pump()
 assert p.returncode==0,p.returncode
 print(json.dumps({'terminalRestored':True,'originalGate':profile,'actualUsageHTTP':receipt['api'],'sameHarnessNextHuman':receipt['models'],'exitCode':p.returncode}))
finally:
 pathlib.Path(home,'usage-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
