"""Public prompt PR link, saved Config toggle, actual next Write and native cancellation."""
import fcntl,json,os,pathlib,re,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,100,0,0));before=termios.tcgetattr(slave);captured=bytearray();deadline=time.monotonic()+36;screen=VTScreen(100,36)
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIReadOnlyPRFooterPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in re.sub(rb'\x1b\][^\x1b\x07]*(?:\x1b\\|\x07)|\x1b\[[0-?]*[ -/]*[@-~]',b'',bytes(captured[start:])):
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6500:])))
  pump()
def file(path,label):
 while not path.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6500:])))
  pump()
def send(data):os.write(master,data);time.sleep(.18);pump()
def until(predicate,label):
 while not predicate():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' SCREEN '+repr(screen.text())+' RAW '+repr(bytes(captured[-6500:])))
  pump()
def idle():
 text=screen.text();lines=text.splitlines()
 return any(line.strip()=='>' and i+1<len(lines) and 'shift+tab to cycle' in lines[i+1] for i,line in enumerate(lines)) and not re.search(r'(?m)^\s*[·✢✳✶✻✽*]\s+.*…',text)
def setting(key,value,label):
 while json.loads(globalfile.read_text()).get(key) is not value:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6500:])))
  pump()
def config():
 until(idle,'actual idle prompt before Config');send(b'/config\r');until(lambda:'Search settings' in screen.text(),'normal mounted Config entry');send(b'/prStatusFooterEnabled\r');until(lambda:any(line.startswith('> Show PR status footer:') for line in screen.text().splitlines()),'actual selected PR setting')
def commit_config():
 send(b'\r');until(idle,'Config commit returned actual prompt')
def close_phase(phase):
 until(idle,'actual idle prompt before normal exit')
 send(b'\x04\x04');path=root.joinpath('pr-receipt-'+str(phase)+'.json');file(path,'normal exit receipt');receipt=json.loads(path.read_text());assert receipt['terminalRestored'] and termios.tcgetattr(slave)==before,'Go/slave physical termios';root.joinpath('pr-release-'+str(phase)).write_text('inspected');return receipt
try:
 wait(b'PR #812','ordinary prompt actual gh result');paths=json.loads(root.joinpath('pr-paths.json').read_text());globalfile=pathlib.Path(paths['global']);gh=pathlib.Path(paths['ghReceipt'])
 assert gh.read_text().splitlines()[-1]=='pr view --json number,url,reviewDecision,isDraft,headRefName,state','actual native argv'
 config();send(b'\x1b[C');setting('prStatusFooterEnabled',False,'actual saved PR setting');commit_config()
 config();send(b'\x1b[C');setting('prStatusFooterEnabled',True,'actual PR setting preview');send(b'\x1b');setting('prStatusFooterEnabled',False,'Config Esc restored initial snapshot');until(idle,'Config rollback returned actual prompt')
 send(b'Write the actual saved PR footer artifact.\r');wait(b'PR_FOOTER_FIRST_DONE_872','same Harness next Write');assert pathlib.Path(paths['artifact']).read_text()=='PUBLIC_PR_FOOTER_SAME_HARNESS'
 first=close_phase(1);assert first['requests']==2
 wait(b'PR_FOOTER_SECOND_CLI_START_872','fresh CLI');start=captured.index(b'PR_FOOTER_SECOND_CLI_START_872')+len(b'PR_FOOTER_SECOND_CLI_START_872');wait(b'Claude Code','fresh prompt',start);time.sleep(.3);pump();assert b'PR #812' not in re.sub(rb'\x1b\][^\x1b\x07]*(?:\x1b\\|\x07)|\x1b\[[0-?]*[ -/]*[@-~]',b'',bytes(captured[start:])),'saved disabled preference not consumed on next startup'
 assert len(gh.read_text().splitlines())==3,'disabled next turns/startup spawned gh'
 send(b'Continue from the next actual CLI.\r');wait(b'PR_FOOTER_FRESH_DONE_872','fresh same Harness');assert len(gh.read_text().splitlines())==3,'disabled model boundary restarted gh'
 root.joinpath('hold-gh').write_text('hold');config();send(b'\x1b[C');setting('prStatusFooterEnabled',True,'actual held PR setting committed');commit_config();pidfile=pathlib.Path(paths['heldPID']);file(pidfile,'actual held gh subprocess');heldpid=int(pidfile.read_text())
 last=close_phase(2);assert last['requests']==3 and len(gh.read_text().splitlines())==6,'actual held query count'
 try:os.kill(heldpid,0)
 except ProcessLookupError:pass
 else:raise AssertionError('held native gh process survived normal terminal exit')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout')
  pump()
 assert p.returncode==0 and first['unknownFieldsPreserved'] and last['unknownFieldsPreserved'],last
 print(json.dumps({'realPromptPR':True,'savedToggleAndRollback':True,'freshDisabledConsumer':True,'sameHarnessWrite':True,'heldNativeProcessJoined':True,**last}))
finally:
 root.joinpath('pr-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
