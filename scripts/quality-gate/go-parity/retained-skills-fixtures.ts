export type RetainedSkillsCase = { name: string; kind: 'projection' | 'history'; source: string; tools?: 'write' }

// Original persisted inputs, independently shared by Source and Go.
export const retainedSkillsCases: RetainedSkillsCase[] = [
  {
    "name": "duplicate-and-empty-names",
    "kind": "projection",
    "source": "{\"type\":\"invoked_skills\",\"skills\":[{\"name\":\"one\",\"path\":\"/fixture/one\",\"content\":\"body\"},{\"name\":\"two\",\"path\":\"/fixture/two\",\"content\":\"b\"},{\"name\":\"one\",\"path\":\"/new\",\"content\":\"replacement\"},{\"name\":\"\",\"path\":\"/skip\",\"content\":\"skip\"}]}"
  },
  {
    "name": "invoked-empty",
    "kind": "projection",
    "source": "{\"type\":\"invoked_skills\",\"skills\":[]}"
  },
  {
    "name": "invoked-missing-fields",
    "kind": "projection",
    "source": "{\"type\":\"invoked_skills\",\"skills\":[{}]}"
  },
  {
    "name": "invoked-explicit-null-fields",
    "kind": "projection",
    "source": "{\"type\":\"invoked_skills\",\"skills\":[{\"name\":null,\"path\":null,\"content\":null}]}"
  },
  {
    "name": "invoked-nonstrings",
    "kind": "projection",
    "source": "{\"type\":\"invoked_skills\",\"skills\":[{\"name\":[\"a\",null],\"path\":1,\"content\":true},{\"name\":{},\"path\":[\"x\"],\"content\":{}},{\"name\":0,\"path\":false,\"content\":[]}]}"
  },
  {
    "name": "invoked-lone-utf16",
    "kind": "projection",
    "source": "{\"type\":\"invoked_skills\",\"skills\":[{\"name\":\"\\ud800\",\"path\":\"\\udc00\",\"content\":\"\\ud800\\udc00\"},{\"name\":\"\\udc00\",\"path\":\"\\ud800\",\"content\":\"\\ufffd\"}]}"
  },
  {
    "name": "invoked-null-elements",
    "kind": "projection",
    "source": "{\"type\":\"invoked_skills\",\"skills\":[null]}"
  },
  {
    "name": "invoked-null-skills",
    "kind": "projection",
    "source": "{\"type\":\"invoked_skills\",\"skills\":null}"
  },
  {
    "name": "invoked-string-skills",
    "kind": "projection",
    "source": "{\"type\":\"invoked_skills\",\"skills\":\"abc\"}"
  },
  {
    "name": "invoked-shadowed-toString",
    "kind": "projection",
    "source": "{\"type\":\"invoked_skills\",\"skills\":[{\"name\":{\"toString\":null},\"path\":\"p\",\"content\":\"c\"}]}"
  },
  {
    "name": "invoked-missing-skills",
    "kind": "projection",
    "source": "{\"type\":\"invoked_skills\"}"
  },
  {
    "name": "listing-empty",
    "kind": "projection",
    "source": "{\"type\":\"skill_listing\",\"content\":\"\",\"skillCount\":77,\"isInitial\":false}"
  },
  {
    "name": "listing-null",
    "kind": "projection",
    "source": "{\"type\":\"skill_listing\",\"content\":null,\"skillCount\":77,\"isInitial\":false}"
  },
  {
    "name": "listing-false",
    "kind": "projection",
    "source": "{\"type\":\"skill_listing\",\"content\":false,\"skillCount\":77,\"isInitial\":false}"
  },
  {
    "name": "listing-zero",
    "kind": "projection",
    "source": "{\"type\":\"skill_listing\",\"content\":0,\"skillCount\":77,\"isInitial\":false}"
  },
  {
    "name": "listing-whitespace",
    "kind": "projection",
    "source": "{\"type\":\"skill_listing\",\"content\":\" \\n\\t\",\"skillCount\":77,\"isInitial\":false}"
  },
  {
    "name": "listing-array",
    "kind": "projection",
    "source": "{\"type\":\"skill_listing\",\"content\":[\"a\",null,true],\"skillCount\":77,\"isInitial\":false}"
  },
  {
    "name": "listing-object",
    "kind": "projection",
    "source": "{\"type\":\"skill_listing\",\"content\":{\"future\":true},\"skillCount\":77,\"isInitial\":false}"
  },
  {
    "name": "listing-lone-utf16",
    "kind": "projection",
    "source": "{\"type\":\"skill_listing\",\"content\":\"high=\\ud800;low=\\udc00;replacement=\\ufffd\",\"skillCount\":77,\"isInitial\":false}"
  },
  {
    "name": "listing-shadowed-toString",
    "kind": "projection",
    "source": "{\"type\":\"skill_listing\",\"content\":{\"toString\":null},\"skillCount\":77,\"isInitial\":false}"
  },
  {
    "name": "listing-overflow",
    "kind": "projection",
    "source": "{\"type\":\"skill_listing\",\"content\":1e400}"
  },
  {
    "name": "attachments-before-users",
    "kind": "history",
    "source": "[{\"type\":\"attachment\",\"uuid\":\"persisted-invoked\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"invoked_skills\",\"skills\":[{\"name\":\"one\",\"path\":\"/fixture/one\",\"content\":\"body\"},{\"name\":\"two\",\"path\":\"/fixture/two\",\"content\":\"b\"},{\"name\":\"one\",\"path\":\"/new\",\"content\":\"replacement\"},{\"name\":\"\",\"path\":\"/skip\",\"content\":\"skip\"}]}},{\"type\":\"attachment\",\"uuid\":\"persisted-listing\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"skill_listing\",\"content\":\"listed\"}},{\"type\":\"user\",\"uuid\":\"u1\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"first\"}},{\"type\":\"user\",\"uuid\":\"u2\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"second\"}}]"
  },
  {
    "name": "attachments-between-users",
    "kind": "history",
    "source": "[{\"type\":\"user\",\"uuid\":\"u1\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"first\"}},{\"type\":\"attachment\",\"uuid\":\"persisted-invoked\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"invoked_skills\",\"skills\":[{\"name\":\"one\",\"path\":\"/fixture/one\",\"content\":\"body\"},{\"name\":\"two\",\"path\":\"/fixture/two\",\"content\":\"b\"},{\"name\":\"one\",\"path\":\"/new\",\"content\":\"replacement\"},{\"name\":\"\",\"path\":\"/skip\",\"content\":\"skip\"}]}},{\"type\":\"attachment\",\"uuid\":\"persisted-listing\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"skill_listing\",\"content\":\"listed\"}},{\"type\":\"user\",\"uuid\":\"u2\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"second\"}}]"
  },
  {
    "name": "attachments-after-users",
    "kind": "history",
    "source": "[{\"type\":\"user\",\"uuid\":\"u1\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"first\"}},{\"type\":\"user\",\"uuid\":\"u2\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"second\"}},{\"type\":\"attachment\",\"uuid\":\"persisted-invoked\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"invoked_skills\",\"skills\":[{\"name\":\"one\",\"path\":\"/fixture/one\",\"content\":\"body\"},{\"name\":\"two\",\"path\":\"/fixture/two\",\"content\":\"b\"},{\"name\":\"one\",\"path\":\"/new\",\"content\":\"replacement\"},{\"name\":\"\",\"path\":\"/skip\",\"content\":\"skip\"}]}},{\"type\":\"attachment\",\"uuid\":\"persisted-listing\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"skill_listing\",\"content\":\"listed\"}}]"
  },
  {
    "name": "assistant-stops-bubble",
    "kind": "history",
    "source": "[{\"type\":\"user\",\"uuid\":\"u1\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"first\"}},{\"type\":\"assistant\",\"uuid\":\"a\",\"timestamp\":\"2025-01-01T00:00:01Z\",\"message\":{\"id\":\"a\",\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"answer\"}]}},{\"type\":\"attachment\",\"uuid\":\"persisted-invoked\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"invoked_skills\",\"skills\":[{\"name\":\"one\",\"path\":\"/fixture/one\",\"content\":\"body\"},{\"name\":\"two\",\"path\":\"/fixture/two\",\"content\":\"b\"},{\"name\":\"one\",\"path\":\"/new\",\"content\":\"replacement\"},{\"name\":\"\",\"path\":\"/skip\",\"content\":\"skip\"}]}},{\"type\":\"attachment\",\"uuid\":\"persisted-listing\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"skill_listing\",\"content\":\"listed\"}},{\"type\":\"user\",\"uuid\":\"u2\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"second\"}}]"
  },
  {
    "name": "virtual-assistant-stops-before-filter",
    "kind": "history",
    "source": "[{\"type\":\"user\",\"uuid\":\"u1\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"first\"}},{\"type\":\"assistant\",\"uuid\":\"a\",\"timestamp\":\"2025-01-01T00:00:01Z\",\"message\":{\"id\":\"a\",\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"answer\"}]},\"isVirtual\":true},{\"type\":\"attachment\",\"uuid\":\"persisted-invoked\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"invoked_skills\",\"skills\":[{\"name\":\"one\",\"path\":\"/fixture/one\",\"content\":\"body\"},{\"name\":\"two\",\"path\":\"/fixture/two\",\"content\":\"b\"},{\"name\":\"one\",\"path\":\"/new\",\"content\":\"replacement\"},{\"name\":\"\",\"path\":\"/skip\",\"content\":\"skip\"}]}},{\"type\":\"attachment\",\"uuid\":\"persisted-listing\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"skill_listing\",\"content\":\"listed\"}},{\"type\":\"user\",\"uuid\":\"u2\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"second\"}}]"
  },
  {
    "name": "progress-does-not-stop",
    "kind": "history",
    "source": "[{\"type\":\"user\",\"uuid\":\"u1\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"first\"}},{\"type\":\"attachment\",\"uuid\":\"persisted-invoked\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"invoked_skills\",\"skills\":[{\"name\":\"one\",\"path\":\"/fixture/one\",\"content\":\"body\"},{\"name\":\"two\",\"path\":\"/fixture/two\",\"content\":\"b\"},{\"name\":\"one\",\"path\":\"/new\",\"content\":\"replacement\"},{\"name\":\"\",\"path\":\"/skip\",\"content\":\"skip\"}]}},{\"type\":\"progress\",\"uuid\":\"p\"},{\"type\":\"attachment\",\"uuid\":\"persisted-listing\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"skill_listing\",\"content\":\"listed\"}},{\"type\":\"user\",\"uuid\":\"u2\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"second\"}}]"
  },
  {
    "name": "tool-result-string",
    "kind": "history",
    "source": "[{\"type\":\"user\",\"uuid\":\"tr\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"t\",\"content\":\"result\"}]}},{\"type\":\"attachment\",\"uuid\":\"persisted-invoked\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"invoked_skills\",\"skills\":[{\"name\":\"one\",\"path\":\"/fixture/one\",\"content\":\"body\"},{\"name\":\"two\",\"path\":\"/fixture/two\",\"content\":\"b\"},{\"name\":\"one\",\"path\":\"/new\",\"content\":\"replacement\"},{\"name\":\"\",\"path\":\"/skip\",\"content\":\"skip\"}]}},{\"type\":\"attachment\",\"uuid\":\"persisted-listing\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"skill_listing\",\"content\":\"listed\"}},{\"type\":\"user\",\"uuid\":\"u2\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"second\"}}]"
  },
  {
    "name": "tool-result-array",
    "kind": "history",
    "source": "[{\"type\":\"user\",\"uuid\":\"tr\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"t\",\"content\":[{\"type\":\"text\",\"text\":\"result\"}]}]}},{\"type\":\"attachment\",\"uuid\":\"persisted-invoked\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"invoked_skills\",\"skills\":[{\"name\":\"one\",\"path\":\"/fixture/one\",\"content\":\"body\"},{\"name\":\"two\",\"path\":\"/fixture/two\",\"content\":\"b\"},{\"name\":\"one\",\"path\":\"/new\",\"content\":\"replacement\"},{\"name\":\"\",\"path\":\"/skip\",\"content\":\"skip\"}]}},{\"type\":\"attachment\",\"uuid\":\"persisted-listing\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"skill_listing\",\"content\":\"listed\"}},{\"type\":\"user\",\"uuid\":\"u2\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"second\"}}]"
  },
  {
    "name": "tool-result-later-block",
    "kind": "history",
    "source": "[{\"type\":\"user\",\"uuid\":\"tr\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"prefix\"},{\"type\":\"tool_result\",\"tool_use_id\":\"t\",\"content\":\"result\"}]}},{\"type\":\"attachment\",\"uuid\":\"persisted-invoked\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"invoked_skills\",\"skills\":[{\"name\":\"one\",\"path\":\"/fixture/one\",\"content\":\"body\"},{\"name\":\"two\",\"path\":\"/fixture/two\",\"content\":\"b\"},{\"name\":\"one\",\"path\":\"/new\",\"content\":\"replacement\"},{\"name\":\"\",\"path\":\"/skip\",\"content\":\"skip\"}]}},{\"type\":\"attachment\",\"uuid\":\"persisted-listing\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"skill_listing\",\"content\":\"listed\"}},{\"type\":\"user\",\"uuid\":\"u2\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"second\"}}]"
  },
  {
    "name": "empty-skill-no-allocation",
    "kind": "history",
    "source": "[{\"type\":\"user\",\"uuid\":\"u1\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"first\"}},{\"type\":\"attachment\",\"uuid\":\"persisted-empty\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"invoked_skills\",\"skills\":[]}},{\"type\":\"attachment\",\"uuid\":\"persisted-empty-list\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"skill_listing\",\"content\":\"\"}},{\"type\":\"user\",\"uuid\":\"u2\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"second\"}}]"
  },
  {
    "name": "malformed-skill-drop",
    "kind": "history",
    "source": "[{\"type\":\"user\",\"uuid\":\"u1\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"first\"}},{\"type\":\"attachment\",\"uuid\":\"persisted-null\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"invoked_skills\",\"skills\":[null]}},{\"type\":\"user\",\"uuid\":\"u2\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"second\"}}]"
  },
  {
    "name": "shadowed-value-drop",
    "kind": "history",
    "source": "[{\"type\":\"user\",\"uuid\":\"u1\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"first\"}},{\"type\":\"attachment\",\"uuid\":\"persisted-bad\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"invoked_skills\",\"skills\":[{\"name\":{\"toString\":null},\"path\":\"p\",\"content\":\"c\"}]}},{\"type\":\"user\",\"uuid\":\"u2\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"second\"}}]"
  },
  {
    "name": "retained-no-tools",
    "kind": "history",
    "source": "[{\"type\":\"attachment\",\"uuid\":\"persisted-invoked\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"invoked_skills\",\"skills\":[{\"name\":\"one\",\"path\":\"/fixture/one\",\"content\":\"body\"},{\"name\":\"two\",\"path\":\"/fixture/two\",\"content\":\"b\"},{\"name\":\"one\",\"path\":\"/new\",\"content\":\"replacement\"},{\"name\":\"\",\"path\":\"/skip\",\"content\":\"skip\"}]}},{\"type\":\"attachment\",\"uuid\":\"persisted-listing\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"skill_listing\",\"content\":\"listed\"}},{\"type\":\"user\",\"uuid\":\"u1\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"first\"}}]"
  },
  {
    "name": "retained-native-write-tool",
    "kind": "history",
    "source": "[{\"type\":\"attachment\",\"uuid\":\"persisted-invoked\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"invoked_skills\",\"skills\":[{\"name\":\"one\",\"path\":\"/fixture/one\",\"content\":\"body\"},{\"name\":\"two\",\"path\":\"/fixture/two\",\"content\":\"b\"},{\"name\":\"one\",\"path\":\"/new\",\"content\":\"replacement\"},{\"name\":\"\",\"path\":\"/skip\",\"content\":\"skip\"}]}},{\"type\":\"attachment\",\"uuid\":\"persisted-listing\",\"timestamp\":\"2000-01-01T00:00:00Z\",\"attachment\":{\"type\":\"skill_listing\",\"content\":\"listed\"}},{\"type\":\"user\",\"uuid\":\"u1\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"first\"}}]",
    "tools": "write"
  }
]
retainedSkillsCases.push(
  { name: 'invoked empty string collection', kind: 'projection', source: '{"type":"invoked_skills","skills":""}' },
  { name: 'invoked own zero length collection', kind: 'projection', source: '{"type":"invoked_skills","skills":{"length":-0}}' },
  { name: 'listing missing content', kind: 'projection', source: '{"type":"skill_listing"}' },
  { name: 'listing empty array is truthy', kind: 'projection', source: '{"type":"skill_listing","content":[]}' },
  { name: 'nested array conversion error', kind: 'projection', source: '{"type":"invoked_skills","skills":[{"name":["ok",{"toString":false}],"path":"p","content":"c"}]}' },
)
