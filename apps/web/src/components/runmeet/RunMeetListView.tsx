'use client'

import { useEffect, useMemo, useState } from 'react'
import useSWR from 'swr'
import { runMeetApi, type RunMeetCard, type RunMeetListParams } from '@/lib/api'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'
import { isFetchPending, shouldShowError, LOADING_TEXT } from '@/lib/runMeet'
import { MeetCard, cardBox, chip, chipActive, emptyBox, ghostBtn, inputStyle, primaryBtn, tinyBtn } from './ui'

// 團練探索：搜尋 + 篩選 chips + 排序 + 卡片列表 + 底部「已結束的團練（N）」折疊區。
// 篩選/搜尋字/附近座標等跨畫面存活的 state 全在父層 RunMeetScreen（本元件切到詳情頁會被卸載）。

export interface RunMeetFilters {
  q: string
  privacy: '' | 'public' | 'private'
  approval: '' | 'free' | 'review'
  hasSlot: boolean
  sort: 'soon' | 'new' | 'hot'
}
export const EMPTY_FILTERS: RunMeetFilters = { q: '', privacy: '', approval: '', hasSlot: false, sort: 'soon' }

/** 附近搜尋狀態：idle=未啟用、asking=定位中、on=已取得座標、denied=使用者拒絕/取不到。 */
export type NearState = 'idle' | 'asking' | 'on' | 'denied'
export interface NearPos { lat: number; lng: number; radiusKm: number }

const SORTS: { v: RunMeetFilters['sort']; t: string }[] = [
  { v: 'soon', t: '即將開跑' },
  { v: 'new', t: '最新發起' },
  { v: 'hot', t: '人氣' },
]

