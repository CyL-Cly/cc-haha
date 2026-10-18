# 当前完整生命周期边界消费者集合

2026-10-14。冻结 Source `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`。这是当前明确集合的完整执行证据，不追认无法恢复的旧测试身份。

## 历史名单与当前执行身份

旧交接的“33 项”只保留了 32 pass/1 fail 和后续聚焦修复数字。handoff、current validation、progress 没有完整历史 selector；progress 第 208 行已明确旧命令不可恢复。既有 ignored `session-lifecycle-consumers.json` 保存的是后续明确枚举的 48 个消费者，并标注 `historical33ListUnavailable=true`。本次没有猜测旧名单。

本次保留全部 48 个明确名字，执行其全部 29 文件；再加实际 Pet/PublicAccess HTTP/WS/revoke/shutdown 两文件、今天修复 running-Agent fixture 的 continuation/control 两文件。当前是 **33 文件、55 顶层消费者**，与历史 33 tests 是不同身份。两项 running-Agent 修改仅将测试环境配置移到 Native 构造前，生产和原断言未改；所在文件的全部四个实际消费者通过。

## 实际执行

既有 hermetic 临时 HOME/config、已校验 Go 1.25.7/Bun 1.3.14，显式私有 PowerShell 7.6.6。未启用 coverage instrumentation/GOCOVERDIR，因此没有正式覆盖率结论。原测试窗口/断言保留，没有新增镜像 tests、live model quota 或真实 Claude OAuth。

`go test -p=2 ./internal/application ./internal/clibootstrap ./internal/remoteio ./internal/server -run '<55 名字的完整锚定 union>' -race -count=1 -json`。

| 包 | 顶层消费者 | 包耗时 | 状态 |
| --- | ---: | ---: | --- |
| application | 26 | 117.281 秒 | passed |
| clibootstrap | 4 | 9.477 秒 | passed |
| remoteio | 2 | 8.261 秒 | passed |
| server | 23 | 4.893 秒 | passed |

合计 **55 top-level passed / 137 pass events，0 fail，0 skip**。逐项 selector 与实际顶层事件一一匹配，33 文件执行前后 SHA 一致；六个直接相关 Source session/WS/collaboration 当前 blob 与冻结对象相同。完整 names、package/file、arguments、Source blob/SHA、前后 SHA、原事件在 ignored `artifacts/go-migration-20261013/lifecycle-complete-boundary-set-20261014/selection.json` / `receipt.json`。原四包 JSON log SHA：`a9ce0485ab3ff9222ef8367744e9edd14b68fb88cdea80fdac9f244b8a03fe9a`。

## 证据范围

覆盖 Source SDK-only idle/前台 startup、connect 与显式 sync 分离、Stop 取消旧 queued token、权限重连、同 SDK transport/凭据撤销、旧 engine/replacement fence、实际团队 barrier/question/idle mailbox/worker join、remote WS 环境/认证重连和 reader 取消，包含实际 HTTP/WS、私有 subprocess/文件消费者。每个测试的 Source/接口 fixture/实物身份以代码与原 log 为准，不把全部事件称作 live 或完整 GUI。

当前明确集合已完整重跑通过。整个 Go/90%、全部 application 包、最终同版本桌面/Web/CLI、正式 120 对仍须总体任务执行，不能以本次集合外推。checkpoint-11 会统一复核。

## Exact current selectors


### ./internal/application

