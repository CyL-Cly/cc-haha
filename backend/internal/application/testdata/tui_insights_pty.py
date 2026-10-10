"""Actual PTY: /insights menu, actual model report, normal next turn and exit."""
import fcntl,json,os,pathlib,re,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,120,0,0));captured=bytearray();deadline=time.monotonic()+28
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTUIInsightsFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.05)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6000:])))
  pump()
try:
 wait(b'Claude Code','startup');start=len(captured);os.write(master,b'/ins');wait(b'/insights','actual insights menu',start);os.write(master,b'\t\r')
 wait(b'PRIVATE_INSIGHTS_PTY_REPORT_READY_722','report response',start)
 report=pathlib.Path(json.loads(pathlib.Path(home,'insights-report-location.json').read_text()))
 assert report.exists() and 'PRIVATE_INSIGHTS_PTY_REPORT_722' in report.read_text(), 'physical HTML report'
 time.sleep(.2);os.write(master,b'Continue normally.\r');wait(b'PRIVATE_INSIGHTS_PTY_NEXT_722','normal human same Harness');time.sleep(.4);os.write(master,b'\x0f');wait(b'Transcript','transcript');os.write(master,b'\x1b');time.sleep(.2);os.write(master,b'\x04\x04')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout')
  pump()
 receipt=json.loads(pathlib.Path(home,'insights-receipt.json').read_text());assert p.returncode==0 and receipt['requests']==10 and receipt['canonical'] and receipt['terminalRestored'],(receipt,p.returncode)
 print(json.dumps({'terminalRestored':True,'actualInsightsMenu':True,'actualAuxiliaryHTTP':True,'sameHarnessNextTurn':True,'reportFile':True,'canonical':True,'requests':10,'exitCode':p.returncode}))
finally:
 pathlib.Path(home,'insights-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
