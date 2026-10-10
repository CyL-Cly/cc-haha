export type StartupEnvOperation = {
  kind: 'safe' | 'extraCA' | 'resetSettings' | 'setEnv' | 'write' | 'cacheFlagNull'
  raw?: string
  file?: 'user' | 'project' | 'local' | 'flag' | 'ccHaha'
}
export type StartupEnvProfile = {
  name: string
  global?: string
  user?: string
  project?: string
  local?: string
  flag?: string
  flagInline?: string
  flagSameAsUser?: boolean
  ccHaha?: string
  ccHahaBase64?: string
  environment?: string
  allowed?: string[]
  operations: StartupEnvOperation[]
}
const operations: StartupEnvOperation[] = [{ kind: 'safe' }, { kind: 'extraCA' }]
export const cliStartupEnvProfiles: StartupEnvProfile[] = [
  { name: 'absent-files', operations },
  { name: 'safe-source-order', global: '{"env":{"CC_HAHA_PROBE_ORDER":"global","ANTHROPIC_MODEL":"global","NODE_EXTRA_CA_CERTS":"global-ca"}}', user: '{"env":{"CC_HAHA_PROBE_ORDER":"user","ANTHROPIC_MODEL":"user"}}', project: '{"env":{"CC_HAHA_PROBE_ORDER":"project","ANTHROPIC_MODEL":"project","NODE_EXTRA_CA_CERTS":"project-ca"}}', local: '{"env":{"ANTHROPIC_MODEL":"local"}}', flag: '{"env":{"CC_HAHA_PROBE_ORDER":"flag","ANTHROPIC_MODEL":"flag"}}', ccHaha: '{"env":{"CC_HAHA_PROBE_ORDER":"haha","ANTHROPIC_MODEL":"haha"}}', operations },
  { name: 'disabled-user-global-ca-fallback', allowed: [], global: '{"env":{"NODE_EXTRA_CA_CERTS":"global-ca"}}', user: '{"env":{"NODE_EXTRA_CA_CERTS":"user-ca","CC_HAHA_PROBE_DISABLED":"user"}}', operations },
  { name: 'extra-ca-user-disabled-direct', allowed: [], global: '{"env":{"NODE_EXTRA_CA_CERTS":"global-ca"}}', user: '{"env":{"NODE_EXTRA_CA_CERTS":"user-ca"}}', operations: [{ kind: 'extraCA' }] },
  { name: 'extra-ca-project-excluded', project: '{"env":{"NODE_EXTRA_CA_CERTS":"project-ca"}}', operations: [{ kind: 'extraCA' }] },
  { name: 'extra-ca-truthy-boot-no-reads', environment: '{"NODE_EXTRA_CA_CERTS":"boot-ca"}', global: '{"env":{"NODE_EXTRA_CA_CERTS":"global-ca"}}', user: '{"env":{"NODE_EXTRA_CA_CERTS":"user-ca"}}', operations: [{ kind: 'extraCA' }, { kind: 'extraCA' }] },
  { name: 'extra-ca-empty-user-fallback', global: '{"env":{"NODE_EXTRA_CA_CERTS":"global-ca"}}', user: '{"env":{"NODE_EXTRA_CA_CERTS":""}}', operations: [{ kind: 'extraCA' }] },
  { name: 'extra-ca-typed-global', global: '{"env":{"NODE_EXTRA_CA_CERTS":true}}', operations: [{ kind: 'extraCA' }, { kind: 'extraCA' }] },
  { name: 'extra-ca-zero-global', global: '{"env":{"NODE_EXTRA_CA_CERTS":0}}', operations: [{ kind: 'extraCA' }] },
  { name: 'extra-ca-invalid-user-schema', global: '{"env":{"NODE_EXTRA_CA_CERTS":"global-ca"}}', user: '{"model":12,"env":{"NODE_EXTRA_CA_CERTS":"user-ca"}}', operations: [{ kind: 'extraCA' }] },
  { name: 'extra-ca-invalid-json-user', global: '{"env":{"NODE_EXTRA_CA_CERTS":"global-ca"}}', user: '{bad', operations: [{ kind: 'extraCA' }] },
  { name: 'flag-file-inline-env', flag: '{"env":{"CC_HAHA_PROBE_A":"file","CC_HAHA_PROBE_B":"file","ANTHROPIC_MODEL":"file"}}', flagInline: '{"env":{"CC_HAHA_PROBE_A":false,"CC_HAHA_PROBE_C":null,"ANTHROPIC_MODEL":"inline"}}', operations },
  { name: 'flag-inline-invalid-ignore', flag: '{"env":{"ANTHROPIC_MODEL":"flag"}}', flagInline: '{"model":false,"env":{"ANTHROPIC_MODEL":"invalid"}}', operations },
  { name: 'flag-user-same-file-dedup', flagSameAsUser: true, user: '{"env":{"CC_HAHA_PROBE_A":"user","ANTHROPIC_MODEL":"user"}}', flagInline: '{"env":{"ANTHROPIC_MODEL":"inline"}}', operations },
  { name: 'allowed-project-only-order', allowed: ['projectSettings'], user: '{"env":{"CC_HAHA_PROBE_A":"user"}}', project: '{"env":{"ANTHROPIC_MODEL":"project"}}', flag: '{"env":{"ANTHROPIC_MODEL":"flag"}}', operations },
  { name: 'allowed-order-duplicates', allowed: ['localSettings', 'projectSettings', 'localSettings', 'userSettings'], user: '{"env":{"ANTHROPIC_MODEL":"user"}}', project: '{"env":{"ANTHROPIC_MODEL":"project"}}', local: '{"env":{"ANTHROPIC_MODEL":"local"}}', operations },
  { name: 'raw-global-scalars-utf16', global: '{"env":{"CC_HAHA_PROBE_FALSE":false,"CC_HAHA_PROBE_NULL":null,"CC_HAHA_PROBE_NEGZERO":-0,"CC_HAHA_PROBE_HUGE":1e400,"CC_HAHA_PROBE_INTEGER":9007199254740993,"CC_HAHA_PROBE_HIGH":"\\ud800","CC_HAHA_PROBE_LOW":"\\udfff","CC_HAHA_PROBE_\\ud800":"high","CC_HAHA_PROBE_�":"replacement","CC_HAHA_PROBE_NUL\\u0000=END":"a\\u0000b=c"}}', operations },
  { name: 'user-env-explicit-string-coercion', user: '{"env":{"CC_HAHA_PROBE_A":false,"CC_HAHA_PROBE_B":null,"CC_HAHA_PROBE_C":1e400,"CC_HAHA_PROBE_D":[1,null,"\\ud800"]}}', operations },
  { name: 'user-env-string-error-invalid-file', user: '{"env":{"CC_HAHA_PROBE_A":{"toString":1}}}', global: '{"env":{"CC_HAHA_PROBE_B":"global"}}', operations },
  { name: 'host-managed-filter-case-prefix', environment: '{"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST":"yes","ANTHROPIC_MODEL":"boot"}', global: '{"env":{"anthropic_model":"global","VERTEX_REGION_CLAUDE_future":"blocked","CC_HAHA_LOCAL_ACCESS_TOKEN":"blocked","CC_HAHA_AGENT_TEAMS_ENABLED":"blocked","CC_HAHA_AGENT_TEAMS_DEFAULT":"blocked","CC_HAHA_PROBE_KEEP":"global"}}', user: '{"env":{"ANTHROPIC_BASE_URL":"https://attacker.invalid","ANTHROPIC_MODEL":"user","CC_HAHA_PROBE_KEEP":"user"}}', operations },
  { name: 'global-enables-host-filter-later', global: '{"env":{"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST":"1","ANTHROPIC_MODEL":"global","CC_HAHA_LOCAL_ACCESS_TOKEN":"global"}}', user: '{"env":{"ANTHROPIC_MODEL":"user","CC_HAHA_LOCAL_ACCESS_TOKEN":"user"}}', operations },
  { name: 'ssh-boot-protects-auth-only-exact-case', environment: '{"ANTHROPIC_UNIX_SOCKET":"boot-socket","ANTHROPIC_API_KEY":"boot-key"}', global: '{"env":{"ANTHROPIC_UNIX_SOCKET":"global-socket","ANTHROPIC_API_KEY":"global-key","anthropic_api_key":"lower-key","ANTHROPIC_MODEL":"global-model","CC_HAHA_PROBE_KEEP":"global"}}', user: '{"env":{"ANTHROPIC_AUTH_TOKEN":"user-token","ANTHROPIC_MODEL":"user-model"}}', operations },
  { name: 'global-socket-changes-later-filter', global: '{"env":{"ANTHROPIC_UNIX_SOCKET":"global-socket","ANTHROPIC_API_KEY":"global-key"}}', user: '{"env":{"ANTHROPIC_UNIX_SOCKET":"user-socket","ANTHROPIC_API_KEY":"user-key","ANTHROPIC_MODEL":"user-model"}}', operations },
  { name: 'ccd-snapshot-and-ca-empty-bypass', environment: '{"CLAUDE_CODE_ENTRYPOINT":"claude-desktop","OTEL_LOGS_EXPORTER":"boot","NODE_EXTRA_CA_CERTS":""}', global: '{"env":{"OTEL_LOGS_EXPORTER":"global","CC_HAHA_PROBE_NEW":"global"}}', user: '{"env":{"OTEL_LOGS_EXPORTER":"user","NODE_EXTRA_CA_CERTS":"user-ca","CC_HAHA_PROBE_NEW":"user"}}', operations },
  { name: 'ccd-snapshot-once-added-key-mutable', environment: '{"CLAUDE_CODE_ENTRYPOINT":"claude-desktop"}', user: '{"env":{"CC_HAHA_PROBE_NEW":"first"}}', operations: [{ kind: 'safe' }, { kind: 'write', file: 'user', raw: '{"env":{"CC_HAHA_PROBE_NEW":"second"}}' }, { kind: 'resetSettings' }, { kind: 'safe' }] },
  { name: 'nonccd-snapshot-stays-null', user: '{"env":{"CC_HAHA_PROBE_NEW":"first"}}', operations: [{ kind: 'safe' }, { kind: 'setEnv', raw: '{"CLAUDE_CODE_ENTRYPOINT":"claude-desktop"}' }, { kind: 'write', file: 'user', raw: '{"env":{"CC_HAHA_PROBE_NEW":"second"}}' }, { kind: 'resetSettings' }, { kind: 'safe' }] },
  { name: 'settings-cache-stale-then-reset', user: '{"env":{"CC_HAHA_PROBE_NEW":"first","ANTHROPIC_MODEL":"first"}}', operations: [{ kind: 'safe' }, { kind: 'write', file: 'user', raw: '{"env":{"CC_HAHA_PROBE_NEW":"second","ANTHROPIC_MODEL":"second"}}' }, { kind: 'safe' }, { kind: 'resetSettings' }, { kind: 'safe' }] },
  { name: 'safe-uppercase-preserves-original-key', project: '{"env":{"anthropic_model":"lower","baſh_default_timeout_ms":"long-s","claude_code_disabLE_terminal_title":"mixed","node_extra_ca_certs":"unsafe","CC_HAHA_PROBE_UNKNOWN":"unsafe"}}', operations },
  { name: 'cc-haha-invalid-json-empty', ccHaha: '{bad', user: '{"env":{"CC_HAHA_PROBE_KEEP":"user"}}', operations },
  { name: 'cc-haha-deepseek-legacy', ccHaha: '{"env":{"ANTHROPIC_BASE_URL":"https://api.deepseek.com","CC_HAHA_SEND_DISABLED_THINKING":"1","ANTHROPIC_MODEL":"deepseek-chat","CC_HAHA_PROBE_KEEP":"haha"}}', operations },
  { name: 'global-env-null', global: '{"env":null}', operations },
  { name: 'global-env-false', global: '{"env":false}', operations },
  { name: 'unknown-settings-ordered-deep-merge', user: '{"env":{"ANTHROPIC_MODEL":"user"},"future":{"a":1,"items":[1,1,{"x":1}]},"\\ud800":"high","�":"replacement"}', project: '{"future":{"b":2,"items":[1,2,{"x":1}]},"\\udfff":"low"}', flagInline: '{"future":{"items":[2,3]},"effortLevel":123}', operations },
  { name: 'catch-undefined-retains-prior-effort', user: '{"effortLevel":"high","future":[-0,1e400,9007199254740993]}', project: '{"effortLevel":false,"future":[0,1e400,9007199254740992]}', operations },
  { name: 'extra-ca-utf16-keys-log-and-path', global: '{"env":{"2":"two","1":"one","CC_HAHA_PROBE_\\ud800":"high","NODE_EXTRA_CA_CERTS":"ca-\\udfff-\\u0000"}}', operations: [{ kind: 'extraCA' }] },
  { name: 'extra-ca-empty-existing-own', environment: '{"NODE_EXTRA_CA_CERTS":""}', user: '{"env":{"NODE_EXTRA_CA_CERTS":"<HOME>/private-ca.pem"}}', operations: [{ kind: 'extraCA' }] },
  { name: 'cc-haha-model-only-deepseek', ccHaha: '{"env":{"CC_HAHA_SEND_DISABLED_THINKING":"yes","ANTHROPIC_MODEL":"DEEPSEEK_chat","CC_HAHA_PROBE_KEEP":"haha"}}', operations },
  { name: 'cc-haha-no-deepseek-remap', ccHaha: '{"env":{"CC_HAHA_SEND_DISABLED_THINKING":"1","ANTHROPIC_MODEL":"fixture-model","CC_HAHA_PROBE_KEEP":"haha"}}', operations },
  { name: 'trusted-empty-and-project-case-sensitive', user: '', flag: '{}', local: '{"env":{"ANTHROPIC_MODEL":"local"}}', project: '{"env":{"ANTHROPIC_MODEL":"project","anthropic_MODEL":"separate"}}', operations },
  { name: 'cc-haha-original-invalid-utf8-replacement', ccHahaBase64: Buffer.concat([Buffer.from('{"env":{"CC_HAHA_PROBE_INVALID":"'), Buffer.from([0x80]), Buffer.from('"}}')]).toString('base64'), operations },
  { name: 'cc-haha-null-env', ccHaha: '{"env":null}', operations },
  { name: 'eligibility-skip-cached-before-later-change', operations: [{ kind: 'safe' }, { kind: 'setEnv', raw: '{"CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH":"0"}' }, { kind: 'safe' }] },
]

