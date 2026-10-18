"""Actual RunCommand conversation local/OS/file/aux-model and next-human receipt."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home,profile=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,132,0,0))
terminal_before=termios.tcgetattr(slave);captured=bytearray();deadline=time.monotonic()+38
def setup():
 os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTerminalConversationPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-7000:])))
  pump()
def send(value):
 os.write(master,value);time.sleep(.14);pump()
def command(value,needle,label):
 start=len(captured);send(value.encode()+b'\r');wait(needle,label,start)
try:
 wait(b'Claude Code','startup')
 command('/rename pty-name',b'Session renamed to: pty-name','explicit real title')
 command('/color pink',b'color set to: pink','real prompt color')
 wait(b'\x1b[38;5;205mpty-name>','actual pink prompt accent')
 command('/copy',b'No assistant message to copy','cold actual history')
 start=len(captured);send(b'/ta\t');wait(b'tasks','normal external completion gate',start)
 assert b'Toggle a searchable tag' not in captured[start:],'internal tag leaked into normal public completion'
 send(b'\x15\x1b')
 command('/export public-cold.txt',b'Conversation exported to:','cold actual export header without inference')
 work=pathlib.Path(pathlib.Path(home,'conversation-workdir.txt').read_text())
 cold=pathlib.Path(work,'public-cold.txt').read_text()
 assert 'Claude Code v' in cold,'actual cold export identity missing'
 if profile=='cached-marketing':
  assert 'Claude Max' in cold and '3 guest passes' in cold and '[✻] [✻] [✻]' in cold and '/passes' in cold and 'Message from PRIVATE_COMPANY:' in cold and 'PRIVATE_COMPANY_ANNOUNCEMENT' in cold,'actual cached marketing business consumer missing'
 else:
  assert 'Sonnet 4.5' in cold and 'API Usage Billing' in cold,'actual cold export API billing missing'
 if profile=='sandbox':
  assert 'Your bash commands will be sandboxed. Disable with /sandbox.' in cold,'actual sandbox dependency/settings business consumer missing'
 command('PTY_FIRST_HUMAN actual Write',b'PUBLIC_FIRST_RESPONSE','same Harness actual tool and response')
 command('/copy',b'Select content to copy:','real code selection dialog')
 start=len(captured);send(b'\x1b[B\r');wait(b'Copied to clipboard','real selected-code clipboard',start)
 clipboard=pathlib.Path(home,'clipboard-stdin.txt')
 assert clipboard.read_text()=='fmt.Println("PUBLIC_CODE")','actual clipboard OS child stdin differs'
 wait(b'\x1b]52;c;','actual OSC clipboard terminal output')
 work=pathlib.Path(pathlib.Path(home,'conversation-workdir.txt').read_text())
 assert pathlib.Path(work,'public-actual-tool.txt').read_text()=='PUBLIC_COMMAND_TOOL','actual tool consumer absent'
 command('/export',b'Select export method:','real export chooser')
 start=len(captured);send(b'\x1b[B\r');wait(b'Enter filename:','real export filename form',start)
 start=len(captured);send(b'\x15public-form.json\r');wait(b'Conversation exported to:','actual form file write',start)
 content=pathlib.Path(work,'public-form.txt').read_text();assert 'PTY_FIRST_HUMAN' in content and 'PUBLIC_FIRST_RESPONSE' in content and 'File created successfully at:' in content,'export did not consume live history/tool result'
 if profile=='sandbox':assert 'Your bash commands will be sandboxed. Disable with /sandbox.' in content,'live export lost actual sandbox availability'
 command('/rename',b'auto-pty-topic','actual shared small-model name')
 command('/color reset',b'color reset to default','persisted reset sentinel')
 command('PTY_NEXT_HUMAN',b'PUBLIC_NEXT_HUMAN_COMPLETE','same Harness next human')
 send(b'\x04\x04')
 receipt_file=pathlib.Path(home,'conversation-receipt.json')
 while not receipt_file.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('normal exit receipt missing '+repr(bytes(captured[-5000:])))
  pump()
 receipt=json.loads(receipt_file.read_text());assert receipt['terminalRestored'] and receipt['ordinary']==3 and receipt['names']==1,receipt
 assert termios.tcgetattr(slave)==terminal_before,'actual slave termios was not restored'
 pathlib.Path(home,'conversation-pty-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('physical exit remained pending')
  pump()
 assert p.returncode==0,p.returncode
 print(json.dumps({'terminalRestored':True,'actualClipboardChildAndOSC':True,'actualToolAndFileExport':True,'explicitAndActualAuxRename':True,'persistedColorReset':True,'sameHarnessNextHuman':True,'ordinaryRequests':3,'nameRequests':1,'exitCode':p.returncode}))
finally:
 pathlib.Path(home,'conversation-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
