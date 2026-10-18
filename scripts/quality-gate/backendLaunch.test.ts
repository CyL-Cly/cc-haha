import { afterEach, describe, expect, test } from 'bun:test'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { dirname, isAbsolute, join } from 'node:path'
import { createQualityGateSandbox, type QualityGateSandbox } from './sandbox'
import { BACKEND_TARGET_ENV, resolveBackendLaunchTarget, spawnBackendServer, type BackendLaunchTarget, type BackendServer } from './backendLaunch'

const directories: string[] = []
const sandboxes: QualityGateSandbox[] = []
const servers: BackendServer[] = []

afterEach(async () => {
  await Promise.all(servers.splice(0).map(server => server.stop(100)))
  for (const sandbox of sandboxes.splice(0)) sandbox.cleanup()
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true })
})

function setup() {
  const rootDir = mkdtempSync(join(tmpdir(), 'cc-haha-backend-launch-test-'))
  directories.push(rootDir)
  const guardedDir = join(rootDir, 'guarded-config')
  mkdirSync(guardedDir)
  writeFileSync(join(guardedDir, 'settings.json'), '{"sentinel":"unchanged"}\n')
  const sandbox = createQualityGateSandbox({
    label: 'backend-launch-test',
    sourceConfigDir: guardedDir,
    seedProviders: false,
    source: { PATH: process.env.PATH, ANTHROPIC_API_KEY: 'ambient-secret-must-not-leak' },
    envOverrides: { CLAUDE_CLI_PATH: join(rootDir, 'mock-cli.ts'), CC_HAHA_DISABLE_TERMINAL_SHELL_ENV: '1' },
  })
  sandboxes.push(sandbox)
  return { rootDir, sandbox, guardedDir }
}

function freePort() {
  return new Promise<number>((resolvePort, reject) => {
    const server = createServer()
    server.once('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const address = server.address()
      server.close(() => resolvePort(typeof address === 'object' && address ? address.port : 0))
    })
  })
}

async function expectPortClosed(port: number) {
  await new Promise<void>((resolveClosed, reject) => {
    const probe = createServer()
    probe.once('error', reject)
    probe.listen(port, '127.0.0.1', () => probe.close(() => resolveClosed()))
  })
}

// A disposable, loopback-only child proves the process boundary without importing
// the product server, calling a provider, or reading the real home/config.
const FIXTURE = `
import { createServer } from 'node:http'
import { writeFileSync } from 'node:fs'
import { spawn } from 'node:child_process'
const args = process.argv.slice(2)
const host = args[args.indexOf('--host') + 1]
const port = Number(args[args.indexOf('--port') + 1])
let workerPid
if (process.env.FIXTURE_SPAWN_WORKER) {
  const worker = spawn(process.execPath, ['-e', "require('node:fs').writeFileSync(process.env.FIXTURE_WORKER_READY, 'ready'); setInterval(() => {}, 1000)"], { stdio: 'ignore' })
  workerPid = worker.pid
}
writeFileSync(process.env.FIXTURE_CAPTURE, JSON.stringify({
  args, cwd: process.cwd(), home: process.env.HOME, config: process.env.CLAUDE_CONFIG_DIR,
  cli: process.env.CLAUDE_CLI_PATH, shell: process.env.CC_HAHA_DISABLE_TERMINAL_SHELL_ENV,
  ambientSecret: process.env.ANTHROPIC_API_KEY, port: process.env.SERVER_PORT,
  custom: process.env.FIXTURE_CUSTOM,
  credentialScope: process.env.FIXTURE_CAPTURE_CREDENTIAL_SCOPE ? {
    root: process.env.CC_HAHA_ELECTRON_SMOKE_STATE_ROOT,
    log: process.env.CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG,
    keys: Object.keys(process.env).filter(name => [
      'CC_HAHA_ELECTRON_SMOKE_STATE_ROOT', 'CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG',
    ].includes(name.toUpperCase())).sort(),
  } : undefined,
  workerPid,
}))
console.log('fixture stdout')
console.error('fixture stderr')
if (process.env.FIXTURE_EXIT) process.exit(Number(process.env.FIXTURE_EXIT))
const readyAt = Date.now() + Number(process.env.FIXTURE_HEALTH_DELAY_MS || '0')
const server = createServer((request, response) => {
  if (request.url !== '/health') { response.writeHead(404); response.end(); return }
  if (process.env.FIXTURE_HANG_HEALTH) return
  response.writeHead(Date.now() >= readyAt ? 200 : 503)
  response.end(JSON.stringify({ status: 'ok' }))
})
process.on('SIGTERM', () => {
  if (process.env.FIXTURE_IGNORE_TERM) return
  writeFileSync(process.env.FIXTURE_SHUTDOWN, 'graceful shutdown')
  server.close(() => process.exit(0))
})
server.listen(port, host)
`

