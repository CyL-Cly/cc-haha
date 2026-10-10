import os,sys,pty,select,subprocess,time,json,re,termios,signal,fcntl,struct
binary,root,work=sys.argv[1:]
master,slave=pty.openpty()
fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack("HHHH",32,120,0,0))
before=termios.tcgetattr(slave)
env=os.environ.copy()
def child_terminal():
 os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
proc=subprocess.Popen([binary,'--no-computer-use'],cwd=work,env=env,stdin=slave,stdout=slave,stderr=slave,preexec_fn=child_terminal,close_fds=True)
output=bytearray()
deadline=time.monotonic()+55
def checkpoint(label): print(json.dumps({"phase":label}),flush=True)
def plain():
 return re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07]*(?:\x07|\x1b\\)', '',output.decode('utf8',errors='replace')).replace('\r','')
def wait(text,timeout=20):
 limit=min(deadline,time.monotonic()+timeout)
 while time.monotonic()<limit:
  if text in plain(): return
  if proc.poll() is not None: raise AssertionError('CLI exited before '+text+': '+plain()[-4000:])
  if select.select([master],[],[],.1)[0]:
   try: output.extend(os.read(master,65536))
   except OSError: pass
 proc.send_signal(signal.SIGQUIT)
 for _ in range(10):
  if select.select([master],[],[],.1)[0]:
   try: output.extend(os.read(master,65536))
   except OSError: break
 raise AssertionError('Missing '+text+': '+plain()[-12000:])
def send(value): os.write(master,(value+'\r').encode())
try:
 wait('Claude Code');checkpoint('ready')
 send('/model help');wait('Run /model to open the model selection menu');checkpoint('help')
 send('/model opus');wait('Set model to');checkpoint('opus')
 send('/model MiXeD-Public-Custom');wait('Set model to MiXeD-Public-Custom');checkpoint('custom')
 send('typed public next');wait('PUBLIC_TYPED_NEXT');checkpoint('next-ask')
 send('/model default');wait('(default)');checkpoint('default')
 send('/exit')
 limit=time.monotonic()+15
 while proc.poll() is None:
  if time.monotonic()>limit: raise AssertionError('public typed /exit did not close')
  if select.select([master],[],[],.05)[0]:
   try: output.extend(os.read(master,65536))
   except OSError: pass
 code=proc.returncode
 after=termios.tcgetattr(master)
 assert code==0,(code,plain()[-4000:])
 assert before==after,('terminal not restored',before,after)
 open(os.path.join(root,'public-tui-output.txt'),'w').write(plain())
 print(json.dumps({'terminalRestored':True,'exitCode':code,'publicTypedCommands':4,'physicalNextASK':True}))
finally:
 if proc.poll() is None:
  proc.send_signal(signal.SIGTERM)
  limit=time.monotonic()+5
  while proc.poll() is None and time.monotonic()<limit:
   if select.select([master],[],[],.05)[0]:
    try: output.extend(os.read(master,65536))
    except OSError: pass
  if proc.poll() is None: proc.kill();proc.wait()
 os.close(master);os.close(slave)
