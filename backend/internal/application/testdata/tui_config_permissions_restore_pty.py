"""Real /config keyboard consumer: current VT focus, nested-key Escape, no POST."""

import errno
import fcntl
import hashlib
import json
import os
import pathlib
import select
import signal
import stat
import struct
import subprocess
import sys
import termios
import time
import traceback

# Reuse the existing same-directory parser without writing a source __pycache__.
sys.dont_write_bytecode = True
from vt_screen import VTScreen


binary, home = sys.argv[1:]
root = pathlib.Path(home).resolve(strict=True)
assert root.joinpath("owned-config-permissions-pty").read_bytes() == b"owned fixture only\n"
master, slave = os.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 38, 140, 0, 0))
os.set_blocking(master, False)
before = termios.tcgetattr(slave)
screen = VTScreen(140, 38)
captured = bytearray()
deadline = time.monotonic() + 35
phase = "startup"
child = None
settings = None
cleanup_term_attempted = False
business_passed = False
failure = None
failure_traceback = None
controls = []
inputs = []
success_receipt = None


def received_term(signum, frame):
    raise RuntimeError("owned Python supervisor received TERM")


signal.signal(signal.SIGTERM, received_term)


def setup_child():
    os.setsid()
    fcntl.ioctl(slave, termios.TIOCSCTTY, 0)


def check_deadline(label):
    if time.monotonic() >= deadline:
        raise AssertionError("business deadline in " + label)
    if child.poll() is not None:
        raise AssertionError("owned helper exited during " + label + ": " + str(child.returncode))


def pump():
    if select.select([master], [], [], min(.04, max(0, deadline - time.monotonic())))[0]:
        try:
            data = os.read(master, 65536)
        except OSError as error:
            if error.errno not in (errno.EAGAIN, errno.EWOULDBLOCK, errno.EIO):
                raise
        else:
            if len(captured) + len(data) > 2 * 1024 * 1024:
                raise AssertionError("bounded PTY output budget exceeded")
            captured.extend(data)
            screen.feed(data)


def rendered_rows():
    return ["".join(line).rstrip() for line in screen.lines]


def visible(text):
    return any(text in row for row in rendered_rows())


def marked(text, marker):
    # LocalPanel's visible '> ' marker is its focus/selection contract. Its
    # hardware cursor is nil; do not mistake the last VT cursor for row focus.
    for row in rendered_rows():
        start = row.find(text)
        if start >= 0 and row[start:] == text and row[:start].strip() == marker:
            return True
    return False


def prompt(text=""):
    expected = ">" + (" " + text if text else "")
    return "".join(screen.lines[screen.row]).rstrip() == expected and screen.col == 2 + len(text)


def frame():
    return {
        "width": screen.width, "height": screen.height,
        "row": screen.row, "column": screen.col,
        "rawOffset": len(captured), "renderedRows": rendered_rows(),
    }


def wait(predicate, label):
    while True:
        # A just-arrived true predicate cannot turn a late result into PASS.
        check_deadline(label)
        if predicate():
            return
        pump()


def send(data, reason, enter=None):
    assert 0 < len(data) <= 4096 and b"\n" not in data
    if b"\r" in data:
        assert data == b"\r", "Enter is a separate observed local-action input"
        if enter == "local-/config":
            assert prompt("/config"), "Never submit ordinary composer text"
        elif enter == "config-search-accept":
            assert marked("Search settings… permission", ">")
            assert visible("Default permission mode: default")
        else:
            raise AssertionError("unapproved Enter context")
    inputs.append({
        "phase": phase, "hex": data.hex(), "reason": reason,
        "enterIntent": enter, "rawOffsetBefore": len(captured),
        "monotonic": time.monotonic(),
    })
    view = memoryview(data)
    while view:
        check_deadline("keyboard write")
        if select.select([], [master], [], min(.04, max(0, deadline - time.monotonic())))[1]:
            try:
                count = os.write(master, view)
            except BlockingIOError:
                continue
            if count <= 0:
                raise AssertionError("owned PTY write made no progress")
            view = view[count:]
        pump()


