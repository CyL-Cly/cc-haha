"""Real terminal: original local processed users, zero inference, next turn."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',32,120,0,0));captured=bytearray();deadline=time.monotonic()+28
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTUILocalNoQueryFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.05)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-5000:])))
  pump()
try:
 wait(b'Claude Code','startup');os.write(master,b'/agent general-purpose\r');wait(b'Usage: /agent <agent> <prompt>','actual usage visible')
 time.sleep(.3);start=len(captured);os.write(master,b'/modelonly\r');wait(b'This skill can only be invoked by Claude','actual model-only error',start)
 time.sleep(.3);os.write(master,b'Continue after the two local messages.\r');wait(b'LOCAL_VISIBLE_USERS_DONE_6CE1','same actual Harness next human')
 time.sleep(.4);os.write(master,b'\x0f');wait(b'Transcript','transcript');os.write(master,b'\x1b');time.sleep(.1);os.write(master,b'\x04\x04')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout')
  pump()
 receipt=json.loads(pathlib.Path(home,'local-noquery-receipt.json').read_text())
 assert p.returncode==0 and receipt['requests']==1 and receipt['actualLocalHistory'] and receipt['terminalRestored'],(receipt,p.returncode)
 print(json.dumps({'terminalRestored':True,'usageAndModelOnlyZeroPOST':True,'originalProcessedLocalUsersVisible':True,'sameHarnessNextHuman':True,'requests':1,'exitCode':p.returncode}))
finally:
 pathlib.Path(home,'local-noquery-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
