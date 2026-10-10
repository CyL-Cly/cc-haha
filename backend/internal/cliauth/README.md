This package implements general and OpenAI `auth status` output. The production
CLI now dispatches status and logout through `clibootstrap.MainCommand`, which
shares the native settings, global writer, secure store and OpenAI storage
producers. The general status and account text projections are compared with
the actual original handler in 40 profiles, plus 40 descriptor/CCR/managed-context profiles; logout state transitions are
compared in four profiles and exercised through real private files and the
ordinary compiled CLI. These commands do not create an ASK session.

CLI OAuth login/post-token installation remains pending. Status now consumes inherited credential descriptors, CCR file fallback and
managed-context source selection, with the original one-shot cache and private
0700/0600 persistence. Real compiled CLI tests cover inherited pipe descriptors.
Every startup flag and native platform have not been accepted as a complete
profile; the internal Homespace schema bootstrap remains guarded. Model-request
credential paths have their own separate acceptance scope. The OpenAI token/storage producers have their
own separately scoped Source tests; passing status is not proof of login.

The reference is the real command registration in `src/main.tsx`, the OpenAI
branch of `src/cli/handlers/auth.ts:authStatus`, and
`src/services/openaiAuth/storage.ts`. The parity lane first obtains options or
parser errors from the actual guarded Commander tree. It then calls the real
TS handler with those options and compares the Go handler after Cobra parsing.
The older isolated parity lane excludes global CLI initialization; the newer
native command tests exercise the real shared initialization separately.

`NewOpenAIStorage` takes a disposable/configured home and an explicit environment
getter. Its injected `ReadSecure` callback is the external storage boundary;
this component never invokes a keychain program. Selected token files take
precedence and never fall back, even when missing or invalid. Without a selected
file, the file-backed marker prevents secure fallback. Null reads are memoized
by the same file-path/secure-storage keys until `Clear` is called. The token
read API is used only by status; mutable cached JS object identity is not claimed.

Standard profiles compare stdout and stderr bytes, exit status, file/marker/
secure read calls, and cached transitions against two independent actual TS
runs. Additional native-process probes execute the real TS handler with its
stdout/stderr/exit functions intact and compare actual pipe bytes and OS status.
Credentials are fake; all fixture file paths are inside disposable homes.
Both implementations preserve the original `expiresAt > 0` check, including
JSON overflow to Infinity, negative zero and fractional date clipping. JSON
output retains lone UTF-16 units; text output replaces them at the UTF-8 boundary.

Malformed raw secure-storage values and out-of-range text dates retain the actual
TS name, message, partial output and stack as separate Source observations.
The Go result returns an explicit incomplete diagnostic instead of claiming
these cases are compatible. Queued file/secure read error-log diagnostics are
also retained as pending Source metadata; their storage/sinks are not migrated.
The ignored report is `artifacts/go-parity/cliauth/source-only-pending-diagnostics.json`.
Neither these observations nor passing finite cases prove complete CLI, Harness
or Benchmark equivalence.
