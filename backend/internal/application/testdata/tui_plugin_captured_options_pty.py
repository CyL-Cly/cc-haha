"""Actual selected manifest survives physical update/uninstall and saves through the same CLI."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home,operation=sys.argv[1:];root=pathlib.Path(home)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',40,150,0,0));before=termios.tcgetattr(slave);captured=bytearray();screen=VTScreen(150,40);deadline=time.monotonic()+22
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIPluginCapturedOptionsPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def wait(text):
 while text not in screen.text():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(text+' '+repr(bytes(captured[-14000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.12);pump()
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
try:
 wait('Claude Code');file('captured-paths.json');paths=json.loads(root.joinpath('captured-paths.json').read_text());config=pathlib.Path(paths['config']);plugin=pathlib.Path(paths['plugin'])
 send(b'/plugin\r');wait('Plugins · discover');choose('zeta@mkt');send(b' ');wait('[selected] zeta@mkt');send(b'\r');wait('Installed 1 plugin. Run /reload-plugins to activate.');send(b'\x1b')
 if operation=='raw':
  send(b'/plugin disable zeta@mkt\r');wait('Plugins · installed');wait('disabled');send(b'\x1b');send(b'/plugin enable zeta@mkt\r')
 else:
  send(b'/plugin manage\r');wait('Plugins · installed');choose('zeta@mkt');send(b'\r');wait('Update now')
  if operation=='update':
   manifest=plugin.joinpath('.claude-plugin/plugin.json');value=json.loads(manifest.read_text());value['version']='2.0.0';value['userConfig']={'replacement':{'type':'string','title':'Replacement schema','description':'Actual replacement option','required':True}};manifest.write_text(json.dumps(value));choose('Update now')
  else:choose('Uninstall')
  send(b'\r')
 wait('Configure Raw channel' if operation=='raw' else 'Configure zeta')
 if operation=='raw':
  for label,old,value in [('High A','old-a','alpha'),('High B','old-b','beta'),('Low','old-c','gamma'),('Replacement character','old-d','delta'),('Literal binding name','old-e','epsilon'),('Number','invalid','0x10')]:
   wait(label);wait(old)
   for _ in old:send(b'\x7f')
   send(b'\x1b[200~'+value.encode()+b'\x1b[201~');wait(value);send(b'\r')
 else:
  assert 'Region' in screen.text() and 'Replacement schema' not in screen.text(),screen.text()
  installed=json.loads(config.joinpath('plugins/installed_plugins.json').read_text())['plugins'];assert (not installed.get('zeta@mkt')) if operation=='uninstall' else installed['zeta@mkt'][0]['version']=='2.0.0'
  send(b'\x1b[200~Captured region\x1b[201~');wait('Captured region');send(b'\r')
 wait('✓ Enabled and configured zeta. Run /reload-plugins to apply.')
 saved=json.loads(config.joinpath('settings.json').read_text())
 if operation=='raw':
  assert saved['pluginConfigs']['zeta@mkt']['mcpServers']['chat']=={'\ud800':'alpha','\ud801':'beta','\udc00':'gamma','\ufffd':'delta','$plugin-option:d800':'epsilon','amount':16},saved
  assert saved['pluginConfigs']['other@mkt']['options']=={'\ud802':'untouched old raw','\ufffd':'separate old replacement'} and saved['futureUser']=={'keep':True},saved
 else:assert saved['pluginConfigs']['zeta@mkt']['options']=={'region':'Captured region'}
 send(b'\x1b');send(b'CAPTURED_OPTIONS_NEXT_REQUEST\r');wait('CAPTURED_OPTIONS_NEXT_DONE');send(b'\x04\x04');file('captured-receipt.json');receipt=json.loads(root.joinpath('captured-receipt.json').read_text());assert receipt['requests']==1 and receipt['invalid']==0 and receipt['actualCapturedOptions'] and receipt['exitCode']==0 and receipt['terminalRestored'] and termios.tcgetattr(slave)==before,receipt;root.joinpath('captured-termios-release').write_text('actual slave restored')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('normal process exit deadline')
  pump()
 assert p.returncode==0,(receipt,bytes(captured[-4000:]))
 print(json.dumps({'actualSourceSelectedManifest':True,'actualStoreAfterPhysicalOperation':True,'nextHumanAndNormalExit':True,**receipt}))
finally:
 root.joinpath('captured-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait(timeout=3)
 os.close(master);os.close(slave)
