import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import { mkdir, readFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.homeDir, 'WhatsApp actual app-state persistence')
if (process.env.CC_HAHA_WHATSAPP_LEAF !== '1') throw new Error('Unowned app-state persistence fixture')
const require = createRequire(new URL('../../../../adapters/package.json', import.meta.url))
const leaf = (name: string) => join(dirname(require.resolve('@whiskeysockets/baileys')), name)
const { proto } = await import(leaf('../WAProto/index.js'))
const { useMultiFileAuthState } = await import(leaf('Utils/use-multi-file-auth-state.js'))
const directory = join(input.homeDir, 'appstate-auth')
await mkdir(directory, { recursive: true })
const { state, saveCreds } = await useMultiFileAuthState(directory)
await saveCreds()
const id = Buffer.from('actual app-state key').toString('base64')
if (input.operation === 'seed') {
  const value = proto.Message.AppStateSyncKeyData.fromObject({
    keyData: Buffer.from([0, 1, 128, 255]),
    fingerprint: { rawId: 7, currentIndex: 9, deviceIndexes: [1, 3, 5] },
    timestamp: '1700000000123',
  })
  await state.keys.set({ 'app-state-sync-key': { [id]: value }, 'app-state-sync-version': { regular: { version: 8, hash: Buffer.alloc(128, 7), indexValueMap: {}, futureVersionField: { untouched: true } } } })
}
const loaded = (await state.keys.get('app-state-sync-key', [id]))[id]
const raw = await readFile(join(directory, `app-state-sync-key-${id.replaceAll('/', '__').replaceAll(':', '-')}.json`), 'utf8')
process.stdout.write(JSON.stringify({ directory, id, raw, keyHex: Buffer.from(loaded.keyData).toString('hex'), timestamp: loaded.timestamp.toString(), fingerprintHex: Buffer.from(proto.Message.AppStateSyncKeyFingerprint.encode(loaded.fingerprint).finish()).toString('hex') }) + '\n')
