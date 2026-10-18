import { execFileSync, spawn } from 'node:child_process'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { DataMigration, type DataMigrationHooks } from './dataMigration'
import { resolveSidecarExecutable } from './sidecarManager'
import { runStorageRelocationWorker } from './storageRelocationWorker'
import { removeTestSandbox } from '../../../scripts/pr/test-environment'

let root: string
let desktopRoot: string
let env: NodeJS.ProcessEnv
beforeAll(async () => {
  root = await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()), 'go-storage-host-'))
  desktopRoot = path.join(root, 'install')
  const executable = resolveSidecarExecutable(desktopRoot)
  await fs.mkdir(path.dirname(executable), { recursive: true })
  env = { ...process.env, HOME: path.join(root, 'home'), USERPROFILE: path.join(root, 'home'), CLAUDE_CONFIG_DIR: path.join(root, 'home', '.claude'), TMPDIR: root }
  await fs.mkdir(env.HOME!, { recursive: true })
  const repositoryRoot = path.resolve(import.meta.dirname, '../../..')
  const buildHome = path.join(root, 'build-home')
  await fs.mkdir(buildHome, { recursive: true })
  // Use the same checksum-verified, offline seed as all other Go consumers.
  // The physical worker test must not download modules or inherit a real cache
  // when Vitest runs under a disposable desktop HOME. Execute the Bun-only
  // tool in its own runtime; Electron's Node typecheck does not import it.
  const dependencyModule = path.join(repositoryRoot, 'scripts/pr/go-dependencies.ts')
  const preparation = `
    import { execFileSync } from 'node:child_process'
    const { createHermeticGoEnvironment, hydrateGoDependencies } = await import(${JSON.stringify(dependencyModule)})
    const env = createHermeticGoEnvironment(${JSON.stringify(buildHome)}, process.env, false, ${JSON.stringify(repositoryRoot)})
    await hydrateGoDependencies(${JSON.stringify(repositoryRoot)}, env, {
      execute: async (command, options) => {
        try {
          const stdout = execFileSync(command[0], command.slice(1), { ...options, encoding: 'utf8', stdio: 'pipe', timeout: 120_000, maxBuffer: 16 * 1024 * 1024 })
          return { exitCode: 0, stdout, stderr: '' }
        } catch (error) {
          return { exitCode: error.status ?? 1, stdout: String(error.stdout ?? ''), stderr: String(error.stderr ?? error) }
        }
      },
    })
    process.stdout.write(JSON.stringify(env))
  `
  const buildEnv: NodeJS.ProcessEnv = JSON.parse(execFileSync('bun', ['--no-env-file', '-e', preparation], {
    cwd: repositoryRoot, env: process.env, encoding: 'utf8', stdio: 'pipe', timeout: 120_000, maxBuffer: 16 * 1024 * 1024,
  }))
  execFileSync(process.env.CC_HAHA_GO_EXECUTABLE || 'go', ['build', '-o', executable, './cmd/claude-haha'], {
    cwd: path.join(repositoryRoot, 'backend'),
    env: buildEnv,
    timeout: 120_000,
    stdio: 'pipe',
  })
}, 130_000)
afterAll(async () => { if (root) removeTestSandbox(root, 'storage relocation worker') })

async function fixture(name: string, invalid = false) {
  const home = path.join(root, name, 'home')
  const source = path.join(home, '.claude')
  const target = path.join(root, name, 'new-data')
  const userData = path.join(root, name, 'profile')
  await fs.mkdir(source, { recursive: true })
  await fs.mkdir(userData, { recursive: true })
  await fs.writeFile(path.join(userData, 'app-mode.json'), '{"mode":"default","unknown":true}')
  await fs.writeFile(path.join(source, 'settings.json'), '{"unknownShared":42}')
  const directory = path.join(source, 'connectors', 'runtime', 'dingtalk', '1.0.50-win32-x64')
  await fs.mkdir(path.join(source, 'connectors'), { recursive: true })
  const state = { connectors: { dingtalk: { enabled: true, unknown: 'keep', installation: {
    directory, command: path.join(directory, 'dws.exe'), args: [],
    env: { DWS_CONFIG_DIR: path.join(source, 'connectors', 'accounts', 'dws'), FUTURE: { keep: true } },
  } } } }
  await fs.writeFile(path.join(source, 'connectors', 'state.json'), invalid ? '{"schemaVersion":100,"connectors":{}}' : JSON.stringify(state))
  const skill = path.join(source, 'connectors', 'marketplace', 'plugins', 'office-dingtalk', 'skills', 'office-dingtalk', 'SKILL.md')
  await fs.mkdir(path.dirname(skill), { recursive: true })
  await fs.writeFile(skill, 'old generated skill')
  const calls: string[] = []
  const hooks: DataMigrationHooks = {
    preview: async () => ({ activeTasks: 0, externalProcesses: 0 }),
    quiesce: async () => { calls.push('quiesce') },
    relocate: async (sourceDir, targetDir, stagedDir, signal) => {
      calls.push('go-relocate')
      await runStorageRelocationWorker({ desktopRoot, request: { sourceDir, targetDir, stagedDir }, signal, env: { ...env, HOME: home, USERPROFILE: home, CLAUDE_CONFIG_DIR: source } })
    },
    resume: async () => { calls.push('resume') },
    restart: () => { calls.push('restart') },
    progress: vi.fn(),
    platform: process.platform,
    credentialStore: { read: async () => null, write: async () => {}, remove: async () => {} },
  }
  const app = { getPath(name: string) { return name === 'home' ? home : name === 'userData' ? userData : path.join(desktopRoot, 'cc-haha') } }
  return { home, source, target, userData, skill, calls, migration: new DataMigration(app, hooks, {}) }
}

