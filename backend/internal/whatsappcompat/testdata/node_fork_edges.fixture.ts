import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import { readFileSync } from 'node:fs'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'WhatsApp fork consumer edges')
if (process.env.CC_HAHA_WHATSAPP_LEAF !== '1') throw new Error('Unowned fork edge fixture')
const input = await Bun.stdin.json()
const adapterRequire = createRequire(new URL('../../../../adapters/package.json', import.meta.url))
const leaf = (name: string) => join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')), name)
console.info = () => {}; console.warn = () => {}; console.error = () => {}
if (input.op === 'persist') {
  const { useMultiFileAuthState } = await import(leaf('Utils/use-multi-file-auth-state.js'))
  const dir = join(process.env.HOME!, 'original-auth')
  const { state } = await useMultiFileAuthState(dir)
  await state.keys.set({ session: { '111.0': input.record } })
  process.stdout.write(JSON.stringify({ raw: readFileSync(join(dir, 'session-111.0.json'), 'utf8') }) + '\n')
} else if (input.op === 'offline') {
  // Execute the installed original callback unchanged. Only its external queue
  // and event-buffer primitives are observed; no SDK message body is replaced.
  const source = readFileSync(leaf('Socket/messages-recv.js'), 'utf8')
  const start = source.indexOf('const processNode = async (type, node, identifier, exec) => {')
  const end = source.indexOf('\n    // recv a message', start)
  if (start < 0 || end < 0) throw new Error('Original processNode boundary missing')
  const outcomes: string[] = []
  const processNode = new Function('offlineNodeProcessor', 'processNodeWithBuffer', source.slice(start, end) + '\nreturn processNode')(
    { enqueue() { outcomes.push('append') } },
    async () => { outcomes.push('notify') }
  )
  for (const value of input.values) {
    await processNode('message', { attrs: { offline: value } }, 'processing message', async () => {})
  }
  process.stdout.write(JSON.stringify({ outcomes }) + '\n')
} else {
  const { initAuthCreds } = await import(leaf('Utils/auth-utils.js'))
  const { BufferJSON } = await import(leaf('Utils/generics.js'))
  const { makeLibSignalRepository } = await import(leaf('Signal/libsignal.js'))
  function peer(creds: any, values: any) {
    const keys = {
      async get(type: string, ids: string[]) { const out: any = {}; for (const id of ids) out[id] = values[type]?.[id] ?? null; return out },
      async set(updates: any) { for (const type of Object.keys(updates)) { values[type] ??= {}; for (const [id, value] of Object.entries(updates[type])) { if (value) values[type][id] = value; else delete values[type][id] } } },
      async transaction(f: any) { return await f() }
    }
    return { creds, values, repo: makeLibSignalRepository({ creds, keys } as any, { trace() {}, debug() {}, info() {}, warn() {}, error() {} } as any) }
  }
  if (input.op === 'prekey') {
    const a = peer(initAuthCreds(), {}), b = peer(initAuthCreds(), {})
    const prefixed = (p: Buffer) => Buffer.concat([Buffer.from([5]), p])
    await a.repo.injectE2ESession({ jid: '222@s.whatsapp.net', session: { registrationId: b.creds.registrationId, identityKey: prefixed(b.creds.signedIdentityKey.public), signedPreKey: { keyId: b.creds.signedPreKey.keyId, publicKey: prefixed(b.creds.signedPreKey.keyPair.public), signature: b.creds.signedPreKey.signature } } })
    let data = Buffer.from('PRIVATE_FIRST_PREKEY')
    if (input.message) {
      const { proto } = await import(leaf('../WAProto/index.js'))
      data = Buffer.concat([Buffer.from(proto.Message.encode(proto.Message.fromObject(input.message)).finish()), Buffer.from([1])])
    }
    const packet = await a.repo.encryptMessage({ jid: '222@s.whatsapp.net', data })
    process.stdout.write(JSON.stringify({ a: { creds: a.creds, values: a.values }, b: { creds: b.creds, values: b.values }, packet }, BufferJSON.replacer) + '\n')
  } else {
    if (input.now !== undefined) Date.now = () => input.now
    const b = peer(JSON.parse(JSON.stringify(input.b.creds), BufferJSON.reviver), JSON.parse(JSON.stringify(input.b.values), BufferJSON.reviver))
    const before = JSON.stringify(b.values, BufferJSON.replacer)
    let error: string | undefined, plain: string | undefined
    try { plain = (await b.repo.decryptMessage({ jid: '111@s.whatsapp.net', type: input.type, ciphertext: Buffer.from(input.ciphertext, 'base64') })).toString() }
    catch (e: any) { error = e.message }
    process.stdout.write(JSON.stringify({ plain, error, unchanged: before === JSON.stringify(b.values, BufferJSON.replacer), b: { creds: b.creds, values: b.values } }, BufferJSON.replacer) + '\n')
  }
}
