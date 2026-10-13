"""Production Go CLI /doctor and following Read turn through a private PTY."""
import fcntl,json,os,pathlib,select,signal,struct,subprocess,sys,termios,time
binary,home,dismiss,flags_json=sys.argv[1:]
master,slave=os.openpty()
fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',32,120,0,0))
before=termios.tcgetattr(slave)
buffer=bytearray();deadline=time.monotonic()+35
p=subprocess.Popen([binary,*json.loads(flags_json)],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),cwd=home,preexec_fn=os.setsid,close_fds=True)
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
 wait(b'Claude Code','production TUI startup')
 os.write(master,b'/doctor\r')
 wait(b'Available versions','actual local native diagnostic and joined channels')
 wait(b'Latest version: 3.0.1','signed Go repository metadata consumer')
 os.write(master,b'\r' if dismiss=='enter' else b'\x1b')
 wait(b'Claude Code diagnostics dismissed','Source confirmation dismissal')
 pump(.1)
 os.write(master,b'Read proof.txt with the actual Read tool and report its marker.\r')
 wait(b'DOCTOR_PUBLIC_NEXT_READ_OK','same harness physical Read and next request')
 os.write(master,b'\x04\x04')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit deadline '+buffer.decode(errors='replace'))
  pump()
 assert p.returncode==0,(p.returncode,buffer.decode(errors='replace'))
 pump(0);raw_after=termios.tcgetattr(slave)
 if sys.platform=='darwin':fcntl.ioctl(slave,0x4004667f,struct.pack('i',0))
 after=termios.tcgetattr(slave)
 assert after==before,('terminal not restored',before,raw_after,after)
 pathlib.Path(home,'doctor-terminal.raw').write_bytes(buffer)
 print(json.dumps({'dismiss':dismiss,'exitCode':p.returncode,'restored':True}))
finally:
 if p.poll() is None:os.killpg(p.pid,signal.SIGKILL);p.wait()
 os.close(master);os.close(slave)
