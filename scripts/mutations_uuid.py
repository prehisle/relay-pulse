"""UUID/占位符那组守卫的 bite-test 变异清单。

跑法（脚本在 meta 仓，本文件只是数据）：

    python3 ../.claude/skills/bite-test/scripts/bite_test.py \
        --repo . --runner 'go test' --runner-flags '' \
        --spec scripts/mutations_uuid.py

⚠️ `--runner-flags ''` 不能省：默认值是 pytest 的 flag，go test 遇到会在每条变异上
都非零退出，脚本据此判"检出"，打印一片 RED 实则什么都没验到。
⚠️ selector 里的 `-count=1` 也不能省：Go 缓存测试结果，缓存命中会让变异后的代码
复用上一次的绿。

判据：每条都必须 RED。出现 STILL GREEN 就是守卫失效——先确认是测试覆盖缺口，
还是那段代码已经没用了，别急着改清单。
"""

MUTATIONS = [

    ('DeriveUUIDv4 不设 version 位',
     'internal/identity/userid.go',
     'u[6] = (u[6] & 0x0f) | 0x40 // version 4',
     '// version 位不设',
     ['./internal/identity/', '-run', 'TestDeriveUUIDv4_IsRFC4122Conformant', '-count=1']),

    ('DeriveUUIDv4 不设 variant 位',
     'internal/identity/userid.go',
     'u[8] = (u[8] & 0x3f) | 0x80 // variant RFC4122',
     '// variant 位不设',
     ['./internal/identity/', '-run', 'TestDeriveUUIDv4_IsRFC4122Conformant', '-count=1']),

    ('DeriveUUIDv4 忽略 salt（两个槽位会撞值）',
     'internal/identity/userid.go',
     'sha256.Sum256([]byte(hash + "|" + salt))',
     'sha256.Sum256([]byte(hash))',
     ['./internal/identity/', '-run', 'TestDeriveUUIDv4_StableAndSaltSeparated', '-count=1']),

    ('DeriveUUIDv4 退化成 DeriveUUID 的别名',
     'internal/identity/userid.go',
     'sum := sha256.Sum256([]byte(hash + "|" + salt))',
     'return DeriveUUID(hash, salt)\n\tsum := sha256.Sum256([]byte(hash))',
     ['./internal/identity/', '-run', 'TestDeriveUUIDv4', '-count=1']),

    ('newUUIDv7 改用 v4（version 位与内嵌时间戳全废）',
     'internal/monitor/probe.go',
     'var uuidV7Source = uuid.NewV7',
     'var uuidV7Source = uuid.NewRandom',
     ['./internal/monitor/', '-run', 'TestInjectRandUUIDv7_IsVersion7WithLiveTimestamp', '-count=1']),

    ('newUUIDv7 熵源失败时退回 v4（静默兜底）',
     'internal/monitor/probe.go',
     '\t\tlogger.Error("probe", "生成 UUIDv7 失败，占位符将注入空串",\n\t\t\t"error", err)\n\t\treturn ""',
     '\t\treturn uuid.New().String()',
     ['./internal/monitor/', '-run', 'TestNewUUIDv7_FailsLoudOnEntropyError', '-count=1']),

    ('UNIX_MS 退化成秒级时间戳',
     'internal/monitor/probe.go',
     'strconv.FormatInt(time.Now().UnixMilli(), 10)',
     'strconv.FormatInt(time.Now().Unix(), 10)',
     ['./internal/monitor/', '-run', 'TestInjectUnixMS_IsLiveJSONNumber', '-count=1']),

    ('删掉 {{RAND_UUID_V7}} 的替换项（占位符原样留在请求里）',
     'internal/monitor/probe.go',
     '"{{RAND_UUID_V7}}", newUUIDv7(),',
     '',
     ['./internal/monitor/', '-run', 'TestInjectPlaceholders', '-count=1']),

    ('DeriveUUID 被「顺手修好」格式位（会改变生产模板发出的值）',
     'internal/identity/userid.go',
     's := hash[:32]',
     's := hash[:32]\n\ts = s[:12] + "4" + s[13:16] + "8" + s[17:]',
     ['./internal/identity/', '-run', 'TestDeriveUUID_GoldenValueUnchanged', '-count=1']),

    ('两个 STABLE 占位符用同一个 salt',
     'internal/monitor/probe.go',
     'identity.DeriveUUIDv4(userIDHash, "stable_2")',
     'identity.DeriveUUIDv4(userIDHash, "stable_1")',
     ['./internal/monitor/', '-run', 'TestInjectPlaceholders_SameNameSameValue', '-count=1']),

    ('UNIX_MS 带引号（落成 JSON 字符串而非数字）',
     'internal/monitor/probe.go',
     'strconv.FormatInt(time.Now().UnixMilli(), 10),',
     '`"` + strconv.FormatInt(time.Now().UnixMilli(), 10) + `"`,',
     ['./internal/monitor/', '-run', 'TestInjectUnixMS_IsLiveJSONNumber', '-count=1']),

    ('{{RAND_UUID}} 去掉收尾 }}（变成 V7 版的真前缀）',
     'internal/monitor/probe.go',
     '"{{RAND_UUID}}", uuid.New().String(),',
     '"{{RAND_UUID", uuid.New().String(),',
     ['./internal/monitor/', '-run', 'TestInjectPlaceholders_NoPrefixShadowing', '-count=1']),

    ('STABLE_UUID 改用每次随机（每通道稳定性没了）',
     'internal/monitor/probe.go',
     '"{{STABLE_UUID}}", identity.DeriveUUIDv4(userIDHash, "stable_1"),',
     '"{{STABLE_UUID}}", uuid.New().String(),',
     ['./internal/monitor/', '-run', 'TestInjectStableUUID_PerChannelStableAndConformant', '-count=1']),

]
