#!/usr/bin/env python3
# Railway 系統負擔監控：各服務(api/worker/web/Redis) 的 CPU / 記憶體 + 專案網路流出量，
# 超門檻（記憶體/CPU 持續偏高，或網路流出接近免費額度）就發 Telegram；健康則靜默。stdlib only。
# ⚠️ Railway 走 Cloudflare，會擋 python 預設 User-Agent → 必須帶自訂 UA（下方 UA 常數）。
#
# 環境變數（GitHub Actions 用 Secrets/env 帶入）：
#   RAILWAY_API_TOKEN(機密), RAILWAY_PROJECT_ID, RAILWAY_ENVIRONMENT_ID,
#   TELEGRAM_BOT_TOKEN(機密), TELEGRAM_CHAT_ID
# 門檻(非機密, workflow env)：
#   MEM_ALERT_GB(單一服務記憶體警示,預設0.5)  CPU_ALERT_VCPU(預設0.6)  NET_TX_ALERT_GB(預設400)  FORCE_NOTIFY
import os, sys, json, datetime, urllib.request, urllib.parse, urllib.error

try:
    sys.stdout.reconfigure(encoding="utf-8")
    sys.stderr.reconfigure(encoding="utf-8")
except Exception:
    pass


def env(k, d=None):
    v = os.environ.get(k, "")
    return v if v != "" else d


TOK = env("RAILWAY_API_TOKEN")
PROJECT = env("RAILWAY_PROJECT_ID")
ENVID = env("RAILWAY_ENVIRONMENT_ID")
TG_TOKEN = env("TELEGRAM_BOT_TOKEN")
TG_CHAT = env("TELEGRAM_CHAT_ID")
MEM_ALERT = float(env("MEM_ALERT_GB", "0.5"))
CPU_ALERT = float(env("CPU_ALERT_VCPU", "0.6"))
NET_TX_ALERT = float(env("NET_TX_ALERT_GB", "400"))
FORCE = str(env("FORCE_NOTIFY", "false")).lower() in ("1", "true", "yes")
UA = "Mozilla/5.0 (compatible; dor-monitor/1.0)"

if not TOK or not PROJECT or not ENVID:
    print("missing RAILWAY_API_TOKEN / RAILWAY_PROJECT_ID / RAILWAY_ENVIRONMENT_ID", file=sys.stderr)
    sys.exit(1)


def gql(q, v=None):
    body = json.dumps({"query": q, "variables": v or {}}).encode()
    req = urllib.request.Request("https://backboard.railway.app/graphql/v2", data=body,
                                 headers={"Authorization": "Bearer " + TOK, "Content-Type": "application/json", "User-Agent": UA})
    with urllib.request.urlopen(req, timeout=40) as r:
        d = json.load(r)
    if d.get("errors"):
        raise RuntimeError(d["errors"][0].get("message"))
    return d["data"]


def tg(text):
    data = urllib.parse.urlencode({"chat_id": TG_CHAT, "text": text, "parse_mode": "HTML"}).encode()
    req = urllib.request.Request("https://api.telegram.org/bot%s/sendMessage" % TG_TOKEN, data=data,
                                 headers={"Content-Type": "application/x-www-form-urlencoded", "User-Agent": UA})
    urllib.request.urlopen(req, timeout=30).read()


def latest(vals):
    return vals[-1]["value"] if vals else 0.0


# 1) 服務清單（動態抓，IDs 改了也不會壞）
svc = gql('query($p:String!){ project(id:$p){ services { edges { node { id name } } } } }', {"p": PROJECT})
services = [(e["node"]["id"], e["node"]["name"]) for e in svc["project"]["services"]["edges"]]

now = datetime.datetime.now(datetime.timezone.utc)
start = (now - datetime.timedelta(hours=2)).strftime("%Y-%m-%dT%H:%M:%SZ")
MQ = ('query($p:String!,$e:String!,$s:String!,$st:DateTime!){'
      ' metrics(projectId:$p, environmentId:$e, serviceId:$s,'
      ' measurements:[CPU_USAGE,MEMORY_USAGE_GB], startDate:$st, sampleRateSeconds:600){'
      ' measurement values { ts value } } }')

rows = []
alerts = []
for sid, sname in services:
    try:
        m = gql(MQ, {"p": PROJECT, "e": ENVID, "s": sid, "st": start})["metrics"]
    except Exception:
        rows.append((sname, None, None))
        continue
    by = {x["measurement"]: x["values"] for x in m}
    cpu = latest(by.get("CPU_USAGE", []))
    mem = latest(by.get("MEMORY_USAGE_GB", []))
    rows.append((sname, cpu, mem))
    if mem > MEM_ALERT:
        alerts.append("%s 記憶體 %.2fGB 超門檻(%.2f)" % (sname, mem, MEM_ALERT))
    if cpu > CPU_ALERT:
        alerts.append("%s CPU %.2f vCPU 超門檻(%.2f)" % (sname, cpu, CPU_ALERT))

# 2) 專案用量（網路流出量本期估；金額請看 Railway Billing）
egress = None
try:
    eu = gql('query($p:String!){ estimatedUsage(projectId:$p, measurements:[NETWORK_TX_GB]){ measurement estimatedValue } }', {"p": PROJECT})["estimatedUsage"]
    for x in eu:
        if x["measurement"] == "NETWORK_TX_GB":
            egress = x["estimatedValue"]
    if egress is not None and egress > NET_TX_ALERT:
        alerts.append("網路流出 %.1fGB 接近/超過免費額度門檻(%.0f)" % (egress, NET_TX_ALERT))
except Exception:
    pass

breach = len(alerts) > 0
icon = "⚠️" if breach else "✅"  # ⚠️ / ✅
lines = ["%s <b>Railway 系統負擔</b>" % icon]
for sname, cpu, mem in rows:
    if cpu is None:
        lines.append("· %s：(無資料)" % sname)
    else:
        lines.append("· %s：CPU %.2f vCPU · 記憶體 %.2f GB" % (sname, cpu, mem))
if egress is not None:
    lines.append("網路流出(本期估): %.2f GB" % egress)
lines.append("月費以 Railway → Billing 為準（API 只給用量、不給金額）")
for a in alerts:
    lines.append("⚠️ " + a)
msg = "\n".join(lines)
print(msg.replace("<b>", "").replace("</b>", ""))

if breach or FORCE:
    if TG_TOKEN and TG_CHAT:
        tg(msg)
        print("[telegram sent]")
    else:
        print("[telegram not configured — message above would be sent]")
else:
    print("[healthy — no alert]")
