import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'ordinary MCP review producer')
if (process.env.CC_HAHA_MCP_REVIEW_FIXTURE !== '1') throw new Error('Unowned ordinary MCP review producer')
await import('../../preload')
const { default: review } = await import('../../src/commands/review')
const args: string[] = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (review.type !== 'prompt') throw new Error('Actual Source review command changed')
console.log(JSON.stringify(await Promise.all(args.map(arg => review.getPromptForCommand(arg, {} as any)))))
