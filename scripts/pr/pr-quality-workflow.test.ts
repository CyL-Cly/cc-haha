import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { parse } from 'yaml'
import { GO_PLATFORMS } from './go-platforms'

type WorkflowJob = {
  if?: string
  'runs-on'?: string
  'continue-on-error'?: boolean
  needs?: string | string[]
  strategy?: { 'fail-fast'?: boolean; matrix?: { include?: Array<{ id: string; target: string; runner: string }> } }
  steps?: Array<{ name?: string; run?: string; uses?: string; with?: Record<string, unknown>; env?: Record<string, unknown>; 'working-directory'?: string }>
}

function workflowJobs(workflow: string) {
  return (parse(workflow) as { jobs: Record<string, WorkflowJob> }).jobs
}

describe('PR quality workflow', () => {
  test('uses packageManager as the single pinned Bun version source', () => {
    const workflow = readFileSync('.github/workflows/pr-quality.yml', 'utf8')
    const packageJson = JSON.parse(readFileSync('package.json', 'utf8')) as {
      packageManager?: string
    }
    const setupBunStepCount = workflow.match(/uses: oven-sh\/setup-bun@v2/g)?.length ?? 0
    const versionFileCount = workflow.match(/bun-version-file: package\.json/g)?.length ?? 0

    expect(packageJson.packageManager).toBe('bun@1.3.14')
    expect(setupBunStepCount).toBeGreaterThan(0)
    expect(versionFileCount).toBe(setupBunStepCount)
    expect(workflow).not.toContain('bun-version:')
    expect(workflow).not.toContain('1.3.12')
    expect(workflow).not.toContain('bun-version: latest')
  })

  test('builds scope before routing independent quality jobs', () => {
    const workflow = readFileSync('.github/workflows/pr-quality.yml', 'utf8')

    expect(workflow).toContain('scope-plan:')
    expect(workflow).toContain('--plan-only')
    expect(workflow).toContain("if: needs.scope-plan.outputs.desktop_checks == 'true'")
    expect(workflow).toContain("if: needs.scope-plan.outputs.server_checks == 'true'")
    expect(workflow).toContain("if: needs.scope-plan.outputs.go_checks == 'true'")
    expect(workflow).toContain("if: needs.scope-plan.outputs.go_parity_checks == 'true'")
    expect(workflow).toContain("if: needs.scope-plan.outputs.provider_contract_checks == 'true'")
    expect(workflow).toContain("if: needs.scope-plan.outputs.chat_contract_checks == 'true'")
    expect(workflow).toContain("if: needs.scope-plan.outputs.persistence_checks == 'true'")
    expect(workflow).toContain("if: needs.scope-plan.outputs.adapter_checks == 'true'")
    expect(workflow).toContain("if: needs.scope-plan.outputs.desktop_native_checks == 'true'")
    expect(workflow).toContain("if: needs.scope-plan.outputs.docs_checks == 'true'")
    expect(workflow).toContain("if: needs.scope-plan.outputs.coverage_checks == 'true'")
  })

  test('installs frozen dependencies before policy regressions without blocking product routing', () => {
    const workflow = readFileSync('.github/workflows/pr-quality.yml', 'utf8')
    const jobs = workflowJobs(workflow)
    const policySteps = jobs['policy-enforcement'].steps ?? []
    const installIndex = policySteps.findIndex((step) => step.name === 'Install root dependencies')
    const regressionIndex = policySteps.findIndex((step) => step.name === 'Run policy regression tests')

    expect(jobs['policy-enforcement'].needs).toBe('scope-plan')
    expect(installIndex).toBeGreaterThanOrEqual(0)
    expect(installIndex).toBeLessThan(regressionIndex)
    for (const jobId of [
      'desktop-checks',
      'server-checks',
      'go-platform-checks',
      'go-parity',
      'provider-contract-checks',
      'chat-contract-checks',
      'agent-flow-checks',
      'adapter-checks',
      'desktop-native-checks',
      'macos-swift-checks',
      'persistence-checks',
      'docs-checks',
      'coverage-checks',
    ]) {
      expect(jobs[jobId].needs).toBe('scope-plan')
    }
  })

  test('installs imported workspace dependencies and ripgrep before runtime and coverage tests', () => {
    const jobs = workflowJobs(readFileSync('.github/workflows/pr-quality.yml', 'utf8'))
    for (const [job, command] of [
      ['server-checks', 'bun run check:server'],
      ['coverage-checks', 'bun run check:coverage'],
      ['desktop-native-checks', 'bun run check:native'],
    ]) {
      const steps = jobs[job].steps ?? []
      const check = steps.findIndex(step => step.run === command)
      expect(check).toBeGreaterThanOrEqual(0)
      for (const workspace of ['desktop', 'adapters']) {
        const install = steps.findIndex(step =>
          step['working-directory'] === workspace && step.run === 'bun install --frozen-lockfile',
        )
        expect(install).toBeGreaterThanOrEqual(0)
        expect(install).toBeLessThan(check)
      }
      const ripgrep = steps.findIndex(step => step.run?.includes('apt-get install') && /\bripgrep\b/.test(step.run))
      expect(ripgrep).toBeGreaterThanOrEqual(0)
      expect(ripgrep).toBeLessThan(check)
    }
  })

  test('runs Go build, vet, unit, race and parity using the backend toolchain declaration', () => {
    const workflow = readFileSync('.github/workflows/pr-quality.yml', 'utf8')
    const jobs = workflowJobs(workflow)
    for (const [id, command] of [['go-platform-checks', 'check:go'], ['go-parity', 'check:go-parity']]) {
      const job = jobs[id]
      expect(job.needs).toBe('scope-plan')
      expect(job['continue-on-error']).not.toBe(true)
      expect(job.steps?.find(step => step.uses === 'actions/setup-go@v5')?.with).toEqual({ 'go-version-file': 'backend/go.mod', cache: false,
        ...(id === 'go-platform-checks' ? { architecture: "${{ endsWith(matrix.target, '/arm64') && 'arm64' || 'x64' }}" } : {}),
      })
      expect(job.steps?.some(step => step.run === `bun run ${command}`)).toBe(true)
      const prepare = job.steps?.findIndex(step => step.run === 'bun run prepare:go') ?? -1
      const check = job.steps?.findIndex(step => step.run === `bun run ${command}`) ?? -1
      expect(prepare).toBeGreaterThanOrEqual(0)
      expect(prepare).toBeLessThan(check)
      expect(job.steps?.[prepare].name).toContain('setup network only')
      expect(jobs['pr-quality-gate'].needs).toContain(id === 'go-platform-checks' ? 'go-checks' : id)
    }
    expect(workflow).toContain('go_checks: ${{ steps.policy.outputs.go_checks }}')
    expect(workflow).toContain('go_parity_checks: ${{ steps.policy.outputs.go_parity_checks }}')
    expect(workflow).toContain('path: artifacts/go-checks/')
    expect(workflow).toContain('path: artifacts/go-parity/')
    const packageJson = JSON.parse(readFileSync('package.json', 'utf8')) as { scripts: Record<string, string> }
    expect(packageJson.scripts['check:go']).toBe('bun run scripts/pr/run-go-tests.ts')
    expect(packageJson.scripts['check:go-matrix']).toBe('bun run scripts/pr/run-go-matrix.ts')
    expect(packageJson.scripts['check:go-parity']).toBe('bun run scripts/quality-gate/go-parity/index.ts')
    expect(packageJson.scripts['prepare:go']).toBe('bun run scripts/pr/go-dependencies.ts')
    expect(packageJson.scripts['check:policy']).toContain('scripts/pr/run-go-tests.test.ts')
    expect(packageJson.scripts['check:policy']).toContain('scripts/pr/go-dependencies.test.ts')
    expect(packageJson.scripts['check:policy']).toContain('scripts/pr/go-platforms.test.ts')
  })

  test('executes and closes the complete shipped native Go matrix in PR and manual workflows', () => {
    const release = workflowJobs(readFileSync('.github/workflows/release-desktop.yml', 'utf8'))
    const targets = (release.build.strategy?.matrix?.include ?? []).map((entry: any) => entry.target_triple)
    expect(targets.sort()).toEqual(['aarch64-apple-darwin', 'x86_64-apple-darwin', 'aarch64-unknown-linux-gnu', 'x86_64-unknown-linux-gnu', 'aarch64-pc-windows-msvc', 'x86_64-pc-windows-msvc'].sort())
    for (const path of ['.github/workflows/pr-quality.yml', '.github/workflows/nightly-quality.yml']) {
      const jobs = workflowJobs(readFileSync(path, 'utf8'))
      const matrix = jobs['go-platform-checks']
      expect(matrix.strategy?.['fail-fast']).toBe(false)
      expect(matrix.strategy?.matrix?.include).toEqual(GO_PLATFORMS.map(platform => ({ id: platform.target.replace('/', '-'), target: platform.target, runner: platform.runner })))
      expect(matrix['runs-on']).toBe('${{ matrix.runner }}')
      expect(matrix.steps?.find(step => step.uses === 'actions/upload-artifact@v4')?.with?.name).toBe('go-checks-${{ matrix.id }}')
      const closure = jobs['go-checks']
      for (const job of [matrix, closure]) expect(job.steps?.find(step => step.uses === 'actions/checkout@v4')?.env).toEqual({ GIT_CONFIG_COUNT: '1', GIT_CONFIG_KEY_0: 'core.autocrlf', GIT_CONFIG_VALUE_0: 'false' })
      expect(closure.needs).toContain('go-platform-checks')
      expect(closure.if).toContain('always()')
      expect(closure.steps?.some(step => step.run?.includes('test "$GO_PLATFORM_RESULT" = success'))).toBe(true)
      expect(closure.steps?.some(step => step.run?.includes('bun run check:go-matrix --reports-dir'))).toBe(true)
      expect(closure.steps?.find(step => step.uses === 'actions/download-artifact@v4')?.with).toEqual({ pattern: 'go-checks-*', path: 'artifacts/go-platform-reports/' })
      expect(matrix['continue-on-error']).not.toBe(true)
      expect(closure['continue-on-error']).not.toBe(true)
    }
  })

  test.each([
    { selected: true, result: 'success', exit: 0 },
    { selected: true, result: 'failure', exit: 1 },
    { selected: true, result: 'cancelled', exit: 1 },
    { selected: true, result: 'skipped', exit: 1 },
    { selected: false, result: 'skipped', exit: 0 },
    { selected: false, result: 'success', exit: 1 },
  ])('enforces selected Go and parity results in the stable gate: %j', scenario => {
    const jobs = workflowJobs(readFileSync('.github/workflows/pr-quality.yml', 'utf8'))
    const gateScript = jobs['pr-quality-gate'].steps!.find(step => step.run?.includes('require_selected'))!.run!
    const script = gateScript.replace(/\$\{\{\s*([^}]+?)\s*\}\}/g, (_match, expression: string) => {
      if (expression === 'needs.scope-plan.outputs.go_checks' || expression === 'needs.scope-plan.outputs.go_parity_checks') return String(scenario.selected)
      if (expression.startsWith('needs.scope-plan.outputs.')) return 'false'
      if (expression === 'needs.go-checks.result' || expression === 'needs.go-parity.result') return scenario.result
      if (expression === 'needs.scope-plan.result' || expression === 'needs.policy-enforcement.result') return 'success'
      return 'skipped'
    })
    const result = Bun.spawnSync(['bash', '-c', script], { env: { PATH: process.env.PATH ?? '' }, stdout: 'pipe', stderr: 'pipe' })
    expect(result.exitCode, new TextDecoder().decode(result.stdout)).toBe(scenario.exit)
  })

  test('requires macOS Swift checks alongside the selected Linux native packaging lane', () => {
    const jobs = workflowJobs(readFileSync('.github/workflows/pr-quality.yml', 'utf8'))
    const packageJson = JSON.parse(readFileSync('package.json', 'utf8')) as { scripts: Record<string, string> }
    const macos = jobs['macos-swift-checks']!
    const linux = jobs['desktop-native-checks']!
    const gate = jobs['pr-quality-gate']!
    expect(macos['runs-on']).toBe('macos-latest')
    expect(linux['runs-on']).toBe('ubuntu-22.04')
    for (const job of [macos, linux]) {
      expect(job.needs).toBe('scope-plan')
      expect(job.if).toBe("needs.scope-plan.outputs.desktop_native_checks == 'true'")
      expect(job['continue-on-error']).not.toBe(true)
    }
    expect(macos.steps?.some(step => step.run === 'bun run check:swift')).toBe(true)
    expect(linux.steps?.some(step => step.run === 'bun run check:native')).toBe(true)
    expect(gate.needs).toContain('macos-swift-checks')
    expect(gate.needs).toContain('desktop-native-checks')
    expect(packageJson.scripts['check:swift']).toBe('bun run scripts/pr/run-swift-checks.ts')
    expect(packageJson.scripts['check:policy']).toContain('scripts/pr/run-swift-checks.test.ts')
    expect(packageJson.scripts['check:policy']).toContain('scripts/quality-gate/package-smoke/index.test.ts')
    for (const command of ['check:swift', 'build:sidecars', 'test:compiled-sidecar-smoke', 'check:electron', 'electron:package:dir', 'test:package-smoke:current']) {
      expect(packageJson.scripts['check:native']).toContain(`bun run ${command}`)
    }
  })

  test.each([
    { selected: true, macos: 'success', linux: 'success', exit: 0 },
    { selected: true, macos: 'failure', linux: 'success', exit: 1 },
    { selected: true, macos: 'skipped', linux: 'success', exit: 1 },
    { selected: true, macos: 'success', linux: 'failure', exit: 1 },
    { selected: false, macos: 'skipped', linux: 'skipped', exit: 0 },
    { selected: false, macos: 'success', linux: 'skipped', exit: 1 },
  ])('enforces both native results in the stable gate: %j', scenario => {
    const jobs = workflowJobs(readFileSync('.github/workflows/pr-quality.yml', 'utf8'))
    const gateScript = jobs['pr-quality-gate']!.steps!.find(step => step.run?.includes('require_selected'))!.run!
    const script = gateScript.replace(/\$\{\{\s*([^}]+?)\s*\}\}/g, (_match, expression: string) => {
      if (expression === 'needs.scope-plan.outputs.desktop_native_checks') return String(scenario.selected)
      if (expression.startsWith('needs.scope-plan.outputs.')) return 'false'
      if (expression === 'needs.macos-swift-checks.result') return scenario.macos
      if (expression === 'needs.desktop-native-checks.result') return scenario.linux
      if (expression === 'needs.scope-plan.result' || expression === 'needs.policy-enforcement.result') return 'success'
      return 'skipped'
    })
    const result = Bun.spawnSync(['bash', '-c', script], {
      env: { PATH: process.env.PATH ?? '' },
      stdout: 'pipe', stderr: 'pipe',
    })
    expect(result.exitCode, new TextDecoder().decode(result.stdout)).toBe(scenario.exit)
  })

  test('keeps coverage artifacts observable in CI', () => {
    const workflow = readFileSync('.github/workflows/pr-quality.yml', 'utf8')

    expect(workflow).toContain('COVERAGE_BASE_REF: origin/${{ github.base_ref }}')
    expect(workflow).toContain('cat "$latest_report" >> "$GITHUB_STEP_SUMMARY"')
    expect(workflow).toContain('uses: actions/upload-artifact@v4')
    expect(workflow).toContain('path: artifacts/coverage/')
    expect(workflow).toContain('retention-days: 14')
  })

  test('installs and validates the isolated React site for docs changes', () => {
    const workflow = readFileSync('.github/workflows/pr-quality.yml', 'utf8')
    const jobs = workflowJobs(workflow)
    const docsSteps = jobs['docs-checks'].steps ?? []
    const runDocs = docsSteps.find((step) => step.name === 'Run docs checks')

    expect(workflow).toContain('cache-dependency-path: site/package-lock.json')
    expect(runDocs?.run).toBe(
      'npm --prefix site ci && npm --prefix site run build && npm --prefix site run check',
    )
    expect(workflow).not.toContain('vitepress')
  })

  test('keeps required PR checks deterministic and secret-free', () => {
    const workflow = readFileSync('.github/workflows/pr-quality.yml', 'utf8')

    expect(workflow).not.toContain('--allow-live')
    expect(workflow).not.toContain('QUALITY_GATE_PROVIDER_API_KEY')
    expect(workflow).not.toContain('secrets.')
    expect(workflow).not.toContain('pull_request_target')
    expect(workflow.match(/uses: actions\/checkout@v4/g)?.length).toBeGreaterThan(0)
    expect(workflow.match(/persist-credentials: false/g)?.length).toBe(
      workflow.match(/uses: actions\/checkout@v4/g)?.length,
    )
  })

  test('exposes a single required gate job for branch protection', () => {
    const workflow = readFileSync('.github/workflows/pr-quality.yml', 'utf8')

    expect(workflow).toContain('pr-quality-gate:')
    expect(workflow).toContain('name: pr-quality-gate')
    expect(workflow).toContain('if: always()')
    expect(workflow).toContain('require_success "scope-plan" "${{ needs.scope-plan.result }}"')
    expect(workflow).toContain('require_success "policy-enforcement" "${{ needs.policy-enforcement.result }}"')
    expect(workflow).toContain('require_selected "provider-contract-checks"')
    expect(workflow).toContain('require_selected "chat-contract-checks"')
    expect(workflow).toContain('require_selected "agent-flow-checks"')
    expect(workflow).toContain('require_selected "coverage-checks"')
  })

  test('routes the deterministic agent flow through the scope plan and keeps its evidence', () => {
    const workflow = readFileSync('.github/workflows/pr-quality.yml', 'utf8')
    const jobs = workflowJobs(workflow)
    const steps = jobs['agent-flow-checks'].steps ?? []

    expect(workflow).toContain('agent_flow_checks: ${{ steps.policy.outputs.agent_flow_checks }}')
    expect(steps.some((step) => step.run === 'bun run check:agent-flow')).toBe(true)
    expect(workflow).toContain('path: artifacts/agent-flow/')
    // The lane must stay runnable on an untrusted fork: no desktop install, no
    // secrets, no live provider.
    expect(steps.some((step) => String(step.run ?? '').includes('secrets'))).toBe(false)
  })
})

