# DingTalk 解绑原入口与实际配置清理

范围是 `/api/adapters/dingtalk/unbind` 及原非注册方法回退，不是钉钉整个平台完成。原 `src/server/api/adapters.ts` 的 POST unbind 通过同 AdapterService 移除 clientId/clientSecret/permissionCardTemplateId，清空 allowedUsers/pairedUsers，保留工作目录、endpoint、其他平台及未知字段。GET/PUT 的非注册回退仍是原配置接口，其他不支持的方法仍405。POST registration begin/poll 的实际生产者与钉钉聊天运行时继续开放。

Go 接线复用已有原格式原子 writer。Slack 解绑也使用这个共享私有函数，原字段和行为不变。没有新持久化 shape、全局 schema marker、模型请求或第三方平台调用。

新 Source fixture 导入并执行原完整 handleAdaptersApi 和原真实 AdapterService，全部文件写在一次性 HOME/config。14个 profile 比较原HTTP status/body、实际保存文档与0600，覆盖账号/card移除、跨平台保留、未知字段、工作目录、重复路径、GET/PUT/405，以及原缺失/null/bool/number/string/array旧平台字段。

| 检查 | 结果 |
| --- | --- |
| 288 新实际Source/HTTP回归先红 | FAILED，原200/405分支被Go整组501挡住；`/tmp/go-dingtalk-unbind-current-source-red-288.log` |
| 289 新14 profile + 原Slack及AdapterConfig回归 | PASS/race，Server4.363s、adapterconfig3.242s；`/tmp/go-dingtalk-unbind-current-source-green-289.log` |
| 290 初始完整Factory测试 | FAILED：测试自身没建私有config目录，非生产缺陷；原日志保留 |
| 291 真NewDesktopServer→HTTP→同实际存储 | PASS/race2.188s，新解绑及原management测试；前后读取一致、其他绑定保留、再次解绑原bytes一致；`/tmp/go-dingtalk-unbind-actual-desktop-factory-291.log` |
| 292 三包vet | exit0；`/tmp/go-dingtalk-unbind-affected-vet-292.log` |
| 293 完整Server/adapterconfig Source/race | PASS59.551/3.213s；`/tmp/go-dingtalk-unbind-affected-full-source-race-293.log` |

所有检查使用现有hermetic Go环境、固定依赖和实际Source Bun，无真实HOME/Keychain/平台账号或模型。当前运行的P285签名包不包含此变更；新包原生设置解绑操作未验收，不能把HTTP证据写成已经点过真实桌面按钮。完整verify及D18/D35仍开放。
