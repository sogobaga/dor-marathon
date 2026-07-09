'use client'

import { useEffect, useState } from 'react'
import { profileApi, type VipPricing } from '@/lib/api'
import { getUserToken, withUserAuth } from '@/lib/userAuth'

// 升級 VIP 視窗：權益 + 月/年方案(原價刪除線 + 綠色優惠價 + 現省) + 促銷期限 + 取消訂閱/退款條款 + 公司資訊。
// expired=true：因試用到期自動跳出（標題改為「VIP 試用已到期」）。
// onSubscribe 未提供時，訂閱鈕顯示「金流整合中」佔位（綠界定期定額於 Phase 4 接上）。
export default function UpgradeVipModal({ expired, onClose, onSubscribe }: {
  expired?: boolean
  onClose: () => void
  onSubscribe?: (plan: 'monthly' | 'annual') => void
}) {
  const [pricing, setPricing] = useState<VipPricing | null>(null)
  const [loadErr, setLoadErr] = useState(false)
  const [notice, setNotice] = useState('')

  useEffect(() => {
    if (!getUserToken()) { setLoadErr(true); return }
    withUserAuth((t) => profileApi.vipPricing(t))
      .then(setPricing)
      .catch(() => setLoadErr(true))
  }, [])

  const ntd = (yuan: number) => `NT$${yuan.toLocaleString()}`
  function subscribe(plan: 'monthly' | 'annual') {
    if (onSubscribe) onSubscribe(plan)
    else setNotice('訂閱付款即將開放（金流整合中），敬請期待！')
  }

  return (
    <div data-skin="default" onClick={onClose} style={{ position: 'fixed', inset: 0, zIndex: 3500, background: 'rgba(4,8,6,.88)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 16 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: '100%', maxWidth: 400, maxHeight: '94dvh', overflowY: 'auto', WebkitOverflowScrolling: 'touch', background: '#0b0e13', border: '1px solid var(--gold)', borderRadius: 18, boxShadow: '0 16px 50px rgba(0,0,0,.7)', padding: '20px 18px 18px' }}>
        {/* 標題 */}
        <div style={{ textAlign: 'center', marginBottom: 6 }}>
          <div style={{ fontSize: 12, letterSpacing: '.3em', color: 'var(--gold)', fontWeight: 800 }}>✦ DOR VIP ✦</div>
          <div style={{ fontSize: 20, fontWeight: 900, color: '#fff', marginTop: 4 }}>{expired ? 'VIP 試用已到期' : '升級 VIP 會員'}</div>
          {expired && <div style={{ fontSize: 13, color: 'var(--tx-dim)', marginTop: 4 }}>是否繼續享有 VIP 專屬權益？</div>}
        </div>

        {/* 權益 */}
        <div style={{ background: 'rgba(255,255,255,.04)', border: '1px solid var(--line-2)', borderRadius: 12, padding: '12px 14px', margin: '12px 0' }}>
          <div style={{ fontSize: 11, letterSpacing: '.2em', color: 'var(--tx-faint)', fontWeight: 800, marginBottom: 8 }}>VIP 專屬權益</div>
          {[
            ['🏆', '解鎖完整個人任務', '全部階段課表任你挑戰'],
            ['🎟️', '每月 3 張 100 元活動優惠券', '報名活動直接折抵，每月自動補齊'],
            ['🔒', '解鎖 VIP 限定活動', '搶先報名 VIP 專屬賽事'],
          ].map(([ico, t, d]) => (
            <div key={t} style={{ display: 'flex', gap: 10, alignItems: 'flex-start', padding: '5px 0' }}>
              <span style={{ fontSize: 17, lineHeight: 1.4 }}>{ico}</span>
              <div>
                <div style={{ fontSize: 13.5, fontWeight: 700, color: 'var(--tx)' }}>{t}</div>
                <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 1 }}>{d}</div>
              </div>
            </div>
          ))}
        </div>

        {/* 方案 */}
        {loadErr ? (
          <div style={{ fontSize: 13, color: 'var(--hunt)', textAlign: 'center', padding: '10px 0' }}>方案載入失敗，請稍後再試</div>
        ) : !pricing ? (
          <div style={{ fontSize: 13, color: 'var(--tx-faint)', textAlign: 'center', padding: '10px 0' }}>載入方案中…</div>
        ) : (
          <>
            {pricing.in_promo_window && (
              <div style={{ fontSize: 12.5, color: '#ffcf6b', background: 'rgba(231,184,75,.12)', border: '1px solid rgba(231,184,75,.35)', borderRadius: 10, padding: '8px 11px', marginBottom: 10, lineHeight: 1.6, textAlign: 'center' }}>
                🎉 限時優惠中！{pricing.promo_ends_at ? `${pricing.promo_ends_at.slice(0, 10)} 後恢復原價` : '把握機會'}
              </div>
            )}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              <PlanCard label="月繳方案" unit="/ 月" p={pricing.monthly} ntd={ntd} onSub={() => subscribe('monthly')} />
              <PlanCard label="年繳方案" unit="/ 年" p={pricing.annual} ntd={ntd} highlight onSub={() => subscribe('annual')} />
            </div>
          </>
        )}

        {notice && <div style={{ fontSize: 12.5, color: 'var(--fug)', textAlign: 'center', marginTop: 12, fontWeight: 700 }}>{notice}</div>}

        {/* 取消訂閱 / 退款條款 */}
        <div style={{ marginTop: 16, fontSize: 11, color: 'var(--tx-faint)', lineHeight: 1.75 }}>
          <div style={{ fontWeight: 800, color: 'var(--tx-dim)', marginBottom: 3 }}>訂閱與取消說明</div>
          <div>· 訂閱採綠界「定期定額」自動續扣（月繳每月／年繳每年）。可隨時取消，取消後不再續扣。</div>
          <div>· 取消或扣款失敗後，VIP 權益維持至<b>當期到期日</b>，到期即自動降為一般會員（VIP 限定功能重新上鎖）。</div>
          <div>· 本服務為數位服務，<b>恕不退費</b>（已扣款期數不退款，VIP 時間到期為止）。</div>
          <div style={{ marginTop: 4 }}>詳見 <a href="/terms" target="_blank" rel="noreferrer" style={{ color: 'var(--fug)' }}>服務條款</a> · <a href="/privacy" target="_blank" rel="noreferrer" style={{ color: 'var(--fug)' }}>隱私權政策</a></div>
        </div>

        {/* 公司資訊 */}
        <div style={{ marginTop: 12, paddingTop: 10, borderTop: '1px solid var(--line)', fontSize: 11, color: 'var(--tx-faint)', lineHeight: 1.8 }}>
          <div>客服信箱：<a href="mailto:info@unityprosper.com" style={{ color: 'var(--fug)' }}>info@unityprosper.com</a></div>
          <div>地址：新北市八里區四維街 13 號 2 樓</div>
          <div>電話：<a href="tel:0933951586" style={{ color: 'var(--fug)' }}>0933-951586</a>　·　統一編號：83005678</div>
        </div>

        <button onClick={onClose} style={{ marginTop: 14, width: '100%', background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 12, padding: '11px', fontSize: 13.5, cursor: 'pointer', fontFamily: 'inherit' }}>{expired ? '稍後再說' : '關閉'}</button>
      </div>
    </div>
  )
}

