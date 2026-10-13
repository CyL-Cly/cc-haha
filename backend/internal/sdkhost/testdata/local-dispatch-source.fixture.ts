import { readFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
assertDisposableFixtureHome(process.env.HOME, 'Local command dispatch')
if (process.env.CC_HAHA_LOCAL_DISPATCH_FIXTURE !== '1') throw new Error('Unowned local dispatch fixture')
await installClientFixtureCapabilities(process.env.HOME!, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../../preload')
const { findCommand } = await import('../../../../src/commands')
const { parseSlashCommand } = await import('../../../../src/utils/slashCommandParsing')
const { formatCommandInputTags } = await import('../../../../src/utils/messages')
const source = readFileSync(new URL('../../../../src/QueryEngine.ts', import.meta.url), 'utf8')
const start = source.indexOf('    // A local command may perform asynchronous work inside processUserInput')
const end = source.indexOf('\n    const {', start)
if (start < 0 || end < start) throw new Error('Missing original QueryEngine local dispatch boundary')
const code = new Bun.Transpiler({ loader: 'ts' }).transformSync(`async function* dispatch(prompt, options, commands) { ${source.slice(start, end)} }`)
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const outputs = []
for (const row of input.rows) {
  const dispatch = new Function('findCommand', 'parseSlashCommand', 'formatCommandInputTags', 'isPendingSessionMessage', 'randomUUID', 'getSessionId', code + ';return dispatch')(findCommand, parseSlashCommand, formatCommandInputTags, () => row.pending === true, () => 'allocated-source-uuid', () => 'source-session')
  const iterator = dispatch(row.content, { uuid: row.uuid }, input.commands)
  outputs.push((await iterator.next()).value ?? null)
  await iterator.return()
}
process.stdout.write(JSON.stringify({ outputs, sourceSha256: createHash('sha256').update(source).digest('hex') }))
