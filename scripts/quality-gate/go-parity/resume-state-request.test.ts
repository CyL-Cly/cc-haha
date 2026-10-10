import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, stat, utimes, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { stateCases, previousSessionId, type StateCase } from './resume-state-fixtures'
import { requestContextWire } from './request-context-fixtures'

const root = fileURLToPath(new URL('../../../', import.meta.url)), allocationRoot = tmpdir(), artifact = join(root, 'artifacts/go-parity/resume-state/first-request')
let buildHome: string, executable: string
beforeAll(async () => {
  buildHome = await mkdtemp(join(allocationRoot, 'resume-state-request-build-')); executable = join(buildHome, process.platform === 'win32' ? 'fixture.exe' : 'fixture')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/resumestate/testdata/fixture'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
  const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
  try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
}, 190_000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
async function initialize(row: StateCase) {
  const home = row.input.home as string
  for (const name of ['.claude', 'policy', 'private-plans', 'session.jsonl', 'resume-state-result.json']) await rm(join(home, name), { recursive: true, force: true })
  for (const directory of row.directories ?? []) await mkdir(directory, { recursive: true })
  const stats: Record<string, { modified: number, birth: number, size: number }> = {}
  for (const [path, value] of Object.entries({ ...row.files, [join(home, '.claude', '.claude.json')]: '{"userID":"fixture-resume-device"}' })) {
    await mkdir(dirname(path), { recursive: true }); await writeFile(path, value)
    if (row.times?.[path] !== undefined) await utimes(path, row.times[path] / 1000, row.times[path] / 1000)
    const info = await stat(path); stats[path] = { modified: info.mtime.getTime(), birth: info.birthtime.getTime(), size: info.size }
  }
  return stats
}
async function run(row: StateCase, go: boolean) {
  const home = row.input.home as string, stats = await initialize(row), socket: Array<{ path: string, method: string, headers: Array<[string, string]>, body: string }> = []
  const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) { const url = new URL(request.url); socket.push({ path: url.pathname + url.search, method: request.method, headers: [...request.headers], body: await request.text() }); return new Response(requestContextWire, { headers: { 'content-type': 'text/event-stream', 'request-id': 'resume-response' } }) } })
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    child = Bun.spawn(go ? [executable] : [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/resume-state.fixture.ts')], { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_RESUME_STATE_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, NODE_ENV: 'production', TZ: 'UTC', ANTHROPIC_API_KEY: 'offline-fixture-key', ANTHROPIC_BASE_URL: server.url.origin, CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1', CLAUDE_CODE_DISABLE_FAST_MODE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_CODE_MAX_RETRIES: '0', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0' }), stdin: new Blob([JSON.stringify({ ...row.input, stat: stats, request: { origin: server.url.origin, promptSource: JSON.stringify('next original prompt\ud800'), runtime: { platform: process.platform, arch: process.arch, version: process.version } } })]), stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 35_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      await mkdir(artifact, { recursive: true }); await writeFile(join(artifact, row.name + (go ? '-go' : '-source') + '-stderr.txt'), stderr)
      expect(code, stderr).toBe(0); expect(stderr).toBe(''); expect(JSON.parse(stdout)).toEqual({ result: 'resume-state-result.json' })
      const result = JSON.parse(await readFile(join(home, 'resume-state-result.json'), 'utf8'))
      await writeFile(join(artifact, row.name + (go ? '-go' : '-source') + '-raw.json'), JSON.stringify({ original: row, result }, null, 2))
      if (!go) {
        expect(result.sourceOnly.capabilities.blocked).toEqual([])
        expect(result.sourceOnly.capabilities.secureReads).toEqual(row.input.bare ? ['read'] : ['read', 'read'])
        expect(result.sourceOnly.capabilities.absentReads).toEqual(row.input.bare ? [] : [{ operation: 'readFileSync', path: '/home/claude/.claude/remote/.oauth_token' }])
      }
      for (const [path, value] of Object.entries(row.files)) expect(await readFile(path, 'utf8')).toBe(value)
      expect(socket).toHaveLength(1); expect(result.requestObservation.requests).toHaveLength(1)
      const captured = result.requestObservation.requests[0]!
      expect({ path: socket[0]!.path, method: socket[0]!.method, body: socket[0]!.body }).toEqual({ path: captured.path, method: captured.method, body: captured.body })
      const headers = new Headers(socket[0]!.headers); for (const [name, value] of captured.headers) expect(headers.get(name), name).toBe(value)
      return { observation: result.observation, requestObservation: result.requestObservation }
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; server.stop(true) }
}
for (const name of ['jsonl-path-skips-original-state', 'specific-sid-latest-any-main', 'continue-latest-original-file', 'skills-restore-and-suppress', 'plan-recover-user', 'file-history-copied', 'normal-original-no-config-hooks', 'original-managed-disabled-hooks']) test('original file selection → real restore state → actual first HTTP: ' + name, async () => {
  const home = await mkdtemp(join(allocationRoot, 'resume-state-request-case-'))
  try {
    const row = stateCases(home).find(row => row.name === name)!
    if (name === 'skills-restore-and-suppress') row.input.source = JSON.stringify({ ...JSON.parse(row.input.source as string), sessionId: previousSessionId })
    const source = await run(row, false), replay = await run(row, false); expect(replay).toEqual(source)
    const go = await run(row, true)
    expect(go).toEqual(source)
  } finally { await rm(home, { recursive: true, force: true }) }
}, 115_000)
