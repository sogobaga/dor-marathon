'use client'

import { useEffect } from 'react'
import PhoneFrame from '@/components/PhoneFrame'
import ScrollArea from '@/components/ScrollArea'
import { scrollIntoNearest } from '@/lib/scrollIntoNearest'

export default function PrivacyPage() {
  // 錨點（#coros／#ai）：不用瀏覽器原生 hash 捲動（見 lib/scrollIntoNearest.ts 檔頭的 iOS 根容器問題），
  // 改成掛載與 hashchange 時只捲最近的 ScrollArea。
  useEffect(() => {
    const go = () => {
      let id = ''
      try { id = decodeURIComponent(window.location.hash.replace(/^#/, '')) } catch { return }
      if (id) scrollIntoNearest(document.getElementById(id), { block: 'start', offset: 8 })
    }
    go()
    window.addEventListener('hashchange', go)
    return () => window.removeEventListener('hashchange', go)
  }, [])
  return (
    <PhoneFrame>
      <header style={header}>
        <a href="/" style={back}>← 返回</a>
        <strong style={{ fontSize: 16 }}>隱私權政策</strong>
        <span style={{ width: 36 }} />
      </header>

      <ScrollArea>
      <div style={body}>
        <p style={{ ...p, color: 'var(--tx-faint)' }}>最後更新：2026 年 10 月</p>
        <p style={p}>DOR 城市探索（www.dor.tw，以下簡稱「本平台」）重視你的隱私。本政策說明我們蒐集哪些資料、如何使用，以及你的權利。</p>

        <H>1. 我們蒐集的資料</H>
        <ul style={ul}>
          <li><b>帳號資料</b>：以 Google 登入時取得的顯示名稱、Email、頭像。</li>
          <li><b>個人/報名資料</b>：你在報名時填寫的真實姓名、暱稱、手機、地址、生日、性別等（依各賽事必填設定）。</li>
          <li><b>運動數據</b>：經你同意連接 COROS、Strava（或透過 Terra 連接的其他運動裝置）後匯入的跑步、健行與走路活動，包含距離、時間、配速、心率等；各來源實際蒐集的項目，COROS 見第 5 節、Strava 見第 4 節、Terra 見第 3 節。</li>
        </ul>

        <H>2. 我們如何使用</H>
        <ul style={ul}>
          <li>處理賽事報名與身分識別。</li>
          <li>判定賽事任務達成、計算完賽與排行榜（賽事排名與里程競賽採計 App GPS 正式紀錄；主辦方開放外部數據的賽事會一併採計 Garmin／COROS 等裝置同步紀錄；Strava 數據依其平台規範一律不計入賽事）。</li>
          <li>累積經驗值、推導等級、產生完賽證明。</li>
          <li>排行榜對外一律以<b>暱稱</b>顯示，不顯示你的真實姓名。</li>
        </ul>

        <H>3. 第三方服務</H>
        <ul style={ul}>
          <li><b>Strava</b>：運動數據來源，依其 <a href="https://www.strava.com/legal/privacy" target="_blank" rel="noreferrer" style={link}>Strava 隱私政策</a> 處理。</li>
          <li><b>COROS</b>：運動數據來源。你授權後，DOR 會直接向 COROS 讀取你的運動紀錄，不經過其他整合商；蒐集項目與你的選擇見第 5 節，另請參閱 <a href="https://coros.com/privacy" target="_blank" rel="noreferrer" style={link}>COROS 隱私權政策</a>。</li>
          <li><b>Terra（tryterra.co）</b>：運動穿戴資料整合商。當你選擇「連接你常用的跑步裝置（Garmin 等）」時，我們透過 Terra 取得你的<b>跑步活動資料</b>（距離、時間、配速、心率、爬升、路線）。<b>COROS 現已改由 DOR 直接串接</b>（見第 5 節），新的 COROS 連接不再經過 Terra；先前已透過 Terra 連接 COROS 的使用者，其既有連線仍依本條處理。此屬<b>跨境傳輸</b>（資料經 Terra 於境外處理），僅在你<b>明確授權連接後</b>才啟用；你可隨時中斷，中斷即停止取得新資料。詳見 <a href="https://tryterra.co/privacy-policy" target="_blank" rel="noreferrer" style={link}>Terra 隱私政策</a>。</li>
          <li><b>Google</b>：第三方登入。</li>
          <li><b>綠界 ECPay</b>：金流付款處理。</li>
          <li><b>雲端基礎設施</b>：本平台運作於雲端主機、資料庫與網路防護／加速服務之上，這些供應商僅依我們的指示代為儲存與傳輸資料，不得自行使用；處理地點可能位於台灣境外。</li>
        </ul>

        <H>4. 關於 Strava 資料</H>
        <ul style={ul}>
          <li>我們僅在你以官方「Connect with Strava」流程<b>明確同意</b>後才連接。</li>
          <li>僅匯入你<b>連接之後</b>的跑步活動，不抓取連接/註冊前的歷史資料。</li>
          <li>你可隨時於「會員中心 → 運動數據」<b>中斷</b>連接；中斷後我們不再同步新活動。</li>
          <li>我們不會公開散布你的個別活動數據，亦不會用於本平台服務以外之用途。</li>
          <li>當你於本平台<b>中斷連接</b>，或直接於 Strava 端<b>撤銷授權</b>，我們將刪除已匯入的 Strava 活動資料（你已獲得的平台獎勵，如經驗值、DP 幣等，不受影響）。</li>
        </ul>

        <H id="coros">5. COROS 直連</H>
        <p style={p}>當你選擇把 COROS 帳號連接到 DOR，我們會<b>直接</b>向 COROS 取得你的運動紀錄，<b>不經過 Terra 等任何中間整合商</b>。連接一律在 COROS 的官方授權頁面完成（DOR 不會取得、也不會保存你的 COROS 帳號密碼），且只有在你<b>勾選同意並完成授權後</b>才會啟用。不連接不影響你使用 DOR 的其他功能（例如以 DOR 的 GPS 跑步追蹤記錄成績），只是手錶紀錄不會匯入。</p>

        <SubH>5.1 取得與保存的資料、如何同步</SubH>
        <ul style={ul}>
          <li><b>我們取得與保存的資料</b>：從你的 COROS 帳號讀取跑步、健行、走路類的運動紀錄，並保存——開始與結束時間、時間、距離、配速、<b>平均心率</b>、手錶型號（取自你的 COROS 帳號裝置清單）、COROS 活動編號（用於避免重複匯入），以及 COROS 為你的帳號產生的使用者識別碼（用來避免同一個 COROS 帳號被連接到多個 DOR 帳號）、授權憑證（加密保存）、連接時間與上次同步時間。DOR 只呼叫「讀取運動紀錄與裝置清單」所需的功能；除用來辨識帳號的唯一識別碼外，<b>不會讀取</b>你的睡眠、體能與健康指標或個人檔案。COROS 授權畫面所列的權限範圍可能較廣，但 DOR 實際只使用上述部分。</li>
          <li><b>我們不會取得或保存</b>：路線與位置座標（GPS 軌跡）、FIT 檔案。DOR 透過 COROS 提供的標準介面（MCP）由伺服器程式直接讀取，過程中沒有任何 AI 模型參與（見第 6 節）。</li>
          <li><b>如何同步</b>：<b>只有在你開啟 DOR 時</b>才會自動向 COROS 讀取新紀錄，且每位使用者<b>最短約每 25 分鐘</b>一次；你沒有開啟 DOR 時，我們不會讀取你的 COROS 資料。你也可以在「會員中心 → 運動數據」按「匯入數據」立即同步（有頻率限制）。我們只保存你<b>連接之後</b>開始的活動。</li>
          <li><b>授權到期與重新授權</b>：COROS 的授權會到期；到期或失效時我們會提示你「重新授權」，重新授權不會刪除你已匯入的紀錄。</li>
        </ul>

        <SubH>5.2 用途、可見範圍、保存與刪除</SubH>
        <ul style={ul}>
          <li><b>用途</b>：僅用於——①把活動計入你的 DOR 累計里程、經驗值、等級、成就與獎勵；②計入你參加的賽事或挑戰的成績與排名（限主辦方開放外部裝置數據的賽事；Strava 數據不計入賽事）；③判定任務達成與完賽；④避免同一趟活動與你的 DOR GPS 紀錄、其他來源或其他帳號重複計算，並檢查數據是否合理（異常的距離或配速可能不計入或被標記）；⑤客服、故障排除與防弊。我們不會把這些資料用於上述以外的目的。</li>
          <li><b>誰看得到</b>：<b>只有你本人</b>能在個人頁看到自己每一筆活動的明細（含心率與裝置型號）。其他使用者<b>只會看到衍生結果</b>——例如累計里程、是否完賽、完賽時間與名次，以你的暱稱（顯示名稱）、頭像與稱號呈現於賽事排行榜、貢獻榜與百里英雄榜等榜單；<b>不會看到你的單筆活動明細、路線或心率數值</b>。這些榜單可能是公開頁面（包含未登入的訪客）。經 DOR 授權的管理人員，僅在客服、爭議處理與防弊必要時，可於後台查看你的活動紀錄。</li>
          <li><b>不出售、不分享</b>：我們<b>不會出售、出租或與任何第三方分享</b>你的 COROS 活動資料。為運作本服務，我們使用雲端主機、資料庫與網路防護／加速服務的供應商（見第 3 節），他們僅依我們的指示代為儲存與傳輸資料，處理地點可能位於台灣境外。僅 DOR 管理員可見的營運通知（例如同步中斷提醒）可能含你的顯示名稱與同步狀態，不含活動內容。</li>
          <li><b>安全</b>：資料傳輸使用 HTTPS 加密，授權憑證加密保存，後台僅限具權限的管理人員存取。</li>
          <li><b>保存期間</b>：已匯入的紀錄保存到你中斷連接、要求刪除或刪除帳號為止；授權憑證在你中斷連接時刪除。</li>
          <li><b>在 DOR 中斷連線</b>：到「會員中心 → 運動數據」按「中斷連線」（隨時都可以，不受任何開放設定限制），我們會立即停止讀取、刪除保存的授權憑證，並<b>刪除已匯入的 COROS 活動紀錄</b>（賽事成績與排名會隨之重新計算）。你已獲得的獎勵（經驗值、DP 幣、累計里程等）<b>不受影響</b>；其發放紀錄（例如每次獎勵對應的距離與時間點，以及為避免重複發放而保存、無法還原的雜湊值）會保留，這些紀錄不含心率、路線或裝置型號。</li>
          <li><b>在 COROS 端撤銷</b>：你也可以在 COROS 帳戶中撤銷對 DOR 的授權；撤銷後 DOR 無法再讀取新紀錄，但已匯入的紀錄會保留到你在 DOR 按「中斷連線」或來信要求刪除為止，建議同時在 DOR 中斷連線。</li>
          <li><b>你的權利</b>：你可隨時來信 <a href="mailto:service@dor.tw" style={link}>service@dor.tw</a>，依個人資料保護法請求查詢、閱覽、複製、更正、停止處理或刪除與你有關的資料（含上述運動紀錄），我們會在合理期間內處理。</li>
          <li><b>相關政策與商標</b>：詳見 <a href="https://coros.com/privacy" target="_blank" rel="noreferrer" style={link}>COROS 隱私權政策</a>。COROS 為其所有人之商標；DOR 與 COROS 並無隸屬、贊助或背書關係。</li>
        </ul>

        <H id="ai">6. 外部 AI 服務</H>
        <p style={p}>我們<b>不會</b>將你的運動數據（包含從 COROS、Strava 或其他裝置與平台取得的活動紀錄、心率與裝置型號）提供給任何外部人工智慧（AI）服務，也<b>不會</b>使用這些資料訓練、測試、評估或改進任何 AI 模型。本平台目前<b>沒有</b>以 AI 處理使用者運動數據的功能；日後若要新增，我們會先更新本政策並取得你的明確同意（你可隨時撤回）。COROS 來源的資料不會用於此類用途。</p>

        <H>7. 團練同步跑（即時位置）</H>
        <p style={p}>「團練同步跑」只在你參加團練、並<b>主動按下「開始跑步」且同意說明後</b>才啟用。啟用期間，你的即時位置<b>只暫存於伺服器的記憶體快取</b>，只分享給<b>同一個團練中正在跑步、且同時分享位置的成員</b>（不限地點的團練完全不傳送位置，只顯示在跑人數）；位置<b>不會被保存</b>，不寫入資料庫或紀錄檔，你結束跑步或離開頁面即刪除，連線異常中斷時也會在數分鐘內自動清除。為保護住家等隱私，若你不是從集合地點附近出發，<b>起點 200 公尺內、以及之後回到起點 200 公尺內，都不會分享你的位置</b>。你也可以隨時暫停分享。</p>

        <H>8. 資料保留與刪除</H>
        <p style={p}>你可來信要求查詢、更正或刪除你的個人資料與帳號。我們會在合理期間內處理。連接 COROS 後的保存與刪除，另見第 5 節；Strava 見第 4 節。</p>

        <H>9. 聯絡我們</H>
        <p style={p}>隱私相關問題請來信：<a href="mailto:service@dor.tw" style={link}>service@dor.tw</a></p>
        <ul style={ul}>
          <li>統一編號：83005678</li>
        </ul>

        <div style={{ textAlign: 'center', marginTop: 28, fontSize: 12, color: 'var(--tx-faint)' }}>
          DOR · 城市探索　·　<a href="/terms" style={link}>服務條款</a>　·　<a href="/support" style={link}>支援與聯絡</a>
        </div>
      </div>
      </ScrollArea>
    </PhoneFrame>
  )
}

function H({ id, children }: { id?: string; children: React.ReactNode }) {
  return <h2 id={id} style={{ fontSize: 15, fontWeight: 800, margin: '20px 0 8px', color: 'var(--tx)' }}>{children}</h2>
}

function SubH({ children }: { children: React.ReactNode }) {
  return <h3 style={{ fontSize: 14, fontWeight: 800, margin: '14px 0 6px', color: 'var(--tx)' }}>{children}</h3>
}

const header: React.CSSProperties = { padding: '14px 18px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid var(--line)' }
const back: React.CSSProperties = { color: 'var(--tx-dim)', fontSize: 14, textDecoration: 'none' }
const body: React.CSSProperties = { maxWidth: 640, margin: '0 auto', padding: '18px 20px 40px' }
const p: React.CSSProperties = { fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.75, margin: '0 0 10px' }
const ul: React.CSSProperties = { fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.8, paddingLeft: 18, margin: '0 0 6px', display: 'flex', flexDirection: 'column', gap: 5 }
const link: React.CSSProperties = { color: 'var(--fug)', textDecoration: 'underline' }
