"""Actual public CLI -> nine real LSP queries, Write, diagnostics, next human."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',42,150,0,0));before=termios.tcgetattr(slave);captured=bytearray();screen=VTScreen(150,42);deadline=time.monotonic()+33
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTerminalLSPPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def wait(text):
 while text not in screen.text():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(text+' '+repr(bytes(captured[-20000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.12);pump()
try:
 wait('Claude Code');send(b'PUBLIC_LSP_START_731\r');wait('PUBLIC_LSP_NINE_OPERATIONS_DONE_731');send(b'PUBLIC_LSP_NEXT_HUMAN_731\r');wait('PUBLIC_LSP_NEXT_HUMAN_DONE_731');send(b'\x04\x04')
 while not root.joinpath('lsp-pty-receipt.json').exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('actual normal exit receipt '+repr(bytes(captured[-22000:])))
  pump()
 value=json.loads(root.joinpath('lsp-pty-receipt.json').read_text());assert value['requests']==13 and value['exitCode']==0 and all(v for k,v in value.items() if k not in ['requests','exitCode','methods']),value
 assert termios.tcgetattr(slave)==before,'physical slave terminal not restored';root.joinpath('lsp-pty-release').write_text('physical inspection completed')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('normal process exit held')
  pump()
 assert p.returncode==0,(value,repr(bytes(captured[-22000:])))
 print(json.dumps({'publicRunCommand':True,'physicalSlaveRestored':True,**value}))
finally:
 root.joinpath('lsp-public-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait(timeout=3)
 os.close(master);os.close(slave)
