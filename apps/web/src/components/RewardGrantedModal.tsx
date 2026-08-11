'use client'

// 活動獎勵系統 P3：完成挑戰即得獎勵演出彈窗。掛在 RaceDetailScreen 的 personal-progress SWR（pp.newly_granted），
// 只有「這次呼叫剛好把 attempt 判定為完成」時後端才會回非空陣列，之後 revalidate 一律為空，故天然只跳一次
// （見 RaceDetailScreen 對應 useEffect：依賴 pp?.newly_granted 陣列參照變動才觸發，不需要額外 localStorage 記錄）。
// 樣式比照 ExpSettlementModal 的深色慶祝卡片，但不做逐項演出動畫（獎勵種類少、一次性列出即可）。
import { createPortal } from 'react-dom'
import type { GrantedReward } from '@/lib/api'
import { overlayMount } from '@/lib/overlayMount'
import DpCoin from './DpCoin'
import GpCoin from './GpCoin'

export default function RewardGrantedModal({ rewards, onClose }: { rewards: GrantedReward[]; onClose: () => void }) {
  // 掛載點：手機模擬框內→portal 進框(桌機不鋪滿視窗)；獨立路由(無手機框)→退回 document.body(視窗)
  const om = overlayMount()
  const content = (
    <div data-skin="default" onClick={onClose} style={{ ...overlay, position: om.position }}>
      <div onClick={(e) => e.stopPropagation()} style={panel}>
        <div style={{ textAlign: 'center' }}>
          <div style={{ fontSize: 12, letterSpacing: '.35em', color: 'var(--gold)', fontWeight: 700 }}>CONGRATULATIONS</div>
          <div style={{ fontSize: 22, fontWeight: 900, color: '#fff', margin: '4px 0 2px' }}>🎉 恭喜完成挑戰！</div>
          <div style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>獲得以下獎勵</div>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 16 }}>
          {rewards.map((r, i) => <RewardRow key={i} r={r} />)}
        </div>

        <button onClick={onClose} style={closeBtn}>太棒了！</button>
      </div>
    </div>
  )
  return om.node ? createPortal(content, om.node) : content
}

function RewardRow({ r }: { r: GrantedReward }) {
  return (
    <div style={row}>
      {r.type === 'exp' && <span style={{ fontWeight: 900, color: 'var(--fug)' }}>⭐ +{r.amount} EXP</span>}
      {r.type === 'dp' && (
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontWeight: 900, color: 'var(--gold)' }}>
          <DpCoin size={16} />+{r.amount} DP
        </span>
      )}
      {r.type === 'gp' && (
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontWeight: 900, color: 'var(--violet)' }}>
          <GpCoin size={16} />+{r.amount} GP
        </span>
      )}
      {r.type === 'vip' && <span style={{ fontWeight: 900, color: 'var(--gold)' }}>✦ VIP +{r.days} 天</span>}
      {r.type === 'serial' && (
        <span style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', justifyContent: 'center' }}>
          <span style={{ fontWeight: 800, color: '#fff' }}>🎁 {r.item_label || '活動獎勵'}</span>
          <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>已放入活動獎勵</span>
        </span>
      )}
    </div>
  )
}

// z-index 比照 ExpSettlementModal（3300），高於可拖曳資訊面板(500)與大多數覆蓋層
const overlay: React.CSSProperties = { position: 'fixed', inset: 0, zIndex: 3300, background: 'rgba(4,8,6,.82)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 18 }
const panel: React.CSSProperties = { width: '100%', maxWidth: 380, background: 'rgba(10,14,12,.92)', border: '1px solid rgba(229,196,107,.35)', borderRadius: 18, padding: '24px 20px 20px', boxShadow: '0 20px 80px rgba(0,0,0,.6)' }
const row: React.CSSProperties = { display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'rgba(255,255,255,.04)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '11px 14px', fontSize: 14.5 }
const closeBtn: React.CSSProperties = { marginTop: 18, width: '100%', background: 'linear-gradient(135deg,#E5C46B,#caa64e)', color: '#fff', fontWeight: 800, border: 'none', borderRadius: 12, padding: '12px 20px', fontSize: 14.5, cursor: 'pointer', fontFamily: 'inherit' }