def read_json(path):
    resolved = path.resolve(strict=True)
    if root not in resolved.parents or not stat.S_ISREG(resolved.stat().st_mode):
        raise AssertionError("JSON path escaped the owned fixture HOME: " + str(path))
    if resolved.stat().st_size > 64 * 1024:
        raise AssertionError("owned JSON exceeded its finite byte budget: " + str(path))
    with resolved.open("rb") as stream:
        encoded = stream.read(64 * 1024 + 1)
    if len(encoded) > 64 * 1024:
        raise AssertionError("owned JSON grew beyond its finite byte budget: " + str(path))
    value = json.loads(encoded)
    if not isinstance(value, dict):
        raise AssertionError("owned JSON has an invalid object shape: " + str(path))
    return value


def wait_file(path, label):
    wait(path.exists, label)
    return read_json(path)


def wait_settings(predicate, label):
    observed = None

    def matches():
        nonlocal observed
        observed = read_json(settings)
        return predicate(observed)

    wait(matches, label)
    return observed


def note(name, data):
    observation = frame()
    controls.append({"name": name, "observed": data, "monotonic": time.monotonic(), **observation})
    root.joinpath("permissions-controls.json").write_text(json.dumps(controls, indent=2) + "\n")


try:
    child = subprocess.Popen(
        [binary, "-test.run=^TestNativeCLIConfigPermissionsRestorePTYFixtureProcess$", "-test.count=1"],
        stdin=slave, stdout=slave, stderr=slave, env=os.environ.copy(),
        preexec_fn=setup_child, close_fds=True,
    )
    wait(lambda: visible("Claude Code") and prompt(), "actual RunCommand startup and empty prompt")
    paths = wait_file(root.joinpath("permissions-paths.json"), "owned helper paths")
    settings = pathlib.Path(paths["settings"]).resolve(strict=True)
    if root not in settings.parents:
        raise AssertionError("settings path escaped the owned fixture HOME")
    expected = {
        "permissions": {"allow": ["Read(owned/**)"], "future": {"keep": ["nested", 7]}},
        "futureUser": {"keep": True},
    }
    assert paths["original"] == expected and read_json(settings) == expected
    note("actual-startup-empty-prompt", True)
    phase = "config-entry"
    send(b"/config", "type local Config command without Enter")
    wait(lambda: prompt("/config"), "current composer Config text and cursor")
    note("local-config-command-before-enter", True)
    send(b"\r", "submit only observed local Config command", enter="local-/config")
    wait(lambda: visible("Search settings") and visible("Default permission mode: default"), "actual current Config entrance")
    note("config-visible", True)
    phase = "permission-search"
    send(b"/permission", "type actual Config search without Enter")
    wait(lambda: marked("Search settings… permission", ">") and visible("Default permission mode: default"), "current focused permission search and default row")
    note("permission-search-focused-default-visible", True)
    send(b"\r", "accept current Config search and leave its focus", enter="config-search-accept")
    wait(lambda: marked("Search settings… permission", "") and marked("Default permission mode: default", ">"), "search focus exited and default row selected before Right")
    note("search-focus-exited-default-row-selected", True)
    phase = "actual-setting-change"
    send(b"\x1b[C", "cycle selected default permission row once to plan")
    wait(lambda: marked("Default permission mode: plan", ">"), "current changed Config row")
    changed = wait_settings(lambda data: isinstance(data.get("permissions"), dict) and data["permissions"].get("defaultMode") == "plan", "physical plan write")
    assert changed["permissions"]["allow"] == ["Read(owned/**)"]
    assert changed["permissions"]["future"] == {"keep": ["nested", 7]}
    assert changed["futureUser"] == {"keep": True}
    root.joinpath("permissions-plan-observed.json").write_text(json.dumps(changed, indent=2) + "\n")
    note("plan-visible-and-physically-saved", changed)
    phase = "escape-restore"
    send(b"\x1b", "Escape actual Config and restore its initial settings")
    wait(prompt, "Escape returned actual empty composer prompt")
    restored = wait_settings(lambda data: data == expected, "Escape must delete nested defaultMode while retaining literal allow/unknown fields")
    assert "defaultMode" not in restored["permissions"]
    root.joinpath("permissions-restored-observed.json").write_text(json.dumps(restored, indent=2) + "\n")
    note("escape-physical-exact-original-and-empty-prompt", restored)
    phase = "natural-exit"
    send(b"\x04", "first Ctrl-D at observed empty prompt")
    wait(lambda: any(row == "Press Ctrl-D again to exit" for row in rendered_rows()), "real current prompt exit acknowledgement")
    note("current-ctrl-d-confirmation", True)
    send(b"\x04", "second Ctrl-D confirms natural application exit")
    receipt = wait_file(root.joinpath("permissions-receipt.json"), "RunCommand natural-exit receipt")
    physical_restored = termios.tcgetattr(slave) == before
    assert receipt["terminalRestored"] is True and physical_restored
    assert receipt["unknownFieldsPreserved"] is True
    assert receipt["modelPOSTRequests"] == 0 and receipt["otherRequests"] == 0
    assert receipt["exitCode"] == 0
    root.joinpath("permissions-terminal-release").write_text("physical slave inspected\n")
    while child.poll() is None:
        if time.monotonic() >= deadline:
            raise AssertionError("natural helper exit exceeded business deadline")
        pump()
    assert child.returncode == 0
    note("natural-helper-zero-and-two-termios-checks", True)
    business_passed = True
    success_receipt = {"qualified": True, "physicalTerminalRestored": physical_restored, "cleanupTERMAttempted": False, **receipt}
