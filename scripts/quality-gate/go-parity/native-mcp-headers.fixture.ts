import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'MCP dynamic headers')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/services/mcp/headersHelper.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const functions = ast.body.flatMap((node: any) => node.type === 'FunctionDeclaration' ? [node] : node.type === 'ExportNamedDeclaration' && node.declaration?.type === 'FunctionDeclaration' ? [node.declaration] : [])
const code = functions.filter((node: any) => ['isMcpServerFromProjectOrLocalSettings', 'getMcpHeadersFromHelper', 'getMcpServerHeaders'].includes(node.id.name)).map((node: any) => source.slice(node.start, node.end)).join('\n')
if (code.split('function ').length !== 4) throw new Error('Actual Source headersHelper exports changed')
const result = []
for (const row of input.cases) {
  const requests: any[] = []
  const deps: Record<string, any> = {
    getIsNonInteractiveSession: () => row.nonInteractive,
    checkHasTrustDialogAccepted: () => row.trusted,
    process: { env: input.environment },
    execFileNoThrowWithCwd: async (...args: any[]) => { requests.push(args); if (row.throw) throw new Error('native process fixture failure'); return row.output },
    logAntError: () => {}, logEvent: () => {}, logMCPDebug: () => {}, logMCPError: () => {}, logError: () => {},
    MACRO: { FEEDBACK_CHANNEL: 'isolated fixture' }, errorMessage: (e: any) => String(e.message), jsonParse: JSON.parse,
  }
  const run = new Function(...Object.keys(deps), code+';return getMcpServerHeaders')(...Object.values(deps))
  result.push({ headers: await run('physical-header-peer', row.config), requests })
}
process.stdout.write(JSON.stringify(result))
