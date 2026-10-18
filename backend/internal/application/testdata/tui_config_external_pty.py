"""Public Config forced external-import discovery, clear, and next real Write."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',38,160,0,0));captured=bytearray();deadline=time.monotonic()+32;before=termios.tcgetattr(slave)
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIConfigExternalPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-8500:])))
  pump()
def file(path,label):
 while not path.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-8500:])))
  pump()
def send(data):os.write(master,data);time.sleep(.18);pump()
def dialog():
 start=len(captured);send(b'/config\r');wait(b'Search settings','normal Config entry',start);send(b'/showExternalIncludesDialog\r');start=len(captured);send(b'\x1b[C');wait(b'Allow external CLAUDE.md file imports?','actual external-import dialog',start);wait(b'outside-claude.md','actual discovered external file',start)
try:
 wait(b'Claude Code','startup');paths=json.loads(root.joinpath('external-paths.json').read_text());globalfile=pathlib.Path(paths['global'])
 send(b'Use the original project instructions before approval.\r');wait(b'EXTERNAL_FIRST_DONE_572','first real cached memory');assert json.loads(root.joinpath('external-request-1.json').read_text())=={'external':False,'project':True}
 dialog();send(b'\r');state=json.loads(globalfile.read_text())['projects'][paths['work']];assert state['hasClaudeMdExternalIncludesApproved'] and state['hasClaudeMdExternalIncludesWarningShown'];send(b'\r')
 send(b'Keep the original cached memory before clear.\r');wait(b'EXTERNAL_CACHED_DONE_572','original cache retained');assert json.loads(root.joinpath('external-request-2.json').read_text())=={'external':False,'project':True}
 send(b'/clear\r');send(b'Consume the approved external instructions after clear.\r');wait(b'EXTERNAL_APPROVED_DONE_572','actual clear rebuild reads approved external file');assert json.loads(root.joinpath('external-request-3.json').read_text())=={'external':True,'project':True}
 dialog();send(b'\x1b');state=json.loads(globalfile.read_text())['projects'][paths['work']];assert state['hasClaudeMdExternalIncludesApproved'] is False and state['hasClaudeMdExternalIncludesWarningShown'];send(b'\r')
 send(b'/new\r');send(b'Write the actual next artifact after disabling external imports.\r');wait(b'EXTERNAL_DISABLED_WRITE_DONE_572','same Harness actual Write after disabled-import clear');assert pathlib.Path(paths['artifact']).read_text()=='EXTERNAL_CONFIG_ACTUAL_NEXT_WRITE_572';assert json.loads(root.joinpath('external-request-4.json').read_text())=={'external':False,'project':True}
 send(b'\x04\x04');receiptfile=root.joinpath('external-receipt.json');file(receiptfile,'normal exit receipt');receipt=json.loads(receiptfile.read_text());assert receipt['terminalRestored'] and termios.tcgetattr(slave)==before,'actual slave and Go termios restoration';assert receipt['requests']==5 and receipt['sameHarnessWrite'] and receipt['unknownFieldsPreserved'] and receipt['externalDisabled'],receipt;root.joinpath('external-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout')
  pump()
 assert p.returncode==0,repr(bytes(captured[-8500:]));print(json.dumps({'forcedDiscoveryDialog':True,'approvedAfterClear':True,'escapeDisablesExternal':True,**receipt}))
finally:
 root.joinpath('external-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
