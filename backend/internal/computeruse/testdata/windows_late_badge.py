# Physical stdout/stdin protocol fixture for the external badge process.
import os,sys,pathlib
root=pathlib.Path(os.environ["CC_HAHA_LATE_BADGE_ROOT"])
(root/"pid").write_text(str(os.getpid()))
try:
    for _ in range(1152):os.write(1,b"x"*65536)
    os.write(1,b"READY\n")
    (root/"ready").write_text("physical late READY emitted")
    for line in sys.stdin:
        with (root/"activities").open("a") as f:f.write(line)
    (root/"eof").write_text("stdin EOF")
except BrokenPipeError:
    (root/"broken").write_text("stdout reader closed before late READY")
    os._exit(31)
