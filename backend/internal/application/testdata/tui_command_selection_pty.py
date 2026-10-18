"""Actual CLI menu choices through original shared command/skill producers."""
import fcntl,json,os,pathlib,re,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',32,120,0,0));captured=bytearray();deadline=time.monotonic()+32;screen=VTScreen(120,32)
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTUICommandSelectionFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.05)[0]:
  data=os.read(master,65536);captured.extend(data);screen.feed(data)
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6000:])))
  pump()
def selected(command):
 while not any(line.startswith('> /'+command+' ') for line in screen.text().splitlines()):
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('actual selected '+command+' '+repr(screen.text()))
  pump()
def idle():
 while not any(line.strip()=='>' and i+1<len(screen.text().splitlines()) and 'shift+tab to cycle' in screen.text().splitlines()[i+1] for i,line in enumerate(screen.text().splitlines())) or re.search(r'(?m)^\s*[·✢✳✶✻✽*]\s+.*…',screen.text()):
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('actual idle prompt before exit '+repr(screen.text()))
  pump()
try:
 wait(b'Claude Code','initialization')
 os.write(master,b'/co');wait(b'/cost','actual shared choices')
 # Shared Source ranking selects /cost first. Verify the live selected row;
 # fixed arrow offsets previously selected /color after ranking changed.
 selected('cost');os.write(master,b'\r');wait(b'Total cost:','selected cost executed locally')
 start=len(captured);os.write(master,b'/pty-');wait(b'/pty-args','actual skill selection',start)
 os.write(master,b'\r');wait(b'<target> <region>','actual required-args hint',start)
 os.write(master,b'"a target" ');wait(b'[region]','same Source quoted argument parser',start)
 os.write(master,b'europe\r');wait(b'ACTUAL_COMMAND_CHOICE_DONE_9D','selected skill actual Harness')
 # The assistant delta precedes real ASK Return/finally and permission/input
 # cleanup. Observe the terminal's idle render before invoking exit.
 idle()
 os.write(master,b'\x04\x04')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout '+repr(bytes(captured[-7000:])))
  pump()
 receipt=json.loads(pathlib.Path(home,'command-selection-receipt.json').read_text())
 assert p.returncode==0 and receipt['requests']==1 and receipt['actualNamedArguments'] and receipt['terminalRestored'],(receipt,p.returncode,repr(bytes(captured[-6000:])))
 pathlib.Path(home,'command-selection-terminal.txt').write_bytes(captured)
 print(json.dumps({'terminalRestored':True,'selectedLocalCommandZeroPOST':True,'requiredArgumentsEditable':True,'quotedArgumentsPreserved':True,'actualNamedPromptReachedHarness':True,'requests':1,'exitCode':p.returncode}))
finally:
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
