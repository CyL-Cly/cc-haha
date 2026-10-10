# Collaboration ReadSession completion-order repair — 2026-10-08

Root Verify272 observed `TestActualDesktopCollaborationToolsCreateReadSendWaitAndExecuteSharedChild` fail at its original line74: ReadSession did not read actual child history, after0.40s. This repair preserves the original business contract and strengthens the physical test; it does not add sleeps, a production read join, implicit flush or fabricated history.

Original source boundaries reviewed:

- `src/server/services/sessionCollaborationService.ts` CreateSession calls actual session create, records assignment, dispatches with pump and returns describeCreation's current delivery/state. It does not wait for the model task to finish.
- ReadSession calls the dependency's current bounded history page. `src/server/services/sessionCollaborationHost.ts` binds that directly to `sessionService.getSessionHistoryPage(..., projectContext:false)`; `src/server/services/sessionService.ts` reads the current durable file. Reading a session is a snapshot operation, not a task join.
- `src/server/services/sessionCollaborationHost.ts` updates terminal collaboration state only on actual SDK result; provider bytes, receipt or request completion are not that event.
- QueryEngine checkpoint/transcript persistence and desktop SDK eager-flush environment precede actual SDK result in the original producer paths. A model peer merely writing final SSE still has not crossed the CLI consumer, Record/checkpoint or result boundary.

The old test closed childCompleted inside the fake provider HTTP handler immediately after writing final SSE. Parent ReadSession could then run before CLI processing/persistence, so the transport-level channel did not establish the precondition the test claimed. Its assertion searched the entire next parent request, which also contains automatic child-completion notifications and could hide an empty ReadSession result.

Changed paths:

- `backend/internal/application/session_collaboration_integration_test.go`: keeps actual NewDesktopServer, private provider HTTP, native MainPrint parent/child, actual List/Create/Read/Send/Wait tools, WebSocket projections, real child Write/Read artifact, state file and server Close. Parent extracts the SID from the actual CreateSession tool result. It uses real WaitSessions snapshots and returned revision, proceeding to ReadSession only after that target's actual completed state. Read assertion now inspects only the ReadSession tool_result.messages for the actual child final answer. No broad-body completion-notification false positive remains.
- The child final response is held until the parent consumes a real target nonterminal WaitSessions snapshot; that gate guarantees the regression exercises waiting even on a fast loopback server. A counter must prove this branch occurred. The gate uses channels and the original business protocol, with no elapsed-time assumption or sleeps. Failure/capacity-blocked states still fail the test.
- `backend/internal/sessioncollaboration/source_test.go` and `scripts/quality-gate/go-parity/native-collaboration.fixture.ts`: add a transport dependency historyCommit operation and original-service scenario. Source and Go read the empty snapshot after dispatch, still do not invent transcript records from a completed status, and see the final answer only when the dependency's durable snapshot contains it. Original production Source/service algorithms are unchanged.

No production Go, SDK host, shared constructor, account/config/credentials, model quota, IM adapter or guard was changed in this repair.

Observed commands and results, unique logs retained:

- Root Verify272 original failure retained by root; not reclassified as a production test pass.
- PASS `/tmp/go-collaboration-read-order-first-1791412246.log`, old focused single run application4.626s. PASS old focused repeat20 `/tmp/go-collaboration-read-order-red-repeat-second-1791412313.log`, application59.112s. These intermittent passes do not disprove the wrong completion milestone seen in Verify272.
- PASS `/tmp/go-collaboration-authoritative-wait-third-1791412382.log`, first actual-wait test application9.157s. Sessioncollaboration package compiled but that initial filter selected no Source test; not claimed as Source evidence.
- PASS `/tmp/go-collaboration-state-history-strong-fourth-1791412428.log`: stronger ReadSession-specific messages assertion repeat10 application41.538s; actual original complete collaboration Source lifecycle/history comparison repeat10 sessioncollaboration111.505s, including the new snapshot/commit scenario.
- PASS `/tmp/go-collaboration-history-original-source-fifth-1791412484.log`: actual original full Source collaboration leaf13.290s plus server credential/revocation/lifecycle2.261s.
- PASS `/tmp/go-collaboration-held-child-real-wait-sixth-1791412537.log`: controlled child final held until nonterminal wait, physical application repeat10 40.496s.
- PASS `/tmp/go-collaboration-held-target-state-final-seventh-1791412630.log`: final counter requires the actual target member (not merely a members property), physical application repeat10 39.335s. ReadSession must contain the real child's committed final message; artifact, five tool projections, send/wait and Close assertions remain.

Runs use pinned Bun `/tmp/go-current-test.ts`, offline hydrated dependencies/private HOME/caches, `go test -p=4 -race -count=1` with the explicit focused regexes; repeat runs pass a final `-count=10`/20. No real provider or public network was used. git diff --check passed; git status/shared tracked diff and changed test/fixture code reviewed. Global verify is owned by root and is not claimed here.

The three changed test/fixture files and this evidence document are now frozen for the next root verification corpus snapshot.
