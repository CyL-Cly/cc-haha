import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Exact resume boundary')
if (process.env.CC_HAHA_RESUME_BOUNDARY_FIXTURE !== '1') throw new Error('Missing exact resume fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir)
bootstrap.setOriginalCwd(input.homeDir)
bootstrap.setProjectRoot(input.homeDir)
bootstrap.switchSession('bbbbbbbb-cccc-dddd-eeee-ffffffffffff' as any)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { loadConversationForResume } = await import('../../../src/utils/conversationRecovery')
const { flushSessionStorage } = await import('../../../src/utils/sessionStorage')
const output = []
for (const row of input.rows) {
  const messages = row.messages.map((raw: string) => JSON.parse(raw))
  try {
    const result = await loadConversationForResume({ messages } as any, undefined, { resumeSessionAt: row.boundary })
    output.push({ messages: result?.messages, interruption: result?.turnInterruptionState })
  } catch (error) {
    output.push({ error: (error as Error).message })
  }
}
await flushSessionStorage()
if (capabilities.blocked.length) throw new Error(`Source capability escaped: ${capabilities.blocked.join(', ')}`)
process.stdout.write(JSON.stringify({ output }))
