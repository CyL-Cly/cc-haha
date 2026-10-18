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
  lines=[''.join(line).rstrip() for line in self.lines];last=max((i for i,line in enumerate(lines) if line.strip()),default=0);return '\n'.join(lines[max(0,last-10):last+1])
screen=Screen()
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeCLIParentPaneTeamFooterPTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:
   data=os.read(master,65536);captured.extend(data);screen.feed(data.decode('utf8','replace'))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in re.sub(rb'\x1b\][^\x1b\x07]*(?:\x1b\\|\x07)|\x1b\[[0-?]*[ -/]*[@-~]',b'',bytes(captured[start:])):
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-8500:])))
  pump()
def state(predicate,label):
 while not predicate(screen.footer()):
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' SCREEN '+repr(screen.footer())+' RAW '+repr(bytes(captured[-4500:])))
  pump()
def file(path,label):
 while not path.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6500:])))
  pump()
def send(data):os.write(master,data);time.sleep(.18);pump()
try:
 wait(b'PR #742','actual initial PR footer');paths=json.loads(root.joinpath('pane-footer-paths.json').read_text());state(lambda text:'PR #742' in text,'initial current prompt PR visible')
 send(b'Spawn the actual private pane worker for the footer test.\r');wait(b'PANE_FOOTER_SPAWN_DONE_742','real TeamCreate and Agent pane');file(pathlib.Path(paths['child']),'actual daemon child writes physical artifact');assert pathlib.Path(paths['child']).read_text()=='PUBLIC_PANE_FOOTER_CHILD_WRITE_742';team=json.loads(pathlib.Path(paths['team']).read_text());worker=next(member for member in team['members'] if member['name']=='worker');assert worker['backendType']=='tmux' and worker['tmuxPaneId'];root.joinpath('pane-footer-actual-pane.txt').write_text(worker['tmuxPaneId']);assert pathlib.Path(paths['daemon'],'tmux-'+str(os.getuid()),paths['label']).is_socket(),'actual Source socket label';state(lambda text:'PR #742' not in text,'actual background+pane-team primary count hides PR');root.joinpath('pane-footer-active-screen.txt').write_text(screen.footer())
 send(b'SHUTDOWN_PRIVATE_PANE_FOOTER_742\r');wait(b'PANE_FOOTER_SHUTDOWN_REQUEST_DONE_742','real parent shutdown request');state(lambda text:'PR #742' in text,'actual accepted shutdown removes pane roster and restores PR');root.joinpath('pane-footer-removed-screen.txt').write_text(screen.footer());team=json.loads(pathlib.Path(paths['team']).read_text());assert all(member['name']=='team-lead' for member in team['members']),'real accepted shutdown persisted roster removal'
 send(b'WRITE_PARENT_AFTER_PANE_FOOTER_742\r');wait(b'PANE_FOOTER_NEXT_WRITE_DONE_742','same Harness next actual parent Write');assert pathlib.Path(paths['parent']).read_text()=='PUBLIC_PANE_FOOTER_NEXT_PARENT_WRITE_742';state(lambda text:'PR #742' in text,'next actual prompt retained roster footer consumer')
 send(b'\x04\x04');receiptfile=root.joinpath('pane-footer-receipt.json');file(receiptfile,'normal exit after real children');receipt=json.loads(receiptfile.read_text());assert receipt['terminalRestored'] and termios.tcgetattr(slave)==before,'Go and physical slave termios';assert receipt['actualChildWrite'] and receipt['sameHarnessParentWrite'] and receipt['unknownFieldsPreserved'] and receipt['privatePanesJoined'],receipt;root.joinpath('pane-footer-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('normal exit timeout')
  pump()
 assert p.returncode==0,repr(bytes(captured[-8500:]));print(json.dumps({'actualPaneRosterHidesPR':True,'acceptedShutdownRestoresPR':True,**receipt}))
finally:
 root.joinpath('pane-footer-terminal.txt').write_bytes(captured)
 if p.poll() is None:
  p.send_signal(signal.SIGTERM)
  try:p.wait(timeout=8)
  except subprocess.TimeoutExpired:p.kill();p.wait()
 os.close(master);os.close(slave)
