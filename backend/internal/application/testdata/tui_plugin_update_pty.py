"""Public installed Update now: unchanged shortcut, disk update, configuration, failure, same Harness."""
import fcntl,json,os,pathlib,select,shutil,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',40,150,0,0));before=termios.tcgetattr(slave);captured=bytearray();screen=VTScreen(150,40);deadline=time.monotonic()+28
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIPluginUpdatePTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def wait(text,label):
 while text not in screen.text():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-16000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.15);pump()
def file(name):
 while not root.joinpath(name).exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('missing '+name+' '+repr(bytes(captured[-10000:])))
  pump()
def choose(text):
 send(b'\x1b[H')
 for _ in range(20):
  if any(line.startswith('> ') and text in line for line in screen.text().splitlines()):return
  send(b'\x1b[B')
 raise AssertionError('cannot select '+text+' '+screen.text())
def update():
 send(b'/plugin manage\r');wait('Plugins · installed','installed actual list');choose('zeta@mkt');send(b'\r');wait('Update now','actual Source Update now entry');choose('Update now');send(b'\r')
try:
 wait('Claude Code','normal CLI');file('update-paths.json');paths=json.loads(root.joinpath('update-paths.json').read_text());config=pathlib.Path(paths['config']);plugin=pathlib.Path(paths['plugin'])
 send(b'/plugin\r');wait('Plugins · discover','actual catalogue');choose('zeta@mkt');send(b' ');wait('[selected] zeta@mkt','actual selection');send(b'\r');wait('Installed 1 plugin. Run /reload-plugins to activate.','physical install');send(b'\x1b');send(b'/reload-plugins\r');wait('Reloaded:','first actual activation');send(b'/zeta:run\r');wait('UPDATE_INITIAL_DONE_734','original Skill actual model')
 update();wait('zeta is already at the latest version (1.0.0).','Source early unchanged result');assert 'Configure zeta' not in screen.text(),'alreadyUpToDate entered options';assert 'pluginConfigs' not in json.loads(config.joinpath('settings.json').read_text()),'unchanged operation wrote missing options';send(b'\x1b')
 manifest=plugin.joinpath('.claude-plugin/plugin.json');value=json.loads(manifest.read_text());value['version']='2.0.0';manifest.write_text(json.dumps(value));plugin.joinpath('skills/run/SKILL.md').write_text('---\ndescription: Actual updated Skill\n---\nUPDATE_SKILL_TWO_734')
 update();wait('Configure zeta','actual updated post-enable missing options');send(b'\x1b[200~Updated region\x1b[201~');wait('Updated region','actual pasted configuration value');send(b'\r');wait('Plugins · installed','actual saved update');assert json.loads(config.joinpath('plugins/installed_plugins.json').read_text())['plugins']['zeta@mkt'][0]['version']=='2.0.0';send(b'\x1b');send(b'/zeta:run\r');wait('UPDATE_BEFORE_RELOAD_DONE_734','active old Skill until explicit Source reload');send(b'/reload-plugins\r');wait('1 queued','actual local command queued while physical model SSE remains held');root.joinpath('update-held-model-release').write_text('actual queued input released');
 while screen.text().count('Reloaded:')<2:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('queued reload output was lost '+repr(bytes(captured[-16000:])))
  pump()
 send(b'/zeta:run\r');wait('UPDATE_NEW_SKILL_DONE_734','new actual Skill reaches same Harness')
 shutil.rmtree(plugin);update();wait('Failed to update: Plugin source not found at','actual missing source failure');assert json.loads(config.joinpath('plugins/installed_plugins.json').read_text())['plugins']['zeta@mkt'][0]['version']=='2.0.0';send(b'\x1b');wait('Plugins · installed','failure Esc returns list');send(b'\x1b');send(b'UPDATE_NEXT_HUMAN_REQUEST_734\r');wait('UPDATE_NEXT_HUMAN_DONE_734','ordinary next human after error');send(b'\x04\x04');file('update-receipt.json')
 receipt=json.loads(root.joinpath('update-receipt.json').read_text());assert receipt['requests']==4 and receipt['invalidRequest']==0 and receipt['exitCode']==0 and receipt['actualDiskVersionOptionsAndOrphan'] and receipt['terminalRestored'] and termios.tcgetattr(slave)==before,receipt;root.joinpath('update-termios-release').write_text('actual slave restored')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('normal process exit deadline')
  pump()
 assert p.returncode==0,(receipt,bytes(captured[-4000:]))
 print(json.dumps({'actualUnchangedEarlyReturn':True,'actualUpdatedConfigurationAndDiskVersion':True,'actualOldAndNewSkillBeforeAfterReload':True,'actualMissingSourceErrorAndNextHuman':True,**receipt}))
finally:
 root.joinpath('update-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait(timeout=3)
 os.close(master);os.close(slave)
