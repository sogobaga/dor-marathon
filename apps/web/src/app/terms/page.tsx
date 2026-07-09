'use client'

import PhoneFrame from '@/components/PhoneFrame'
import ScrollArea from '@/components/ScrollArea'

export default function TermsPage() {
  return (
    <PhoneFrame>
      <header style={header}>
        <a href="/" style={back}>← 返回</a>
        <strong style={{ fontSize: 16 }}>服務條款與退款政策</strong>
        <span style={{ width: 36 }} />
      </header>

      <ScrollArea>
      <div style={body}>
        <p style={{ ...p, color: 'var(--tx-faint)' }}>最後更新：2026 年 6 月</p>

        {/* 退款政策置頂強調 */}
        <div style={notice}>
          <div style={{ fontWeight: 800, color: 'var(--gold)', marginBottom: 6 }}>⚠️ 重要：退款與鑑賞期</div>
          <div style={{ fontSize: 13, color: 'var(--tx)', lineHeight: 1.75 }}>
            本平台之城市探索為「特定期間提供之線上活動／數位服務」，<b>一經報名繳費完成即開通參賽資格，恕不退款</b>；
            並依《消費者保護法》及《通訊交易解除權合理例外情事適用準則》，本服務<b>不適用七天鑑賞期</b>（七日內無條件解除權）。
          </div>
        </div>

        <H>退款政策</H>
        <ul style={ul}>
          <li>城市探索賽事屬<b>線上活動／數位服務</b>，於特定期間提供，報名繳費完成即開通參賽資格。</li>
          <li><b>報名繳費完成後恕不退款</b>（含已開通但未實際參賽者）。</li>
          <li>本服務屬「特定日期或期間提供之活動」「一經提供即難以回復之數位內容／線上服務」，依法<b>不適用七天鑑賞期</b>。</li>
          <li><b>例外</b>：若賽事因<b>主辦方因素</b>取消或延期，將另行公告退費、順延或其他處理方式。</li>
          <li>報名相關問題請聯絡 <a href="mailto:info@unityprosper.com" style={link}>info@unityprosper.com</a>。</li>
        </ul>

        <H>服務條款</H>
        <ol style={ol}>
          <li><b>服務內容</b>：DOR 城市探索（www.dor.tw）為線上路跑／挑戰賽事的報名與運動數據追蹤平台，提供賽事報名、運動數據連接（如 Strava）、任務、排行榜與等級系統。</li>
          <li><b>帳號</b>：你應提供正確資料並妥善保管帳號，不得冒用他人身分。</li>
          <li><b>報名與付款</b>：報名須依各賽事規定填寫並完成付款；報名成功即視為同意該賽事規則與本條款。</li>
          <li><b>使用規範</b>：不得以不正當方式（如偽造定位、竄改數據、重複上傳）取得成績或獎勵。本平台得對異常數據進行標記、不予計入或取消資格。</li>
          <li><b>第三方服務</b>：連接 Strava、Google 登入、綠界 ECPay 金流等第三方服務時，亦受其各自條款與政策約束。</li>
          <li><b>智慧財產</b>：平台內容與商標屬本平台或其授權方所有，未經同意不得重製或散布。</li>
          <li><b>免責</b>：賽事成績與排行榜依會員上傳／同步之資料計算，本平台不對裝置或 GPS 誤差負責；服務可能因維護或不可抗力暫停。</li>
          <li><b>服務變更與終止</b>：本平台得隨時調整或終止部分服務，重大變更將公告。</li>
          <li><b>準據法</b>：以中華民國法律為準據法。</li>
        </ol>

        <H>聯絡我們</H>
        <p style={p}><a href="mailto:info@unityprosper.com" style={link}>info@unityprosper.com</a></p>
        <ul style={ul}>
          <li>地址：新北市八里區四維街 13 號 2 樓</li>
          <li>電話：<a href="tel:0933951586" style={link}>0933-951586</a></li>
          <li>統一編號：83005678</li>
        </ul>

        <div style={{ textAlign: 'center', marginTop: 28, fontSize: 12, color: 'var(--tx-faint)' }}>
          DOR · 城市探索　·　<a href="/privacy" style={link}>隱私權政策</a>　·　<a href="/support" style={link}>支援與聯絡</a>
        </div>
      </div>
      </ScrollArea>
    </PhoneFrame>
  )
}

function H({ children }: { children: React.ReactNode }) {
  return <h2 style={{ fontSize: 15, fontWeight: 800, margin: '22px 0 8px', color: 'var(--tx)' }}>{children}</h2>
}

const header: React.CSSProperties = { padding: '14px 18px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid var(--line)' }
const back: React.CSSProperties = { color: 'var(--tx-dim)', fontSize: 14, textDecoration: 'none' }
const body: React.CSSProperties = { maxWidth: 640, margin: '0 auto', padding: '18px 20px 40px' }
const notice: React.CSSProperties = { background: 'rgba(229,196,107,.08)', border: '1px solid rgba(229,196,107,.35)', borderRadius: 12, padding: 14, marginTop: 6 }
const p: React.CSSProperties = { fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.75, margin: '0 0 10px' }
const ul: React.CSSProperties = { fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.85, paddingLeft: 18, margin: '0 0 6px', display: 'flex', flexDirection: 'column', gap: 6 }
const ol: React.CSSProperties = { fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.85, paddingLeft: 18, margin: '0 0 6px', display: 'flex', flexDirection: 'column', gap: 7 }
const link: React.CSSProperties = { color: 'var(--fug)', textDecoration: 'underline' }
