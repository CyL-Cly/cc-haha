import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, readdir, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { probeCli } from './cliInventory'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
let buildHome: string, executable: string
beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'go-clibootstrap-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'fixture.exe' : 'fixture')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/clibootstrap/testdata/fixture'], {
    cwd: resolve(rootDir, 'backend'), env: await createGoBuildEnvironment(rootDir, buildHome),
    stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill(), 180_000)
  try {
    const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
    expect(code, stderr).toBe(0)
  } finally { clearTimeout(timer) }
}, 190_000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

async function preparedFiles(home: string) {
  return Promise.all((await readdir(join(home, 'tmp'))).filter(file => file.startsWith('claude-settings-')).sort().map(async name => ({
    name, bytes: (await readFile(join(home, 'tmp', name))).toString('base64'),
  })))
}
async function candidate(home: string, args: string[], env: Record<string, string> = {}) {
  const child = Bun.spawn([executable], {
    cwd: home, env: createSandboxedTestEnvironment(home, env),
    stdin: new Blob([JSON.stringify({ home, args })]), stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill(), 20_000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, stderr).toBe(0)
    return JSON.parse(stdout)
  } finally { clearTimeout(timer) }
}

const cases: string[][] = [
  [], ['--settings', '{}'], ['--settings={ "env": { "SAFE_BOOTSTRAP_MARKER": "from flag" }, "unknown": 1 }'],
  ['--settings', '\ufeff\u00a0{"unicode":"💡"}\u2028'], ['--settings', '{invalid}'],
  ['--settings', '[]'], ['--settings', 'null'], ['--settings', '{"a":1} trailing'],
  ['--settings', 'fixtures/settings.json'], ['--settings=fixtures/invalid.json'],
  ['--settings', 'fixtures/BOM.json'], ['--settings', 'fixtures/empty.json'],
  ['--settings', 'fixtures/missing.json'], ['--settings', ' fixtures/settings.json '],
  ['--settings'], ['--settings', '--help'], ['--settings=', '--settings', '{"second":true}'],
  ['--settings', '{"first":true}', '--settings', '{"second":true}'],
  ['--settings', 'fixtures/settings.json', '--settings', 'fixtures/missing.json'],
  ['--', '--settings', '{}'], ['--setting-sources', ''], ['--setting-sources='],
  ['--setting-sources', 'project,user,local,user'], ['--setting-sources', '\ufeffuser\u00a0, project ,local'],
  ['--setting-sources', 'USER'], ['--setting-sources', ' '], ['--setting-sources', 'user,'],
  ['--setting-sources'], ['--setting-sources', '--help'],
  ['--setting-sources=user', '--setting-sources=local'], ['--', '--setting-sources=project'],
  ['--setting-sources=invalid', '--settings', '{"beforeSourceError":true}'],
  ['--setting-sources=user', '--settings', '{invalid}'],
  ['--help', '--settings', '{invalid}'], ['--unknown', '--setting-sources=local'],
  ['--settings', 'fixtures/directory'],
  ['--settings', 'fixtures/settings.json/child'],
]
for (const args of cases) {
  test('actual pre-Commander startup settings/stdio/files/order parity: ' + JSON.stringify(args), async () => {
    const home = await mkdtemp(join(tmpdir(), 'cli-bootstrap-parity-'))
    try {
      createSandboxedTestEnvironment(home)
      await mkdir(join(home, 'fixtures/directory'), { recursive: true })
      for (const [name, bytes] of [
        ['settings.json', '{"env":{"SAFE_BOOTSTRAP_MARKER":"unchanged"},"unknown":7}'],
        ['invalid.json', 'not valid JSON'], ['BOM.json', '\ufeff{}'], ['empty.json', ''],
      ]) await writeFile(join(home, 'fixtures', name), bytes)
      const reference = await probeCli(args, { home, mode: 'bootstrap' })
      const tsFiles = await preparedFiles(home)
      await rm(join(home, 'tmp'), { recursive: true, force: true })
      const result = await candidate(home, args)
      expect({ exitCode: result.exitCode, stdout: result.stdout, stderr: result.stderr })
        .toEqual({ exitCode: reference.exitCode, stdout: reference.stdout, stderr: reference.stderr })
      expect({ state: result.state, events: result.events, environment: result.environment }).toEqual(reference.bootstrap)
      expect(await preparedFiles(home)).toEqual(tsFiles)
      expect(reference.parsed).toBeNull()
      expect(reference.blocked.every(value => value.startsWith('subprocess:'))).toBe(true)
      expect((await readFile(join(home, 'fixtures/settings.json'))).toString()).toBe('{"env":{"SAFE_BOOTSTRAP_MARKER":"unchanged"},"unknown":7}')
    } finally { await rm(home, { recursive: true, force: true }) }
  }, 25_000)
}

test('inline content paths are stable across actual TS and Go processes, while existing symlinks use the actual canonical path', async () => {
  const home = await mkdtemp(join(tmpdir(), 'cli-bootstrap-stable-path-'))
  try {
    createSandboxedTestEnvironment(home)
    const args = ['--settings', '  {"unicode":"💡","order":1}  ']
    const first = await probeCli(args, { home, mode: 'bootstrap' })
    const second = await probeCli(args, { home, mode: 'bootstrap' })
    expect(first.bootstrap).toEqual(second.bootstrap)
    expect((await candidate(home, args)).state).toEqual(first.bootstrap.state)
    await writeFile(join(home, 'target.json'), '{}')
    await symlink(join(home, 'target.json'), join(home, 'alias.json'))
    const source = await probeCli(['--settings', 'alias.json'], { home, mode: 'bootstrap' })
    const go = await candidate(home, ['--settings', 'alias.json'])
    expect({ state: go.state, events: go.events, environment: go.environment }).toEqual(source.bootstrap)
    expect(source.bootstrap.state.flagSettingsPath.endsWith('/target.json')).toBe(true)
  } finally { await rm(home, { recursive: true, force: true }) }
}, 25_000)

test('startup selects settings before init without applying either persisted or flag environment variables', async () => {
  const home = await mkdtemp(join(tmpdir(), 'cli-bootstrap-env-order-'))
  const env = { CC_HAHA_CLI_BOOTSTRAP_MARKER: 'initial child environment' }
  try {
    createSandboxedTestEnvironment(home)
    await mkdir(join(home, '.claude'), { recursive: true })
    const userSettings = '{"env":{"CC_HAHA_CLI_BOOTSTRAP_MARKER":"persisted user settings"},"unknown":true}'
    await writeFile(join(home, '.claude/settings.json'), userSettings)
    const args = ['--settings', '{"env":{"CC_HAHA_CLI_BOOTSTRAP_MARKER":"inline flag settings"}}', '--setting-sources=user']
    const reference = await probeCli(args, { home, env, mode: 'bootstrap' })
    const go = await candidate(home, args, env)
    expect({ state: go.state, events: go.events, environment: go.environment }).toEqual(reference.bootstrap)
    expect(go.environment.marker).toBe('initial child environment')
    expect((await readFile(join(home, '.claude/settings.json'))).toString()).toBe(userSettings)
    expect(reference.exitCode).toBe(0)
    expect(reference.parsed).toBeNull()
  } finally { await rm(home, { recursive: true, force: true }) }
}, 25_000)
