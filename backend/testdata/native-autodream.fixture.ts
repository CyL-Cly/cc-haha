import fs from 'node:fs'
import path from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await Bun.stdin.text())
const home = assertDisposableFixtureHome(input.homeDir, 'Auto-dream Source')
if (process.env.CC_HAHA_AUTODREAM_SOURCE_FIXTURE !== '1') throw new Error('Missing auto-dream Source fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
Bun.plugin({ name: 'actual-autodream-config-exports', setup(build) {
  build.onLoad({ filter: /\/src\/services\/autoDream\/autoDream\.ts$/ }, async args => ({ contents: await Bun.file(args.path).text() + '\nexport { getConfig, isGateOpen }\n', loader: 'ts' }))
} })
;(globalThis as any).MACRO = { VERSION: '1.2.3-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../preload')
const bootstrap = await import('../../src/bootstrap/state')
fs.mkdirSync(path.join(home, 'work'), { recursive: true })
bootstrap.setOriginalCwd(path.join(home, 'work'))
bootstrap.setIsInteractive(false)
bootstrap.switchSession('11111111-1111-1111-1111-111111111111' as any)
const memory = path.join(home, 'memory')
process.env.CLAUDE_COWORK_MEMORY_PATH_OVERRIDE = memory
const locks = await import('../../src/services/autoDream/consolidationLock')
const { buildConsolidationPrompt } = await import('../../src/services/autoDream/consolidationPrompt')
const dream = await import('../../src/tasks/DreamTask/DreamTask')
const { drainSdkEvents } = await import('../../src/utils/sdkEventQueue')
const { getProjectDir } = await import('../../src/utils/sessionStorage')
const { listCandidates } = await import('../../src/utils/listSessionsImpl')
const transcript = getProjectDir(path.join(home, 'work'))
if (input.nowOverride !== undefined) Date.now = () => input.nowOverride
const now = Date.now()
const lockFile = path.join(memory, '.consolidate-lock')
const lockResults = []
for (const row of input.locks) {
  fs.rmSync(memory, { recursive: true, force: true })
  fs.mkdirSync(memory, { recursive: true })
  if (row.body !== null) {
    fs.writeFileSync(lockFile, row.body.replaceAll('$SELF', String(process.pid)))
    const at = new Date(now - row.ageMs)
    fs.utimesSync(lockFile, at, at)
  }
  const before = await locks.readLastConsolidatedAt()
  const prior = await locks.tryAcquireConsolidationLock()
  const acquired = prior !== null
  const wrote = acquired ? fs.readFileSync(lockFile, 'utf8').replace(String(process.pid), '$SELF') : null
  const stamp = acquired ? fs.statSync(lockFile).mtimeMs : null
  if (acquired) {
    if (row.finish === 'complete') await locks.completeConsolidationLock()
    if (row.finish === 'rollback') await locks.rollbackConsolidationLock(prior)
  }
  lockResults.push({ beforeAge: before === 0 ? null : now - before, priorAge: prior === null || prior === 0 ? prior : now - prior, acquired, bodyHasPrior: wrote?.includes(' auto-dream-v1 ') ?? false, exists: fs.existsSync(lockFile), completedBody: row.finish === 'complete' && acquired ? fs.readFileSync(lockFile, 'utf8') : null, stampPreserved: row.finish === 'complete' && acquired ? fs.statSync(lockFile).mtimeMs === stamp : null })
}
fs.mkdirSync(transcript, { recursive: true })
for (const file of input.files) {
  const target = path.join(transcript, file.name)
  if (file.directory) fs.mkdirSync(target)
  else fs.writeFileSync(target, '{}\n')
  fs.utimesSync(target, new Date(file.mtime), new Date(file.mtime))
}
const sessions = await locks.listSessionsTouchedSince(input.since)
const candidates = (await listCandidates(transcript, true)).map(c=>c.sessionId)
const prompts = input.prompts.map((row:any)=>buildConsolidationPrompt(row.memory,row.transcripts,row.extra))
const tasks = []
for (const row of input.tasks) {
  let state:any = { tasks:{} }
  const setter = (fn:any) => { state=fn(state) }
  const controller = new AbortController()
  const id = dream.registerDreamTask(setter,{sessionsReviewing:5,priorMtime:0,abortController:controller})
  drainSdkEvents()
  const snapshots = [JSON.parse(JSON.stringify(state.tasks[id]))]
  for (const action of row.actions) {
    if (action.type==='turn') dream.addDreamTurn(id,action.turn,action.paths,setter)
    if (action.type==='complete') dream.completeDreamTask(id,setter)
    if (action.type==='fail') dream.failDreamTask(id,setter)
    if (action.type==='kill') await dream.DreamTask.kill(id,setter)
    snapshots.push(JSON.parse(JSON.stringify(state.tasks[id])))
  }
  tasks.push({ snapshots, aborted:controller.signal.aborted, events:drainSdkEvents().map((e:any)=>({status:e.status,summary:e.summary,outputFile:e.output_file})) })
}
const configs = []
if (input.configs) {
  ;(await import('../../src/utils/config')).enableConfigs()
  process.env.USER_TYPE = 'ant'
  const growthbook = await import('../../src/services/analytics/growthbook')
  const cache = await import('../../src/utils/settings/settingsCache')
  const source:any = await import('../../src/services/autoDream/autoDream')
  for (const row of input.configs) {
    growthbook.setGrowthBookConfigOverride('tengu_onyx_plover', row.raw)
    fs.mkdirSync(path.join(home, '.claude'), { recursive: true })
    fs.writeFileSync(path.join(home, '.claude/settings.json'), JSON.stringify(row.settings ?? {}))
    cache.resetSettingsCache()
    bootstrap.setKairosActive(row.kairos ?? false)
    bootstrap.setIsRemoteMode(row.remote ?? false)
    configs.push({ config: source.getConfig(), gate: source.isGateOpen() })
  }
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ now, lockResults, sessions, candidates, transcript, prompts, tasks, configs, blocked:capabilities.blocked }), () => resolve()))
