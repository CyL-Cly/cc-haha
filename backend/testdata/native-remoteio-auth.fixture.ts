import fs from 'node:fs'
import { mock } from 'bun:test'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
const home = assertDisposableFixtureHome(process.env.HOME, 'Original ingress FD/cache authentication')
if (process.env.CC_HAHA_REMOTEIO_FIXTURE !== '1') throw new Error('Unowned ingress auth fixture')
const input = await Bun.stdin.json()
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
const tokenDirectory = join(home, 'remote')
const tokenFile = join(tokenDirectory, '.session_ingress_token')
fs.mkdirSync(tokenDirectory, { mode: 0o700 })
if (input.fallback !== undefined) fs.writeFileSync(tokenFile, input.fallback, { mode: 0o600 })
const descriptorFile = join(home, 'descriptor-token')
fs.writeFileSync(descriptorFile, input.fdBytes ?? '', { mode: 0o600 })
const descriptor = fs.openSync(descriptorFile, 'r')
if (input.fdClosed) fs.closeSync(descriptor)
const originalRead = fs.readFileSync, originalMkdir = fs.mkdirSync, originalWrite = fs.writeFileSync
const fdPath = (process.platform === 'darwin' || process.platform === 'freebsd' ? '/dev/fd/' : '/proc/self/fd/') + descriptor
const wellKnownDirectory = '/home/claude/.claude/remote'
const wellKnownFile = wellKnownDirectory + '/.session_ingress_token'
// Remap only these two exact hard-coded native paths and the one descriptor
// opened by the guard. Original cache/parseInt/trim/fallback/persist code runs.
const mapped = {
  ...fs,
  readFileSync: ((path: any, ...args: any[]) => {
    // Closed descriptors have lost provenance in the guard. Supply exactly
    // the native EBADF completion here; no read of a reused descriptor occurs.
    if (path === fdPath && input.fdClosed) throw Object.assign(new Error('Owned descriptor was closed'), { code: 'EBADF' })
    return originalRead(path === fdPath ? descriptor : path === wellKnownFile ? tokenFile : path, ...args)
  }) as any,
  mkdirSync: ((path: any, ...args: any[]) => originalMkdir(path === wellKnownDirectory ? tokenDirectory : path, ...args)) as any,
  writeFileSync: ((path: any, ...args: any[]) => originalWrite(path === wellKnownFile ? tokenFile : path, ...args)) as any,
}
mock.module('fs', () => ({ ...mapped, default: mapped }))
await import('../../preload')
for (const key of ['CLAUDE_CODE_SESSION_ACCESS_TOKEN', 'CLAUDE_CODE_WEBSOCKET_AUTH_FILE_DESCRIPTOR', 'CLAUDE_SESSION_INGRESS_TOKEN_FILE', 'CLAUDE_CODE_REMOTE', 'CLAUDE_CODE_ORGANIZATION_UUID']) delete process.env[key]
Object.assign(process.env, input.env)
if (input.fdEnv !== undefined) process.env.CLAUDE_CODE_WEBSOCKET_AUTH_FILE_DESCRIPTOR = input.fdEnv.replaceAll('{fd}', String(descriptor))
const auth = await import('../../src/utils/sessionIngressAuth')
const results: any[] = []
for (const update of input.updates ?? [{}]) {
  Object.assign(process.env, update)
  results.push({ token: auth.getSessionIngressAuthToken(), headers: auth.getSessionIngressAuthHeaders() })
}
const persisted = fs.existsSync(tokenFile) ? fs.readFileSync(tokenFile, 'utf8') : null
const fileMode = fs.existsSync(tokenFile) ? fs.statSync(tokenFile).mode & 0o777 : null
if (!input.fdClosed) fs.closeSync(descriptor)
if (capabilities.blocked.length) throw new Error('Original ingress auth capability escape: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ results, persisted, fileMode, capabilities }))
