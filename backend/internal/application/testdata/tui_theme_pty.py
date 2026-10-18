"""Public RunCommand, actual stores, preview colors and same Harness requests."""
import fcntl,json,os,pathlib,re,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,120,0,0));captured=bytearray();deadline=time.monotonic()+35
before=termios.tcgetattr(slave)
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIThemePTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6500:])))
  pump()
def send(data):os.write(master,data);time.sleep(.18);pump()
def rgb(data,background,channels):
 target=[48 if background else 38,2,*channels]
 for match in re.findall(rb'\x1b\[([0-9;]*)m',data):
  codes=[int(value or b'0') for value in match.split(b';')]
  if any(codes[index:index+5]==target for index in range(len(codes)-4)):return True
 return False
try:
 wait(b'Claude Code','public CLI startup')
 paths=json.loads(pathlib.Path(home,'theme-paths.json').read_text());global_file=pathlib.Path(paths['global']);user_file=pathlib.Path(paths['user'])
 send(b'PRIVATE_THEME_DRAFT\x13');send(b'/theme\r');wait(b'Syntax theme: dark','actual Theme entrance')
 assert b'Auto (match terminal)' not in captured,'normal build admitted AUTO_THEME'
 start=len(captured);send(b'\x1b[B');wait(b'light (ctrl+t to disable)','actual preview focus',start)
 assert json.loads(global_file.read_text()).get('theme','dark')=='dark','focus wrote global theme'
 assert 'syntaxHighlightingDisabled' not in json.loads(user_file.read_text()),'focus wrote user setting'
 assert rgb(captured[start:],False,[0,0,0]) and rgb(captured[start:],True,[255,168,180]),'Source light fallback palette missing '+repr(bytes(captured[start:]))
 start=len(captured);send(b'\x14');wait(b'highlighting disabled (ctrl+t to enable)','actual syntax toggle',start)
 assert json.loads(user_file.read_text())['syntaxHighlightingDisabled'] is True,'Ctrl+T did not write actual user store'
 send(b'\x1b');wait(b'Theme picker dismissed','Source system cancel output')
 assert json.loads(global_file.read_text()).get('theme','dark')=='dark','cancel saved preview'
 assert json.loads(user_file.read_text())['syntaxHighlightingDisabled'] is True,'standalone cancel rolled back independent syntax setting'
 send(b'\x13\r');wait(b'THEME_PUBLIC_DONE_1','stashed draft same Harness POST')
 start=len(captured);send(b'/config\r');wait(b'Search settings','actual Config entrance',start);send(b'/theme\r');send(b'\x1b[C');wait(b'Syntax highlighting disabled','child Theme entrance',start)
 send(b'\x1b[B');send(b'\x14');send(b'\r')
 assert json.loads(global_file.read_text())['theme']=='light' and json.loads(user_file.read_text())['syntaxHighlightingDisabled'] is False,'child accept not persisted'
 send(b'\x1b')
 assert json.loads(global_file.read_text()).get('theme','dark')=='dark' and json.loads(user_file.read_text())['syntaxHighlightingDisabled'] is True,'Config Escape did not restore mount snapshot'
 start=len(captured);send(b'/config\r');wait(b'Search settings','next Config entrance',start);send(b'/theme\r');send(b'\x1b[C');send(b'\x1b[A');send(b'\x14');send(b'\r');send(b'\r')
 assert json.loads(global_file.read_text())['theme']=='light-ansi','Config commit missing chosen ANSI palette'
 start=len(captured);send(b'NEXT_THEME_QUERY\r');wait(b'THEME_PUBLIC_DONE_2','same Harness next human',start)
 assert b'\x1b[91m' in captured[start:],'next prompt did not use saved Source ANSI claude role'
 assert json.loads(global_file.read_text())['futureGlobal']==17 and json.loads(user_file.read_text())['futureUser']=={'preserved':True},'unknown state lost'
 send(b'\x04\x04');receipt_file=pathlib.Path(home,'theme-receipt.json')
 while not receipt_file.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('exit receipt missing '+repr(bytes(captured[-3000:])))
  pump()
 receipt=json.loads(receipt_file.read_text())
 if sys.platform=='darwin':fcntl.ioctl(slave,0x4004667f,struct.pack('I',0))
 assert termios.tcgetattr(slave)==before,'termios not restored';pathlib.Path(home,'theme-pty-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('CLI exit timeout')
  pump()
 assert p.returncode==0 and receipt['requests']==2 and receipt['terminalRestored'],(receipt,p.returncode,bytes(captured[-5000:]))
 print(json.dumps({'actualPreviewNoSave':True,'actualSyntaxUserStore':True,'actualConfigRollback':True,'actualNextPromptSavedPalette':True,'sameHarnessStashedDraftPOST':True,**receipt}))
finally:
 pathlib.Path(home,'theme-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
