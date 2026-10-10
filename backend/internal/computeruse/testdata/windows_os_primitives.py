# External Windows OS primitives only. The original helper dispatch and JPEG code run unchanged.
import ctypes, types, sys, os, json, threading, atexit
from PIL import Image, ImageChops, ImageDraw, ImageFilter
_stop=threading.Event()
def record(event,**fields):
    path=os.environ.get("CC_HAHA_FIXTURE_JOURNAL")
    if path:
        with open(path,"a",encoding="utf-8") as f:f.write(json.dumps({"event":event,"pid":os.getpid(),"tag":os.environ.get("CC_HAHA_COMPUTER_USE_INPUT_TAG"),**fields},ensure_ascii=False)+"\n")
record("launch",argv=sys.argv)
atexit.register(lambda:record("exit"))
class Function:
    def __init__(self,name): self.name=name
    def __call__(self,*args):
        if self.name == "GetDpiForMonitor": return 1
        if self.name == "SetWindowsHookExW":record("hook");return 1
        if self.name == "GetMessageW":_stop.wait();return 0
        if self.name in ("PostMessageW","PostThreadMessageW","PostQuitMessage"):_stop.set();record("stop");return 1
        if self.name == "GetWindowThreadProcessId":args[1]._obj.value=42;return 1
        if self.name == "GetTopWindow":return 0
        return 1
class DLL:
    def __getattr__(self,name):
        v=Function(name); setattr(self,name,v); return v
ctypes.WinDLL=lambda *a,**k:DLL()
ctypes.WINFUNCTYPE=ctypes.CFUNCTYPE
ctypes.windll=types.SimpleNamespace(user32=DLL(),shcore=DLL(),gdi32=DLL(),kernel32=DLL())
py=types.ModuleType("pyautogui"); py.position=lambda:(3,4); sys.modules["pyautogui"]=py
mon=types.ModuleType("screeninfo")
mon.get_monitors=lambda:[types.SimpleNamespace(x=-8,y=0,width=8,height=6,is_primary=True,name="隔离😀")]
sys.modules["screeninfo"]=mon
class Capture:
    def __enter__(self): return self
    def __exit__(self,*a): pass
    def grab(self,r): return types.SimpleNamespace(size=(r["width"],r["height"]),rgb=bytes([11,32,54])*(r["width"]*r["height"]))
m=types.ModuleType("mss"); m.mss=Capture; sys.modules["mss"]=m

# Preserve original badge create/READY/stdin loop, replacing only its native host facts.
if sys.argv[0].endswith("win_cursor_badge.py"):
    sys.platform="win32"
    class Input:
        def __iter__(self):
            for line in sys.__stdin__:record("activity",line=line);yield line
            record("stdin_eof")
    sys.stdin=Input()
# A physical blocking I/O barrier before original helper dispatch, for cancellation/Close tests.
if sys.argv[0].endswith("win_helper.py") and os.environ.get("CC_HAHA_FIXTURE_HOLD"):
    if len(sys.argv)>1 and sys.argv[1]==os.environ["CC_HAHA_FIXTURE_HOLD"]:
        record("held")
        with open(os.environ["CC_HAHA_FIXTURE_BARRIER"],"rb",buffering=0) as f:f.read(1)

# Wire-envelope fixture operates at the external helper boundary. No dispatcher is replaced in actual-helper rows.
if sys.argv[0].endswith("win_helper.py") and os.environ.get("CC_HAHA_FIXTURE_ENVELOPE"):
    with open(os.environ["CC_HAHA_FIXTURE_ENVELOPE"],encoding="utf-8") as f:rows=json.load(f)
    row=rows[sys.argv[1]]
    os.write(1,row.get("stdout","").encode("utf-8"));os.write(2,row.get("stderr","").encode("utf-8"))
    os._exit(row.get("code",0))
