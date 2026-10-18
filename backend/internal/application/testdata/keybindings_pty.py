"""Public RunCommand trusted keybinding load/chord/hot reload/next-turn receipt."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty()
fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',34,132,0,0))
terminal_before=termios.tcgetattr(slave)
captured=bytearray(); deadline=time.monotonic()+33
def setup():
 os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLITrustedKeybindingPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6000:])))
  pump()
def send(value):
 os.write(master,value);time.sleep(.16);pump()
try:
 wait(b'Claude Code','startup')
 start=len(captured);send(b'\x0b\x10');wait(b'Claude understands your codebase','custom chord actual Help',start)
 send(b'\x1b');time.sleep(.18);pump()
 file=pathlib.Path(pathlib.Path(home,'keybinding-path.txt').read_text())
 invalid={'future':{'untouched':18},'bindings':None}
 temp=file.with_suffix('.replacement');temp.write_text(json.dumps(invalid));temp.replace(file)
 wait(b'Found 1 keybinding error','actual reload validation warning',len(captured))
 updated={'future':{'untouched':19},'bindings':[{'context':'Chat','bindings':{'alt+p':None,'ctrl+k ctrl+p':'command:help','alt+j':'command:skills'}}]}
 update_start=len(captured)
 temp=file.with_suffix('.replacement');temp.write_text(json.dumps(updated));temp.replace(file)
 # The trusted gate is also read by command availability. Its absolute call
 # count cannot prove that this file replacement reached the UI. keysChanged
 # installs the real bindings before clearing the previous validation warning;
 # observe that actual clear redraw before sending the newly added shortcut.
 while True:
  frame=bytes(captured[update_start:]).rsplit(b'\x1b[J',1)[-1]
  if b'Claude Code' in frame and b'bypass permissions on' in frame and b'Found 1 keybinding error' not in frame:break
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('actual fixed-config validation warning never cleared')
  pump()
 start=len(captured);send(b'\x1bj');wait(b'Available skills from the current project','real hot-reloaded shortcut',start)
 send(b'\x1b');time.sleep(.18);pump()
 send(b'KEYBINDING_NEXT_HUMAN\r');wait(b'KEYBINDING_PUBLIC_NEXT_COMPLETE','same Harness next human')
 send(b'\x04\x04')
 receipt_file=pathlib.Path(home,'keybinding-receipt.json')
 while not receipt_file.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('normal exit did not restore terminal')
  pump()
 receipt=json.loads(receipt_file.read_text())
 assert termios.tcgetattr(slave)==terminal_before,'actual slave terminal flags were not restored'
 assert json.loads(file.read_text())==updated,'loader rewrote user metadata'
 pathlib.Path(home,'keybinding-pty-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('normal exit did not restore terminal')
  pump()
 assert p.returncode==0 and receipt['terminalRestored'] and receipt['requests']==1 and receipt['gateCalls']>=4,(receipt,p.returncode)
 print(json.dumps({'terminalRestored':True,'trustedGate':True,'actualChordHelp':True,'actualValidationWarning':True,'actualFixedConfigHotReloadSkills':True,'localShortcutsZeroModelRequests':True,'nextHumanRequests':1,'unknownFieldsPreserved':True,'exitCode':p.returncode}))
finally:
 pathlib.Path(home,'keybindings-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