describe('full quality workflow', () => {
  test('runs every deterministic lane on demand without secrets or live providers', () => {
    const workflow = readFileSync('.github/workflows/nightly-quality.yml', 'utf8')
    const jobs = workflowJobs(workflow)
    const runs = (jobs['full-deterministic'].steps ?? []).map((step) => step.run ?? '')

    expect(workflow).toContain('workflow_dispatch:')
    // Manual only, and it stays that way. This sweep costs about ninety minutes of
    // CI; when to spend that is the maintainer's decision, so a schedule must not
    // reappear here without one.
    expect(workflow).not.toContain('schedule:')
    expect(workflow).not.toContain('cron:')
    expect(runs).toContain('bun run prepare:go')
    expect(runs.indexOf('bun run prepare:go')).toBeLessThan(runs.indexOf('bun run check:go'))
    expect(runs.indexOf('bun run prepare:go')).toBeLessThan(runs.indexOf('bun run check:go-parity'))
    for (const command of [
      'bun run check:policy',
      'bun run check:go',
      'bun run check:go-parity',
      'bun run check:agent-flow',
      'bun run check:server',
      'bun run check:provider-contract',
      'bun run check:chat-contract',
      'bun run check:adapters',
      'bun run check:desktop',
      'bun run check:electron',
      'bun run check:persistence-upgrade',
      'bun run check:quarantine',
      'bun run check:coverage',
    ]) {
      expect(runs, `full sweep must run ${command}`).toContain(command)
    }

    expect(workflow).not.toContain('--allow-live')
    expect(workflow).not.toContain('secrets.')
    expect(workflow.match(/persist-credentials: false/g)?.length).toBe(
      workflow.match(/uses: actions\/checkout@v4/g)?.length,
    )
  })

  test('re-proves the dependency graph so PR selection cannot silently degrade', () => {
    const workflow = readFileSync('.github/workflows/nightly-quality.yml', 'utf8')
    const jobs = workflowJobs(workflow)
    const runs = (jobs['selection-drift'].steps ?? []).map((step) => step.run ?? '')

    expect(runs.some((run) => run.includes('module-graph.test.ts'))).toBe(true)
    expect(runs.some((run) => run.includes('check:impact'))).toBe(true)
  })
})

describe('docs deployment workflow', () => {
  test('builds and uploads the isolated React site', () => {
    const workflow = readFileSync('.github/workflows/deploy-docs.yml', 'utf8')

    expect(workflow).toContain("      - 'site/**'")
    expect(workflow).toContain('cache-dependency-path: site/package-lock.json')
    expect(workflow).toContain('run: npm --prefix site ci')
    expect(workflow).toContain('run: npm --prefix site run build')
    expect(workflow).toContain('path: site/dist')
    expect(workflow).not.toContain('vitepress')
    expect(workflow).not.toContain('docs/.vitepress/dist')
  })
})
