import { expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const fixtures = ['storage', 'settings', 'migrations'] as const
const cases = fixtures.flatMap(fixture => ['input', 'home', 'config', 'cwd', 'config-symlink', 'config-dangling', 'config-file'].map(boundary => [fixture, boundary] as const))
const tripwire = `
import { plugin } from 'bun'
plugin({ name: 'production-import-tripwire', setup(build) {
  build.onLoad({ filter: /[\\\\/]src[\\\\/].*\\.(?:ts|tsx|js)$/ }, args => {
    process.stdout.write('PRODUCTION_IMPORT_REACHED\\n')
    return { contents: 'throw new Error("production import tripwire")', loader: 'ts' }
  })
}})
await import(process.env.CC_HAHA_GUARD_FIXTURE)
`

test.each(cases)('invalid %s fixture %s fails before any production module is loaded', async (fixture, boundary) => {
  const home = await mkdtemp(join(tmpdir(), 'go-fixture-guard-'))
  try {
    const loader = join(home, 'guard-loader.ts')
    await writeFile(loader, tripwire)
    const sentinel = join(home, 'state-sentinel.json')
    await writeFile(sentinel, '{"protected":"unchanged"}')
    if (boundary === 'config-symlink') {
      await mkdir(join(home, 'config-target'))
      await symlink(join(home, 'config-target'), join(home, '.claude'), 'dir')
    }
    if (boundary === 'config-dangling') await symlink(join(home, 'missing-config'), join(home, '.claude'), 'dir')
    if (boundary === 'config-file') await writeFile(join(home, '.claude'), '{}')
    const child = Bun.spawn([process.execPath, '--no-env-file', loader], {
      cwd: boundary === 'cwd' ? root : home,
      env: createSandboxedTestEnvironment(home, {
        CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(),
        CC_HAHA_GUARD_FIXTURE: join(root, `scripts/quality-gate/go-parity/${fixture}.fixture.ts`),
        ...(boundary === 'home' ? { HOME: join(home, 'not-the-input-home') } : {}),
        ...(boundary === 'config' ? { CLAUDE_CONFIG_DIR: join(home, 'wrong-config') } : {}),
      }),
      stdin: new Blob([JSON.stringify({ homeDir: boundary === 'input' ? '' : home, path: join(home, boundary === 'input' ? 'wrong.json' : '.claude/settings.json'), migrationScope: boundary === 'input' ? 'wrong' : 'current-settings-fixture', steps: [] })]),
      stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill('SIGKILL'), 5000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code).not.toBe(0)
      expect(stdout).not.toContain('PRODUCTION_IMPORT_REACHED')
      expect(stderr).toMatch(/fixture requires|Storage fixture requires|Settings fixture requires|Migration fixture requires/)
      expect(await readFile(sentinel, 'utf8')).toBe('{"protected":"unchanged"}')
    } finally { clearTimeout(timer); child.kill(); await child.exited }
  } finally { await rm(home, { recursive: true, force: true }) }
}, 10_000)
