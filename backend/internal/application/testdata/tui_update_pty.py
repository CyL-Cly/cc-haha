"""Real CLI updater startup, Config channel change/minimum and exit cancellation."""
import fcntl,json,os,pathlib,select,signal,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',38,140,0,0));captured=bytearray();deadline=time.monotonic()+35;before=termios.tcgetattr(slave)
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIUpdaterPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6000:])))
  pump()
def waitfile(file,label,predicate=lambda _:True):
 while not file.exists() or not predicate(file.read_text()):
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.18);pump()
try:
 wait(b'Claude Code','startup');paths=json.loads(root.joinpath('update-paths.json').read_text());settings=pathlib.Path(paths['settings']);installed=pathlib.Path(paths['installed'])
 wait(b'Update installed', 'actual startup physical updater result');assert installed.is_file();assert b'2.3.5' in subprocess.check_output([str(installed),'--version'],env=os.environ.copy())
 send(b'/config\r');wait(b'Search settings','actual config');send(b'/autoUpdatesChannel\r');send(b'\x1b[C');wait(b'Switch to Stable Channel','actual downgrade submenu');start=len(captured);send(b'\x1b[B\r');wait(b'Search settings','actual Stay callback returns parent',start)
 saved=json.loads(settings.read_text());assert saved['autoUpdatesChannel']=='stable' and saved['minimumVersion']=='2.3.4' and saved['futureUser']=={'keep':True},saved
 waitfile(root.joinpath('release-check-count'),'channel change requested real stable catalog',lambda v:v=='2');wait(b'Update installed','previous success retained after minimum skipped',start);assert installed.is_file()
 root.joinpath('hold-update').write_text('owned');start=len(captured);send(b'\x1b[C');wait(b'latest','stable-to-latest actual selected row changes',start);waitfile(root.joinpath('update-started'),'actual next channel starts physical request');assert 'minimumVersion' not in json.loads(settings.read_text())
 send(b'\r');send(b'\x04\x04');waitfile(root.joinpath('update-cancelled'),'exit cancelled physical request');waitfile(root.joinpath('update-receipt.json'),'exit joins update worker')
 receipt=json.loads(root.joinpath('update-receipt.json').read_text());assert termios.tcgetattr(slave)==before,'actual slave termios not restored';root.joinpath('update-pty-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout')
  pump()
 assert p.returncode==0 and receipt['gets']==2 and receipt['downloads']==1 and receipt['modelRequests']==0 and receipt['terminalRestored'],{'processReturnCode':p.returncode,'receipt':receipt,'terminalTail':repr(bytes(captured[-6000:]))}
 print(json.dumps({'startupActualGoInstall':True,'configStableMinimumConsumer':True,'channelChangeRealHTTP':True,'stopPhysicalCancellationAndJoin':True,**receipt}))
finally:
 if p.poll() is None:
  # Collect the actual blocked stacks after failure; successful waits retain
  # their original deadline and do not run this diagnostic.
  p.send_signal(signal.SIGQUIT)
  diagnostic=time.monotonic()+2
  while p.poll() is None and time.monotonic()<diagnostic:pump()
 root.joinpath('update-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
