"""Actual public parent CLI, private daemon child, and current-screen PR consumers."""
import fcntl,json,os,pathlib,re,select,signal,struct,subprocess,sys,termios,time,unicodedata
binary,home=sys.argv[1:];root=pathlib.Path(home);width,height=100,42
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',height,width,0,0));before=termios.tcgetattr(slave);captured=bytearray();deadline=time.monotonic()+50
class Screen:
 def __init__(self):self.lines=[[' ']*width for _ in range(height)];self.row=0;self.col=0;self.pending='';self.saved=(0,0)
 def advance(self):
  self.row+=1
  if self.row>=height:self.lines.pop(0);self.lines.append([' ']*width);self.row=height-1
 def feed(self,text):
  text=self.pending+text;self.pending='';i=0
  while i<len(text):
   c=text[i]
   if c=='\x1b':
    if i+1>=len(text):self.pending=text[i:];break
    if text[i+1]==']':
     match=re.search('\x07|\x1b\\\\',text[i+2:])
     if not match:self.pending=text[i:];break
     i+=2+match.end();continue
    if text[i+1]=='[':
     match=re.match(r'\x1b\[([0-?]*)([ -/]*)([@-~])',text[i:])
     if not match:self.pending=text[i:];break
     raw,_,op=match.groups();values=[int(v) if v else 0 for v in raw.split(';')] if re.fullmatch(r'[0-9;]*',raw) else [];n=(values[0] or 1) if values else 1
     if op=='A':self.row=max(0,self.row-n)
     elif op=='B':self.row=min(height-1,self.row+n)
     elif op=='C':self.col=min(width-1,self.col+n)
     elif op=='D':self.col=max(0,self.col-n)
     elif op in ('H','f'):self.row=min(height-1,max(0,n-1));self.col=min(width-1,max(0,(values[1] or 1)-1 if len(values)>1 else 0))
     elif op=='G':self.col=min(width-1,max(0,n-1))
     elif op=='E':self.row=min(height-1,self.row+n);self.col=0
     elif op=='F':self.row=max(0,self.row-n);self.col=0
     elif op=='K':
      mode=values[0] if values else 0;start=0 if mode in (1,2) else self.col;end=self.col+1 if mode==1 else width;self.lines[self.row][start:end]=[' ']*(end-start)
     elif op=='J':
      mode=values[0] if values else 0
      if mode in (2,3):self.lines=[[' ']*width for _ in range(height)]
      elif mode==0:self.lines[self.row][self.col:]=[' ']*(width-self.col);self.lines[self.row+1:]=[[' ']*width for _ in range(height-self.row-1)]
     elif op=='s':self.saved=(self.row,self.col)
     elif op=='u':self.row,self.col=self.saved
     i+=match.end();continue
    if text[i+1]=='7':self.saved=(self.row,self.col)
    elif text[i+1]=='8':self.row,self.col=self.saved
    i+=2;continue
   if c=='\r':self.col=0
   elif c=='\n':self.advance()
   elif c=='\b':self.col=max(0,self.col-1)
   elif c=='\t':self.col=min(width-1,(self.col//8+1)*8)
   elif ord(c)>=32:
    if self.col>=width:self.col=0;self.advance()
    cells=2 if unicodedata.east_asian_width(c) in ('W','F') else 1
    if not unicodedata.combining(c):self.lines[self.row][self.col]=c;self.col+=cells
   i+=1
 def footer(self):
  lines=[''.join(line).rstrip() for line in self.lines];last=max((i for i,line in enumerate(lines) if line.strip()),default=0);return '\n'.join(lines[:last+1])
screen=Screen()
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLITeamsDialogPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:
   data=os.read(master,65536);captured.extend(data);root.joinpath('teams-dialog-running-terminal.txt').write_bytes(captured);screen.feed(data.decode('utf8','replace'))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in re.sub(rb'\x1b\][^\x1b\x07]*(?:\x1b\\|\x07)|\x1b\[[0-?]*[ -/]*[@-~]',b'',bytes(captured[start:])):
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-8500:])))
  pump()
def state(predicate,label):
 while not predicate(screen.footer()):
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' SCREEN '+repr(screen.footer())+' RAW '+repr(bytes(captured[-4500:])))
  pump()
def pane_diagnostics():
 env=os.environ.copy();env['TMUX_TMPDIR']=paths['daemon'];env['HOME']=str(pathlib.Path(paths['team']).parents[3]);args=['tmux','-L',paths['label']]
 panes=subprocess.run(args+['list-panes','-a','-F','#{pane_id}'],env=env,capture_output=True,text=True,timeout=2)
 capture=panes.stdout+panes.stderr
 for pane in panes.stdout.splitlines():capture+=subprocess.run(args+['capture-pane','-p','-S','-100','-t',pane],env=env,capture_output=True,text=True,timeout=2).stdout
 root.joinpath('teams-dialog-panes.txt').write_text(capture)
 return capture
def file(path,label):
 diagnostic=time.monotonic()+2;inspected=False
 while not path.exists():
  if 'child writes' in label and not inspected and time.monotonic()>diagnostic:
   inspected=True;pane_diagnostics()
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6500:]))+' PANES '+(root.joinpath('teams-dialog-panes.txt').read_text() if root.joinpath('teams-dialog-panes.txt').exists() else ''))
  pump()
def send(data):os.write(master,data);time.sleep(.18);pump()
def members():return json.loads(pathlib.Path(paths['team']).read_text())['members']
def roster(predicate,label):
 while not predicate(members()):
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(members())+' SCREEN '+repr(screen.footer()))
  pump()