except BaseException as error:
    failure = repr(error)
    failure_traceback = traceback.format_exc()
    raise
finally:
    cleanup_errors = []
    descriptor_closes = []
    # Snapshot while the private PTY and settings still exist. Go's TempDir may
    # be removed as soon as this child finishes; the bounded stderr copy lasts.
    observation = frame()
    try:
        terminal_after = repr(termios.tcgetattr(slave))
    except BaseException as error:
        terminal_after = {"readError": repr(error)}
    try:
        physical = read_json(settings) if settings is not None else {"notObserved": True}
    except BaseException as error:
        physical = {"readError": repr(error)}
        # A parse/shape error is itself failure; retain finite actual bytes so
        # that it cannot erase the only observation before TempDir removal.
        if settings is not None:
            try:
                resolved = settings.resolve(strict=True)
                metadata = resolved.stat()
                if root not in resolved.parents or not stat.S_ISREG(metadata.st_mode) or metadata.st_size > 64 * 1024:
                    raise AssertionError("failed settings snapshot is outside its owned finite file contract")
                with resolved.open("rb") as stream:
                    physical_raw = stream.read(64 * 1024 + 1)
                if len(physical_raw) > 64 * 1024:
                    raise AssertionError("failed settings snapshot grew beyond its finite byte budget")
                physical.update(rawBytes=len(physical_raw), rawSHA256=hashlib.sha256(physical_raw).hexdigest(), rawPrefix=physical_raw[:2048].decode("utf-8", "replace"), rawPrefixTruncated=len(physical_raw) > 2048)
            except BaseException as snapshot_error:
                physical["rawSnapshotError"] = repr(snapshot_error)
    # On failure close both owned PTY references BEFORE joining the helper.
    # This permits actual EOF/HUP; any such exit remains failure cleanup, never
    # a Ctrl-D/natural-close PASS. The Popen handle is the exact direct child.
    for name, descriptor in (("master", master), ("slave", slave)):
        try:
            os.close(descriptor)
            descriptor_closes.append(name)
        except BaseException as error:
            cleanup_errors.append(name + " close: " + repr(error))
    if child is not None and child.poll() is None:
        cleanup_term_attempted = True
        try:
            child.terminate()
        except ProcessLookupError:
            pass
        except BaseException as error:
            cleanup_errors.append("direct child TERM: " + repr(error))
        try:
            child.wait(timeout=5)
        except subprocess.TimeoutExpired:
            cleanup_errors.append("owned helper did not close within five seconds after its single TERM")
        except BaseException as error:
            cleanup_errors.append("direct child wait: " + repr(error))
    diagnostic = {
        "businessPassed": business_passed, "failure": failure, "phase": phase,
        "pid": child.pid if child is not None else None,
        "returnCode": child.returncode if child is not None else None,
        "directChildOnly": True, "cleanupTERMAttempted": cleanup_term_attempted,
        "cleanupErrors": cleanup_errors, "descriptorCloseOrder": descriptor_closes,
        "ptyClosedForFailure": not business_passed,
        "closureByEOFOrHUPIsNaturalEvidence": False,
        "controlCount": len(controls), "inputCount": len(inputs),
        "termiosBefore": repr(before), "termiosAfterBeforeDescriptorClose": terminal_after,
        "currentFrame": observation, "physicalSettings": physical,
        "rawBytes": len(captured), "rawSHA256": hashlib.sha256(captured).hexdigest(),
    }
    try:
        root.joinpath("permissions-terminal.txt").write_bytes(captured)
        root.joinpath("permissions-inputs.json").write_text(json.dumps(inputs, indent=2) + "\n")
        root.joinpath("permissions-driver-terminal.json").write_text(json.dumps(diagnostic, indent=2) + "\n")
    except BaseException as error:
        cleanup_errors.append("owned diagnostic write: " + repr(error))
    if not business_passed or cleanup_errors or cleanup_term_attempted:
        diagnostic["recentControls"] = [
            {key: value for key, value in item.items() if key != "renderedRows"}
            for item in controls[-12:]
        ]
        diagnostic["inputs"] = inputs[-16:]
        diagnostic["rawTailHex"] = bytes(captured[-512:]).hex()
        diagnostic["traceback"] = (failure_traceback or "")[-4096:]
        physical_json = json.dumps(physical, ensure_ascii=False, separators=(",", ":")).encode()
        if len(physical_json) > 4096:
            diagnostic["physicalSettings"] = {
                "portableSnapshotTruncated": True, "jsonBytes": len(physical_json),
                "jsonSHA256": hashlib.sha256(physical_json).hexdigest(),
                "jsonPrefix": physical_json[:2048].decode("utf-8", "replace"),
            }
        encoded = json.dumps(diagnostic, ensure_ascii=False, separators=(",", ":"))
        # Maximum frame is 38x140 cells; nevertheless cap the portable stdout
        # copy without removing the core screen/settings/phase observations.
        if len(encoded.encode()) > 24 * 1024:
            diagnostic.pop("recentControls", None)
            diagnostic.pop("rawTailHex", None)
            diagnostic["portableOptionalDetailsOmitted"] = True
            encoded = json.dumps(diagnostic, ensure_ascii=False, separators=(",", ":"))
        if len(encoded.encode()) > 24 * 1024:
            diagnostic.pop("traceback", None)
            diagnostic.pop("inputs", None)
            diagnostic["portableOptionalDetailsOmitted"] = True
            encoded = json.dumps(diagnostic, ensure_ascii=False, separators=(",", ":"))
        if len(encoded.encode()) > 24 * 1024:
            # Finite current rows are still explicit evidence: report omitted
            # suffix cells rather than allowing an unbounded failure output.
            diagnostic["currentFrame"]["renderedRows"] = [row[:80] for row in observation["renderedRows"]]
            diagnostic["portableFrameColumnsTruncated"] = 80
            encoded = json.dumps(diagnostic, ensure_ascii=False, separators=(",", ":"))
        if len(encoded.encode()) > 24 * 1024:
            # A pathological error string must not displace the actual frame
            # and private settings observation in Go's retained output tail.
            diagnostic = {
                "businessPassed": False, "phase": phase, "failure": (failure or "")[:512],
                "currentFrame": diagnostic["currentFrame"],
                "physicalSettings": diagnostic["physicalSettings"],
                "controlCount": len(controls), "inputCount": len(inputs),
                "cleanupTERMAttempted": cleanup_term_attempted,
                "cleanupErrors": [item[:256] for item in cleanup_errors[:4]],
                "descriptorCloseOrder": descriptor_closes,
                "portableDiagnosticTruncated": True,
            }
            encoded = json.dumps(diagnostic, ensure_ascii=False, separators=(",", ":"))
        assert len(encoded.encode()) <= 24 * 1024, "portable diagnostic byte cap"
        print("CONFIG_PERMISSIONS_PTY_FAILURE_DIAGNOSTIC=" + encoded, file=sys.stderr, flush=True)
    if cleanup_errors or cleanup_term_attempted:
        raise AssertionError("owned PTY did not satisfy natural-close contract: " + repr(cleanup_errors))

print(json.dumps(success_receipt))
