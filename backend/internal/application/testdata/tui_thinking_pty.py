"""Actual CLI/shared Harness thinking selection and confirmation on native PTY."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',32,120,0,0));captured=bytearray();deadline=time.monotonic()+35

def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTUIThinkingFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.05)[0]:captured.extend(os.read(master,65536))
def wait(needle,label,start=0,redraw=False):
 last_redraw=0
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6000:])))
  if redraw and time.monotonic()-last_redraw>.1:
   os.write(master,b'\x0c');last_redraw=time.monotonic()
  pump()
try:
 wait(b'Claude Code','initialization')
 os.write(master,b'\x1bt');wait(b'Toggle thinking mode','actual Alt+T picker')
 os.write(master,b'\x1b[A\r');wait(b'Thinking on','first true callback')
 os.write(master,b'First actual thinking turn\r');wait(b'TUI_THINKING_FIRST_DONE','first actual Harness')
 start=len(captured);os.write(master,b'\x1bt');wait(b'Toggle thinking mode','mid-conversation picker',start)
 os.write(master,b'\x1b[B\r');wait(b'Do you want to proceed?','actual mid-conversation confirmation',start)
 os.write(master,b'\x1b');time.sleep(.1)
 os.write(master,b'\r');start=len(captured);os.write(master,b'\x1b');time.sleep(.1)
 # Esc backs out of confirmation to the same choices; choose again and confirm.
 os.write(master,b'\r');time.sleep(.1);os.write(master,b'\r');wait(b'Thinking off','confirmed original callback',start,redraw=True)
 os.write(master,b'Second actual thinking turn\r');wait(b'TUI_THINKING_SECOND_DONE','actual disabled request')
 # Assistant text is streamed before accounting/finally. Wait for the real
 # SDK result observer rather than attempting exit on a partial text delta.
 while not pathlib.Path(home,'thinking-second-finalized.json').exists():
  if time.monotonic()>deadline:raise AssertionError('actual second SDK result missing')
  pump()
 # Observe the real first-key confirmation before delivering the second key;
 # one raw chunk can be coalesced by the PTY input decoder under suite load.
 start=len(captured);os.write(master,b'\x04')
 wait(b'Press Ctrl-D again to exit','actual exit confirmation',start,redraw=True)
 os.write(master,b'\x04')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout '+repr(bytes(captured[-7000:])))
  pump()
 receipt=json.loads(pathlib.Path(home,'thinking-receipt.json').read_text());assert p.returncode==0 and receipt['terminalRestored'] and receipt['requests']==2 and receipt['actualDisabledSecondRequest'],(receipt,p.returncode,repr(bytes(captured[-7000:])))
 pathlib.Path(home,'thinking-terminal.txt').write_bytes(captured)
 print(json.dumps({'terminalRestored':True,'sharedHarnessRequests':2,'initialSelection':True,'midConversationCancelAndConfirm':True,'actualThinkingChange':True,'exitCode':p.returncode}))
finally:
 pathlib.Path(home,'thinking-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
