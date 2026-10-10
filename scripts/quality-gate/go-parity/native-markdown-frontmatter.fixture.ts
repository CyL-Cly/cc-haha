import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Native Markdown frontmatter')
if (process.env.CC_HAHA_MARKDOWN_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') {
  throw new Error('Markdown contract requires its isolated production fixture')
}

const { parseFrontmatter } = await import('../../../src/utils/frontmatterParser')
const input: string[] = JSON.parse(await new Response(Bun.stdin.stream()).text())
const output = input.map(markdown => {
  const result = parseFrontmatter(markdown)
  const properties = Object.entries(result.frontmatter).map(([key, value]) => ({
    key,
    type: value === null ? 'null' : Array.isArray(value) ? 'array' : typeof value,
    truthy: Boolean(value),
    string: String(value),
  }))
  return { frontmatter: result.frontmatter, content: result.content, properties }
})
process.stdout.write(JSON.stringify(output))
