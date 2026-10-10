import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Dedicated SDK OAuth recovery')
if (process.env.CC_HAHA_SDK_OAUTH_FIXTURE !== '1') throw new Error('Missing SDK OAuth fixture')
Object.defineProperty(process, 'platform', { value: 'linux' })
await import('../../../preload')
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
spyOn(console, 'log').mockImplementation(() => {})
const { hahaOAuthService } = await import('../../../src/server/services/hahaOAuthService')
await hahaOAuthService.saveTokens(input.tokens)
const { ConversationService } = await import('../../../src/server/services/conversationService')
const service: any = new ConversationService(), sent: any[] = [], forwarded: any[] = []
const session: any = { sdkMessages: [], pendingPermissionRequests: new Map(), outputCallbacks: [(value: any) => forwarded.push(value)], sdkToken: 'private-fixture-token', sdkSocket: { send: (line: string) => sent.push(JSON.parse(line)) }, pendingOutbound: [], workDir: input.homeDir, usesOfficialOAuth: input.official, officialOAuthToken: input.token }
service.sessions.set('fixture', session)
for (let i = 0; i < 20; i++) service.handleSdkPayload('fixture', JSON.stringify({ type: 'system', subtype: 'api_retry', error_status: ' 401 ' }))
await session.officialOAuthRefreshPromise
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ sent, forwarded, token: session.officialOAuthToken }), () => resolve()))
process.exit(0)
