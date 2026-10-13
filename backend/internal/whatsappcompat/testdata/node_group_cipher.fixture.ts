import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import { mkdir, readFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.homeDir, 'actual original group cipher auth')
if (process.env.CC_HAHA_WHATSAPP_LEAF !== '1') throw new Error('Unowned group cipher fixture')
const require = createRequire(new URL('../../../../adapters/package.json', import.meta.url))
const leaf = (name: string) => join(dirname(require.resolve('@whiskeysockets/baileys')), name)
const { useMultiFileAuthState } = await import(leaf('Utils/use-multi-file-auth-state.js'))
const { makeLibSignalRepository } = await import(leaf('Signal/libsignal.js'))
const { makeCacheableSignalKeyStore, addTransactionCapability } = await import(leaf('Utils/auth-utils.js'))
const group = '123-456@g.us'
const author = '111@s.whatsapp.net'
const keyID = `${group}::111::0`
const keyFile = `sender-key-${keyID.replaceAll('/', '__').replaceAll(':', '-')}.json`
const logger = { trace() {}, debug() {}, info() {}, warn() {}, error() {} }
async function peer(name: string) {
  const directory = join(input.homeDir, name)
  await mkdir(directory, { recursive: true })
  const { state, saveCreds } = await useMultiFileAuthState(directory)
  await saveCreds()
  const keys = addTransactionCapability(makeCacheableSignalKeyStore(state.keys, logger), logger, { maxCommitRetries: 10, delayBetweenTriesMs: 3000 })
  const auth = { creds: state.creds, keys }
  return { directory, state, repo: makeLibSignalRepository(auth, logger) }
}
console.info = () => {}
console.warn = () => {}
console.error = () => {}
const sender = await peer('sender')
const receiver = await peer('receiver')
if (input.operation === 'seed') {
  const packets = []
  let distribution
  for (let index = 0; index < 7; index++) {
    const result = await sender.repo.encryptGroupMessage({ group, meId: author, data: Buffer.from(`ACTUAL_GROUP_PACKET_${index}`) })
    distribution ??= result.senderKeyDistributionMessage
    packets.push(Buffer.from(result.ciphertext).toString('base64'))
  }
  await receiver.repo.processSenderKeyDistributionMessage({ item: { groupId: group, axolotlSenderKeyDistributionMessage: distribution }, authorJid: author })
  process.stdout.write(JSON.stringify({ group, author, keyID, keyFile, senderDir: sender.directory, receiverDir: receiver.directory, packets, distribution: Buffer.from(distribution).toString('base64'), senderRaw: await readFile(join(sender.directory, keyFile), 'utf8'), receiverRaw: await readFile(join(receiver.directory, keyFile), 'utf8') }) + '\n')
} else if (input.operation === 'decrypt' || input.operation === 'decrypt-with-distribution') {
  const target = input.peer === 'sender' ? sender : receiver
  if (input.operation === 'decrypt-with-distribution') await target.repo.processSenderKeyDistributionMessage({ item: { groupId: group, axolotlSenderKeyDistributionMessage: Buffer.from(input.distribution, 'base64') }, authorJid: author })
  const before = await readFile(join(target.directory, keyFile), 'utf8').catch(() => null)
  let error = null, plain = null
  try {
    plain = Buffer.from(await target.repo.decryptGroupMessage({ group, authorJid: author, msg: Buffer.from(input.packet, 'base64') })).toString()
  } catch (caught) { error = caught instanceof Error ? caught.message : String(caught) }
  const after = await readFile(join(target.directory, keyFile), 'utf8').catch(() => null)
  process.stdout.write(JSON.stringify({ plain, error, unchanged: before === after, raw: after }) + '\n')
} else if (input.operation === 'redistribute') {
  await receiver.repo.processSenderKeyDistributionMessage({ item: { groupId: group, axolotlSenderKeyDistributionMessage: Buffer.from(input.distribution, 'base64') }, authorJid: author })
  process.stdout.write(JSON.stringify({ raw: await readFile(join(receiver.directory, keyFile), 'utf8') }) + '\n')
} else if (input.operation === 'encrypt') {
  const result = await sender.repo.encryptGroupMessage({ group, meId: author, data: Buffer.from(input.plain) })
  process.stdout.write(JSON.stringify({ packet: Buffer.from(result.ciphertext).toString('base64'), raw: await readFile(join(sender.directory, keyFile), 'utf8') }) + '\n')
} else throw new Error('Unowned actual group operation')
