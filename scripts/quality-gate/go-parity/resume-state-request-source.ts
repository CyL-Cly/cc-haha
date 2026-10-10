// Imported only after the caller validates private HOME and exact loopback
// capabilities. No production modules are statically imported by this fixture.
async function dependencies() {
  const bootstrap = await import('../../../src/bootstrap/state'), messages = await import('../../../src/utils/messages')
  const config = await import('../../../src/utils/config'); config.enableConfigs()
  const { queryModelWithStreaming } = await import('../../../src/services/api/claude')
  const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
  const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
  const { runWithWorkload } = await import('../../../src/utils/workloadContext')
  return { bootstrap, messages, queryModelWithStreaming, getEmptyToolPermissionContext, asSystemPrompt, runWithWorkload }
}
export async function initializeStateRequest() { await dependencies() }
export async function executeStateRequest(input: { origin: string, promptSource: string }, restored: unknown, allocations: Array<unknown>) {
  const result = restored as { messages: unknown[], sessionId?: string } | null
  if (!result?.sessionId || typeof result.sessionId !== 'string') throw new Error('First request requires an actual successfully restored SID')
  const { bootstrap, messages, queryModelWithStreaming, getEmptyToolPermissionContext, asSystemPrompt, runWithWorkload } = await dependencies()
  bootstrap.switchSession(result.sessionId as Parameters<typeof bootstrap.switchSession>[0])
  bootstrap.setSdkBetas([]); bootstrap.setPromptCache1hEligible(false); bootstrap.setPromptCache1hAllowlist([])
  const history = [...result.messages, messages.createUserMessage({ content: JSON.parse(input.promptSource) })] as Parameters<typeof queryModelWithStreaming>[0]['messages']
  const generation = structuredClone(allocations), historyBefore = history.map(message => JSON.stringify(message))
  const requests: unknown[] = [], prefetchAllocations: unknown[] = [], yields: unknown[] = []
  await runWithWorkload(undefined, async () => {
    for await (const event of queryModelWithStreaming({ messages: history, systemPrompt: asSystemPrompt([]), thinkingConfig: { type: 'disabled' }, tools: [], signal: new AbortController().signal,
      options: { model: 'fixture-model', querySource: 'sdk', isNonInteractiveSession: true, agents: [], mcpTools: [], hasAppendSystemPrompt: false, getToolPermissionContext: async () => getEmptyToolPermissionContext(),
        fetchOverride: async (url, init) => { const target = new URL(String(url)); if (target.origin !== input.origin || init?.method !== 'POST') throw new Error('Restored first request escaped exact loopback POST'); prefetchAllocations.push(structuredClone(allocations)); requests.push({ path: target.pathname + target.search, method: init.method, headers: [...new Headers(init.headers)], body: String(init.body) }); return fetch(url, init) },
      },
    })) yields.push(JSON.stringify(event))
  })
  return { observation: { generation, historyBefore, requests }, sourceOnly: { prefetchAllocations, yields, finalAllocations: structuredClone(allocations) } }
}
