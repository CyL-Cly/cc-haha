import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'TUI restored history')
if (process.env.CC_HAHA_TUI_HISTORY_FIXTURE !== '1') throw new Error('Private history fixture required')
const require = createRequire(new URL('../../../desktop/package.json', import.meta.url))
const ts = require('typescript')
const source = readFileSync(new URL('../../../src/utils/fullscreen.ts', import.meta.url), 'utf8')
const ast = ts.createSourceFile('fullscreen.ts', source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
const selected = ast.statements.filter((node: any) =>
  ts.isFunctionDeclaration(node) && ['isTmuxControlModeEnvHeuristic', 'probeTmuxControlModeSync', 'isTmuxControlMode', 'isFullscreenEnvEnabled'].includes(node.name?.text))
if (selected.length !== 4) throw new Error('Original fullscreen producer changed')
const code = ts.transpileModule(selected.map((node: any) => node.getText(ast).replace(/^export /, '')).join('\n'), { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
// Load the generic compiler before the Source filesystem capability guard. Its
// own package-case probe is unrelated to Source's private state reads.
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { normalizeMessages, isNotEmptyMessage, shouldShowUserMessage, getMessagesAfterCompactBoundary } = await import('../../../src/utils/messages')
const { isEnvTruthy, isEnvDefinedFalsy } = await import('../../../src/utils/envUtils')
const fullscreen = input.fullscreen.map((row: any) => {
  let probes = 0
  const actual = new Function('process', 'isEnvTruthy', 'isEnvDefinedFalsy', 'spawnSync', 'logForDebugging', 'let loggedTmuxCcDisable=false; let tmuxControlModeProbed;\n' + code + ';return isFullscreenEnvEnabled')(
    { env: row.environment }, isEnvTruthy, isEnvDefinedFalsy,
    (command: string, args: string[], options: any) => {
      if (command !== 'tmux' || JSON.stringify(args) !== JSON.stringify(['display-message', '-p', '#{client_control_mode}']) || options.timeout !== 2000) throw new Error('Physical probe contract changed')
      probes++
      return { status: row.error ? 1 : 0, stdout: row.output ?? '' }
    }, () => {})
  return { enabled: actual(), probes }
})
const shutdownSource = readFileSync(new URL('../../../src/utils/gracefulShutdown.ts', import.meta.url), 'utf8')
const shutdownAST = ts.createSourceFile('gracefulShutdown.ts', shutdownSource, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
const hintNode = shutdownAST.statements.find((node: any) => ts.isFunctionDeclaration(node) && node.name?.text === 'printResumeHint')
if (!hintNode) throw new Error('Source resume hint disappeared')
const hintCode = ts.transpileModule(hintNode.getText(shutdownAST), { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
const hints = (input.hints ?? []).map((row: any) => {
  const writes: string[] = []
  const print = new Function('process', 'chalk', 'writeSync', 'getIsInteractive', 'isSessionPersistenceDisabled', 'getSessionId', 'sessionIdExists', 'getCurrentSessionTitle', 'let resumeHintPrinted=false;\n' + hintCode + ';return printResumeHint')(
    { stdout: { isTTY: row.tty } }, { dim: (text: string) => text },
    (fd: number, text: string) => { if (fd !== 1) throw new Error('Source wrong output FD'); writes.push(text) },
    () => row.interactive, () => row.disabled, () => row.sid, () => row.exists, () => row.title)
  print(); print()
  return writes
})
const histories = input.histories.map((row: any) => {
  const normalized = normalizeMessages(row.records).filter(isNotEmptyMessage)
  const compact = row.fullscreen ? normalized : getMessagesAfterCompactBoundary(normalized, { includeSnipped: true })
  return compact.filter((message: any) => shouldShowUserMessage(message, row.transcript)).flatMap((message: any) => {
    if (!['user', 'assistant'].includes(message.type)) return []
    return message.message.content.map((block: any) => ({ kind: message.type, block: block.type, text: block.text ?? block.thinking ?? null, id: block.id ?? block.tool_use_id ?? null, input: block.input ?? null, content: block.content ?? null, result: message.toolUseResult ?? null, isError: block.is_error === true }))
  })
})
if (capabilities.blocked.length) throw new Error('Source capabilities blocked: ' + capabilities.blocked.join(', '))
console.log(JSON.stringify({ fullscreen, histories, hints, blocked: capabilities.blocked }))
