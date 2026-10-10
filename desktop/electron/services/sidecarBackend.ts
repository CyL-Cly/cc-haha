export const SIDECAR_BACKEND_ENV = 'CC_HAHA_BACKEND'

/** Desktop has one production backend. Reject obsolete or ambiguous overrides. */
export function assertGoSidecarEnvironment(env: NodeJS.ProcessEnv): void {
  const selected = env[SIDECAR_BACKEND_ENV]
  if (selected === undefined || selected === 'go') return
  throw new Error(`The desktop backend is Go; ${SIDECAR_BACKEND_ENV} cannot select ${JSON.stringify(selected)}`)
}

export function sidecarBinaryName(triple: string): string {
  const extension = triple.includes('windows') ? '.exe' : ''
  return `claude-sidecar-go-${triple}${extension}`
}
