'use client'

// Terra／Strava 串接結束公告橫幅（個人頁「運動數據」分頁，已連接者看到）。文案與狀態見 lib/wearableSunset.ts。
// 半透明琥珀底＋一般文字色（專案規則：只有金黃「實心」底才強制白字；與同頁既有的授權提醒橫幅同一個樣式）。
// 內容一律是純文字節點（React 會轉義），不經任何 HTML 注入路徑。

export default function SunsetBanner({ title, lines }: { title: string; lines: string[] }) {
  return (
    <div
      role="status"
      data-sunset-banner=""
      style={{
        fontSize: 11.5, color: 'var(--tx)', background: 'rgba(245,158,11,.14)', border: '1px solid rgba(245,158,11,.35)',
        borderRadius: 8, padding: '8px 10px', marginBottom: 12, lineHeight: 1.6,
      }}
    >
      <div style={{ fontWeight: 800, fontSize: 12.5 }}>{title}</div>
      {lines.map((l, i) => (
        <div key={i} style={{ marginTop: 4 }}>{l}</div>
      ))}
    </div>
  )
}