def mail(name,kind):
 path=pathlib.Path(paths['team']).parent/'inboxes'/(name+'.json')
 rows=[]
 try:rows+=json.loads(path.read_text())
 except (FileNotFoundError,json.JSONDecodeError):pass
 history=path.with_name(name+'.history.jsonl')
 try:rows+=[json.loads(line) for line in history.read_text().splitlines() if line]
 except (FileNotFoundError,json.JSONDecodeError):pass
 result=[]
 for row in rows:
  try:
   if json.loads(row.get('text','{}')).get('type')==kind:result.append(row)
  except json.JSONDecodeError:pass
 return result
def actual_model_notifications():
 expected={kind+':'+name:1 for kind in ('idle','terminated') for name in ('one','two','three')};expected['approved:one']=1
 receipt=root/'teams-dialog-inbox-model-receipt.json'
 while True:
  try:
   value=json.loads(receipt.read_text())
   if value==expected:return
   assert all(key in expected and count==1 for key,count in value.items()),('unknown/duplicate actual model notification',value)
  except (FileNotFoundError,json.JSONDecodeError):pass
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('actual seven notification queries not consumed '+(receipt.read_text() if receipt.exists() else 'no HTTP receipt')+' SCREEN '+repr(screen.footer()))
  pump()
try:
 wait(b'Claude','actual public initial prompt');file(root/'teams-dialog-paths.json','actual private setup');paths=json.loads(root.joinpath('teams-dialog-paths.json').read_text())
 send(b'Spawn three actual private Teams pane workers.\r');wait(b'TEAMS_DIALOG_SPAWN_DONE_928','real TeamCreate and three Agent panes')
 for name in ('one','two','three'):
  path=pathlib.Path(paths['child']+'-'+name);file(path,'actual daemon child writes '+name);assert path.read_text()=='PUBLIC_TEAMS_CHILD_WRITE_'+name
 roster(lambda rows:len(rows)==4 and all(row.get('backendType')=='tmux' and row.get('tmuxPaneId') for row in rows if row['name']!='team-lead'),'three actual pane memberships')
 root.joinpath('teams-dialog-actual-pane.txt').write_text(next(row['tmuxPaneId'] for row in members() if row['name']=='one'))
 roster(lambda rows:all(not row.get('isActive',False) for row in rows if row['name']!='team-lead'),'actual worker idle publications')
 send(b'\x1b[B\x1b[B\r');state(lambda text:'Team public-teams-dialog' in text and '3 teammates' in text,'Footer teams entry actual dialog')
 initial={row['name']:row.get('mode') for row in members() if row['name']!='team-lead'}
 send(b'\x1b[Z');roster(lambda rows:all(row.get('mode')!=initial[row['name']] for row in rows if row['name']!='team-lead'),'batch atomic mode cycle')
 for name in initial:
  while not mail(name,'mode_set_request'):
   if time.monotonic()>deadline:raise AssertionError('actual mode mailbox '+name)
   pump()
 send(b'\r');state(lambda text:'@one' in text and 'Prompt' in text and '← back' in text,'actual member detail')
 send(b'p');state(lambda text:'TEAMS_WORKER_ONE_928' in text,'real prompt expansion')
 send(b's');roster(lambda rows:all(row['name']!='one' for row in rows),'actual detail shutdown mailbox/child approval/roster removal')
 assert mail('one','shutdown_request'),'real shutdown request not persisted'
 send(b'\x1b[D');state(lambda text:'2 teammates' in text and 'Enter view' in text,'detail back to live list')
 send(b'k');roster(lambda rows:all(row['name']!='two' for row in rows),'actual native pane kill and membership removal')
 state(lambda text:'1 teammate' in text,'live remaining idle pane')
 send(b'p');roster(lambda rows:all(row['name']=='team-lead' for row in rows),'actual idle prune pane cleanup')
 state(lambda text:'No teammates' in text,'actual empty mounted list')
 actual_model_notifications()
 send(b'\x1b');state(lambda text:'Team public-teams-dialog' not in text,'real dialog unmount')
 send(b'WRITE_PARENT_AFTER_PANE_FOOTER_742\r');wait(b'PANE_FOOTER_NEXT_WRITE_DONE_742','same Harness next actual parent Write');assert pathlib.Path(paths['parent']).read_text()=='PUBLIC_PANE_FOOTER_NEXT_PARENT_WRITE_742'
 send(b'\x04\x04');receiptfile=root.joinpath('teams-dialog-receipt.json');file(receiptfile,'normal exit after real children');receipt=json.loads(receiptfile.read_text());assert receipt['terminalRestored'] and termios.tcgetattr(slave)==before,'Go and physical slave termios';assert receipt['actualChildWrite'] and receipt['sameHarnessParentWrite'] and receipt['unknownFieldsPreserved'] and receipt['privatePanesJoined'],receipt;root.joinpath('teams-dialog-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('normal exit timeout')
  pump()
 assert p.returncode==0,(receipt,repr(bytes(captured[-8500:])));print(json.dumps({'actualTeamsDialogActions':True,'batchModesAndMailboxes':True,'shutdownKillPrune':True,**receipt}))
finally:
 root.joinpath('teams-dialog-terminal.txt').write_bytes(captured)
 if p.poll() is None:
  p.send_signal(signal.SIGTERM)
  try:p.wait(timeout=8)
  except subprocess.TimeoutExpired:p.kill();p.wait()
 os.close(master);os.close(slave)
