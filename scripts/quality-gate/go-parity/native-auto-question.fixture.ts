import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Automatic Question')
if (process.env.CC_HAHA_AUTO_QUESTION_FIXTURE !== '1') throw new Error('Missing automatic question fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths(), loopbackFetchOrigin: input.loopbackFetchOrigin })
await import('../../../preload')
spyOn(console, 'log').mockImplementation(() => {})
spyOn(console, 'info').mockImplementation(() => {})
Bun.plugin({ name: 'actual-auto-question-private-functions', setup(build) {
  build.onLoad({ filter: /\/autoQuestionDecisionService\.ts$/ }, async args => ({
    contents: `${await Bun.file(args.path).text()}\nexport { buildModelPrompt, parseModelAnswers, areQuestionsValid }`, loader: 'ts',
  }))
  build.onLoad({ filter: /\/conversationService\.ts$/ }, async args => ({
    contents: `${await Bun.file(args.path).text()}\nexport { parseAutoQuestions, autoQuestionConversationText }`, loader: 'ts',
  }))
} })
const decision: any = await import('../../../src/server/services/autoQuestionDecisionService')
const conversation: any = await import('../../../src/server/services/conversationService')
const { normalizeAutoQuestionSettings } = await import('../../../src/shared/autoQuestionSettings')
const { ProviderService } = await import('../../../src/server/services/providerService')
const { SettingsService } = await import('../../../src/server/services/settingsService')
const { sessionService } = await import('../../../src/server/services/sessionService')
const rows = input.cases.map((row: any) => {
  const questions = conversation.parseAutoQuestions(row.input)
  return {
    settings: normalizeAutoQuestionSettings(row.settings), questions,
    valid: questions ? decision.areQuestionsValid(questions) : false,
    prompt: questions ? decision.buildModelPrompt(questions, row.context) : null,
    answers: questions ? decision.parseModelAnswers(row.response, questions) : null,
    text: conversation.autoQuestionConversationText(row.messages, row.includeSidechain),
  }
})
const http = []
for (const row of input.http ?? []) {
  const provider = await new ProviderService().addProvider(row.provider)
  const answer = await decision.decideAutoQuestionAnswers({ questions: conversation.parseAutoQuestions({ questions: row.questions }), conversationText: row.context, providerId: provider.id, sessionId: 'private-fixture', signal: new AbortController().signal })
  http.push(answer)
}
const lifecycle = []
for (const row of input.lifecycle ?? []) {
  const provider = await new ProviderService().addProvider(row.provider)
  await new SettingsService().updateUserSettings({ autoQuestion: { enabled: true, timeoutMinutes: 1 } })
  const id = crypto.randomUUID()
  const directory = `${(sessionService as any).getProjectsDir()}/${(sessionService as any).sanitizePath(input.homeDir)}`
  await (await import('node:fs/promises')).mkdir(directory, { recursive: true })
  const entries = [{ type: 'session-meta', workDir: input.homeDir, runtimeProviderId: provider.id },
    ...(row.kind === 'missing_context' ? [] : [{ type: 'user', uuid: crypto.randomUUID(), sessionId: id, cwd: input.homeDir, message: { role: 'user', content: 'Prefer JSON.' } }])]
  await Bun.write(`${directory}/${id}.jsonl`, entries.map(entry => JSON.stringify(entry)).join('\n') + '\n')
  const service: any = new conversation.ConversationService(), sent: any[] = [], forwarded: any[] = []
  const session: any = { providerId: provider.id, providerConfigFingerprint: JSON.stringify(provider), usesOfficialOAuth: false,
    sdkMessages: [], pendingPermissionRequests: new Map(), outputCallbacks: [(message: any) => forwarded.push(message)],
    sdkToken: 'private-fixture', sdkSocket: { send: (line: string) => sent.push(JSON.parse(line)) }, pendingOutbound: [], workDir: input.homeDir }
  service.sessions.set(id, session)
  service.handleSdkPayload(id, JSON.stringify({ type: 'control_request', request_id: 'request', request: { subtype: 'can_use_tool', tool_name: 'AskUserQuestion', input: row.input } }))
  await new Promise(resolve => setTimeout(resolve, 5))
  const request = session.pendingPermissionRequests.get('request')
  request.autoAnswerCreatedAt -= 120_000
  if (row.kind === 'activity') service.cancelAutoQuestionAnswer(id, 'request')
  if (row.kind === 'removed') service.handleSdkPayload(id, JSON.stringify({ type: 'control_cancel_request', request_id: 'request' }))
  if (row.kind === 'disabled') await new SettingsService().updateUserSettings({ autoQuestion: { enabled: false, timeoutMinutes: 1 } })
  if (row.kind === 'provider_changed') await new ProviderService().updateProvider(provider.id, { apiKey: 'fake-changed-key' })
  service.refreshAutoQuestionSettings()
  const deadline = Date.now() + 300
  while (Date.now() < deadline && sent.length === 0) await new Promise(resolve => setTimeout(resolve, 5))
  lifecycle.push({ sent, pending: session.pendingPermissionRequests.has('request'), cancelled: request.autoAnswerCancelled === true,
    autoResolved: session.autoResolvedRequestIds?.has('request') === true,
    synthetic: forwarded.filter(message => message.type === 'control_response') })
  service.clearSessionAutoAnswerWaits(session)
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, http, lifecycle, capabilities }), () => resolve()))
process.exit(0)
