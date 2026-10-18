"""Real public CLI: Config editor mode, Vim edits, raw UTF16 and same Harness."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',32,120,0,0));captured=bytearray();deadline=time.monotonic()+35
before=termios.tcgetattr(slave)
standalone=os.environ.get('CC_HAHA_VIM_PTY_STANDALONE')=='1'
queued=os.environ.get('CC_HAHA_VIM_PTY_QUEUED')=='1'
shift=1 if queued else 0
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIVimPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6500:])))
  pump()
def send(data):os.write(master,data);time.sleep(.18);pump()
try:
 wait(b'Claude Code','actual CLI startup')
 paths=json.loads(pathlib.Path(home,'vim-paths.json').read_text());global_file=pathlib.Path(paths['global'])
 if standalone:
  if queued:
   send(b'INITIAL_HELD_MODEL\r')
   while not pathlib.Path(home,'vim-model-held').exists():
    if time.monotonic()>deadline:raise AssertionError('actual first model not held')
    pump()
   send(b'/vim\r');wait(b'1 queued','actual busy Vim input queue')
   assert json.loads(global_file.read_text()).get('editorMode','normal')=='normal','busy command mutated mode before queue execution'
   pathlib.Path(home,'vim-release-model').write_text('release actual HTTP response')
  else:send(b'/vim\r')
  wait(b'Editor mode set to vim.','actual local vim entrance');wait(b'INSERT','actual command mode published')
 else:
  send(b'/config\r');wait(b'Search settings','actual Config entrance')
  send(b'/editor\r');wait(b'Editor mode','actual setting search')
  send(b'\x1b[C');assert json.loads(global_file.read_text())['editorMode']=='vim','actual global editor setting not saved'
  start=len(captured);send(b'\r');wait(b'INSERT','actual Vim mode published',start)
 send(b'\x1b[200~alpha beta gamma delta\x1b[201~')
 start=len(captured);send(b'\x1b');wait(b'NORMAL','actual INSERT escape',start)
 send(b'0dwrBxu0ywp');send(b'\r');wait(('VIM_PUBLIC_DONE_'+str(1+shift)).encode(),'actual operator/yank/paste/undo request')
 send(b'i');send('😀'.encode());send(b'\x1b');send(b'x');send(b'\r');wait(('VIM_PUBLIC_DONE_'+str(2+shift)).encode(),'actual lone surrogate canonical request')
 if standalone:
  start=len(captured);send(b'i/vim\r');wait(b'Editor mode set to normal.','actual local normal entrance',start)
 else:
  start=len(captured);send(b'i/config\r');wait(b'Search settings','actual next Config entrance',start)
  send(b'/editor\r');send(b'\x1b[D');assert json.loads(global_file.read_text()).get('editorMode','normal')=='normal',('actual global',global_file.read_text(),repr(bytes(captured[-6000:])))
  send(b'\r')
 send(b'normal mode restores plain input\r');wait(('VIM_PUBLIC_DONE_'+str(3+shift)).encode(),'actual normal-mode next request')
 assert json.loads(global_file.read_text())['futureGlobal']=={'keep':True},'unknown global field lost'
 send(b'\x04\x04');receipt_file=pathlib.Path(home,'vim-receipt.json')
 while not receipt_file.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('exit receipt missing '+repr(bytes(captured[-3000:])))
  pump()
 receipt=json.loads(receipt_file.read_text())
 if sys.platform=='darwin':fcntl.ioctl(slave,0x4004667f,struct.pack('I',0))
 assert termios.tcgetattr(slave)==before,'actual slave termios not restored';pathlib.Path(home,'vim-pty-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('actual CLI exit timeout')
  pump()
 assert p.returncode==0 and receipt['requests']==3+shift and receipt['terminalRestored'],(receipt,p.returncode,bytes(captured[-5000:]))
 request=json.loads(pathlib.Path(home,'vim-request-'+str(2+shift)+'.json').read_text());content=request['messages'][-1]['content'];text=content if isinstance(content,str) else next(block['text'] for block in content if block['type']=='text')
 assert [ord(ch) for ch in text]==[0xd83d],repr(text)
 print(json.dumps({'actualConfigModeConsumer':not standalone,'actualLocalVimCommandConsumer':standalone,'actualQueuedLocalCommandConsumer':queued,'actualSourceOperatorsYankPasteUndo':True,'sameHarnessRawSurrogatePOST':True,'actualNormalModeRestored':True,**receipt}))
finally:
 pathlib.Path(home,'vim-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
