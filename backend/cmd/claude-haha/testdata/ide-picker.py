"""Actual Go CLI /ide input through a private PTY; no real editor account."""
import fcntl,json,os,pathlib,select,signal,struct,subprocess,sys,termios,time
binary,home,profile,flags_json=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',28,100,0,0));before=termios.tcgetattr(slave)
buffer=bytearray();deadline=time.monotonic()+25
p=subprocess.Popen([binary,*json.loads(flags_json)],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),cwd=home,preexec_fn=os.setsid,close_fds=True)
def pump(t=.02):
 readable,_,_=select.select([master],[],[],t)
 if readable:
  try:buffer.extend(os.read(master,65536))
  except OSError:pass
def wait(marker,label):
 while marker not in buffer:
  if time.monotonic()>deadline:raise AssertionError('deadline '+label+' '+buffer.decode(errors='replace'))
  if p.poll() is not None:raise AssertionError('premature exit '+label+' '+str(p.returncode)+' '+buffer.decode(errors='replace'))
  pump()
try:
 wait(b'Claude Code','actual Go TUI startup')
 os.write(master,b'/ide\r');wait(b'Select IDE','actual typed IDE local command')
 if profile=='cancel':
  os.write(master,b'\x1b');wait(b'IDE selection cancelled','actual local cancellation')
 else:
  # The Source default selection is None. Move to the single valid IDE.
  os.write(master,b'\x1b[A\r');wait(b'Do you wish to enable auto-connect to IDE?','actual preference dialog')
  os.write(master,b'\x1b[B\r');wait(b'Connected to Private CLI IDE.','actual native WS connection')
  os.write(master,b'Use the actual IDE diagnostics.\r');wait(b'PUBLIC_IDE_TOOL_CONTINUED','same Harness action and next provider request')
  # A new chooser reflects the current IDE. Select None and disconnect.
  os.write(master,b'/ide\r');pump(.1);os.write(master,b'\x1b[B\r');wait(b'Disconnected from Private CLI IDE.','same Owner physical disconnect')
 os.write(master,b'\x04\x04')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('Go TUI exit deadline '+buffer.decode(errors='replace'))
  pump()
 assert p.returncode==0,(p.returncode,buffer.decode(errors='replace'))
 pump(0);raw_after=termios.tcgetattr(slave)
 if sys.platform=='darwin':fcntl.ioctl(slave,0x4004667f,struct.pack('i',0))
 observed_after=termios.tcgetattr(slave);assert observed_after==before,('terminal not restored',before,raw_after,observed_after)
 pathlib.Path(home,'ide-terminal.raw').write_bytes(buffer)
 print(json.dumps({'profile':profile,'exitCode':p.returncode,'restored':True}))
finally:
 if p.poll() is None:os.killpg(p.pid,signal.SIGKILL);p.wait()
 os.close(master);os.close(slave)
