'use client'

import PhoneFrame from '@/components/PhoneFrame'
import ScrollArea from '@/components/ScrollArea'

export default function SupportPage() {
  return (
    <PhoneFrame>
      <header style={header}>
        <a href="/" style={back}>← 返回</a>
        <strong style={{ fontSize: 16 }}>支援與聯絡</strong>
        <span style={{ width: 36 }} />
      </header>

      <ScrollArea>
      <div style={body}>
        <Section title="聯絡我們">
          <p style={p}>有任何問題、帳號或資料相關需求，歡迎來信：</p>
          <a href="mailto:info@hero-mi.com" style={mail}>info@hero-mi.com</a>
        </Section>

        <Section title="連接 / 中斷 Strava">
          <ol style={ol}>
            <li><b>連接</b>：登入後 → 右上角頭像進「會員中心」→「運動數據」分頁 → 點官方「Connect with Strava」按鈕 → 於 Strava 授權。</li>
            <li><b>中斷</b>：「運動數據」分頁 →「中斷」。中斷後我們不再同步你的新活動；已匯入的紀錄會保留。</li>
            <li><b>更換 Strava 帳號</b>：先到 <a href="https://www.strava.com/logout" target="_blank" rel="noreferrer" style={link}>strava.com 登出</a> → 回本站「中斷」→ 重新連接（連到的是你瀏覽器當下登入的 Strava 帳號）。</li>
          </ol>
          <p style={pDim}>我們只會匯入你「連接之後」的跑步活動（距離、時間、配速、爬升、心率、路線），用於賽事任務達成判定、排行榜與等級。</p>
          <div style={{ marginTop: 14 }}>
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img src="/strava/powered_by_strava.svg" alt="Powered by Strava" style={{ height: 18, opacity: 0.85 }} />
          </div>
        </Section>

        <Section title="報名與退款">
          <p style={p}>雲端馬拉松為線上活動／數位服務，<b>報名繳費完成後恕不退款</b>，且<b>不適用七天鑑賞期</b>。詳見 <a href="/terms" style={link}>服務條款與退款政策</a>。</p>
        </Section>

        <Section title="隱私與資料">
          <p style={p}>我們如何蒐集與使用你的資料，請見 <a href="/privacy" style={link}>隱私權政策</a>。你可隨時來信要求查詢或刪除你的個人資料／帳號。</p>
        </Section>

        <div style={{ textAlign: 'center', marginTop: 28, fontSize: 12, color: 'var(--tx-faint)' }}>
          DOR · 雲端馬拉松　·　<a href="/terms" style={link}>服務條款</a>　·　<a href="/privacy" style={link}>隱私權政策</a>
        </div>
      </div>
      </ScrollArea>
    </PhoneFrame>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div style={card}>
      <h2 style={{ fontSize: 15, fontWeight: 800, margin: '0 0 10px', color: 'var(--tx)' }}>{title}</h2>
      {children}
    </div>
  )
}

const header: React.CSSProperties = { padding: '14px 18px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid var(--line)' }
const back: React.CSSProperties = { color: 'var(--tx-dim)', fontSize: 14, textDecoration: 'none' }
const body: React.CSSProperties = { maxWidth: 640, margin: '0 auto', padding: '18px 18px 40px', display: 'flex', flexDirection: 'column', gap: 14 }
const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 16 }
const p: React.CSSProperties = { fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.7, margin: '0 0 8px' }
const pDim: React.CSSProperties = { fontSize: 12.5, color: 'var(--tx-dim)', lineHeight: 1.7, margin: '10px 0 0' }
const ol: React.CSSProperties = { fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.8, paddingLeft: 18, margin: 0, display: 'flex', flexDirection: 'column', gap: 6 }
const mail: React.CSSProperties = { fontSize: 16, fontWeight: 800, color: 'var(--fug)', textDecoration: 'none' }
const link: React.CSSProperties = { color: 'var(--fug)', textDecoration: 'underline' }
