"""Public MCP import command: real PTY selection, collision, cancel and restore."""
import fcntl,json,os,pathlib,select,signal,struct,subprocess,sys,termios,time
binary,root,work,bun,source_fixture=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',28,120,0,0))
# The driver owns this disposable controlling terminal so child exit does not
# detach it before we inspect the real restored termios state.
os.setsid();signal.signal(signal.SIGHUP,signal.SIG_IGN);fcntl.ioctl(slave,termios.TIOCSCTTY,0);before=termios.tcgetattr(slave)
# Source's own raw-mode input producer demonstrates the BSD kernel PENDIN
# bookkeeping flag on restoration. Compare all configured flags/characters and
# preserve this exact observed terminal state for the Go command comparison.
source_env=os.environ.copy();source_env.update({'CC_HAHA_CLI_MCP_SECRET_TTY_FIXTURE':'1','CC_HAHA_FIXTURE_TEMP_ROOT':root,'BUN_OPTIONS':'--no-env-file'})
source=subprocess.Popen([bun,'--no-env-file',source_fixture],cwd=os.environ['HOME'],stdin=slave,stdout=slave,stderr=slave,env=source_env,close_fds=True)
source_output=bytearray();deadline=time.monotonic()+10
while b'Enter OAuth client secret: ' not in source_output:
 if time.monotonic()>deadline or source.poll() is not None:raise AssertionError('Source raw prompt '+repr(source_output))
 if select.select([master],[],[],.05)[0]:source_output.extend(os.read(master,65536))
# Original readClientSecret terminates only when one data chunk is exactly CR/LF.
# This reference stage checks raw-mode/termios restoration, not secret content;
# send one CR so no text+newline chunk coalescing can change the Source contract.
# Real Go import selections/storage and separate Unicode/secret comparisons remain.
os.write(master,b'\r')
while source.poll() is None:
 if time.monotonic()>deadline:source.kill();raise AssertionError('Source secret input exit')
 if select.select([master],[],[],.05)[0]:source_output.extend(os.read(master,65536))
assert source.returncode==0
source_after=termios.tcgetattr(slave)
# PENDIN is kernel queued-input bookkeeping, not application raw mode.
source_config=list(source_after);source_config[3]&=~getattr(termios,'PENDIN',0)
before_config=list(before);before_config[3]&=~getattr(termios,'PENDIN',0)
assert source_config==before_config,('Source did not restore terminal configuration',before,source_after)
def run(cancel):
 captured=bytearray();deadline=time.monotonic()+18
 p=subprocess.Popen([binary,'mcp','add-from-claude-desktop','-s','user'],cwd=work,stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),close_fds=True)
 def pump():
  if select.select([master],[],[],.05)[0]:
   try:captured.extend(os.read(master,65536))
   except OSError:pass
 def wait(needle,label):
  while needle not in captured:
   if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-5000:])))
   pump()
 try:
  wait(b'Please select the servers you want to import','actual selector')
  wait(b'existing (already exists)','actual collision metadata')
  if cancel:os.write(master,b'\x1b')
  else:os.write(master,b' \r')
  while p.poll() is None:
   if time.monotonic()>deadline:raise AssertionError('command exit timeout '+repr(bytes(captured[-5000:])))
   pump()
  pump()
  after=termios.tcgetattr(slave);after_config=list(after);after_config[3]&=~getattr(termios,'PENDIN',0)
  # Source and native configured flags/characters must match exactly. BSD
  # PENDIN tracks unread input and must not be cleared by consuming user input.
  assert p.returncode==0 and after_config==source_config,(p.returncode,'terminal restoration',before,after,bytes(captured[-5000:]))
  expected=b'No servers were imported.' if cancel else b'Successfully imported 2 MCP servers to user config.'
  assert expected in captured,(expected,bytes(captured[-5000:]))
 finally:
  pathlib.Path(root,'mcp-import-cancel-terminal.txt' if cancel else 'mcp-import-terminal.txt').write_bytes(captured)
  if p.poll() is None:p.kill();p.wait()

run(False)
path=pathlib.Path(os.environ['CLAUDE_CONFIG_DIR'],'.claude.json');before=path.read_bytes();run(True);assert path.read_bytes()==before,'cancel mutated configuration'
os.close(master);os.close(slave)
print(json.dumps({'actualSelector':True,'collisionSuffix':True,'selectedTwo':True,'cancelNoMutation':True,'terminalRestored':True,'exitCode':0}))
