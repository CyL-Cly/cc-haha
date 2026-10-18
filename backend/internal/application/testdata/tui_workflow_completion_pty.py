"""Actual CLI returns foreground, then native idle notification executes tools."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,140,0,0));before=termios.tcgetattr(slave);captured=bytearray();deadline=time.monotonic()+35
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIIdleWorkflowCompletionPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label):
 while needle not in captured:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.15);pump()
try:
 wait(b'Claude Code','startup');send(b'Launch the actual idle workflow.\r');wait(b'WORKFLOW_IDLE_READY_708','foreground returned while child pending')
 send(b'/workflows\r');wait(b'Dynamic workflow runs in this session','actual blocking local JSX menu');pathlib.Path(home,'release-child').write_text('release physical child response')
 work=pathlib.Path(pathlib.Path(home,'completion-workdir.txt').read_text())
 while not (work/'child-artifact.txt').exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('actual child did not complete under local UI')
  pump()
 until=time.monotonic()+.6
 while time.monotonic()<until:pump()
 assert not (work/'notification-artifact.txt').exists() and b'WORKFLOW_IDLE_NOTIFY_DONE_708' not in captured,'Source queue must pause while local JSX owns input'
 send(b'\x1b');send(b'PRIVATE_DRAFT_RETAINED_708')
 wait(b'WORKFLOW_IDLE_NOTIFY_DONE_708','idle task notification consumed by same Harness without human submit')
 work=pathlib.Path(pathlib.Path(home,'completion-workdir.txt').read_text());assert (work/'child-artifact.txt').read_text()=='PHYSICAL_PTY_WORKFLOW_CHILD_ARTIFACT';assert (work/'notification-artifact.txt').read_text()=='PHYSICAL_PTY_NOTIFICATION_ARTIFACT'
 send(b'\r');wait(b'WORKFLOW_DRAFT_NEXT_DONE_708','human draft retained across actual idle model/tools');send(b'\x04\x04')
 receipt_file=pathlib.Path(home,'completion-receipt.json')
 while not receipt_file.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('normal exit receipt timeout')
  pump()
 receipt=json.loads(receipt_file.read_text());assert before==termios.tcgetattr(slave),'physical slave termios restoration';pathlib.Path(home,'completion-pty-release').write_text('restoration inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout')
  pump()
 assert p.returncode==0 and receipt['parentRequests']==5 and receipt['childRequests']==2 and receipt['terminalRestored']
 print(json.dumps({'actualIdleNotificationConsumer':True,'physicalChildAndParentArtifacts':True,'humanDraftPreserved':True,**receipt}))
finally:
 pathlib.Path(home,'completion-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
