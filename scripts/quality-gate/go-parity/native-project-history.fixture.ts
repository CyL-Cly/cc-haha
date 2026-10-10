import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'Project session history')
if (process.env.CC_HAHA_PROJECT_HISTORY_FIXTURE !== '1') throw new Error('Unowned project history fixture')
const { ProjectSessionHistory } = await import('../../../src/server/services/projectSessionHistory')
let rows = input.rows.map((row: any) => ({ ...row.info, transcriptPath: row.path, logicalProjectRoot: row.root }))
let now = 0
let revision = 0
let mutation = 0
let scope = 'private-scope'
let loads = 0
let hydrates = 0
let mutateHydrates = 0
let mutateLoads = 0
const deleted = new Set<string>()
const saved = new Map<string, string>()
const ids = new Map<string, number>()
const normalizeCursor = (cursor: string | null) => {
  if (cursor === null) return null
  const value = JSON.parse(Buffer.from(cursor, 'base64url').toString())
  if (!ids.has(value.snapshot)) ids.set(value.snapshot, ids.size + 1)
  return `${ids.get(value.snapshot)}:${value.offset}`
}
const history = new ProjectSessionHistory({
  now: () => now, scope: () => scope, revision: () => String(revision), mutation: () => String(mutation),
  load: async () => { loads += 1; if (mutateLoads > 0) { mutateLoads -= 1; revision += 1 }; return rows },
  hydrate: async (row: any) => {
    hydrates += 1
    if (mutateHydrates > 0) { mutateHydrates -= 1; mutation += 1 }
    if (deleted.has(row.id)) return null
    const { transcriptPath, logicalProjectRoot, ...info } = row
    return info
  },
})
const results = []
for (const op of input.operations) {
  if (op.kind === 'advance') { now += op.ms; continue }
  if (op.kind === 'delete') { for (const id of op.ids) deleted.add(id); continue }
  if (op.kind === 'revision') { revision += 1; continue }
  if (op.kind === 'scope') { scope = op.scope; continue }
  if (op.kind === 'mutate') { mutateHydrates = op.hydrates ?? 0; mutateLoads = op.loads ?? 0; continue }
  if (op.kind === 'rows') { rows = op.rows.map((row: any) => ({ ...row.info, transcriptPath: row.path, logicalProjectRoot: row.root })); revision += 1; continue }
  const options = { ...op.options }
  if (options.cursor?.startsWith('$')) options.cursor = saved.get(options.cursor.slice(1))
  if (op.offset !== undefined) {
    const value = JSON.parse(Buffer.from(options.cursor, 'base64url').toString())
    value.offset = op.offset
    options.cursor = Buffer.from(JSON.stringify(value)).toString('base64url')
  }
  try {
    const page = await history.list(options)
    if (op.save && page.nextCursor) saved.set(op.save, page.nextCursor)
    results.push({ value: { sessions: page.sessions, nextCursor: normalizeCursor(page.nextCursor) }, loads, hydrates })
  } catch (error) {
    const e = error as any
    results.push({ error: { status: e.statusCode, code: e.code, message: e.message }, loads, hydrates })
  }
}
process.stdout.write(JSON.stringify(results))
