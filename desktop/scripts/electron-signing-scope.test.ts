// @vitest-environment node

import { describe, expect, it } from 'vitest'
import { execFileSync } from 'node:child_process'
import { mkdir, mkdtemp, realpath, rm } from 'node:fs/promises'
import { createRequire } from 'node:module'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import afterPack from './electron-signing-scope'

function context(electronPlatformName = 'darwin') {
  return {
    electronPlatformName,
    appOutDir: '/private/fixture/app',
    outDir: '/private/fixture/out',
    arch: 3,
    targets: [],
    packager: { platformSpecificBuildOptions: {
      identity: 'fixture identity',
      hardenedRuntime: true,
      entitlements: 'build/main.plist',
      entitlementsInherit: 'build/inherit.plist',
      signIgnore: ['sidecar'],
      timestamp: 'fixture timestamp',
      notarize: { fixture: true },
      unknownSigningSetting: { keep: ['future'] },
    } },
  }
}

type Context = Parameters<typeof afterPack>[0]

describe('Electron afterPack explicit non-CU offline signing', () => {
  it('keeps the whole default mac context unchanged', () => {
    const value = context()
    const before = structuredClone(value)
    afterPack(value as unknown as Context, {})
    expect(value).toEqual(before)
  })

  it('changes only timestamp and notarize for the exact mac exclusion', () => {
    const value = context()
    const expected = structuredClone(value)
    Object.assign(expected.packager.platformSpecificBuildOptions, { timestamp: 'none', notarize: false })
    afterPack(value as unknown as Context, { CC_HAHA_SCOPE_EXCLUSIONS: 'computer-use' })
    expect(value).toEqual(expected)
  })

  it.each(['linux', 'win32'])('does not alter the %s platform context', (platform) => {
    const value = context(platform)
    const before = structuredClone(value)
    afterPack(value as unknown as Context, { CC_HAHA_SCOPE_EXCLUSIONS: 'computer-use' })
    expect(value).toEqual(before)
  })

  it('rejects unknown scope before changing any context', () => {
    const value = context()
    const before = structuredClone(value)
    expect(() => afterPack(value as unknown as Context, { CC_HAHA_SCOPE_EXCLUSIONS: 'unknown' }))
      .toThrow('CC_HAHA_SCOPE_EXCLUSIONS must be exactly computer-use or unset')
    expect(value).toEqual(before)
  })

  it('loads the real TypeScript hook through the actual packaging Node and electron-builder resolver', async () => {
    const home = await realpath(await mkdtemp(path.join(tmpdir(), 'electron-hook-node-')))
    try {
      const tmp = path.join(home, 'tmp')
      await mkdir(tmp, { mode: 0o700 })
      const hook = fileURLToPath(new URL('./electron-signing-scope.ts', import.meta.url))
      const resolvePath = createRequire(import.meta.url).resolve('app-builder-lib/out/util/resolve.js')
      const stdout = execFileSync(process.execPath, ['--input-type=module', '--eval', NODE_HOOK_CHECK, hook, resolvePath], {
        cwd: path.dirname(fileURLToPath(import.meta.url)),
        env: { HOME: home, USERPROFILE: home, TMPDIR: tmp, TMP: tmp, TEMP: tmp, PATH: '/usr/bin:/bin', LANG: 'en_US.UTF-8' },
        encoding: 'utf8',
        timeout: 15_000,
        maxBuffer: 1024 * 1024,
      })
      const rows = stdout.split(/\r?\n/).filter(line => line.startsWith('ELECTRON_SCOPE_HOOK_NODE '))
      expect(rows).toHaveLength(1)
      expect(JSON.parse(rows[0].slice('ELECTRON_SCOPE_HOOK_NODE '.length))).toMatchObject({
        resolverLoaded: true,
        defaultWholeContextUnchanged: true,
        excludedOnlyTimestampAndNotarize: true,
        otherPlatformsUnchanged: true,
        invalidScopeRejected: true,
        actualMacOptionsTimestamp: 'none',
        externalOperations: [],
      })
    } finally {
      await rm(home, { recursive: true, force: true })
    }
  }, 30_000)
})

