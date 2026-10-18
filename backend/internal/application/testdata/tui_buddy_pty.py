"""Actual RunCommand Buddy state, private global, Write/Harness and physical termios."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',42,120,0,0));before=termios.tcgetattr(slave);screen=VTScreen(120,42);captured=bytearray();deadline=time.monotonic()+22
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIBuddyPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def wait(text):
 while text not in screen.text():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(text+' '+repr(bytes(captured[-10000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.12);pump()
def file(name):
 while not root.joinpath(name).exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('Missing '+name+' '+repr(bytes(captured[-6000:])))
  pump()
def ambient(name):return any(name in line[80:] for line in screen.text().splitlines())
def wait_ambient(name,present):
 while ambient(name)!=present:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('Ambient '+name+' '+str(present)+' '+screen.text())
  pump()
def wait_card_closed():
 while 'Press q or Enter to dismiss' in screen.text():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('Actual Buddy card unmount '+screen.text())
  pump()
def wait_pet():
 while not any('♥' in line[80:] for line in screen.text().splitlines()):
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('Actual pet frame '+screen.text())
  pump()
def wait_global(predicate):
 while True:
  value=json.loads(globalpath.read_text())
  if predicate(value):return value
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('Actual global transition '+repr(value)+' '+screen.text())
  pump()
try:
 wait('Claude Code');file('buddy-paths.json');paths=json.loads(root.joinpath('buddy-paths.json').read_text());globalpath=pathlib.Path(paths['global']);wait_ambient('Public Buddy',True)
 send(b'\x1b[B');send(b'\r');wait('Press q or Enter to dismiss');send(b'q');wait_card_closed();wait_ambient('Public Buddy',True)
 send(b'/buddy info\r');wait('Press q or Enter to dismiss');send(b'\r');assert 'Press q or Enter to dismiss' in screen.text(),screen.text();send(b'\x1b');assert 'Press q or Enter to dismiss' in screen.text(),screen.text();send(b'q');wait_card_closed();wait_ambient('Public Buddy',True);wait('❯ /buddy info')
 send(b'/buddy pet\r');wait_ambient('Public Buddy',True);wait_pet();wait('❯ /buddy pet');wait('⎿  You pet Public Buddy! ♥')
 send(b'/buddy mute\r');wait_global(lambda value:value.get('companionMuted') is True);wait_ambient('Public Buddy',False);wait('❯ /buddy mute');wait('⎿  Public Buddy is now muted.')
 send(b'/buddy unmute\r');wait_global(lambda value:value.get('companionMuted') is False);wait_ambient('Public Buddy',True)
 send(b'/buddy release\r');wait_global(lambda value:'companion' not in value);wait_ambient('Public Buddy',False)
 send(b'/buddy pet\r');send(b'/buddy hatch\r');value=wait_global(lambda value:'companion' in value);name=value['companion']['name'];assert value['futureBuddy']=={'keep':True} and value['companion']['appearanceSeed'].startswith('hatch:');wait_ambient(name,True)
 send(b'BUDDY_FIRST_HUMAN\r');wait('BUDDY_FIRST_DONE');assert pathlib.Path(paths['written']).read_text()=='BUDDY_PHYSICAL_WRITE'
 send(b'/buddy pet\r');wait_ambient(name,True);send(b'BUDDY_NEXT_HUMAN\r');wait('BUDDY_NEXT_DONE');send(b'\x04\x04');file('buddy-receipt.json');receipt=json.loads(root.joinpath('buddy-receipt.json').read_text());assert receipt['requests']==3 and receipt['invalid']==0 and receipt['exitCode']==0 and receipt['actualWrite'] and receipt['terminalRestored'] and termios.tcgetattr(slave)==before,receipt;root.joinpath('buddy-termios-release').write_text('Actual slave restored')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('Normal exit deadline')
  pump()
 assert p.returncode==0,receipt
 print(json.dumps({'actualBuddyCardAndKeyQuirk':True,'actualAmbientAndPet':True,'actualGlobalActions':True,'canonicalIntroAndSameHarness':True,**receipt}))
finally:
 root.joinpath('buddy-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait(timeout=3)
 os.close(master);os.close(slave)
