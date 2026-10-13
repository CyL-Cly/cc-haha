import { assertDisposableFixtureHome } from './fixture-guard'
import { parse } from 'acorn'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'disconnect policy')
if (process.env.CC_HAHA_NATIVE_DISCONNECT_FIXTURE !== '1') throw new Error('disconnect fixture guard missing')
function functions(source: string, names: string[]): string {
  const code = new Bun.Transpiler({ loader: 'ts' }).transformSync(source)
  const ast: any = parse(code, { ecmaVersion: 'latest', sourceType: 'module' })
  const found = new Map<string, string>()
  for (const entry of ast.body) {
    const node = entry.type === 'ExportNamedDeclaration' ? entry.declaration : entry
    if (node?.type === 'VariableDeclaration') {
      for (const declaration of node.declarations) if (names.includes(declaration.id?.name)) found.set(declaration.id.name, `const ${code.slice(declaration.start, declaration.end)};`)
    }
    if (node?.type === 'FunctionDeclaration' && names.includes(node.id.name)) found.set(node.id.name, code.slice(node.start, node.end))
  }
  for (const name of names) if (!found.has(name)) throw new Error(`Frozen Source function missing: ${name}`)
  return names.map(name => found.get(name)).join('\n')
}
const handler = await Bun.file(new URL('../../../src/server/ws/handler.ts', import.meta.url)).text()
const parser = await Bun.file(new URL('../../../src/server/ws/cliMessageParsing.ts', import.meta.url)).text()
const code = functions(handler, ['PENDING_PERMISSION_DISCONNECT_CLEANUP_MS', 'BACKGROUND_TASK_DISCONNECT_MAX_MS', 'isDisconnectedBackgroundOnlySession', 'hasTrackedNonAgentTasks', 'hasActiveCliRun', 'isRootModelActivity', 'trackCliRunForegroundBoundary', 'resumeCliRunForeground']) + '\n' + functions(parser, ['cliParentToolUseId'])
const run = new Function('row', `
const id='fixture';
const migrationMaintenance={isActive:!!row.migration};
const activeNonAgentTasks=new Map([[id,new Map((row.shells||[]).map((stopped,i)=>[String(i),{localStopConfirmed:stopped}]))]]);
const activeAgentTasks=new Map([[id,new Map(Array.from({length:row.agents||0},(_,i)=>[i,{}]))]]);
const activeCliRuns=new Set(row.cli?[id]:[]);
const cliRunsAwaitingBackgroundTasks=new Set(row.waiting?[id]:[]);
const hasActiveClients=()=>!!row.clients;
const hasPendingOrActiveUserTurn=()=>!!row.pending;
const hasActiveTeamWorkForParent=()=>!!row.team;
const conversationService={getPendingPermissionRequests:()=>Array(row.tools||0)};
const computerUseApprovalService={getPendingRequests:()=>Array(row.computer||0)};
let clears=0; const clearBackgroundTaskDisconnectCeiling=()=>{clears++};
${code}
for(const event of row.events||[])trackCliRunForegroundBoundary(id,event);
return {background:isDisconnectedBackgroundOnlySession(id),waiting:cliRunsAwaitingBackgroundTasks.has(id),clears,permissionCeiling:PENDING_PERMISSION_DISCONNECT_CLEANUP_MS,backgroundCeiling:BACKGROUND_TASK_DISCONNECT_MAX_MS};
`)
const exitCode = functions(handler, ['emitStoppedForNonAgentTasksAfterRuntimeExit'])
const getExit = new Function('description', `
const runtimeExitFailedSessions=new Set();
const task={taskId:'shell',description};
const activeNonAgentTasks=new Map([['fixture',new Map([['shell',task]])]]);
let result;
const emitAuthoritativeNonAgentStopped=async(_id,t)=>{result={status:t.terminalStatus,summary:t.terminalSummary};};
${exitCode}
return emitStoppedForNonAgentTasksAfterRuntimeExit('fixture').then(()=>result);
`)
const runtimeExit = await getExit('Background shell')
process.stdout.write(JSON.stringify(input.rows.map((row: any) => ({ ...run(row), runtimeExit }))))
