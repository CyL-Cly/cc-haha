import { expect, test } from 'bun:test'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, statSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve, sep } from 'node:path'
import { buildCLI, cliBinaryName } from './build'

test('maps all released targets and rejects unsupported targets before touching Go', () => {
  for (const os of ['darwin', 'linux', 'windows']) {
    for (const arch of ['amd64', 'arm64']) expect(cliBinaryName(`${os}/${arch}`)).toBe(`claude-haha-${os}-${arch}${os === 'windows' ? '.exe' : ''}`)
  }
  expect(() => cliBinaryName('linux/386')).toThrow('Unsupported')
})

test('builds under physically disabled private telemetry, drops credentials, and removes all compiler state', async () => {
  const root = mkdtempSync(join(tmpdir(), 'cli-build-regression-'))
  mkdirSync(join(root, 'backend'))
  writeFileSync(join(root, 'backend/go.mod'), 'module fixture.test/cli\n\ngo 1.25.7\n')
  const calls: string[][] = []
  let privateHome = ''
  try {
    const result = await buildCLI({ rootDir: root, target: 'windows/arm64', version: '1.2.3-fixture', sourceEnvironment: { PATH: '/fixture', HOME: '/real-home', ANTHROPIC_API_KEY: 'must-not-leak', GOFLAGS: '-overlay=/real-home/private', CC_HAHA_GO_EXECUTABLE: '/private/go' }, execute: async (command, { env }) => {
      calls.push(command)
      privateHome = env.HOME
      expect(env.HOME).not.toBe('/real-home')
      expect(statSync(env.HOME).mode & 0o777).toBe(0o700)
      expect(readFileSync(join(env.TEST_TELEMETRY_DIR, 'mode'), 'utf8')).toBe('off\n')
      expect(statSync(env.TEST_TELEMETRY_DIR).mode & 0o777).toBe(0o700)
      expect(env.ANTHROPIC_API_KEY).toBeUndefined()
      expect(env.GOFLAGS).toBe('-mod=readonly')
      expect(env.GOPROXY).toBe('off')
      if (command[1] === 'env') return { exitCode: 0, stderr: '', stdout: JSON.stringify({ GOTELEMETRY: 'off', GOTELEMETRYDIR: env.TEST_TELEMETRY_DIR, GOENV: '', GOMODCACHE: env.GOMODCACHE, GOCACHE: env.GOCACHE }) }
      if (command[1] === 'mod') return { exitCode: 0, stderr: '', stdout: JSON.stringify({ Require: [] }) }
      expect(env).toMatchObject({ GOOS: 'windows', GOARCH: 'arm64', CGO_ENABLED: '0', GOAMD64: 'v1', GOARM64: 'v8.0' })
      writeFileSync(command[command.indexOf('-o') + 1], 'native artifact fixture')
      return { exitCode: 0, stdout: '', stderr: '' }
    } })
    expect(result).toEqual({ output: join(root, 'bin/native/claude-haha-windows-arm64.exe'), target: 'windows/arm64', version: '1.2.3-fixture' })
    expect(calls.map(command => command[1])).toEqual(['env', 'mod', 'build'])
    expect(calls.every(command => command[0] === '/private/go')).toBe(true)
    expect(calls.at(-1)).toContain('-X main.version=1.2.3-fixture')
    expect(existsSync(privateHome)).toBe(false)
  } finally { rmSync(root, { recursive: true, force: true }) }
})

test('rejects a compiler reporting telemetry on before hydration or build and cleans its HOME', async () => {
  const root = mkdtempSync(join(tmpdir(), 'cli-build-error-'))
  let home = '', count = 0
  try {
    await expect(buildCLI({ rootDir: root, version: 'fixture', sourceEnvironment: {}, execute: async (_, { env }) => {
      home = env.HOME
      count++
      return { exitCode: 0, stdout: JSON.stringify({ GOTELEMETRY: 'on' }), stderr: '' }
    } })).rejects.toThrow('Go CLI compiler did not accept the private offline telemetry environment')
    expect(count).toBe(1)
    expect(existsSync(home)).toBe(false)
    await expect(buildCLI({ rootDir: root, version: 'fixture -X secret' })).rejects.toThrow('linker')
  } finally { rmSync(root, { recursive: true, force: true }) }
})

