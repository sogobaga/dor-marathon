'use client'

import { useCallback, useEffect, useState } from 'react'
import useSWR, { mutate } from 'swr'
import { runMeetApi, type RunMeetCard, type RunMeetDetail, type RunMeetMemberDetail } from '@/lib/api'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'
import { useVipSubscribeFlow } from '@/lib/useVipSubscribeFlow'
import { fmtMeetRange, createBtnText, createGate, runMeetLocationText, CREATE_GATE_WAIT_TEXT } from '@/lib/runMeet'
import UpgradeVipModal from './UpgradeVipModal'
import BindCardModal from './BindCardModal'
import RunMeetListView, { EMPTY_FILTERS, type NearPos, type NearState, type RunMeetFilters } from './runmeet/RunMeetListView'
import RunMeetMineView from './runmeet/RunMeetMineView'
import RunMeetDetailView from './runmeet/RunMeetDetailView'
import RunMeetFormModal from './runmeet/RunMeetFormModal'
import { LoginModal } from './UserAuthBar'
import {
  RunMeetModal, Toast, backBtn, chip, chipActive, ghostBtn, goldPill, headerStyle,
  modalSubText, modalTitle, primaryBtn, scrollBody,
} from './runmeet/ui'

// 團練邀請（父容器）。列表 ⇄ 詳情是同一個 Screen 內的 selectedId 切換，不是 Next.js 路由；
// 篩選、搜尋字、附近座標、tab 這些「跨畫面要存活」的 state 一律放這層——子元件切換時會被卸載
// （與 PartnerPerksScreen 的 favOverride 同樣的理由）。
//
// ⚠️ 入口 gate 由 dashboard 的 runmeet_entry 控制，但後端 Router 第一行也有 requireEntry，
//    非白名單直接 403（前端隱藏 ≠ 後端有擋）。
export default function RunMeetScreen({ onBack, initialMeetId }: { onBack: () => void; initialMeetId?: string }) {
  const user = useUser()
  const uid = user?.id ?? 'guest'
  const [tab, setTab] = useState<'explore' | 'mine'>('explore')
  const [selected, setSelected] = useState<{ id: string; card: RunMeetCard | null } | null>(
    initialMeetId ? { id: initialMeetId, card: null } : null,
  )
  const [filters, setFilters] = useState<RunMeetFilters>(EMPTY_FILTERS)
  const [near, setNear] = useState<NearPos | null>(null)
  const [nearState, setNearState] = useState<NearState>('idle')
  const [showCreate, setShowCreate] = useState(false)
  // 「再辦一次」：帶著要複製的原團資料（owner 視角、含成員層地點）；非 null 時開複製建立表單。
  const [duplicateSeed, setDuplicateSeed] = useState<RunMeetMemberDetail | null>(null)
  const [vipModal, setVipModal] = useState(false)
  const [created, setCreated] = useState<{ meet: RunMeetDetail; password?: string } | null>(null)
  // 未登入深連結（分享卡「登入並加入這場團練」點過來）：直接在原地跳登入視窗，而不是把人
  // 送回首頁再讓他自己找登入按鈕——登入成功後 useUser() 會重新渲染這個畫面，loggedIn 轉真，
  // 直接落在這個團練的詳情，不用使用者自己再找路回來。見下方 !loggedIn 分支。
  const [showLogin, setShowLogin] = useState(false)
  const [toast, setToast] = useState<{ text: string; tone: 'ok' | 'err' } | null>(null)
  const vipFlow = useVipSubscribeFlow()

  const showToast = useCallback((text: string, tone: 'ok' | 'err' = 'ok') => setToast({ text, tone }), [])
  useEffect(() => {
    if (!toast) return
    const t = setTimeout(() => setToast(null), 3200)
    return () => clearTimeout(t)
  }, [toast])

  const { data: quota, error: quotaErr, mutate: reloadQuota } = useSWR(
    getUserToken() ? ['run-meet-quota', uid] : null,
    () => withUserAuth((t) => runMeetApi.quota(t)),
    { shouldRetryOnError: false },
  )
  // 入口 gate：後端 Router 第一行就有 requireEntry，未開放一律 403（?runmeet= 深連結也擋得住）。
  // 這裡把它渲染成明確說明，而不是讓每個子查詢各自顯示「載入失敗」。
  const gated = (quotaErr as any)?.status === 403

  // ⚠️ 未登入必須自己擋（比照 MonopolyScreen「請先登入才能遊玩」）。
  // 分享文案帶的是 {origin}/?runmeet={id}，收到連結的人絕大多數沒登入；
  // 沒有這段的話所有 SWR key 都是 null（不會發請求、也拿不到 403），
  // 詳情頁會落到「找不到這個團練，可能已被發起人刪除。」——把主要傳播動線變成假的錯誤訊息。
  const loggedIn = Boolean(getUserToken())

  // 列表/我的/配額一起失效（建立、加入、退出、審核之後都要）
  const refreshLists = useCallback(() => {
    void reloadQuota()
    void mutate((key) => Array.isArray(key) && (key[0] === 'run-meets' || key[0] === 'run-meet-mine'))
  }, [reloadQuota])

  // 「附近的團練」：取瀏覽器定位。未授權時以引導文案取代（不可報錯）。
  // ⚠️ 座標只當查詢參數傳給後端（後端不寫 DB、不進 log），也不存在本地。
  function toggleNear() {
    if (nearState === 'on') { setNearState('idle'); setNear(null); return }
    if (typeof navigator === 'undefined' || !navigator.geolocation) { setNearState('denied'); return }
    setNearState('asking')
    navigator.geolocation.getCurrentPosition(
      (pos) => {
        setNear({ lat: Number(pos.coords.latitude.toFixed(6)), lng: Number(pos.coords.longitude.toFixed(6)), radiusKm: 10 })
        setNearState('on')
      },
      () => setNearState('denied'),
      { enableHighAccuracy: false, timeout: 8000, maximumAge: 300000 },
    )
  }

  // 開表單前的配額/VIP 閘門：'ok' 直接開、'vip' 跳升級引導（非 VIP 或政策要求 VIP）、
  // 'wait' 是 VIP 本月次數也用完（無解法，只能提示等下個月，不可再跳升級引導那是死路）。
  // 「再辦一次」（openDuplicate）與「＋ 發起團練」（openCreate）共用同一支 createGate，
  // 任何一邊改判定邏輯都不會漏改另一邊——不可讓使用者填完整張表才在送出時被擋。
  function openCreate() {
    if (!quota) return
    const gate = createGate(quota)
    if (gate === 'vip') { setVipModal(true); return }
    if (gate === 'wait') { showToast(CREATE_GATE_WAIT_TEXT, 'err'); return }
    setShowCreate(true)
  }

  // 「再辦一次」：複製既有團練設定建立新團練，走的是同一個建立流程（正常消耗一次發起次數），
  // 所以開表單前要過與「＋ 發起團練」完全一樣的閘門。
  function openDuplicate(meet: RunMeetMemberDetail) {
    if (!quota) return
    const gate = createGate(quota)
    if (gate === 'vip') { setVipModal(true); return }
    if (gate === 'wait') { showToast(CREATE_GATE_WAIT_TEXT, 'err'); return }
    setDuplicateSeed(meet)
  }

  // 建立成功共用收尾（「＋ 發起團練」與「再辦一次」都走這裡）：關掉對應的表單、刷新列表/配額、
  // 顯示一次性分享文案彈窗。
  function handleCreated(meet: RunMeetDetail, info: { created: boolean; remaining?: number; pendingKept?: number; password?: string }) {
    setShowCreate(false)
    setDuplicateSeed(null)
    refreshLists()
    setCreated({ meet, password: info.password })
    showToast(`團練建立完成！本月還剩 ${info.remaining ?? 0} 次`)
  }

  if (!loggedIn) {
    return (
      <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
        <header style={headerStyle}>
          <button onClick={onBack} style={backBtn}>← 返回</button>
          <span style={{ flex: 1, fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>🤝 團練邀請</span>
        </header>
        <div style={{ ...scrollBody, textAlign: 'center', paddingTop: 60 }}>
          <div style={{ fontSize: 34 }}>🔒</div>
          <div style={{ fontSize: 15, fontWeight: 900, color: 'var(--tx)', marginTop: 10 }}>
            {initialMeetId ? '登入就能查看這場團練' : '登入後就能揪團練、跟大家一起跑'}
          </div>
          <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 8, lineHeight: 1.8 }}>
            {initialMeetId
              ? '登入後就能看到完整集合時間地點、目前有誰要來，還能直接留言或按讚，一分鐘內搞定。'
              : '登入後就能看到集合時間地點，直接加入別人辦的團練，或自己發起一場。'}
          </div>
          <button onClick={() => setShowLogin(true)} style={{ ...primaryBtn, width: 'auto', padding: '9px 22px', marginTop: 18 }}>
            {initialMeetId ? '登入查看團練' : '登入'}
          </button>
        </div>
        {showLogin && <LoginModal onClose={() => setShowLogin(false)} />}
      </div>
    )
  }

  if (gated) {
    return (
      <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
        <header style={headerStyle}>
          <button onClick={onBack} style={backBtn}>← 返回</button>
          <span style={{ flex: 1, fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>🤝 團練邀請</span>
        </header>
        <div style={{ ...scrollBody, textAlign: 'center', paddingTop: 60 }}>
          <div style={{ fontSize: 34 }}>🚧</div>
          <div style={{ fontSize: 15, fontWeight: 900, color: 'var(--tx)', marginTop: 10 }}>團練邀請尚未開放</div>
          <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 8, lineHeight: 1.8 }}>這個功能還在測試中，開放後會在會員頁看到入口。</div>
        </div>
      </div>
    )
  }

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)', position: 'relative' }}>
      {selected ? (
        <RunMeetDetailView
          id={selected.id}
          fallbackCard={selected.card}
          quota={quota ?? null}
          onBack={() => { setSelected(null); refreshLists() }}
          // 團練已被刪除的倒數導頁要切回「首頁」，不是這個 Screen 內部的列表——沿用本 Screen
          // 自己收到的 onBack（PhoneShell 傳進來的，會整個關掉團練邀請畫面回到首頁）。
          onToast={showToast}
          onChanged={refreshLists}
          onLearnVip={() => setVipModal(true)}
          onDuplicate={openDuplicate}
        />
      ) : (
        <>
          <header style={headerStyle}>
            <button onClick={onBack} style={backBtn}>← 返回</button>
            <span style={{ flex: 1, fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>🤝 團練邀請</span>
          </header>

          <div style={{ display: 'flex', gap: 8, padding: '10px 16px 0' }}>
            <button onClick={() => setTab('explore')} style={tab === 'explore' ? chipActive : chip}>探索</button>
            <button onClick={() => setTab('mine')} style={tab === 'mine' ? chipActive : chip}>我的</button>
            <span style={{ flex: 1 }} />
            <button onClick={openCreate} style={{ ...primaryBtn, width: 'auto', padding: '6px 14px', fontSize: 13 }}>{createBtnText(quota?.remaining ?? 0, true)}</button>
          </div>

          <div style={scrollBody}>
            {tab === 'explore' ? (
              <RunMeetListView
                filters={filters}
                setFilters={setFilters}
                near={near}
                nearState={nearState}
                onToggleNear={toggleNear}
                onOpen={(m) => setSelected({ id: m.id, card: m })}
                onCreate={openCreate}
              />
            ) : (
              <RunMeetMineView
                onOpen={(m) => setSelected({ id: m.id, card: m })}
                onCreate={openCreate}
                onGoExplore={() => setTab('explore')}
                remaining={quota?.remaining ?? 0}
              />
            )}
          </div>
        </>
      )}

      {showCreate && quota && (
        <RunMeetFormModal
          mode="create"
          quota={quota}
          onClose={() => setShowCreate(false)}
          onSaved={handleCreated}
          onLearnVip={() => setVipModal(true)}
        />
      )}

      {/* 「再辦一次」：複製 duplicateSeed（原團完整資料）預填的建立表單——不預填時間與密碼
          （見 RunMeetFormModal 檔頭說明），送出後走同一套 handleCreated 收尾。 */}
      {duplicateSeed && quota && (
        <RunMeetFormModal
          mode="create"
          initial={duplicateSeed}
          quota={quota}
          onClose={() => setDuplicateSeed(null)}
          onSaved={handleCreated}
          onLearnVip={() => setVipModal(true)}
        />
      )}

      {/* 建立成功：一次性分享文案（密碼來自表單記憶體，任何 API 都不會回讀） */}
      {created && (
        <RunMeetModal onClose={() => setCreated(null)} maxWidth={360}>
          <div style={modalTitle}>團練建立完成 🎉</div>
          <div style={modalSubText}>
            把下面這段貼到群組或社群，揪人一起跑吧！
            {created.password && <><br /><b style={{ color: 'var(--gold)' }}>密碼只會顯示這一次</b>，請先複製保存。</>}
          </div>
          <pre style={{ marginTop: 12, background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '10px 12px', fontSize: 12.5, color: 'var(--tx)', whiteSpace: 'pre-wrap', wordBreak: 'break-all', fontFamily: 'inherit', lineHeight: 1.7 }}>
            {shareText(created.meet, created.password)}
          </pre>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginTop: 16 }}>
            <button
              onClick={() => {
                navigator.clipboard?.writeText(shareText(created.meet, created.password)).then(
                  () => showToast('連結已複製，快去揪人吧！'),
                  () => showToast('複製失敗，請手動複製', 'err'),
                )
              }}
              style={{ ...ghostBtn, width: '100%', padding: '11px 0' }}
            >複製文案</button>
            <button onClick={() => { const m = created.meet; setCreated(null); setSelected({ id: m.id, card: m }) }} style={primaryBtn}>看看我的團練</button>
          </div>
        </RunMeetModal>
      )}

      {vipModal && (
        <UpgradeVipModal
          reason={quota
            ? `VIP 會員每月可發起 ${quota.vip_cap} 個團練，並可為每個團練上傳最多 ${quota.vip_image_limit} 張圖片。`
            : 'VIP 會員每月可發起更多團練，並可為每個團練上傳更多張圖片。'}
          onClose={() => setVipModal(false)}
          onSubscribe={vipFlow.subscribe}
          subscribing={vipFlow.busy}
          subscribeError={vipFlow.error}
        />
      )}
      {vipFlow.bindCard && (
        <BindCardModal
          plan={vipFlow.bindCard.plan}
          amountCents={vipFlow.bindCard.amount_cents}
          token={vipFlow.bindCard.token}
          orderId={vipFlow.bindCard.order_id}
          serverType={vipFlow.bindCard.server_type}
          onClose={vipFlow.closeBindCard}
          onSuccess={() => { vipFlow.handleBindSuccess(); setVipModal(false); void reloadQuota() }}
        />
      )}

      {toast && <Toast text={toast.text} tone={toast.tone} />}
    </div>
  )
}

// 一次性分享文案（規格 1.4）：標題｜時間｜地點 + 密碼 + 深連結。
function shareText(meet: RunMeetDetail, password?: string): string {
  const origin = typeof window !== 'undefined' ? window.location.origin : 'https://www.dor.tw'
  const lines = [`🏃 ${meet.title}｜${fmtMeetRange(meet.meet_at, meet.ends_at)}｜${runMeetLocationText(meet)}`]
  if (password) lines.push(`入團密碼：${password}`)
  lines.push(`👉 ${origin}/?runmeet=${meet.id}`)
  return lines.join('\n')
}
