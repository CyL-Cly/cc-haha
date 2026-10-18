import { expect, test } from 'bun:test'
import { createHash } from 'node:crypto'
import { mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'

const frozenMain = '6d21316ec4635f29ecbaa43e1a5574bfd31d7155'
const frozenRuntimeInventorySHA = '9f1e2571f7cf077c92d0956397fb87a2d1dd826bde814989a4f392b14cd6a79c'

test('whole original ant sink gates preserve empty-token Datadog and real first-party plugin export', async () => {
  expect(Bun.version).toBe('1.3.14')
  const root = fileURLToPath(new URL('../../../', import.meta.url))
  const hash = (data: Buffer | string) => createHash('sha256').update(data).digest('hex')
  const walk = (directory: string): string[] => readdirSync(join(root, directory), { withFileTypes: true }).flatMap(entry => {
    const path = directory + '/' + entry.name
    if (entry.isSymbolicLink()) throw new Error('Original Source inventory must not contain symlinks: ' + path)
    return entry.isDirectory() ? walk(path) : entry.isFile() ? [path] : []
  })
  const sourcePaths = [...walk('src'), ...walk('stubs'), 'preload.ts', 'tsconfig.json'].sort()
  const sourceIdentity = () => sourcePaths.map(path => [path, hash(readFileSync(join(root, path)))])
  const before = sourceIdentity()
  expect(before).toHaveLength(3148)
  // Six unrelated Source tests differ in the migration worktree. Bind every
  // input before/after, and require all actual runtime inputs to be frozen MAIN.
  const runtimeBefore = before.filter(([path]) => !/\.(test|spec)\.[^.]+$/.test(path!))
  expect(runtimeBefore).toHaveLength(2548)
  expect(hash(JSON.stringify(runtimeBefore))).toBe(frozenRuntimeInventorySHA)
  const harnessPaths = ['scripts/quality-gate/go-parity/native-ant-datadog-sink.fixture.ts', 'scripts/quality-gate/go-parity/native-ant-datadog-sink.test.ts', 'scripts/quality-gate/go-parity/client-options-capabilities.ts', 'scripts/quality-gate/go-parity/fixture-guard.ts', 'scripts/pr/test-environment.ts']
  const harnessIdentity = () => Object.fromEntries(harnessPaths.map(path => [path, hash(readFileSync(join(root, path)))]))
  const harnessBefore = harnessIdentity()
  const owned = mkdtempSync(join(tmpdir(), 'whole-ant-datadog-source-'))
  const requests: Array<{ method: string; path: string; headers: Record<string, string>; body: any }> = []
  const rejectedHTTP: Array<{ method: string; path: string }> = []
  const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
    const path = new URL(request.url).pathname
    if (request.method === 'HEAD' && path === '/safe') return new Response(null, { status: 204 })
    if (request.method !== 'POST' || path !== '/events') {
      rejectedHTTP.push({ method: request.method, path })
      return new Response('Unowned original Source HTTP request', { status: 403 })
    }
    requests.push({ method: request.method, path, headers: Object.fromEntries(request.headers), body: await request.json() })
    return Response.json({ accepted: true })
  } })
  try {
    for (const kind of ['ant-enabled', 'ant-disabled', 'ant-killed', 'external-override']) {
      const home = join(owned, kind)
      mkdirSync(home)
      const overrides = { tengu_log_datadog_events: kind !== 'ant-disabled', tengu_frond_boric: { datadog: kind === 'ant-killed' } }
      const env = createSandboxedTestEnvironment(home, {
        CC_HAHA_FIXTURE_TEMP_ROOT: owned, CC_HAHA_ANT_DATADOG_SOURCE: '1', NODE_ENV: 'production', USER_TYPE: kind === 'external-override' ? 'external' : 'ant',
        CLAUDE_INTERNAL_FC_OVERRIDES: JSON.stringify(overrides), CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH: '1', ANTHROPIC_BASE_URL: peer.url.origin + '/launch', NO_COLOR: '1',
      }, { PATH: '/usr/bin:/bin' })
      const beforeRequests = requests.length
      const child = Bun.spawn([process.execPath, '--no-env-file', join(root, harnessPaths[0]!)], { cwd: home, env, stdin: new Blob([JSON.stringify({ home, kind, origin: peer.url.origin })]), stdout: 'pipe', stderr: 'pipe' })
      const deadline = setTimeout(() => child.kill(), 20000)
      let exit: number, stdout: string, stderr: string
      try { [exit, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]) } finally { clearTimeout(deadline) }
      expect({ kind, exit, stdout, stderr }).toEqual({ kind, exit: 0, stdout: '[]\n', stderr: '' })
      const receipt = JSON.parse(readFileSync(join(home, 'ant-datadog-source.json'), 'utf8'))
      const physicalHTTP = requests.slice(beforeRequests)
      console.log(JSON.stringify({ kind, receipt, physicalHTTP, sourceIdentity: { frozenMain, files: before.length, inventorySHA256: hash(JSON.stringify(before)), runtimeFiles: runtimeBefore.length, frozenRuntimeInventorySHA256: frozenRuntimeInventorySHA }, harnessSHA: harnessBefore }))
      expect(receipt.moduleIdentity).toEqual({ analytics: true, datadog: true, config: true, analyticsLogEvent: true, datadogTracker: true })
      expect(receipt.sourceAfter).toEqual(receipt.sourceBefore)
      expect(receipt.sourceBefore['src/services/analytics/datadog.ts']).toBe('f36aedd86cd29837514fe76d924f8ace14d7ba3f627030b0d9aa1b0d7fb3bf4f')
      expect(receipt.caps.blocked).toEqual([])
      expect(receipt.deniedNativeHTTPS).toEqual([])
      expect(receipt.exits).toEqual([0])
      expect(receipt.userSettingsUnchanged).toBe(true)
      expect(receipt.globalAfter.futureGlobal).toEqual({ keep: true })
      expect(receipt.globalAfter.migrationVersion).toBe(11)
      expect(receipt.analyticsDisabled).toBe(false)
      expect(receipt.provider).toBe('firstParty')
      expect(receipt.firstPartyKilled).toBe(false)
      expect(receipt.gate).toBe(kind === 'ant-enabled' || kind === 'ant-killed')
      expect(receipt.datadogKilled).toBe(kind === 'ant-killed')
      expect(receipt.initializations).toEqual([{ nonInteractive: true, trust: false }])
      expect(receipt.initialization).toEqual({ initializedByActualRoute: kind === 'ant-enabled', result: false, sameMemoizedPromise: true, routeUsesMemoizedPromise: kind === 'ant-enabled' ? true : null, nativePromise: true })
      if (kind === 'ant-enabled') {
        expect(receipt.routes.length).toBeGreaterThan(0)
        expect(receipt.routes.map((row: any) => row.name)).toContain('tengu_plugin_list_command')
        expect(receipt.routes[0].initializerCachedBefore).toBe(false)
        for (const route of receipt.routes) {
          expect(route.initializerCachedAfter).toBe(true)
          expect(route.nativePromise).toBe(true)
          expect(route.initializerNativePromise).toBe(true)
          expect(Object.keys(route.properties).some(key => key.startsWith('_PROTO_'))).toBe(false)
        }
      } else expect(receipt.routes).toEqual([])
      expect(receipt.prepares.length).toBeGreaterThan(0)
      expect(physicalHTTP.length).toBe(receipt.prepares.length)
      const events = physicalHTTP.flatMap(row => row.body.events)
      expect(events.filter((row: any) => row.event_data.event_name === 'tengu_plugin_list_command')).toHaveLength(1)
      for (const row of physicalHTTP) {
        expect(row.headers['x-api-key']).toBe('fake-owned-ant-datadog-api-key')
        expect(row.headers.authorization).toBeUndefined()
      }
      expect(receipt.datadogHTTPRejected).toBe(true)
      expect(receipt.deniedTelemetry).toEqual([{ phase: 'negative-datadog-http-probe', target: 'https://http-intake.logs.us5.datadoghq.com/api/v2/logs' }])
    }
    expect(rejectedHTTP).toEqual([])
    expect(sourceIdentity()).toEqual(before)
    expect(harnessIdentity()).toEqual(harnessBefore)
  } finally {
    peer.stop(true)
    rmSync(owned, { recursive: true, force: true })
  }
}, 30000)