test('uses the declared repository artifact root for private Poppler under a narrow lane TMPDIR', async () => {
  const artifacts = resolve(import.meta.dir, '../../artifacts')
  mkdirSync(artifacts, { recursive: true })
  const owned = mkdtempSync(join(artifacts, 'cli-poppler-root-regression-'))
  const root = join(owned, 'repository')
  const lane = mkdtempSync(join(tmpdir(), 'cli-poppler-narrow-temp-'))
  const bin = join(root, 'artifacts/poppler/bin')
  const foreign = join(owned, 'foreign-repository/artifacts/poppler/bin')
  for (const directory of [join(root, 'backend'), bin, foreign]) mkdirSync(directory, { recursive: true })
  writeFileSync(join(root, 'backend/go.mod'), 'module fixture.test/private-cli\n\ngo 1.25.7\n')
  const previous = Object.fromEntries(['TMPDIR', 'TMP', 'TEMP'].map(key => [key, process.env[key]]))
  let compilerHome = '', calls = 0
  try {
    Object.assign(process.env, { TMPDIR: lane, TMP: lane, TEMP: lane })
    const result = await buildCLI({ rootDir: root, target: 'linux/arm64', version: 'private-fixture', sourceEnvironment: { CC_HAHA_TEST_POPPLER_BIN: bin, CC_HAHA_SANDBOX_PHYSICAL_BROKER: '1', ANTHROPIC_API_KEY: 'must-not-leak' }, execute: async (command, { env }) => {
      calls++
      compilerHome = env.HOME
      expect(realpathSync(env.HOME).startsWith(realpathSync(lane) + sep)).toBe(true)
      expect(env.CC_HAHA_TEST_POPPLER_BIN).toBe(realpathSync(bin))
      expect(env.CC_HAHA_SANDBOX_PHYSICAL_BROKER).toBe('1')
      expect(env.ANTHROPIC_API_KEY).toBeUndefined()
      expect(env.GOPROXY).toBe('off')
      if (command[1] === 'env') return { exitCode: 0, stderr: '', stdout: JSON.stringify({ GOTELEMETRY: 'off', GOTELEMETRYDIR: env.TEST_TELEMETRY_DIR, GOENV: '', GOMODCACHE: env.GOMODCACHE, GOCACHE: env.GOCACHE }) }
      if (command[1] === 'mod') return { exitCode: 0, stderr: '', stdout: JSON.stringify({ Require: [] }) }
      expect(command[1]).toBe('build')
      writeFileSync(command[command.indexOf('-o') + 1], 'fake-execute build artifact')
      return { exitCode: 0, stdout: '', stderr: '' }
    } })
    expect(calls).toBe(3)
    expect(existsSync(result.output)).toBe(true)
    expect(existsSync(compilerHome)).toBe(false)
    // Supplying the real root must not authorize sibling artifacts or a link
    // into the permitted artifact tree. Neither rejection invokes a compiler.
    const link = join(root, 'artifacts/poppler-link')
    symlinkSync(bin, link, 'dir')
    for (const [path, message] of [[foreign, 'private'], [link, 'symlink']] as const) {
      await expect(buildCLI({ rootDir: root, version: 'private-fixture', sourceEnvironment: { CC_HAHA_TEST_POPPLER_BIN: path }, execute: async () => { calls++; throw new Error('rejected directory reached compiler') } })).rejects.toThrow(message)
    }
    expect(calls).toBe(3)
  } finally {
    for (const [key, value] of Object.entries(previous)) {
      if (value === undefined) delete process.env[key]
      else process.env[key] = value
    }
    rmSync(owned, { recursive: true, force: true })
    rmSync(lane, { recursive: true, force: true })
  }
})

test('removes the allocated compiler HOME when physical sandbox broker initialization is rejected before execute', async () => {
  const lane = realpathSync(mkdtempSync(join(tmpdir(), 'cli-build-initialization-lane-')))
  const root = join(lane, 'repository')
  const previous = Object.fromEntries(['TMPDIR', 'TMP', 'TEMP'].map(key => [key, process.env[key]]))
  let calls = 0
  try {
    mkdirSync(root)
    Object.assign(process.env, { TMPDIR: lane, TMP: lane, TEMP: lane })
    expect(realpathSync(tmpdir())).toBe(lane)
    expect(statSync(lane).mode & 0o777).toBe(0o700)
    const before = readdirSync(lane).filter(name => name.startsWith('cc-haha-cli-build-')).sort()
    expect(before).toEqual([])
    let failure: unknown
    try {
      await buildCLI({ rootDir: root, version: 'private-initialization-fixture', sourceEnvironment: { CC_HAHA_SANDBOX_PHYSICAL_BROKER: '2' }, execute: async () => {
        calls++
        throw new Error('rejected physical sandbox broker reached compiler execute')
      } })
    } catch (error) { failure = error }
    const after = readdirSync(lane).filter(name => name.startsWith('cc-haha-cli-build-')).sort()
    console.log(`CLI_BUILD_INITIALIZATION_CLEANUP ${JSON.stringify({ errorName: failure instanceof Error ? failure.name : null, errorMessage: failure instanceof Error ? failure.message : null, executeCalls: calls, beforeHomes: before, afterHomes: after, retainedHomes: after.map(name => ({ name, directory: statSync(join(lane, name)).isDirectory(), mode: statSync(join(lane, name)).mode & 0o777, canonicalParentIsLane: resolve(realpathSync(join(lane, name)), '..') === lane })) })}`)
    expect(failure).toBeInstanceOf(Error)
    expect((failure as Error).message).toBe('Physical sandbox broker requires explicit 1')
    expect(calls).toBe(0)
    expect(after).toEqual(before)
  } finally {
    for (const [key, value] of Object.entries(previous)) {
      if (value === undefined) delete process.env[key]
      else process.env[key] = value
    }
    rmSync(lane, { recursive: true, force: true })
    console.log(`CLI_BUILD_INITIALIZATION_CLEANUP_FINALLY ${JSON.stringify({ laneRemoved: !existsSync(lane) })}`)
  }
})
