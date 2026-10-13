"""Public Go CLI Dream task through terminal input; private fixtures only."""
import fcntl,json,os,pathlib,re,select,signal,struct,subprocess,sys,termios,time
binary,home,profile=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',32,120,0,0));before=termios.tcgetattr(slave)
buffer=bytearray();deadline=time.monotonic()+25
p=subprocess.Popen([binary,'--model','fixture-model','--thinking','disabled','--no-computer-use'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),cwd=home,preexec_fn=os.setsid,close_fds=True)
def pump(t=.02):
 ready,_,_=select.select([master],[],[],t)
 if ready:
  try:buffer.extend(os.read(master,65536))
  except OSError:pass
def wait(marker,label):
 while marker not in buffer:
  if time.monotonic()>deadline:raise AssertionError('deadline '+label+' '+buffer.decode(errors='replace'))
  if p.poll() is not None:raise AssertionError('premature exit '+label+' '+str(p.returncode)+' '+buffer.decode(errors='replace'))
  pump()
def close_dialog():
 start=len(buffer);os.write(master,b'\x1b')
 while True:
  rendered=re.sub(rb'\x1b\[[0-9;:?]*[A-Za-z]',b'',bytes(buffer[start:])).replace(b'\r',b'')
  if re.search(rb'(?m)^> *$',rendered):return
  if time.monotonic()>deadline:raise AssertionError('dialog did not return to input '+buffer.decode(errors='replace'))
  pump()
try:
 wait(b'Claude Code','public TUI')
 os.write(master,b'PARENT_BEFORE_DREAM\r');wait(b'PUBLIC_PARENT_READY','actual parent request')
 if profile!='disabled':
  wait(b'dreaming','task footer');os.write(master,b'\x1b[1;2B');wait(b'Memory consolidation','actual task dialog');wait(b'reviewing 5 sessions','original detail counter')
  if profile=='kill':
   while not pathlib.Path(home,'dream-http-opened').exists():
    if time.monotonic()>deadline:raise AssertionError('actual Dream HTTP never opened')
    pump()
   os.write(master,b'x');wait(b'killed','dedicated background task stop')
  else:
   pathlib.Path(home,'release-dream').write_text('release private model response')
   wait(b'completed','actual terminal completion');wait(b'Improved','idle memory saved notification')
  close_dialog()
 os.write(master,b'AFTER_DREAM\r');wait(b'PUBLIC_PARENT_AFTER','same owner next human turn')
 os.write(master,b'\x04\x04')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('terminal exit deadline '+buffer.decode(errors='replace'))
  pump()
 assert p.returncode==0,(p.returncode,buffer.decode(errors='replace'))
 pump(0);raw_after=termios.tcgetattr(slave)
 if sys.platform=='darwin':fcntl.ioctl(slave,0x4004667f,struct.pack('i',0))
 assert termios.tcgetattr(slave)==before,('terminal not restored',before,raw_after,termios.tcgetattr(slave))
 pathlib.Path(home,'dream-terminal.raw').write_bytes(buffer)
 print(json.dumps({'profile':profile,'exitCode':p.returncode,'restored':True}))
finally:
 if p.poll() is None:os.killpg(p.pid,signal.SIGKILL);p.wait()
 os.close(master);os.close(slave)