const NODE_HOOK_CHECK = String.raw`import assert from 'node:assert/strict'
import childProcess from 'node:child_process'
import http from 'node:http'
import https from 'node:https'
import net from 'node:net'
import tls from 'node:tls'
import { syncBuiltinESMExports } from 'node:module'
import { pathToFileURL } from 'node:url'

assert.equal(process.release.name, 'node')
assert.equal(process.versions.bun, undefined)
assert.ok(process.features.typescript, 'The actual packaging Node must load erasable TypeScript')
const attempts = []
function forbid(name) {
  return () => { attempts.push(name); throw new Error('Forbidden external operation: ' + name) }
}
for (const name of ['exec', 'execFile', 'spawn', 'fork', 'execSync', 'execFileSync', 'spawnSync']) {
  childProcess[name] = forbid('child_process.' + name)
}
for (const [name, object, keys] of [
  ['http', http, ['request', 'get']],
  ['https', https, ['request', 'get']],
  ['net', net, ['connect', 'createConnection']],
  ['tls', tls, ['connect']],
]) {
  for (const key of keys) object[key] = forbid(name + '.' + key)
}
globalThis.fetch = forbid('fetch')
syncBuiltinESMExports()
const [hookPath, resolverPath] = process.argv.slice(-2)
assert.ok(hookPath.startsWith('/') && resolverPath.startsWith('/'))
const { resolveFunction } = await import(pathToFileURL(resolverPath).href)
const afterPack = await resolveFunction('module', hookPath, 'afterPack')
assert.equal(typeof afterPack, 'function')
function context(platform) {
  return {
    electronPlatformName: platform,
    appOutDir: '/private/fixture/app',
    outDir: '/private/fixture/out',
    arch: 3,
    targets: [],
    packager: { platformSpecificBuildOptions: {
      identity: undefined,
      hardenedRuntime: true,
      entitlements: 'build/main.plist',
      entitlementsInherit: 'build/inherit.plist',
      signIgnore: ['keep-sidecar'],
      notarize: true,
      unknownSigningSetting: { keep: ['future'] },
    } },
  }
}
const originalScope = process.env.CC_HAHA_SCOPE_EXCLUSIONS
try {
  delete process.env.CC_HAHA_SCOPE_EXCLUSIONS
  const ordinary = context('darwin')
  const ordinaryBefore = structuredClone(ordinary)
  await afterPack(ordinary)
  assert.deepEqual(ordinary, ordinaryBefore)
  process.env.CC_HAHA_SCOPE_EXCLUSIONS = 'computer-use'
  const excluded = context('darwin')
  const excludedBefore = structuredClone(excluded)
  await afterPack(excluded)
  excludedBefore.packager.platformSpecificBuildOptions.timestamp = 'none'
  excludedBefore.packager.platformSpecificBuildOptions.notarize = false
  assert.deepEqual(excluded, excludedBefore)
  for (const platform of ['linux', 'win32']) {
    const value = context(platform)
    const before = structuredClone(value)
    await afterPack(value)
    assert.deepEqual(value, before)
  }
  process.env.CC_HAHA_SCOPE_EXCLUSIONS = 'unknown'
  const invalid = context('darwin')
  const invalidBefore = structuredClone(invalid)
  await assert.rejects(async () => afterPack(invalid), { message: 'CC_HAHA_SCOPE_EXCLUSIONS must be exactly computer-use or unset' })
  assert.deepEqual(invalid, invalidBefore)
  await import(pathToFileURL(resolverPath.replace('/util/resolve.js', '/index.js')).href)
  const { MacPackager } = await import(pathToFileURL(resolverPath.replace('/util/resolve.js', '/macPackager.js')).href)
  const appPath = '/private/fixture/Test.app'
  const defaultOptions = ordinary.packager.platformSpecificBuildOptions
  const excludedOptions = excluded.packager.platformSpecificBuildOptions
  const originalForFile = await MacPackager.prototype.getOptionsForFile.call({ resourceList: Promise.resolve([]), platformSpecificBuildOptions: defaultOptions }, appPath, false, defaultOptions)
  const scopedForFile = await MacPackager.prototype.getOptionsForFile.call({ resourceList: Promise.resolve([]), platformSpecificBuildOptions: excludedOptions }, appPath, false, excludedOptions)
  assert.equal(originalForFile(appPath).timestamp, undefined)
  assert.equal(scopedForFile(appPath).timestamp, 'none')
  assert.equal(scopedForFile(appPath).entitlements, defaultOptions.entitlements)
  assert.equal(scopedForFile(appPath + '/Contents/Frameworks/Helper.app').entitlements, defaultOptions.entitlementsInherit)
  assert.equal(scopedForFile(appPath).hardenedRuntime, true)
  assert.deepEqual(attempts, [])
  console.log('ELECTRON_SCOPE_HOOK_NODE ' + JSON.stringify({ node: process.versions.node, typescript: process.features.typescript, resolverLoaded: true, defaultWholeContextUnchanged: true, excludedOnlyTimestampAndNotarize: true, otherPlatformsUnchanged: true, invalidScopeRejected: true, actualMacOptionsTimestamp: scopedForFile(appPath).timestamp, externalOperations: attempts }))
} finally {
  if (originalScope === undefined) delete process.env.CC_HAHA_SCOPE_EXCLUSIONS
  else process.env.CC_HAHA_SCOPE_EXCLUSIONS = originalScope
}
`
