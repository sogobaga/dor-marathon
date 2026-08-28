export const meta = {
  name: 'security-audit',
  description: 'DOR 資安對抗性審計：個資/金流/授權/公開端點洩漏/密鑰注入 五維度掃描＋每發現獨立複核推翻誤報',
  whenToUse: '大改動後、上線前、或定期體檢。回傳經對抗驗證後 CONFIRMED 的漏洞清單（依嚴重度）。可用 args 傳入本次要聚焦的範圍字串（例如剛改的套件名），未傳則全站掃。',
  phases: [
    { title: 'Audit', detail: '五維度掃描→每發現對抗驗證（pipeline）' },
  ],
}

const FINDINGS_SCHEMA = {
  type: 'object',
  required: ['findings'],
  properties: {
    findings: {
      type: 'array',
      items: {
        type: 'object',
        required: ['title', 'severity', 'file', 'leaks', 'trigger', 'fix'],
        properties: {
          title: { type: 'string' },
          severity: { type: 'string', enum: ['critical', 'high', 'medium', 'low'] },
          file: { type: 'string', description: 'file:line' },
          leaks: { type: 'string', description: '洩漏/暴露了什麼機敏資料，或什麼安全性質被破壞' },
          trigger: { type: 'string', description: '誰在什麼條件下能觸發/看到' },
          fix: { type: 'string', description: '建議修法' },
        },
      },
    },
  },
}

const VERDICT_SCHEMA = {
  type: 'object',
  required: ['verdict', 'reason'],
  properties: {
    verdict: { type: 'string', enum: ['CONFIRMED', 'FALSE_POSITIVE', 'NEEDS_HUMAN'] },
    severity_adjusted: { type: 'string', enum: ['critical', 'high', 'medium', 'low', 'none'] },
    reason: { type: 'string', description: '對抗驗證結論：嘗試推翻此發現的結果（有無 gate/是否已清空/是否真能觸發/實際影響面）' },
    fix_confirmed: { type: 'string', description: '若成立，最小修法（file:line）' },
  },
}

// 每次審計後把「已修的漏洞模式」沉澱到這裡，讓後續審計把同類再犯抓得更準。
const CONTEXT = `
專案：DOR 馬拉松平台（www.dor.tw，正式上線中）。Go 後端 services/api（chi router、pgx、internal/* 分套件）＋ Next.js 前端 apps/web。
既有安全慣例（背景，勿當漏洞）：帳號編碼 account_code 機敏（面向玩家 API 不得回傳他人的）；單一登入 session_epoch；活動數據來源 gate；ECPay 走 CheckMacValue 簽章、AES、卡號不落地；admin 用 adminacct RequirePerm/RequireSuper 分權；HTML 輸入走 bluemonday 消毒；SQL 全參數化。
**已知漏洞模式（重點找同類再犯）**：
1. 公開端點漏清機敏欄位——race/service.go 的 ListPublic/GetPublicDetail 回傳 Race/RaceDetail 前須逐一清空機敏欄位（已清 GroupKey/RewardConfig/EntryRewardConfig/TestWhitelist/ReviewNote）。任何新增回傳賽事資料給公開端點的路徑，或 Race struct 新增機敏欄位，都要確認走過清空區塊。
2. 白名單入口只有前端擋——*_entry 白名單功能（monopoly/title/achievement/personal/explore/training/strategy/analytics…）後端必須有 requireEntry 中介層複查（resolveEntry != "shown" → 403），不能只靠前端 UI 隱藏。monopoly SEC-H5、title/achievement 已補。檢查是否有新 entry 功能只做了前端 gate。
`

phase('Audit')

const scope = typeof args === 'string' && args.trim() ? `\n\n**本次聚焦範圍（優先但不限於）**：${args.trim()}` : ''

