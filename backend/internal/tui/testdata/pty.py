"""Native Unix PTY driver for the production Go TUI, with a mock Session.

No provider, TypeScript process or developer state is used by this fixture.
The Go test executable is reused; this driver never invokes the Go toolchain.
"""
import fcntl
import json
import os
import pathlib
import select
import signal
import struct
import subprocess
import sys
import termios
import time

binary, home, scenario = sys.argv[1:]
master, slave = os.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
captured = bytearray()
events_path = pathlib.Path(home, ".fixture", "events.jsonl")
deadline = time.monotonic() + 18


def child_terminal():
    os.setsid()
    fcntl.ioctl(slave, termios.TIOCSCTTY, 0)


process = subprocess.Popen(
    [binary, "-test.run=^TestTerminalFixtureProcess$", "-test.count=1"],
    stdin=slave, stdout=slave, stderr=slave, env=os.environ.copy(),
    preexec_fn=child_terminal, close_fds=True,
)


def events():
    if not events_path.exists():
        return []
    raw = events_path.read_bytes()
    # Only complete newline-terminated writes are evidence.
    return [json.loads(row) for row in raw.splitlines() if row] if raw.endswith(b"\n") else []


def pump(timeout=0.05):
    readable, _, _ = select.select([master], [], [], timeout)
    if readable:
        captured.extend(os.read(master, 65536))


def wait(predicate, description):
    while not predicate():
        if time.monotonic() >= deadline:
            raise AssertionError("deadline waiting for " + description)
        if process.poll() is not None:
            raise AssertionError("process exited while waiting for " + description)
        pump()


