"""Actual CLI plugin installation, option trust domains, reload and skill."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,140,0,0));captured=bytearray();deadline=time.monotonic()+35;before=termios.tcgetattr(slave);screen=VTScreen(140,36)
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIPluginPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:
   data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def wait(needle,label,start=0):
 while needle.decode() not in screen.text():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.18);pump()
def settings(config):return json.loads((config/'settings.json').read_text())
def choose(label):
 send(b'\x1b[H')
 for _ in range(20):
  if any(line.startswith('> ') and label in line for line in screen.text().splitlines()):return
  send(b'\x1b[B')
 raise AssertionError('Actual visible choice missing '+label+' '+screen.text())
try:
 wait(b'Claude Code','startup');send(b'/plugins install sample@mkt\r');wait(b'Install for you (user scope)','actual Source scoped discovery detail');send(b'\r');wait(b'Configure sample','actual installed manifest form')
 send(b'North\r');wait(b'Configure Chat','actual per-channel form');send(b'PRIVATE_CHANNEL_SECRET\r');wait(b'Run /reload-plugins','Source explicit activation boundary');send(b'\x1b')
 send(b'/plugin manage\r');wait(b'sample@mkt','installed native list after configuration');send(b'\r');wait(b'Configure options','actual Manage options entry');choose('Configure options');send(b'\r');wait(b'Configure sample','explicit full top schema');send(b'\tPRIVATE_TOP_SECRET\r');wait(b'Configuration saved.','actual top save returns details');send(b'\x1b');wait('Plugins · installed'.encode(),'return actual installed parent');send(b'\x1b');wait(b'\n>','ordinary input after closing plugin parent')
 config=pathlib.Path(pathlib.Path(home,'plugins-config.txt').read_text());value=settings(config);assert value['pluginConfigs']['sample@mkt']['options']['region']=='North' and value['futureUser']=={'keep':True};secrets=json.loads((config/'.credentials.json').read_text())['pluginSecrets'];assert secrets['sample@mkt']['secret']=='PRIVATE_TOP_SECRET' and secrets['sample@mkt/chat']['token']=='PRIVATE_CHANNEL_SECRET';assert b'PRIVATE_TOP_SECRET' not in captured and b'PRIVATE_CHANNEL_SECRET' not in captured,'typed secrets leaked from masked local form';assert 'PRIVATE_' not in (config/'settings.json').read_text()
 send(b'/reload-plugins\r');wait(b'Reloaded:','actual same-session activation');send(b'/sample:sample supplied arguments\r');wait(b'PLUGIN_PUBLIC_SKILL_NEXT_DONE_341','saved options consumed by actual skill model input')
 send(b'/plugin manage\r');wait(b'sample@mkt','installed native list');send(b' ')
 while settings(config)['enabledPlugins']['sample@mkt'] is not False:
  if time.monotonic()>deadline:raise AssertionError('Space did not execute Source Plugin toggle consumer')
  pump()
 send(b'\x1b');send(b'/reload-plugins\r')
 start=len(captured);send(b'/plugin marketplace\r');wait(b'a add marketplace','actual native marketplace list',start);send(b'\r');wait(b'Enable auto-update','Source auto-update availability');choose('Enable auto-update');send(b'\r')
 known=config/'plugins/known_marketplaces.json'
 while json.loads(known.read_text())['mkt'].get('autoUpdate') is not True:
  if time.monotonic()>deadline:raise AssertionError('actual marketplace auto-update writer did not run')
  pump()
 send(b'\x1b');send(b'\x04\x04')
 receiptpath=pathlib.Path(home,'plugins-receipt.json')
 while not receiptpath.exists():
  if time.monotonic()>deadline:raise AssertionError('joined terminal receipt timeout')
  pump()
 receipt=json.loads(receiptpath.read_text());assert receipt['requests']==1 and receipt['terminalRestored'] and termios.tcgetattr(slave)==before,receipt
 pathlib.Path(home,'plugins-termios-release').write_text('actual slave termios verified')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout')
  pump()
 assert p.returncode==0,receipt
 print(json.dumps({'actualNativeInstall':True,'optionsAndChannelCredentialDomains':True,'maskedSecretForm':True,'sameHarnessReloadAndSkill':True,'realPluginShortcutToggle':True,'actualMarketplaceAutoUpdate':True,**receipt}))
finally:
 pathlib.Path(home,'plugins-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
