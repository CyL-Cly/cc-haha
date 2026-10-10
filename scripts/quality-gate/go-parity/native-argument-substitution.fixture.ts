import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Native Skill arguments')
if (process.env.CC_HAHA_NATIVE_SKILL_ARGUMENT_FIXTURE !== '1') throw new Error('Undeclared Skill argument fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { parseArguments, substituteArguments } = await import('../../../src/utils/argumentSubstitution')
const observations = input.map((row: { content: string; args?: string; names: string[] }) => ({
  arguments: parseArguments(row.args ?? ''),
  content: substituteArguments(row.content, row.args, true, row.names),
}))
process.stdout.write(JSON.stringify(observations))
