"""Actual public doctor subcommand, confirmation, cancellation and PTY restore."""
import fcntl,json,os,pathlib,select,signal,struct,subprocess,sys,termios,time
binary,home,dismiss=sys.argv[1:]
master,slave=os.openpty()
fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',80,140,0,0))
before=termios.tcgetattr(slave)
buffer=bytearray();deadline=time.monotonic()+30
p=subprocess.Popen([binary,'doctor'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),cwd=home,preexec_fn=os.setsid,close_fds=True)
def pump(timeout=.02):
 readable,_,_=select.select([master],[],[],timeout)
 if readable:
  try:buffer.extend(os.read(master,65536))
  except OSError:pass
def wait(marker,label):
 while marker not in buffer:
  if time.monotonic()>deadline:raise AssertionError('deadline '+label+' '+buffer.decode(errors='replace'))
  if p.poll() is not None:raise AssertionError('premature exit '+label+' '+str(p.returncode)+' '+buffer.decode(errors='replace'))
  pump()
try:
 wait(b'Loading diagnostics','real async public Doctor screen')
 if dismiss!='early':
  wait(b'Large CLAUDE.md file detected','same memory/context producer')
  wait(b'Version Locks','same Source PID-lock producer')
  wait(b'Latest version: 3.0.1','real release metadata')
  assert p.poll() is None,'doctor must wait for Source confirmation'
 else:
  while not pathlib.Path(home,'doctor-release-request-opened').is_file():
   if time.monotonic()>deadline:raise AssertionError('no actual release HTTP opened '+buffer.decode(errors='replace'))
   if p.poll() is not None:raise AssertionError('premature exit before actual HTTP')
   pump()
 os.write(master,b'\r' if dismiss=='enter' else b'\x1b')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit deadline '+buffer.decode(errors='replace'))
  pump()
 assert p.returncode==0,(p.returncode,buffer.decode(errors='replace'))
 pump(0);raw_after=termios.tcgetattr(slave)
 if sys.platform=='darwin':fcntl.ioctl(slave,0x4004667f,struct.pack('i',0))
 after=termios.tcgetattr(slave)
 assert after==before,('terminal not restored',before,raw_after,after)
 pathlib.Path(home,'doctor-public-terminal.raw').write_bytes(buffer)
 print(json.dumps({'dismiss':dismiss,'exitCode':p.returncode,'restored':True}))
finally:
 if p.poll() is None:os.killpg(p.pid,signal.SIGKILL);p.wait()
 os.close(master);os.close(slave)
