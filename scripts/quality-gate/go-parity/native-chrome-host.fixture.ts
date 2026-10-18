import { mock } from 'bun:test'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'

const home = process.env.HOME!
const directory = process.env.CC_HAHA_SOURCE_CHROME_SOCKET_DIR!
assertDisposableFixtureHome(home, 'Chrome native host Source')
if (process.env.CC_HAHA_SOURCE_CHROME_HOST !== '1' || !directory.startsWith('/tmp/cc-ch-source-')) {
  throw new Error('Unowned Chrome native host fixture')
}
// Replace only the OS socket-location primitive. The real Source host starts
// its net.Server, manages clients, reads framing and validates/forwards JSON.
mock.module('../../../src/utils/claudeInChrome/common', () => ({
  getSocketDir: () => directory,
  getSecureSocketPath: () => join(directory, `${process.pid}.sock`),
}))
const { runChromeNativeHost } = await import('../../../src/utils/claudeInChrome/chromeNativeHost')
await runChromeNativeHost()
