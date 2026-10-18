import fs from 'node:fs'
import { createRequire } from 'node:module'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = await new Response(Bun.stdin.stream()).json()
const home = assertDisposableFixtureHome(input.home, 'Actual conversation metadata')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_METADATA_FIXTURE !== '1') throw new Error('Original metadata guard required')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const caps = await installClientFixtureCapabilities(home)
const path = new URL('../../../src/utils/sessionStorage.ts', import.meta.url).pathname
const source = ts.createSourceFile(path, fs.readFileSync(path, 'utf8'), ts.ScriptTarget.Latest, true)
const names = ['appendEntryToFile', 'saveCustomTitle', 'saveAgentName', 'saveAgentColor', 'saveTag']
const functions = source.statements.filter((node: any) => ts.isFunctionDeclaration(node) && node.name && names.includes(node.name.text))
if (functions.length !== names.length) throw new Error('Frozen actual metadata function identity changed')
const body = new Bun.Transpiler({ loader: 'ts' }).transformSync(functions.map((node: any) => node.getText(source)).join('\n')).replaceAll('export ', '')
const results = []
for (const [index, row] of input.rows.entries()) {
  const file = join(home, 'transcript-' + index + '.jsonl')
  if (!row.missing) fs.writeFileSync(file, row.initial, { mode: 0o644 })
  const project = { currentSessionTitle: '', currentSessionAgentName: '', currentSessionAgentColor: '', currentSessionTag: '', markMetadataPersistence: () => {} }
  const deps = {
    getSettings_DEPRECATED: () => ({ cleanupPeriodDays: row.disabled ? 0 : 30 }), getFsImplementation: () => fs,
    jsonStringify: JSON.stringify, constants: fs.constants, openSync: fs.openSync, fstatSync: fs.fstatSync,
    fchmodSync: fs.fchmodSync, fsAppendFileSync: fs.appendFileSync, closeSync: fs.closeSync,
    getSessionId: () => input.sid, getProject: () => project, getTranscriptPathForSession: () => file,
    logEvent: () => {}, updateSessionName: async () => {},
  }
  const save = new Function(...Object.keys(deps), body + '\nreturn {saveCustomTitle,saveAgentName,saveAgentColor,saveTag}')(...Object.values(deps))
  for (const action of row.actions) await save[action.name](input.sid, action.value, file)
  results.push({ exists: fs.existsSync(file), content: fs.existsSync(file) ? fs.readFileSync(file, 'utf8') : '', mode: fs.existsSync(file) ? fs.statSync(file).mode & 0o777 : 0, metadata: project })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Metadata source boundary escaped ' + JSON.stringify(caps.blocked))
process.stdout.write(JSON.stringify({ results, caps: { blocked: caps.blocked, secureReads: caps.secureReads } }))