describe('actual Go storage relocation worker at desktop migration entry', () => {
  it('quiesces, rebases old metadata, regenerates skill, and publishes with unknown fields preserved', async () => {
    const f = await fixture('success')
    const preview = await f.migration.prepare(f.target)
    await f.migration.start(preview.id)
    await f.migration.wait()
    expect(f.calls).toEqual(['quiesce', 'go-relocate', 'restart'])
    expect(f.migration.status?.stage).toBe('restarting')
    const state = JSON.parse(await fs.readFile(path.join(f.target, 'connectors', 'state.json'), 'utf8'))
    expect(state.connectors.dingtalk).toMatchObject({ enabled: true, unknown: 'keep', installation: { directory: expect.stringContaining(f.target), env: { FUTURE: { keep: true }, DWS_CONFIG_DIR: expect.stringContaining(f.target) } } })
    const skill = await fs.readFile(path.join(f.target, path.relative(f.source, f.skill)), 'utf8')
    expect(skill).toContain(f.target)
    expect(skill).toContain('@1.0.50')
    expect(skill).not.toContain(f.source)
    expect(await fs.readFile(f.skill, 'utf8')).toBe('old generated skill')
    expect(await fs.readFile(path.join(f.target, 'settings.json'), 'utf8')).toBe('{"unknownShared":42}')
    expect(JSON.parse(await fs.readFile(path.join(f.userData, 'app-mode.json'), 'utf8')).portable_dir).toBe(f.target)
  }, 20_000)

  it('propagates actual Go failure and restores the source pointer without publishing the stage', async () => {
    const f = await fixture('failure', true)
    const before = await fs.readFile(path.join(f.userData, 'app-mode.json'), 'utf8')
    const preview = await f.migration.prepare(f.target)
    await f.migration.start(preview.id)
    await f.migration.wait()
    expect(f.migration.status).toMatchObject({ stage: 'failed', error: expect.stringContaining('Unsupported connector state version') })
    expect(f.calls).toEqual(['quiesce', 'go-relocate', 'resume'])
    expect(await fs.readFile(path.join(f.userData, 'app-mode.json'), 'utf8')).toBe(before)
    expect(await fs.readdir(f.target)).toEqual([])
    expect(await fs.readFile(path.join(f.source, 'settings.json'), 'utf8')).toBe('{"unknownShared":42}')
  }, 20_000)

  it('joins a real cancelled child before rejecting and refuses an ambiguous backend', async () => {
    const controller = new AbortController()
    const promise = runStorageRelocationWorker({ desktopRoot, request: { sourceDir: root, targetDir: root, stagedDir: root }, env, signal: controller.signal, spawnFn: () => {
      const child = spawn(process.execPath, ['-e', 'setInterval(()=>{},1000)'], { stdio: ['pipe', 'pipe', 'pipe'] })
      child.once('spawn', () => controller.abort(new Error('cancel physical relocation')))
      return child
    } })
    await expect(promise).rejects.toThrow('cancel physical relocation')
    expect(() => runStorageRelocationWorker({ desktopRoot, request: { sourceDir: root, targetDir: root, stagedDir: root }, env: { ...env, CC_HAHA_BACKEND: 'typescript' }, signal: new AbortController().signal })).toThrow('backend is Go')
  })
})
