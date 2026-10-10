import { expect, test } from 'bun:test'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
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
    await expect(buildCLI({ rootDir: root, version: 'fixture', execute: async (_, { env }) => {
      home = env.HOME
      count++
      return { exitCode: 0, stdout: JSON.stringify({ GOTELEMETRY: 'on' }), stderr: '' }
    } })).rejects.toThrow()
    expect(count).toBe(1)
    expect(existsSync(home)).toBe(false)
    await expect(buildCLI({ rootDir: root, version: 'fixture -X secret' })).rejects.toThrow('linker')
  } finally { rmSync(root, { recursive: true, force: true }) }
})
