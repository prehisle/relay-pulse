"""bite-test 变异清单：cx-gpt 订阅态族的 wire 形态守卫。

守卫在 internal/config/template_cxgpt_wire_parity_test.go，被保护对象是 templates/ 下
五份 cx-gpt 模板「headers 与 body 两块逐字节相同、且确实是订阅态形态」这个关系。

破坏它不会有任何运行时报错：五份是手工同步的，只改一份的后果是五个模型悄悄跑在两套
客户端指纹上，跨模型的可用率比较从此不可比——而面板上看不出任何异常。

跑法（Go 项目必须传 --runner-flags ''，否则每条变异都会因未知 flag 假 RED）：

    python3 .claude/skills/bite-test/scripts/bite_test.py \
        --repo relay-pulse --runner 'go test' --runner-flags '' \
        --spec relay-pulse/scripts/mutations_cxgpt_wire_parity.py
"""

SELECTORS = ["./internal/config/", "-run", "TestCxGPT", "-count=1"]

SRC = "templates/cx-gpt-arith.json"
SOL = "templates/cx-gpt56-arith.json"
LUNA = "templates/cx-gpt56luna-arith.json"
TERRA = "templates/cx-gpt56terra-arith.json"
ASTRA = "templates/cx-gpt6astra-arith.json"
GUARD = "internal/config/template_cxgpt_wire_parity_test.go"

MUTATIONS = [
    # ① 最真实的失败形态：改了源的一个身份头，忘了同步另外四份。
    ("改源的 x-codex-beta-features 而不同步派生（改一份忘四份）",
     SRC,
     '"x-codex-beta-features": "remote_compaction_v2"',
     '"x-codex-beta-features": "remote_compaction_v3"',
     SELECTORS),

    # ② 升 CLI 版本只改了一份——UA 与 version 必须同族同步，这是最可能发生的一次漂移。
    ("只把 terra 的 version 抬到新版（族内客户端版本分裂）",
     TERRA,
     '"version": "0.154.0"',
     '"version": "0.155.0"',
     SELECTORS),

    # ③ body 的空白也上 wire——最隐蔽的一类漂移，unmarshal 后比结构完全看不见。
    ("改 luna 的 body 空白（wire 字节变了但 JSON 结构不变）",
     LUNA,
     '"tools": [],',
     '"tools": [ ],',
     SELECTORS),

    # ④ 平台态残留回流：把 openai-beta 加回某一份 = 有人 revert 回了旧形态。
    ("把 openai-beta 头加回 sol（平台态残留回流）",
     SOL,
     '"originator": "codex_exec",',
     '"openai-beta": "responses=experimental",\n\t\t"originator": "codex_exec",',
     SELECTORS),

    # ⑤ 非真空保障的核心：把稳定身份换成固定常量。这条**不会**破坏「五份逐字节相同」
    #    （若五份一起改），但它是模板注释里点名的系统性风险——所有通道共用一个合成账号，
    #    网关按 chatgpt-account-id 做限流时会把它们算成同一个账号的 N 倍流量。
    ("把源的 chatgpt-account-id 换成固定常量（所有通道共用一个账号身份）",
     SRC,
     '"chatgpt-account-id": "{{STABLE_UUID2}}"',
     '"chatgpt-account-id": "c0ffee00-dead-4bee-8fee-badc0ffee000"',
     SELECTORS),

    # ⑥ 非真空保障：把 headers 掏空。少了长度与标记断言，「都空 = 逐字节相同」会通过。
    ("把 astra 的 headers 掏成空对象（守卫若只比相等则会放过）",
     ASTRA,
     '"x-codex-window-id": "{{SESSION_UUID_V7}}:0"',
     '"x-codex-window-id": ""',
     SELECTORS),

    # ⑨ 会话级字段退回每请求随机（2026-09-21 那次改动被 revert 的形态）。零运行时症状：
    #    请求照发、探针照绿，只有上游重新看到 288 会话/天/行。
    ("把源的 session-id 改回 {{RAND_UUID_V7}}（会话级退回每请求随机）",
     SRC,
     '"session-id": "{{SESSION_UUID_V7}}"',
     '"session-id": "{{RAND_UUID_V7}}"',
     SELECTORS),

    # ⑩ 反向：把请求级的 x-client-request-id 也并进会话占位符。真 claude-cli 抓包里
    #    同一次调用的 6 次重试中唯一在变的就是它；冻住它可能撞上网关去重 = 探针假红。
    ("把源的 x-client-request-id 并进会话占位符（请求级退化成会话级）",
     SRC,
     '"x-client-request-id": "{{RAND_UUID_V7}}"',
     '"x-client-request-id": "{{SESSION_UUID_V7}}"',
     SELECTORS),

    # ⑦ 新增模板漏登记的对偶：从族名单里摘掉一项，目录里就多出一个不受保护的成员。
    ("从订阅态族名单里摘掉 terra（模拟新增模板漏登记）",
     GUARD,
     '\t"cx-gpt56terra-arith.json",\n',
     '',
     SELECTORS),

    # ⑧ 豁免必须是显式决定：把 gpt-5.4 从平台态名单摘掉，它就变成一个未归类模板。
    ("从平台态豁免名单里摘掉 gpt54（豁免退化成巧合）",
     GUARD,
     '\t"cx-gpt54-arith.json",\n',
     '',
     SELECTORS),
]
