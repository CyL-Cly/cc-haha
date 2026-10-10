"""Actual Go RunCommand/MainPrint/loopback provider through a native Unix PTY."""
import fcntl,json,os,pathlib,select,signal,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty()
fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',30,110,0,0))
before=termios.tcgetattr(slave)
captured=bytearray();deadline=time.monotonic()+28

def setup():
 os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTUIPresentationFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.05)[0]: captured.extend(os.read(master,65536))
def wait(needle,label):
 while needle not in captured:
  if time.monotonic()>deadline or p.poll() is not None: raise AssertionError('waiting '+label+' '+repr(bytes(captured[-8000:])))
  pump()
try:
 wait(b'Claude Code','real CLI TUI initialization')
 os.write(master,b'/');wait(b'/debug','actual command menu');wait(b'/simplify','multiple shared command candidates')
 os.write(master,b'\x1b');time.sleep(.08);os.write(master,b'\x15')
 os.write(master,b"!printf 'ACTUAL_TUI_LOCAL_SHELL_A\\nACTUAL_TUI_LOCAL_SHELL_B\\n'; printf ACTUAL_TUI_LOCAL_FILE > actual-local-shell-artifact\r")
 wait(b'ACTUAL_TUI_LOCAL_SHELL_A\r\nACTUAL_TUI_LOCAL_SHELL_B','real local shell output')
 os.write(master,b'\x0c');time.sleep(.1)
 os.write(master,b'Execute the actual CLI presentation task\r')
 wait(b'REAL_TUI_SHARED_HARNESS_DONE_Z','shared Harness completion')
 assert b'RAW_TUI_LINE1' in captured and 'RAW_TUI_LINE2 中文'.encode() in captured
 assert b'Read 2 lines' in captured,bytes(captured[-6000:])
 assert b'Error: Exit code 7' in captured,bytes(captured[-6000:])
 assert b'REAL_FAILURE_LINE_13' not in captured,'normal error skipped original summary limit'
 assert b'to see all)' in captured,'long original failure has no expansion affordance'
 assert b'system-reminder' not in captured,'model-only read reminder leaked in terminal'
 assert b'"RAW_TUI_LINE1\\nRAW_TUI_LINE2' not in captured,'transport JSON escaped newline leaked'
 os.write(master,b'\x0f');wait(b'Transcript','real transcript shortcut')
 wait(b'REAL_FAILURE_LINE_13','complete original failure in transcript')
 os.write(master,b'\x1b');time.sleep(.08)
 os.write(master,b'\x04\x04')
 while p.poll() is None:
  if time.monotonic()>deadline: raise AssertionError('native CLI did not exit')
  pump()
 assert p.returncode==0,p.returncode
 receipt=json.loads(pathlib.Path(home,'receipt.json').read_text())
 assert receipt['terminalRestored'] is True,receipt
 assert receipt['requests']==4,receipt
 assert receipt['readReminderReachedModel'] is True,receipt
 assert receipt['localShellPhysicalArtifact'] is True,receipt
 assert receipt['localShellHistoryReachedModel'] is True,receipt
 pathlib.Path(home,'terminal.txt').write_bytes(captured)
 print(json.dumps({'terminalRestored':True,'sharedHarnessRequests':4,'decodedOutput':True,'readSummary':True,'errorShown':True,'reminderPreservedInModel':True,'exitCode':p.returncode}))
finally:
 if p.poll() is None: p.kill();p.wait()
 os.close(master);os.close(slave)
