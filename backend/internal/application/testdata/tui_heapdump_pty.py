"""Actual PTY: native release command menu/output, subsequent Harness and exit."""
import fcntl,json,os,pathlib,re,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,120,0,0));captured=bytearray();deadline=time.monotonic()+28
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTUIHeapDumpFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.05)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6000:])))
  pump()
def wait_rendered(needle,label,start=0):
 # Paths are hard-wrapped at the actual PTY width. A forced redraw provides
 # the full visible frame; join its lines after removing cursor/style CSI.
 while needle not in b''.join(re.sub(rb'\x1b\[[0-?]*[ -/]*[@-~]',b'',bytes(captured[start:])).split()):
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-5000:])))
  pump()
try:
 wait(b'Claude Code','startup');start=len(captured);os.write(master,b'/hea');time.sleep(.25)
 for _ in range(5):pump()
 assert b'/heapdump ' not in captured[start:] and b'Dump the JS heap' not in captured[start:],'hidden command leaked to prefix menu'
 os.write(master,b'pdump\r');wait_rendered(b'.heap.pprof','actual native heap output',start);time.sleep(.3);redraw=len(captured);os.write(master,b'\x0c');wait_rendered(b'-diagnostics.json','actual wrapped diagnostic output',redraw)
 os.write(master,b'Continue after actual native heap dump.\r');wait(b'HEAP_ACTUAL_NEXT_HUMAN_834','same actual Harness next human');time.sleep(.5);os.write(master,b'\x0f');wait(b'Transcript','transcript');os.write(master,b'\x1b');time.sleep(.2);os.write(master,b'\x04\x04')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout')
  pump()
 assert p.returncode==0,(p.returncode,bytes(captured[-5000:]))
 receipt=json.loads(pathlib.Path(home,'heap-receipt.json').read_text());assert receipt['requests']==1 and receipt['actualLocalHistory'] and receipt['canonical'] and receipt['terminalRestored'],receipt
 print(json.dumps({'terminalRestored':True,'hiddenPrefixPreserved':True,'nativeHeapZeroPOST':True,'actualReadableGoProfile':True,'sameHarnessNextHuman':True,'canonical':True,'requests':1,'exitCode':p.returncode}))
finally:
 pathlib.Path(home,'heap-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
