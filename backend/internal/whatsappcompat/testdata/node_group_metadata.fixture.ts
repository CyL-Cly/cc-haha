import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'WhatsApp group metadata')
if (process.env.CC_HAHA_WHATSAPP_LEAF !== '1') throw new Error('Unowned group metadata fixture')
const adapterRequire = createRequire(new URL('../../../../adapters/package.json', import.meta.url))
const leaf = join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')), 'Socket/groups.js')
const { extractGroupMetadata } = await import(leaf)
const input = await Bun.stdin.json()
function wire(node: any): any {
  return {
    tag: node.tag,
    attrs: node.attrs,
    content: typeof node.content === 'string'
      ? Buffer.from(node.content)
      : Array.isArray(node.content) ? node.content.map(wire) : undefined
  }
}
// The two SDKs expose different names for the same metadata. This projection
// includes every shared group metadata field; the original decoder runs intact.
function shared(metadata: any) {
  return {
    id: metadata.id,
    subject: metadata.subject ?? '',
    subjectOwner: metadata.subjectOwner ?? '',
    subjectOwnerPn: metadata.subjectOwnerPn ?? '',
    subjectTime: metadata.subjectTime,
    creation: metadata.creation,
    owner: metadata.owner ?? '',
    ownerPn: metadata.ownerPn ?? '',
    ownerCountryCode: metadata.owner_country_code ?? '',
    description: metadata.desc ?? '',
    descriptionId: metadata.descId ?? '',
    descriptionOwner: metadata.descOwner ?? '',
    descriptionOwnerPn: metadata.descOwnerPn ?? '',
    descriptionTime: metadata.descTime ?? 0,
    linkedParent: metadata.linkedParent ?? '',
    locked: metadata.restrict,
    announce: metadata.announce,
    parent: metadata.isCommunity,
    defaultSubGroup: metadata.isCommunityAnnounce,
    approval: metadata.joinApprovalMode,
    allMemberAdd: metadata.memberAddMode,
    ephemeral: metadata.ephemeralDuration !== undefined,
    expiration: metadata.ephemeralDuration ?? 0,
    size: metadata.size,
    addressingMode: metadata.addressingMode,
    participants: metadata.participants.map((participant: any) => ({
      id: participant.id,
      phone: participant.phoneNumber ?? (participant.id.endsWith('@s.whatsapp.net') ? participant.id : ''),
      lid: participant.lid ?? (participant.id.endsWith('@lid') ? participant.id : ''),
      admin: participant.admin === 'admin' || participant.admin === 'superadmin',
      superadmin: participant.admin === 'superadmin'
    }))
  }
}
process.stdout.write(JSON.stringify(input.map((node: any) => shared(extractGroupMetadata({ tag: 'iq', attrs: {}, content: [wire(node)] })))) + '\n')
