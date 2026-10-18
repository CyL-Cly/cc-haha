import { expect, test } from 'bun:test'
import { mkdtemp, realpath, rm } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'

test('actual original record Promise retains identity only with a synchronous read-only adapter', async () => {
  expect(Bun.version).toBe('1.3.14')
  const home = await realpath(await mkdtemp(join(tmpdir(), 'cc-haha-original-record-identity-')))
  const fixture = fileURLToPath(new URL('./native-transcript-observer-identity.fixture.ts', import.meta.url))
  const child = Bun.spawn([process.execPath, '--no-env-file', fixture], {
    cwd: home,
    env: { HOME: home, USERPROFILE: home, CLAUDE_CONFIG_DIR: join(home, '.claude'), TMPDIR: home, CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), BUN_OPTIONS: '--no-env-file', CC_HAHA_ORIGINAL_OBSERVER_IDENTITY: '1', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', DISABLE_TELEMETRY: '1', PATH: home },
    stdin: new Blob([JSON.stringify({ home })]), stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 15000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, stderr || stdout).toBe(0)
    expect(stderr).toBe('')
    expect(JSON.parse(stdout)).toEqual({ identities: [
      { profile: 'sync', samePromise: true, statuses: ['fulfilled', 'fulfilled'] },
      { profile: 'async', samePromise: false, statuses: ['pending', 'fulfilled'] },
    ], persisted: true, blocked: [] })
  } finally { clearTimeout(timer); child.kill(); await child.exited; await rm(home, { recursive: true, force: true }) }
}, 20000)
