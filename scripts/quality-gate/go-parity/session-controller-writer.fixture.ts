import * as fs from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Headless actual adopted writer')
if (process.env.CC_HAHA_SESSION_CONTROLLER_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || Object.keys(input).some(key => !['home','writerOnly','origin','temporary','sid','continue','forkSession','checkpointing','disabled','resumeInterrupted','resumeAt','model','sdk','stat','runtime'].includes(key))) throw new Error('Original private writer input required')
const capabilities = await installClientFixtureCapabilities(home)
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'policy')
;(globalThis as any).MACRO = { VERSION: 'fixture-version', BUILD_TIME: '', PACKAGE_URL: '', README_URL: '', ISSUES_URL: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setIsInteractive(false)
bootstrap.switchSession('11111111-1111-4111-8111-111111111111')
const settings = await import('../../../src/utils/settings/settings')
settings.getInitialSettings()
process.env.USER_TYPE = 'external'
const config = await import('../../../src/utils/config')
config.enableConfigs()
const storage = await import('../../../src/utils/sessionStorage')
const path = join(home, '.claude', 'projects', home.replace(/[^a-zA-Z0-9]/g, '-'), '11111111-1111-4111-8111-111111111111.jsonl')
storage.adoptResumedSessionFile()
await storage.recordQueueOperation({ type: 'queue-operation', operation: 'enqueue', timestamp: '2025-01-01T00:00:00Z', sessionId: '11111111-1111-4111-8111-111111111111', content: 'original adopted queued entry' })
if (input.writerOnly === 'replace') {
  fs.renameSync(path, path + '.old')
  fs.writeFileSync(path, 'replacement-sentinel\n')
} else if (input.writerOnly === 'delete') fs.unlinkSync(path)
else if (input.writerOnly === 'policy-off') bootstrap.setSessionPersistenceDisabled(true)
await storage.flushSessionStorage()
const observed = { exists: fs.existsSync(path), bytes: fs.existsSync(path) ? fs.readFileSync(path, 'utf8') : null }
process.stdout.write(JSON.stringify({ observed, capabilities }))
