import fs from 'node:fs'
import path from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await Bun.stdin.text())
const home = assertDisposableFixtureHome(input.homeDir, 'Client private executable capability')
const peer = path.join(home, 'bin/private-peer')
fs.mkdirSync(path.dirname(peer), { recursive: true })
fs.writeFileSync(peer, '#!/bin/sh\nprintf private-ok', { mode: 0o700 })
let capabilities: Awaited<ReturnType<typeof installClientFixtureCapabilities>> | undefined
try {
  const grant = input.mode === 'outside-grant' ? '/bin/sh' : input.mode === 'directory-grant' ? path.dirname(peer) : peer
  capabilities = await installClientFixtureCapabilities(home, { privateExecutableFiles: [grant], approvedSpawn: request => request.args.length === 0 })
  const env = { ...process.env, PATH: path.dirname(peer)+':/usr/bin:/bin' }
  let cwd = home, command = input.mode === 'basename' || input.mode === 'foreign-path' ? 'private-peer' : peer
  if (input.mode === 'foreign-path') env.PATH = '/usr/bin:/bin'
  if (input.mode === 'wrong-home') env.HOME = '/Users/fixture-forbidden'
  if (input.mode === 'wrong-cwd') cwd = '/'
  if (input.mode === 'replace-symlink') { fs.unlinkSync(peer); fs.symlinkSync('/bin/sh', peer) }
  const child = require('node:child_process').spawn(command, input.mode === 'wrong-args' ? ['forbidden'] : [], { cwd, env, shell: input.mode === 'shell', stdio: ['pipe', 'pipe', 'pipe'] })
  let stdout = ''
  child.stdout.on('data', (data: Buffer) => { stdout += data.toString() })
  const code = await new Promise<number | null>((resolve, reject) => { child.once('error', reject); child.once('close', resolve) })
  console.log(JSON.stringify({ code, stdout, blocked: capabilities.blocked }))
} catch (error) {
  console.log(JSON.stringify({ error: String(error), blocked: capabilities?.blocked ?? [] }))
}