function PlanCard({ label, unit, p, ntd, highlight, onSub }: {
  label: string
  unit: string
  p: { original: number; price: number; save: number; promo: boolean }
  ntd: (y: number) => string
  highlight?: boolean
  onSub: () => void
}) {
  return (
    <div style={{ border: `1px solid ${highlight ? 'var(--gold)' : 'var(--line-2)'}`, borderRadius: 14, padding: '13px 14px', background: highlight ? 'rgba(231,184,75,.06)' : 'transparent' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div>
          <div style={{ fontSize: 13.5, fontWeight: 800, color: 'var(--tx)' }}>{label}{highlight && <span style={{ fontSize: 10.5, color: 'var(--gold)', marginLeft: 6, fontWeight: 800 }}>最超值</span>}</div>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginTop: 6, flexWrap: 'wrap' }}>
            {p.promo && <span style={{ fontSize: 14, color: 'var(--tx-faint)', textDecoration: 'line-through' }}>{ntd(p.original)}</span>}
            <span style={{ fontSize: 26, fontWeight: 900, color: '#46E3A0', lineHeight: 1 }}>{ntd(p.price)}</span>
            <span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>{unit}</span>
          </div>
          {p.promo && p.save > 0 && <div style={{ fontSize: 12, color: '#46E3A0', fontWeight: 700, marginTop: 4 }}>現省 {ntd(p.save)}</div>}
        </div>
      </div>
      <button onClick={onSub} style={{ marginTop: 12, width: '100%', background: 'var(--gold)', color: '#fff', fontWeight: 900, border: 'none', borderRadius: 10, padding: '11px', fontSize: 14.5, cursor: 'pointer', fontFamily: 'inherit' }}>立即訂閱 · {ntd(p.price)}{unit}</button>
    </div>
  )
}
