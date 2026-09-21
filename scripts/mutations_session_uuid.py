"""bite-test 变异清单：会话级标识 {{SESSION_UUID_V7}} 的派生与注入。

被保护对象是 internal/identity/session.go 与 probe.go 注入点的一组不变量：窗口内稳定、
跨窗口轮转且时间戳同步前移、内嵌时间戳落在「刚过去」、派生键每一维都参与、相位逐行错开。

为什么值得守：这些不变量**全部只有上游看得见**。破坏任何一条，请求照发、探针照绿、
面板毫无异常——会话身份是我们唯一无法从自己这侧观测的输出。

跑法（Go 项目必须传 --runner-flags ''，否则每条变异都会因未知 flag 假 RED）：

    python3 .claude/skills/bite-test/scripts/bite_test.py \
        --repo relay-pulse --runner 'go test' --runner-flags '' \
        --spec relay-pulse/scripts/mutations_session_uuid.py
"""

SELECTORS = [
    "./internal/identity/",
    "./internal/monitor/",
    "-run",
    "TestDeriveSession|TestSessionKey|TestSessionWindow|TestInjectSessionUUID|TestInjectPlaceholders",
    "-count=1",
]

SESSION = "internal/identity/session.go"
PROBE = "internal/monitor/probe.go"

MUTATIONS = [
    # ① 退回「每次注入都重新生成」——本次改动想消除的形态，也是最可能被无意 revert 的一条。
    ("会话时间戳取 now 而不是窗口起点（退化成每请求一换）",
     SESSION,
     "return deriveUUIDv7(key, sessionWindowStart(key, now))",
     "return deriveUUIDv7(key, now)",
     SELECTORS),

    # ② 另一侧：窗口不轮转 = 站长提的「永久固定」，v7 时间戳会永久停在某一刻。
    ("窗口起点钉死成固定时刻（永久冻结会话）",
     SESSION,
     "return now.Add(-offset).Truncate(SessionWindow).Add(offset)",
     "return time.UnixMilli(1758000000000).Add(offset)",
     SELECTORS),

    # ③ 相位方向搞反：会话创建时刻落到未来 = turn 早于会话本身。
    ("相位加反方向（会话创建时刻跑到未来）",
     SESSION,
     "return now.Add(-offset).Truncate(SessionWindow).Add(offset)",
     "return now.Add(offset).Truncate(SessionWindow).Add(offset)",
     SELECTORS),

    # ④ 去掉相位错开：全站在整点一起轮转、前 48 位逐字节相同。
    ("去掉逐行相位错开（所有通道同一毫秒创建会话）",
     SESSION,
     "offset := time.Duration(binary.BigEndian.Uint64(digest[:8])%windowMillis) * time.Millisecond",
     "offset := time.Duration(0)",
     SELECTORS),

    # ⑤ 派生键漏一维：两条不同监测行共用一个会话身份。
    ("派生键漏掉 requestModel（换模型仍用同一会话）",
     SESSION,
     "key := sessionKey(provider, service, channel, rowKey, requestModel)",
     "key := sessionKey(provider, service, channel, rowKey)",
     SELECTORS),

    # ⑥ 长度前缀退化成分隔符拼接：("a|b","c") 与 ("a","b|c") 撞键。
    ("长度前缀编码退化成分隔符拼接（派生键可碰撞）",
     SESSION,
     """		b.WriteString(strconv.Itoa(len(f)))
		b.WriteByte(':')
		b.WriteString(f)""",
     """		b.WriteString(f)
		b.WriteByte('|')""",
     SELECTORS),

    # ⑦ version 位写错：产出的不再是 UUIDv7，解析 version 的网关一眼可辨。
    ("version 位写成 4（产出不再是 v7）",
     SESSION,
     "u[6] = (u[6] & 0x0f) | 0x70 // version 7",
     "u[6] = (u[6] & 0x0f) | 0x40 // version 7",
     SELECTORS),

    # ⑧ 注入点漏传一维——派生函数本身没问题，调用方喂错参数，identity 的测试看不见。
    ("注入点不传 requestModel（probe.go 侧漏维）",
     PROBE,
     "cfg.Provider, cfg.Service, cfg.Channel, cfg.ModelID, requestModel, time.Now())",
     'cfg.Provider, cfg.Service, cfg.Channel, cfg.ModelID, "", time.Now())',
     SELECTORS),

    # ⑨ inline 分叉被短路：占位 PSC（provider 空 + channel=__test__）也走窗口化派生，
    #    不同提交方在同一小时内的自助测试会共用 session-id 与 prompt_cache_key。
    ("inline 探测也走窗口化派生（不同提交方共用会话）",
     PROBE,
     '	if cfg.ModelID == "" {',
     "	if false {",
     SELECTORS),

    # ⑩ 反向：调度器路径也退化成一次性会话 = 整个特性没生效，而面板毫无异常。
    ("调度器路径也退化成一次性会话（特性整体失效）",
     PROBE,
     '	if cfg.ModelID == "" {',
     "	if true {",
     SELECTORS),
]
