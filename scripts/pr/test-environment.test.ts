import { describe, expect, test } from 'bun:test'
import { existsSync, mkdirSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { delimiter, join, sep } from 'node:path'
import { spawnSync } from 'node:child_process'
import {
  createOfflineTestEnvironment,
  createSandboxedTestEnvironment,
} from './test-environment'

describe('offline test environment', () => {
  test('keeps ordinary runtime variables while removing credentials and local provider state', () => {
    const environment = createOfflineTestEnvironment({
      HOME: '/tmp/test-home',
      XDG_CONFIG_HOME: '/tmp/test-home/.config',
      CLAUDE_CONFIG_DIR: '/tmp/test-home/.claude',
    }, {
      Path: '/usr/bin',
      CI: 'true',
      DATABASE_URL: 'postgres://user:secret@database.example/app',
      GITHUB_TOKEN: 'real-token',
      ANTHROPIC_API_KEY: 'real-key',
      ANTHROPIC_BASE_URL: 'https://provider.example',
      CLAUDE_CLI_PATH: '/real/claude',
      CC_HAHA_TRACE_PROVIDER_ID: 'real-provider',
      Home: '/real/home',
      HTTP_PROXY: 'http://proxy.example',
      SSH_AUTH_SOCK: '/tmp/real-agent.sock',
      XDG_CONFIG_HOME: '/real/config',
    })

    expect(environment.PATH).toBe('/usr/bin')
    expect(environment.CI).toBe('true')
    expect(environment.HOME).toBe('/tmp/test-home')
    expect(environment.Home).toBeUndefined()
    expect(environment.XDG_CONFIG_HOME).toBe('/tmp/test-home/.config')
    expect(environment.CLAUDE_CONFIG_DIR).toBe('/tmp/test-home/.claude')
    expect(environment.DATABASE_URL).toBeUndefined()
    expect(environment.GITHUB_TOKEN).toBeUndefined()
    expect(environment.ANTHROPIC_API_KEY).toBeUndefined()
    expect(environment.ANTHROPIC_BASE_URL).toBeUndefined()
    expect(environment.CLAUDE_CLI_PATH).toBeUndefined()
    expect(environment.CC_HAHA_TRACE_PROVIDER_ID).toBeUndefined()
    expect(environment.HTTP_PROXY).toBeUndefined()
    expect(environment.SSH_AUTH_SOCK).toBeUndefined()
  })

  test('routes cross-platform user and temporary directories into one sandbox', () => {
    const sandboxHome = mkdtempSync(join(tmpdir(), 'cc-haha-test-env-'))
    try {
      const environment = createSandboxedTestEnvironment(sandboxHome, {}, {
        PATH: '/usr/bin',
        APPDATA: '/real/app-data',
        LOCALAPPDATA: '/real/local-app-data',
        XDG_DATA_HOME: '/real/xdg-data',
      })

      expect(environment.APPDATA).toBe(join(sandboxHome, '.config'))
      expect(environment.LOCALAPPDATA).toBe(join(sandboxHome, '.local'))
      expect(environment.XDG_DATA_HOME).toBe(join(sandboxHome, '.local', 'share'))
      expect(environment.TMPDIR).toBe(join(sandboxHome, 'tmp'))
      expect(environment.NODE_ENV).toBe('test')
      expect(environment.BUN_OPTIONS).toBe('--no-env-file')
      expect(existsSync(environment.TMPDIR)).toBe(true)
    } finally {
      rmSync(sandboxHome, { recursive: true, force: true })
    }
  })

  test('prevents descendant Bun processes from loading a workspace dotenv file', async () => {
    const sandboxHome = mkdtempSync(join(tmpdir(), 'cc-haha-test-env-'))
    const workspace = join(sandboxHome, 'workspace')
    mkdirSync(workspace)
    writeFileSync(
      join(workspace, '.env'),
      'CC_HAHA_DESCENDANT_DOTENV_SENTINEL=leaked\n',
      'utf8',
    )

    try {
      const proc = Bun.spawn(
        [
          'bun',
          '-e',
          'process.stdout.write(process.env.CC_HAHA_DESCENDANT_DOTENV_SENTINEL ?? "clean")',
        ],
        {
          cwd: workspace,
          env: createSandboxedTestEnvironment(sandboxHome),
          stdout: 'pipe',
          stderr: 'pipe',
        },
      )
      const [stdout, stderr, exitCode] = await Promise.all([
        new Response(proc.stdout).text(),
        new Response(proc.stderr).text(),
        proc.exited,
      ])

      expect(exitCode).toBe(0)
      expect(stderr).toBe('')
      expect(stdout).toBe('clean')
    } finally {
      rmSync(sandboxHome, { recursive: true, force: true })
    }
  })
})

describe('sandboxed macOS credential subprocess isolation', () => {
  const posixTest = test.skipIf(process.platform === 'win32')

  function fixture() {
    const root = realpathSync(mkdtempSync(join(tmpdir(), 'cc-haha-security-env-')))
    const bin = join(root, 'downstream-bin')
    const marker = join(root, 'downstream-security-called')
    mkdirSync(bin, { mode: 0o700 })
    // This is the only fallback executable used by these tests. Never resolve
    // the real security command, even while demonstrating the old failure.
    const quotedMarker = `'${marker.replaceAll("'", "'\\''")}'`
    writeFileSync(join(bin, 'security'), `#!/bin/sh\nprintf '%s\\n' sentinel >> ${quotedMarker}\nexit 73\n`, { mode: 0o700 })
    return { root, bin, marker }
  }

  function invoke(env: Record<string, string>, fallbackBin: string, shell = false) {
    const previousPath = process.env.PATH
    process.env.PATH = fallbackBin
    try {
      // Both parent-side resolution and the child's PATH are private, so a
      // regression cannot accidentally invoke a system credential command.
      const result = spawnSync(
        shell ? '/bin/sh' : 'security',
        shell ? ['-c', 'security find-generic-password -s quality-gate-sentinel'] : ['find-generic-password', '-s', 'quality-gate-sentinel'],
        { cwd: env.HOME, env, encoding: 'utf8', timeout: 5_000 },
      )
      expect(result.error).toBeUndefined()
      expect(result.signal).toBeNull()
      return result
    } finally {
      if (previousPath === undefined) delete process.env.PATH
      else process.env.PATH = previousPath
    }
  }

  posixTest('denies bare and shell security before a downstream executable', () => {
    const f = fixture()
    try {
      const home = join(f.root, 'home')
      const env = createSandboxedTestEnvironment(home, {}, { PATH: f.bin })
      const bare = invoke(env, f.bin)
      const shell = invoke(env, f.bin, true)
      expect([bare.status, shell.status]).toEqual([44, 44])
      expect([bare.stdout, bare.stderr, shell.stdout, shell.stderr]).toEqual(['', '', '', ''])
      expect(existsSync(f.marker)).toBe(false)
      expect(env.PATH.split(delimiter)[0].startsWith(realpathSync(home) + sep)).toBe(true)
    } finally {
      rmSync(f.root, { recursive: true, force: true })
    }
  })

  posixTest('a nested sandbox retains its own denial after its parent is removed', () => {
    const f = fixture()
    try {
      const parentHome = join(f.root, 'parent-home')
      const nestedHome = join(f.root, 'nested-home')
      const parent = createSandboxedTestEnvironment(parentHome, {}, { PATH: f.bin })
      const nested = createSandboxedTestEnvironment(nestedHome, {}, parent)
      rmSync(parentHome, { recursive: true, force: true })
      expect([invoke(nested, f.bin).status, invoke(nested, f.bin, true).status]).toEqual([44, 44])
      expect(existsSync(f.marker)).toBe(false)
      expect(nested.PATH.split(delimiter)[0].startsWith(realpathSync(nestedHome) + sep)).toBe(true)
    } finally {
      rmSync(f.root, { recursive: true, force: true })
    }
  })

  posixTest('guards explicit PATH overrides while permitting a later owned physical fixture', () => {
    const f = fixture()
    try {
      const env = createSandboxedTestEnvironment(join(f.root, 'home'), { PATH: f.bin }, { PATH: f.bin })
      expect([invoke(env, f.bin).status, invoke(env, f.bin, true).status]).toEqual([44, 44])
      expect(existsSync(f.marker)).toBe(false)

      // Physical credential fixtures explicitly own and prepend their fake
      // executable after constructing the sandbox. Keep that test boundary.
      const physical = { ...env, PATH: f.bin + delimiter + env.PATH }
      expect(invoke(physical, f.bin).status).toBe(73)
      expect(existsSync(f.marker)).toBe(true)
    } finally {
      rmSync(f.root, { recursive: true, force: true })
    }
  })
})
