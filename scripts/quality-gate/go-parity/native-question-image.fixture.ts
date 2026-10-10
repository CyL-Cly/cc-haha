import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Native Question image normalization')
if (process.env.CC_HAHA_NATIVE_QUESTION_IMAGE_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Question image requires disposable production configuration')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir)
bootstrap.setOriginalCwd(input.homeDir)
bootstrap.setProjectRoot(input.homeDir)
bootstrap.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { normalizeMessagesForAPI } = await import('../../../src/utils/messages')
const { validateImagesForAPI } = await import('../../../src/utils/imageValidation')
const normalized = []
for (const item of input.cases ?? []) {
  bootstrap.setIsInteractive(!item.nonInteractive)
  const history = item.historySources.map((source: string) => JSON.parse(source))
  const before = JSON.stringify(history)
  try {
    const messages = normalizeMessagesForAPI(history, [], item.model)
    normalized.push({ messages: messages.map(message => JSON.stringify(message)), unchanged: before === JSON.stringify(history) })
  } catch (error) {
    normalized.push({ error: { name: (error as Error).name, message: (error as Error).message } })
  }
}
const sizes = []
for (const item of input.sizes ?? []) {
  const images = item.lengths.map((length: number) => ({ type: 'image', source: { type: 'base64', media_type: 'image/png', data: item.units === 'astral' ? '😀'.repeat(length / 2) : 'A'.repeat(length) } }))
  const messages = [{ type: 'user', message: { role: 'user', content: images } }]
  try { validateImagesForAPI(messages); sizes.push({ ok: true }) }
  catch (error) { sizes.push({ error: { name: (error as Error).name, message: (error as Error).message } }) }
}
process.stdout.write(JSON.stringify({ normalized, sizes, capabilities }))
