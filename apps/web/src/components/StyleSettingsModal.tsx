'use client'

// 帳號層級「風格設定」選單彈窗（migration 193+194，契約 retro_skin/CONTRACT_R3.md §1、
// docs/skins/CUTE_CONTRACT.md §2）：把 ProfileScreen 個人資料分頁原本並排的風格卡片，收斂成
// 一顆「風格設定」按鈕＋這個選單彈窗。選項一律純文字（不放圖示／emoji），目前選中者右側顯示「✓」。
// 套用邏輯（樂觀更新 SWR 快取＋PUT /me/ui-skin＋失敗回滾）留在 ProfileScreen.chooseSkin()，本檔只
// 負責呈現與把點擊轉呼叫出去，不重複那段邏輯。
//
// 掛載慣例比照 RaceRankingScreen 的底部選單（overlayMount portal＋.phone-shell 自動框住桌機、z 3000、
// 底部彈出）；顏色一律用 token（var(--bg-1)/var(--tx)/…），四種 skin（default/scifi/retro/cute）下
// 都要可讀——因此刻意不像 RewardGrantedModal/UpgradeVipModal 那樣強制 data-skin="default"。
import { createPortal } from 'react-dom'
import { overlayMount } from '@/lib/overlayMount'

export type SkinKey = 'default' | 'scifi' | 'retro' | 'cute'

// 供 ProfileScreen 的「風格設定」按鈕顯示「目前風格名稱」使用，與下方選項共用同一份文案來源。
export const SKIN_LABEL: Record<SkinKey, string> = {
  default: '預設風格',
  scifi: '未來科技',
  retro: '復古 RPG',
  cute: '溫馨可愛',
}

const OPTIONS: { key: SkinKey; desc: string }[] = [
  { key: 'default', desc: '網站原本的外觀。' },
  { key: 'scifi', desc: '深空霓虹主題，全站粒子背景、GPS 跑步頁改為 3D 發光城市地圖。' },
  { key: 'retro', desc: '復古 RPG 大地圖：羊皮紙選單、像素大地圖與魔法光點。' },
  { key: 'cute', desc: '粉嫩手繪風、圓體字，跑步時化身閃亮小光點。' },
]

export default function StyleSettingsModal({ current, busy, err, onChoose, onClose }: {
  current: SkinKey
  busy: boolean
  err: string
  onChoose: (skin: SkinKey) => void
  onClose: () => void
}) {
  // 掛載點：手機模擬框內→portal 進框(桌機不鋪滿視窗)；獨立路由(無手機框)→退回 document.body(視窗)
  const om = overlayMount()
  const content = (
    <div
      onClick={onClose}
      style={{
        position: om.position, inset: 0, zIndex: 3000, height: 'max(100dvh, var(--app-h, 0px))',
        background: 'rgba(0,0,0,.6)', display: 'flex', alignItems: 'flex-end', justifyContent: 'center',
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          width: '100%', maxWidth: 460, maxHeight: '82dvh', overflow: 'hidden', display: 'flex',
          flexDirection: 'column', background: 'var(--bg-1)', borderRadius: '18px 18px 0 0',
          border: '1px solid var(--line-2)', borderBottom: 'none',
        }}
      >
        <div style={{ padding: '14px 18px 10px', borderBottom: '1px solid var(--line)', flexShrink: 0 }}>
          <div style={{ fontSize: 15, fontWeight: 900, color: 'var(--tx)' }}>風格設定</div>
        </div>

        <div style={{ overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', padding: 14, display: 'flex', flexDirection: 'column', gap: 8 }}>
          {OPTIONS.map((o) => {
            const selected = current === o.key
            return (
              <button
                key={o.key}
                type="button"
                disabled={busy}
                onClick={() => onChoose(o.key)}
                style={{
                  display: 'flex', alignItems: 'center', gap: 10, width: '100%', textAlign: 'left',
                  cursor: busy ? 'default' : 'pointer', background: selected ? 'var(--bg-2)' : 'transparent',
                  border: `1px solid ${selected ? 'var(--fug)' : 'var(--line-2)'}`, borderRadius: 10,
                  padding: '12px 14px', fontFamily: 'inherit', opacity: busy ? 0.6 : 1,
                }}
              >
                <span style={{ flex: 1, minWidth: 0 }}>
                  <span style={{ display: 'block', fontSize: 13.5, fontWeight: 700, color: 'var(--tx)' }}>{SKIN_LABEL[o.key]}</span>
                  <span style={{ display: 'block', fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 2, lineHeight: 1.5 }}>{o.desc}</span>
                </span>
                {selected && <span style={{ flexShrink: 0, fontSize: 15, fontWeight: 900, color: 'var(--fug)' }}>✓</span>}
              </button>
            )
          })}
          {err && <div style={{ fontSize: 12, color: 'var(--hunt)' }}>{err}</div>}
        </div>

        <div style={{ padding: 14, borderTop: '1px solid var(--line)', flexShrink: 0 }}>
          <button
            type="button"
            onClick={onClose}
            style={{
              width: '100%', background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10,
              padding: '12px', color: 'var(--tx)', fontSize: 13.5, fontWeight: 700, cursor: 'pointer', fontFamily: 'inherit',
            }}
          >
            關閉
          </button>
        </div>
      </div>
    </div>
  )
  return om.node ? createPortal(content, om.node) : content
}
