import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'File analytics Source comparison')
const input = await Bun.stdin.json()
const root = input.root
let captured: unknown[] = []
mock.module(root + '/src/services/analytics/index.ts', () => ({
  logEvent: (eventName: string, metadata: unknown) => captured.push({ name: eventName, metadata }),
}))
const privacy = await import(root + '/src/utils/privacyLevel.ts')
const config = await import(root + '/src/services/analytics/config.ts')
const files = await import(root + '/src/utils/fileOperationAnalytics.ts')
const results = []
for (const row of input.cases) {
  for (const key of ['NODE_ENV', 'DISABLE_TELEMETRY', 'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC', 'CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY', 'CLAUDE_CODE_USE_AZURE_OPENAI']) delete process.env[key]
  Object.assign(process.env, row.env)
  captured = []
  if (row.file) files.logFileOperation(row.file)
  results.push({ level: privacy.getPrivacyLevel(), disabled: config.isAnalyticsDisabled(), events: captured })
}
process.stdout.write(JSON.stringify(results))