export default function RunMeetListView({
  filters, setFilters, near, nearState, onToggleNear, onOpen, onCreate,
}: {
  filters: RunMeetFilters
  setFilters: React.Dispatch<React.SetStateAction<RunMeetFilters>>
  near: NearPos | null
  nearState: NearState
  onToggleNear: () => void
  onOpen: (m: RunMeetCard) => void
  onCreate: () => void
}) {
  const user = useUser()
  const uid = user?.id ?? 'guest'
  const nearOn = nearState === 'on' && !!near

  const params: RunMeetListParams = useMemo(() => ({
    q: filters.q.trim() || undefined,
    privacy: filters.privacy || undefined,
    approval: filters.approval || undefined,
    has_slot: filters.hasSlot ? '1' : undefined,
    sort: filters.sort,
    ...(nearOn && near ? { near_lat: near.lat, near_lng: near.lng, radius_km: near.radiusKm } : {}),
  }), [filters, near, nearOn])
  // SWR key 第一格命名空間 'run-meets'（realtime topic runmeet 依此精準失效，見 siteRealtimeStore）
  const filterKey = JSON.stringify(params)

  const [offset, setOffset] = useState(0)
  const [acc, setAcc] = useState<RunMeetCard[]>([])
  useEffect(() => { setOffset(0); setAcc([]) }, [filterKey])

  const { data, error, isLoading } = useSWR(
    ['run-meets', filterKey, offset, uid],
    () => withUserAuth((t) => runMeetApi.list(t, { ...params, limit: 20, offset })),
    { keepPreviousData: true },
  )
  useEffect(() => {
    if (!data) return
    setAcc((prev) => {
      if (offset === 0) return data.items
      const seen = new Set(prev.map((i) => i.id))
      return [...prev, ...data.items.filter((i) => !seen.has(i.id))]
    })
  }, [data, offset])

  const total = data?.total ?? 0
  const hasFilter = !!(filters.q.trim() || filters.privacy || filters.approval || filters.hasSlot || nearOn)
  const items = acc

  return (
    <>
      {/* 搜尋 */}
      <div style={{ display: 'flex', gap: 8, marginBottom: 10 }}>
        <input
          value={filters.q}
          onChange={(e) => setFilters((f) => ({ ...f, q: e.target.value }))}
          placeholder="搜尋團練名稱或地點"
          style={{ ...inputStyle, padding: '9px 12px', fontSize: 13.5 }}
        />
        {filters.q && (
          <button onClick={() => setFilters((f) => ({ ...f, q: '' }))} style={{ ...ghostBtn, flexShrink: 0 }}>清除</button>
        )}
      </div>

      {/* 篩選 chips（橫向捲動） */}
      <div style={{ display: 'flex', gap: 7, overflowX: 'auto', paddingBottom: 8, WebkitOverflowScrolling: 'touch' }}>
        <button
          onClick={() => setFilters((f) => ({ ...f, privacy: '', approval: '', hasSlot: false }))}
          style={!filters.privacy && !filters.approval && !filters.hasSlot ? chipActive : chip}
        >全部</button>
        <button onClick={() => setFilters((f) => ({ ...f, privacy: f.privacy === 'public' ? '' : 'public' }))} style={filters.privacy === 'public' ? chipActive : chip}>🌐 公開</button>
        <button onClick={() => setFilters((f) => ({ ...f, privacy: f.privacy === 'private' ? '' : 'private' }))} style={filters.privacy === 'private' ? chipActive : chip}>🔒 私密</button>
        <button onClick={() => setFilters((f) => ({ ...f, approval: f.approval === 'free' ? '' : 'free' }))} style={filters.approval === 'free' ? chipActive : chip}>⚡ 自由加入</button>
        <button onClick={() => setFilters((f) => ({ ...f, hasSlot: !f.hasSlot }))} style={filters.hasSlot ? chipActive : chip}>✅ 尚有名額</button>
        <button onClick={onToggleNear} style={nearOn ? chipActive : chip}>
          {nearState === 'asking' ? '📍 定位中…' : '📍 附近'}
        </button>
      </div>

      {/* 未授權定位：以引導文案取代，不報錯（沿用專案 GPS 授權慣例） */}
      {nearState === 'denied' && (
        <div style={{ ...cardBox, padding: '10px 12px', marginBottom: 10, fontSize: 12, color: 'var(--tx-dim)', lineHeight: 1.7 }}>
          需要定位權限才能找「附近的團練」。請在瀏覽器網址列的權限設定中允許位置存取後再試一次。
          <div style={{ marginTop: 4, color: 'var(--tx-faint)' }}>你的位置只會用來這次查詢，不會被儲存。</div>
        </div>
      )}
      {nearOn && (
        <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginBottom: 8, lineHeight: 1.6 }}>
          已依你目前位置排序（{near?.radiusKm} 公里內）。為保護發起人隱私，只顯示距離範圍、不顯示精確距離。
        </div>
      )}

      {/* 排序 */}
      <div style={{ display: 'flex', gap: 6, marginBottom: 10 }}>
        {SORTS.map((s) => (
          <button key={s.v} onClick={() => setFilters((f) => ({ ...f, sort: s.v }))} style={filters.sort === s.v ? chipActive : chip}>{s.t}</button>
        ))}
      </div>

      {/* 判定順序：pending → error → 空狀態 → 內容。見 lib/runMeet.ts isFetchPending 的註解，
          只判 isLoading 會讓「金鑰切換的空窗期」誤顯示成載入失敗。 */}
      {items.length === 0 && isFetchPending(isLoading, data, error) ? (
        <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: '20px 2px' }}>{LOADING_TEXT}</div>
      ) : shouldShowError(isLoading, data, error, items.length > 0) ? (
        <div style={{ color: 'var(--hunt)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>載入失敗，請稍後再試</div>
      ) : items.length === 0 ? (
        <div style={emptyBox}>
          {filters.q.trim() ? (
            <>
              <div style={{ fontSize: 14, fontWeight: 800, color: 'var(--tx)' }}>找不到「{filters.q.trim()}」相關的團練</div>
              <button onClick={() => setFilters((f) => ({ ...f, q: '' }))} style={{ ...ghostBtn, marginTop: 12 }}>清除搜尋</button>
            </>
          ) : hasFilter ? (
            <>
              <div style={{ fontSize: 14, fontWeight: 800, color: 'var(--tx)' }}>找不到符合條件的團練</div>
              <div style={{ fontSize: 12.5, marginTop: 6 }}>試試放寬篩選條件</div>
              <button onClick={() => setFilters(EMPTY_FILTERS)} style={{ ...ghostBtn, marginTop: 12 }}>清除篩選</button>
            </>
          ) : (
            <>
              <div style={{ fontSize: 14, fontWeight: 800, color: 'var(--tx)' }}>目前還沒有人發起團練 🏃</div>
              <div style={{ fontSize: 12.5, marginTop: 6 }}>當第一個揪跑的人吧！</div>
              <button onClick={onCreate} style={{ ...primaryBtn, width: 'auto', padding: '10px 20px', marginTop: 12 }}>＋ 發起團練</button>
            </>
          )}
        </div>
      ) : (
        <>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            {items.map((m) => <MeetCard key={m.id} meet={m} onOpen={() => onOpen(m)} />)}
          </div>
          {items.length < total && (
            <button onClick={() => setOffset(items.length)} style={{ ...ghostBtn, width: '100%', marginTop: 12 }}>
              載入更多（{items.length} / {total}）
            </button>
          )}
        </>
      )}

      <EndedSection onOpen={onOpen} uid={uid} />
    </>
  )
}

// 已結束的團練：預設收合（規格 5.6——過期不靠排程，純查詢條件 ended=1）。
function EndedSection({ onOpen, uid }: { onOpen: (m: RunMeetCard) => void; uid: string }) {
  const [open, setOpen] = useState(false)
  const { data } = useSWR(
    getUserToken() ? ['run-meets', 'ended', uid] : null,
    () => withUserAuth((t) => runMeetApi.list(t, { ended: '1', sort: 'new', limit: 20 })),
  )
  const items = data?.items ?? []
  if (items.length === 0) return null
  return (
    <div style={{ marginTop: 18 }}>
      <button onClick={() => setOpen((v) => !v)} style={{ ...tinyBtn, width: '100%', padding: '10px 0', fontSize: 12.5 }}>
        {open ? '▾' : '▸'} 已結束的團練（{data?.total ?? items.length}）
      </button>
      {open && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginTop: 12 }}>
          {items.map((m) => <MeetCard key={m.id} meet={m} onOpen={() => onOpen(m)} />)}
        </div>
      )}
    </div>
  )
}
