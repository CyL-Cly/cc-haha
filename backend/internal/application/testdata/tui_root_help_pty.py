"""Original root shortcuts and committed settings/fullscreen mode changes."""
import fcntl,json,os,pathlib,re,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,100,0,0))
before=termios.tcgetattr(slave);screen=VTScreen(100,36);captured=bytearray();deadline=time.monotonic()+32
def setup():
 os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeRootHelpPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:
   data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def until(predicate,label):
 while not predicate():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' SCREEN '+repr(screen.text())+' RAW '+repr(bytes(captured[-6000:])))
  pump()
def send(value):os.write(master,value)
def ready():return any(line.startswith('> ') or line=='>' for line in screen.text().splitlines())
def help_general():return 'Claude understands your codebase' in screen.text()
def transcript():return 'Showing detailed transcript' in screen.text()
def alt():
 active=False
 for m in re.finditer(rb'\x1b\[\?1049([hl])',captured):active=m.group(1)==b'h'
 return active
def settings(flag):
 path=pathlib.Path(pathlib.Path(home,'root-help-settings-path').read_text())
 data=json.loads(path.read_text()) if path.exists() else {}
 data.setdefault('env',{})['CLAUDE_CODE_NO_FLICKER']=flag
 path.write_text(json.dumps(data))
def expanded(value):
 path=pathlib.Path(pathlib.Path(home,'root-help-global-path').read_text())
 data=json.loads(path.read_text())
 return data.get('showExpandedTodos',False) is (value=='tasks') and data.get('showSpinnerTree') is (value=='teammates') and data.get('futureRootView')=={'keep':True}
try:
 until(ready,'startup actual ordinary prompt');assert not alt(),'initial mode not ordinary'
 send(b'ROOT_HELP_HUMAN_1\r');until(lambda:'ROOT_HELP_DONE_1' in screen.text() and ready(),'same Harness initial request')
 send(b'/help\r');until(help_general,'actual ordinary Help')
 send(b'\x14');until(lambda:expanded('tasks'),'root expanded-view actual global Save')
 send(b'\x14');until(lambda:expanded('none'),'root collapsed-view actual global Save')
 send(b'\x0f');until(transcript,'root Ctrl-O remains active over local JSX')
 assert not ready(),'transcript unmounted completion wait'
 send(b'\x05');until(lambda:'ctrl+e to collapse' in screen.text(),'actual ordinary transcript show-all')
 send(b'\x1b');until(lambda:not transcript() and help_general(),'root Escape exits transcript while Help stays pending')
 settings('1');until(lambda:alt() and help_general() and '▔' in screen.text(),'actual settings env enters alternate screen without typing')
 send(b'\t');until(lambda:'Browse default commands:' in screen.text(),'actual fullscreen Help tab')
 send(b'\x0f');until(transcript,'fullscreen root transcript over Help')
 assert '↑↓ scroll' in screen.text() and '▔' not in screen.text(),'local JSX still rendered as centered modal in transcript'
 send(b'\x05');pump();assert 'ctrl+e to collapse' not in screen.text(),'virtual transcript enabled inactive show-all handler'
 send(b'\x0f');until(lambda:not transcript() and help_general() and '▔' in screen.text(),'actual prompt-branch remount resets Help tab')
 send(b'\x1b');until(lambda:not help_general() and ready(),'actual current-fullscreen zero-record completion')
 send(b'ROOT_HELP_HUMAN_2\r');until(lambda:'ROOT_HELP_DONE_2' in screen.text() and ready(),'next human after fullscreen dismissal')
 send(b'/help\r');until(help_general,'actual second Help mount')
 send(b'\t');until(lambda:'Browse default commands:' in screen.text(),'actual second Help selected tab')
 settings('0');until(lambda:not alt() and help_general() and '▔' not in screen.text(),'actual settings exits alternate screen and remounts Help')
 send(b'\x0f');until(transcript,'ordinary transcript after environment flip')
 send(b'\x05');until(lambda:'ctrl+e to collapse' in screen.text(),'ordinary show-all restored after fullscreen env flip')
 send(b'\x1b');until(lambda:not transcript() and help_general(),'actual transcript exit retains Help')
 send(b'\x1b');until(lambda:not help_general() and ready(),'actual ordinary canonical onDone completion')
 send(b'ROOT_HELP_HUMAN_3\r');until(lambda:'ROOT_HELP_DONE_3' in screen.text() and ready(),'same Harness ordinary canonical replay')
 send(b'\x14');until(lambda:expanded('tasks'),'actual committed saved view before next Session')
 send(b'\x04\x04');receipt_path=pathlib.Path(home,'root-help-receipt.json');until(receipt_path.exists,'normal exit + actual subscriptions/owned worker join')
 receipt=json.loads(receipt_path.read_text());assert receipt=={'code':0,'requests':3,'terminalRestored':True},receipt
 assert termios.tcgetattr(slave)==before,'physical slave termios not restored'
 pathlib.Path(home,'root-help-restoration-inspected').write_text('same live leader inspected')
 until(lambda:p.poll() is not None,'public child clean exit');assert p.returncode==0,p.returncode
 print(json.dumps({'terminalRestored':True,'actualModelRequests':3,'runtimeFullscreenFlips':2,'rootTranscriptWithPendingHelp':True,'exitCode':p.returncode}))
finally:
 pathlib.Path(home,'root-help-terminal.txt').write_bytes(captured)
 pathlib.Path(home,'root-help-final-screen.txt').write_text(screen.text())
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
