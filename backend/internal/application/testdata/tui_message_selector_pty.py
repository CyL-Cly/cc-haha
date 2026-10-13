"""Native CLI DoubleEsc selector -> model compaction -> editable original prompt."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',35,110,0,0));captured=bytearray();deadline=time.monotonic()+25
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTUIMessageSelectorFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.05)[0]:captured.extend(os.read(master,65536))
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-5000:])))
  pump()
def wait_idle():
 while True:
  frame=bytes(captured).split(b'\x1b[J')[-1]
  if b'SELECTOR_NATIVE_ORIGINAL_DONE' in frame and b'Working' not in frame:return
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('original turn idle '+repr(bytes(captured[-5000:])))
  pump()
try:
 wait(b'Claude Code','ready')
 os.write(master,b'Original authored task for native selector\r')
 wait(b'SELECTOR_NATIVE_ORIGINAL_DONE','original actual model')
 wait_idle();os.write(master,b'\x1b');time.sleep(.05);os.write(master,b'\x1b')
 wait(b'Rewind or summarize conversation','DoubleEsc selector')
 os.write(master,b'\x1b[A\r');wait(b'Summarize from here','restore/summary choices')
 os.write(master,b'\x1b[B\r');wait(b'Add context (optional)','feedback editor')
 os.write(master,b'preserve original explicit request\r')
 wait(b'Conversation summarized','summary finished')
 # The original authored input is repopulated; Enter submits it unchanged.
 os.write(master,b'\r');wait(b'SELECTOR_NATIVE_CONTINUED_DONE','same session continuation')
 time.sleep(.2);os.write(master,b'\x04\x04')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout '+repr(bytes(captured[-5000:])))
  pump()
 receipt=json.loads(pathlib.Path(home,'message-selector-receipt.json').read_text())
 assert p.returncode==0 and receipt['requests']==3 and receipt['feedbackReachedModel'] and receipt['sameSessionContinuation'] and receipt['terminalRestored'],(receipt,p.returncode,repr(bytes(captured[-5000:])))
 pathlib.Path(home,'message-selector-terminal.txt').write_bytes(captured)
 print(json.dumps(receipt))
finally:
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