try:
    wait(lambda: b"Go TUI fixture" in captured, "initial terminal view")
    if scenario == "foreground-task":
        os.write(master, b"abc\x02X")
        wait(lambda: b"abXc" in captured, "idle Ctrl+B readline cursor")
        assert not [e for e in events() if e["kind"] == "action"], "idle Ctrl+B invoked a task"
        os.write(master, b"\x05\x15foreground\r")
        wait(lambda: b"FG_TASK_REGISTERED" in captured, "foreground producer in active TUI")
        os.write(master, b"\x02")
        wait(lambda: b"FG_BACKGROUND_OK" in captured, "actual background host action")
        actions = [e for e in events() if e["kind"] == "action"]
        assert actions == [{"kind": "action", "name": "task:background"}], actions
        os.write(master, b"def\x02Y\r")
        wait(lambda: any(e["kind"] == "permission-ready" for e in events()), "readline restored after task completion")
        submitted = [e for e in events() if e["kind"] == "submit"]
        assert [e["input"]["Text"] for e in submitted] == ["foreground", "deYf"], submitted
        assert len([e for e in events() if e["kind"] == "action"]) == 1
        os.write(master, b"y")
        wait(lambda: b"FILE_CREATED" in captured, "next ordinary turn completed")
        os.write(master, b"\x04\x04")
        while process.poll() is None:
            if time.monotonic() >= deadline:
                raise AssertionError("foreground scenario did not exit")
            pump()
        pump(0)
        assert process.returncode == 0, (process.returncode, captured.decode(errors="replace"))
        assert len([e for e in events() if e["kind"] == "foreground-unsubscribed"]) == 1
        assert any(e.get("kind") == "terminal-state-restored" and e.get("restored") is True for e in events())
        assert len([e for e in events() if e["kind"] == "closed"]) == 1
        pathlib.Path(home, ".fixture", "terminal.raw").write_bytes(captured)
        print(json.dumps({"terminalRestored": True, "scenario": scenario, "foregroundTask": True, "readlineRestored": True, "exitCode": process.returncode}))
        sys.exit(0)
    if scenario == "model-picker":
        os.write(master, b"\x1b[200~" + "模型草稿👋".encode() + b"\x1b[201~\x1bp")
        wait(lambda: b"Select model" in captured, "native model picker rendering")
        assert not [e for e in events() if e["kind"] == "submit"], "model picker reached model prompt"
        os.write(master, b"\x1b")
        wait(lambda: any(e["kind"] == "model-selection" for e in events()), "Source cancellation callback")
        selections = [e for e in events() if e["kind"] == "model-selection"]
        assert selections[0]["result"]["Cancelled"] is True, selections
        os.write(master, b"\x1bp")
        wait(lambda: len([e for e in events() if e["kind"] == "action"]) == 2, "reopened picker")
        # Original Select next, ModelPicker decrease effort, Select accept.
        os.write(master, b"\x1b[B\x1b[D\r")
        wait(lambda: len([e for e in events() if e["kind"] == "model-selection"]) == 2, "selection callback")
        selection = [e for e in events() if e["kind"] == "model-selection"][-1]
        assert selection["id"] == "native-picker" and selection["result"] == {"Cancelled": False, "Model": "opus", "Effort": "low", "HasToggledEffort": True}, selection
        wait(lambda: b"MODEL_SELECTED_ORIGINAL_HOST" in captured, "shared host selection result")
        assert not [e for e in events() if e["kind"] == "submit"], "selection started another model loop"
        os.write(master, b"\r")
        wait(lambda: any(e["kind"] == "permission-ready" for e in events()), "unchanged prompt and tool path")
        submits = [e for e in events() if e["kind"] == "submit"]
        assert len(submits) == 1 and submits[0]["input"]["Text"] == "模型草稿👋", submits
        os.write(master, b"y")
        wait(lambda: b"FILE_CREATED" in captured, "real terminal stream and tool completion")
        os.write(master, b"\x04\x04")
        while process.poll() is None:
            if time.monotonic() >= deadline: raise AssertionError("model picker scenario did not exit")
            pump()
        pump(0)
        assert process.returncode == 0, (process.returncode, captured.decode(errors="replace"))
        assert any(e.get("kind") == "terminal-state-restored" and e.get("restored") is True for e in events())
        assert len([e for e in events() if e["kind"] == "closed"]) == 1
        assert b"\x1b[?1049l" in captured and b"\x1b[?2004l" in captured and b"\x1b[?25h" in captured
        print(json.dumps({"terminalRestored": True, "scenario": scenario, "modelPicker": True, "exitCode": process.returncode}))
        sys.exit(0)
    if scenario == "keybindings":
        os.write(master, b"\x1b[200~" + "中文👋".encode() + b"\x1b[201~")
        wait(lambda: "中文👋".encode() in captured, "original command draft")
        os.write(master, b"x\x1bu\x1bm")
        wait(lambda: any(e["kind"] == "action" for e in events()), "shared host command action")
        actions = [e for e in events() if e["kind"] == "action"]
        assert actions == [{"kind": "action", "name": "command:model"}], actions
        assert not [e for e in events() if e["kind"] == "submit"], "host command became model prompt"
        wait(lambda: b"HOST_COMMAND_ONLY" in captured, "host action notice")
        os.write(master, b"\x13\r")
        wait(lambda: any(e["kind"] == "permission-ready" for e in events()), "preserved draft submission")
        submits = [e for e in events() if e["kind"] == "submit"]
        assert len(submits) == 1 and submits[0]["input"]["Text"] == "中文👋", submits
        os.write(master, b"y")
        wait(lambda: b"FILE_CREATED" in captured, "draft turn completion")
        os.write(master, b"\x04\x04")
        while process.poll() is None:
            if time.monotonic() >= deadline:
                raise AssertionError("configured key scenario did not exit")
            pump()
        pump(0)
        assert process.returncode == 0, (process.returncode, captured.decode(errors="replace"))
        assert any(e.get("kind") == "terminal-state-restored" and e.get("restored") is True for e in events())
        assert len([e for e in events() if e["kind"] == "closed"]) == 1
        assert b"\x1b[?1049l" in captured and b"\x1b[?2004l" in captured and b"\x1b[?25h" in captured
        pathlib.Path(home, ".fixture", "terminal.raw").write_bytes(captured)
        print(json.dumps({"terminalRestored": True, "scenario": scenario, "hostCommand": True, "customUndo": True, "nullUnbinding": True, "exitCode": process.returncode}))
        sys.exit(0)
    if scenario == "editor":
        os.write(master, b"draft-before-editor")
        wait(lambda: b"draft-before-editor" in captured,"editor draft")
        os.write(master,b"\x07")
        wait(lambda: any(e["kind"]=="editor-terminal-state" for e in events()),"native editor handoff")
        wait(lambda: "edited 中文".encode() in captured,"edited prompt after terminal resume")
        os.write(master,b"\r")
        wait(lambda: any(e["kind"]=="permission-ready" for e in events()),"edited submission")
        submits=[e for e in events() if e["kind"]=="submit"]
        assert len(submits)==1 and submits[0]["input"]["Text"]=="edited 中文", submits
        os.write(master,b"y")
        wait(lambda: b"FILE_CREATED" in captured,"edited turn completion")
        os.write(master,b"\x04\x04")
        while process.poll() is None:
            if time.monotonic()>=deadline:raise AssertionError("editor scenario did not exit")
            pump()
        pump(0)
        assert process.returncode==0,(process.returncode,captured.decode(errors="replace"))
        restored=[e for e in events() if e["kind"]=="terminal-state-restored"]
        editor=[e for e in events() if e["kind"]=="editor-terminal-state"]
        assert len(restored)==1 and restored[0]["restored"] is True
        assert len(editor)==1 and editor[0]["state"]==restored[0]["entryState"],(editor,restored)
        assert not list(pathlib.Path(home).glob("claude-prompt-*")), "prompt editor temp file leaked"
        pathlib.Path(home,".fixture","terminal.raw").write_bytes(captured)
        print(json.dumps({"terminalRestored": True,"scenario":scenario,"exitCode":process.returncode}))
        sys.exit(0)
    if scenario != "interaction":
        if scenario == "context-cancel":
            os.write(master, b"context-cancel\r")
        elif scenario == "signal":
            os.kill(process.pid, signal.SIGTERM)
        else:
            raise AssertionError("unknown PTY scenario " + scenario)
        while process.poll() is None:
            if time.monotonic() >= deadline:
                raise AssertionError("abnormal termination did not complete")
            pump()
        pump(0)
        assert process.returncode == 0, (process.returncode, captured.decode(errors="replace"))
        assert any(e.get("kind") == "terminal-state-restored" and e.get("restored") is True for e in events())
        assert len([e for e in events() if e["kind"] == "closed"]) == 1
        assert b"\x1b[?1049l" in captured and b"\x1b[?2004l" in captured and b"\x1b[?25h" in captured
        if scenario == "context-cancel":
            assert any(e["kind"] == "context-cancelled" for e in events())
            assert any(e["kind"] == "expected-context-error" for e in events())
        pathlib.Path(home, ".fixture", "terminal.raw").write_bytes(captured)
        print(json.dumps({"terminalRestored": True, "scenario": scenario, "exitCode": process.returncode}))
        sys.exit(0)
    pasted = "你好👨‍👩‍👧‍👦\n第二行\tX"
    os.write(master, b"\x1b[200~" + pasted.encode() + b"\x1b[201~")
    wait(lambda: "你好".encode() in captured, "rendered Unicode paste")
    assert not [e for e in events() if e["kind"] == "submit"], "paste submitted a turn"
    os.write(master, b"\\\r")
    os.write(master, "第三行\r".encode())
    wait(lambda: any(e["kind"] == "permission-ready" for e in events()), "tool permission")
    submitted = [e for e in events() if e["kind"] == "submit"]
    assert len(submitted) == 1, submitted
    assert submitted[0]["input"]["Text"] == "你好👨‍👩‍👧‍👦\n第二行    X\n第三行", submitted
    os.write(master, b"y")
    wait(lambda: any(e["kind"] == "finished" for e in events()), "stream/tool completion")
    wait(lambda: b"FILE_CREATED" in captured, "final streamed output")
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 10, 40, 0, 0))
    os.kill(process.pid, signal.SIGWINCH)
    wait(lambda: any(e.get("kind") == "resize" and e.get("width") == 40 and e.get("height") == 10 for e in events()), "actual resize event")
    os.write(master, b"cancel\r")
    wait(lambda: b"partial_cancel" in captured, "partial text before cancellation")
    os.write(master, b"\x1b")
    wait(lambda: any(e["kind"] == "cancelled" for e in events()), "Escape cancellation")
    os.write(master, b"\x04\x04")
    while process.poll() is None:
        if time.monotonic() >= deadline:
            raise AssertionError("Ctrl-D double press did not terminate")
        pump()
    pump(0)
    assert process.returncode == 0, (process.returncode, captured.decode(errors="replace"))
    # macOS revokes ioctl on a PTY after its controlling session leader exits.
    # The fixture reads the same native descriptor before Run and in Close,
    # after Bubble Tea shutdown restored it but before the leader exits.
    assert any(e.get("kind") == "terminal-state-restored" and e.get("restored") is True
               for e in events()), "native terminal modes were not restored"
    assert b"\x1b[?1049l" in captured, "alternate screen was not restored"
    assert b"\x1b[?2004l" in captured, "bracketed paste was not disabled on exit"
    assert b"\x1b[?25h" in captured, "cursor was not restored"
    assert len([e for e in events() if e["kind"] == "closed"]) == 1
    pathlib.Path(home, ".fixture", "terminal.raw").write_bytes(captured)
    print(json.dumps({"terminalRestored": True, "unicodePaste": True,
                      "approval": True, "cancellation": True, "resize": True,
                      "exitCode": process.returncode}))
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
    os.close(slave)
