import { assertDisposableFixtureHome } from './fixture-guard'
import { hoistToolResultMediaForCompatibility, shouldHoistNestedToolResultMedia } from '../../../src/server/proxy/transform/anthropicMediaHoist'

assertDisposableFixtureHome(process.env.HOME, 'Provider media compatibility')
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
process.stdout.write(JSON.stringify(rows.map((row: { kind: string, value: never }) => {
  try {
    return row.kind === 'predicate'
      ? shouldHoistNestedToolResultMedia(row.value)
      : hoistToolResultMediaForCompatibility(row.value)
  } catch (error) {
    return { error: { name: (error as Error).name, message: (error as Error).message } }
  }
})))
