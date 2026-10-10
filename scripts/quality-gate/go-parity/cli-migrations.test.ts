import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { basename, join, resolve } from 'node:path'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { cliMigrationsProfiles, type CLIMigrationsProfile } from './cli-migrations-fixtures'

const root = resolve(import.meta.dir, '../../..')
const sourceOnly = process.env.CC_HAHA_CLI_MIGRATIONS_SOURCE_ONLY === '1'
let buildHome: string, executable: string

beforeAll(async () => {
  if (Bun.version !== '1.3.14') throw new Error('Migrations require pinned Bun1.3.14')
  if (sourceOnly) return
  buildHome = await mkdtemp(join(tmpdir(), 'cli-migrations-build-'))
  executable = join(buildHome, 'migrations.test')
  const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/climigrations'], {
    cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome),
    stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const [code, out, err] = await Promise.all([
    child.exited, new Response(child.stdout).text(), new Response(child.stderr).text(),
  ])
  expect(code, out + err).toBe(0)
}, 180000)

afterAll(async () => {
  if (buildHome) await rm(buildHome, { recursive: true, force: true })
})

async function record(source: boolean, profile: CLIMigrationsProfile) {
  const home = await mkdtemp(join(tmpdir(), 'cli-migrations-parity-'))
  try {
    const env = createSandboxedTestEnvironment(home, {
      CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', USER_TYPE: 'external',
      CLAUDE_CODE_ENABLE_XAA: '0', CLAUDE_CODE_SIMPLE: '1',
      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', DISABLE_TELEMETRY: '1',
      ANTHROPIC_API_KEY: 'fixture-not-a-secret', CC_HAHA_CLI_MIGRATIONS_FIXTURE: '1',
    })
    const command = source
      ? [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/cli-migrations.fixture.ts')]
      : [executable, '-test.run=^TestCLIMigrationsFixture$']
    const child = Bun.spawn(command, {
      cwd: home, env, stdin: new Blob([JSON.stringify({ ...profile, home })]),
      stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill(), 20000)
    try {
      const [code, out, err] = await Promise.all([
        child.exited, new Response(child.stdout).arrayBuffer(), new Response(child.stderr).arrayBuffer(),
      ])
      const stdout = Buffer.from(out).toString('utf8'), stderr = Buffer.from(err).toString('utf8')
      const dir = join(root, 'artifacts/go-parity/cli-migrations-raw')
      await mkdir(dir, { recursive: true })
      await writeFile(join(dir, basename(home) + (source ? '-source' : '-go') + '.json'), JSON.stringify({
        profile: profile.name, home, command, env, code,
        stdoutBase64: Buffer.from(out).toString('base64'), stderrBase64: Buffer.from(err).toString('base64'),
      }, null, 2) + '\n')
      expect(code, stdout + stderr).toBe(0)
      expect(stderr).toBe('')
      const raw = JSON.parse(stdout.replace(/\nPASS\n$/, ''))
      if (source) {
        expect(raw.capabilities.blocked).toEqual([])
        expect(raw.sourceOnly.bindings.map((binding: any) => binding.local)).toContain('resetAutoModeOptInForDefaultOffer')
        expect(raw.sourceOnly.extracted).toContain('resetAutoModeOptInForDefaultOffer')
      }
      const normalize = (value: any): any => {
        if (typeof value === 'string') return value.replaceAll(home, '<HOME>')
        if (Array.isArray(value)) return value.map(normalize)
        if (value && typeof value === 'object') {
          const output: any = {}
          for (const [key, item] of Object.entries(value)) {
            if (key === 'rawBase64' && typeof item === 'string') {
              output[key] = Buffer.from(Buffer.from(item, 'base64').toString('latin1').replaceAll(home, '<HOME>'), 'latin1').toString('base64')
            } else output[key.replaceAll(home, '<HOME>')] = normalize(item)
          }
          return output
        }
        return value
      }
      return normalize({ name: raw.name, result: raw.result })
    } finally { clearTimeout(timer) }
  } finally { await rm(home, { recursive: true, force: true }) }
}

for (const profile of cliMigrationsProfiles) {
  test('CLI migrations actual Source self: ' + profile.name, async () => {
    const source = await record(true, profile)
    if (!profile.schema) {
      const initial = JSON.parse(profile.global ?? '{}')
      expect(source.result.error).toBe(profile.fault === 'global' && initial.migrationVersion !== 11 ? 'Error: private fixture write fault' : null)
      const starts = source.result.effects.filter((event: any) => event.kind === 'migrationStart')
      if (initial.migrationVersion !== 11) {
        if (profile.fault === 'global') expect(starts.length).toBeGreaterThan(0)
        else expect(starts).toHaveLength(10)
      } else expect(starts).toHaveLength(0)
    }
    expect(source).toEqual(await record(true, profile))
  }, 60000)
  ;(sourceOnly ? test.skip : test)('CLI migrations independent Go: ' + profile.name, async () => {
    expect(await record(false, profile)).toEqual(await record(true, profile))
  }, 60000)
}

test.each(['file bytes', 'marker order', 'updater repeat', 'UTF16 identity', 'return-error continuation', 'async split'])(
  'migration comparator rejects %s', async mutation => {
    const name = mutation === 'updater repeat' ? 'global-failure-partial-no-marker'
      : mutation === 'UTF16 identity' ? 'mcp-merge-local-first'
      : mutation === 'return-error continuation' ? 'user-return-error-continues'
      : mutation === 'async split' ? 'numeric-eleven-async' : 'false-auto-updates'
    const expected = await record(true, cliMigrationsProfiles.find(profile => profile.name === name)!)
    const changed = structuredClone(expected)
    if (mutation === 'file bytes') changed.result.final.files['.claude/.claude.json'].rawBase64 += 'AA=='
    else if (mutation === 'marker order') changed.result.effects.reverse()
    else if (mutation === 'updater repeat') changed.result.effects.splice(changed.result.effects.findIndex((event: any) => event.kind === 'updaterGlobal'), 1)
    else if (mutation === 'UTF16 identity') {
      const event = changed.result.effects.find((event: any) => event.kind === 'settingsUpdate')
      event.patch = event.patch.replace('\\ud800', '�')
    } else if (mutation === 'return-error continuation') changed.result.final.disableAutoUpdater = null
    else changed.result.sync = structuredClone(changed.result.complete)
    expect(() => expect(changed).toEqual(expected)).toThrow()
  },
)

test.each(['home', 'config', 'extra-input'])('migration fixture guard before imports %s', async fault => {
  const home = await mkdtemp(join(tmpdir(), 'cli-migrations-guard-'))
  try {
    const loader = join(home, 'guard.ts')
    await writeFile(loader, `import {plugin} from 'bun'\nplugin({name:'tripwire',setup(build){build.onLoad({filter:/[\\\\/]src[\\\\/].*\\.(?:ts|tsx|js)$/},()=>{process.stdout.write('PRODUCTION_REACHED');return {contents:'throw new Error("production reached")',loader:'ts'}})}})\nawait import(${JSON.stringify(join(root, 'scripts/quality-gate/go-parity/cli-migrations.fixture.ts'))})`)
    const env = createSandboxedTestEnvironment(home, {
      CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), CC_HAHA_CLI_MIGRATIONS_FIXTURE: '1', NODE_ENV: 'production', USER_TYPE: 'external',
      ...(fault === 'home' ? { HOME: home + '/wrong' } : {}),
      ...(fault === 'config' ? { CLAUDE_CONFIG_DIR: home + '/wrong' } : {}),
    })
    const child = Bun.spawn([process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', loader], {
      cwd: home, env, stdin: new Blob([JSON.stringify({ home, name: 'guard', ...(fault === 'extra-input' ? { extra: true } : {}) })]),
      stdout: 'pipe', stderr: 'pipe',
    })
    const [code, out, err] = await Promise.all([
      child.exited, new Response(child.stdout).text(), new Response(child.stderr).text(),
    ])
    expect(code).not.toBe(0)
    expect(out).not.toContain('PRODUCTION_REACHED')
    expect(err).toContain(fault === 'extra-input' ? 'declared private inputs' : 'disposable HOME/config before production imports')
  } finally { await rm(home, { recursive: true, force: true }) }
})

if (process.env.CC_HAHA_CAPTURE_CLI_MIGRATIONS === '1') {
  const rows = []
  for (const input of cliMigrationsProfiles) rows.push({ input, expected: await record(true, input) })
  await writeFile(join(root, 'backend/internal/climigrations/testdata/contract.json'), JSON.stringify(rows, null, 2) + '\n')
}
