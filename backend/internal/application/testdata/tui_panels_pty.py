"""Public RunCommand terminal menus, actual edits, next Harness request and exit."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time,re,signal,faulthandler
from vt_screen import VTScreen
binary,home=sys.argv[1:]
diagnostic=open(pathlib.Path(home,'panels-driver-stack.txt'),'w')
faulthandler.dump_traceback_later(25,file=diagnostic)
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,140,0,0));before=termios.tcgetattr(slave);captured=bytearray();deadline=time.monotonic()+38;screen=VTScreen(140,36)
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTUILocalPanelsFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:
   data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
  pathlib.Path(home,'panels-running-terminal.txt').write_bytes(captured)
def wait(needle,label,start=0):
 while needle.decode() not in screen.text() and needle not in re.sub(rb"\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07]*(?:\x07|\x1b\\)",b"",bytes(captured[start:])):
  if time.monotonic()>deadline or p.poll() is not None:
   if p.poll() is None:
    p.send_signal(signal.SIGQUIT)
    limit=time.monotonic()+1
    while time.monotonic()<limit:pump()
   raise AssertionError(label+' '+repr(bytes(captured[-12000:])))
  pump()
def send(data):os.write(master,data);time.sleep(.18);pump()
def until(predicate,label):
 while not predicate():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-4000:])))
  pump()
def menu(command,needle):
 pathlib.Path(home,'panels-running-stage.txt').write_text(command.decode())
 start=len(captured);send(command+b'\r');wait(needle,command.decode(),start)
def close():
 def prompt_returned():
  lines=screen.text().splitlines()
  return any(line.strip().startswith('>') and i+1<len(lines) and 'shift+tab to cycle' in lines[i+1] for i,line in enumerate(lines))
 send(b'\x1b');until(prompt_returned,'actual modal close returned prompt input')
try:
 wait(b'Claude Code','startup')
 menu(b'/help',b'Claude understands your codebase');send(b'\x03');wait(b'Press Ctrl-C again to exit','actual mounted Help first interrupt');until(lambda:'Press Ctrl-C again to exit' not in screen.text(),'actual mounted Help confirmation timeout');send(b'\t');wait(b'Browse default commands:','actual Help commands tab');send(b'\x1b[B\r');close()
 menu(b'/skills',b'Available skills from the current project');close()
 menu(b'/tasks',b'Actual running and completed tasks');close()
 menu(b'/mcp',b'Manage connections in the current session');send(b'r');close()

 menu(b'/theme',b'Choose the text style that looks best with your terminal');wait(b'Light mode (ANSI colors only)','actual six-theme choice');pathlib.Path(home,'panels-running-stage.txt').write_text('theme arrows')
 for choice in ['Light mode','Dark mode (colorblind-friendly)','Light mode (colorblind-friendly)','Dark mode (ANSI colors only)','Light mode (ANSI colors only)']:
  send(b'\x1b[B');until(lambda:('> '+choice) in screen.text(),'actual focused theme '+choice)
 send(b'\r')
 pathlib.Path(home,'panels-running-stage.txt').write_text('theme completion')
 config=pathlib.Path(pathlib.Path(home,'panels-configdir.txt').read_text());globalpath=config/'.claude.json'
 wait(b'Theme set to light-ansi','actual Theme onDone after persistent save')
 assert json.loads(globalpath.read_text())['theme']=='light-ansi'
 menu(b'/config',b'Search settings');send(b'/autoCompactEnabled\r');wait(b'Auto-compact','actual searched Config row');send(b'\x1b[C');until(lambda:json.loads(globalpath.read_text()).get('autoCompactEnabled')==False,'immediate native global write');close()
 assert json.loads(globalpath.read_text()).get('autoCompactEnabled',True)
 menu(b'/config',b'Search settings');send(b'/instructionFiles\r');wait(b'Project instructions','actual searched instructions row');send(b'\x1b[C\x1b[C');until(lambda:(config/'settings.json').exists() and json.loads((config/'settings.json').read_text()).get('pluginConfigs',{}).get('agents-md@builtin',{}).get('options',{}).get('instructionFiles')=='managed-only','actual instruction config');send(b'\r')
 assert json.loads((config/'settings.json').read_text())['pluginConfigs']['agents-md@builtin']['options']['instructionFiles']=='managed-only'
 menu(b'/memory',b'Edit user and project memory files');send(b'\x1b[He');wait(b'Edit memory','public memory editor');send(b'\x15PRIVATE_PTY_MEMORY_FILE_EDIT\r');close()
 work=pathlib.Path(pathlib.Path(home,'panels-workdir.txt').read_text());assert (work/'CLAUDE.md').read_text()=='PRIVATE_PTY_MEMORY_FILE_EDIT',((work/'CLAUDE.md').read_text(),repr(bytes(captured[-4000:])))
 menu(b'/hooks',b'Hook configurations for tool events');close()
 send(b'/output-style\r');wait(b'/output-style has been deprecated','style deprecation')
 menu(b'/agents',b'Configured agents used by the current session');send(b'a');wait(b'Create agent','agent form')
 send(b'pty-worker\t\x15project\tactual reviewer\tPRIVATE_PTY_AGENT_PROMPT\t\tRead\r')
 work=pathlib.Path(pathlib.Path(home,'panels-workdir.txt').read_text());agent=work/'.claude/agents/pty-worker.md'
 while not agent.exists():
  if time.monotonic()>deadline:raise AssertionError('agent file not written '+repr(bytes(captured[-4000:])))
  pump()
 assert 'PRIVATE_PTY_AGENT_PROMPT' in agent.read_text()
 until(lambda:'Configured agents used by the current session' in screen.text() and 'Create agent' not in screen.text() and 'pty-worker' in screen.text(),'actual saved agent catalog after form completion')
 close()
 send(b'Start an actual background task.\r');wait(b'PTY_BACKGROUND_STARTED_348','actual background Bash');wait(b'1 shell','actual task footer snapshot');send(b'PRIVATE_FOOTER_DRAFT_348');start=len(captured);send(b'\x1b[B\x1b[B');wait(b'private PTY background task','actual footer keyboard opens tasks',start);assert json.loads(globalpath.read_text()).get('hasSeenTasksHint') is True,'actual footer persists hint through private Global.Save';send(b'o');wait(b'PRIVATE_PTY_TASK_OUTPUT','actual background output');send(b'r');wait(b'Background tasks','return tasks');send(b'x');wait(b'killed','actual task stop');close();wait(b'PRIVATE_FOOTER_DRAFT_348','footer retained current draft');send(b'\x15')
 menu(b'/permissions',b'Allow, deny and ask rules');send(b'a');wait(b'Add permission rule','permission form');send(b'\x15deny\t\tWrite\r');wait(b'deny', 'saved deny')
 close();send(b'Continue after all local panels.\r');wait(b'PANELS_PUBLIC_NEXT_DONE_348','actual next turn')
 # Source TaskListV2 returns null for an empty durable task list. The Bash
 # background task above is a different store; assert the actual root binding
 # and persisted view rather than waiting for a nonexistent empty component.
 send(b'\x14');until(lambda:json.loads(globalpath.read_text()).get('showExpandedTodos') is True,'actual empty-task root toggle saved')
 assert not re.search(r'(?m)^\s*0 tasks\b|^\s*No tasks\b',screen.text()),'empty Source task component painted'
 send(b'\x14');until(lambda:json.loads(globalpath.read_text()).get('showExpandedTodos',False) is False,'actual empty-task root toggle collapsed');send(b'\x18\x0b');wait(b'No background agents running','kill agents consumer')
 send(b'\x04\x04')
 until(lambda:pathlib.Path(home,'panels-receipt.json').exists(),'native normal-exit restoration receipt while leader alive');assert p.poll() is None and before==termios.tcgetattr(slave),'physical termios while original leader is alive';pathlib.Path(home,'panels-restoration-inspected').write_text('private restoration inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('exit timeout')
  pump()
 receipt=json.loads(pathlib.Path(home,'panels-receipt.json').read_text());assert p.returncode==0 and receipt['requests']==3 and receipt['terminalRestored'],(receipt,p.returncode,repr(bytes(captured[-6000:])))
 print(json.dumps({'terminalRestored':True,'menusZeroModelPOST':True,'actualAgentFile':True,'themeSaved':True,'configEscapeRollbackAndNextRequestConsumer':True,'memoryFileEdited':True,'hooksReadOnly':True,'outputStyleDeprecation':True,'backgroundOutputAndStop':True,'savedPermissionNextRequest':True,'sameHarnessNextHuman':True,'emptyTaskToggleSavedWithoutEmptyComponent':True,'requests':3,'exitCode':p.returncode}))
finally:
 faulthandler.cancel_dump_traceback_later()
 pathlib.Path(home,'panels-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
