"""bite-test 变异清单：subhdr 影子族的逐字节派生守卫。

守卫在 internal/config/template_subhdr_parity_test.go，被保护对象是 templates/ 下
subhdr 族四份 JSON「除四个模型串外逐字节相同」这个关系——它是跨模型对照实验的有效性前提，
破坏它不会有任何运行时报错，只会让对照数据悄悄失去可比性。

跑法（Go 项目必须传 --runner-flags ''，否则每条变异都会因未知 flag 假 RED）：

    python3 .claude/skills/bite-test/scripts/bite_test.py \
        --repo relay-pulse --runner 'go test' --runner-flags '' \
        --spec relay-pulse/scripts/mutations_subhdr_parity.py
"""

SELECTORS = ["./internal/config/", "-run", "TestSubhdrShadow", "-count=1"]

SRC = "templates/cx-gpt6astra-subhdr-arith.json"
SOL = "templates/cx-gpt56-subhdr-arith.json"
LUNA = "templates/cx-gpt56luna-subhdr-arith.json"
TERRA = "templates/cx-gpt56terra-subhdr-arith.json"
GUARD = "internal/config/template_subhdr_parity_test.go"

MUTATIONS = [
    # ① 最真实的失败形态：改了源，忘了同步三份派生。
    ("改源模板的 header 而不同步派生（改一份忘三份）",
     SRC,
     '"x-codex-beta-features": "remote_compaction_v2"',
     '"x-codex-beta-features": "remote_compaction_v3"',
     SELECTORS),

    # ② 给单个模型单独调 probe 档位——gpt-6-astra 历史上真被单独放宽过 5s/10s→8s/15s，
    #    对照实验里这么做会让两边延迟口径不一致、差异算不清。
    ("给 terra 单独放宽 slow_latency（破坏对照的超时口径一致）",
     TERRA,
     '"slow_latency": "8s"',
     '"slow_latency": "10s"',
     SELECTORS),

    # ③ 只给一份刷 CLI 版本号，族内形态分裂。
    ("只把 sol 的 version 抬到新版（族内客户端版本分裂）",
     SOL,
     '"version": "0.154.0"',
     '"version": "0.155.0"',
     SELECTORS),

    # ④ body 的空白也上 wire——这是最隐蔽的一类漂移，unmarshal 后比结构根本看不见。
    ("改 luna 的 body 空白（wire 字节变了但 JSON 结构不变）",
     LUNA,
     '"tools": [],',
     '"tools": [ ],',
     SELECTORS),

    # ⑤ 删掉派生里的一个 header，行数与内容同时变。
    ("删掉 sol 的 accept 头（行数与内容同时偏离源）",
     SOL,
     '"accept": "text/event-stream",\n\t\t"Authorization"',
     '"Authorization"',
     SELECTORS),

    # ⑥ 非真空保障：把派生的 model 写回源的值。少了这条断言，
    #    「把派生整个复制成源的副本」也能骗过逐行比对。
    ("把 luna 的 model 写成与源相同（会与源撞 DB 业务键）",
     LUNA,
     '"model": "GPT-5.6-Luna-sub"',
     '"model": "GPT-6-Astra-sub"',
     SELECTORS),

    # ⑦ 顶层字段缩进从 tab 漂成空格 → 差异白名单定位失效，
    #    那时守卫会把真实差异当成"允许差异"放过去，必须 fail 而不是静默降级。
    ("terra 顶层 model 的缩进从 tab 改成空格（白名单定位失效）",
     TERRA,
     '\t"model": "GPT-5.6-Terra-sub"',
     '    "model": "GPT-5.6-Terra-sub"',
     SELECTORS),

    # ⑧ 新增派生却漏登记的对偶：从名单里摘掉一项，目录里就多出一个不受保护的族成员。
    ("从族名单里摘掉 terra（模拟新增派生漏登记）",
     GUARD,
     '\t"cx-gpt56terra-subhdr-arith.json",\n}',
     '}',
     SELECTORS),
]
