import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.homeDir, 'Skill progress normalizer')
if (process.env.CC_HAHA_NATIVE_SKILL_PROGRESS_FIXTURE !== '1') throw new Error('Unowned Skill progress fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(home); state.setCwdState(home); state.setIsInteractive(false)
state.switchSession('11111111-1111-4111-8111-111111111111')
const { normalizeMessages, createProgressMessage } = await import('../../../src/utils/messages')
const { normalizeMessage } = await import('../../../src/utils/queryHelpers')
const { isTranscriptMessage, isChainParticipant } = await import('../../../src/utils/sessionStorage')
const cases = input.messages.map((raw: string) => {
  const normalized: any[] = normalizeMessages([JSON.parse(raw)])
  // Exact SkillTool producer selection after Source normalization; the real
  // SkillTool entrypoint itself is independently exercised by the fork fixture.
  const progress = normalized.filter(message =>
    (message.type === 'assistant' || message.type === 'user') &&
    message.message.content.some(block => block.type === 'tool_use' || block.type === 'tool_result'),
  ).map(message => createProgressMessage({
    toolUseID: 'skill_parent-message', parentToolUseID: 'parent-skill',
    data: { message, type: 'skill_progress', prompt: 'original skill prompt', agentId: 'child-agent' },
  } as any))
  return { normalized, progress, sdk: progress.flatMap(message => [...normalizeMessage(message)]),
    transcript: progress.map(message => isTranscriptMessage(message as any)),
    chain: progress.map(message => isChainParticipant(message)) }
})
if (capabilities.blocked.length) throw new Error('Source Skill progress escaped fixture: ' + JSON.stringify(capabilities.blocked))
process.stdout.write(JSON.stringify({ cases, capabilities }))
