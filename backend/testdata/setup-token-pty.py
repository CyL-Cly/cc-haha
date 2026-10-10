import os,pty,select,signal,subprocess,sys,time,fcntl,termios,struct,json,re
master,slave=pty.openpty()
fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',40,100,0,0))
original=termios.tcgetattr(slave)
child=subprocess.Popen(sys.argv[1:],stdin=slave,stdout=slave,stderr=sys.stderr,start_new_session=True)
steps=json.loads(os.environ.get('CC_HAHA_SETUP_PTY_STEPS','[]'))
step=0
seen=b''
step_due=None
try:
 while True:
  ready,_,_=select.select([master],[],[],0.02)
  if ready:
   try:data=os.read(master,65536)
   except OSError:data=b''
   if data:
    os.write(sys.stdout.fileno(),data)
    seen+=data
  if step<len(steps):
   instruction=steps[step]
   text=re.sub(r'\x1b(?:\][^\x07]*(?:\x07|\x1b\\)|\[[0-?]*[ -/]*[@-~])','',seen.decode('utf8','replace'))
   if step_due is None and re.sub(r'\s','',instruction.get('wait','')) in re.sub(r'\s','',text):
    step_due=time.monotonic()+instruction.get('delay',0)
   if step_due is not None and time.monotonic()>=step_due:
    if 'send' in instruction:os.write(master,instruction['send'].encode('utf8'))
    if 'signal' in instruction:os.kill(child.pid,getattr(signal,instruction['signal']))
    if 'write' in instruction:
     # Only a parent-authorized path inside this fixture's private HOME.
     target=os.path.join(os.environ['HOME'],instruction['write'])
     if os.path.commonpath([target,os.environ['HOME']])!=os.environ['HOME']:raise RuntimeError('unowned PTY observation gate')
     open(target,'w').write('ready')
    seen=b''
    step+=1;step_due=None
  if child.poll() is not None and not ready:break
 result=child.wait()
 if os.environ.get('CC_HAHA_SETUP_PTY_RECEIPT'):
  before_probe=termios.tcgetattr(slave)
  pending=struct.unpack('i',fcntl.ioctl(slave,termios.FIONREAD,struct.pack('i',0)))[0]
  after_probe=termios.tcgetattr(slave)
  with open(os.environ['CC_HAHA_SETUP_PTY_RECEIPT'],'w') as f:
   json.dump({'exit':result,'steps':step,'termiosRestored':after_probe==original,'beforeProbe':repr(before_probe),'afterProbe':repr(after_probe),'original':repr(original),'pendingBytes':pending},f)
 sys.exit(result if result>=0 else 128-result)
finally:
 os.close(master);os.close(slave)
 if child.poll() is None:
  os.killpg(child.pid,signal.SIGTERM)
  try:child.wait(timeout=2)
  except subprocess.TimeoutExpired:os.killpg(child.pid,signal.SIGKILL);child.wait()
