import { chmodSync, lstatSync, mkdirSync, readFileSync, readdirSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import { delimiter, join } from 'node:path'

const SAFE_INHERITED_ENV_NAMES = new Set([
  'CI',
  'COMSPEC',
  'GITHUB_ACTIONS',
  'HOME',
  'LANG',
  'LC_ALL',
  'LC_CTYPE',
  'NUMBER_OF_PROCESSORS',
  'OS',
  'PATH',
  'PATHEXT',
  'PROCESSOR_ARCHITECTURE',
  'PROCESSOR_IDENTIFIER',
  'RUNNER_ARCH',
  'RUNNER_OS',
  'SHELL',
  'SYSTEMROOT',
  'TEMP',
  'TERM',
  'TMP',
  'TMPDIR',
  'TZ',
  'USERPROFILE',
  'WINDIR',
])

export function createOfflineTestEnvironment(
  overrides: Record<string, string>,
  source: NodeJS.ProcessEnv = process.env,
): Record<string, string> {
  const environment: Record<string, string> = {}
  for (const safeName of SAFE_INHERITED_ENV_NAMES) {
    const entry = Object.entries(source).find(([name]) => name.toUpperCase() === safeName)
    if (entry?.[1] !== undefined) {
      environment[safeName] = entry[1]
    }
  }

  return {
    ...environment,
    ...overrides,
  }
}

const SECURITY_DENY_SCRIPT = '#!/bin/sh\nexit 44\n'

function privateCredentialCommandPath(sandboxHome: string, inheritedPath?: string): string {
  const directory = join(realpathSync(sandboxHome), '.cc-haha-test-bin')
  mkdirSync(directory, { recursive: true, mode: 0o700 })
  const directoryStat = lstatSync(directory)
  if (!directoryStat.isDirectory() || directoryStat.uid !== process.getuid!() || (directoryStat.mode & 0o777) !== 0o700) {
    throw new Error('Sandbox credential command directory must be private and owned')
  }
  const executable = join(directory, 'security')
  try {
    writeFileSync(executable, SECURITY_DENY_SCRIPT, { flag: 'wx', mode: 0o700 })
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== 'EEXIST') throw error
  }
  const executableStat = lstatSync(executable)
  if (!executableStat.isFile() || executableStat.uid !== process.getuid!() || executableStat.nlink !== 1 ||
      (executableStat.mode & 0o777) !== 0o700 || readFileSync(executable, 'utf8') !== SECURITY_DENY_SCRIPT) {
    throw new Error('Sandbox credential command must be the owned denial primitive')
  }
  return inheritedPath ? directory + delimiter + inheritedPath : directory
}

export function createSandboxedTestEnvironment(
  sandboxHome: string,
  overrides: Record<string, string> = {},
  source: NodeJS.ProcessEnv = process.env,
) {
  const configHome = join(sandboxHome, '.config')
  const dataHome = join(sandboxHome, '.local', 'share')
  const cacheHome = join(sandboxHome, '.cache')
  const stateHome = join(sandboxHome, '.local', 'state')
  const tempHome = join(sandboxHome, 'tmp')
  for (const directory of [configHome, dataHome, cacheHome, stateHome, tempHome]) {
    mkdirSync(directory, { recursive: true })
  }

  const environment = createOfflineTestEnvironment({
    HOME: sandboxHome,
    USERPROFILE: sandboxHome,
    APPDATA: configHome,
    LOCALAPPDATA: join(sandboxHome, '.local'),
    XDG_CONFIG_HOME: configHome,
    XDG_DATA_HOME: dataHome,
    XDG_CACHE_HOME: cacheHome,
    XDG_STATE_HOME: stateHome,
    CODEX_HOME: join(sandboxHome, '.codex'),
    GH_CONFIG_DIR: join(configHome, 'gh'),
    CLAUDE_CONFIG_DIR: join(sandboxHome, '.claude'),
    TEMP: tempHome,
    TMP: tempHome,
    TMPDIR: tempHome,
    BUN_OPTIONS: '--no-env-file',
    CC_HAHA_DISABLE_TERMINAL_SHELL_ENV: '1',
    NODE_ENV: 'test',
    ...overrides,
  }, source)
  // HOME does not isolate macOS's system keychain. Deny that external primitive
  // even when a nested sandbox or explicit PATH override is requested. A test
  // owning a physical fake may still prepend it after constructing this env.
  if (process.platform !== 'win32') {
    environment.PATH = privateCredentialCommandPath(sandboxHome, environment.PATH)
  }
  return environment
}

function openDirectoryTree(directory: string) {
  const stat = lstatSync(directory)
  if (!stat.isDirectory()) return
  if ((stat.mode & 0o700) !== 0o700) chmodSync(directory, stat.mode | 0o700)
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    if (entry.isDirectory()) openDirectoryTree(join(directory, entry.name))
  }
}

/**
 * Remove a lane's sandbox HOME without letting a test's leftovers turn a finished,
 * passing run red. A read-only directory left under the sandboxed TMPDIR blocks a
 * plain recursive rm on POSIX, so open the tree up (never following links) and
 * retry; anything still in the way (macOS runners have reported EROFS) becomes a
 * warning. The sandbox lives under the OS temp dir, so nothing user-owned leaks.
 */
export function removeTestSandbox(directory: string, label: string, warn: (message: string) => void = console.warn) {
  try {
    rmSync(directory, { recursive: true, force: true })
    return
  } catch {
    // Retry below with the tree made writable.
  }
  try {
    openDirectoryTree(directory)
    rmSync(directory, { recursive: true, force: true })
  } catch (error) {
    const prefix = process.env.GITHUB_ACTIONS === 'true' ? '::warning::' : ''
    warn(`${prefix}${label}: could not remove test sandbox ${directory}: ${error instanceof Error ? error.message : String(error)}`)
  }
}
