import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'IM platform credentials')
if (process.env.CC_HAHA_IM_PLATFORM_CREDENTIAL_FIXTURE !== '1') throw new Error('Unowned platform credential fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { loadConfig } = await import('../../adapters/common/config')
const keys = ['WECHAT_ACCOUNT_ID', 'WECHAT_BOT_TOKEN', 'WECHAT_BASE_URL', 'WECHAT_USER_ID', 'WECOM_BOT_ID', 'WECOM_BOT_SECRET', 'QQ_APP_ID', 'QQ_APP_SECRET']
mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
const rows = []
for (const profile of input.profiles) {
  for (const key of keys) delete process.env[key]
  Object.assign(process.env, profile.env)
  writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!, 'adapters.json'), profile.file)
  const c = loadConfig()
  rows.push({
    wechat: { accountId: c.wechat.accountId, botToken: c.wechat.botToken, baseUrl: c.wechat.baseUrl, userId: c.wechat.userId },
    wecom: { botId: c.wecom.botId, secret: c.wecom.secret },
    qq: { appId: c.qq.appId, appSecret: c.qq.appSecret },
  })
}
process.stdout.write(JSON.stringify(rows))
