# Computer Use language kernel

The Go executable owns the original Acorn module compiler, persistent-cell
bookkeeping, subprocess protocol, resource limits, and sandbox lifecycle. The
embedded Acorn version remains 8.16.0. Compilation transforms are implemented
in Go and compared against the original `compileReplCell` with UTF16 offsets.

`assets/api.js` and `assets/realm.js` are pure guest-language functions derived
from `src/vendor/computer-use-mcp/replApi.ts` and the realm template in
`src/utils/computerUse/replWorker.ts`. They contain no Node imports or business
runtime launcher. Production embeds them in the Go binary; Source Bun runs only
as an isolated deterministic test oracle. `sandbox-base.sb` preserves the fixed
process/IPC/sysctl rules of the repository's sandbox-runtime revision. Runtime
read access is limited to the canonical executable, disposable directory, and
the original system paths; there is no network/Unix socket/PTY allowance.

The hidden `--computer-use-repl-worker` route precedes CLI/bootstrap state reads.
The worker exposes JSON primitives/guest objects and native app RPCs only. Its
private compiler callable is never available through guest `eval` or function
constructors. The JSON parser preserves lone UTF16 units which Goja's default
decoder replaces. Code generation, host reflection, filesystem, process,
modules, and networking are unavailable to submitted cells.

Source/compiler/worker/sandbox regression evidence is scoped. Complete engine
equivalence has not been established: Goja does not yet support async generators,
and modern JSON reviver context and all malformed-runtime diagnostics still
need their complete Source corpus. Do not treat the common-cell tests as a
complete migration or remove the existing full-catalog guard on this evidence.
