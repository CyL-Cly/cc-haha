"""The real raw-secret callback: UTF16 backspace, no echo and Ctrl-C cancel."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,bun,fixture,home=sys.argv[1:]
for typed,cancel in [("private-你",False),("private-😀",False),("",True)]:
 for source in [True,False]:
  master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',24,100,0,0));capture=bytearray();deadline=time.monotonic()+12
  def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
  args=[bun,'--no-env-file',fixture] if source else [binary,'-test.run=^TestMCPClientSecretTTYFixtureProcess$','-test.count=1']
  p=subprocess.Popen(args,cwd=home,stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
  try:
   while b'Enter OAuth client secret: ' not in capture:
    if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('prompt '+repr(capture))
    if select.select([master],[],[],.05)[0]:capture.extend(os.read(master,65536))
   # Source prints the prompt before setRawMode. Wait for actual raw readiness
   # on both transports so Ctrl-C reaches the callback rather than POSIX SIGINT.
   while termios.tcgetattr(slave)[3] & (termios.ICANON | termios.ISIG):
    if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('raw terminal readiness')
    time.sleep(.001)
   if cancel:os.write(master,b'\x03')
   else:
    os.write(master,typed.encode());time.sleep(.1);os.write(master,b'\x7f');time.sleep(.1);os.write(master,b'\r')
   while p.poll() is None:
    if time.monotonic()>deadline:raise AssertionError('raw secret exit timeout')
    if select.select([master],[],[],.05)[0]:
     try:capture.extend(os.read(master,65536))
     except OSError:pass
   assert p.returncode==0,(p.returncode,capture)
   receipt=json.loads(pathlib.Path(home,'source-secret-receipt.json' if source else 'native-secret-receipt.json').read_text())
   if not source:assert receipt==json.loads(pathlib.Path(home,'source-secret-receipt.json').read_text()),('native vs actual Source',receipt)
   if cancel:assert receipt['failure']=='Cancelled' and receipt['secret']=='',receipt
   else:assert receipt['secret']==('private-\ud83d' if typed.endswith('😀') else 'private-') and receipt['failure']=='',receipt
   assert typed.encode() not in capture if typed else True,'secret was echoed'
  finally:
   pathlib.Path(home,('source-' if source else 'native-')+('secret-cancel-terminal.txt' if cancel else 'secret-unicode-terminal.txt')).write_bytes(capture)
   if p.poll() is None:p.kill();p.wait()
   os.close(master);os.close(slave)
print(json.dumps({'unicodeBackspace':True,'noEcho':True,'ctrlCCancel':True}))
