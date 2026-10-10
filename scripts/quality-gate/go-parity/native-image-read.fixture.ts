import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Image Read physical source')
if (process.env.CC_HAHA_IMAGE_READ_FIXTURE !== '1') throw new Error('Unowned Image Read fixture')
const { readImageWithTokenBudget } = await import('../../../src/tools/FileReadTool/FileReadTool')
const rows = await Bun.stdin.json()
const output = []
for (const row of rows) {
  try { output.push({ value: await readImageWithTokenBudget(row.path, row.tokens) }) }
  catch (error) { output.push({ error: (error as Error).message }) }
}
process.stdout.write(JSON.stringify(output))
