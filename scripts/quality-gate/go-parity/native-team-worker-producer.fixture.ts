import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Team worker bootstrap producer')
if (process.env.CC_HAHA_TEAM_WORKER_PRODUCER_FIXTURE !== '1') throw new Error('Missing team worker producer fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { ConversationService } = await import('../../../src/server/services/conversationService')
const { parseAgentFromJson } = await import('../../../src/tools/AgentTool/loadAgentsDir')
const { initialRuntimeEffort } = await import('../../../src/utils/swarm/teamWorkerRuntime')
const { resolveAgentTools } = await import('../../../src/tools/AgentTool/agentToolUtils')
const rows = input.cases.map((row: any) => {
  process.env.CLAUDE_CODE_DISABLE_AUTO_MEMORY = 'true'
  const service: any = new ConversationService()
  const args: string[] = service.buildSessionCliArgs('child', 'ws://127.0.0.1/sdk/child?token=private', false, { model: row.model, teamWorker: row.start })
  const index = (name: string) => args.indexOf(name)
  const agentJson = JSON.parse(args[index('--agents') + 1]!)[row.start.name]
  const agent = parseAgentFromJson(row.start.name, agentJson, 'flagSettings')!
  const fields = Object.fromEntries(Object.entries(agent).filter(([_, value]) => typeof value !== 'function'))
  const pool = row.available.map((name: string) => ({ name, aliases: [] }))
  const resolved = resolveAgentTools(agent, pool as any, false, true)
  return { fields, agentId: args[index('--agent-id') + 1], parent: args[index('--parent-session-id') + 1],
    baseTools: index('--tools') < 0 ? null : args[index('--tools') + 1]!.split(','),
    maxTurns: index('--max-turns') < 0 ? null : Number(args[index('--max-turns') + 1]),
    tools: resolved.resolvedTools.map(tool => tool.name),
    workerEffort: initialRuntimeEffort(() => 'high', true) ?? null,
    ordinaryEffort: initialRuntimeEffort(() => 'high', false),
  }
})
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, capabilities }), () => resolve()))
process.exit(0)
