"""Actual normal CLI selection -> bulk installation -> real Skill -> next human."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',38,145,0,0));before=termios.tcgetattr(slave);captured=bytearray();screen=VTScreen(145,38);deadline=time.monotonic()+28
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIPluginBulkPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def wait(text,label):
 while text not in screen.text():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-10000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.18);pump()
def file(name):
 while not root.joinpath(name).exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('actual receipt missing '+name+' '+repr(bytes(captured[-10000:])))
  pump()
try:
 wait('Claude Code','normal CLI startup');send(b'/plugin\r');wait('Plugins · discover','actual plugin catalog');wait('> zeta@mkt','actual popularity order first');send(b' ');wait('[selected] zeta@mkt','native first selection');send(b'\x1b[B');wait('> alpha@mkt','actual next catalog row');send(b' ');wait('[selected] alpha@mkt','native second selection');send(b'\r');wait('Installed 2 plugins. Run /reload-plugins to activate.','actual bulk completion')
 assert 'Configure' not in screen.text(),'bulk incorrectly entered single-install post configuration'
 config=pathlib.Path(root.joinpath('bulk-config.txt').read_text());settings=json.loads(config.joinpath('settings.json').read_text());assert list(settings['enabledPlugins'])==['zeta@mkt','alpha@mkt'] and 'pluginConfigs' not in settings,settings
 send(b'\x1b');wait('\n>','actual panel closes');send(b'/reload-plugins\r');wait('Reloaded:','explicit Source activation');send(b'/zeta:run\r');wait('PUBLIC_BULK_SKILL_DONE_884','actual installed Skill through same Harness');send(b'PUBLIC_BULK_NEXT_HUMAN_REQUEST_884\r');wait('PUBLIC_BULK_HUMAN_DONE_884','ordinary next human');send(b'\x04\x04');file('bulk-receipt.json')
 receipt=json.loads(root.joinpath('bulk-receipt.json').read_text());assert receipt['requests']==2 and receipt['actualBulkSettingsNoPostConfiguration'] and receipt['terminalRestored'] and termios.tcgetattr(slave)==before,receipt;root.joinpath('bulk-termios-release').write_text('physical terminal restored')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('normal exit deadline')
  pump()
 assert p.returncode==0,receipt
 print(json.dumps({'actualPopularitySelectionOrder':True,'bulkNoSingleOptionsDialog':True,'actualSkillAndNextHuman':True,**receipt}))
finally:
 root.joinpath('bulk-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait(timeout=3)
 os.close(master);os.close(slave)
