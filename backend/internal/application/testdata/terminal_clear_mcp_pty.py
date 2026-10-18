"""Public RunCommand /clear -> actual memoized MCP catalog -> next model HTTP."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',32,132,0,0))
before=termios.tcgetattr(slave);captured=bytearray();deadline=time.monotonic()+32
def setup():
 os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTerminalClearMCPPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-5000:])))
  pump()
def send(value):
 os.write(master,value);time.sleep(.12);pump()
try:
 wait(b'Claude Code','startup');wait(b'OWNED_CLEAR_MCP_CATALOG_READY','real catalog initial publication')
 start=len(captured);send(b'PUBLIC_CLEAR_MCP_HUMAN_1\r');wait(b'PUBLIC_CLEAR_MCP_COMPLETE_1','initial same Harness',start)
 start=len(captured);send(b'/clear\r');wait(b'Claude Code','original clear logo',start);wait(b'OWNED_CLEAR_MCP_CATALOG_READY','actual fresh catalog publication',start)
 start=len(captured);send(b'PUBLIC_CLEAR_MCP_HUMAN_2\r');wait(b'PUBLIC_CLEAR_MCP_COMPLETE_2','next model receives restored MCP catalog',start)
 send(b'\x04\x04')
 receipt_file=pathlib.Path(home,'clear-mcp-receipt.json')
 while not receipt_file.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('normal clear exit receipt missing '+repr(bytes(captured[-5000:])))
  pump()
 receipt=json.loads(receipt_file.read_text());assert receipt['terminalRestored'] and receipt['code']==0,receipt
 assert receipt['initialize']==1 and receipt['lists']==1 and receipt['prompts']==1 and receipt['models']==2,receipt
 assert termios.tcgetattr(slave)==before,'real slave termios not restored'
 pathlib.Path(home,'clear-mcp-pty-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('public CLI remained pending')
  pump()
 assert p.returncode==0,p.returncode
 print(json.dumps({'terminalRestored':True,'actualModelRequests':2,'samePhysicalMCPInitialize':1,'exitCode':p.returncode}))
finally:
 pathlib.Path(home,'clear-mcp-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
