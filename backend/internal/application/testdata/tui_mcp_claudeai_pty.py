"""Actual public connector catalog, private browser URL, Enter and clear lifecycle."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',42,150,0,0));before=termios.tcgetattr(slave);captured=bytearray();screen=VTScreen(150,42);deadline=time.monotonic()+36
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIClaudeAIConnectorPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def wait(text,label):
 while text not in screen.text():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-14000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.16);pump()
def file(name):
 while not root.joinpath(name).exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('missing '+name+' '+repr(bytes(captured[-14000:])))
  pump()
def choose(text):
 send(b'\x1b[H')
 for _ in range(12):
  if any(line.startswith('> ') and text in line for line in screen.text().splitlines()):return
  send(b'\x1b[B')
 raise AssertionError('cannot select '+text+' '+screen.text())
def detail(status):
 send(b'/mcp\r');wait('claude.ai Owned · '+status,'actual account catalog');send(b'\r');wait('Claude.ai Owned MCP Server','actual proxy menu')
def clear():
 detail('connected');choose('Clear authentication');root.joinpath('proxy-browser-url').unlink(missing_ok=True);send(b'\r');wait('Press Enter to open','first clear step');assert not root.joinpath('proxy-browser-url').exists(),'clear opened browser before first Enter';send(b'\r');file('proxy-browser-url');assert root.joinpath('proxy-browser-url').read_text()=='https://claude.ai/settings/connectors';wait('Press Enter when done.','second clear step')
def prompt():send(b'\x1b');send(b'\x1b');send(b'\x1b')
try:
 wait('Claude Code','normal CLI');detail('needs-auth');choose('Authenticate');send(b'\r');wait('Authenticating with claude.ai Owned','proxy auth');file('proxy-browser-url');assert root.joinpath('proxy-browser-url').read_text()=='https://claude.ai/api/organizations/private-public-org/mcp/start-auth/mcpsrv-public?product_surface=cli';root.joinpath('proxy-browser-grant').write_text('owned external connector grant');send(b'\r');wait('Authentication successful. Connected to claude.ai Owned.','real proxy reconnect');send(b'\x1b');send(b'PUBLIC_PROXY_TOOL_REQUEST_813\r');wait('PUBLIC_PROXY_TOOL_DONE_813','actual tool and same Harness HTTP')
 clear();prompt();send(b'PUBLIC_PROXY_CANCELLED_CLEAR_REQUEST_813\r');wait('PUBLIC_PROXY_CANCELLED_CLEAR_DONE_813','Esc preserves real catalog')
 clear();send(b'\r');wait('Disconnected from claude.ai Owned.','real local connector clear');send(b'\x1b');send(b'PUBLIC_PROXY_CONFIRMED_CLEAR_REQUEST_813\r');wait('PUBLIC_PROXY_CONFIRMED_CLEAR_DONE_813','confirmed clear excludes tool next HTTP')
 detail('needs-auth');choose('Authenticate');send(b'\r');wait('Authenticating with claude.ai Owned','global close held browser');send(b'\x04\x04');file('proxy-receipt.json');receipt=json.loads(root.joinpath('proxy-receipt.json').read_text());assert receipt['exitCode']==0 and receipt['catalogs']==1 and receipt['connections']==1 and receipt['tools']==1 and receipt['models']==4 and receipt['cachedAccountAndUnknownFieldsPreserved'] and receipt['terminalRestored'] and termios.tcgetattr(slave)==before,receipt;root.joinpath('proxy-termios-release').write_text('observed physical termios')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('actual proxy CLI failed to join')
  pump()
 assert p.returncode==0,receipt
 print(json.dumps(receipt))
finally:
 root.joinpath('proxy-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait(timeout=3)
 os.close(master);os.close(slave)
