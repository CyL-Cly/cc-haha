import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
assertDisposableFixtureHome(process.env.HOME, 'Native IM workspace')
if (process.env.CC_HAHA_IM_WORKSPACE_FIXTURE !== '1') throw new Error('Unowned IM workspace fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { loadConfig, resolveAdapterWorkspace } = await import('../../../adapters/common/config')
const file = join(process.env.CLAUDE_CONFIG_DIR!, 'adapters.json')
const rows = []
for (const profile of input.profiles) {
  const warnings: string[] = []
  console.warn = value => { warnings.push(String(value)) }
  for (const key of ['ADAPTER_DEFAULT_PROJECT_DIR', 'CLAUDE_ADAPTER_DEFAULT_WORK_DIR', 'PWD', 'ADAPTER_ALLOWED_PROJECT_ROOTS', 'TELEGRAM_BOT_TOKEN', 'SLACK_BOT_TOKEN', 'SLACK_APP_TOKEN', 'ADAPTER_SERVER_URL']) delete process.env[key]
  Object.assign(process.env, profile.env)
  process.chdir(profile.cwd)
  mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
  if (profile.file === null) rmSync(file, { force: true })
  else writeFileSync(file, profile.file)
  try {
    const config = loadConfig()
    const workspace = resolveAdapterWorkspace(config, config[input.platform ?? 'telegram'])
    rows.push({ workspace, serverUrl: config.serverUrl, token: String(config[input.platform ?? 'telegram'].botToken), ...(input.platform === 'slack' ? { appToken: String(config.slack.appToken) } : {}), warnings })
  } catch (err) { rows.push({ error: err instanceof Error ? err.message : String(err), warnings }) }
}
process.stdout.write(JSON.stringify(rows))
