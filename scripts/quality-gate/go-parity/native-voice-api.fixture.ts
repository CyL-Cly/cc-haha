import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { join } from 'node:path'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Native Voice API')
if (process.env.CC_HAHA_VOICE_API_FIXTURE !== '1') throw new Error('Missing Voice API fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { createSenseVoiceProvider } = await import('../../../src/server/services/voice/sensevoice/provider')
const { createVoiceService } = await import('../../../src/server/services/voice/defaultRegistry')
const { handleVoiceApi } = await import('../../../src/server/api/voice')
const provider = createSenseVoiceProvider({ dataRoot: join(input.homeDir, 'voice'), platform: { platform: 'darwin', arch: 'arm64' } })
const service = createVoiceService([provider])
const rows=[]
for (const row of input.cases) {
 const url = new URL(`http://localhost${row.path}`)
 const req = new Request(url, { method: row.method, ...(row.body === undefined ? {} : { body: row.body }), ...(row.wav === undefined ? {} : { body: Buffer.from(row.wav, 'base64') }) })
 const response = await handleVoiceApi(req,url,url.pathname.split('/').filter(Boolean),service)
 rows.push({status:response.status,body:await response.json()})
}
await provider.dispose()
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({rows,capabilities}),()=>resolve()))
process.exit(0)
