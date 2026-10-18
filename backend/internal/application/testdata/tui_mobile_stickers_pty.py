"""Public CLI mobile QR/tabs/aliases, real browser child, next Harness and restoration."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',42,120,0,0));before=termios.tcgetattr(slave);screen=VTScreen(120,42);captured=bytearray();deadline=time.monotonic()+22
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIMobileStickersPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
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
try:
 wait('Claude Code');send(b'/mobile\r');wait('https://apps.apple.com/app/claude-by-anthropic/id6473753684');assert any(c in screen.text() for c in '█▀▄'),screen.text()
 send(b'\t');wait('https://play.google.com/store/apps/details?id=com.anthropic.claude');send(b'\x1b[D');wait('[iOS] / Android');send(b'\x1b[C');wait('iOS / [Android]');send(b'\r');wait('iOS / [Android]');send(b'n');wait('\n>')
 send(b'/ios ignored-android\r');wait('[iOS] / Android');send(b'q');wait('\n>');send(b'/android\r');wait('[iOS] / Android');send(b'\x1b');wait('\n>')
 send(b'/mobile\r');wait('[iOS] / Android');send(b'\x03');wait('\n>');send(b'/stickers ignored\r');wait('Opening sticker page in browser');file('mobile-browser-receipt');assert root.joinpath('mobile-browser-receipt').read_text()=='https://www.stickermule.com/claudecode\n'
 send(b'MOBILE_STICKERS_NEXT_HUMAN\r');wait('MOBILE_STICKERS_NEXT_DONE');send(b'\x04\x04');file('mobile-receipt.json');receipt=json.loads(root.joinpath('mobile-receipt.json').read_text());assert receipt['requests']==1 and receipt['invalid']==0 and receipt['exitCode']==0 and receipt['actualBrowserChild'] and receipt['terminalRestored'] and termios.tcgetattr(slave)==before,receipt;root.joinpath('mobile-termios-release').write_text('Actual slave restored')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('Normal exit deadline')
  pump()
 assert p.returncode==0,receipt
 print(json.dumps({'actualMobileQR':True,'threePlatformKeysAndAliases':True,'fourCloseConsumers':True,'sameHarnessNextHuman':True,**receipt}))
finally:
 root.joinpath('mobile-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait(timeout=3)
 os.close(master);os.close(slave)