function fixtureTarget(rootDir: string, sandbox: QualityGateSandbox, extraEnv: Record<string, string> = {}): BackendLaunchTarget {
  const file = join(rootDir, 'server-fixture.mjs')
  writeFileSync(file, FIXTURE)
  return {
    // The kind exercises target selection; this child is explicitly a fixture,
    // not evidence of a Go backend implementation.
    kind: 'go',
    command: process.execPath,
    args: [file, 'server', '--custom', 'literal spaces $(not-a-shell-command)'],
    cwd: sandbox.home,
    env: {
      FIXTURE_CAPTURE: join(sandbox.home, 'capture.json'),
      FIXTURE_SHUTDOWN: join(sandbox.home, 'shutdown.txt'),
      ...extraEnv,
    },
  }
}

function launch(options: Parameters<typeof spawnBackendServer>[0]) {
  const server = spawnBackendServer(options)
  servers.push(server)
  return server
}

describe('backend target selection and sandbox boundary', () => {
  test('defaults to the current Bun with the TS entrypoint only when no target is selected', () => {
    const { rootDir } = setup()
    expect(resolveBackendLaunchTarget(rootDir, undefined, {})).toEqual({
      kind: 'typescript',
      command: process.execPath,
      args: ['--no-env-file', 'run', join(rootDir, 'src/server/index.ts')],
      cwd: rootDir,
      env: {},
    })
  })

  test('uses an explicitly selected executable/args/cwd/env from JSON without inheriting the selector', () => {
    const { rootDir, sandbox } = setup()
    const target = fixtureTarget(rootDir, sandbox)
    expect(resolveBackendLaunchTarget(rootDir, undefined, { [BACKEND_TARGET_ENV]: JSON.stringify(target) })).toEqual(target)
    expect(sandbox.env[BACKEND_TARGET_ENV]).toBeUndefined()
  })

  test('rejects a malformed, unknown, relative, or missing target instead of falling back to TS', () => {
    const { rootDir } = setup()
    for (const json of ['', '{', 'null', '{"kind":"rust","command":"/missing"}']) {
      expect(() => resolveBackendLaunchTarget(rootDir, undefined, { [BACKEND_TARGET_ENV]: json })).toThrow()
    }
    expect(() => resolveBackendLaunchTarget(rootDir, { kind: 'go', command: './backend' })).toThrow('absolute executable')
    expect(() => resolveBackendLaunchTarget(rootDir, { kind: 'go', command: join(rootDir, 'missing-backend') })).toThrow()
    expect(() => resolveBackendLaunchTarget(rootDir, { kind: 'go', command: process.execPath, cwd: 'missing-directory' })).toThrow()
    expect(() => resolveBackendLaunchTarget(rootDir, { kind: 'go', command: process.execPath, args: ['--host=0.0.0.0'] })).toThrow('owned by the sandbox')
    expect(() => resolveBackendLaunchTarget(rootDir, { kind: 'go', command: process.execPath, args: ['--port', '80'] })).toThrow('owned by the sandbox')
  })

  test('does not let a target replace guarded paths, shell isolation, or the mock SDK CLI', () => {
    const { rootDir, sandbox } = setup()
    for (const name of ['HOME', 'home', 'CLAUDE_CONFIG_DIR', 'APPDATA', 'XDG_CONFIG_HOME', 'CODEX_HOME', 'TMPDIR', 'BUN_OPTIONS', 'CLAUDE_CLI_PATH', 'CC_HAHA_DISABLE_TERMINAL_SHELL_ENV', 'MOCK_SDK_TEAM_WORKER_AUDIT', 'SERVER_PORT', 'CC_HAHA_ELECTRON_SMOKE_STATE_ROOT', 'cc_haha_electron_smoke_state_root', 'CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG']) {
      const target = fixtureTarget(rootDir, sandbox, { [name]: '/outside-the-sandbox' })
      expect(() => resolveBackendLaunchTarget(rootDir, target)).toThrow('cannot override sandbox')
    }
  })

  test('validates the supplied environment against the actual sandbox before spawning', async () => {
    const { rootDir, sandbox } = setup()
    const target = fixtureTarget(rootDir, sandbox)
    const port = await freePort()
    for (const env of [
      { ...sandbox.env, CLAUDE_CONFIG_DIR: join(rootDir, 'guarded-config') },
      { ...sandbox.env, home: '/outside-the-sandbox' },
      { ...sandbox.env, CLAUDE_CLI_PATH: '/another-cli' },
    ]) {
      expect(() => spawnBackendServer({ rootDir, port, sandbox, env, target })).toThrow()
    }
    expect(existsSync(join(sandbox.home, 'capture.json'))).toBe(false)
  })
})

