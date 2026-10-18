"""Actual public Go pre-CLI fast path, private Git and isolated tmux socket."""
import fcntl,json,os,pathlib,pty,select,shlex,struct,subprocess,sys,termios,time
binary,root,tmux,mode=sys.argv[1:]
root=pathlib.Path(root).resolve();assert root.is_absolute() and (root/'owned-tmux-pty').read_text()=='private'
home,config,temp,work,bindir=[root/n for n in ('home','config','temp','repo.name','bin')]
for p in (home,config,temp,work,bindir):p.mkdir()
socket=root/'tmux.sock'
assert len(str(socket).encode())<100
trace=root/'tmux.argv.log'
wrapper=bindir/'tmux'
wrapper.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> '+shlex.quote(str(trace))+'\nexec '+shlex.quote(tmux)+' -S '+shlex.quote(str(socket))+' -f /dev/null "$@"\n');wrapper.chmod(0o700)
env={'HOME':str(home),'USERPROFILE':str(home),'CLAUDE_CONFIG_DIR':str(config),'TMPDIR':str(temp),'CC_HAHA_NATIVE_FIXTURE_ROOT':str(root),'PATH':str(bindir)+':/usr/bin:/bin','TERM':'xterm-256color','SHELL':'/bin/sh','DISABLE_TELEMETRY':'1','GIT_CONFIG_NOSYSTEM':'1','GIT_CONFIG_GLOBAL':str(home/'absent.gitconfig'),'GIT_AUTHOR_NAME':'fixture','GIT_AUTHOR_EMAIL':'fixture@example.invalid','GIT_COMMITTER_NAME':'fixture','GIT_COMMITTER_EMAIL':'fixture@example.invalid','FORCE_COLOR':'0'}
(config/'settings.json').write_text('{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false}')
(work/'tracked.txt').write_text('PRIVATE_REAL_GIT_BYTES')
def command(args,cwd=work,check=True):return subprocess.run(args,cwd=cwd,env=env,capture_output=True,check=check,timeout=15)
for args in (['init','-b','main'],['add','.'],['commit','-m','private fixture']):command(['/usr/bin/git']+args)
def lower(args,check=True):return command([str(wrapper)]+args,check=check)
if mode in ('async','async-rewake','async-header'):
 tree=work/'.claude'/'worktrees'/'physical';tree.mkdir(parents=True)
 (tree/'tracked.txt').write_text('PRIVATE_REAL_GIT_BYTES')
 script=root/'actual-background.sh';pidpath=root/'background.pid'
 header='printf \"%s\\n\" '+shlex.quote(json.dumps({'async':True,'asyncTimeout':3000},separators=(',',':')))+'\n' if mode=='async-header' else ''
 script.write_text('printf \"%s\" \"$$\" > '+shlex.quote(str(pidpath))+'\n'+header+'exec /bin/sleep 30\n')
 async_hook={'type':'command','command':'exec /bin/sh '+shlex.quote(str(script)),'timeout':10}
 if mode=='async':async_hook['async']=True
 if mode=='async-rewake':async_hook['asyncRewake']=True
 success={'type':'command','command':'while [ ! -f '+shlex.quote(str(pidpath))+' ]; do /bin/sleep 0.01; done; printf "%s\\n" '+shlex.quote(str(tree))}
 # Source first successful nonempty output: put the real VCS path first; the
 # second background job still starts concurrently and must be owned at exit.
 (config/'settings.json').write_text(json.dumps({'hooks':{'WorktreeCreate':[{'hooks':[success,async_hook]}]}}))
 env['CLAUDE_CODE_TMPDIR']=str(temp)
