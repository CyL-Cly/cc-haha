"""Actual PTY: native release command menu/output, subsequent Harness and exit."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,120,0,0));captured=bytearray();deadline=time.monotonic()+28
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTUIReleaseNotesFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.05)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6000:])))
  pump()
try:
 wait(b'Claude Code','startup');start=len(captured);os.write(master,b'/release');wait(b'/release-notes','actual command menu',start);os.write(master,b'\r');wait(b'PRIVATE_TUI_RELEASE_ONE_6AF','native sorted release output',start)
 time.sleep(.4);os.write(master,b'\x0c');wait(b'PRIVATE_TUI_RELEASE_TWO_6AF','full native multiline output',start)
 one=captured.find(b'PRIVATE_TUI_RELEASE_ONE_6AF',start);two=captured.find(b'PRIVATE_TUI_RELEASE_TWO_6AF',start);assert one<two,'oldest-first release order'
 os.write(master,b'Continue after local release notes.\r');wait(b'RELEASE_REAL_NEXT_HUMAN_6AF','same actual Harness next human');time.sleep(.5);os.write(master,b'\x0f');wait(b'Transcript','transcript');os.write(master,b'\x1b');time.sleep(.2);os.write(master,b'\x04\x04')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout')
  pump()
 receipt=json.loads(pathlib.Path(home,'release-receipt.json').read_text());assert p.returncode==0 and receipt['requests']==1 and receipt['actualLocalHistory'] and receipt['canonical'] and receipt['terminalRestored'],(receipt,p.returncode)
 print(json.dumps({'terminalRestored':True,'actualReleaseMenu':True,'cachedOutputZeroPOST':True,'sourceSortedMultiline':True,'sameHarnessNextHuman':True,'canonical':True,'requests':1,'exitCode':p.returncode}))
finally:
 pathlib.Path(home,'release-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
