import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdtemp, readFile, readdir, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'

const root = resolve(import.meta.dir, '../../..')
let home: string, executable: string, customExecutable: string
beforeAll(async () => {
  home = await mkdtemp(join(tmpdir(), 'go-cli-version-build-'))
  executable = join(home, 'cli')
  customExecutable = join(home, 'custom-cli')
  const env = await createGoBuildEnvironment(root, home)
  for (const [target, version] of [[executable, '999.0.0-local'], [customExecutable, '2.9.8-fixture+test']]) {
    const child = Bun.spawn(['go', 'build', '-ldflags', '-X main.version='+version, '-o', target, './cmd/claude-haha'], {
      cwd: join(root, 'backend'), env, stdout: 'pipe', stderr: 'pipe', stdin: 'ignore',
    })
    const timer = setTimeout(() => child.kill(), 170_000)
    try {
      const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
      expect(code, stderr).toBe(0)
    } finally { clearTimeout(timer) }
  }
}, 360_000)
afterAll(async () => { if (home) await rm(home, { recursive: true, force: true }) })

async function run(source: boolean, args: string[], version: string) {
  const fixtureHome = await mkdtemp(join(tmpdir(), 'cli-version-parity-'))
  try {
    const env = createSandboxedTestEnvironment(fixtureHome, {
      CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), CLAUDE_CODE_LOCAL_BUILD_TIME: '2000-01-01T00:00:00.000Z',
      CLAUDE_CODE_LOCAL_VERSION: version, CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH: '1',
    })
    const before = await readdir(join(fixtureHome, '.config'))
    const child = Bun.spawn(source ? [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/cli-version.fixture.ts')] : [version === '999.0.0-local' ? executable : customExecutable, ...args], {
      cwd: fixtureHome, env, stdin: source ? new Blob([JSON.stringify({ home: fixtureHome, args })]) : 'ignore', stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill(), 10_000)
    try {
      const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(await readdir(join(fixtureHome, '.config'))).toEqual(before)
      expect(await readdir(fixtureHome)).not.toContain('.claude')
      let evidence: any
      if (source) evidence = JSON.parse(await readFile(join(fixtureHome, 'version-evidence.json'), 'utf8'))
      return { exitCode, stdout, stderr, evidence }
    } finally { clearTimeout(timer) }
  } finally { await rm(fixtureHome, { recursive: true, force: true }) }
}

for (const version of ['999.0.0-local', '2.9.8-fixture+test']) {
  for (const flag of ['--version', '-v', '-V']) {
    test('actual CLI fast path preserves output and avoids configuration/production module loading: '+version+' '+flag, async () => {
      const source = await run(true, [flag], version), repeated = await run(true, [flag], version), candidate = await run(false, [flag], version)
      expect(source.evidence.blocked).toEqual([])
      expect(source.evidence.secureReads).toEqual([])
      expect(source.evidence.absentReads).toEqual([])
      expect(source.evidence.loaded).toEqual(['src/entrypoints/cli.tsx'])
      expect(source).toEqual(repeated)
      expect({ ...candidate, evidence: undefined }).toEqual({ ...source, evidence: undefined })
      expect(source.exitCode).toBe(0)
      expect(source.stderr).toBe('')
      expect(source.stdout).toBe(version+' (Claude Code)\n')
    }, 30_000)
  }
}
