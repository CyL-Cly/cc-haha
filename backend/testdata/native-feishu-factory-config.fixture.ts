import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Feishu factory config')
if (process.env.CC_HAHA_FEISHU_FACTORY_FIXTURE !== '1') throw new Error('Unowned Feishu factory fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { loadConfig } = await import('../../adapters/common/config')
const keys = ['FEISHU_APP_ID', 'FEISHU_APP_SECRET', 'FEISHU_DOMAIN', 'FEISHU_ENCRYPT_KEY', 'FEISHU_VERIFICATION_TOKEN']
mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
const rows = []
for (const profile of input.profiles) {
  for (const key of keys) delete process.env[key]
  Object.assign(process.env, profile.env)
  writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!, 'adapters.json'), profile.file)
  const c = loadConfig().feishu
  rows.push({ appId: c.appId, appSecret: c.appSecret, domain: c.domain, encryptKey: c.encryptKey, verificationToken: c.verificationToken })
}
process.stdout.write(JSON.stringify(rows))
