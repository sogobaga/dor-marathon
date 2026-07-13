#!/usr/bin/env python3
# Neon compute 用量監控 + Telegram 超標警示（stdlib only，無需 pip 安裝）。
#
# 讀 Neon project API 的當期 compute_time_seconds → 算「已用 CU-hr」與「照目前速度推估月底 CU-hr」，
# 超過預算門檻（已用達 WARN_PCT，或推估超過預算）就發 Telegram 訊息；正常則靜默（只印 log）。
# 這支只打 Neon「控制台」API，不連資料庫、不會喚醒 Neon compute。
#
# 需要的環境變數（GitHub Actions 用 Secrets 帶入）：
#   NEON_API_KEY, NEON_PROJECT_ID, TELEGRAM_BOT_TOKEN, TELEGRAM_CHAT_ID
# 可調參數（workflow env，非機密）：
#   BUDGET_CU_HR(月預算,預設80)  WARN_PCT(警示比例,預設0.75)
#   USD_PER_CU_HR(Launch 費率,預設0.106)  USD_TWD(匯率,預設32)  FORCE_NOTIFY(強制發訊,測試用)
import os, sys, json, datetime, urllib.request, urllib.parse

try:  # Windows 終端機(cp950)也能印 emoji；GitHub runner 本就 UTF-8
    sys.stdout.reconfigure(encoding="utf-8")
    sys.stderr.reconfigure(encoding="utf-8")
except Exception:
    pass


def env(k, d=None):
    v = os.environ.get(k, "")
    return v if v != "" else d


NEON_API_KEY = env("NEON_API_KEY")
NEON_PROJECT_ID = env("NEON_PROJECT_ID")
TG_TOKEN = env("TELEGRAM_BOT_TOKEN")
TG_CHAT = env("TELEGRAM_CHAT_ID")
BUDGET = float(env("BUDGET_CU_HR", "80"))
WARN = float(env("WARN_PCT", "0.75"))
USD_PER_CU_HR = float(env("USD_PER_CU_HR", "0.106"))
USD_TWD = float(env("USD_TWD", "32"))
FORCE = str(env("FORCE_NOTIFY", "false")).lower() in ("1", "true", "yes")

if not NEON_API_KEY or not NEON_PROJECT_ID:
    print("missing NEON_API_KEY / NEON_PROJECT_ID", file=sys.stderr)
    sys.exit(1)


def parse_iso(s):
    return datetime.datetime.fromisoformat(s.replace("Z", "+00:00"))


def http_json(url, headers=None, data=None):
    req = urllib.request.Request(url, headers=headers or {}, data=data)
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)


def tg_send(text):
    data = urllib.parse.urlencode({"chat_id": TG_CHAT, "text": text, "parse_mode": "HTML"}).encode()
    return http_json("https://api.telegram.org/bot%s/sendMessage" % TG_TOKEN, data=data,
                     headers={"Content-Type": "application/x-www-form-urlencoded"})


proj = http_json("https://console.neon.tech/api/v2/projects/%s" % NEON_PROJECT_ID,
                 headers={"Authorization": "Bearer %s" % NEON_API_KEY, "Accept": "application/json"}).get("project", {})

used = (proj.get("compute_time_seconds") or 0) / 3600.0
start = parse_iso(proj["consumption_period_start"]) if proj.get("consumption_period_start") else None
end = parse_iso(proj["consumption_period_end"]) if proj.get("consumption_period_end") else None
now = datetime.datetime.now(datetime.timezone.utc)

elapsed_s = max((now - start).total_seconds(), 1.0) if start else 1.0
if start and end and (end > start):
    frac = min(max(elapsed_s / (end - start).total_seconds(), 1e-6), 1.0)
else:
    frac = 1.0
projected = used / frac
pct = (used / BUDGET * 100) if BUDGET > 0 else 0.0
active_s = proj.get("active_time_seconds") or 0
awake_ratio = min(active_s / elapsed_s, 1.0)  # compute 醒著時間佔比（健康時應遠低於 1；接近 1＝一直沒休眠）


def twd(cu):
    return cu * USD_PER_CU_HR * USD_TWD


reliable = frac >= 0.05  # 週期至少過 ~5%(約1天)才信任「推估月底」，避免週期初期爆量誤報
over_budget = (BUDGET > 0) and ((used >= BUDGET * WARN) or (reliable and projected > BUDGET))
never_sleeps = (elapsed_s > 2 * 3600) and (awake_ratio > 0.85)  # 疑似 24/7 背景迴圈回歸/異常流量
breach = over_budget or never_sleeps

icon = "⚠️" if breach else "✅"  # ⚠️ / ✅
period = ("%s~%s" % (start.date(), end.date())) if start and end else "?"
proj_txt = ("%.1f CU-hr (~NT$%.0f)" % (projected, twd(projected))) if reliable else "(週期初期，待累積)"
lines = [
    "%s <b>Neon 用量</b>" % icon,
    "本月已用: <b>%.1f</b> / %.0f CU-hr (%.0f%%, ~NT$%.0f)" % (used, BUDGET, pct, twd(used)),
    "推估月底: %s" % proj_txt,
    "compute 醒著佔比: %.0f%%" % (awake_ratio * 100),
    "週期已過 %.0f%% (%s)" % (frac * 100, period),
]
if never_sleeps:
    lines.append("⚠️ compute 幾乎不休眠 → 疑似背景迴圈回歸或異常流量，請查！")
msg = "\n".join(lines)

print(msg.replace("<b>", "").replace("</b>", ""))

if breach or FORCE:
    if TG_TOKEN and TG_CHAT:
        try:
            tg_send(msg)
            print("[telegram sent]")
        except Exception as e:
            print("[telegram error] %s" % e, file=sys.stderr)
            sys.exit(1)
    else:
        print("[telegram not configured — the message above would be sent]")
else:
    print("[healthy — no alert]")
