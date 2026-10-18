# Private physical subprocess coverage experiment — 2026-10-14

This experiment leaves the official collector, source, scope, denominator and 90% threshold unchanged. It uses immutable checkpoint04, which had failed unit tests and therefore provides diagnostic evidence only.

The exact build was `go build -cover -covermode=atomic -coverpkg=./...,go.mau.fi/libsignal/...,go.mau.fi/whatsmeow/... -tags native_process_fixture -o <private>/claude-haha-covered ./cmd/claude-haha`, on darwin/arm64 Go1.25.7. The approved shared compiler cache was reused and never cleaned/moved/deleted. Every child had disposable state, fake loopback model, CI=1, and its own GOCOVERDIR. The original physical exit driver was copied privately with only the coverage/CI environment injection; its original success assertions were unchanged.

Actual `/exit`, `/quit`, Ctrl-D and after-Read cases all passed, including exactly one SessionEnd hook, process exit, physical terminal restoration, and the Read case's two real loopback POSTs with Read output provenance. Raw coverage metadata/counters were produced by these real processes and converted using `go tool covdata textfmt`.

SHA256 identities:

| Evidence | SHA256 |
| --- | --- |
| Complete checkpoint04 source snapshot record | `171d9158afb666b2a0cb6812684121b4315ac453718f6e7667a709f4c509b442` |
| Instrumented actual CLI binary | `619c91add4198d14aeeb1d3c0be8f8e26880c4695e47b284ce502aa9ca30e82e` |

Every one of the snapshot's 5,739 source/dependency file hashes was verified before and after execution. Both default and tagged build inventories contain 2,598 files. The only substitutions are `main_auth_login_native.go` → `main_auth_login_fixture.go` and `main_native_default.go` → `main_native_process_fixture.go`, isolating external OS primitives. Fixture counters cannot replace default primitive coverage.

The strict analysis streams the 3.1GiB unit profile: 37,174,429 rows contain 180,633 distinct true compiler blocks. It admits physical counters only when the original production file SHA, compiler block start/end columns and statement count all match. **13,144 observed matching blocks** were admitted, with **zero rejections**. Twelve executed unmatched fixture blocks were withheld. The distinct executable-line sets and original changed-line denominator are identical before and after. The snapshot lacks remote refs, so changed lines use the verified original `origin/main` merge base `6b9d2aec1d9321a4dfff90d16b51f10c1ab984c8`; no branch or ref was changed.

| Diagnostic changed executable coverage | Before | Strict union |
| --- | ---: | ---: |
| Covered lines | 259,835 | 259,932 |
| Executable denominator | 357,989 | 357,989 |
| Percent | 72.581839% | 72.608935% |

The **97 new lines** mainly belong to TUI run (29), exit (18), public command main (12), CLI locks (7) and worktree exit (6). This sample adds **zero MainPrint or SDKHost lines**: these ordinary exit/Read consumers were already covered by direct unit consumers. It proves that some genuine child evidence is missing, but does not explain the outstanding MainPrint/SDKHost debt or approach 90%.

Compiler declaration-only admission was not rerun in this private analysis; its unchanged unmapped-declaration diagnostics are listed in the raw result. The unit failure, declaration admission and coverage shortfall prevent treating either profile as a formal passed gate. Broad parity/property consumers remain a separate investigation if the root elects to expand collection.

Raw build arguments, inventories, target/toolchain, profile hashes, physical receipts, private counter directories and strict merged/normalized profiles are preserved under `artifacts/go-migration-20261013/cli-flags/subprocess-cover/`; `experiment.json` and `analysis.json` are the complete machine-readable receipts. No counter fabrication, synthetic traversal, source exclusion, threshold decrease, or official collector edit was performed.
