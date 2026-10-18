"""Public RunCommand, actual StatusLine shell/settings/Vim/model/tools and exit."""
import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
from vt_screen import VTScreen
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,132,0,0))
before=termios.tcgetattr(slave);captured=bytearray();deadline=time.monotonic()+32;screen=VTScreen(132,36)
def setup():
 os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTerminalStatusLinePTYFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:
   data=os.read(master,65536);captured.extend(data);screen.feed(data)
  except OSError:pass
def until(check,label,limit=None):
 stop=min(deadline,time.monotonic()+limit) if limit else deadline
 while not check():
  if time.monotonic()>stop or p.poll() is not None:raise AssertionError(label+' SCREEN '+repr(screen.text())+' RAW '+repr(bytes(captured[-6000:])))
  pump()
def visible(text):return text in screen.text()
def send(value):os.write(master,value)
def settle(seconds):
 stop=time.monotonic()+seconds
 while time.monotonic()<stop:pump()
try:
 until(lambda:visible('PUBLIC_STATUSLINE_ONE INSERT'),'actual startup footer')
 paths=json.loads(pathlib.Path(home,'statusline-paths.json').read_text());native_home=pathlib.Path(paths['home']);receipt=native_home/'public-statusline-inputs.jsonl'
 settle(.45);assert len(receipt.read_text().splitlines())==1,'initial render spuriously reran original mount command'
 send(b'\x1b');until(lambda:visible('PUBLIC_STATUSLINE_ONE NORMAL'),'actual Vim NORMAL callback')
 send(b'i');until(lambda:visible('PUBLIC_STATUSLINE_ONE INSERT'),'actual Vim INSERT callback')
 send(b'PUBLIC_STATUSLINE_WRITE\r');until(lambda:visible('PUBLIC_STATUSLINE_WRITE_DONE'),'same Harness actual Write')
 until(lambda:any(json.loads(row)['cost']['total_lines_added']>0 for row in receipt.read_text().splitlines()),'actual Write cost tracker in callback')
 send(b'PUBLIC_STATUSLINE_NEXT_HUMAN\r');until(lambda:visible('PUBLIC_STATUSLINE_NEXT_DONE'),'same Harness next human actual Read')
 settings=pathlib.Path(paths['settings']);value=json.loads(settings.read_text());value['statusLine']['command']=paths['held'];settings.write_text(json.dumps(value))
 until(lambda:(native_home/'statusline-held-ready').exists(),'idle actual setting watcher launched new command')
 pid=int((native_home/'statusline-held-ready').read_text())
 def reaped(pid):
  try:os.kill(pid,0);return False
  except ProcessLookupError:return True
 send(b'/he');until(lambda:visible('Tab: complete'),'actual command completion menu')
 until(lambda:reaped(pid),'actual completion unmount failed to retire held statusline',2)
 assert not visible('PUBLIC_STATUSLINE_ONE'),'completion menu retained prompt statusline'
 hidden_count=len(receipt.read_text().splitlines())
 first_script=pathlib.Path(paths['normalScript']);first_script.write_text(first_script.read_text().replace('PUBLIC_STATUSLINE_ONE','PUBLIC_STATUSLINE_COMPLETION_REMOUNT'))
 value['statusLine']['command']=paths['normal'];settings.write_text(json.dumps(value));settle(.45)
 assert len(receipt.read_text().splitlines())==hidden_count,'completion menu ran hot-reloaded statusline command'
 send(b'\x1b');until(lambda:visible('NORMAL'),'actual Vim exits INSERT while completion owns its draft')
 send(b'cc');until(lambda:not visible('Tab: complete'),'actual Vim change-line clears completion draft')
 until(lambda:visible('PUBLIC_STATUSLINE_COMPLETION_REMOUNT'),'actual cleared completion draft remounted fresh command')
 (native_home/'statusline-held-ready').unlink()
 value['statusLine']['command']=paths['held'];settings.write_text(json.dumps(value))
 until(lambda:(native_home/'statusline-held-ready').exists(),'actual held command before bracketed paste')
 pid=int((native_home/'statusline-held-ready').read_text())
 send(b'\x1b[200~')
 until(lambda:reaped(pid),'actual bracketed paste-start failed to retire held statusline',2)
 assert not visible('PUBLIC_STATUSLINE_COMPLETION_REMOUNT'),'active bracketed paste retained prompt statusline'
 hidden_count=len(receipt.read_text().splitlines())
 first_script.write_text(first_script.read_text().replace('PUBLIC_STATUSLINE_COMPLETION_REMOUNT','PUBLIC_STATUSLINE_PASTE_REMOUNT'))
 value['statusLine']['command']=paths['normal'];settings.write_text(json.dumps(value));settle(.45)
 assert len(receipt.read_text().splitlines())==hidden_count,'active bracketed paste ran hot-reloaded statusline command'
 send(b'\x1b[201~');until(lambda:visible('PUBLIC_STATUSLINE_PASTE_REMOUNT'),'actual paste-end remounted fresh command')
 (native_home/'statusline-held-ready').unlink()
 value['statusLine']['command']=paths['held'];settings.write_text(json.dumps(value))
 until(lambda:(native_home/'statusline-held-ready').exists(),'actual held command before shortcut help')
 pid=int((native_home/'statusline-held-ready').read_text())
 send(b'?');until(lambda:visible('! for bash mode'),'actual question-mark shortcut HelpOpen')
 until(lambda:reaped(pid),'actual shortcut HelpOpen failed to retire held statusline',2)
 assert not visible('PUBLIC_STATUSLINE_PASTE_REMOUNT'),'shortcut HelpOpen retained prompt statusline'
 hidden_count=len(receipt.read_text().splitlines())
 first_script.write_text(first_script.read_text().replace('PUBLIC_STATUSLINE_PASTE_REMOUNT','PUBLIC_STATUSLINE_SHORTCUT_REMOUNT'))
 value['statusLine']['command']=paths['normal'];settings.write_text(json.dumps(value));settle(.45)
 assert len(receipt.read_text().splitlines())==hidden_count,'shortcut HelpOpen ran hot-reloaded statusline command'
 send(b'\x1f');settle(.15)
 # The actual Undo history restores /he. Original PromptInputFooter gives
 # suggestions priority over HelpOpen; Stash below exposes retained help again.
 assert visible('Tab: complete'),'actual Undo did not restore its completion draft SCREEN '+repr(screen.text())+' RAW '+repr(bytes(captured[-3000:]))
 assert len(receipt.read_text().splitlines())==hidden_count,'actual Undo remounted hidden StatusLine'
 send(b'\x13');settle(.15)
 assert visible('! for bash mode'),'actual Stash removed shortcut HelpOpen SCREEN '+repr(screen.text())+' RAW '+repr(bytes(captured[-3000:]))
 assert len(receipt.read_text().splitlines())==hidden_count,'actual Stash remounted hidden StatusLine'
 send(b'\x1b');until(lambda:visible('PUBLIC_STATUSLINE_SHORTCUT_REMOUNT'),'actual Help dismiss remounted fresh command')
 assert not visible('Rewind'),'shortcut Help dismiss invoked rewind instead'
 assert not visible('! for bash mode'),'shortcut Help remained open after actual dismiss'
 (native_home/'statusline-held-ready').unlink()
 value['statusLine']['command']=paths['held'];settings.write_text(json.dumps(value))
 until(lambda:(native_home/'statusline-held-ready').exists(),'actual held command before modal help')
 pid=int((native_home/'statusline-held-ready').read_text())
 send(b'/help\r');until(lambda:visible('Claude understands your codebase'),'actual Source General Help modal unmounted prompt footer')
 until(lambda:reaped(pid),'actual modal unmount failed to retire held statusline',2)
 hidden_count=len(receipt.read_text().splitlines())
 first_script=pathlib.Path(paths['normalScript']);first_script.write_text(first_script.read_text().replace('PUBLIC_STATUSLINE_SHORTCUT_REMOUNT','PUBLIC_STATUSLINE_REMOUNT'))
 value['statusLine']['command']=paths['normal'];settings.write_text(json.dumps(value));settle(.45)
 assert len(receipt.read_text().splitlines())==hidden_count,'hidden modal ran hot-reloaded statusline command'
 send(b'\x1b');until(lambda:visible('PUBLIC_STATUSLINE_REMOUNT'),'actual closing modal remounted footer with fresh native output')
 remount_count=len(receipt.read_text().splitlines())
 assert remount_count-hidden_count in (1,2),'remount plus at most one pending original settings change reran command'
 (native_home/'statusline-held-ready').unlink()
 value['statusLine']['command']=paths['held'];settings.write_text(json.dumps(value))
 until(lambda:(native_home/'statusline-held-ready').exists(),'second held native command after remount')
 pid=int((native_home/'statusline-held-ready').read_text())
 send(b'/exit\r')
 result_file=pathlib.Path(home,'statusline-pty-receipt.json')
 until(result_file.exists,'exit while held statusline did not cancel/join',3)
 result=json.loads(result_file.read_text());assert result['terminalRestored'] and result['code']==0 and result['actualModelRequests']==4,result
 assert termios.tcgetattr(slave)==before,'real slave termios not restored'
 try:os.kill(pid,0);raise AssertionError('held statusline native PID retained after normal exit')
 except ProcessLookupError:pass
 assert not (native_home/'forbidden-late-statusline').exists(),'old script executed late physical side effect'
 callback_count=len(receipt.read_text().splitlines())
 pathlib.Path(home,'statusline-pty-release').write_text('inspected')
 until(lambda:p.poll() is not None,'public helper remained pending');assert p.returncode==0,p.returncode
 print(json.dumps({'terminalRestored':True,'actualModelRequests':4,'statusLineCallbacks':callback_count,'completionHeldNativePIDReaped':True,'pasteHeldNativePIDReaped':True,'shortcutHelpHeldNativePIDReaped':True,'shortcutHelpHotReloadSuspended':True,'shortcutHelpUndoStashPreserved':True,'shortcutHelpDismissRemounted':True,'modalHeldNativePIDReaped':True,'modalHotReloadSuspended':True,'heldNativePIDReaped':True,'exitCode':p.returncode}))
finally:
 pathlib.Path(home,'statusline-terminal.txt').write_bytes(captured)
 if 'pid' in locals():
  try:os.kill(pid,15)
  except ProcessLookupError:pass
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
