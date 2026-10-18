"""Real agent definitions -> /mcp pre-auth -> browser callback -> actual child MCP."""
import fcntl,json,os,pathlib,select,socket,struct,subprocess,sys,termios,time,urllib.parse,urllib.request
from vt_screen import VTScreen
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',42,150,0,0));before=termios.tcgetattr(slave);captured=bytearray();screen=VTScreen(150,42);deadline=time.monotonic()+28
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIAgentOnlyMCPPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
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
def authorize():
 file('agent-browser-url');wait('copy this URL manually','actual browser URL');assert 'URL >' not in screen.text() and '(c copy)' not in screen.text(),'agent Source has no manual callback TextInput/copy handler'
 address=root.joinpath('agent-browser-url').read_text();query=urllib.parse.parse_qs(urllib.parse.urlparse(address).query);return query['redirect_uri'][0]+'?code=private-agent-code&state='+urllib.parse.quote(query['state'][0])
try:
 wait('Claude Code','ordinary startup');send(b'/mcp\r');wait('agentauth · not connected (agent-only)','real agent-only inline server');send(b'\r');wait('Agentauth MCP Server','real agent-specific menu');wait('Used by: auth-worker','actual agent definition origin');send(b'\r');first=authorize();send(b'\x1b');wait('Agentauth MCP Server','Esc cancels real pre-auth listener')
 port=urllib.parse.urlparse(first).port
 with socket.socket() as probe:probe.settimeout(.5);assert probe.connect_ex(('127.0.0.1',port))!=0,'cancelled callback listener survived'
 root.joinpath('agent-browser-url').unlink();send(b'\r');callback=authorize()
 with urllib.request.urlopen(callback,timeout=3) as response:assert response.status==200,'real browser callback failed'
 wait('Authentication successful for agentauth. The server will connect when the agent runs.','Source pre-auth completes without eager peer connect');wait('agentauth · not connected (agent-only)','agent remains deferred until actual worker');send(b'\x1b');send(b'PUBLIC_AGENT_MCP_RUN_CHILD_928\r');wait('PUBLIC_AGENT_MCP_PARENT_DONE_928','actual Agent child consumed saved OAuth and tool');send(b'PUBLIC_AGENT_MCP_NEXT_HUMAN_928\r');wait('PUBLIC_AGENT_MCP_NEXT_HUMAN_DONE_928','same parent Harness next human');send(b'\x04\x04');file('agent-mcp-receipt.json')
 receipt=json.loads(root.joinpath('agent-mcp-receipt.json').read_text());assert receipt['requests']==5 and receipt['tokens']==1 and receipt['initializes']==1 and receipt['tools']==1 and receipt['exitCode']==0 and all(v for k,v in receipt.items() if k not in ['requests','tokens','initializes','tools','exitCode']) and termios.tcgetattr(slave)==before,receipt;root.joinpath('agent-mcp-termios-release').write_text('physical terminal restored')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('normal exit deadline')
  pump()
 assert p.returncode==0,(receipt,repr(bytes(captured[-16000:])))
 print(json.dumps({'realAgentOnlyBrowserPreauthCancelAndActualChildMCP':True,**receipt}))
finally:
 root.joinpath('agent-mcp-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait(timeout=3)
 os.close(master);os.close(slave)
