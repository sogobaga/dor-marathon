'use client'

import { useEffect, useState } from 'react'
import { profileApi } from '@/lib/api'
import { getUserToken, withUserAuth } from '@/lib/userAuth'
import { loadEcpaySdk, type EcpayServerType } from '@/lib/ecpay'

const PLAN_LABEL: Record<'monthly' | 'annual', string> = { monthly: 'VIP 月費訂閱', annual: 'VIP 年費訂閱' }

function ntd(cents: number) {
  return `NT$${Math.round(cents / 100).toLocaleString()}`
}

type Stage = 'loading' | 'ready' | 'confirming' | 'success' | 'redirecting' | 'error'

// 綠界站內付 2.0 綁卡視窗（VIP 訂閱 Phase E）。inline position:fixed 全屏彈窗——比照站內既有彈窗慣例
// （UpgradeVipModal／CancelSubscriptionModal），不 portal 到 body，會被桌機 .phone-shell 模擬框自動包住。
//
// 流程：loadEcpaySdk（jQuery→node-forge→SDK，見 lib/ecpay.ts）→ ECPay.initialize(serverType) →
// ECPay.addBindingCard(token) 把卡號輸入介面渲染進 <div id="ECPayPayment">（固定 id，SDK 硬編碼，
// 不可更動）→ 使用者填卡後按「確認綁卡付款」→ ECPay.getBindCardPayToken 取得 BindCardPayToken →
// 打我方 complete API：paid（成功，顯示卡末四碼）｜3d_required（整頁導轉 three_d_url，官方明示勿用
// iframe，3D 完成後綠界會 302 導回 `/?vip_bind=success|fail`，見 PhoneShell 的 query 處理）。
//
// retryKey 是唯一會重新觸發「載入 SDK→initialize→addBindingCard」整段流程的依據（見下方 useEffect
// deps）：serverType/token 在同一次訂閱流程中不會變、一般轉譯不會誤觸發重複 initialize；使用者按
// 「重試」才遞增 retryKey——這與官方範例「切換語系時重新呼叫 addBindingCard 更新同一個 div」是
// 同一種被支援的用法（重呼叫 initialize/addBindingCard 本身是安全的），不是繞過限制的取巧寫法。
export default function BindCardModal({ plan, amountCents, token, orderId, serverType, onClose, onSuccess }: {
  plan: 'monthly' | 'annual'
  amountCents: number
  token: string
  orderId: string
  serverType: EcpayServerType
  onClose: () => void
  onSuccess: () => void
}) {
  const [stage, setStage] = useState<Stage>('loading')
  const [errMsg, setErrMsg] = useState('')
  const [cardLast4, setCardLast4] = useState('')
  const [retryKey, setRetryKey] = useState(0)

  useEffect(() => {
    let cancelled = false
    setStage('loading')
    setErrMsg('')

    loadEcpaySdk(serverType)
      // 官方已知陷阱：容器剛掛載的同一 tick 呼叫 initialize，瀏覽器可能尚未完成 repaint、
      // #ECPayPayment 尺寸仍為 0 導致 SDK 略過渲染；雙層 rAF 延後一個畫面週期是官方建議解法。
      .then(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
      .then(() => new Promise<void>((resolve, reject) => {
        const ECPay = window.ECPay
        if (!ECPay) { reject(new Error('綠界 SDK 載入失敗')); return }
        ECPay.initialize(serverType, 1, (msg) => { if (msg != null) reject(new Error(msg)); else resolve() })
      }))
      .then(() => new Promise<void>((resolve, reject) => {
        const ECPay = window.ECPay!
        ECPay.addBindingCard(token, ECPay.Language?.zhTW ?? 'zh-TW', (msg) => { if (msg != null) reject(new Error(msg)); else resolve() })
      }))
      .then(() => { if (!cancelled) setStage('ready') })
      .catch((e) => { if (!cancelled) { setStage('error'); setErrMsg(e?.message || '綁卡介面載入失敗，請重試') } })

    return () => { cancelled = true }
  }, [serverType, token, retryKey])

  function confirmPay() {
    const ECPay = window.ECPay
    if (!ECPay) { setErrMsg('綠界 SDK 尚未就緒'); return }
    setStage('confirming')
    setErrMsg('')
    ECPay.getBindCardPayToken(async (paymentInfo, msg) => {
      if (msg != null || !paymentInfo?.BindCardPayToken) {
        setStage('ready')
        setErrMsg(msg || '請完整填寫卡片資訊')
        return
      }
      if (!getUserToken()) { setStage('ready'); setErrMsg('登入已過期，請重新登入'); return }
      try {
        const res = await withUserAuth((t) => profileApi.vipBindComplete(t, paymentInfo.BindCardPayToken, orderId))
        if (res.status === 'paid') {
          setCardLast4(res.card_last4 || '')
          setStage('success')
        } else if (res.status === '3d_required' && res.three_d_url) {
          setStage('redirecting')
          window.location.href = res.three_d_url // 官方明示：整頁導轉，勿用 iframe
        } else {
          setStage('ready')
          setErrMsg('付款結果異常，請重試')
        }
      } catch (e: any) {
        setStage('ready')
        setErrMsg(e?.message || '綁卡付款失敗，請重試')
      }
    })
  }

  const closable = stage === 'ready' || stage === 'loading' || stage === 'error'

  return (
    <div data-skin="default" onClick={() => { if (closable) onClose() }} style={overlay}>
      <div onClick={(e) => e.stopPropagation()} style={card}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <div style={{ fontSize: 17, fontWeight: 900, color: '#fff' }}>{PLAN_LABEL[plan]}</div>
          {closable && <button onClick={onClose} style={closeBtn}>✕</button>}
        </div>

        {stage === 'success' ? (
          <>
            <div style={{ textAlign: 'center', padding: '18px 0 6px' }}>
              <div style={{ fontSize: 36 }}>✓</div>
              <div style={{ fontSize: 16, fontWeight: 800, color: 'var(--gold)', marginTop: 6 }}>VIP 已生效</div>
              {cardLast4 && <div style={{ fontSize: 13, color: 'var(--tx-dim)', marginTop: 8 }}>付款卡片：💳 **** {cardLast4}</div>}
            </div>
            <button onClick={onSuccess} style={primaryBtn}>完成</button>
          </>
        ) : (
          <>
            <div style={{ fontSize: 13, color: 'var(--tx-dim)', marginTop: 4 }}>
              應繳金額 <b style={{ color: 'var(--gold)' }}>{ntd(amountCents)}</b>（訂單編號 {orderId.slice(0, 8)}）
            </div>

            {stage === 'loading' && (
              <div style={{ padding: '30px 0', textAlign: 'center', color: 'var(--tx-faint)', fontSize: 13 }}>載入綠界安全付款介面中…</div>
            )}

            {stage === 'error' && (
              <div style={{ padding: '16px 0', textAlign: 'center' }}>
                <div style={{ color: 'var(--hunt)', fontSize: 13 }}>{errMsg}</div>
                <button onClick={() => setRetryKey((k) => k + 1)} style={{ ...ghostBtn, marginTop: 10 }}>重試</button>
              </div>
            )}

            {/* 綠界渲染卡號輸入介面的固定容器（id 不可更動，SDK 硬編碼）；非 ready/confirming 時隱藏但保留掛載，
                避免容器整個從 DOM 移除又重建，讓 SDK 的量測/事件綁定失效。 */}
            <div
              id="ECPayPayment"
              style={{
                display: stage === 'ready' || stage === 'confirming' ? 'block' : 'none',
                minHeight: stage === 'ready' || stage === 'confirming' ? 380 : 0,
                maxHeight: 420, overflowY: 'auto', margin: '12px 0',
              }}
            />

            {stage === 'ready' && errMsg && <div style={{ color: 'var(--hunt)', fontSize: 12.5, marginBottom: 8 }}>{errMsg}</div>}

            {(stage === 'ready' || stage === 'confirming') && (
              <button onClick={confirmPay} disabled={stage === 'confirming'} style={{ ...primaryBtn, opacity: stage === 'confirming' ? 0.6 : 1 }}>
                {stage === 'confirming' ? '處理中…' : `確認綁卡付款 · ${ntd(amountCents)}`}
              </button>
            )}
            {stage === 'redirecting' && (
              <div style={{ padding: '20px 0', textAlign: 'center', color: 'var(--tx-dim)', fontSize: 13 }}>導向 3D 驗證頁面…</div>
            )}

            {(stage === 'ready' || stage === 'error') && (
              <button onClick={onClose} style={{ ...ghostBtn, width: '100%', marginTop: 8 }}>取消</button>
            )}
          </>
        )}
      </div>
    </div>
  )
}

const overlay: React.CSSProperties = { position: 'fixed', inset: 0, zIndex: 3600, background: 'rgba(4,8,6,.9)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 16 }
const card: React.CSSProperties = { width: '100%', maxWidth: 400, maxHeight: '94dvh', overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', background: '#0b0e13', border: '1px solid var(--gold)', borderRadius: 18, boxShadow: '0 16px 50px rgba(0,0,0,.7)', padding: '18px 16px' }
const closeBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', fontSize: 16, cursor: 'pointer', padding: 4 }
const primaryBtn: React.CSSProperties = { marginTop: 12, width: '100%', background: 'var(--gold)', color: '#fff', fontWeight: 900, border: 'none', borderRadius: 12, padding: '13px', fontSize: 14.5, cursor: 'pointer', fontFamily: 'inherit' }
const ghostBtn: React.CSSProperties = { background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 11, padding: '10px', fontSize: 13, cursor: 'pointer', fontFamily: 'inherit' }
