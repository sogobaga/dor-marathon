---
description: 跑 DOR 資安對抗性審計（五維度掃描＋每發現獨立複核推翻誤報），回報 confirmed 漏洞並修 critical
---

跑一次全站資安審計。這是唯讀盤點——五維度平行掃描（公開端點機敏欄位洩漏／跨用戶個資 PII／金流安全／授權越權／密鑰注入），每個疑似發現都會派**獨立的對抗性複核員嘗試推翻它**，只有推翻不掉的才判 CONFIRMED（過濾誤報，不浪費使用者注意力）。

## 執行步驟

1. 用 Workflow 工具啟動 named workflow `security-audit`。若使用者在 `$ARGUMENTS` 給了聚焦範圍（例如剛改的套件名、剛加的功能），用 `args` 傳入那段字串；沒給就全站掃：
   - `Workflow({ name: "security-audit", args: "$ARGUMENTS" })`（$ARGUMENTS 為空就傳 `args: ""` 或省略 args）

2. 完成通知到達後，讀 `confirmed` 陣列（已通過對抗驗證的真漏洞）：
   - **依嚴重度排序**呈現給使用者：每項寫清楚「洩漏/破壞什麼、誰能觸發、影響面、修法」。
   - **critical / high 的：立即親自修**（資安關鍵，不委派），修完進版號、commit、push，並在 commit message 用 `sec(...)` 前綴。
   - **medium / low 的：列清單給使用者決策**，除非修法是一行且零風險（那就順手修）。
   - `needsHuman` 陣列的：明確標示需要使用者判斷的點（例如「這是刻意設計還是漏洞」）。
   - 沒有 confirmed 就明說「本次未發現成立漏洞」，並簡述哪些維度的疑點在對抗驗證階段被推翻（讓使用者知道核心防護狀況）。

3. 修完後，把「本次修掉的漏洞模式」沉澱進 `.claude/workflows/security-audit.js` 的 `CONTEXT` 常數「已知漏洞模式」清單，讓下次審計把同類再犯抓得更準；同步更新 go-live-todos 記憶。

## 注意
- workflow 本身唯讀，只找不改；修補是我讀完結果後的動作。
- 對抗驗證會過濾掉大量誤報，這是設計——不要跳過驗證直接信 finder 的原始清單。
- 每個 finding 都要落到 file:line 才算數，模糊的描述性顧慮不列入 confirmed。
