import fs from 'node:fs/promises'
import path from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual process worker resume selection')
if (process.env.CC_HAHA_TEAM_WORKER_RESUME_FIXTURE !== '1') throw new Error('Missing worker resume selection fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
Bun.plugin({ name: 'original-worker-resume-selection', setup(build) {
  build.onLoad({ filter: /\/conversationService\.ts$/ }, async args => {
    const source = await Bun.file(args.path).text()
    const marker = '    const shouldResume = options?.teamWorker'
    const start = source.indexOf(marker)
    const end = source.indexOf('\n    const shouldReplacePlaceholder', start)
    if (start < 0 || end < 0) throw new Error('Original worker resume selection moved')
    const declaration = source.slice(start, end)
    // This is the original startSession declaration, with its real imported
    // SessionService file discovery. Only its result is exposed for observation.
    return { contents: source + `\nexport async function __observeWorkerResume(options: any, sessionId: string) { const launchInfo = null;\n${declaration}\nreturn shouldResume }\n`, loader: 'ts' }
  })
} })
const conversation: any = await import('../../../src/server/services/conversationService')
const rows = []
for (const [i, row] of input.rows.entries()) {
  const sessionId = `11111111-2222-4333-8444-${String(i).padStart(12, '0')}`
  const directory = path.join(input.homeDir, '.claude', 'projects', `worker-${i}`)
  await fs.mkdir(directory, { recursive: true })
  if (row.contents !== null) await fs.writeFile(path.join(directory, sessionId + '.jsonl'), row.contents)
  const shouldResume = await conversation.__observeWorkerResume({ teamWorker: { resume: row.requested } }, sessionId)
  rows.push({ shouldResume })
}
if (capabilities.blocked.length) throw new Error('Original worker resume fixture escaped: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ rows, capabilities }))
process.exit(0)
