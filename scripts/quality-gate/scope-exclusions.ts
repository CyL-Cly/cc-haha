export type ScopeExclusion = 'computer-use'

export function validateScopeExclusions(value: readonly ScopeExclusion[] = []): ScopeExclusion[] {
  if (!Array.isArray(value) || (value.length !== 0 && (value.length !== 1 || value[0] !== 'computer-use'))) {
    throw new Error('Only the exact computer-use scope exclusion is supported')
  }
  return value.length ? ['computer-use'] : []
}

export function readScopeExclusions(env: NodeJS.ProcessEnv = process.env): ScopeExclusion[] {
  const value = env.CC_HAHA_SCOPE_EXCLUSIONS
  if (value === undefined) return []
  if (value !== 'computer-use') throw new Error('CC_HAHA_SCOPE_EXCLUSIONS must be exactly computer-use or unset')
  return ['computer-use']
}

export function scopeEnvironment(env: NodeJS.ProcessEnv, exclusions: readonly ScopeExclusion[]): NodeJS.ProcessEnv {
  const result = { ...env }
  if (validateScopeExclusions(exclusions).length) result.CC_HAHA_SCOPE_EXCLUSIONS = 'computer-use'
  else delete result.CC_HAHA_SCOPE_EXCLUSIONS
  return result
}

export function recordComputerUseExclusion(stage: string) {
  console.log(`[scope-exclusion] ${JSON.stringify({ scope: 'computer-use', reason: 'user-requested', stage })}`)
}
