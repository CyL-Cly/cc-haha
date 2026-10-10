import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const home = assertDisposableFixtureHome(process.env.HOME, 'Exit command')
if (process.env.CC_HAHA_EXIT_COMMAND_FIXTURE !== '1') throw new Error('Explicit private exit fixture required')
const input = await Bun.stdin.json()
const require = createRequire(new URL('../../../desktop/package.json', import.meta.url))
const ts = require('typescript')
const source = readFileSync(new URL('../../../src/commands/exit/exit.tsx', import.meta.url), 'utf8')
const ast = ts.createSourceFile('exit.tsx', source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
const selected = ast.statements.filter((node: any) =>
  (ts.isFunctionDeclaration(node) && ['call', 'getRandomGoodbyeMessage'].includes(node.name?.text)) ||
  (ts.isVariableStatement(node) && node.declarationList.declarations.some((d: any) => d.name?.text === 'GOODBYE_MESSAGES')))
if (selected.length !== 3) throw new Error('Actual Source exit functions changed')
const code = ts.transpileModule(selected.map((node: any) => node.getText(ast).replace(/^export /, '')).join('\n'), { compilerOptions: { target: ts.ScriptTarget.ESNext, jsx: ts.JsxEmit.React } }).outputText
let random = 0
const originalRandom = Math.random
Math.random = () => random
// Import after binding the RNG: lodash captures this generic primitive once.
const sample = (await import('lodash-es/sample.js')).default
const React = await import('react')
const metadata = (await import('../../../src/commands/exit/index')).default
const { parseSlashCommand } = await import('../../../src/utils/slashCommandParsing')
const rows = []
try {
  for (const row of input.rows) {
    random = row.random
    const events: unknown[] = []
    const ExitFlow = () => null
    const call = new Function('feature', 'spawnSync', 'sample', 'React', 'ExitFlow', 'isBgSession', 'gracefulShutdown', 'getCurrentWorktreeSession', code + '; return call')(
      // The actual CLI distribution omits BG_SESSIONS. External shutdown and
      // worktree state are the boundaries; original command logic stays real.
      () => false, () => { throw new Error('Disabled BG feature delegated') }, sample, React, ExitFlow,
      () => false, async (code: number, reason: string) => { events.push({ kind: 'shutdown', code, reason }) },
      () => row.worktree ? {} : null)
    const view = await call((...args: unknown[]) => events.push({ kind: 'done', args }))
    rows.push({ events, returnedNull: view === null, worktreeView: view ? { showWorktree: view.props.showWorktree, onCancel: typeof view.props.onCancel === 'function' } : null })
  }
} finally { Math.random = originalRandom }
const inputs = input.inputs.map((text: string) => { const command = parseSlashCommand(text); return !!command && (command.commandName === metadata.name || metadata.aliases.includes(command.commandName)) })
console.log(JSON.stringify({ metadata: { name: metadata.name, aliases: metadata.aliases, type: metadata.type, immediate: metadata.immediate }, inputs, rows, homeOwned: home === process.env.HOME }))
