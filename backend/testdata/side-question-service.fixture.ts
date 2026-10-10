import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'side question service Source')
if (process.env.CC_HAHA_SIDE_QUESTION_FIXTURE !== '1') throw new Error('Missing fixture marker')
const input = await Bun.stdin.json()
const { SideQuestionService, sideQuestionInputSchema } = await import('../../src/server/services/sideQuestionService')
const { ApiError } = await import('../../src/server/middleware/errorHandler')
const id = '11111111-1111-4111-8111-111111111111'
const value = sideQuestionInputSchema.parse({ questionId: id, question: ' current ', history: [] })
const outcome = async (promise: Promise<any>) => { try { return { value: await promise } } catch (e: any) { return { status: e.statusCode, code: e.code, message: e.message } } }
const rows = []
for (const profile of input.profiles) {
  const events: any[] = []
  let ensured!: () => void
  let releaseEnsure!: () => void
  const ensureCalled = new Promise<void>(r => { ensured = r })
  const ensureGate = new Promise<void>(r => { releaseEnsure = r })
  let controlled!: () => void
  let releaseControl!: () => void
  const controlCalled = new Promise<void>(r => { controlled = r })
  const controlGate = new Promise<void>(r => { releaseControl = r })
  const service = new SideQuestionService({
    ensure: async () => { events.push('ensure'); ensured(); if (profile === 'cancel-ensure') await ensureGate },
    control: async (_sid, request, timeout, signal) => {
      events.push({ request, timeout, signal: Boolean(signal) })
      if (request.subtype === 'cancel_side_question') return { cancelled: true }
      controlled()
      if (['cancel-control', 'conflict', 'timeout'].includes(profile)) await controlGate
      if (profile === 'failure') throw new Error('upstream failure')
      if (profile === 'api-error') throw ApiError.conflict('specific conflict')
      if (profile === 'invalid') return { response: 1 }
      return { response: ' response \ud800 ' }
    },
  }, profile === 'timeout' ? 15 : 1000)
  const abort = new AbortController()
  let result: any
  if (profile === 'pre-aborted') { abort.abort(); result = await outcome(service.ask('session', value, new URL('http://127.0.0.1/'), abort.signal)) }
  else if (profile === 'tombstone') { const cancelled = await service.cancel('session', id); result = { cancelled, ask: await outcome(service.ask('session', value, new URL('http://127.0.0.1/'), abort.signal)) } }
  else {
    const ask = outcome(service.ask('session', value, new URL('http://127.0.0.1/'), abort.signal))
    await ensureCalled
    if (profile === 'cancel-ensure') { const cancelled = await service.cancel('session', id); result = { cancelled, ask: await ask }; releaseEnsure() }
    else {
      await controlCalled
      if (profile === 'cancel-control') { const cancelled = await service.cancel('session', id); result = { cancelled, ask: await ask }; releaseControl() }
      else if (profile === 'conflict') { const second = await outcome(service.ask('session', { ...value, questionId: '22222222-2222-4222-8222-222222222222' }, new URL('http://127.0.0.1/'), abort.signal)); releaseControl(); result = { second, ask: await ask } }
      else if (profile === 'timeout') { result = await ask; releaseControl() }
      else { result = await ask; if (profile === 'reuse') result = { first: result, second: await outcome(service.ask('session', value, new URL('http://127.0.0.1/'), abort.signal)) } }
    }
  }
  rows.push({ profile, result, events })
}
process.stdout.write(JSON.stringify(rows))
