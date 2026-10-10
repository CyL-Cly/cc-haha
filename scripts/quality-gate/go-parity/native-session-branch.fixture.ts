import { join } from 'node:path'
import { mkdir, writeFile, readFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Native session branch')
if (process.env.CC_HAHA_SESSION_BRANCH_FIXTURE !== '1') throw new Error('Unowned session branch fixture')
const capabilities = await installClientFixtureCapabilities(home)
const RealDate = Date
;(globalThis as any).Date = class extends RealDate {
  constructor(...args: any[]) { super(args.length ? args[0] : input.now) }
}
const { createSessionBranch } = await import('../../../src/utils/sessionBranching')
const results = []
for (const [index, row] of input.cases.entries()) {
  const dir = join(home, '.claude', 'projects', String(index))
  await mkdir(dir, { recursive: true, mode: 0o700 })
  const path = join(dir, input.sessionId + '.jsonl')
  if (!row.missing) await writeFile(path, row.content, { mode: 0o600 })
  for (const [n, title] of (row.titles ?? []).entries()) {
    await writeFile(join(dir, 'sibling-' + n + '.jsonl'), JSON.stringify({ type: 'custom-title', customTitle: title }) + '\n')
  }
  try {
    const result = await createSessionBranch({ sourceSessionId: input.sessionId, sourceTranscriptPath: path, ...row.options })
    results.push({ result, disk: await readFile(result.forkPath, 'utf8') })
  } catch (error) {
    const e = error as any
    results.push({ error: { code: e.code, message: e.message } })
  }
}
if (capabilities.blocked.length) throw new Error('Branch accessed blocked external capability: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ results, capabilities }))
