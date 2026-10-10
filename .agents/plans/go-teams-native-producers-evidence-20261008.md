# Native Go Teams producer/consumer evidence — 2026-10-08

This records the Teams implementation owned by the interactive inbox agent. It does not claim the entire Go migration or the whole repository gate is complete. Existing changes were preserved; no branch, commit, PR, real-home write or desktop-instance operation was performed.

## Installed producer graph

| Change and paths | Original Source executed | Physical Go evidence |
| --- | --- | --- |
| Strict/permissive inbox schemas and literal XML parser: `backend/internal/teamspawn/interactive_protocol.go`, `interactive_xml.go`, `interactive_inbox.go` | Original `teammateMailbox` parsers and complete `useInboxPoller` poll callback; permission requests remain permissive, permission/sandbox responses and team updates strict, request schemas strip unknowns. Original doubled-backslash UI regex behavior is preserved. | Actual mailbox files, owner queue/read-marker order, permission/plan replies and trusted shutdown. Source fixtures exercise malformed/unknown fields rather than copied parsers. |
| Interactive leader polling, pending/processed inbox and mid-turn attachments: `clibootstrap/native_team_leader_interactive.go`, `native_team_leader_cycle.go`, `native_team_leader_inbox.go`, `native_attachments.go`, `sessioncontroller/team_inbox.go`, `team_prompt.go` | Original callback runs with real mailbox/config/task stores. Full original `getTeammateMailboxAttachments` and `normalizeAttachmentForAPI` execute. | Busy real MainPrint receives mail independently of the active ASK; the subsequent real API request receives it once. Idle continuation uses the same Host and yields to queued human input. Structured protocol stays out of chat attachments. |
| Viewed and in-process own mailbox delivery: `clibootstrap/native_team_leader_interactive.go`, `native_team_inprocess_loop.go`, `native_team_inprocess_turn.go`, `sessioncontroller/inprocess_team_context.go`, `attachments/teammate.go`, `sdkhost/host.go`, `requestretry/open.go` | Original viewed-task selection and in-process ALS role, attachment creation, read markers, pending/processed transition and API XML. | Authenticated independent child reads its own physical file mid-turn; leader pending mail stays intact. Viewed-child mail has the same Source selection behavior. No identity flag manufactures another role. |
| Actual pane spawn/cleanup and ordinary MAIN worker: `teamspawn/pane.go`, `pane_spawn.go`, `pane_command.go`, `clibootstrap/native_team_stage.go`, `native_team_pane_spawn.go`, `native_team_pane_worker.go`, `native_team_pane_inbox.go`, `native_team_resources.go`, `native_team_stop_hooks.go`, `application/command.go` | Full original `handleSpawnSplitPane`, inherited CLI/env arguments, roster/task/mailbox mutations, tmux/iTerm backend methods and original worker poll callback. All OS argv, including the initial `tmux -V`, are compared. | Current cmd is really built and started in a private real tmux pane; initial physical inbox leads to a Write artifact; parent Close kills and joins it. Another real worker test covers mid-turn mail and permission request/response with forged sender rejection. Trusted shutdown kills only the leader-owned pane, not a body-selected victim. |
| Required in-process teammate plans: `builtintools/plan.go`, `builtintools/permission.go`, `clibootstrap/native_team_plan_approval.go`, `native_plan.go`, `native_plan_attachments.go`, `native_team_inprocess_spawn.go`, `native_team_inprocess_loop.go`, `native_team_inprocess_turn.go` | Original `ExitPlanModeV2Tool.call`, permission check and result mapping under actual teammate ALS; original leader approval handler. | Real child writes the plan file, sends a physical request, leader clears awaiting approval and writes the response, and a fresh real sidechain consumes it. Source leaves task permission mode as plan; Go preserves that behavior and parent bypass precedence. |
| Session/TUI queue and cancellation: `application/team_leader_inbox.go`, `team_inbox_local_input.go`, `session.go`, `skill_attachment_notice.go`, `tui/teammates.go` and corresponding TUI state/event bindings | Original Source UI inbox/XML, viewed task and lifecycle conditions. | Actual TUI with `agentTeamsEnabled: true`: busy footer, idle autonomous artifact, fresh teammate input, physical shell cancellation, permission approval, kill and Close join. |
| iTerm setup choice/installation/verification: `teamspawn/pane_setup.go`, `pane.go`, `pane_spawn.go`, `clibootstrap/native_team_pane_setup.go`, `native_team_stage.go`, `application/team_pane_setup.go`, `session.go`, `tui/session.go`, `model.go`, `render.go` | Entire original compiled React `It2SetupPrompt` body/callbacks, actual PM detection/install/verify functions and config saves. Only hook storage/input/render primitives are supplied. | Real TUI selects “Use tmux instead”, saves the original preference while preserving old unknown JSON, starts a true built Go CLI pane, writes the artifact and joins/kills on Close. Fake executables prove uv/pipx/pip/pip3 ordering, HOME installation cwd, install failures, API failure, retry, cancellation and success. No real install or real iTerm GUI was operated. |
| Ordinary Teams admission: `sdkhost/native_team_execution.go`, `builtintools/team.go`, `clibootstrap/main_print.go` | Original normal Teams tools/Agent/print execution; General opt-out and runtime opt-in consumers. | Old rejection test is replaced by the actual installed graph. Binding checks Owner/Controller/tools, full Agent producer, inbox/attachments and cleanup/task consumers. Worker/in-process identities cannot mint an ordinary leader proof. Real ordinary config true completes the original full runHeadless differential. |

