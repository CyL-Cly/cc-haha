"""Actual public CLI image paste, deletion, physical cache, next turn and exit."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty()
fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',30,130,0,0))
before=termios.tcgetattr(slave)
captured=bytearray();deadline=time.monotonic()+29
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestActualCLIClipboardImageFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-5000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.15);pump()
try:
 wait(b'Claude Code','startup')
 start=len(captured);send(b'\x16');wait(b'[Image #1]','first image pill',start)
 send(b'\x7f')
 start=len(captured);send(b'\x16');wait(b'[Image #2]','replacement image pill',start)
 send(b' Inspect current pasted image.\r');wait(b'MEDIA_PTY_IMAGE_DONE','actual image request')
 send(b'Continue after the pasted image.\r');wait(b'MEDIA_PTY_NEXT_DONE','next human request')
 send(b'\x04\x04')
 receipt_path=pathlib.Path(home,'media-pty-receipt.json')
 while not receipt_path.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('receipt '+repr(bytes(captured[-5000:])))
  pump()
 receipt=json.loads(receipt_path.read_text())
 assert receipt['terminalRestored'] and receipt['requests']==2,receipt
 assert termios.tcgetattr(slave)==before,'physical slave termios not restored'
 pathlib.Path(home,'release-media-child').write_text('released')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout')
  pump()
 assert p.returncode==0,(p.returncode,repr(bytes(captured[-5000:])))
 print(json.dumps({**receipt,'physicalTermiosRestored':True,'exitCode':p.returncode}))
finally:
 pathlib.Path(home,'media-pty-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