- `TestDesktopCloseJoinsAlreadyClosedNativeEngineWithoutNewCleanupAdmission` — `backend/internal/application/desktop_close_idempotent_test.go`
- `TestActualSourceDesktopCompletionDoesNotPublishReconnectSnapshot` — `backend/internal/application/desktop_completion_snapshot_source_test.go`
- `TestActualDesktopGrokFactoryCatalogWSWriteAndClose` — `backend/internal/application/desktop_grok_model_flow_test.go`
- `TestActualSourceAndPublicDesktopPlanApprovalCrossProviderRestart` — `backend/internal/application/desktop_plan_restart_source_test.go`
- `TestActualSourceAndPublicDesktopPlanApprovalSameProviderEffortRestart` — `backend/internal/application/desktop_plan_restart_source_test.go`
- `TestActualSourceAndPublicDesktopPlanApprovalRestartClientLifecycle` — `backend/internal/application/desktop_plan_restart_source_test.go`
- `TestActualSourceAndPublicDesktopPlanApprovalDuplicateAndQueuedInput` — `backend/internal/application/desktop_plan_restart_source_test.go`
- `TestDesktopPlanRuntimeApprovalAcknowledgesPersistsAllowsAndChangesNextASK` — `backend/internal/application/desktop_plan_runtime_approval_test.go`
- `TestDesktopPlanRuntimeApprovalFailureRetainsActualPermission` — `backend/internal/application/desktop_plan_runtime_approval_test.go`
- `TestDesktopPlanRuntimeApprovalNonExitIgnoresOverride` — `backend/internal/application/desktop_plan_runtime_approval_test.go`
- `TestDesktopPlanApprovalUpdatesSamePermissionStateAndExecutesActualFiles` — `backend/internal/application/desktop_plan_test.go`
- `TestSDKControlLifecycleMatchesActualSource` — `backend/internal/application/desktop_sdk_control_test.go`
- `TestSDKControlRealPrivateSubprocess` — `backend/internal/application/desktop_sdk_control_test.go`
- `TestDedicatedSDKOAuthRecoveryUsesActualOwnedServiceAndSource` — `backend/internal/application/desktop_sdk_oauth_test.go`
- `TestDedicatedSDKOAuthRecoveryCloseCancelsAndDoesNotEmitStaleToken` — `backend/internal/application/desktop_sdk_oauth_test.go`
- `TestDedicatedSDKPermissionsMatchActualSource` — `backend/internal/application/desktop_sdk_source_test.go`
- `TestDedicatedSDKSocketSharesDesktopPermissionTransportWithoutSecondAgent` — `backend/internal/application/desktop_sdk_test.go`
- `TestDesktopRevokedUserCannotStageThinkingAfterAdmissionLocks` — `backend/internal/application/desktop_stop_queue_test.go`
- `TestActualDesktopStopDropsOldQueuedUsersBeforeFreshNativeWrite` — `backend/internal/application/desktop_stop_queue_test.go`
- `TestActualTeamPermissionReplayedAfterLeadCompletionThenRoutesAndExecutes` — `backend/internal/application/desktop_team_permission_after_lead_test.go`
- `TestActualDesktopApprovedTeamRuntimeBarrierTasksQuestionIdleMailboxWakeAndShutdown` — `backend/internal/application/desktop_team_plan_runtime_test.go`
- `TestActualDesktopTeamWorkerPrepareBarrierParentQuestionAndStop` — `backend/internal/application/desktop_team_worker_test.go`
- `TestDesktopTeammateExitClosesActualSDKTransportAndOldEngineCannotCloseReplacement` — `backend/internal/application/desktop_teammate_shutdown_test.go`
- `TestDesktopTransportRunsSharedGoAgentAndReconnectsPendingPermission` — `backend/internal/application/desktop_test.go`
- `TestActualDesktopLeaderIdleMailboxReconnectSamePendingAndServerCloseJoinsWorker` — `backend/internal/application/team_leader_websocket_test.go`
- `TestDesktopOriginalSDKRunStateProjectionAndReconnectAgainstActualSource` — `backend/internal/application/team_run_state_source_test.go`

### ./internal/clibootstrap

- `TestActualRunningAgentMessageIsConsumedAtNextToolRound` — `backend/internal/clibootstrap/native_agent_continuation_test.go`
- `TestActualAgentContinuationUsesSameSidechainAndSpawningCardAfterColdRestore` — `backend/internal/clibootstrap/native_agent_continuation_test.go`
- `TestActualCLIStreamsAgentMessageControlWhileIndependentAgentIsRunning` — `backend/internal/clibootstrap/native_agent_control_test.go`
- `TestActualRetainedResumeAppendsOnlyNewStreamAndKeepsOriginalTaskCard` — `backend/internal/clibootstrap/native_agent_control_test.go`

