import path from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await Bun.stdin.text())
const home = assertDisposableFixtureHome(input.homeDir, 'Native CLI installation Source')
if (process.env.CC_HAHA_INSTALLATION_SOURCE_FIXTURE !== '1') throw new Error('Missing installation Source fixture')
const capabilities = await installClientFixtureCapabilities(home)
;(globalThis as any).MACRO = { VERSION: '1.2.3-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../preload')
const { getLatestVersion } = await import('../../src/utils/nativeInstaller/download')
const xdg = await import('../../src/utils/xdg')
const versions = []
for (const target of input.targets) {
  try { versions.push({ version: await getLatestVersion(target) }) }
  catch (error) { versions.push({ error: error instanceof Error ? error.message : String(error) }) }
}
const directories = input.environments.map((env: Record<string, string>) => ({
  versions: path.join(xdg.getXDGDataHome({ env, homedir: home }), 'claude', 'versions'),
  staging: path.join(xdg.getXDGCacheHome({ env, homedir: home }), 'claude', 'staging'),
  locks: path.join(xdg.getXDGStateHome({ env, homedir: home }), 'claude', 'locks'),
  executable: path.join(xdg.getUserBinDir({ env, homedir: home }), 'claude'),
}))
console.log(JSON.stringify({ versions, directories, blocked: capabilities.blocked }))
