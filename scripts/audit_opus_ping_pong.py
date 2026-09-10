#!/usr/bin/env python3
"""一次性只读审计：给 cc-opus-ping 通道补 success_contains 之前，量清楚真实响应正文。

对每条使用 cc-opus-ping 模板的生产通道各打两次真实请求：
  old = 当前模板（system 第三段是 CWD/Date，无 pong 指令）
  new = cc-opus-ping-20260630（system 第三段是 "Only reply pong."）
按 monitor.ExtractTextFromSSE 的口径聚合正文，报告是否命中 success_contains="pong"。

只读：不写库、不改配置、不落盘凭据；请求强度等同一次常规探测。
"""

import concurrent.futures
import glob
import json
import os
import re
import ssl
import sys
import urllib.error
import urllib.request
import uuid

MONITORS_DIR = "/opt/relaypulse_pg/config/monitors.d"
TEMPLATES_DIR = "/opt/relaypulse_pg/config/templates"
TARGET_TEMPLATE = "cc-opus-ping"
CANDIDATE_TEMPLATE = "cc-opus-ping-20260630"
TIMEOUT = 20


def parse_monitors(path):
    """解析 monitors.d 的一个 YAML 文件，返回 monitors 数组（每项是扁平 dict）。

    这些文件由 MonitorStore 生成，形状固定（两级缩进、无嵌套、无多行标量），
    故用行解析而不引入 pyyaml 依赖。
    """
    items, cur, in_monitors = [], None, False
    for line in open(path, encoding="utf-8"):
        if line.startswith("monitors:"):
            in_monitors = True
            continue
        if not in_monitors:
            continue
        m = re.match(r"^\s*-\s+(\w+):\s*(.*)$", line)
        if m:
            if cur is not None:
                items.append(cur)
            cur = {m.group(1): m.group(2).strip()}
            continue
        m = re.match(r"^\s+(\w+):\s*(.*)$", line)
        if m and cur is not None:
            cur[m.group(1)] = m.group(2).strip()
    if cur is not None:
        items.append(cur)
    return items


def unquote(v):
    v = (v or "").strip()
    if len(v) >= 2 and v[0] == v[-1] and v[0] in "\"'":
        return v[1:-1]
    return v


def collect_targets():
    """收集使用 TARGET_TEMPLATE 的行，子行缺失字段按 parent 继承（与 loader 同口径）。"""
    targets = []
    for path in sorted(glob.glob(os.path.join(MONITORS_DIR, "*.yaml"))):
        monitors = parse_monitors(path)
        root = next((m for m in monitors if not unquote(m.get("parent"))), None)
        for m in monitors:
            if unquote(m.get("template")) != TARGET_TEMPLATE:
                continue
            base = root if unquote(m.get("parent")) else m
            targets.append(
                {
                    "file": os.path.basename(path),
                    "psc": "{}/{}/{}".format(
                        unquote(base.get("provider")),
                        unquote(base.get("service")),
                        unquote(base.get("channel")),
                    ),
                    "base_url": unquote(m.get("base_url")) or unquote(base.get("base_url")),
                    "api_key": unquote(m.get("api_key")) or unquote(base.get("api_key")),
                    "proxy": unquote(m.get("proxy")) or unquote(base.get("proxy")),
                }
            )
    return targets


def render(template_name, base_url, api_key):
    tmpl = json.load(open(os.path.join(TEMPLATES_DIR, template_name + ".json"), encoding="utf-8"))
    subs = {
        "{{BASE_URL}}": base_url.rstrip("/"),
        "{{API_KEY}}": api_key,
        "{{RAND_UUID}}": str(uuid.uuid4()),
        "{{RAND_UUID2}}": str(uuid.uuid4()),
    }

    def fill(s):
        for k, v in subs.items():
            s = s.replace(k, v)
        return s

    url = fill(tmpl["url"])
    headers = {k: fill(v) for k, v in tmpl["headers"].items()}
    # body 按模板原始字节发送（与 InjectVariables 一致：只做字符串替换，不 re-marshal）
    body = fill(json.dumps(tmpl["body"], ensure_ascii=False, separators=(",", ":")))
    return url, headers, body.encode("utf-8"), (tmpl.get("response") or {}).get("success_contains", "")


def extract_text(raw):
    """复刻 monitor.AggregateResponseText：SSE 抽 text_delta，非 SSE 回退原文。"""
    is_sse = (b"event:" in raw and b"data:" in raw) or raw.startswith(b"data:") or b"\ndata:" in raw
    if not is_sse:
        return raw.decode("utf-8", "replace")
    out = []
    for line in raw.decode("utf-8", "replace").splitlines():
        line = line.strip()
        if not line.startswith("data:"):
            continue
        payload = line[len("data:") :].strip()
        if not payload or payload == "[DONE]":
            continue
        try:
            obj = json.loads(payload)
        except json.JSONDecodeError:
            out.append(payload)
            continue
        delta = obj.get("delta")
        if isinstance(delta, dict) and isinstance(delta.get("text"), str):
            out.append(delta["text"])
        elif isinstance(delta, str):
            out.append(delta)
    text = "".join(out)
    return text if text else raw.decode("utf-8", "replace")


def probe(target, template_name):
    url, headers, body, sc = render(template_name, target["base_url"], target["api_key"])
    req = urllib.request.Request(url, data=body, headers=headers, method="POST")
    ctx = ssl.create_default_context()
    try:
        with urllib.request.urlopen(req, timeout=TIMEOUT, context=ctx) as resp:
            raw, code = resp.read(), resp.status
    except urllib.error.HTTPError as e:
        raw, code = e.read(), e.code
    except Exception as e:  # noqa: BLE001 — 审计脚本，网络层任何失败都如实记录
        return {"code": 0, "err": type(e).__name__ + ": " + str(e)[:120], "text": "", "sc": sc}
    return {"code": code, "err": "", "text": extract_text(raw), "sc": sc}


def main():
    targets = collect_targets()
    print("目标通道 {} 条\n".format(len(targets)), flush=True)

    def run(t):
        if t["proxy"]:
            return t, {"skip": "配置了代理 {}，本脚本不走代理，需单独验证".format(t["proxy"].split("://")[0])}, None
        return t, probe(t, TARGET_TEMPLATE), probe(t, CANDIDATE_TEMPLATE)

    rows = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
        for t, old, new in pool.map(run, targets):
            rows.append((t, old, new))

    hit = miss = 0
    for t, old, new in sorted(rows, key=lambda r: r[0]["psc"]):
        if new is None:
            print("{:34s} SKIP {}".format(t["psc"], old["skip"]))
            continue
        ntext = (new["text"] or "").strip()
        ok = "pong" in ntext  # 与 evaluateStatus 一致：strings.Contains，大小写敏感
        hit, miss = (hit + 1, miss) if ok else (hit, miss + 1)
        print(
            "{:34s} old[{} len={}] new[{} len={}] pong={} nocase={}".format(
                t["psc"],
                old["code"] or old["err"][:20],
                len(old["text"].strip()),
                new["code"] or new["err"][:20],
                len(ntext),
                "Y" if ok else "N",
                "Y" if "pong" in ntext.lower() else "N",
            )
        )
        print("      old正文: {!r}".format(old["text"].strip()[:110]))
        print("      new正文: {!r}".format(ntext[:110]))
    print("\n新模板命中 pong: {} 条；未命中: {} 条".format(hit, miss))
    return 0 if miss == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
