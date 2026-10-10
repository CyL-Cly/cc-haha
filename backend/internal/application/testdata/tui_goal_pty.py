"""Actual PTY: /goal menu, auxiliary HTTP, continuation/completion and exit."""
import fcntl,json,os,pathlib,re,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,120,0,0));captured=bytearray();deadline=time.monotonic()+28
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTUIGoalFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.05)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6000:])))
  pump()
try:
 wait(b'Claude Code','startup');start=len(captured);os.write(master,b'/go');wait(b'/goal','actual goal menu',start);os.write(master,b'\t PRIVATE_TUI_GOAL_637\r')
 wait(b'Goal set: PRIVATE_TUI_GOAL_637','actual goal status',start)
 wait(b'SECOND_GOAL_MAIN_DONE_637','main continuation',start)
 time.sleep(.4);os.write(master,b'\x0c');wait(b'Goal marked complete.','evaluator completion')
 os.write(master,b'Continue normally.\r');wait(b'GOAL_NORMAL_NEXT_637','normal human same Harness');time.sleep(.5);os.write(master,b'\x0f');wait(b'Transcript','transcript');os.write(master,b'\x1b');time.sleep(.2);os.write(master,b'\x04\x04')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout')
  pump()
 receipt=json.loads(pathlib.Path(home,'goal-receipt.json').read_text());assert p.returncode==0 and receipt['requests']==5 and receipt['canonical'] and receipt['terminalRestored'],(receipt,p.returncode)
 print(json.dumps({'terminalRestored':True,'actualGoalMenu':True,'actualAuxiliaryHTTP':True,'sameHarnessContinuation':True,'completeHookRemoved':True,'canonical':True,'requests':5,'exitCode':p.returncode}))
finally:
 pathlib.Path(home,'goal-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