master=slave=process=None;captured=bytearray()
try:
 if mode=='existing' or mode=='nested-existing':
  lower(['new-session','-d','-s','repo_name_worktree-physical','-c',str(work),'--','/bin/sh','-c',"printf 'EXISTING_PHYSICAL_TMUX_MARKER'; sleep 30"])
  lower(['set-option','-g','prefix','C-a'])
 elif mode=='control':env['TERM_PROGRAM']='iTerm.app'
 if mode.startswith('nested'):
  lower(['new-session','-d','-s','private-anchor','-c',str(work),'--','/bin/sh'])
  lower(['set-option','-g','prefix','C-a'])
  lower(['set-option','-g','remain-on-exit','on'])
 master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',28,120,0,0))
 before=termios.tcgetattr(slave)
 def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
 flag='--tmux' if mode=='control' else '--tmux=classic'
 guardian=pathlib.Path(__file__).resolve().parents[2]/'application'/'testdata'/'tui_exit_public_pty.py'
 launch=[binary,'cli','--app-root',str(root),'--worktree=physical',flag,'--version']
 if mode.startswith('nested'):launch=[str(wrapper),'attach-session','-t','private-anchor']
 process=subprocess.Popen(['/usr/bin/python3',str(guardian),'_guardian',str(root)]+launch,cwd=work,env=env,stdin=slave,stdout=slave,stderr=slave,preexec_fn=setup,close_fds=True)
 if mode.startswith('nested'):
  ready=time.monotonic()+5
  while True:
   clients=lower(['list-clients','-F','#{session_name}'],False)
   if clients.returncode==0 and 'private-anchor' in clients.stdout.decode().splitlines():break
   if time.monotonic()>ready:raise AssertionError('owned PTY client never attached')
   time.sleep(.01)
  lower(['send-keys','-t','private-anchor',' '.join(shlex.quote(v) for v in [binary,'cli','--app-root',str(root),'--worktree=physical','--tmux','--version'])+"; printf 'OWNED_NESTED_OUTER_DONE=%s' \"$?\"",'Enter'])

 deadline=time.monotonic()+20;detached=False;observed_prefix=None
 while process.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('actual public tmux PTY deadline '+repr(captured[-2500:]))
  if select.select([master],[],[],.05)[0]:
   try:captured.extend(os.read(master,65536))
   except OSError:pass
  if (mode in ('existing','nested-existing') and b'EXISTING_PHYSICAL_TMUX_MARKER' in captured or mode=='nested' and b'(Claude Code)' in captured) and not detached:
   # A ready screen does not imply the attached session inherited the global
   # prefix. Consume its actual effective option, as a human using that session
   # does; never change an existing server/session keybinding for this test.
   observed_prefix=lower(['show-options','-Av','-t','repo_name_worktree-physical','prefix']).stdout.decode().strip()
   assert observed_prefix.startswith('C-') and len(observed_prefix)==3,observed_prefix
   binding=lower(['list-keys','-T','prefix','d']).stdout.decode()
   assert 'detach-client' in binding,binding
   os.write(master,bytes([ord(observed_prefix[-1].upper())-64])+b'd');detached=True
  # Real tmux -CC with an exiting inner process emits %exit and terminates.
 while select.select([master],[],[],.05)[0]:
  try:
   block=os.read(master,65536)
   if not block:break
   captured.extend(block)
  except OSError:break
 state=json.loads((root/'terminal-state.json').read_text())
 assert process.returncode==0,(process.returncode,repr(captured[-3500:]))
 assert state['childExitCode']==0 and state['terminalRestored'],'tmux terminal modes were not restored'
 tree=work/'.claude'/'worktrees'/'physical'
 assert (tree/'tracked.txt').read_text()=='PRIVATE_REAL_GIT_BYTES'
 actual=trace.read_text().splitlines()
 if mode.startswith('nested'):
  assert any(a.startswith('switch-client -t repo_name_worktree-physical') for a in actual),actual
  creates=[a for a in actual if 'new-session -d -s repo_name_worktree-physical' in a]
  assert len(creates)==1,actual
  if mode=='nested':assert binary in creates[0] and '--version' in creates[0] and '--worktree' not in creates[0],actual
  assert detached and lower(['has-session','-t','private-anchor']).returncode==0
  outer=lower(['capture-pane','-p','-t','private-anchor']).stdout
  assert b'OWNED_NESTED_OUTER_DONE=0' in outer,outer
 else:
  inherited=[a for a in actual if ('new-session -A' in a)]
  assert len(inherited)==1,actual
  assert '--version' in inherited[0] and '--worktree' not in inherited[0] and '--tmux' not in inherited[0],actual
  assert binary in inherited[0],actual
 if mode=='control':
  assert inherited[0].startswith('-CC '),actual
  assert b'iTerm2 Tip' in captured and b'%exit' in captured,repr(captured[-3500:])
 elif mode in ('existing','nested-existing'):
  assert detached and b'EXISTING_PHYSICAL_TMUX_MARKER' in captured
  assert lower(['has-session','-t','repo_name_worktree-physical']).returncode==0,'detach killed existing session'
 else:assert b'(Claude Code)' in captured,repr(captured[-3500:])
 if mode.startswith('async'):
  pid=int(pidpath.read_text());
  try:os.kill(pid,0);raise AssertionError('background Hook PID escaped actual parent CLI cleanup')
  except ProcessLookupError:pass
 assert not (config/'projects').exists(),'pre-CLI fastpath created ASK/canonical history'
 (root/'terminal.raw').write_bytes(captured)
 (root/'receipt.json').write_text(json.dumps({'mode':mode,'exitCode':process.returncode,'terminalRestored':True,'actualTmuxArguments':actual,'observedSessionPrefix':observed_prefix,'physicalTrackedBytes':True,'modelRequests':0,'preCLIHistoryWrites':0},indent=2))
 print(json.dumps({'mode':mode,'exitCode':process.returncode,'terminalRestored':True,'physicalTrackedBytes':True,'modelRequests':0,'preCLIHistoryWrites':0}))
finally:
 if process is not None and process.poll() is None:process.kill();process.wait()
 if master is not None:os.close(master)
 if slave is not None:os.close(slave)
 pid_reply=lower(['display-message','-p','#{pid}'],False)
 pid=int(pid_reply.stdout.strip()) if pid_reply.returncode==0 else None
 lower(['kill-server'],False)
 if pid is not None:
  deadline=time.monotonic()+3
  while True:
   try:os.kill(pid,0)
   except ProcessLookupError:break
   if time.monotonic()>deadline:raise AssertionError('owned tmux server PID survived '+str(pid))
   time.sleep(.01)
 assert lower(['has-session'],False).returncode!=0,'owned tmux server survived cleanup'
 # tmux intentionally leaves a stale Unix socket after server exit. Remove
 # only this test-owned file after observing the actual server PID disappear.
 if socket.exists():socket.unlink()
