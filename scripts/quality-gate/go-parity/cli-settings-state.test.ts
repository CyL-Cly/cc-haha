import { afterAll, beforeAll, describe, expect, test } from 'bun:test'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { settingsStateCases, settingsStateValues, settingsStateRecords, settingsStateSourceKeys, settingsStatePathKeys } from './cli-settings-state-fixtures'
import { assertTraceEqual } from './trace'

const root = fileURLToPath(new URL('../../../', import.meta.url)), allocationRoot = tmpdir()
let buildHome: string, executable: string
const originalInput = { cases: settingsStateCases, values: settingsStateValues, records: settingsStateRecords, sourceKeys: settingsStateSourceKeys, pathKeys: settingsStatePathKeys }

export async function recordSettingsState(kind: 'typescript' | 'go' = 'typescript') {
  const home = await mkdtemp(join(allocationRoot, 'cli-settings-state-case-'))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    child = Bun.spawn(kind === 'typescript' ? [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/cli-settings-state.fixture.ts')] : [executable, '-test.run=^TestSettingsStateFixture$'], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_SETTINGS_STATE_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot }),
      stdin: new Blob([JSON.stringify({ home, ...originalInput })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill(), 15_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stderr).toBe(0)
      expect(stderr).toBe('')
      const raw = kind === 'typescript' ? stdout : stdout.split('\n').find(line => line.startsWith('SETTINGS_STATE_RESULT:'))?.slice('SETTINGS_STATE_RESULT:'.length)
      expect(raw).toBeDefined()
      const result = JSON.parse(raw!)
      expect(result.observations).toHaveLength(5)
      const directory = join(root, 'artifacts/go-parity/cli-settings-state')
      await mkdir(directory, { recursive: true })
      await writeFile(join(directory, `${kind}.json`), JSON.stringify({ input: originalInput, result }, null, 2) + '\n')
      return result
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
}

describe('CLI settings actual Source cache and internal-write state', () => {
  beforeAll(async () => {
    buildHome = await mkdtemp(join(allocationRoot, 'cli-settings-state-build-'))
    executable = join(buildHome, 'cli-settings-state' + (process.platform === 'win32' ? '.exe' : ''))
    const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/clisettings'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child.kill(), 180_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stdout + stderr).toBe(0)
    } finally { clearTimeout(timer) }
  }, 190_000)
  afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

  test('self replay and independent Go original-input state transitions', async () => {
    const source = await recordSettingsState()
    const trace = (result: unknown) => [{ kind: 'state' as const, value: result }]
    assertTraceEqual(trace(source), trace(await recordSettingsState()))
    assertTraceEqual(trace(source), trace(await recordSettingsState('go')))
    const first = source.observations[0].steps
    expect(first[0].source[0].value.kind).toBe('undefined')
    expect(first[1].source[0].value.kind).toBe('null')
    expect(first[2].plugin.kind).toBe('object')
    expect(first[3].plugin.kind).toBe('undefined')
  }, 50_000)

  for (const mutation of ['null-miss', 'plugin-reset', 'window-equality', 'clock-count']) test(`comparator rejects ${mutation}`, async () => {
    const source = await recordSettingsState(), changed = structuredClone(source)
    if (mutation === 'null-miss') changed.observations[0].steps[1].source[0].value.kind = 'undefined'
    else if (mutation === 'plugin-reset') changed.observations[0].steps[2].plugin = { kind: 'undefined' }
    else if (mutation === 'window-equality') changed.observations[3].steps[1].result = true
    else changed.observations[4].nowCalls++
    expect(() => assertTraceEqual([{ kind: 'state', value: source }], [{ kind: 'state', value: changed }])).toThrow('First semantic difference')
  })

  for (const fault of ['home', 'extra-input']) test(`guard before Source imports ${fault}`, async () => {
    const home = await mkdtemp(join(allocationRoot, 'cli-settings-state-guard-'))
    try {
      const input: any = { home, ...originalInput }
      if (fault === 'home') input.home += '-outside'
      else input.unrecognized = true
      const child = Bun.spawn([process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/cli-settings-state.fixture.ts')], { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_SETTINGS_STATE_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot }), stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe' })
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code).not.toBe(0)
      expect(stdout).toBe('')
      expect(stderr).toContain(fault === 'home' ? 'requires disposable HOME/config before production imports' : 'private fixture before production imports')
    } finally { await rm(home, { recursive: true, force: true }) }
  })
})
