"""Actual Config search, mounted nested picker, rollback/commit and next Writes."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',38,140,0,0));captured=bytearray();deadline=time.monotonic()+35
before=termios.tcgetattr(slave)
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIConfigPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6000:])))
  pump()
def wait_saved(path,predicate,label):
 while True:
  try:data=json.loads(path.read_text())
  except (FileNotFoundError,json.JSONDecodeError):data=None
  if isinstance(data,dict) and predicate(data):return data
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(data)+' '+repr(bytes(captured[-8500:])))
  pump()
def send(data):os.write(master,data);time.sleep(.18);pump()
try:
 wait(b'Claude Code','startup');paths=json.loads(pathlib.Path(home,'config-paths.json').read_text());settings=pathlib.Path(paths['settings'])
 send(b'/config\r');wait(b'Search settings','actual Config entrance');send(b'/missing-private-option');wait(b'No settings match','actual filtered empty state')
 send(b'\x1b');send(b'\x1b');start=len(captured);send(b'model\r');wait(b'Model:', 'fallback printable search and enter list',start)
 send(b'\x1b[C');wait(b'Select model','shared native picker');start=len(captured);send(b'\x1b');wait(b'Search settings', 'child cancel restores parent',start);assert json.loads(settings.read_text())['model']=='initial-disk-model'
 start=len(captured);send(b'\x1b[C');wait(b'Select model','second mounted native picker',start);start=len(captured);send(b'3');wait(b'Search settings','actual child selection returns to parent',start);wait_saved(settings,lambda data:data.get('model')=='opus','actual child model persisted');assert json.loads(settings.read_text())['model']=='opus'
 send(b'\x1b');wait_saved(settings,lambda data:data.get('model')=='sonnet','actual parent rollback persisted');assert json.loads(settings.read_text())['model']=='sonnet','Source AppState rollback not saved'
 send(b'Write the first actual Config artifact.\r');wait(b'CONFIG_PUBLIC_REVERT_DONE_792','actual restored model query');assert pathlib.Path(paths['sonnet']).read_text()=='PUBLIC_CONFIG_REVERT_SONNET'
 start=len(captured);send(b'/config\r');wait(b'Search settings','next Config entrance',start);send(b'/model\r');start=len(captured);send(b'\x1b[C');wait(b'Select model','next mounted native picker',start);start=len(captured);send(b'3');wait(b'Search settings','next child selection',start);send(b'\r');wait_saved(settings,lambda data:data.get('model')=='opus' and data.get('futureUser')=={'keep':True},'actual model commit and unknown fields persisted');assert json.loads(settings.read_text())['model']=='opus' and json.loads(settings.read_text())['futureUser']=={'keep':True}
 send(b'Write the second actual Config artifact.\r');wait(b'CONFIG_PUBLIC_COMMIT_DONE_792','actual committed model query');assert pathlib.Path(paths['opus']).read_text()=='PUBLIC_CONFIG_COMMIT_OPUS'
 send(b'\x04\x04');receipt_file=pathlib.Path(home,'config-receipt.json')
 while not receipt_file.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('exit receipt missing')
  pump()
 receipt=json.loads(receipt_file.read_text());assert termios.tcgetattr(slave)==before,'actual slave termios not restored';pathlib.Path(home,'config-pty-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout')
  pump()
 assert p.returncode==0 and receipt['requests']==4 and receipt['terminalRestored'],receipt
 print(json.dumps({'actualSearchAndClear':True,'nestedPickerCancel':True,'modelRollbackNextWrite':True,'modelCommitNextWrite':True,**receipt}))
finally:
 pathlib.Path(home,'config-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
