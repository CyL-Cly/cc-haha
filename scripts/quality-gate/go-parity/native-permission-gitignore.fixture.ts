import { mkdirSync, readFileSync, writeFileSync, existsSync, rmSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Permission Gitignore')
if (process.env.CC_HAHA_PERMISSION_GITIGNORE_FIXTURE !== '1') throw new Error('Missing Gitignore fixture')
const absent = [...absentManagedSettingsFixturePaths()]
for (let directory = dirname(input.homeDir);; directory = dirname(directory)) {
  absent.push(join(directory, '.git'))
  if (directory === dirname(directory)) break
}
const execute = Bun.spawnSync.bind(Bun)
const calls: unknown[] = [], logs: string[] = []
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absent })
await import('../../../preload')
// This is only the subprocess primitive boundary. The actual Source helper,
// repository discovery, cache, ignore checks and file mutation remain intact.
mock.module(resolve(import.meta.dir, '../../../src/utils/execFileNoThrow.ts'), () => ({
  execFileNoThrow: () => { throw new Error('Unexpected Gitignore execFileNoThrow caller') },
  execSyncWithDefaults_DEPRECATED: () => { throw new Error('Unexpected Gitignore synchronous caller') },
  execFileNoThrowWithCwd: async (file: string, args: string[], options: { cwd: string }) => {
    const cwd = resolve(options.cwd)
    if (file !== 'git' || args.length !== 2 || args[0] !== 'check-ignore' || !cwd.startsWith(resolve(input.homeDir) + '/')) throw new Error('Gitignore escaped subprocess grant')
    calls.push({ file, args, cwd: cwd.slice(resolve(input.homeDir).length), preserveOutputOnError: (options as any).preserveOutputOnError })
    try {
      const result = execute(['/usr/bin/git', ...args], { cwd, env: process.env })
      return { stdout: '', stderr: '', code: result.exitCode }
    } catch (error) {
      logs.push(String(error))
      return { stdout: '', stderr: '', code: 1 }
    }
  },
}))
mock.module(resolve(import.meta.dir, '../../../src/utils/log.ts'), () => ({ logError: (error: unknown) => logs.push(String(error)) }))
const { addFileGlobRuleToGitignore } = await import('../../../src/utils/git/gitignore')
const { findGitRoot } = await import('../../../src/utils/git')
const rows = []
for (let i = 0; i < input.rows.length; i++) {
  const row = input.rows[i]
  const root = join(input.homeDir, `repo-${i}`), cwd = join(root, row.nested ? 'nested' : '')
  mkdirSync(cwd, { recursive: true })
  if (row.repo) {
    const result = execute(['/usr/bin/git', 'init', '--quiet', root], { env: process.env })
    if (result.exitCode !== 0) throw new Error(`Private git init failed: ${result.stderr}`)
  }
  if (row.local !== undefined) writeFileSync(join(root, '.gitignore'), row.local)
  const global = join(input.homeDir, '.config/git/ignore')
  rmSync(global, { force: true })
  if (row.global !== undefined) {
    mkdirSync(dirname(global), { recursive: true })
    writeFileSync(global, row.global)
  }
  findGitRoot.cache.clear()
  const start = calls.length
  for (let j = 0; j < (row.repeat ?? 1); j++) await addFileGlobRuleToGitignore(row.filename, cwd)
  rows.push({ content: existsSync(global) ? readFileSync(global, 'utf8') : null, calls: calls.slice(start) })
}
process.stdout.write(JSON.stringify({ rows, logs, capabilities }))