### ./internal/remoteio

- `TestPhysicalRemoteInputParentCancellationClosesReaderAndJoinsTransport` — `backend/internal/remoteio/cancellation_test.go`
- `TestActualSourceEnvironmentUpdateRefreshesProxyAndAuthOnWebSocketReconnect` — `backend/internal/remoteio/network_refresh_source_test.go`

### ./internal/server

- `TestAdmissionPreservesSocketUserOrderWhilePermissionBypasses` — `backend/internal/server/admission_test.go`
- `TestShutdownCancelsAdmissionAndDropsQueuedUserFrames` — `backend/internal/server/admission_test.go`
- `TestDisconnectActualRuntimeLossPersistsFailedAndRejectsLateTerminal` — `backend/internal/server/disconnect_lifecycle_test.go`
- `TestDisconnectFailedBookendRetainsProofRetriesAndJoinsOnClose` — `backend/internal/server/disconnect_lifecycle_test.go`
- `TestDisconnectRootBackgroundPredicateExclusions` — `backend/internal/server/disconnect_lifecycle_test.go`
- `TestDisconnectPhysicalTimerRootFollowupAndQueuedCanceledCallbacks` — `backend/internal/server/disconnect_lifecycle_test.go`
- `TestDisconnectPermissionCeilingAndMigrationFence` — `backend/internal/server/disconnect_lifecycle_test.go`
- `TestDisconnectExpiredStartupWaitsAndReconnectSupersedes` — `backend/internal/server/disconnect_lifecycle_test.go`
- `TestDisconnectBackgroundAndRootBoundaryAgainstActualFrozenSource` — `backend/internal/server/disconnect_source_test.go`
- `TestActualSourceForegroundSnapshotsIgnoreSDKOnlyRunAndIncludePendingStartup` — `backend/internal/server/foreground_snapshot_source_test.go`
- `TestDesktopSnapshotRegisteredUserBeforeFactoryAndSettlesBeforeCompletion` — `backend/internal/server/foreground_snapshot_test.go`
- `TestShutdownWaitsForInFlightFactoryAndConcurrentClose` — `backend/internal/server/lifecycle_test.go`
- `TestDisconnectRechecksActiveTurnAndReopensFromLatestHistory` — `backend/internal/server/lifecycle_test.go`
- `TestDeletingSessionCancelsOpeningFactoryBeforeRemovingHistory` — `backend/internal/server/lifecycle_test.go`
- `TestShutdownHookFollowsRuntimeCleanupAndSharesError` — `backend/internal/server/lifecycle_test.go`
- `TestPetPhysicalHTTPProjectionMembershipAndIndependentCredentials` — `backend/internal/server/pet_test.go`
- `TestPetPhysicalWebSocketFiltersAndSwitchesWithoutStoppingFullClient` — `backend/internal/server/pet_test.go`
- `TestPublicAccessActualServerSharedSocketRevokeAndShutdown` — `backend/internal/server/public_access_test.go`
- `TestDedicatedSDKCredentialsReplayReconnectRevocationAndShutdown` — `backend/internal/server/sdk_test.go`
- `TestSessionCollaborationAuthenticationMatchesSourceBeforeOrdinaryCORS` — `backend/internal/server/session_collaboration_auth_test.go`
- `TestSessionCollaborationCredentialRevokedWithOwnedRuntimeAndOnShutdown` — `backend/internal/server/session_collaboration_auth_test.go`
- `TestClientConnectionReplaysPermissionsAndOnlySyncRequestsTurnState` — `backend/internal/server/user_admission_stop_test.go`
- `TestStopRevokesAllOldQueuedInputsAndAllowsOnlyFreshReplacement` — `backend/internal/server/user_admission_stop_test.go`
