import { afterAll, beforeAll, expect, test } from 'bun:test'
import { chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { createRequire } from 'node:module'
import { join, resolve } from 'node:path'
import { buildCLI } from './build'
import { createSandboxedTestEnvironment } from '../pr/test-environment'

const { nativeName, executable } = createRequire(import.meta.url)('./launch.cjs')
const { installCLI } = createRequire(import.meta.url)('./install.cjs')
const repo = resolve(import.meta.dir, '../..')
const root = realpathSync(mkdtempSync(join(tmpdir(), 'cli-launch-regression-')))
const caller = join(root, 'caller 中文 space')
const packaged = join(root, 'package space')
const node = process.env.CC_HAHA_NODE_EXECUTABLE || 'node'
let binary = ''
const env = createSandboxedTestEnvironment(join(root, 'home'), {}, process.env)

beforeAll(async () => {
  mkdirSync(caller, { recursive: true })
  mkdirSync(join(packaged, 'scripts/cli'), { recursive: true })
  mkdirSync(join(packaged, 'backend/cmd/claude-haha'), { recursive: true })
  writeFileSync(join(packaged, 'backend/go.mod'), 'module fixture.test/cli\n\ngo 1.25.7\n')
  writeFileSync(join(packaged, 'backend/cmd/claude-haha/main.go'), `package main
import("encoding/json";"fmt";"io";"os";"os/signal";"syscall")
var version = "unset"
func main(){
 if len(os.Args)>1 && os.Args[1]=="--version" { fmt.Println(version);return }
 if len(os.Args)>1 && os.Args[1]=="--wait" { c:=make(chan os.Signal,1);signal.Notify(c,os.Interrupt,syscall.SIGTERM);fmt.Println("ready");s:=<-c;fmt.Fprintln(os.Stderr,s.String());os.Exit(23) }
 input,_:=io.ReadAll(os.Stdin);cwd,_:=os.Getwd();json.NewEncoder(os.Stdout).Encode(map[string]any{"args":os.Args[1:],"input":string(input),"cwd":cwd,"caller":os.Getenv("CALLER_DIR"),"home":os.Getenv("HOME"),"marker":os.Getenv("MARKER")});fmt.Fprintln(os.Stderr,"native stderr");os.Exit(17)
}
`)
  for (const name of ['launch.cjs', 'install.cjs']) copyFileSync(join(repo, 'scripts/cli', name), join(packaged, 'scripts/cli', name))
  const built = await buildCLI({ rootDir: packaged, version: 'fixture-1.2.3', sourceEnvironment: process.env })
  binary = built.output
  copyFileSync(join(repo, 'bin/claude-haha'), join(packaged, 'bin/claude-haha'))
  chmodSync(join(packaged, 'bin/claude-haha'), 0o755)
}, 180_000)
afterAll(() => rmSync(root, { recursive: true, force: true }))

async function run(command: string[], extra: Record<string, string> = {}, input = '') {
  const child = Bun.spawn(command, { cwd: caller, env: { ...env, ...extra }, stdin: 'pipe', stdout: 'pipe', stderr: 'pipe' })
  child.stdin.write(input)
  await child.stdin.flush()
  child.stdin.end()
  const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
  return { code, stdout, stderr }
}

test('npm packaging launch preserves actual native Go arguments, caller cwd, unicode stdin, stderr and exit status', async () => {
  const args = ['--print', '中文 😀', 'literal $() `text`', '--server-option=value with space', '']
  const input = '多行\n粘贴 👩‍💻\n'
  const output = await run([node, join(packaged, 'scripts/cli/launch.cjs'), ...args], { MARKER: 'original', CLAUDE_CODE_FORCE_RECOVERY_CLI: '1' }, input)
  expect(output.code).toBe(17)
  expect(output.stderr).toBe('native stderr\n')
  expect(JSON.parse(output.stdout)).toEqual({ args, input, cwd: caller, caller, home: env.HOME, marker: 'original' })
  const explicit = await run([node, join(packaged, 'scripts/cli/launch.cjs')], { CALLER_DIR: '/original/caller' })
  expect(JSON.parse(explicit.stdout).caller).toBe('/original/caller')
})

test('direct Unix launcher resolves relative symlinks and execs native Go with no JavaScript runtime', async () => {
  if (process.platform === 'win32') return
  const linked = join(root, 'linked')
  symlinkSync('package space/bin/claude-haha', linked)
  const output = await run([linked, '--version'], { PATH: '/usr/bin:/bin' })
  expect(output).toEqual({ code: 0, stdout: 'fixture-1.2.3\n', stderr: '' })
  const args = ['中文', 'literal $()']
  const value = await run([join(packaged, 'bin/claude-haha'), ...args], { PATH: '/usr/bin:/bin' }, 'input 😀')
  expect(JSON.parse(value.stdout)).toMatchObject({ args, input: 'input 😀', cwd: caller, caller })
})

test('missing and non-executable native artifacts fail explicitly without invoking Source or recovery', async () => {
  const missing = join(root, 'missing native')
  for (const command of [[node, join(packaged, 'scripts/cli/launch.cjs')], ...(process.platform === 'win32' ? [] : [[join(packaged, 'bin/claude-haha')]])]) {
    const result = await run(command, { CC_HAHA_GO_BINARY: missing, CLAUDE_CODE_FORCE_RECOVERY_CLI: '1' })
    expect(result.code).toBe(1)
    expect(result.stdout).toBe('')
    expect(result.stderr).toContain('Go executable is missing')
    expect(result.stderr).toContain('build:cli')
  }
  expect(() => executable(packaged, { CC_HAHA_GO_BINARY: 'relative' })).toThrow('absolute')
  if (process.platform !== 'win32') {
    const readonly = join(root, 'not executable')
    writeFileSync(readonly, 'fixture', { mode: 0o600 })
    expect(() => executable(packaged, { CC_HAHA_GO_BINARY: readonly })).toThrow('not executable')
  }
  expect(nativeName('win32', 'arm64')).toBe('claude-haha-windows-arm64.exe')
  expect(() => nativeName('aix', 'x64')).toThrow('Unsupported')
})

test('explicit-prefix install produces a standalone native executable and leaves no launcher or runtime dependency', async () => {
  const prefix = join(root, 'installed 中文 space')
  expect(() => installCLI({ root: packaged })).toThrow('explicit absolute')
  expect(() => installCLI({ root: packaged, prefix: 'relative' })).toThrow('explicit absolute')
  const installed = installCLI({ root: packaged, prefix })
  expect(readFileSync(installed).equals(readFileSync(binary))).toBe(true)
  const output = await run([installed, '--version'], { PATH: '/unavailable' })
  expect(output).toEqual({ code: 0, stdout: 'fixture-1.2.3\n', stderr: '' })
  expect(existsSync(`${installed}.install-${process.pid}`)).toBe(false)
  const windows = installCLI({ root: packaged, prefix: join(root, 'windows-install'), source: binary, platform: 'win32', arch: 'arm64' })
  expect(windows).toEndWith('claude-haha.exe')
})

test('npm process forwards interrupts and returns the native shutdown status', async () => {
  if (process.platform === 'win32') return
  for (const signal of ['SIGINT', 'SIGTERM'] as const) {
    const child = Bun.spawn([node, join(packaged, 'scripts/cli/launch.cjs'), '--wait'], { cwd: caller, env, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
    const reader = child.stdout.getReader()
    const ready = await reader.read()
    expect(new TextDecoder().decode(ready.value)).toBe('ready\n')
    reader.releaseLock()
    child.kill(signal)
    expect(await child.exited).toBe(23)
    expect(await new Response(child.stderr).text()).toContain(signal === 'SIGINT' ? 'interrupt' : 'terminated')
  }
})
