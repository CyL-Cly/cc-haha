import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Wecom parent error Source')
if (process.env.CC_HAHA_WECOM_PARENT_ERROR_FIXTURE !== '1') throw new Error('Unowned Wecom parent error fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
console.log = console.warn = console.error = () => {}
mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!, 'adapters.json'), JSON.stringify({ wecom: { allowedUsers: ['U1'] } }))
const { ImChatRuntime } = await import('../../adapters/common/chat-runtime')
const notices: string[] = []
let first = true
const runtime = new ImChatRuntime({
  port: { platform: 'wecom', logPrefix: '[WeCom]', sendNotice: async (_id: string, text: string) => {
    if (first) { first = false; throw input.frame }
    notices.push(text)
  }, createResponse: () => ({ append: async () => {}, finish: async () => {} }) },
  config: {}, platformConfig: {}, bridge: {}, sessionStore: {}, httpClient: {}, defaultWorkDir: '',
} as any)
await runtime.handleInbound({ chatId: 'U1', userId: 'U1', displayName: 'U1', text: '/help' })
process.stdout.write(JSON.stringify(notices))
