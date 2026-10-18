"""Real fullscreen Help modal, outer scrolling, onDone and terminal restoration."""
import fcntl,json,os,pathlib,re,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,100,0,0))
before=termios.tcgetattr(slave);screen=VTScreen(100,36);captured=bytearray();deadline=time.monotonic()+32
def setup():
 os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeFullscreenHelpPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:
   data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def until(predicate,label):
 while not predicate():
  if time.monotonic()>deadline or (p.poll() is not None and not predicate()):raise AssertionError(label+' SCREEN '+repr(screen.text())+' RAW '+repr(bytes(captured[-6000:])))
  pump()
def send(value):os.write(master,value)
def ready():return any(line.startswith('> ') or line=='>' for line in screen.text().splitlines())
def modal():return 'Claude understands your codebase' in screen.text() and '▔' in screen.text()
def custom():return 'Browse custom commands:' in screen.text() and '/owned-00' in screen.text()
def upper():
 lines=screen.text().splitlines();end=next((i for i,line in enumerate(lines) if '▔' in line),len(lines))
 return '\n'.join(lines[:end])
try:
 until(lambda:ready(),'startup actual prompt')
 assert b'\x1b[?1049h' in captured,'real alternate screen was not entered'
 send(b'FULLSCREEN_HELP_HUMAN_1\r');until(lambda:'FULLSCREEN_HELP_DONE_1' in screen.text() and ready(),'actual first model and prompt')
 send(b'/help\r');until(modal,'whole fullscreen general modal')
 lines=screen.text().splitlines();title=next(line for line in lines if 'Claude Code v' in line)
 assert title.startswith('   Claude Code v'),title
 send(b'\t');until(lambda:'Browse default commands:' in screen.text(),'actual default tab')
 send(b'\t');until(custom,'actual custom tab')
 first=re.search(r'^\s+/(\S+)',screen.text().split('Browse custom commands:',1)[1],re.M).group(1)
 send(b'\x1b[B');until(lambda:'❯ /'+first in screen.text(),'actual read-only focused first command')
 initial=upper();send(b'\x1b[5~');until(lambda:upper()!=initial,'PageUp must scroll actual outer transcript')
 assert custom() and '❯ /'+first in screen.text(),'PageUp changed Help list selection'
 send(b'\x1b[6~');until(lambda:upper()==initial,'PageDown must restore outer transcript')
 send(b'\x1b');until(lambda:'Browse custom commands:' not in screen.text() and ready(),'actual Help onDone unmount')
 assert 'You: /help' not in screen.text() and 'Help dialog dismissed' not in screen.text(),'fullscreen zero-record completion left command history'
 send(b'FULLSCREEN_HELP_HUMAN_2\r');until(lambda:'FULLSCREEN_HELP_DONE_2' in screen.text() and ready(),'same Harness after no-record dismissal')
 send(b'/help\r');until(modal,'actual second modal mount')
 send(b'\x03');until(lambda:'Press Ctrl-C again to exit' in screen.text(),'actual first Ctrl-C remains modal')
 send(b'\x03');until(lambda:not modal() and ready(),'second Ctrl-C completes only mounted Help')
 send(b'FULLSCREEN_HELP_HUMAN_3\r');until(lambda:'FULLSCREEN_HELP_DONE_3' in screen.text() and ready(),'next human after modal double press')
 send(b'\x04\x04');receipt_path=pathlib.Path(home,'fullscreen-help-receipt.json')
 until(receipt_path.exists,'owned normal exit and worker join')
 receipt=json.loads(receipt_path.read_text());assert receipt=={'code':0,'requests':3,'terminalRestored':True},receipt
 assert termios.tcgetattr(slave)==before,'physical slave termios not restored'
 pathlib.Path(home,'fullscreen-help-restoration-inspected').write_text('same live leader inspected')
 until(lambda:p.poll() is not None,'public child clean process exit')
 assert p.returncode==0,p.returncode
 print(json.dumps({'terminalRestored':True,'actualModelRequests':3,'modalAndOuterScroll':True,'exitCode':p.returncode}))
finally:
 pathlib.Path(home,'fullscreen-help-terminal.txt').write_bytes(captured)
 pathlib.Path(home,'fullscreen-help-final-screen.txt').write_text(screen.text())
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
