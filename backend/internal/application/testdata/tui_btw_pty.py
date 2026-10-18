"""Actual /btw immediate mount, two sockets, native cancellation, Write and termios."""
import fcntl,json,os,pathlib,re,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',35,100,0,0));before=termios.tcgetattr(slave);screen=VTScreen(100,35);captured=bytearray();deadline=time.monotonic()+25
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIBtwPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def wait(text):
 while text not in screen.text():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(text+' '+repr(bytes(captured[-12000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.12);pump()
def file(name):
 while not root.joinpath(name).exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('Missing '+name+' '+repr(bytes(captured[-8000:])))
  pump()
def bold_answer():
 bold=False
 for match in re.finditer(rb'\x1b\[([0-9;]*)m|([^\x1b]+)',bytes(captured)):
  if match.group(1) is not None:
   for code in match.group(1).split(b';'):
    if code in (b'',b'0',b'22'):bold=False
    elif code==b'1':bold=True
  elif bold and b'BTW_SIDE_ANSWER_LINE_A' in match.group(2):return True
 return False
try:
 wait('Claude Code');file('btw-paths.json');paths=json.loads(root.joinpath('btw-paths.json').read_text());globalpath=pathlib.Path(paths['global'])
 send(b'BTW_HELD_MAIN\r');file('btw-main-started');send(b'/btw \r');wait('Usage: /btw');send(b'/btw BTW_SIDE_HELD\r');file('btw-side-first-started');wait('Answering...');assert 'BTW_PARENT_FIRST_DONE' not in screen.text();send(b'\x1b');file('btw-side-first-cancelled');root.joinpath('btw-release-main').write_text('Parent continues');wait('BTW_PARENT_FIRST_DONE')
 send(b'/btw BTW_SIDE_ANSWERED\r');wait('BTW_SIDE_ANSWER_LINE_B');send(b'\x1b[A');wait('BTW_SIDE_ANSWER_LINE_A');assert '**BTW_SIDE_ANSWER_LINE_A**' not in screen.text() and bold_answer(),screen.text();send(b'\x1b[B');assert 'BTW_SIDE_ANSWER_LINE_D' in screen.text() and 'BTW_SIDE_ANSWER_LINE_A' not in screen.text(),screen.text();send(b' ')
 send(b'BTW_NEXT_HUMAN\r');wait('BTW_NEXT_HUMAN_DONE');assert pathlib.Path(paths['written']).read_text()=='BTW_SHARED_HARNESS_WRITE';value=json.loads(globalpath.read_text());assert value['btwUseCount']==2 and value['futureBtw']=={'keep':True},value
 send(b'BTW_GLOBAL_CLOSE_MAIN\r');file('btw-close-main-started');send(b'/btw BTW_SIDE_CLOSE\r');file('btw-close-side-started');send(b'\x03');file('btw-close-side-cancelled');send(b'\x04\x04');file('btw-receipt.json');receipt=json.loads(root.joinpath('btw-receipt.json').read_text());assert receipt['requests']==7 and receipt['invalid']==0 and receipt['sideCanceled']==2 and receipt['mainCanceled']==1 and receipt['exitCode']==0 and receipt['actualWrite'] and receipt['terminalRestored'] and termios.tcgetattr(slave)==before,receipt;root.joinpath('btw-termios-release').write_text('Actual slave restored')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('Normal exit deadline')
  pump()
 assert p.returncode==0,receipt
 print(json.dumps({'immediateBusyEmptyAndFork':True,'sourceScrollDismiss':True,'parentContinuesAndEphemeralHistory':True,'actualNextWrite':True,'ownedCancellationAndJoin':True,**receipt}))
finally:
 root.joinpath('btw-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait(timeout=3)
 os.close(master);os.close(slave)
