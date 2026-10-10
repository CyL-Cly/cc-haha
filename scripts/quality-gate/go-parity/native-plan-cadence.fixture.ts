import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
if (process.env.CC_HAHA_PLAN_CADENCE_FIXTURE !== '1') throw new Error('Fixture only')
const source = readFileSync(resolve(import.meta.dir, '../../../src/utils/attachments.ts'), 'utf8')
const begin = source.indexOf('function getPlanModeAttachmentTurnCount(')
const end = source.indexOf('async function getPlanModeAttachments(', begin)
const feedbackBegin = source.indexOf('function isToolResultBlock(')
const feedbackEnd = source.indexOf('/**\n * Tools that succeeded', feedbackBegin)
if (begin < 0 || end < 0 || feedbackBegin < 0 || feedbackEnd < 0) throw new Error('Original Source anchors missing')
const code = new Bun.Transpiler({ loader: 'ts' }).transformSync(source.slice(feedbackBegin, feedbackEnd) + '\n' + source.slice(begin, end))
const observe = new Function('histories', code + '\nreturn histories.map(messages => ({...getPlanModeAttachmentTurnCount(messages), count: countPlanModeAttachmentsSinceLastExit(messages)}));')
const input = await Bun.stdin.json()
console.log(JSON.stringify(observe(input)))
