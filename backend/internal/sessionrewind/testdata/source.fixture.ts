import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_REWIND_FIXTURE !== '1') throw new Error('Checkpoint comparison requires pinned private Source fixture')
if (Object.keys(input).some(key => !['home', 'messages', 'rows', 'complete', 'frozen', 'files', 'commands', 'classifierPlatform', 'classifierUserType', 'diffRequests', 'previewRequests', 'executeRequest', 'observePaths', 'modes', 'trimError', 'lateMessages'].includes(key))) throw new Error('Only original native checkpoint inputs')
const home = assertDisposableFixtureHome(input.home, 'Session rewind')
const project = join(home, 'project'), config = join(home, '.claude')
fs.mkdirSync(project, { recursive: true })
fs.mkdirSync(join(config, 'file-history', 'fixture-session'), { recursive: true })
fs.writeFileSync(join(config, '.claude.json'), JSON.stringify({ customApiKeyResponses: { approved: ['fixture'], rejected: [] } }))
fs.writeFileSync(join(config, 'settings.json'), '{}')
const transcript = join(home, 'checkpoint.jsonl')
fs.writeFileSync(transcript, (input.rows ?? []).map((row: unknown) => JSON.stringify(row)).join('\n') + '\n')
for (const file of input.files ?? []) {
  if (!file.path.startsWith(home + '/')) throw new Error('Only private native files')
  fs.mkdirSync(dirname(file.path), { recursive: true })
  fs.writeFileSync(file.path, Buffer.from(file.hex, 'hex'))
}
for (const [path, mode] of Object.entries(input.modes ?? {})) {
 if (!path.startsWith(home + '/')) throw new Error('Only private native modes')
 fs.chmodSync(path, mode as number)
}
function observedStates() {
 return (input.observePaths ?? []).map((path: string) => {
  if (!path.startsWith(home + '/')) throw new Error('Only private observable paths')
  try { const info=fs.statSync(path); return {path,exists:true,hex:fs.readFileSync(path).toString('hex'),mode:info.mode & 0o7777} }
  catch(error) { if ((error as NodeJS.ErrnoException).code==='ENOENT') return {path,exists:false};throw error }
 })
}
const initialStates = observedStates()
Object.assign(globalThis, { MACRO: { VERSION: 'fixture', PACKAGE_URL: 'fixture', NATIVE_PACKAGE_URL: 'fixture', BUILD_TIME: 'fixture', FEEDBACK_CHANNEL: 'local', VERSION_CHANGELOG: '', ISSUES_EXPLAINER: '' } })
const absent = absentManagedSettingsFixturePaths()
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absent })
const bootstrap = await import('../../../../src/bootstrap/state')
bootstrap.setOriginalCwd(project)
bootstrap.setCwdState(project)
bootstrap.setProjectRoot(project)
bootstrap.setIsInteractive(false)
bootstrap.setAllowedSettingSources(['userSettings'])
const { enableConfigs } = await import('../../../../src/utils/config')
enableConfigs()
const { sessionService } = await import('../../../../src/server/services/sessionService')
// The service boundary receives the exact same display-message records as Go.
// Its snapshot reducer and rewind algorithm run unchanged on the native JSONL.
sessionService.getSessionMessages = async () => input.messages
sessionService.getSessionMessageCwd = async (_id: string, messageId: string) => input.messages.find((message: any) => message.id === messageId)?.cwd ?? null
sessionService.getSessionMessagesWithEvidence = async () => ({ messages: input.messages, transcriptEvidenceComplete: input.complete })
sessionService.getSessionWorkDir = async () => project
;(sessionService as any).findSessionFile = async () => ({ filePath: transcript, projectDir: home })
;(sessionService as any).readTargetedJsonlEntries = async () => null
const { listSessionTurnCheckpoints, getSessionTurnCheckpointDiff, previewSessionRewind, executeSessionRewind, parseSessionRewindMode } = await import('../../../../src/server/services/sessionRewindService')
const checkpoints = await listSessionTurnCheckpoints('fixture-session', undefined, input.frozen)
const diffs = []
for (const request of input.diffRequests ?? []) {
 try { diffs.push(await getSessionTurnCheckpointDiff('fixture-session', request.selector, request.path, input.frozen)) }
 catch(error) { diffs.push({error: error instanceof Error ? error.message : String(error)}) }
}
const previews = []
for (const selector of input.previewRequests ?? []) {
 try { previews.push(await previewSessionRewind('fixture-session', selector)) }
 catch(error) { previews.push({error: error instanceof Error ? error.message : String(error)}) }
}
const { recordedCommandIsReadOnly } = await import('../../../../src/tools/BashTool/readOnlyValidation')
// Explicit pure classifier inputs; this does not claim native Windows evidence.
if (input.classifierPlatform) {
  const { getPlatform } = await import('../../../../src/utils/platform')
  getPlatform.cache.set(undefined, input.classifierPlatform)
}
process.env.USER_TYPE = input.classifierUserType ?? 'external'
const { bashCommandIsSafe_DEPRECATED } = await import('../../../../src/tools/BashTool/bashSecurity')
const commands = (input.commands ?? []).map((command: string) => ({ command, readOnly: recordedCommandIsReadOnly(command), securityPasses: bashCommandIsSafe_DEPRECATED(command).behavior === 'passthrough' }))
let execute
if (input.executeRequest) {
 const calls: string[]=[]
 const { conversationService } = await import('../../../../src/server/services/conversationService')
 conversationService.stopSessionAndWait = async () => { calls.push('stop');if(input.lateMessages) input.messages=input.lateMessages }
 // Runtime stop and transcript mutation are shared consumer seams. Product
 // physical JSONL trimming is verified separately in the native Server tests.
 sessionService.trimSessionMessagesFrom = async (_id: string, targetId: string) => {
  calls.push('trim:'+targetId)
  if(input.trimError) throw new Error(input.trimError)
  const index=input.messages.findIndex((message: any)=>message.id===targetId)
  const removedMessageIds=input.messages.slice(index).map((message: any)=>message.id)
  return {removedCount:removedMessageIds.length,removedMessageIds}
 }
 let result
 try { result=await executeSessionRewind('fixture-session',input.executeRequest.selector,parseSessionRewindMode(input.executeRequest.mode)) }
 catch(error) { result={error:error instanceof Error ? error.message:String(error)} }
 execute={result,calls,files:observedStates()}
 // Give Go the same original native inputs after the Source comparator exits.
 for (const state of initialStates) {
  if(state.exists) { fs.mkdirSync(dirname(state.path),{recursive:true});fs.writeFileSync(state.path,Buffer.from(state.hex,'hex'));fs.chmodSync(state.path,state.mode) }
  else fs.rmSync(state.path,{force:true})
 }
}
process.stdout.write(JSON.stringify({ checkpoints, diffs, previews, commands, execute, capabilities }))
