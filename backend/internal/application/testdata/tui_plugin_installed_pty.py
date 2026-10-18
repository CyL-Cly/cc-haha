"""Public CLI plugin Installed -> raw flagged lifecycle -> actual stdio menu/tool."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',42,150,0,0));before=termios.tcgetattr(slave);captured=bytearray();screen=VTScreen(150,42);deadline=time.monotonic()+28
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIPluginInstalledPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
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
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('missing receipt '+name+' '+repr(bytes(captured[-10000:])))
  pump()
def choose(text):
 send(b'\x1b[H')
 for _ in range(20):
  if any(line.startswith('> ') and text in line for line in screen.text().splitlines()):return
  send(b'\x1b[B')
 raise AssertionError('cannot select '+text+' '+screen.text())
try:
 wait('Claude Code','normal CLI startup');send(b'/plugin\r');wait('> zeta@mkt','actual Discover');send(b' ');wait('[selected] zeta@mkt','actual bulk selection before accepting');send(b'\r');wait('Installed 1 plugin. Run /reload-plugins to activate.','actual bulk single selection')
 send(b'\t');wait('Plugins · installed','Installed actual unified catalog');wait('flagged @ mkt','real shared flagged cache');choose('flagged @ mkt');send(b'\r');wait('Status: Removed','flagged detail');wait('reason: delisted','actual reason');send(b'\x1b');wait('Plugins · installed','flagged cancel preserves');choose('flagged @ mkt');send(b'\r');wait('Status: Removed','flagged reopen');send(b'\r');wait('Plugins · installed','actual dismiss')
 config=pathlib.Path(root.joinpath('installed-config.txt').read_text());flags=json.loads(config.joinpath('plugins/flagged-plugins.json').read_text())['plugins'];assert 'flagged@mkt' not in flags and flags['\ud800@mkt']['seenAt'],repr(flags)
 choose('zeta@mkt');send(b'\r');wait('Mark for update','Source draft mark');choose('Mark for update');send(b'\r');wait('Local plugins cannot be updated remotely','real local-source update guard');send(b'\x1b');wait('Plugins · installed','plugin back')
 choose('owned ·');send(b'\r');wait('Owned MCP Server','same cached peer');wait('Command: /usr/bin/python3','actual stdio command');wait('Capabilities: tools','actual metadata');send(b'\x03');wait('Owned MCP Server','first Ctrl-C retains mounted stdio menu');choose('View tools');send(b'\r');wait('Tools for owned','actual tool list');wait('Friendly calculator','actual annotation title');send(b'\r');wait('Full name: mcp__owned__calculate','actual tool full name');wait('n (required): number - Number to double','actual input schema');wait('Full actual MCP description','full description');send(b'\x03');wait('Full name: mcp__owned__calculate','first Ctrl-C retains actual tool detail');send(b'\x1b');wait('Tools for owned','tool back');send(b'\x1b');wait('Owned MCP Server','list back');choose('Disable');send(b'\r');wait('Plugins · installed','actual disable returns parent');wait('owned · user · disabled','actual peer disabled')
 send(b'\x1b');send(b'PUBLIC_INSTALLED_DISABLED_REQUEST_673\r');wait('PUBLIC_INSTALLED_DISABLED_DONE_673','disabled next model catalogue');send(b'/mcp\r');wait('MCP servers','ordinary MCP list');wait('owned · disabled','same actual peer');wait('[Failed to parse] Project config (shared via .mcp.json)','actual project parse status');wait('[Error] MCP config is not a valid JSON','actual malformed file error');wait('[Warning] [warning-peer] mcpServers.warning-peer: Missing environment variables: CC_HAHA_WARNING_MISSING','actual ancestor environment warning');choose('owned ·');send(b'\r');wait('Owned MCP Server','ordinary shared menu');choose('Enable');send(b'\r');wait('MCP servers','enable returns list');wait('owned · connected','actual reconnect completion');send(b'\x1b');send(b'/reload-plugins\r');wait('Reloaded:','explicit plugin activation');send(b'/zeta:run\r');wait('PUBLIC_INSTALLED_TOOL_DONE_673','same Harness Skill and physical MCP call');send(b'PUBLIC_INSTALLED_NEXT_HUMAN_REQUEST_673\r');wait('PUBLIC_INSTALLED_NEXT_HUMAN_DONE_673','ordinary next human');send(b'/mcp\r');wait('MCP servers','real MCP list before global exit');send(b'\x04');wait('MCP servers','first Ctrl-D retains actual list');send(b'\x04');file('installed-receipt.json')
 receipt=json.loads(root.joinpath('installed-receipt.json').read_text());assert receipt['requests']==4 and receipt['exitCode']==0 and all(v for k,v in receipt.items() if k not in ['requests','exitCode']) and termios.tcgetattr(slave)==before,receipt;root.joinpath('installed-termios-release').write_text('physical terminal restored')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('normal exit deadline')
  pump()
 assert p.returncode==0,receipt
 print(json.dumps({'publicInstalledFlagAndStdioMenus':True,'sourceConfirmationAndSameHarness':True,**receipt}))
finally:
 root.joinpath('installed-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait(timeout=3)
 os.close(master);os.close(slave)
