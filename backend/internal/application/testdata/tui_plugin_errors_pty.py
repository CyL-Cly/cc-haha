"""Actual startup pending -> failure subscription -> Errors removal -> next turn."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',38,145,0,0));before=termios.tcgetattr(slave);captured=bytearray();screen=VTScreen(145,38);deadline=time.monotonic()+33

def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIPluginErrorsPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def wait(text,label):
 while text not in screen.text():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-10000:])))
  pump()
def file(name,label):
 while not root.joinpath(name).exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-10000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.18);pump()
def selectrow(label):
 for _ in range(12):
  if any(line.strip()=='> '+label for line in screen.text().splitlines()):send(b'\r');return
  send(b'\x1b[B')
 raise AssertionError('actual Errors selectable row absent '+label+' '+screen.text())
try:
 wait('Claude Code','actual ordinary startup');file('errors-started-ghost','actual startup reconciliation HTTP held');send(b'/plugin\r');wait('Plugins · discover','normal plugin entry');send(b'\t\t\t');wait('Plugins · errors','normal Errors tab while startup pending');assert not any(line.strip().startswith('> ghost') for line in screen.text().splitlines()),'pending status faked as failure'
 root.joinpath('errors-release-ghost').write_text('actual source failure released');wait('ghost · user','failed subscription refreshed same open Errors');selectrow('ghost · user')
 paths=json.loads(root.joinpath('errors-paths.json').read_text())
 for path in paths.values():
  value=json.loads(pathlib.Path(path).read_text());assert 'ghost' not in value['extraKnownMarketplaces'] and 'bad@ghost' not in value['enabledPlugins'] and value['futureSource']=={'keep':True},value
 send(b'\x1b');wait('\n>','actual Errors panel closes before later result');file('errors-started-late','second real reconciliation GET held');root.joinpath('errors-release-late').write_text('closed panel late failure');file('errors-replied-late','actual late HTTP503 fulfilled')
 send(b'PUBLIC_ERRORS_NEXT_HUMAN_REQUEST_528\r');wait('PUBLIC_ERRORS_NEXT_HUMAN_DONE_528','same Harness after error remediation');assert 'Plugins · errors' not in screen.text(),'late callback recreated a closed panel';send(b'\x04\x04');file('errors-receipt.json','native worker join and restoration receipt')
 receipt=json.loads(root.joinpath('errors-receipt.json').read_text());assert receipt['requests']==1 and receipt['marketRequests']==2 and receipt['actualScopesRemovedUnknownPreserved'] and receipt['terminalRestored'] and termios.tcgetattr(slave)==before,receipt;root.joinpath('errors-termios-release').write_text('physical termios verified')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('normal exit timeout')
  pump()
 assert p.returncode==0,receipt
 print(json.dumps({'realPendingHTTP':True,'liveFailureRefreshWithoutManualR':True,'actualMultiScopeRemoval':True,'lateFailureDoesNotReopenClosedPanel':True,'sameHarnessNextHuman':True,**receipt}))
finally:
 root.joinpath('errors-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait(timeout=3)
 os.close(master);os.close(slave)
