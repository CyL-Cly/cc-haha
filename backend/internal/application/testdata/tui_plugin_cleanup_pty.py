"""Actual plugin uninstall data y/n/Esc and shared-project local override."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',42,150,0,0));before=termios.tcgetattr(slave);captured=bytearray();screen=VTScreen(150,42);deadline=time.monotonic()+28
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIPluginCleanupPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def wait(text,label):
 while text not in screen.text():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-15000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.15);pump()
def file(name):
 while not root.joinpath(name).exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('missing receipt '+name+' '+repr(bytes(captured[-10000:])))
  pump()
def choose(text):
 send(b'\x1b[H')
 for _ in range(18):
  if any(line.startswith('> ') and text in line for line in screen.text().splitlines()):return
  send(b'\x1b[B')
 raise AssertionError('cannot select '+text+' '+screen.text())
def install(name):
 send(b'/plugin\r');wait('Plugins · discover','real Discover');choose(name+'@mkt');send(b' ');send(b'\r');wait('Installed 1 plugin. Run /reload-plugins to activate.','actual install');send(b'\t');wait('Plugins · installed','real Installed');choose(name+'@mkt');send(b'\r');wait('Uninstall','actual installed detail');choose('Uninstall');send(b'\r')
try:
 wait('Claude Code','normal CLI');file('cleanup-paths.json');paths=json.loads(root.joinpath('cleanup-paths.json').read_text());config=pathlib.Path(paths['config']);work=pathlib.Path(paths['work'])
 install('zeta');wait('Delete it along with the plugin?','actual keep/delete prompt');send(b'\r');assert config.joinpath('plugins/data/zeta-mkt/state').exists(),'Enter deleted data';send(b'\x1b');wait('Uninstall','Esc cancels actual uninstall');assert json.loads(config.joinpath('settings.json').read_text())['pluginConfigs']['zeta@mkt']['options']['region']=='Zeta saved';choose('Uninstall');send(b'\r');wait('Delete it along with the plugin?','reopen actual cleanup');send(b'n');wait('Plugins · installed','keep uninstalls');assert config.joinpath('plugins/data/zeta-mkt/state').exists();send(b'\x1b');send(b'PUBLIC_PLUGIN_KEEP_REQUEST_925\r');wait('PUBLIC_PLUGIN_KEEP_NEXT_925','same ASK after saved config delete and keep-data')
 install('alpha');wait('Delete it along with the plugin?','actual delete-data prompt');send(b'y');wait('Plugins · installed','delete uninstalls');assert not config.joinpath('plugins/data/alpha-mkt').exists();send(b'\x1b');send(b'PUBLIC_PLUGIN_DELETE_REQUEST_925\r');wait('PUBLIC_PLUGIN_DELETE_NEXT_925','same ASK after actual data delete')
 send(b'/plugin install shared@mkt\r');wait('Install for all collaborators','actual scope chooser');choose('Install for all collaborators');send(b'\r');wait('Plugins · discover','actual project installation');send(b'\t');wait('Plugins · installed','project Installed');choose('shared@mkt');send(b'\r');wait('Uninstall','shared detail');choose('Uninstall');send(b'\r');wait('Disable it just for you in .claude/settings.local.json?','actual shared confirmation');send(b'n');wait('Uninstall','confirmation no retains install');assert json.loads(work.joinpath('.claude/settings.json').read_text())['enabledPlugins']['shared@mkt'] is True;choose('Uninstall');send(b'\r');wait('Disable it just for you','actual re-confirm');send(b'y');wait('Disabled shared in .claude/settings.local.json.','actual local override');assert json.loads(work.joinpath('.claude/settings.json').read_text())['enabledPlugins']['shared@mkt'] is True and json.loads(work.joinpath('.claude/settings.local.json').read_text())['enabledPlugins']['shared@mkt'] is False;send(b'\x1b');send(b'PUBLIC_PLUGIN_PROJECT_REQUEST_925\r');wait('PUBLIC_PLUGIN_PROJECT_NEXT_925','next ASK consumes true project/false local override');send(b'\x04\x04');file('cleanup-receipt.json')
 receipt=json.loads(root.joinpath('cleanup-receipt.json').read_text());assert receipt['requests']==3 and receipt['exitCode']==0 and receipt['actualDataAndScopedSettings'] and receipt['terminalRestored'] and termios.tcgetattr(slave)==before,receipt;root.joinpath('cleanup-termios-release').write_text('physical terminal restored')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('normal exit deadline')
  pump()
 assert p.returncode==0,receipt
 print(json.dumps({'rawDataYNEscapeAndSharedConfirmation':True,'sameASKAfterEachActualProducer':True,**receipt}))
finally:
 root.joinpath('cleanup-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait(timeout=3)
 os.close(master);os.close(slave)
