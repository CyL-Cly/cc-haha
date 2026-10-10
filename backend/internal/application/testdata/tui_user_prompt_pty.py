"""Actual terminal/Go Session: blocked Hook warning, then physical tools."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',34,120,0,0));captured=bytearray();deadline=time.monotonic()+27
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTUIUserPromptFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.05)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-5000:])))
  pump()
try:
 wait(b'Claude Code','startup');os.write(master,b'PRIVATE_FIRST_BLOCKED_PROMPT\r');wait(b'UserPromptSubmit operation blocked by hook:','actual warning')
 wait(b'actual-private-prompt-block','actual blocking reason');time.sleep(.3)
 start=len(captured);os.write(master,b'Next human must use actual Write and Read.\r');wait(b'ACTUAL_USER_PROMPT_NEXT_HUMAN_DONE','same shared Harness continuation',start)
 time.sleep(.4);os.write(master,b'\x04\x04')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout')
  pump()
 receipt=json.loads(pathlib.Path(home,'user-prompt-receipt.json').read_text())
 assert p.returncode==0 and receipt['requests']==3 and receipt['physicalTools'] and receipt['terminalRestored'] and receipt['firstBlockedZeroPOST'] and receipt['hookInputs']==2,(receipt,p.returncode)
 print(json.dumps({'terminalRestored':True,'actualWarningVisible':True,'firstBlockedZeroPOST':True,'sameHarnessNextHumanPhysicalTools':True,'requests':3,'hookInputs':2,'exitCode':p.returncode}))
finally:
 pathlib.Path(home,'user-prompt-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
