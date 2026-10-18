"""Normal /mcp OAuth UI, actual PKCE HTTP, manual redirect, abort and close."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time,urllib.parse
from vt_screen import VTScreen
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',42,150,0,0));before=termios.tcgetattr(slave);captured=bytearray();screen=VTScreen(150,42);deadline=time.monotonic()+33
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIMCPAuthenticationPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def wait(text,label):
 while text not in screen.text():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-16000:])))
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
def open_auth():
 send(b'/mcp\r');wait('auth ·','ordinary MCP peer');send(b'\r');wait('Auth MCP Server','real remote menu');choose('Authenticate');root.joinpath('auth-browser-url').unlink(missing_ok=True);send(b'\r');wait('Authenticating with auth','owned auth UI');file('auth-browser-url');wait('URL >','actual manual callback field')
def callback():
 address=root.joinpath('auth-browser-url').read_text();query=urllib.parse.parse_qs(urllib.parse.urlparse(address).query);url=query['redirect_uri'][0]+'?code=private-code&state='+urllib.parse.quote(query['state'][0]);send(b'\x1b[200~'+url.encode()+b'\x1b[201~\r')
try:
 wait('Claude Code','normal CLI');open_auth();send(b'c');file('auth-clipboard-url');assert root.joinpath('auth-clipboard-url').read_text()==root.joinpath('auth-browser-url').read_text(),'actual clipboard stdin differs';wait('Copied!','copy feedback');send(b'\x15');callback();wait('Authentication successful. Connected to auth.','actual manual PKCE + reconnect');wait('auth · connected','actual peer publication');send(b'\x1b');send(b'PUBLIC_MCP_AUTH_TOOL_REQUEST_724\r');wait('PUBLIC_MCP_AUTH_TOOL_DONE_724','real authenticated tool through same Harness')
 send(b'/mcp\r');wait('auth · connected','cached actual access token');send(b'\r');wait('Auth MCP Server','ordinary authenticated menu');choose('Clear authentication');send(b'\r');wait('Authentication cleared for auth.','actual revoke and disconnect');send(b'\x1b');send(b'PUBLIC_MCP_AUTH_CLEARED_REQUEST_724\r');wait('PUBLIC_MCP_AUTH_CLEARED_DONE_724','next request excludes cleared tools')
 open_auth();callback();file('auth-token-held');send(b'\x1b');wait('Auth MCP Server','Esc unmounts and joins held exchange');file('auth-token-cancelled');send(b'\x1b');wait('MCP servers','cancel back to real server list');send(b'\x1b');send(b'PUBLIC_MCP_AUTH_CANCELLED_REQUEST_724\r');wait('PUBLIC_MCP_AUTH_CANCELLED_DONE_724','next human after actual cancellation')
 open_auth();send(b'\x04\x04');file('auth-receipt.json');receipt=json.loads(root.joinpath('auth-receipt.json').read_text());assert receipt['requests']==4 and receipt['tokens']==2 and receipt['revokes']==2 and receipt['tools']==1 and receipt['exitCode']==0 and all(v for k,v in receipt.items() if k not in ['requests','tokens','revokes','tools','exitCode']) and termios.tcgetattr(slave)==before,receipt;root.joinpath('auth-termios-release').write_text('physical terminal restored')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('normal exit deadline')
  pump()
 assert p.returncode==0,receipt
 print(json.dumps({'actualManualCallbackAndNamespaceTool':True,'clearNextAskEscAndGlobalJoin':True,**receipt}))
finally:
 root.joinpath('auth-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait(timeout=3)
 os.close(master);os.close(slave)
