"""Actual public CLI: private stats, PNG clipboard, effort and next request."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,132,0,0))
before=termios.tcgetattr(slave);captured=bytearray();deadline=time.monotonic()+38
def setup():
 os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTerminalContextStatsPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-7000:])))
  pump()
def send(text):
 os.write(master,text);time.sleep(.12);pump()
def command(text,needle,label):
 start=len(captured);send(text.encode()+b'\r');wait(needle,label,start)
try:
 wait(b'Claude Code','startup')
 command('/stats',b'All time','actual stats overview')
 wait(b'Favorite model','actual physical transcript totals')
 start=len(captured);send(b'\t');wait(b'Models','actual stats tab',start)
 start=len(captured);send(b'\x13');wait(b'copied!','actual native PNG clipboard',start)
 png=pathlib.Path(home,'stats-screenshot.png').read_bytes();assert png.startswith(b'\x89PNG\r\n\x1a\n'),'PNG OS child receipt missing'
 assert struct.unpack('>II',png[16:24])[0]>96,'actual Source bitmap screenshot width missing'
 assert 'class PNGf' in pathlib.Path(home,'stats-screenshot-args.txt').read_text(),'Source PNG clipboard primitive changed'
 start=len(captured);send(b'r');wait(b'Last 7 days','real date range',start)
 start=len(captured);send(b'r');wait(b'Last 30 days','real date range',start)
 start=len(captured);send(b'r');wait(b'All time','all-time cached range',start)
 start=len(captured);send(b'\x04');wait(b'Stats dialog dismissed','stats Ctrl+D owns dialog',start)
 command('/context',b'Context Usage','real context counter/grid')
 wait('⛶'.encode(),'actual colored free-space grid')
 assert b'\x1b[90m' in captured,'trusted grid color missing'
 command('/effort low',b'Set effort level to low','physical saved effort')
 config=pathlib.Path(pathlib.Path(home,'stats-config-dir.txt').read_text());saved=json.loads(pathlib.Path(config,'settings.json').read_text());assert saved['effortLevel']=='low' and saved['future']['preserve']==37,saved
 command('PUBLIC_NEXT_EFFORT_LOW',b'PUBLIC_EFFORT_LOW_MODEL_COMPLETE','same Harness actual low request')
 command('/effort xhigh',b'this session only','session-only effort')
 saved=json.loads(pathlib.Path(config,'settings.json').read_text());assert saved['effortLevel']=='low','session-only effort wrongly saved'
 command('PUBLIC_NEXT_EFFORT_XHIGH',b'PUBLIC_EFFORT_XHIGH_MODEL_COMPLETE','same Harness actual xhigh request')
 command('/effort auto',b'Effort level set to auto','original unset producer')
 send(b'\x04\x04')
 receipt=pathlib.Path(home,'context-stats-receipt.json')
 while not receipt.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('normal exit receipt '+repr(bytes(captured[-7000:])))
  pump()
 value=json.loads(receipt.read_text());assert value['terminalRestored'] and value['ordinary']==2 and value['counting']>0,value
 assert termios.tcgetattr(slave)==before,'physical slave terminal restoration missing'
 pathlib.Path(home,'context-stats-pty-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('physical process exit pending')
  pump()
 assert p.returncode==0,p.returncode
 print(json.dumps({'terminalRestored':True,'actualPNGClipboard':True,'privateTranscriptStats':True,'originalRangeAndTab':True,'actualContextCounterAndColoredGrid':True,'persistedAndSessionEffortReachNextRequest':True,'ordinary':2,'exitCode':0}))
finally:
 pathlib.Path(home,'context-stats-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
