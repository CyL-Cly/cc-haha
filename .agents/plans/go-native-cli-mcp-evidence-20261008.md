# Native CLI MCP management evidence — 2026-10-08

Scope: public Go CLI `mcp add`, `add-json`, `remove`, `list`, `get`,
`reset-project-choices`, and `add-from-claude-desktop`. This is not evidence that
every MCP command or the complete Go migration is finished.

The public entry uses `MainCommand` and its same process settings, global config,
secure storage, plugin options, plugin MCP, configuration service/editor and
runtime. It constructs no MainPrint, model loop, second MCP registry or session.
`main.go` only adds a narrow MCP dispatch. `main_native_process_fixture.go` adds
unavailable Keychain IO under its existing test-only build tag; release builds
continue using the actual native boundary.

Implemented behavior:

- Original scope/transport validation and environment/header parsing, including
  ordering, duplicate overwrite, JavaScript whitespace, prototype assignment,
  URL warning, ignored stdio OAuth flags, callback integer-prefix parsing and
  secret-before-config-write ordering.
- Original shared schema/policy/editor persistence for local/user/project; real
  conflict detection, mutation and OAuth cleanup. Unknown global fields remain.
- Real HTTP/stdio initialize handshake and actual scope/status/detail output;
  bounded parallel list health checks and connection shutdown after the command.
- Project approval/rejection reset through the actual shared global updater.
- Original macOS/WSL desktop config path discovery and stdio-only schema;
  ordered selection defaults, duplicate-name suffix and sequential mutation.
  The native terminal supports selection, confirm and cancel. It restores its
  original terminal configuration and reports restore errors.

Passed deterministic checks (all private HOME/config/work, no external models,
  accounts, public network, real Keychain or public telemetry):

| Check | Evidence |
| --- | --- |
| 22 parsing primitive profiles against actual Source | included in combined22/six-package23 |
| 17 original CLI handler profiles, exact stdout/stderr and persisted original mutation arguments | `/tmp/go-cli-mcp-handler-source-tenth.log`, PASS12.826s |
| 13 original Desktop reader profiles and 4 actual React dialog selection/mutation profiles | `/tmp/go-cli-mcp-desktop-source-thirteenth.log`, PASS1.562s |
| 23 original token-source/eligibility profiles | `/tmp/go-cli-mcp-eligibility-source-eighteenth.log`, PASS1.871s |
| Actual shared bootstrap, physical local/user/project/unknown fields/reset and HTTP OAuth cleanup | `/tmp/go-cli-mcp-real-native-seventh.log`, PASS3.468s |
| Actual public Go binary, two stdio handshakes + SIGINT shutdown; actual PTY import/confirm/suffix/cancel | `/tmp/go-cli-mcp-public-stdio-pty-twentyfirst.log`, PASS5.005s |
| Owned MCP combined Source/race | `/tmp/go-cli-mcp-owned-combined-source-race-twentysecond.log`, mcpcli2.493s/cmd22.424s |
| Full six-package Source/race | `/tmp/go-cli-mcp-shared-six-packages-source-race-twentythird.log`, mcpcli4.393s/mcpconfig3.597s/mcpauth46.420s/mcpruntime3.218s/cmd31.399s; mcpapi has no test files |
| Six-package vet | `/tmp/go-cli-mcp-six-packages-vet-twentyfourth.log`, exit0 |
| Whole checkout whitespace | `git diff --check`, exit0 |

Source handler extraction substitutes mutation/health/exit IO seams; it is not a
Source filesystem/network integration test. Actual Go tests prove those physical
effects separately and retain existing configuration/auth Source comparisons.
The Desktop React test substitutes renderer/hooks, while the public native PTY
test proves keyboard/terminal behavior. On BSD, PENDIN is kernel input-queue
bookkeeping; the terminal test compares every configured flag/control character
against the actual original `readClientSecret` restoration, exempting only that
queue flag. It does not read/discard pending user input to force a match. Initial
PTY detachment/PENDIN assertion failures remain in logs14–20.

Incomplete, with explicit production capability errors retained:

- `mcp serve`: native MCP server request dispatch and the original ordinary tool
  context/permission/cache/lifetime have not been bound.
- Eligible Claude.ai organization MCP fetch and authenticated proxy transport.
  Ordinary ineligible accounts work; an eligible account is not silently an
  empty configuration list. OAuth token file-descriptor source remains guarded.
- XAA setup/login/status/logout and their native CLI interaction; plain add's
  XAA fail-fast validation does not close those commands.
- Secret TTY Unicode/backspace/caller-cancellation boundaries need more proof.
- The first failing `list` bare/SIMPLE/disabled-auth branch test shows one secure
  read before eligibility. Source requires zero; fix awaits the current signed
  candidate build freeze release. Failure is preserved in
  `/tmp/go-cli-mcp-bare-secure-failure-first-twentyfifth.log`. That new test is not
  included in the preceding six-package passing snapshot.

