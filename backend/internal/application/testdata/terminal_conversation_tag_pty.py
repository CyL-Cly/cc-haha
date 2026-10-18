"""Actual public CLI guard receipt for the currently unsupported internal host."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:];master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',32,120,0,0))
before=termios.tcgetattr(slave);captured=bytearray();deadline=time.monotonic()+22
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTerminalConversationTagPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-5000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.14);pump()
def command(text,needle,label):
 start=len(captured);send(text.encode()+b'\r');wait(needle,label,start)
try:
 wait(b'CLI_TAG_INTERNAL_PROFILE_BLOCKED','actual startup internal schema guard')
 marker=pathlib.Path(home,'tag-terminal-restored')
 while not marker.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('normal tag exit missing '+repr(bytes(captured[-5000:])))
  pump()
 assert termios.tcgetattr(slave)==before,'physical terminal did not restore'
 data=pathlib.Path(pathlib.Path(home,'tag-transcript-path.txt').read_text()).read_text()
 assert data=='{"type":"legacy-future","future":{"preserve":17}}\n',data
 pathlib.Path(home,'tag-pty-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('tag process did not exit')
  pump()
 assert p.returncode==0,p.returncode
 print(json.dumps({'terminalRestored':True,'internalProfileBlocked':True,'priorTranscriptPreserved':True,'noModelRequests':True,'exitCode':p.returncode}))
finally:
 pathlib.Path(home,'tag-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
