#!/usr/bin/env bun

import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { createRequire } from 'node:module'
import { createHermeticGoEnvironment, hydrateGoDependencies } from '../pr/go-dependencies'

const { nativeName } = createRequire(import.meta.url)('./launch.cjs') as { nativeName: (platform?: string, arch?: string) => string }
export const CLI_TARGETS = ['darwin/amd64', 'darwin/arm64', 'linux/amd64', 'linux/arm64', 'windows/amd64', 'windows/arm64'] as const
type Result = { exitCode: number; stdout: string; stderr: string }
type Execute = (command: string[], options: { cwd: string; env: Record<string, string> }) => Promise<Result>

export function cliBinaryName(target: string) {
  if (!(CLI_TARGETS as readonly string[]).includes(target)) throw new Error(`Unsupported Go CLI target: ${target}`)
  const [os, arch] = target.split('/')
  return nativeName(os === 'windows' ? 'win32' : os, arch === 'amd64' ? 'x64' : arch)
}

function writableDirectories(path: string) {
  if (!existsSync(path) || !statSync(path).isDirectory()) return
  chmodSync(path, 0o700)
  for (const entry of readdirSync(path, { withFileTypes: true })) {
    if (entry.isDirectory()) writableDirectories(join(path, entry.name))
  }
}

async function execute(command: string[], options: { cwd: string; env: Record<string, string> }): Promise<Result> {
  const child = Bun.spawn(command, { ...options, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
  const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
  return { exitCode, stdout, stderr }
}

export async function buildCLI(options: {
  rootDir: string
  target?: string
  outputDir?: string
  version?: string
  sourceEnvironment?: NodeJS.ProcessEnv
  execute?: Execute
}) {
  const root = resolve(options.rootDir)
  const target = options.target ?? `${process.platform === 'win32' ? 'windows' : process.platform}/${process.arch === 'x64' ? 'amd64' : process.arch}`
  const name = cliBinaryName(target)
  const version = options.version ?? JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version
  if (typeof version !== 'string' || !/^[A-Za-z0-9.+_-]+$/.test(version)) throw new Error('CLI build requires a version without whitespace or linker expressions')
  const home = mkdtempSync(join(tmpdir(), 'cc-haha-cli-build-'))
  chmodSync(home, 0o700)
  const env = createHermeticGoEnvironment(home, options.sourceEnvironment)
  const telemetry = join(home, 'telemetry')
  mkdirSync(telemetry, { recursive: true, mode: 0o700 })
  writeFileSync(join(telemetry, 'mode'), 'off\n', { mode: 0o600 })
  env.GOTELEMETRY = 'off'
  env.TEST_TELEMETRY_DIR = telemetry
  for (const path of [env.CLAUDE_CONFIG_DIR, env.XDG_CONFIG_HOME, env.TMPDIR, env.GOPATH, env.GOMODCACHE, env.GOCACHE]) {
    mkdirSync(path, { recursive: true, mode: 0o700 })
    chmodSync(path, 0o700)
  }
  const go = options.sourceEnvironment?.CC_HAHA_GO_EXECUTABLE || 'go'
  const run = options.execute ?? execute
  const runner: Execute = (command, settings) => run([command[0] === 'go' ? go : command[0], ...command.slice(1)], settings)
  const cwd = join(root, 'backend')
  const checked = async (args: string[]) => {
    const result = await runner(['go', ...args], { cwd, env })
    if (result.exitCode !== 0) throw new Error(`Go CLI ${args[0]} failed: ${result.stderr || result.stdout}`)
    return result.stdout
  }
  try {
    // Verify the physical off file before dependency hydration's first Go call.
    const state = JSON.parse(await checked(['env', '-json', 'GOTELEMETRY', 'GOTELEMETRYDIR', 'GOENV', 'GOMODCACHE', 'GOCACHE']))
    if (state.GOTELEMETRY !== 'off' || resolve(state.GOTELEMETRYDIR) !== telemetry || state.GOENV !== ''
      || state.GOMODCACHE !== env.GOMODCACHE || state.GOCACHE !== env.GOCACHE || readFileSync(join(telemetry, 'mode'), 'utf8') !== 'off\n') {
      throw new Error('Go CLI compiler did not accept the private offline telemetry environment')
    }
    await hydrateGoDependencies(root, env, { execute: runner, sourceEnvironment: options.sourceEnvironment })
    const [GOOS, GOARCH] = target.split('/')
    Object.assign(env, { GOOS, GOARCH, CGO_ENABLED: '0', GOAMD64: 'v1', GOARM64: 'v8.0' })
    const output = join(resolve(options.outputDir ?? join(root, 'bin', 'native')), name)
    mkdirSync(dirname(output), { recursive: true })
    await checked(['build', '-trimpath', '-ldflags', `-X main.version=${version}`, '-o', output, './cmd/claude-haha'])
    if (!statSync(output).isFile()) throw new Error('Go CLI build did not produce a regular executable')
    chmodSync(output, 0o755)
    return { output, target, version }
  } finally {
    writableDirectories(home)
    rmSync(home, { recursive: true, force: true })
  }
}

if (import.meta.main) {
  const args = process.argv.slice(2)
  if (args[0] === '--') args.shift()
  const values: Record<string, string> = {}
  for (let index = 0; index < args.length; index += 2) {
    if (!['--target', '--output-dir', '--version'].includes(args[index]) || !args[index + 1] || args[index + 1].startsWith('--')) throw new Error(`Invalid CLI build argument: ${args[index]}`)
    values[args[index]] = args[index + 1]
  }
  const result = await buildCLI({ rootDir: resolve(import.meta.dir, '../..'), target: values['--target'], outputDir: values['--output-dir'], version: values['--version'], sourceEnvironment: process.env })
  console.log(`Built Go CLI ${result.version} (${result.target}): ${result.output}`)
}
