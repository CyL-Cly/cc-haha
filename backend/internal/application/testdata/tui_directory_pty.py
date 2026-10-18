"""Actual CLI directory dialog, persisted permission and next tool execution."""
import fcntl,json,os,pathlib,re,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,140,0,0));captured=bytearray();deadline=time.monotonic()+35
terminal_before=termios.tcgetattr(slave)
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIAddDirectoryPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6000:])))
  pump()
def wait_rendered(needle,label,start=0):
 while True:
  rendered=re.sub(rb'\x1b\[[0-?]*[ -/]*[@-~]',b'',bytes(captured[start:])).replace(b'\r\n',b'').replace(b'\n',b'')
  if needle in rendered:return
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.18);pump()
try:
 wait(b'Claude Code','startup');paths=json.loads(pathlib.Path(home,'directory-paths.json').read_text());directory=paths['directory'].encode();second=paths['second'].encode()
 send(b'/add-dir '+directory+b'/\r');wait(b'Yes, and remember in local settings','validated actual chooser');send(b'\x1b[B\r');wait_rendered(b'saved to local settings','actual remember callback with physical wrapped lines')
 local=pathlib.Path(paths['settings']);saved=local.read_bytes();settings=json.loads(saved);assert settings['futureLocal']=={'keep':True} and settings['permissions']['allow']==['Read'] and settings['permissions']['additionalDirectories']==[paths['directory']]
 start=len(captured);send(b'/add-dir '+directory+b'\r');wait_rendered(b'already accessible','same-context Source validation across physical terminal wrapping',start)
 send(b'/add-dir\r');wait(b'Directory path','actual noargs form');send(b'does-not-exist-owned\r');wait_rendered(b'was not found','actual invalid stat feedback across physical terminal wrapping');send(b'\x15'+second+b'\r');wait(b'for this session','actual session-only form');assert local.read_bytes()==saved,'session form persisted unexpectedly'
 send(b'Write the actual artifact in my added directory.\r');wait(b'DIRECTORY_PUBLIC_NEXT_DONE_462','same Harness actual Write after directory context');assert pathlib.Path(paths['target']).read_text()=='PUBLIC_ADDED_DIRECTORY_WRITE'
 start=len(captured);send(b'/status\r');wait(b'Session ID:','actual status same-session properties',start);wait(b'Project local settings','actual loaded setting-source consumer',start);send(b'\x1b')
 send(b'/permissions\r');wait(b'w workspace','actual permission workspace entrance');send(b'w');wait_rendered(b'Original working directory','actual permission map after physical path wrapping');send(b'\r');wait(b'Remove directory from workspace?','actual removal confirmation');send(b'\r');wait(b'Removed directory','actual session-only removal');assert local.read_bytes()==saved,'Source session-only removal edited remembered settings';send(b'\x1b');send(b'\x1b')
 start=len(captured);send(b'Attempt a write after removing its workspace access.\r');wait(b'Allow Write?','actual removed-directory approval',start);send(b'n');wait(b'DIRECTORY_PUBLIC_REMOVED_DONE_462','actual denial returned to same Harness');assert pathlib.Path(paths['target']).read_text()=='PUBLIC_ADDED_DIRECTORY_WRITE'
 send(b'\x04\x04')
 receipt_file=pathlib.Path(home,'directory-receipt.json')
 while not receipt_file.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('normal exit receipt missing')
  pump()
 receipt=json.loads(receipt_file.read_text());assert termios.tcgetattr(slave)==terminal_before,'actual slave terminal flags were not restored'
 pathlib.Path(home,'directory-pty-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout')
  pump()
 assert p.returncode==0 and receipt['requests']==4 and receipt['terminalRestored'],receipt
 print(json.dumps({'actualRememberSettings':True,'noargsValidationAndSessionOnly':True,'nextNativeWrite':True,'actualStatusWithoutDoctorNetwork':True,'workspaceRemovalAndNextRealPermission':True,**receipt}))
finally:
 pathlib.Path(home,'directory-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