describe('actual backend process lifecycle', () => {
  test('binds a Go child to its actual private credential scope without relying on provider credentials', async () => {
    const { rootDir, sandbox } = setup()
    const target = fixtureTarget(rootDir, sandbox, { FIXTURE_CAPTURE_CREDENTIAL_SCOPE: '1' })
    const server = launch({ rootDir, port: await freePort(), sandbox, target })
    await server.waitUntilHealthy(5_000)
    const { credentialScope } = JSON.parse(readFileSync(join(sandbox.home, 'capture.json'), 'utf8'))
    expect(credentialScope.root).toBe(realpathSync(sandbox.home))
    expect(isAbsolute(credentialScope.log)).toBe(true)
    expect(dirname(credentialScope.log)).toBe(realpathSync(sandbox.home))
    expect(credentialScope.keys).toEqual([
      'CC_HAHA_ELECTRON_SMOKE_STATE_ROOT', 'CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG',
    ])
    expect(sandbox.env.CC_HAHA_ELECTRON_SMOKE_STATE_ROOT).toBeUndefined()
    expect(sandbox.env.CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG).toBeUndefined()
    await server.stop(1_000)
    expect(sandbox.detectUserStateMutations()).toEqual([])
  })

  test('rejects supplied credential redirects and derives Go scope even when sandbox overrides contain them', async () => {
    const { rootDir, sandbox } = setup()
    const rootKey = 'CC_HAHA_ELECTRON_SMOKE_STATE_ROOT'
    const logKey = 'CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG'
    const previous = { [rootKey]: process.env[rootKey], [logKey]: process.env[logKey] }
    const target = fixtureTarget(rootDir, sandbox, { FIXTURE_CAPTURE_CREDENTIAL_SCOPE: '1' })
    const port = await freePort()
    const rejected = { ...sandbox.env, [rootKey]: join(rootDir, 'supplied-root'), [logKey]: join(rootDir, 'supplied-log') }
    expect(() => launch({ rootDir, port, sandbox, env: rejected, target })).toThrow('cannot change sandbox variable')
    expect(existsSync(join(sandbox.home, 'capture.json'))).toBe(false)
    // A caller can construct sandbox.env through envOverrides. Matching those
    // values is not authority to select a different OS credential root.
    Object.assign(sandbox.env, {
      [rootKey]: join(rootDir, 'sandbox-override-root'),
      [logKey]: join(rootDir, 'sandbox-override-log'),
      [rootKey.toLowerCase()]: join(rootDir, 'lowercase-sandbox-root'),
      [logKey.toLowerCase()]: join(rootDir, 'lowercase-sandbox-log'),
    })
    const supplied = { ...sandbox.env }
    const targetBefore = { ...target.env }
    const suppliedBefore = { ...supplied }
    try {
      process.env[rootKey] = join(rootDir, 'ambient-root')
      process.env[logKey] = join(rootDir, 'ambient-log')
      const server = launch({ rootDir, port, sandbox, env: supplied, target })
      await server.waitUntilHealthy(5_000)
      const { credentialScope } = JSON.parse(readFileSync(join(sandbox.home, 'capture.json'), 'utf8'))
      expect(credentialScope.root).toBe(realpathSync(sandbox.home))
      expect(isAbsolute(credentialScope.log)).toBe(true)
      expect(dirname(credentialScope.log)).toBe(realpathSync(sandbox.home))
      expect(credentialScope.keys).toEqual([rootKey, logKey])
      expect(supplied).toEqual(suppliedBefore)
      expect(target.env).toEqual(targetBefore)
      expect(process.env[rootKey]).toBe(join(rootDir, 'ambient-root'))
      expect(process.env[logKey]).toBe(join(rootDir, 'ambient-log'))
      await server.stop(1_000)
    } finally {
      for (const [name, value] of Object.entries(previous)) {
        if (value === undefined) delete process.env[name]
        else process.env[name] = value
      }
    }
    expect(sandbox.detectUserStateMutations()).toEqual([])
  })

  test('passes the selected args/cwd and sandbox environment, polls health, captures both logs, and shuts down', async () => {
    const { rootDir, sandbox } = setup()
    const target = fixtureTarget(rootDir, sandbox, { FIXTURE_CUSTOM: 'selected', FIXTURE_HEALTH_DELAY_MS: '150' })
    const port = await freePort()
    const server = launch({ rootDir, port, sandbox, target })
    const logs = Promise.all([new Response(server.stdout).text(), new Response(server.stderr).text()])
    await server.waitUntilHealthy(5_000)
    expect(JSON.parse(readFileSync(join(sandbox.home, 'capture.json'), 'utf8'))).toEqual({
      args: ['server', '--custom', 'literal spaces $(not-a-shell-command)', '--host', '127.0.0.1', '--port', String(port)],
      cwd: realpathSync(sandbox.home),
      home: sandbox.home,
      config: sandbox.configDir,
      cli: sandbox.env.CLAUDE_CLI_PATH,
      shell: '1',
      port: String(port),
      custom: 'selected',
    })
    await server.stop(1_000)
    await server.stop(1_000)
    const [stdout, stderr] = await logs
    expect(stdout).toContain('fixture stdout')
    expect(stderr).toContain('fixture stderr')
    if (process.platform !== 'win32') expect(readFileSync(join(sandbox.home, 'shutdown.txt'), 'utf8')).toBe('graceful shutdown')
    expect(sandbox.detectUserStateMutations()).toEqual([])
    await expectPortClosed(port)
  })

  test('also actually boots the default TS target inside a disposable repo', async () => {
    const { rootDir, sandbox } = setup()
    const entry = join(rootDir, 'src/server/index.ts')
    mkdirSync(join(rootDir, 'src/server'), { recursive: true })
    writeFileSync(entry, FIXTURE)
    sandbox.env.FIXTURE_CAPTURE = join(sandbox.home, 'default.json')
    sandbox.env.FIXTURE_SHUTDOWN = join(sandbox.home, 'shutdown.txt')
    sandbox.env.FIXTURE_CAPTURE_CREDENTIAL_SCOPE = '1'
    const target = resolveBackendLaunchTarget(rootDir, undefined, {})
    const server = launch({ rootDir, port: await freePort(), sandbox, target })
    await server.waitUntilHealthy(5_000)
    expect(server.target.kind).toBe('typescript')
    expect(existsSync(join(sandbox.home, 'default.json'))).toBe(true)
    expect(JSON.parse(readFileSync(join(sandbox.home, 'default.json'), 'utf8')).credentialScope).toEqual({ keys: [] })
  })

  test('fails promptly on a selected backend exit and never launches the default backend', async () => {
    const { rootDir, sandbox } = setup()
    const server = launch({ rootDir, port: await freePort(), sandbox, target: fixtureTarget(rootDir, sandbox, { FIXTURE_EXIT: '31' }) })
    await expect(server.waitUntilHealthy(5_000)).rejects.toThrow('backend exited before health readiness (exit 31)')
    expect(await server.exited).toBe(31)
    expect(sandbox.detectUserStateMutations()).toEqual([])
  })

  test('bounds a health request whose connection never responds', async () => {
    const { rootDir, sandbox } = setup()
    const server = launch({ rootDir, port: await freePort(), sandbox, target: fixtureTarget(rootDir, sandbox, { FIXTURE_HANG_HEALTH: '1' }) })
    const started = Date.now()
    await expect(server.waitUntilHealthy(250)).rejects.toThrow('Timed out waiting for go backend health')
    expect(Date.now() - started).toBeLessThan(2_000)
  })

  test('forces shutdown when the backend ignores its graceful signal', async () => {
    const { rootDir, sandbox } = setup()
    const port = await freePort()
    const server = launch({ rootDir, port, sandbox, target: fixtureTarget(rootDir, sandbox, { FIXTURE_IGNORE_TERM: '1' }) })
    await server.waitUntilHealthy(5_000)
    await server.stop(50)
    expect(await server.exited).not.toBe(0)
    await expectPortClosed(port)
  })

  test('reaps a worker that outlives its backend leader', async () => {
    const { rootDir, sandbox } = setup()
    const ready = join(sandbox.home, 'worker-ready.txt')
    const server = launch({ rootDir, port: await freePort(), sandbox, target: fixtureTarget(rootDir, sandbox, {
      FIXTURE_SPAWN_WORKER: '1', FIXTURE_WORKER_READY: ready,
    }) })
    await server.waitUntilHealthy(5_000)
    const deadline = Date.now() + 2_000
    while (!existsSync(ready) && Date.now() < deadline) await Bun.sleep(10)
    expect(existsSync(ready)).toBe(true)
    const { workerPid } = JSON.parse(readFileSync(join(sandbox.home, 'capture.json'), 'utf8'))
    expect(typeof workerPid).toBe('number')
    await server.stop(500)
    let alive = true
    while (alive && Date.now() < deadline) {
      try { process.kill(workerPid, 0) } catch { alive = false }
      if (alive) await Bun.sleep(10)
    }
    expect(alive).toBe(false)
  })
})
