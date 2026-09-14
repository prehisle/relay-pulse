#!/usr/bin/env python3
"""bite-test 的 Go 版 runner —— 证明 UUID/占位符那组守卫非真空。

跑法：python3 scripts/bite_test_uuid_placeholders.py（需要 go 在 PATH 上）。
判据：每条都必须 RED。出现 STILL GREEN 就是守卫失效，别急着改清单——
先确认是测试覆盖缺口还是那段代码已经没用了。

meta 仓的 .claude/skills/bite-test/scripts/bite_test.py 把 pytest 的
`-q --no-header -p no:cacheprovider` 硬编码进了 runner 调用，go test 不认这些
flag，会直接报错退出——那样每条变异都「失败」，看上去全是 RED，实际什么都没验到。
本脚本沿用它的硬规则（只在 /tmp 副本上变异、工作区一个字节不动），只把 runner
换成 `go test <pkg> -run <regex>`。
"""
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

SRC = Path(__file__).resolve().parent.parent

# (说明, 相对路径, 原串, 替换串, 包, 该变红的测试正则)
MUTATIONS = [
    ("DeriveUUIDv4 不设 version 位",
     "internal/identity/userid.go",
     "u[6] = (u[6] & 0x0f) | 0x40 // version 4",
     "// version 位不设",
     "./internal/identity/", "TestDeriveUUIDv4_IsRFC4122Conformant"),

    ("DeriveUUIDv4 不设 variant 位",
     "internal/identity/userid.go",
     "u[8] = (u[8] & 0x3f) | 0x80 // variant RFC4122",
     "// variant 位不设",
     "./internal/identity/", "TestDeriveUUIDv4_IsRFC4122Conformant"),

    ("DeriveUUIDv4 忽略 salt（两个槽位会撞值）",
     "internal/identity/userid.go",
     'sha256.Sum256([]byte(hash + "|" + salt))',
     "sha256.Sum256([]byte(hash))",
     "./internal/identity/", "TestDeriveUUIDv4_StableAndSaltSeparated"),

    ("DeriveUUIDv4 退化成 DeriveUUID 的别名",
     "internal/identity/userid.go",
     'sum := sha256.Sum256([]byte(hash + "|" + salt))',
     "return DeriveUUID(hash, salt)\n\tsum := sha256.Sum256([]byte(hash))",
     "./internal/identity/", "TestDeriveUUIDv4"),

    ("newUUIDv7 改用 v4（version 位与内嵌时间戳全废）",
     "internal/monitor/probe.go",
     "var uuidV7Source = uuid.NewV7",
     "var uuidV7Source = uuid.NewRandom",
     "./internal/monitor/", "TestInjectRandUUIDv7_IsVersion7WithLiveTimestamp"),

    # 本轮 codex 复审的核心一条：静默降级会让熵源故障彻底不可见（探针照常绿）
    ("newUUIDv7 熵源失败时退回 v4（静默兜底）",
     "internal/monitor/probe.go",
     '\t\tlogger.Error("probe", "生成 UUIDv7 失败，占位符将注入空串",\n\t\t\t"error", err)\n\t\treturn ""',
     '\t\treturn uuid.New().String()',
     "./internal/monitor/", "TestNewUUIDv7_FailsLoudOnEntropyError"),

    ("UNIX_MS 退化成秒级时间戳",
     "internal/monitor/probe.go",
     "strconv.FormatInt(time.Now().UnixMilli(), 10)",
     "strconv.FormatInt(time.Now().Unix(), 10)",
     "./internal/monitor/", "TestInjectUnixMS_IsLiveJSONNumber"),

    ("删掉 {{RAND_UUID_V7}} 的替换项（占位符原样留在请求里）",
     "internal/monitor/probe.go",
     '"{{RAND_UUID_V7}}", newUUIDv7(),',
     "",
     "./internal/monitor/", "TestInjectPlaceholders"),

    ("DeriveUUID 被「顺手修好」格式位（会改变生产模板发出的值）",
     "internal/identity/userid.go",
     "s := hash[:32]",
     's := hash[:32]\n\ts = s[:12] + "4" + s[13:16] + "8" + s[17:]',
     "./internal/identity/", "TestDeriveUUID_GoldenValueUnchanged"),

    ("两个 STABLE 占位符用同一个 salt",
     "internal/monitor/probe.go",
     'identity.DeriveUUIDv4(userIDHash, "stable_2")',
     'identity.DeriveUUIDv4(userIDHash, "stable_1")',
     "./internal/monitor/", "TestInjectPlaceholders_SameNameSameValue"),

    ("UNIX_MS 带引号（落成 JSON 字符串而非数字）",
     "internal/monitor/probe.go",
     'strconv.FormatInt(time.Now().UnixMilli(), 10),',
     '`"` + strconv.FormatInt(time.Now().UnixMilli(), 10) + `"`,',
     "./internal/monitor/", "TestInjectUnixMS_IsLiveJSONNumber"),

    # 这条专门证明「无前缀吞并」那个测试不是真空的：占位符靠「以 }} 收尾」
    # 才在分叉字符上错开，去掉收尾它立刻变成 {{RAND_UUID_V7}} 的真前缀，
    # strings.NewReplacer 按参数顺序比较、短名在前，就会把后者吃成半截。
    ("{{RAND_UUID}} 去掉收尾 }}（变成 V7 版的真前缀）",
     "internal/monitor/probe.go",
     '"{{RAND_UUID}}", uuid.New().String(),',
     '"{{RAND_UUID", uuid.New().String(),',
     "./internal/monitor/", "TestInjectPlaceholders_NoPrefixShadowing"),

    ("STABLE_UUID 改用每次随机（每通道稳定性没了）",
     "internal/monitor/probe.go",
     '"{{STABLE_UUID}}", identity.DeriveUUIDv4(userIDHash, "stable_1"),',
     '"{{STABLE_UUID}}", uuid.New().String(),',
     "./internal/monitor/", "TestInjectStableUUID_PerChannelStableAndConformant"),
]

work = Path(tempfile.mkdtemp(prefix="bite-go-"))
copy = work / "relay-pulse"
shutil.copytree(SRC, copy, ignore=shutil.ignore_patterns(
    ".git", "node_modules", "dist", "*.db", "*.db-wal", "*.db-shm"))

undetected = 0
for label, rel, old, new, pkg, testre in MUTATIONS:
    target = copy / rel
    original = target.read_text(encoding="utf-8")
    if old not in original:
        print(f"{'SKIP(anchor missing)':24} {label}")
        undetected += 1
        continue
    target.write_text(original.replace(old, new, 1), encoding="utf-8")
    try:
        r = subprocess.run(["go", "test", pkg, "-run", testre, "-count=1"],
                           cwd=copy, capture_output=True, text=True, timeout=300)
    finally:
        target.write_text(original, encoding="utf-8")
    detected = r.returncode != 0
    if not detected:
        undetected += 1
    tag = "RED (good)" if detected else "STILL GREEN <-- vacuous!"
    print(f"{tag:24} {label}")
    if not detected:
        print(f"{'':24}   ^ 变异未被检出，输出: {r.stdout.strip()[:160]}")

total = len(MUTATIONS)
print(f"\n{total - undetected}/{total} 条变异被检出")
shutil.rmtree(work, ignore_errors=True)
sys.exit(1 if undetected else 0)