// Original input profiles; expected observations are captured from Source.
cliStartupEnvProfiles.push(...[
  {
    "name": "empty-user",
    "global": "{\"env\":{\"CC_HAHA_PROBE_BEFORE\":\"kept\",\"NODE_EXTRA_CA_CERTS\":\"global-ca\"}}",
    "user": "{\"hooks\":{},\"env\":{\"ANTHROPIC_MODEL\":\"original-model\",\"NODE_EXTRA_CA_CERTS\":\"source-ca\"}}",
    "operations": [
      {
        "kind": "safe"
      },
      {
        "kind": "extraCA"
      }
    ]
  },
  {
    "name": "empty-flag",
    "global": "{\"env\":{\"CC_HAHA_PROBE_BEFORE\":\"kept\",\"NODE_EXTRA_CA_CERTS\":\"global-ca\"}}",
    "flag": "{\"hooks\":{},\"env\":{\"ANTHROPIC_MODEL\":\"original-model\",\"NODE_EXTRA_CA_CERTS\":\"source-ca\"}}",
    "operations": [
      {
        "kind": "safe"
      },
      {
        "kind": "extraCA"
      }
    ]
  },
  {
    "name": "empty-flagInline",
    "global": "{\"env\":{\"CC_HAHA_PROBE_BEFORE\":\"kept\",\"NODE_EXTRA_CA_CERTS\":\"global-ca\"}}",
    "flagInline": "{\"hooks\":{},\"env\":{\"ANTHROPIC_MODEL\":\"original-model\",\"NODE_EXTRA_CA_CERTS\":\"source-ca\"}}",
    "operations": [
      {
        "kind": "safe"
      },
      {
        "kind": "extraCA"
      }
    ]
  },
  {
    "name": "empty-project",
    "global": "{\"env\":{\"CC_HAHA_PROBE_BEFORE\":\"kept\",\"NODE_EXTRA_CA_CERTS\":\"global-ca\"}}",
    "project": "{\"hooks\":{},\"env\":{\"ANTHROPIC_MODEL\":\"original-model\",\"NODE_EXTRA_CA_CERTS\":\"source-ca\"}}",
    "operations": [
      {
        "kind": "safe"
      },
      {
        "kind": "extraCA"
      }
    ]
  },
  {
    "name": "empty-user-ca-direct",
    "global": "{\"env\":{\"CC_HAHA_PROBE_BEFORE\":\"kept\",\"NODE_EXTRA_CA_CERTS\":\"global-ca\"}}",
    "user": "{\"hooks\":{},\"env\":{\"NODE_EXTRA_CA_CERTS\":\"user-ca\"}}",
    "allowed": [],
    "operations": [
      {
        "kind": "extraCA"
      }
    ]
  },
  {
    "name": "empty-event-arrays-and-booleans",
    "user": "{\"hooks\":{\"StopFailure\":[],\"Stop\":[]},\"disableAllHooks\":false,\"allowManagedHooksOnly\":true,\"env\":{\"NODE_EXTRA_CA_CERTS\":\"user-ca\"}}",
    "operations": [
      {
        "kind": "safe"
      },
      {
        "kind": "extraCA"
      }
    ]
  },
  {
    "name": "empty-inline-with-cached-null",
    "flagInline": "{\"hooks\":{},\"env\":{\"ANTHROPIC_MODEL\":\"inline-model\"}}",
    "operations": [
      {
        "kind": "cacheFlagNull"
      },
      {
        "kind": "safe"
      },
      {
        "kind": "extraCA"
      }
    ]
  }
])
