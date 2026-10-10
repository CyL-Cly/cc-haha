import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'CLI auth URL')
if (process.env.CC_HAHA_CLI_AUTH_URL_FIXTURE !== '1') throw new Error('Unowned CLI auth URL')
await import('../../preload')
const { buildAuthUrl } = await import('../../src/services/oauth/client')
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
console.log(JSON.stringify(rows.map((row: any) => buildAuthUrl(row))))
