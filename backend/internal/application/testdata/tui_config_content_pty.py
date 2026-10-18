"""Public CLI content pickers, true shared Settings tabs and fresh startup."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',38,150,0,0));captured=bytearray();deadline=time.monotonic()+40;before=termios.tcgetattr(slave)
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIConfigContentPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6500:])))
  pump()
def file(path,label):
 while not path.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6500:])))
  pump()
def send(data):os.write(master,data);time.sleep(.18);pump()
def config():
 start=len(captured);send(b'/config\r');wait(b'Search settings','normal Config entry',start)
def picker(key,marker):
 send(b'/'+key+b'\r');start=len(captured);send(b'\x1b[C');wait(marker,'normal selected child picker',start)
def close_phase(phase):
 send(b'\x04\x04');receipt_file=root.joinpath('content-receipt-'+str(phase)+'.json');file(receipt_file,'normal exit receipt')
 receipt=json.loads(receipt_file.read_text());assert receipt['terminalRestored'] and termios.tcgetattr(slave)==before,'actual slave/Go termios restoration'
 root.joinpath('content-release-'+str(phase)).write_text('inspected');return receipt
try:
 wait(b'Claude Code','startup');paths=json.loads(root.joinpath('content-paths.json').read_text());settings=pathlib.Path(paths['settings']);local=pathlib.Path(paths['local']);globalfile=pathlib.Path(paths['global'])
 config();picker(b'outputStyle',b'Preferred output style');wait(b'Public native custom style description','actual loaded style description');send(b'\x1b[F\r');assert json.loads(local.read_text())['outputStyle']=='Fixture Style'
 picker(b'language',b'preferred response and voice language');send(b'\x1b[200~'+('\ufeff\u00a0 日本語 \u00a0').encode()+b'\x1b[201~');send(b'\r');assert json.loads(settings.read_text())['language']=='日本語';send(b'\r')
 send(b'Use the actual selected output style and language.\r');wait(b'CONFIG_CONTENT_FIRST_DONE_389','same Harness selected settings prompt');assert json.loads(root.joinpath('content-request-1.json').read_text())=={'language':True,'style':True}
 config();send(b'/verbose\r');send(b'\x1b[C');assert json.loads(globalfile.read_text())['verbose'] is True
 send(b'\x1b[A');send(b'\x1b[A');start=len(captured);send(b'\x1b[D');wait(b'[Status]','actual Config unmount to Status',start);assert json.loads(globalfile.read_text())['verbose'] is True
 start=len(captured);send(b'\x1b[D');wait(b'[Usage]','actual API-key Usage tab',start);wait(b'subscription plans.','actual non-subscriber consumer message',start)
 send(b'\x1b[C');start=len(captured);send(b'\x1b[C');wait(b'[Config]','reentered fresh Config tab',start);send(b'\x1b[B');send(b'/verbose\r');send(b'\x1b[C');assert json.loads(globalfile.read_text()).get('verbose',False) is False;send(b'\x1b');assert json.loads(globalfile.read_text())['verbose'] is True
 config();picker(b'outputStyle',b'Preferred output style');send(b'\x1b[H\r');assert json.loads(local.read_text())['outputStyle']=='default';picker(b'language',b'preferred response and voice language');send(b'\x15 ');send(b'\r');assert 'language' not in json.loads(settings.read_text());send(b'\r')
 send(b'Write the actual Settings tabs artifact using the same Harness.\r');wait(b'CONFIG_CONTENT_CACHED_DONE_389','original same-session section cache retained');assert pathlib.Path(paths['artifact']).read_text()=='PUBLIC_SETTINGS_TABS_ACTUAL_WRITE'
 first=close_phase(1);assert first['requests']==3
 wait(b'CONFIG_CONTENT_SECOND_CLI_START_389','fresh actual RunCommand startup');start=captured.index(b'CONFIG_CONTENT_SECOND_CLI_START_389')+len(b'CONFIG_CONTENT_SECOND_CLI_START_389');wait(b'Claude Code','fresh actual CLI prompt',start);send(b'Continue in the next actual CLI session.\r');wait(b'CONFIG_CONTENT_FRESH_DONE_389','fresh cache consumes saved reset');assert json.loads(root.joinpath('content-request-4.json').read_text())=={'language':False,'style':False};last=close_phase(2)
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout')
  pump()
 assert p.returncode==0 and last['requests']==4 and first['unknownFieldsPreserved'] and last['unknownFieldsPreserved'],last
 print(json.dumps({'publicStylePicker':True,'languageTrimReset':True,'sharedTabsConfigUnmountSnapshot':True,'apiKeyUsageActualMessage':True,'sameHarnessWrite':True,'freshCLIConsumer':True,**last}))
finally:
 root.joinpath('content-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
