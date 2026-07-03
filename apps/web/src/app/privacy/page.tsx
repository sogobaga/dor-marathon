'use client'

import PhoneFrame from '@/components/PhoneFrame'
import ScrollArea from '@/components/ScrollArea'

export default function PrivacyPage() {
  return (
    <PhoneFrame>
      <header style={header}>
        <a href="/" style={back}>← 返回</a>
        <strong style={{ fontSize: 16 }}>隱私權政策</strong>
        <span style={{ width: 36 }} />
      </header>

      <ScrollArea>
      <div style={body}>
        <p style={{ ...p, color: 'var(--tx-faint)' }}>最後更新：2026 年 6 月</p>
        <p style={p}>DOR 城市探索（dor.hero-mi.com，以下簡稱「本平台」）重視你的隱私。本政策說明我們蒐集哪些資料、如何使用，以及你的權利。</p>

        <H>1. 我們蒐集的資料</H>
        <ul style={ul}>
          <li><b>帳號資料</b>：以 Google 登入時取得的顯示名稱、Email、頭像。</li>
          <li><b>個人/報名資料</b>：你在報名時填寫的真實姓名、暱稱、手機、地址、生日、性別等（依各賽事必填設定）。</li>
          <li><b>運動數據</b>：經你同意連接 Strava 後匯入的跑步活動，包含距離、移動時間、配速、爬升、心率與路線軌跡。</li>
        </ul>

        <H>2. 我們如何使用</H>
        <ul style={ul}>
          <li>處理賽事報名與身分識別。</li>
          <li>判定賽事任務達成、計算完賽與排行榜。</li>
          <li>累積經驗值、推導等級、產生完賽證明。</li>
          <li>排行榜對外一律以<b>暱稱</b>顯示，不顯示你的真實姓名。</li>
        </ul>

        <H>3. 第三方服務</H>
        <ul style={ul}>
          <li><b>Strava</b>：運動數據來源，依其 <a href="https://www.strava.com/legal/privacy" target="_blank" rel="noreferrer" style={link}>Strava 隱私政策</a> 處理。</li>
          <li><b>Google</b>：第三方登入。</li>
          <li><b>綠界 ECPay</b>：金流付款處理。</li>
        </ul>

        <H>4. 關於 Strava 資料</H>
        <ul style={ul}>
          <li>我們僅在你以官方「Connect with Strava」流程<b>明確同意</b>後才連接。</li>
          <li>僅匯入你<b>連接之後</b>的跑步活動，不抓取連接/註冊前的歷史資料。</li>
          <li>你可隨時於「會員中心 → 運動數據」<b>中斷</b>連接；中斷後我們不再同步新活動。</li>
          <li>我們不會公開散布你的個別活動數據，亦不會用於本平台服務以外之用途。</li>
        </ul>

        <H>5. 資料保留與刪除</H>
        <p style={p}>你可來信要求查詢、更正或刪除你的個人資料與帳號。我們會在合理期間內處理。</p>

        <H>6. 聯絡我們</H>
        <p style={p}>隱私相關問題請來信：<a href="mailto:info@hero-mi.com" style={link}>info@hero-mi.com</a></p>

        <div style={{ textAlign: 'center', marginTop: 28, fontSize: 12, color: 'var(--tx-faint)' }}>
          DOR · 城市探索　·　<a href="/terms" style={link}>服務條款</a>　·　<a href="/support" style={link}>支援與聯絡</a>
        </div>
      </div>
      </ScrollArea>
    </PhoneFrame>
  )
}

function H({ children }: { children: React.ReactNode }) {
  return <h2 style={{ fontSize: 15, fontWeight: 800, margin: '20px 0 8px', color: 'var(--tx)' }}>{children}</h2>
}

const header: React.CSSProperties = { padding: '14px 18px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid var(--line)' }
const back: React.CSSProperties = { color: 'var(--tx-dim)', fontSize: 14, textDecoration: 'none' }
const body: React.CSSProperties = { maxWidth: 640, margin: '0 auto', padding: '18px 20px 40px' }
const p: React.CSSProperties = { fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.75, margin: '0 0 10px' }
const ul: React.CSSProperties = { fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.8, paddingLeft: 18, margin: '0 0 6px', display: 'flex', flexDirection: 'column', gap: 5 }
const link: React.CSSProperties = { color: 'var(--fug)', textDecoration: 'underline' }
