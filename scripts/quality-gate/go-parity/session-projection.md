# Session metadata projection migration boundary

`backend/internal/sessionprojection` ports the reduction in
`src/server/services/sessionService.ts:getMetadataProjection` (line 1330) and
the already-located persisted branch of `getSessionLaunchInfo` (line 4760).
It uses the production Go `sessionmetadata.Reader` and an explicitly supplied
shared `sessionstore.Budget`; it does not execute TypeScript business code.
The same Source projection supplies `scanSessionListSummary`, `getCustomTitle`
and `getSessionWorkDir`. Their locator, index and in-memory/side-chat branches
remain separate integration work.

Construction requires a real reader, shared budget, config-scope callback,
working-directory callback, explicit platform, date parser and stat adapter.
The adapter supplies the actual decimal `dev:ino:size:mtimeNs` signature plus
birthtime/mtime ISO strings. A native birthtime adapter is still required in
the Server bootstrap; this component does not invent a default birthtime.
The oracle obtains birthtime from the same private file's independent native
stat, never from a Source projection or Source-parsed timestamp.

The reducer preserves Source complete-line summary versus valid-EOF launch
counts, title precedence and UTF16 slicing, runtime/permission/effort changes,
unknown repository/worktree properties and their order, and the 128 KiB
serialized metadata budget. The original JSONL bytes and format are unchanged.
Its 32-entry cache and shared in-flight build retain Source's config/file scope
and signature identity. Source excludes the logical project argument from
that identity and includes a `signature` property only on cache hits; these
observable distinctions are deliberately retained. As in Source, the shared
metadata build is not cancelled by an individual consumer.

The bundled independent Go date parser supports its declared ISO date-only
and explicit-offset date-time forms, plus the explicit GMT/UTC/numeric-offset
RFC layouts in `date.go`. The corpus compares actual Source behavior for
valid, invalid and equal-offset ISO boundaries and explicit RFC examples.
Legacy date syntax, local-timezone parsing and the rest of Bun `Date.parse`
remain unfinished full-migration work. They return `ErrUnsupportedDate` rather
than using Source-parsed milliseconds or silently dropping a candidate.
The Go path API accepts valid Unicode strings, including control characters;
invalid UTF-8 byte paths are explicitly rejected. Lone UTF16 path components
and additional pathological desanitization profiles are not proven by this
corpus. Transcript title and nested JSON UTF16 units are preserved separately
through the shared protocol helpers.

`session-projection.test.ts` runs the actual Source `SessionService` twice on
the same private file, then compares the Go implementation. Ordinary profiles
compare exact serialized bytes, sequential filesystem operation records and
final file contents. Public persisted launch profiles compare results and
file effects while retaining Source's additional locator/existence traces in
`artifacts/go-parity/session-projection/persisted-caller-traces.json`; the Go
component starts after lookup. Concurrent callers compare every exact native
operation record and count, require one shared scan, and retain all raw traces;
parallel stat completions have no guaranteed total order. The fixtures deny
Source network/native/credential capabilities before production imports and
validate disposable HOME/config, known input/output paths and canonical
ancestors. Both runtimes reject leaf/directory symlink escapes, including a
missing input beneath an external directory symlink, without changing private
sentinels.

This evidence does not complete session discovery, SQLite index/history
projection, session HTTP routing, CLI resume reconstruction or desktop/Agent
lifecycle integration. Source exception stack formatting, other native OS
matrices and untested ordinary-JSON nesting profiles remain pending. No live
provider, user configuration, keychain, branch or commit is needed for these
checks.