const DIMENSIONS = [
  {
    key: 'public-leak',
    prompt: `維度：**公開/弱認證端點的機敏欄位洩漏**。系統性檢查所有「公開可讀」或「OptionalAuth」或「登入即可、不分對象」的 API 回應 struct，是否把不該給該對象看的欄位一起序列化出去。
方法：
1. grep 所有 handler 回傳的 struct（respondJSON 的 payload）、特別是名稱含 Public/List/Detail/Preview/Ranking/Leaderboard/Feed 的。
2. 逐一比對「這個端點的受眾」vs「struct 的每個 json 欄位」：白名單 email、金流機率(prob_bp)/面額權重/金額區間、序號 code/link、跑團鑰匙 group_key、後台審核備註 review_note、其他使用者的 account_code/email/phone/address/birthday/real_name、密碼 hash、token、webhook secret、任何內部備註。
3. 重點掃 race（public detail/list、reward preview、brochure、standings、leaderboard、contributors、recommendations）、profile（dashboard、heroes、follows、athlete）、explore（ranking）、partner、activityreward、monopoly、event 各套件的公開回傳。
4. 比照「Repository 撈全欄位 → Service 逐一清空機敏欄位」的模式，找出「撈了全欄位但沒清乾淨」的端點；以及 Race/RaceDetail struct 是否有新機敏欄位未被清空區塊涵蓋。
每個發現要具體到欄位名與 file:line。`,
  },
  {
    key: 'pii-cross-user',
    prompt: `維度：**跨使用者個資（PII）洩漏 / 帳號編碼隱私**。檢查面向玩家的 API 是否洩漏「他人」的個資。
方法：
1. grep account_code / email / phone / address / birthday / real_name / snap_real_name / snap_phone / snap_address 出現在哪些回應。
2. 判斷每處：是「只回本人自己的」還是「列表/他人也會帶」？面向玩家的排行榜/貢獻榜/追蹤/推薦/報名名單/賽事詳情/站內信/大富翁/城市探索等，凡帶到「別人的」PII 或 account_code 即為漏洞（account_code 面向玩家列表一律不得回傳他人的，僅本人個資頁+後台例外）。
3. 檢查 user_id 來源：是否有端點用 query/body 傳入的 user_id 直接查別人資料（IDOR），而非從 token 的 CtxKeyUserID 取。
4. 檢查後台管理端點回傳 PII 是否有 perm gate（admin 看得到是正常，但要確認真的有 RequirePerm）。`,
  },
  {
    key: 'payment',
    prompt: `維度：**金流安全**（ECPay 綠界）。
方法：
1. ECPay 回調（Notify/webhook）：CheckMacValue 簽章是否強制驗證、失敗是否拒絕、金額是否與訂單比對（防竄改回調）、TradeNo 對應、重放冪等。
2. 訂單金額：建立訂單/報名時金額是否由後端計算，還是信任前端傳入？折抵（VIP券/優惠券/序號/promo）能否被竄改成負數或超額折抵？
3. 訂單狀態機：pending→paid→cancelled→refunded 有無非法跳躍；退款金額可否超過原付款；重複退款防護（曾抓到重試退兩次）。
4. 綁卡/定期定額：card token 是否落地（PCI）、bind 環境 gate、續扣金額鎖定。
5. 金流憑證（HashKey/HashIV/MerchantID）是否 hardcode 在 repo 或前端 bundle。
grep internal/payment、race repository 訂單/報名交易、ecpay 相關。`,
  },
  {
    key: 'authz',
    prompt: `維度：**授權 / 越權 / 存取控制**。
方法：
1. admin 路由（main.go 掛 perm(...)/RequireSuper 的區塊）：逐一確認每個 /admin/* 端點都有對應 perm 中介層，有沒有漏掛直接暴露的（尤其新加端點）。
2. 寫入/危險操作：改別人資料、發獎、調 EXP/DP/GP、改訂單狀態、發序號、廣播、刪除——是否 admin-only 且 perm 正確。
3. 跨用戶寫入（IDOR 寫）：follow/unfollow、取消報名、上傳活動、改個資——user_id 是否恆從 token 取，能否對「別人的」資源操作。
4. session_epoch 單一登入：refresh 是否正確擋 stale、有無繞過。
5. **白名單入口後端複查**（已知模式）：*_entry 白名單功能後端有沒有 requireEntry，還是只有前端 UI 擋（可直接打 API 繞過）——逐一檢查每個 entry 功能。`,
  },
  {
    key: 'secret-injection',
    prompt: `維度：**密鑰洩漏 / 注入 / 輸出安全**。
方法：
1. 密碼/token/密鑰：password_hash 是否出現在任何回應 struct；JWT secret、ECPay HashKey/HashIV、RESEND/TELEGRAM/R2/DATABASE_URL 是否 hardcode（非 env 讀取）或洩漏到前端 bundle/API。
2. SQL 注入：grep 字串拼接 SQL（fmt.Sprintf 進 query、+ 拼 WHERE）——應全走參數化 $1；特別看動態排序/IN 清單/LIKE。
3. XSS / HTML 注入：使用者可輸入且會被前台 render 的 HTML（簡章 block content、合作商家 description、站內信、賽事說明）——後端是否 sanitize（bluemonday）；前端 dangerouslySetInnerHTML 有無未消毒。
4. 檔案/SSRF：圖片 URL、影片嵌入(FB Reel/YouTube)、routing 代呼外部 API——scheme/網域白名單是否嚴格。
5. CSP / 開放重定向 / CORS：next.config.mjs headers、任何 redirect 是否驗證目標網域。`,
  },
]

