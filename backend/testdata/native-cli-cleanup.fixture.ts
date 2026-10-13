import fs from 'node:fs'
import path from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await Bun.stdin.text())
const home = assertDisposableFixtureHome(input.homeDir, 'Native CLI cleanup Source')
if (process.env.CC_HAHA_INSTALLATION_SOURCE_FIXTURE !== '1') throw new Error('Missing cleanup Source fixture')
const capabilities = await installClientFixtureCapabilities(home)
;(globalThis as any).MACRO = { VERSION: '1.2.3-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../preload')
const { cleanupOldVersions } = await import('../../src/utils/nativeInstaller/installer')
const now = Date.now()
const versions = path.join(home, '.local/share/claude/versions')
const staging = path.join(home, '.cache/claude/staging')
const locks = path.join(home, '.local/state/claude/locks')
const executable = path.join(home, '.local/bin/claude')
const observations = []
for (const profile of input.profiles) {
  for (const directory of [versions, staging, locks, path.dirname(executable)]) {
    fs.rmSync(directory, { recursive: true, force: true })
    fs.mkdirSync(directory, { recursive: true })
  }
  process.env.ENABLE_PID_BASED_VERSION_LOCKING = profile.pid ? 'true' : 'false'
  for (const file of profile.files) {
    const target = path.join(versions, file.name)
    if (file.directory) fs.mkdirSync(target)
    else fs.writeFileSync(target, file.empty ? '' : 'fixture binary', { mode: file.mode })
    fs.utimesSync(target, new Date(now - file.ageMs), new Date(now - file.ageMs))
  }
  for (const entry of profile.staging ?? []) {
    const target = path.join(staging, entry.name)
    fs.mkdirSync(target)
    fs.writeFileSync(path.join(target, 'download.part'), 'staged')
    fs.utimesSync(target, new Date(now - entry.ageMs), new Date(now - entry.ageMs))
  }
  if (profile.symlink) fs.symlinkSync(path.relative(path.dirname(executable), path.join(versions, profile.symlink)), executable)
  for (const lease of profile.locks ?? []) {
    const target = path.join(locks, lease.name + '.lock')
    if (lease.directory) fs.mkdirSync(target)
    else if (lease.malformed) fs.writeFileSync(target, '{not json')
    else fs.writeFileSync(target, JSON.stringify({ pid: lease.active ? process.pid : 0, version: lease.name, execPath: process.execPath, acquiredAt: now - lease.ageMs }))
    fs.utimesSync(target, new Date(now - lease.ageMs), new Date(now - lease.ageMs))
  }
  await cleanupOldVersions()
  observations.push({ versions: fs.readdirSync(versions).sort(), staging: fs.readdirSync(staging).sort(), locks: fs.readdirSync(locks).sort(), symlink: fs.existsSync(executable) ? fs.readlinkSync(executable) : null })
}
console.log(JSON.stringify({ observations, blocked: capabilities.blocked }))
