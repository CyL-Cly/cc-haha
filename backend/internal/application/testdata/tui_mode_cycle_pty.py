"""Actual Shift+Tab -> same permission producer -> original tool/model consumer."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',32,120,0,0));captured=bytearray();deadline=time.monotonic()+36
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTUIModeCycleFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.05)[0]:captured.extend(os.read(master,65536))
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6000:])))
  pump()
try:
 wait(b'Claude Code','initialization')
 os.write(master,b'\x1b[Z');wait(b'accept edits on','actual mode cycle')
 os.write(master,b'Run actual write/read task\r');wait(b'MODE_PTY_FIRST_DONE_82','same actual tool policy')
 time.sleep(.25);start=len(captured);os.write(master,b'\x1b[Z');wait(b'plan mode ','actual plan mode',start)
 # Incremental ANSI rendering preserves the previous line's unchanged "on".
 # A real redraw proves the whole current footer without misreading raw bytes.
 os.write(master,b'\x0c');wait(b'plan mode on','current full redraw',start)
 os.write(master,b'Read artifact in plan mode\r');wait(b'MODE_PTY_SECOND_DONE_82','same original plan context')
 time.sleep(.25);os.write(master,b'\x1b[Z');time.sleep(.15);os.write(master,b'\x04\x04')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout '+repr(bytes(captured[-6000:])))
  pump()
 receipt=json.loads(pathlib.Path(home,'mode-cycle-receipt.json').read_text())
 assert p.returncode==0 and receipt['requests']==5 and all(receipt[k] for k in ['physicalWrite','planReminderReachedOriginalRequest','originalPlanTimestampSaved','terminalRestored']),(receipt,p.returncode,repr(bytes(captured[-6000:])))
 assert b'Allow Write?' not in captured,'Accept edits did not change actual tool policy'
 pathlib.Path(home,'mode-cycle-terminal.txt').write_bytes(captured)
 print(json.dumps({'terminalRestored':True,'actualPermissionCycle':True,'sameWriteProducer':True,'planReminderReachedRequest':True,'planTimestampSaved':True,'requests':5,'exitCode':p.returncode}))
finally:
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
