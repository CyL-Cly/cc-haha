"""Public CLI MCPB Configure -> actual bundle/stdio/file -> next human."""
import fcntl,json,os,pathlib,re,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',38,140,0,0));before=termios.tcgetattr(slave);captured=bytearray();screen=VTScreen(140,38);deadline=time.monotonic()+32

def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIPluginMCPBPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def wait(text,label):
 while text not in screen.text():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-9500:])))
  pump()
def send(data):os.write(master,data);time.sleep(.18);pump()
def selectrow(label):
 for _ in range(12):
  if any(line.strip()=='> '+label for line in screen.text().splitlines()):send(b'\r');return
  send(b'\x1b[B')
 raise AssertionError('actual selectable row absent '+label+' '+screen.text())
def closeparent():send(b'\x1b');wait('Plugins · installed','actual installed parent');send(b'\x1b');wait('\n>','ordinary input restored')
try:
 wait('Claude Code','startup');send(b'/plugin install sample@mkt\r');wait('Install for you (user scope)','real bundle plugin discover');send(b'\r');wait('Run /reload-plugins','actual native bundle install');send(b'\x1b')
 send(b'/plugin manage\r');wait('Plugins · installed','actual installed plugins');send(b'\r');wait('Configure','actual MCPB Configure action');selectrow('Configure');wait('Configure bundle-peer','real bundle forced dialog');send(b'PUBLIC_BUNDLE_FIRST\tPRIVATE_BUNDLE_PUBLIC\r');wait('Configuration saved.','same loader saves actual bundle configuration')
 selectrow('Configure');wait('Configure bundle-peer','forced remount with saved config');assert '› PUBLIC_BUNDLE_FIRST█' in screen.text() and 'PRIVATE_BUNDLE_PUBLIC' not in screen.text();send(b'\x1b');wait('Version: 1.0.0','actual Cancel returns details')
 selectrow('Configure');wait('Configure bundle-peer','actual next edit form');send(b'\x7f'*len('PUBLIC_BUNDLE_FIRST')+b'PUBLIC_BUNDLE_FINAL\tPRIVATE_BUNDLE_PUBLIC\r');wait('Configuration saved.','actual second save');closeparent()
 paths=json.loads(root.joinpath('mcpb-paths.json').read_text());config=pathlib.Path(paths['config']);settings=json.loads(config.joinpath('settings.json').read_text());assert settings['pluginConfigs']['sample@mkt']['mcpServers']['bundle-peer']['word']=='PUBLIC_BUNDLE_FINAL' and settings['futureUser']=={'keep':True},settings;secret=json.loads(config.joinpath('.credentials.json').read_text())['pluginSecrets'];assert secret['sample@mkt/bundle-peer']['token']=='PRIVATE_BUNDLE_PUBLIC';assert b'PRIVATE_BUNDLE_PUBLIC' not in captured and 'PRIVATE_BUNDLE_PUBLIC' not in config.joinpath('settings.json').read_text()
 send(b'/reload-plugins\r');wait('Reloaded:','actual same-session activation');send(b'EXECUTE_PUBLIC_BUNDLE_TOOL_527\r');wait('PUBLIC_MCPB_ACTUAL_FILE_DONE_527','actual owned stdio tool and file');assert pathlib.Path(paths['target']).read_text()=='PUBLIC_BUNDLE_FINAL|PRIVATE_BUNDLE_PUBLIC'
 send(b'NEXT_PUBLIC_BUNDLE_HUMAN_527\r');wait('PUBLIC_MCPB_NEXT_HUMAN_DONE_527','same Harness next human');send(b'\x04\x04')
 while not root.joinpath('mcpb-receipt.json').exists():
  if time.monotonic()>deadline:raise AssertionError('joined receipt timeout')
  pump()
 receipt=json.loads(root.joinpath('mcpb-receipt.json').read_text());assert receipt['requests']==3 and receipt['actualBundleFile'] and receipt['nativeMCPChildJoined'] and receipt['terminalRestored'] and termios.tcgetattr(slave)==before,receipt;root.joinpath('mcpb-termios-release').write_text('actual termios checked')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('normal process exit timeout')
  pump()
 assert p.returncode==0,receipt
 print(json.dumps({'actualManageForcedConfig':True,'cancelRemountSave':True,'actualSecretDomainAndMasking':True,'sameHarnessBundledToolAndNextHuman':True,**receipt}))
finally:
 root.joinpath('mcpb-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait(timeout=3)
 os.close(master);os.close(slave)
