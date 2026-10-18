"""Actual Config team UI snapshot/rollback, picker and shared child/leader consumers."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',40,150,0,0));captured=bytearray();deadline=time.monotonic()+36;before=termios.tcgetattr(slave)
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIConfigTeamPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
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
 start=len(captured);send(b'/config\r');wait(b'Search settings','ordinary Config',start)
def team_picker():
 send(b'/teammateDefaultModel\r');start=len(captured);send(b'\x1b[C');wait(b'Default model for newly spawned teammates.','actual teammate ModelPicker header',start)
try:
 wait(b'Claude Code','startup');paths=json.loads(root.joinpath('config-team-paths.json').read_text());globalfile=pathlib.Path(paths['global']);settings=pathlib.Path(paths['settings'])
 config();send(b'/teammateMode\r');wait(b'Teammate mode [overridden: tmux]','actual CLI override projection');send(b'\x1b[C');assert json.loads(globalfile.read_text())['teammateMode']=='tmux';send(b'\x1b[C');assert json.loads(globalfile.read_text())['teammateMode']=='in-process';send(b'\x1b');assert 'teammateMode' not in json.loads(globalfile.read_text()),'Source Esc restores file only'
 config();team_picker();start=len(captured);send(b'\r');wait(b'Search settings','unset Default confirm restores parent',start);assert 'teammateDefaultModel' not in json.loads(globalfile.read_text());send(b'\x1b')
 config();team_picker();start=len(captured);send(b'4');wait(b'Search settings','actual configured Haiku selection',start);assert json.loads(globalfile.read_text())['teammateDefaultModel']=='haiku';send(b'\r');assert json.loads(settings.read_text())['effortLevel']=='medium' and 'model' not in json.loads(settings.read_text())
 send(b'CONFIG_TEAM_SPAWN_ACTUAL_684\r');wait(b'CONFIG_TEAM_SPAWN_DONE_684','actual retained in-process snapshot despite reverted global');file(pathlib.Path(paths['child']),'actual saved Haiku child Write');assert pathlib.Path(paths['child']).read_text()=='PUBLIC_CONFIG_TEAM_CHILD_WRITE_684'
 send(b'CONFIG_TEAM_NEXT_PARENT_WRITE_684\r');wait(b'CONFIG_TEAM_PARENT_DONE_684','same leader Harness next Write');assert pathlib.Path(paths['parent']).read_text()=='PUBLIC_CONFIG_TEAM_PARENT_WRITE_684'
 send(b'\x04\x04');receiptfile=root.joinpath('config-team-receipt.json');file(receiptfile,'normal exit joined receipt');receipt=json.loads(receiptfile.read_text());assert receipt['terminalRestored'] and termios.tcgetattr(slave)==before,'actual termios restoration';root.joinpath('config-team-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('normal exit timeout')
  pump()
 assert p.returncode==0 and receipt['parentPosts']==5 and receipt['childPosts']==2 and receipt['actualChildWrite'] and receipt['actualParentWrite'] and receipt['unknownFieldsPreserved'],receipt
 print(json.dumps({'modeOverrideAndImmediateSnapshot':True,'escapeFileOnlyRollback':True,'unsetDefaultNoop':True,'skipLeaderSettingsWrite':True,'sameChildAndLeaderHarness':True,**receipt}))
finally:
 root.joinpath('config-team-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.terminate();p.wait(timeout=8)
 os.close(master);os.close(slave)
