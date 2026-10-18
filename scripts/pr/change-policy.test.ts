import { describe, expect, test } from 'bun:test'
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { evaluateChangePolicy } from './change-policy'

describe('evaluateChangePolicy', () => {
  test.each([
    'backend/internal/protocol/sse.go',
    'backend/internal/provider/anthropic.go',
    'backend/cmd/claude-haha/main.go',
    'backend/go.mod',
    'backend/go.sum',
    'scripts/quality-gate/go-parity/fixtures/tool-stream.json',
    'scripts/pr/go-dependencies.ts',
    'scripts/pr/go-platforms.ts',
    'scripts/pr/run-go-matrix.ts',
    'scripts/perf/go-main-paired-benchmark.ts',
    'scripts/perf/go-main-paired-benchmark-core.ts',
    'scripts/perf/go-main-paired-benchmark-evidence.ts',
    'scripts/perf/go-main-source-live.ts',
    'scripts/quality-gate/agent-flow/fixtures/workspace/src/notes.md',
    'contracts/session.json',
  ])('routes cross-process Go contracts without relying on JS imports: %s', file => {
    const result = evaluateChangePolicy([file])
    for (const check of ['go', 'goParity', 'server', 'desktop', 'adapters', 'desktopNative', 'providerContract', 'chatContract', 'agentFlow', 'persistence', 'policy', 'coverage'] as const) {
      expect(result.checks[check], `${file} must select ${check}`).toBe(true)
    }
  })

  test('keeps Go Harness changes under the CLI core approval and same-area test policies', () => {
    const missing = evaluateChangePolicy(['backend/internal/harness/query.go'], ['allow-cli-core-change'])
    expect(missing.areas).toEqual(['cli-core', 'server'])
    expect(missing.missingTestSignals).toEqual(['Go backend product files changed without a Go backend test file in the PR.'])
    expect(missing.blocked).toBe(true)

    const files = ['backend/internal/harness/query.go', 'backend/internal/harness/query_test.go']
    expect(evaluateChangePolicy(files).blockingReasons).toContain('CLI core changes require the allow-cli-core-change label and maintainer approval.')
    const covered = evaluateChangePolicy(files, ['allow-cli-core-change'])
    expect(covered.blocked).toBe(false)
    expect(covered.missingTestSignals).toEqual([])
  })

  test('requires language-specific tests instead of accepting tests for the other implementation', () => {
    const go = evaluateChangePolicy(['backend/internal/protocol/sse.go', 'src/server/__tests__/proxy-streaming.test.ts'], ['allow-cli-core-change'])
    expect(go.missingTestSignals).toContain('Go backend product files changed without a Go backend test file in the PR.')
    const ts = evaluateChangePolicy(['src/server/ws/handler.ts', 'backend/internal/protocol/sse_test.go'], ['allow-cli-core-change'])
    expect(ts.missingTestSignals).toContain('Server product files changed without a server test file in the PR.')
    const misplaced = evaluateChangePolicy(['src/server/ws/handler.ts', 'src/server/ws/handler_test.go'])
    expect(misplaced.missingTestSignals).toContain('Server product files changed without a server test file in the PR.')
    const fixture = evaluateChangePolicy(['backend/internal/protocol/sse.go', 'backend/testdata/fixture_test.go'], ['allow-cli-core-change'])
    expect(fixture.missingTestSignals).toContain('Go backend product files changed without a Go backend test file in the PR.')
  })

  test('preserves the explicit missing-test override and keeps backend instructions on policy only', () => {
    expect(evaluateChangePolicy(['backend/internal/protocol/sse.go'], ['allow-cli-core-change', 'allow-missing-tests']).blocked).toBe(false)
    const guidance = evaluateChangePolicy(['backend/AGENTS.md'])
    expect(guidance.areas).toEqual([])
    expect(guidance.cliCoreFiles).toEqual([])
    expect(guidance.checks.policy).toBe(true)
    expect(guidance.checks.go).toBe(false)
    expect(guidance.checks.desktopNative).toBe(false)
    expect(guidance.blocked).toBe(false)
  })

  test('requires Go checks when the package entrypoint changes and keeps unrelated docs out', () => {
    expect(evaluateChangePolicy(['package.json']).checks.go).toBe(true)
    expect(evaluateChangePolicy(['docs/index.md']).checks.go).toBe(false)
  })

  test.each([
    'src/query.ts', 'src/services/api/claude.ts', 'src/tools/FileWriteTool/FileWriteTool.ts',
    'src/server/ws/handler.ts', 'desktop/src/api/websocket.ts', 'desktop/src/api/sessions.ts',
    'adapters/common/http-client.ts', 'adapters/common/ws-bridge.ts',
    'scripts/quality-gate/coverage-thresholds.json',
  ])('selects Go validation when a reference or wire client changes: %s', file => {
    const result = evaluateChangePolicy([file])
    expect(result.checks.go).toBe(true)
    expect(result.checks.goParity).toBe(true)
    // Cross-language comparison is additive; it does not turn an unrelated TS
    // implementation edit into a native packaging edit.
    expect(result.checks.desktopNative).toBe(false)
  })

  test('blocks CLI core changes without an override label', () => {
    const result = evaluateChangePolicy([
      'src/commands/help.ts',
      'desktop/src/pages/Settings.tsx',
    ])

    expect(result.blocked).toBe(true)
    expect(result.areas).toContain('cli-core')
    expect(result.areas).toContain('desktop')
    expect(result.areaLabels).toContain('area:cli-core')
    expect(result.areaLabels).toContain('area:desktop')
    expect(result.cliCoreFiles).toEqual(['src/commands/help.ts'])
  })

  test('allows CLI core changes with a maintainer override label', () => {
    const result = evaluateChangePolicy(
      ['src/tools/WebSearchTool/backend.ts'],
      ['allow-cli-core-change', 'allow-missing-tests'],
    )

    expect(result.blocked).toBe(false)
    expect(result.areas).toEqual(['cli-core'])
    expect(result.checks.server).toBe(true)
  })

  test('keeps docs-only changes on the docs lane', () => {
    const result = evaluateChangePolicy([
      'docs/index.md',
      'site/src/App.tsx',
      'README.md',
      'README.en.md',
    ])

    expect(result.blocked).toBe(false)
    expect(result.areas).toEqual(['docs'])
    expect(result.checks.docs).toBe(true)
    expect(result.checks.coverage).toBe(false)
    expect(result.checks.desktop).toBe(false)
    expect(result.checks.desktopNative).toBe(false)
  })

  test('routes desktop and server changes without escalating renderer code to native packaging', () => {
    const result = evaluateChangePolicy([
      'desktop/src/pages/Settings.tsx',
      'src/server/ws/handler.ts',
    ])

    expect(result.areas).toEqual(['desktop', 'server'])
    expect(result.checks.desktop).toBe(true)
    expect(result.checks.server).toBe(true)
    expect(result.checks.desktopNative).toBe(false)
    expect(result.checks.chatContract).toBe(true)
    expect(result.checks.coverage).toBe(true)
    expect(result.missingTestSignals).toContain('Desktop product files changed without a desktop test file in the PR.')
    expect(result.missingTestSignals).toContain('Server product files changed without a server test file in the PR.')
  })

  test('keeps adapter changes off the desktop native packaging lane', () => {
    const result = evaluateChangePolicy(['adapters/telegram/index.ts'])

    expect(result.areas).toEqual(['adapters'])
    expect(result.checks.adapters).toBe(true)
    expect(result.checks.desktopNative).toBe(false)
    expect(result.checks.coverage).toBe(true)
    expect(result.blocked).toBe(true)
    expect(result.missingTestSignals).toEqual(['Adapter product files changed without an adapter test file in the PR.'])
  })

  test('allows production changes when matching tests are included', () => {
    const result = evaluateChangePolicy([
      'desktop/src/pages/Settings.tsx',
      'desktop/src/pages/Settings.test.tsx',
    ])

    expect(result.blocked).toBe(false)
    expect(result.missingTestSignals).toEqual([])
  })

  test('routes Electron and packaging changes to the native lane', () => {
    const result = evaluateChangePolicy([
      'desktop/electron/main.ts',
      'desktop/electron/main.test.ts',
    ])

    expect(result.checks.desktop).toBe(false)
    expect(result.checks.desktopNative).toBe(true)
  })

  test.each([
    'native/cu-helper/Sources/cu-helper/VirtualCursor.swift',
    'native/cu-helper/Tests/CuHelperTests/VirtualCursorResourceTests.swift',
    'native/cu-helper/Sources/cu-helper/Resources/LensSequence/frame_000.png',
    'native/cu-helper/Package.swift',
    'native/cu-helper/build.sh',
  ])('requires the native and macOS Swift jobs for a standalone change to %s', file => {
    const result = evaluateChangePolicy([file])
    expect(result.areas).toEqual(['desktop'])
    expect(result.checks.desktopNative).toBe(true)
    expect(result.checks.desktop).toBe(false)
    expect(result.blocked).toBe(false)
  })

  test('routes provider runtime changes to the offline provider contract', () => {
    const result = evaluateChangePolicy([
      'src/server/services/providerRuntimeEnv.ts',
      'src/server/__tests__/provider-runtime-env.test.ts',
    ])

    expect(result.checks.server).toBe(true)
    expect(result.checks.providerContract).toBe(true)
    expect(result.checks.chatContract).toBe(false)
  })

  test('routes persistence and policy changes to their dedicated checks', () => {
    const result = evaluateChangePolicy([
      'desktop/src/lib/persistenceMigrations.ts',
      'desktop/src/lib/persistenceMigrations.test.ts',
      '.github/workflows/pr-quality.yml',
    ])

    expect(result.checks.desktop).toBe(true)
    expect(result.checks.persistence).toBe(true)
    expect(result.checks.policy).toBe(true)
  })

  test('routes desktop UI preference schema changes to the persistence check', () => {
    const result = evaluateChangePolicy([
      'src/server/services/desktopUiPreferencesService.ts',
      'src/server/__tests__/desktop-ui-preferences.test.ts',
    ])

    expect(result.checks.server).toBe(true)
    expect(result.checks.persistence).toBe(true)
  })

  test('routes every source root the dead-import check owns to the policy lane', () => {
    // scripts/pr/dead-imports.test.ts reads these roots rather than importing them,
    // so the import graph cannot pull the policy lane in. Without the prefixes the
    // check exists and never runs on the diffs it was written for.
    expect(evaluateChangePolicy(['src/server/ws/handler.ts']).checks.policy).toBe(true)
    expect(evaluateChangePolicy(['src/utils/attachments.ts']).checks.policy).toBe(true)
    expect(evaluateChangePolicy(['adapters/feishu/index.ts']).checks.policy).toBe(true)
    expect(evaluateChangePolicy(['scripts/perf/local-index-benchmark.ts']).checks.policy).toBe(true)
    // Surfaces still route to their own lanes; policy is additive, not a takeover.
    expect(evaluateChangePolicy(['src/server/ws/handler.ts']).checks.server).toBe(true)
    expect(evaluateChangePolicy(['adapters/feishu/index.ts']).checks.adapters).toBe(true)
    // desktop/ is checked by its own tsconfig, so it must not select this lane.
    expect(evaluateChangePolicy(['desktop/src/pages/Settings.tsx']).checks.policy).toBe(false)
  })

  test('keeps quality ownership and contributor contracts on the policy lane', () => {
    const result = evaluateChangePolicy([
      '.github/CODEOWNERS',
      '.github/copilot-instructions.md',
      'docs/internals/contributing.md',
    ])

    expect(result.checks.policy).toBe(true)
    expect(result.checks.docs).toBe(true)
  })

  test('routes root and nested agent guidance only to the policy lane', () => {
    const result = evaluateChangePolicy([
      'AGENTS.md',
      '.github/AGENTS.md',
      'src/AGENTS.md',
      'desktop/AGENTS.md',
      'adapters/AGENTS.md',
      'docs/AGENTS.md',
    ])

    expect(result.areas).toEqual([])
    expect(result.checks.policy).toBe(true)
    expect(result.checks.desktop).toBe(false)
    expect(result.checks.server).toBe(false)
    expect(result.checks.adapters).toBe(false)
    expect(result.checks.docs).toBe(false)
    expect(result.checks.coverage).toBe(false)
  })

  test('does not require a test file for non-executable desktop assets', () => {
    const result = evaluateChangePolicy([
      'desktop/src/styles/chat.css',
      'desktop/src/assets/logo.svg',
    ])

    expect(result.blocked).toBe(false)
    expect(result.missingTestSignals).toEqual([])
    expect(result.checks.desktop).toBe(true)
    expect(result.checks.coverage).toBe(false)
  })

  test('covers root runtime code outside src/server with tests and coverage', () => {
    const missing = evaluateChangePolicy(['src/services/api/client.ts'])
    expect(missing.checks.server).toBe(true)
    expect(missing.checks.providerContract).toBe(true)
    expect(missing.checks.coverage).toBe(true)
    expect(missing.missingTestSignals).toContain('Root runtime product files changed without a matching root runtime test file in the PR.')

    const covered = evaluateChangePolicy([
      'src/services/api/client.ts',
      'src/services/api/client.test.ts',
    ])
    expect(covered.blocked).toBe(false)
    expect(covered.missingTestSignals).toEqual([])
  })

  test('accepts root runtime regression tests across service and utility seams', () => {
    const result = evaluateChangePolicy(
      [
        'src/utils/messages.ts',
        'src/services/api/streamRetry.test.ts',
      ],
      ['allow-cli-core-change'],
    )

    expect(result.missingTestSignals).toEqual([])
    expect(result.blocked).toBe(false)
  })

  test('blocks coverage baseline and threshold changes without maintainer override', () => {
    const result = evaluateChangePolicy([
      'scripts/quality-gate/coverage-baseline.json',
      'scripts/quality-gate/coverage-thresholds.json',
    ])

    expect(result.blocked).toBe(true)
    expect(result.coveragePolicyFiles).toEqual([
      'scripts/quality-gate/coverage-baseline.json',
      'scripts/quality-gate/coverage-thresholds.json',
    ])
    expect(result.blockingReasons).toContain('Coverage baseline or threshold changes require the allow-coverage-baseline-change label and maintainer approval.')
  })

  test('allows coverage baseline changes with maintainer override', () => {
    const result = evaluateChangePolicy(
      ['scripts/quality-gate/coverage-baseline.json'],
      ['allow-coverage-baseline-change'],
    )

    expect(result.blocked).toBe(false)
  })

  test('normalizes relative and windows-style paths before classification', () => {
    const result = evaluateChangePolicy([
      './desktop\\src\\pages\\Settings.tsx',
      './desktop\\src\\pages\\Settings.test.tsx',
      './scripts\\quality-gate\\coverage.ts',
    ])

    expect(result.files).toContain('desktop/src/pages/Settings.tsx')
    expect(result.files).toContain('scripts/quality-gate/coverage.ts')
    expect(result.areas).toContain('desktop')
    expect(result.checks.coverage).toBe(true)
    expect(result.blocked).toBe(false)
  })

  test('plan-only mode publishes a blocked scope without preventing product jobs', async () => {
    const dir = mkdtempSync(join(tmpdir(), 'change-policy-plan-'))
    try {
      const filesPath = join(dir, 'files.txt')
      const labelsPath = join(dir, 'labels.txt')
      const outputPath = join(dir, 'github-output.txt')
      writeFileSync(filesPath, 'desktop/src/pages/Settings.tsx\n')
      writeFileSync(labelsPath, '')

      const proc = Bun.spawn([
        'bun',
        'run',
        'scripts/pr/change-policy.ts',
        '--files',
        filesPath,
        '--labels-file',
        labelsPath,
        '--plan-only',
      ], {
        cwd: process.cwd(),
        env: { ...process.env, GITHUB_OUTPUT: outputPath },
        stdout: 'pipe',
        stderr: 'pipe',
      })

      expect(await proc.exited).toBe(0)
      const outputs = readFileSync(outputPath, 'utf8')
      expect(outputs).toContain('blocked=true')
      expect(outputs).toContain('desktop_checks=true')
      expect(outputs).toContain('desktop_native_checks=false')
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })
})

describe('evaluateChangePolicy dependent-file widening', () => {
  test('selects the desktop lane when a root change is imported by desktop code', () => {
    const pathOnly = evaluateChangePolicy(['src/shared/modelReasoning.ts'])
    const withDependents = evaluateChangePolicy(
      ['src/shared/modelReasoning.ts'],
      [],
      ['desktop/src/lib/runtimeSelection.ts'],
    )

    expect(pathOnly.checks.desktop).toBe(false)
    expect(withDependents.checks.desktop).toBe(true)
  })

  test('selects the native lane when a desktop change is imported by the Electron host', () => {
    const pathOnly = evaluateChangePolicy(['desktop/src/lib/browserSafePort.ts'])
    const withDependents = evaluateChangePolicy(
      ['desktop/src/lib/browserSafePort.ts'],
      [],
      ['desktop/electron/services/sidecarManager.ts'],
    )

    expect(pathOnly.checks.desktopNative).toBe(false)
    expect(withDependents.checks.desktopNative).toBe(true)
  })

  test('selects the adapter lane when a root change is imported by an adapter', () => {
    const withDependents = evaluateChangePolicy(
      ['src/utils/shared.ts'],
      [],
      ['adapters/telegram/bot.ts'],
    )

    expect(evaluateChangePolicy(['src/utils/shared.ts']).checks.adapters).toBe(false)
    expect(withDependents.checks.adapters).toBe(true)
  })

  test('keeps areas, labels, and the missing-test block scoped to the actual diff', () => {
    const result = evaluateChangePolicy(
      ['src/shared/modelReasoning.ts', 'src/shared/modelReasoning.test.ts'],
      [],
      ['desktop/src/lib/runtimeSelection.ts', 'desktop/src/stores/chatStore.ts'],
    )

    // A hub edit must not demand desktop tests for files the author never touched.
    expect(result.areas).toEqual([])
    expect(result.missingTestSignals).toEqual([])
    expect(result.blocked).toBe(false)
    expect(result.files).toEqual(['src/shared/modelReasoning.test.ts', 'src/shared/modelReasoning.ts'])
    expect(result.checks.desktop).toBe(true)
  })

  test('does not let a dependent file trigger the CLI core block', () => {
    const result = evaluateChangePolicy(
      ['src/server/services/providerService.ts', 'src/server/__tests__/provider.test.ts'],
      [],
      ['src/commands/help.ts', 'src/tools/BashTool/index.ts'],
    )

    expect(result.cliCoreFiles).toEqual([])
    expect(result.blocked).toBe(false)
  })

  test('does not widen docs or coverage lanes', () => {
    const result = evaluateChangePolicy(
      ['src/server/services/providerService.ts', 'src/server/__tests__/provider.test.ts'],
      [],
      ['docs/internals/contributing.md', 'scripts/pr/check-pr.ts', 'desktop/src/pages/Settings.tsx'],
    )

    expect(result.checks.docs).toBe(false)
    // Coverage still reflects the diff, which already contains executable sources.
    expect(result.checks.coverage).toBe(true)
  })

  test('does not widen the policy lane', () => {
    // Split out of the case above once `src/` became a policy prefix: that fixture
    // selects the lane through its own changed files now, so it can no longer show
    // what this asserts — a dependent the import graph added must never select it.
    const result = evaluateChangePolicy(
      ['desktop/src/pages/Settings.tsx'],
      [],
      ['scripts/pr/check-pr.ts', 'src/utils/attachments.ts', 'adapters/feishu/index.ts'],
    )

    expect(result.checks.policy).toBe(false)
  })

  test('selects the agent flow for protocol clients the import graph cannot reach', () => {
    // Regression for d14154379 -> 4626dbef4: adapters/common/http-client.ts hardcoded
    // permissionMode:'default' on POST /api/sessions, short-circuiting the server's
    // global fallback for every IM-created session. Adapters reach the server over
    // the wire, so no dependency graph links them; the coupling has to be declared.
    const adapterHttp = evaluateChangePolicy([
      'adapters/common/http-client.ts',
      'adapters/common/__tests__/http-client.test.ts',
    ])
    expect(adapterHttp.checks.agentFlow).toBe(true)

    const adapterWs = evaluateChangePolicy([
      'adapters/common/ws-bridge.ts',
      'adapters/common/__tests__/ws-bridge.test.ts',
    ])
    expect(adapterWs.checks.agentFlow).toBe(true)

    // An unrelated adapter still stays out of the agent flow lane.
    expect(evaluateChangePolicy([
      'adapters/telegram/formatting.ts',
      'adapters/telegram/__tests__/formatting.test.ts',
    ]).checks.agentFlow).toBe(false)
  })

  test('selects the mock-LLM agent lane for any runtime source change', () => {
    // The lane runs the real CLI agent loop, so a change anywhere in the runtime can
    // break it — not only the server/WebSocket paths the mock-CLI lane watches.
    const toolChange = evaluateChangePolicy(['src/tools/BashTool/BashTool.tsx', 'src/tools/BashTool/BashTool.test.ts'])
    expect(toolChange.checks.agentE2e).toBe(true)
    expect(toolChange.checks.agentFlow).toBe(false)

    for (const harness of [
      'scripts/quality-gate/mock-llm/scriptedModel.ts',
      'scripts/quality-gate/agent-flow/live.ts',
      'scripts/quality-gate/sandbox.ts',
      'bun.lock',
    ]) {
      expect(evaluateChangePolicy([harness]).checks.agentE2e, harness).toBe(true)
    }

    // Desktop-only, docs-only, and agent instruction edits stay out of it.
    expect(evaluateChangePolicy(['desktop/src/pages/Settings.tsx', 'desktop/src/pages/Settings.test.tsx']).checks.agentE2e).toBe(false)
    expect(evaluateChangePolicy(['docs/guide/install.md']).checks.agentE2e).toBe(false)
    expect(evaluateChangePolicy(['src/AGENTS.md']).checks.agentE2e).toBe(false)
  })

  test('ignores dependents that are already part of the diff', () => {
    const result = evaluateChangePolicy(
      ['desktop/src/lib/a.ts', 'desktop/src/lib/a.test.ts'],
      [],
      ['desktop/src/lib/a.ts', './desktop/src/lib/a.ts', ''],
    )

    expect(result.checks.desktop).toBe(true)
    expect(result.checks.desktopNative).toBe(false)
  })
})

test.each([
  'scripts/quality-gate/goBackend.ts',
  'scripts/quality-gate/goBackend.test.ts',
  'scripts/quality-gate/go-parity/native-team-supervisor-helpers.fixture.ts',
  'backend/internal/trajectory/service.go',
  'backend/internal/promptsnapshot/service_test.go',
  'backend/internal/imageinput/unsupported.go',
  'backend/internal/modelyield/native_stream_watchdog.go',
  'backend/internal/streamhistory/early_end.go',
])('native runtime and fixture changes retain Go, parity, and changed coverage: %s', file => {
  const selection = evaluateChangePolicy([file], ['allow-cli-core-change'])
  expect(selection.checks.go).toBe(true)
  expect(selection.checks.goParity).toBe(true)
  expect(selection.checks.coverage).toBe(true)
})

test.each(['backend/internal/whatsappcompat/device_state.go', 'scripts/quality-gate/go-persistence.ts', 'scripts/quality-gate/persistence-upgrade.ts'])('native persisted compatibility changes retain the dedicated persistence lane: %s', file => {
  const result = evaluateChangePolicy([file])
  expect(result.checks.go).toBe(true)
  expect(result.checks.goParity).toBe(true)
  expect(result.checks.persistence).toBe(true)
})

test.each([
  'desktop/src/components/settings/ConnectorsTab.tsx',
  'desktop/src/api/trajectory.ts',
  'desktop/src/types/trajectory.ts',
  'desktop/electron/main.ts',
  'desktop/scripts/build-go-server.ts',
  'adapters/wechat/index.ts',
  'adapters/common/session-recovery.ts',
])('cross-process consumers cannot escape the Go runtime gates: %s', file => {
  const selection = evaluateChangePolicy([file], [])
  expect(selection.checks.go).toBe(true)
  expect(selection.checks.goParity).toBe(true)
})

test('generic performance scripts do not require Go environments or invoke live runs', () => {
  const generic = evaluateChangePolicy(['scripts/perf/session-open-benchmark.ts'])
  expect(generic.checks.policy).toBe(true)
  expect(generic.checks.go).toBe(false)
})


test.each([
  'scripts/perf/source-parent-recorder/record_source_parent.py',
  'scripts/perf/source-parent-recorder/credential_capability.py',
  'scripts/perf/source-parent-recorder/record-source-parent.ts',
  'scripts/perf/source-parent-recorder/formal-command.ts',
  'scripts/perf/source-parent-recorder/formal-transport.ts',
])('routes parent producer-only changes to existing Go and contract gates: %s', file => {
  const result = evaluateChangePolicy([file], [], [])
  // No dependent JS imports or companion benchmark edit may be required.
  expect(result.checks.policy).toBe(true)
  for (const check of ['go', 'goParity', 'server', 'desktop', 'adapters', 'desktopNative', 'providerContract', 'chatContract', 'agentFlow', 'agentE2e', 'persistence', 'coverage'] as const) {
    expect(result.checks[check], `${file} must select ${check}`).toBe(true)
  }
})
