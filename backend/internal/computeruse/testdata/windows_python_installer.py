# Physical external installer fixture: never runs pip or connects to a package index.
import sys,os,json,pathlib,subprocess
root=pathlib.Path(os.environ["CLAUDE_CONFIG_DIR"])/".runtime"
def event(**fields):
    with open(os.environ["CC_HAHA_FIXTURE_JOURNAL"],"a") as f:f.write(json.dumps({"event":"installer",**fields})+"\n")
args=sys.argv[1:]
if args[:2]==["-m","venv"]:
    event(args=args);scripts=pathlib.Path(args[2])/"Scripts";scripts.mkdir(parents=True,exist_ok=True)
    (scripts/"python.exe").write_bytes(pathlib.Path(sys.argv[0]).read_bytes());(scripts/"python.exe").chmod(0o700)
    raise SystemExit(0)
if args[:2]==["-m","ensurepip"]:
    event(args=args);(root/"venv/Scripts/pip.exe").write_text("owned pip marker");raise SystemExit(0)
if args[:3]==["-m","pip","install"]:
    event(args=args)
    failure=pathlib.Path(os.environ["CC_HAHA_FIXTURE_FAIL_UPGRADE"])
    if "--upgrade" in args and failure.exists():
        print("offline fixture upgrade denied",file=sys.stderr);raise SystemExit(7)
    raise SystemExit(0)
if args and args[0].endswith(("win_helper.py","win_cursor_badge.py")):
    os.execv(os.environ["CC_HAHA_FIXTURE_PYTHON"],[os.environ["CC_HAHA_FIXTURE_PYTHON"],*args])
raise SystemExit("fixture forbids unexpected external installer execution")
