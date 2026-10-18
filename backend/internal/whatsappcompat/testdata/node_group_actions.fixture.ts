import { mock } from 'bun:test'
import { EventEmitter } from 'node:events'
import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'WhatsApp group action consumers')
if (process.env.CC_HAHA_WHATSAPP_LEAF !== '1') throw new Error('Unowned group action fixture')
const requireAdapter = createRequire(new URL('../../../../adapters/package.json', import.meta.url))
const socketDirectory = join(dirname(requireAdapter.resolve('@whiskeysockets/baileys')), 'Socket')
const rows = await Bun.stdin.json()
let requests: unknown[] = []
let encodedRequests: string[] = []
let responses: any[] = []
const { encodeBinaryNode } = await import(join(socketDirectory, '../WABinary/index.js'))

function wire(node: any): any {
  return {
    tag: node.tag,
    attrs: node.attrs,
    content: typeof node.content === 'string'
      ? Buffer.from(node.content)
      : Array.isArray(node.content) ? node.content.map(wire) : undefined
  }
}

function physical(node: any): any {
  const content = Buffer.isBuffer(node.content)
    ? node.content.toString('utf8')
    : typeof node.content === 'string' ? node.content
      : Array.isArray(node.content) && node.content.length ? node.content.map(physical) : undefined
  return { tag: node.tag, attrs: node.attrs ?? {}, ...(content === undefined ? {} : { content }) }
}

// Bind only the underlying socket query boundary. The installed original
// makeGroupsSocket, groupQuery and every selected action/decoder stay intact.
mock.module(join(socketDirectory, 'chats.js'), () => ({
  makeChatsSocket: () => ({
    ws: new EventEmitter(),
    authState: { creds: { me: { id: '222:3@s.whatsapp.net' } } },
    ev: { emit() {}, createBufferedFunction: (fn: any) => fn },
    query: async (node: any) => {
      requests.push(physical(node))
      encodedRequests.push(encodeBinaryNode(node).toString('base64'))
      const response = responses.shift()
      if (!response) throw new Error('Unexpected original group query')
      return wire(response)
    }
  })
}))
const { makeGroupsSocket } = await import(join(socketDirectory, 'groups.js'))
const socket = makeGroupsSocket({})
const results = []
for (const row of rows) {
  requests = []
  encodedRequests = []
  responses = row.responses.slice()
  let value = await socket[row.method](...row.args)
  if (row.method === 'groupParticipantsUpdate' || row.method === 'groupRequestParticipantsUpdate') {
    value = value.map((item: any) => ({ jid: item.jid, status: item.status }))
  }
  results.push({ requests, encodedRequests, value, remainingResponses: responses.length })
}
process.stdout.write(JSON.stringify(results) + '\n')
