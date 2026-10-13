import { readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs'
import { open } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { spawn, execFile } from 'node:child_process'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Native shell capabilities')
const args = ['-NoProfile', '-NonInteractive', '-Command', "Write-Output 'ACTUAL_PRIVATE_CAPABILITY'; Start-Sleep -Milliseconds 300"]
const environment = { ...process.env }
const options = { cwd: home, env: environment, stdio: ['pipe', 'pipe', 'pipe'] as const, detached: false }
const capabilities = await installClientFixtureCapabilities(home, {
  readonlyExternalFiles: input.enabled ? [input.executable] : [],
  approvedSpawn: input.enabled ? request => (request.command === input.executable && JSON.stringify(request.args) === JSON.stringify(args) || request.command === '/usr/bin/pgrep' && request.args[0] === '-P') && request.options.detached === false : undefined,
})
const observations: string[] = []
const blocked = (name: string, action: () => unknown) => {
  try { action(); throw new Error('Unexpected capability: ' + name) } catch (error) {
    if (!(error as Error).message.startsWith('Client options fixture blocked ')) throw error
    observations.push(name)
  }
}
if (input.enabled) {
  statSync(input.executable)
  readFileSync(input.executable)
  const child = spawn(input.executable, args, { ...options, stdio: ['pipe', 'pipe', 'pipe'] })
  let stdout = '', stderr = ''
  child.stdout!.on('data', value => { stdout += String(value) })
  child.stderr!.on('data', value => { stderr += String(value) })
  const query = spawn('/usr/bin/pgrep', ['-P', String(child.pid)], { ...options, stdio: ['pipe', 'pipe', 'pipe'] })
  await new Promise<void>((resolve, reject) => { query.on('error', reject); query.on('close', code => code === 1 ? resolve() : reject(new Error('Owned private PowerShell acquired a descendant'))) })
  observations.push('owned-pid-query')
  await new Promise<void>((resolve, reject) => { child.on('error', reject); child.on('exit', code => code === 0 ? resolve() : reject(new Error('Native PowerShell exit ' + code))) })
  if (stdout.trim() !== 'ACTUAL_PRIVATE_CAPABILITY' || stderr) throw new Error('Native PowerShell evidence differs')
  observations.push('physical-spawn')
  const handle = await open(join(home, 'async-output.txt'), 'w')
  const fd = handle.fd
  const withFile = spawn(input.executable, args, { ...options, stdio: ['pipe', fd, fd] })
  await new Promise<void>((resolve, reject) => { withFile.on('error', reject); withFile.on('exit', code => code === 0 ? resolve() : reject(new Error('Native PowerShell FD exit ' + code))) })
  await handle.close()
  if (readFileSync(join(home, 'async-output.txt'), 'utf8').trim() !== 'ACTUAL_PRIVATE_CAPABILITY') throw new Error('Native PowerShell did not use owned fd')
  observations.push('physical-owned-fd')
  blocked('closed-fd', () => spawn(input.executable, args, { ...options, stdio: ['pipe', fd, fd] }))
} else {
  blocked('default-external-read', () => statSync(input.executable))
  blocked('default-spawn', () => spawn(input.executable, args, { ...options, stdio: ['pipe', 'pipe', 'pipe'] }))
}
blocked('external-directory', () => readdirSync(dirname(input.executable)))
blocked('external-write', () => writeFileSync(input.executable, 'must not write'))
blocked('other-executable', () => spawn('/bin/echo', ['must not execute'], { ...options, stdio: ['pipe', 'pipe', 'pipe'] }))
blocked('other-args', () => spawn(input.executable, ['-Command', 'must not execute'], { ...options, stdio: ['pipe', 'pipe', 'pipe'] }))
blocked('other-exec-api', () => execFile(input.executable, args))
blocked('arbitrary-shell', () => spawn('/bin/sh', ['-c', 'must not execute'], { ...options, stdio: ['pipe', 'pipe', 'pipe'] }))
blocked('inherited-environment', () => spawn(input.executable, args, { cwd: home, stdio: ['pipe', 'pipe', 'pipe'] }))
blocked('foreign-home', () => spawn(input.executable, args, { ...options, env: { ...environment, HOME: dirname(home) }, stdio: ['pipe', 'pipe', 'pipe'] }))
blocked('foreign-cwd', () => spawn(input.executable, args, { ...options, cwd: dirname(home), stdio: ['pipe', 'pipe', 'pipe'] }))
blocked('unowned-fd', () => spawn(input.executable, args, { ...options, stdio: ['pipe', 1, 2] }))
blocked('native-spawn', () => Bun.spawn([input.executable, ...args]))
blocked('foreign-pid-query', () => spawn('/usr/bin/pgrep', ['-P', String(process.pid)], { ...options, stdio: ['pipe', 'pipe', 'pipe'] }))
blocked('other-query-args', () => spawn('/usr/bin/pgrep', ['-a'], { ...options, stdio: ['pipe', 'pipe', 'pipe'] }))
process.stdout.write(JSON.stringify({ observations, blocked: capabilities.blocked.length }) + '\n')
