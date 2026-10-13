import * as path from 'node:path'
const input = await Bun.stdin.json()
const source = await import(path.join(process.env.CC_HAHA_TRAJECTORY_SOURCE_ROOT!, 'src/server/services/trajectoryProjection.ts'))
const rows = input.cases.map((item: any) => source.projectTrajectoryRows(item.entries, item.options))
process.stdout.write(`TRAJECTORY_SOURCE_RESULT:${JSON.stringify(rows)}\n`)