const results = await pipeline(
  DIMENSIONS,
  (d) => agent(`${CONTEXT}${scope}\n\n你是資安稽核員，唯讀審計（不要改任何檔案）。${d.prompt}\n\n只回報「真的有問題」的發現，寧缺勿濫但重要的別漏；每個發現具體到 file:line 與欄位/函式名。若此維度掃完沒有真問題，回空 findings 陣列。`,
    { label: `find:${d.key}`, phase: 'Audit', model: 'sonnet', effort: 'high', schema: FINDINGS_SCHEMA }),
  (review, d) => {
    const findings = (review?.findings ?? [])
    if (findings.length === 0) return []
    return parallel(findings.map((f) => () =>
      agent(`${CONTEXT}\n\n你是資安稽核的**對抗性複核員**。有人回報以下漏洞，你的任務是**盡力推翻它**——去讀實際程式碼確認：是否其實已有 gate/清空/驗證？是否實際上無法觸發？受眾是否其實看不到？影響面是否被誇大？只有你真的無法推翻、確認可被利用，才判 CONFIRMED。

回報漏洞（維度 ${d.key}）：
標題：${f.title}
嚴重度：${f.severity}
位置：${f.file}
洩漏/破壞：${f.leaks}
觸發條件：${f.trigger}
建議修法：${f.fix}

去讀 ${f.file} 及相關程式碼，對抗性驗證。若 CONFIRMED，給出經你確認的最小修法(file:line)與校準後的嚴重度。`,
        { label: `verify:${d.key}`, phase: 'Audit', model: 'sonnet', effort: 'high', schema: VERDICT_SCHEMA })
        .then((v) => ({ finding: f, dimension: d.key, verdict: v }))))
  },
)

const flat = results.flat().filter(Boolean)
const confirmed = flat.filter((x) => x.verdict?.verdict === 'CONFIRMED')
const needsHuman = flat.filter((x) => x.verdict?.verdict === 'NEEDS_HUMAN')
const falsePositives = flat.filter((x) => x.verdict?.verdict === 'FALSE_POSITIVE').length
log(`資安審計完成：掃出 ${flat.length} 個疑點 → CONFIRMED ${confirmed.length}、待人工 ${needsHuman.length}、誤報 ${falsePositives}`)
return { confirmed, needsHuman, false_positive_count: falsePositives, all_count: flat.length }