Paths in table rows without `backend/internal/` prefix are relative to that directory. Shared files such as main_print.go/session.go/host.go contain other agents' independent changes; the table claims only the Teams hunks.

## CLI and guard boundaries

- Original `src/main.tsx` registers `--agent-teams` only in the literal false `"external" === 'ant'` branch. Original external Commander and Go reject it with exit 1 and identical stderr. The runtime raw-argv library probe still exists in Source, and was tested separately. No extra public flag was invented.
- `--effort`, `--plugin-dir`, hidden pane identity tuple, teammate mode and `--no-chrome` have actual consumers. Inherited command values match Source. Final Chrome false (`--chrome --no-chrome`) is accepted; final true remains guarded because that integration is outside this implementation.
- Original ant-only mid-turn attachment/footer condition remains. Tests that change runtime USER_TYPE after a valid external startup do not authorize unported ant startup or other private producers.
- Ordinary NativeTeamExecution refusal is removed only for the installed complete graph. Finite/unbound admission and typed-role guards remain. Sandbox and unrelated Chrome/startup producers are not opened by a Teams flag.
- Pane mode keeps the original launch snapshot. iTerm preference is read when original backend detection reads it; setup choices clear detection and re-detect.

## Deterministic results

All commands used `/tmp/go-current-test.ts`, which isolates HOME/config/Go caches and offline dependencies and invokes `go test -p=4 -race -count=1`. Source runs use pinned Bun 1.3.14 and guarded disposable homes.

Final broad pass:

`/tmp/go-teams-all-final-after-async-analytics-twentysixth-1791395436.log`

Pattern: `Team|Teammate|Pane|InProcess|InteractiveInbox|Plan|GuardedParse` across clibootstrap, application, teamspawn, sessioncontroller, tui, builtintools, sdkhost, sdkpermissions, attachments, cliargs. Results: clibootstrap 73.628s, application 31.283s, teamspawn 25.666s; all 10 packages passed. sdkhost had no matching direct tests in this pattern; its real Host is exercised through MainPrint/application.

Other preserved passes:

- `/tmp/go-team-complete-producers-consumers-source-physical-twelfth-1791390952.log`: inbox/viewed/worker/inprocess/required-plan and full original pane producers.
- `/tmp/go-team-ordinary-bindings-source-headless-fourteenth-1791391677.log`: ordinary config true, original runHeadless full physical comparison, own-child mail and required plans.
- `/tmp/go-teams-all-producer-consumer-source-physical-sixteenth-1791392019.log`: all existing bounded Teams/Plan Source + physical cases.
- `/tmp/go-teams-iterm-source-terminal-twentieth-1791393076.log`: true setup TUI→tmux→built CLI→Write→Close; PM subprocess Source differential.
- `/tmp/go-teams-iterm-actual-react-state-twentysecond-1791394328.log`: full original React setup callbacks/state transitions and global persistence.
- `/tmp/go-teams-iterm-no-python-and-chrome-optout-twentyfifth-1791395017.log`: no-Python install/retry/verify/cancel plus final Chrome opt-out.

Failed intermediate evidence was retained, not overwritten: 13th typed-role transition, 18th fixture missing hook export, 19th fixture global path/final newline differences, 21st test pointer declaration, 23rd concurrent queryhooks declarations, 24th concurrent analytics archive import. These failures were repaired or followed by the final passing broad run. The standalone Bun cliargs suite also had an independent beforeAll Go-hydration stdout-empty failure; its new parser behavior is independently covered by the passing Go test that invokes the complete original Commander oracle and actual runtime tool consumers.

`bun run check:impact` ran with exit 0: `/tmp/go-teams-check-impact-final-1791395437.log`. It reports the whole repository's selected lanes and PR blocked metadata because no allow-cli-core-change label is present. No PR/publishing operation was requested. Full repository verify/coverage/native packaging is Root's integration responsibility; this document does not claim those checks passed. `git diff --check` passed and status was reviewed. The new backend is untracked, so ordinary git diff omits it; owned files were reviewed directly and by Source/physical differential tests.

## Authorized real DeepSeek evidence

Private current binary built offline: `/tmp/go-teams-live-current`, build log `/tmp/go-teams-live-current-build-1791394347.log`.

Driver `/tmp/run-go-ordinary-teams-live.py` reads only the Root-authorized private key file inside the script and never prints it. It creates a new private HOME/work/provider index with DeepSeek anthropic protocol and nested tool-result media enabled; it does not read or mutate the real home or Root desktop profile.

Receipt: `/var/folders/sx/hl8zlmxd7gx9b58_1t2zhp5r0000gn/T/go-ordinary-teams-live-3z9lt_hu/receipt.json`

Log: `/tmp/go-ordinary-teams-deepseek-live-first-1791394539.log`

Observed: code 0, 25.929 seconds, physical artifact exact, team removed, final marker, real TeamCreate→Agent→child Write/SendMessage→leader Read→shutdown approval→TeamDelete. Two fresh real child transcripts; task_started 1 and completed task_notification 1. The model submitted early TeamDelete repeatedly; seven original success:false registered-teammate responses preserved the lifecycle boundary until the eighth returned success:true. An early Read before the file existed was a real model ordering error; the final read and artifact passed. Reported tool/model cost fields are Source accounting estimates, not a billing assertion. No Grok login or Grok quota was authorized or used.
