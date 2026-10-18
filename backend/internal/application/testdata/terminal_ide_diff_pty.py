"""Public Config -> actual IDE permission/file -> next human -> held RPC exit."""
import fcntl,json,os,pathlib,select,signal,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',40,150,0,0));captured=bytearray();deadline=time.monotonic()+37;before=termios.tcgetattr(slave)
screen=VTScreen(150,40)
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIIDEDiffPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:
   data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:] and needle.decode('utf8') not in screen.text():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-8500:])))
  pump()
def file(path,label):
 while not path.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-8500:])))
  pump()
def wire(opened,closed,label):
 while True:
  try:data=json.loads(root.joinpath('ide-diff-wire.json').read_text())
  except (FileNotFoundError,json.JSONDecodeError):data={}
  if data.get('open',0)==opened and data.get('close',0)==closed:return data
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(data)+' '+repr(bytes(captured[-8500:])))
  pump()
def wait_saved(path,predicate,label):
 while True:
  try:data=json.loads(path.read_text())
  except (FileNotFoundError,json.JSONDecodeError):data=None
  if isinstance(data,dict) and predicate(data):return data
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(data)+' '+repr(bytes(captured[-8500:])))
  pump()
def send(data):os.write(master,data);time.sleep(.18);pump()
def toggle_config(value):
 start=len(captured);send(b'/config\r');wait(b'Search settings','ordinary Config',start);send(b'/diffTool\r');wait(b'Diff tool','actual connected IDE Config row',start);send(b'\x1b[C');wait_saved(globalfile,lambda data:data.get('diffTool','auto')==value,'actual diff tool setting persisted');assert json.loads(globalfile.read_text()).get('diffTool','auto')==value;send(b'\r')
try:
 wait(b'Claude Code','startup');file(root.joinpath('ide-diff-paths.json'),'actual private paths');paths=json.loads(root.joinpath('ide-diff-paths.json').read_text());globalfile=pathlib.Path(paths['global']);target=pathlib.Path(paths['file'])
 toggle_config('terminal');start=len(captured);send(b'IDE_DIFF_TERMINAL_REQUEST_526\r');wait(b'Allow Edit?','actual terminal permission',start);wire(0,0,'terminal setting must not open IDE');send(b'y');wait(b'IDE_DIFF_TERMINAL_ACTUAL_DONE_526','actual terminal Edit completes',start);assert target.read_text()=='head\nnew\nlast\n'
 toggle_config('auto');start=len(captured);send(b'IDE_DIFF_AUTO_REQUEST_526\r');wait(b'IDE_DIFF_AUTO_ACTUAL_DONE_526','actual IDE saved response consumed',start);wire(1,1,'actual saved tab cleanup');assert target.read_text()=='head\nUSER_IDE_PUBLIC_SAVE_526\nlast\n'
 start=len(captured);send(b'IDE_DIFF_NEXT_HUMAN_REQUEST_526\r');wait(b'IDE_DIFF_NEXT_HUMAN_DONE_526','same Harness next human',start)
 start=len(captured);send(b'IDE_DIFF_HELD_EXIT_REQUEST_526\r');wire(2,1,'second actual IDE RPC held');wait(b'Opened changes in Actual Diff IDE','actual mounted IDE permission',start);p.send_signal(signal.SIGTERM);file(root.joinpath('ide-diff-receipt.json'),'global close joined receipt');receipt=json.loads(root.joinpath('ide-diff-receipt.json').read_text());assert receipt['terminalRestored'] and termios.tcgetattr(slave)==before,'actual termios restoration';assert receipt['physicalCleanupJoined'] and receipt['actualSavedBytes'] and receipt['unknownFieldsPreserved'] and receipt['requests']==9 and receipt['exitCode']==0,receipt;root.joinpath('ide-diff-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('normal exit timeout')
  pump()
 assert p.returncode==0,p.returncode
 print(json.dumps({'configTerminalManualPermission':True,'configAutoActualSavedFile':True,'sameHarnessNextHuman':True,'heldOpenAndCloseCancelled':True,**receipt}))
finally:
 root.joinpath('ide-diff-terminal.txt').write_bytes(captured)
 if p.poll() is None:
  p.terminate()
  try:p.wait(timeout=8)
  except subprocess.TimeoutExpired:p.kill();p.wait(timeout=3)
 os.close(master);os.close(slave)
