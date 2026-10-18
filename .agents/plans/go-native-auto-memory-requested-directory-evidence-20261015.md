# Native auto-memory requested directory evidence — 2026-10-15

This is a bounded ordinary launch-path repair, not whole migration acceptance. Real Claude OAuth login/refresh remains excluded.

The original Source `getClaudeConfigHomeDir()` retains a relative or explicitly empty `CLAUDE_CONFIG_DIR`. `getAutoMemPath()` uses that spelling in its prompt and caches the result by project root. Original filesystem calls resolve it against the process CWD. Native Go previously captured the physical absolute config directory, so the real model request differed even though it read the same physical `MEMORY.md`.

## Evidence

- Whole frozen `loadMemoryPrompt`, `getAutoMemPath`, `getMemoryFiles`, and `getClaudeMds` ran against actual private files. Six profiles: absent, absolute, relative, empty, decomposed Unicode, and relative parent. All exited 0 with empty stderr, zero blocked capabilities, and zero secure-store reads. Source files match HEAD bytes.
- Four original stages preserve the distinct lifetimes: first cold load; changed config directory with warmed path/files; path-cache clear retaining old instruction files; instruction-cache clear reading the new entrypoint. The merged settings object remains unchanged. `CLAUDE_CODE_DISABLE_AUTO_MEMORY=0` explicitly enables this corpus; it does not prove every default enable gate.
- A new public `ParseCommand` → `RunCommand` test performed exactly one actual loopback model POST per profile. It compares the complete original memory prompt and complete original file context, and verifies protected settings and entrypoint contents. Each profile retains its original 30-second context.
- Old production genuinely failed relative, empty, and relative-parent profiles; absent, absolute, and Unicode controls passed. There was no fixture setup failure, compile-only failure, timeout, or skip. Both full prompt and context assertions failed for the three affected profiles. Raw results are retained.
- After repair, the unchanged public assertions pass for all six profiles. New cache/physical-filesystem/finite-guard tests and fourteen existing memory/Agent/context tops also pass under `-race` and default vet.

## Repair

`MemoryPaths.NativeConfigDirectory` is bound only by the actual native factory and sampled only on a project-root memo miss. Finite callers keep their explicit absolute fallback. `nativeMemoryFiles` keeps requested paths in memory metadata and resolves relative strings only for actual read/stat/realpath/mkdir/readdir operations. It retains the original native checks and unsorted directory primitive. Ordinary startup settings guards, settings/memory memo identities, and persisted shapes are unchanged.

Changed production: `backend/internal/usercontext/normal_memory_paths.go`, `backend/internal/clibootstrap/native_prompt.go`, and new `backend/internal/clibootstrap/native_memory_files.go`. Earlier shared ConfigHome getter and canonical Git-root changes in those files were preserved.

## Executed checks

The private environment uses an independent HOME/TMPDIR and the already verified shared module cache. No ordinary race run used child-coverage opt-in.

1. `go test -p=1 -race -count=1 -json ./internal/application -run '^TestActualPublicAutoMemoryRequestedDirectoryAndFileContextAgainstWholeSource$'`: old exit 1, three passing controls / three failed profiles; repaired exit 0, seven passing test events, package 9.560 seconds.
2. `go test -p=1 -race -count=1 -json ./internal/usercontext ./internal/clibootstrap ./internal/application -run <exact receipt selector>`: exit 0, sixteen tops / thirty-eight passing test events, zero skips/failures. Package times 5.977 / 10.733 / 5.823 seconds. The receipt stores the complete selector and arguments.
3. `git diff --check` for owned files: passed. No full verification run was performed by this agent; Root owns the immutable full gate.

Raw logs, full Source/HTTP observations, exact selectors, input SHAs, and result files are in `artifacts/go-migration-20261013/auto-memory-directory-evidence-20261015/receipt.json`. This batch totals seventeen distinct Go tops / forty-five passing test events. Six standalone Source profiles are separately counted.

## Open boundaries

- Ordinary Main startup/runtime ConfigDir admission guard remains closed. This proof covers valid launch values, not an admitted runtime setting change.
- User `CLAUDE.md`/rules requested-path metadata and fresh directory sampling are not proved by this auto-memory corpus.
- Native factory warm/clear behavior after runtime ConfigDir mutation is still constrained by the ordinary settings guard; Source lifetime and the lower cache boundary are proved separately.
- Default auto-memory gates, remote overrides, and further instruction-source/flag sampling are not newly accepted here.
- Actual short MainCommand CLI HEAD parity and cold-title parity remain separate open work; in-process tests do not close those physical discrepancies.