Existing signed package/UI/model receipts have their own binary SHA and must
not be reused as proof of these later source changes. A separate ordinary CLI
official-model receipt is recorded below; no signed desktop MCP proof is claimed.

Adjacent integration regression investigated: old public Write/max-turn tests
failed with ErrRestricted before the HTTP request. Root/Cloud owner found and
fixed the inactive Bedrock callback's guard order. No old fixture/profile or
SDK/media guard was weakened. Original two Write tests plus four public-media
and error-lifetime tests passed in
`/tmp/go-public-write-media-owner-regression-second.log`, clibootstrap6.599s.

## Follow-up fixes and final deterministic snapshot

The two new failing tests are now repaired. The shared CLI selection evaluates
the actual bare/SIMPLE/forced-token/disabled branches before secure storage IO.
All four profiles prove zero Keychain reads. The forced-token fixture separately
disables unrelated first-party managed-policy prefetch through its existing
explicit local skip setting; its missing producer remains guarded.

Native raw-secret input now retains UTF-16 units through a JSON string callback
and `mcpauth.SaveClientSecretJSON`, using the same secure `Change` writer. The
existing string callback/wrapper remains compatible. This avoids discarding a
lone high surrogate left by emoji + DEL before saving credentials. Original
Source `saveMcpClientSecret` and its actual fallback filesystem are compared
against six native physical credential updates, preserving unrelated and future
fields. Failure-before-fix log30 demonstrates the actual lossy file values;
log31 is green. Real original Source/native PTYs also compare Chinese + DEL,
emoji + DEL and Ctrl-C, with no input echo; log32 is green (cmd6.047s).

- `/tmp/go-cli-mcp-secret-raw-source-repair-first-thirtyfirst.log`: mcpauth1.868s,
  mcpcli1.844s/cmd15.958s.
- `/tmp/go-cli-mcp-final-six-source-race-thirtythird.log`: complete packages PASS,
  mcpcli3.744s/mcpconfig2.877s/mcpauth35.011s/mcpruntime2.679s/cmd28.291s;
  mcpapi has no test files. Includes fresh public binary stdio+import PTY, all
  old MCP tests, 25 eligibility profiles, 17 handler profiles, raw-credential and
  terminal regressions.
- `/tmp/go-cli-mcp-final-six-vet-thirtyfourth.log`: exit0.
- `git diff --check`: exit0.

The previous bare/Unicode/lone-unit failures are retained as evidence. This
snapshot is after signed P245; P245 must not be used to claim these fixes.
Caller-context cancellation while blocked on raw secret input remains unproven;
the actual Ctrl-C path is verified. `serve`, eligible Claude.ai organization
transport, OAuth file descriptor, and XAA commands remain unimplemented with
specific guards.

## Official DeepSeek ordinary CLI and cold resume

After deterministic checks passed, two separately authorized official DeepSeek
turns used `https://api.deepseek.com/anthropic`, logical `deepseek-flash[1m]`.
The binary is a fresh ordinary CLI build with `native_process_fixture`, SHA256
`90b6f26942df3aeaa825ddcf8b01b5c6925eea4638a9660249ea51d5f20fc7e4`.
Only external native Keychain/managed policy are isolated; the actual shared
Harness, MCP discovery/permission/tool dispatch, subprocess and physical writer
run unchanged. All HOME/config/work/state are private.

- Public `mcp add-json` really saves the private stdio peer in user scope.
- Cold normal model call chooses `mcp__private__native_probe`, supplies the nonce,
  and the actual peer writes a 0600 artifact. Model exits0 after2turns with
  `MCP_REAL_E2E_OK`.
- A fresh CLI process resumes exactly session
  `1989107b-694c-474c-90bc-c19e50861634`. The prompt asks for the previously used
  nonce without restating it. The model recovers it from canonical history and
  really invokes the same private MCP tool, exits0 after2turns with
  `MCP_RESUME_REAL_OK`.
- Across the two processes: two actual initialize handshakes, two real tool
  calls, two actual SIGINT subprocess shutdowns, one same-SID canonical file.
  Artifact contents match `MCP_OFFICIAL_REAL_C29F`.

Private originals:
`/private/var/folders/sx/hl8zlmxd7gx9b58_1t2zhp5r0000gn/T/go-mcp-official-zziixg13/`
(`receipt.json`, `resume-receipt.json`, model/resume stdout and stderr, peer
journal and canonical file). The credential is read only into the child
environment and is neither echoed nor exported.

Sanitized public evidence:
`artifacts/go-migration-integration/cli-mcp-official-cold-resume-20261008.receipt.json`
and sibling `.peer.jsonl`. These are standalone CLI evidence, not a P245/P248
signed desktop receipt and not an actual public MCP provider/account test.
