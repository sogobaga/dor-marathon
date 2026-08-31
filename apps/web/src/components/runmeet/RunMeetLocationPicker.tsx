'use client'

import { useEffect, useRef, useState } from 'react'
import { runMeetApi, type RunMeetPlaceSuggestion } from '@/lib/api'
import { withUserAuth } from '@/lib/userAuth'
import { loadLeaflet } from '@/lib/leaflet'
import { NO_LOCATION_TEXT } from '@/lib/runMeet'
import { cardBox, chip, chipActive, fieldHint, fieldLabel, ghostBtn, inputStyle, textareaStyle, tinyBtn } from './ui'

// 建立/編輯表單的地點輸入。地點區塊最上方先二選一：「指定地點」／「不限地點」（使用者需求：
// 有時只是想發起團練揪人，不需要大家在同一個地點集合）。選「指定地點」時再兩種方式擇一：
//   ① 熱門跑點快選：搜尋既有 explore_bosses（後端 /run-meets/place-suggest 只回 region/place/lat/lng，
//      不回關主身分/圖片/課表——探索系統有「未揭露關主要遮蔽」的既有規則，不能從這裡開旁路）
//   ② 地圖選點：沿用既有 Leaflet + OpenStreetMap（lib/leaflet.ts），點/拖標記取得 lat/lng，
//      region/place_label 由「最近的 explore_bosses（5 公里內）」帶出**建議值**
// 兩種方式下發起人都可以手動修改公開層文字（只有他知道那個地點敏不敏感）。
//
// ⚠️ 地點三層揭露：region / place_label 是公開層（所有人可見）；lat / lng / meeting_detail 是成員層
//    （只有發起人、已加入成員、後台看得到——後端用不同 DTO，未加入者的 JSON 根本沒有這三個 key）。
//    表單上要把這件事講清楚，否則發起人不會知道「集合細節」寫得多細是安全的。
//
// ⚠️ 「不限地點」切換：切過去時 region/place_label 自動填入「不限」並設為唯讀（讓使用者看得到
//    值、但不能亂改——後端 no_location=true 時本來就會強制清空座標、空白時補「不限」，這裡
//    在前端先做一致的事，避免使用者以為兩欄還沒填）；切回「指定地點」則把「不限」清掉還原成
//    空欄位，不留著讓人誤以為已經填好了。

export interface LocationValue {
  no_location: boolean
  region: string
  place_label: string
  lat: number | null
  lng: number | null
  meeting_detail: string
}

const TAIPEI: [number, number] = [25.0376, 121.5645]
const SUGGEST_MAX_KM = 5 // 超過這個距離就不自動帶入建議（太遠的地標名會誤導）
const r6 = (n: number) => Math.round(n * 1e6) / 1e6

// 「不限地點」時 region/place_label 唯讀（值仍看得到＝「不限」，只是不能編輯）。
const readOnlyInputStyle: React.CSSProperties = { ...inputStyle, color: 'var(--tx-dim)', cursor: 'default' }

/** 兩點球面距離（公里）。只用於前端「最近地標」建議門檻，與後端排序無關。 */
export function haversineKm(aLat: number, aLng: number, bLat: number, bLng: number): number {
  const R = 6371
  const rad = (d: number) => (d * Math.PI) / 180
  const dLat = rad(bLat - aLat)
  const dLng = rad(bLng - aLng)
  const h = Math.sin(dLat / 2) ** 2 + Math.cos(rad(aLat)) * Math.cos(rad(bLat)) * Math.sin(dLng / 2) ** 2
  return 2 * R * Math.asin(Math.min(1, Math.sqrt(h)))
}

export default function RunMeetLocationPicker({ value, onChange }: {
  value: LocationValue
  onChange: (patch: Partial<LocationValue>) => void
}) {
  const [mode, setMode] = useState<'quick' | 'map'>('quick')
  const [q, setQ] = useState('')
  const [items, setItems] = useState<RunMeetPlaceSuggestion[]>([])
  const [busy, setBusy] = useState(false)
  const [nearby, setNearby] = useState<RunMeetPlaceSuggestion | null>(null) // 地圖選點後的最近地標建議
  const [mapErr, setMapErr] = useState('')

  // 快選搜尋（去抖動 350ms）
  useEffect(() => {
    const kw = q.trim()
    if (mode !== 'quick' || kw.length < 1) { setItems([]); return }
    let cancelled = false
    setBusy(true)
    const timer = setTimeout(() => {
      withUserAuth((t) => runMeetApi.placeSuggest(t, kw))
        .then((r) => { if (!cancelled) setItems(r.items) })
        .catch(() => { if (!cancelled) setItems([]) })
        .finally(() => { if (!cancelled) setBusy(false) })
    }, 350)
    return () => { cancelled = true; clearTimeout(timer) }
  }, [q, mode])

  // ── 地圖選點 ──────────────────────────────────────────────
  const mapRef = useRef<any>(null)
  const markerRef = useRef<any>(null)
  const onChangeRef = useRef(onChange)
  onChangeRef.current = onChange

  function applyPoint(lat: number, lng: number) {
    onChangeRef.current({ lat: r6(lat), lng: r6(lng) })
    // 依座標找最近的既有跑點當公開層建議值（門檻 5 公里；超過就不建議）
    withUserAuth((t) => runMeetApi.placeSuggest(t, undefined, r6(lat), r6(lng)))
      .then((res) => {
        const best = res.items[0]
        if (!best) { setNearby(null); return }
        setNearby(haversineKm(lat, lng, best.lat, best.lng) <= SUGGEST_MAX_KM ? best : null)
      })
      .catch(() => setNearby(null))
  }

  useEffect(() => {
    if (mode !== 'map') return
    let cancelled = false
    loadLeaflet().then((L) => {
      if (cancelled || mapRef.current) return
      const el = document.getElementById('runmeet-pick-map')
      if (!el) return
      const center: [number, number] = value.lat != null && value.lng != null ? [value.lat, value.lng] : TAIPEI
      const map = L.map('runmeet-pick-map').setView(center, value.lat != null ? 16 : 12)
      L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', { maxZoom: 19, attribution: '© OpenStreetMap' }).addTo(map)
      const marker = L.marker(center, { draggable: true }).addTo(map)
      marker.on('dragend', () => { const p = marker.getLatLng(); applyPoint(p.lat, p.lng) })
      map.on('click', (e: any) => { marker.setLatLng(e.latlng); applyPoint(e.latlng.lat, e.latlng.lng) })
      mapRef.current = map
      markerRef.current = marker
      // 容器在彈窗內，掛載當下量到的尺寸可能還是 0 → 下一輪再算一次
      setTimeout(() => { try { map.invalidateSize() } catch { /* 已卸載 */ } }, 80)
    }).catch(() => setMapErr('地圖載入失敗，請改用「熱門跑點快選」或直接手動填寫地點文字'))
    return () => {
      cancelled = true
      if (mapRef.current) { try { mapRef.current.remove() } catch { /* ignore */ } mapRef.current = null; markerRef.current = null }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode])

  const hasPoint = value.lat != null && value.lng != null

  // 「指定地點」↔「不限地點」二選一。切到不限地點：region/place_label 固定填「不限」（唯讀，
  // 讓使用者看得到值但不能亂改）、清空座標；切回指定地點：把「不限」清掉還原成空欄位，
  // 不留著讓使用者誤以為已經填好了。
  function setNoLocation(on: boolean) {
    if (on === value.no_location) return
    onChange(on
      ? { no_location: true, region: NO_LOCATION_TEXT, place_label: NO_LOCATION_TEXT, lat: null, lng: null }
      : { no_location: false, region: '', place_label: '' })
  }

  return (
    <div>
      <div style={{ display: 'flex', gap: 7, marginBottom: 10 }}>
        <button type="button" onClick={() => setNoLocation(false)} style={!value.no_location ? chipActive : chip}>📍 指定地點</button>
        <button type="button" onClick={() => setNoLocation(true)} style={value.no_location ? chipActive : chip}>🌏 不限地點</button>
      </div>

      {value.no_location && (
        <div style={{ ...fieldHint, marginBottom: 10 }}>
          這場團練不指定集合地點，大家各自在方便的地方跑；下方可以補充說明想怎麼進行。
        </div>
      )}

      {!value.no_location && (
      <div style={{ display: 'flex', gap: 7, marginBottom: 10 }}>
        <button type="button" onClick={() => setMode('quick')} style={mode === 'quick' ? chipActive : chip}>🔍 熱門跑點快選</button>
        <button type="button" onClick={() => setMode('map')} style={mode === 'map' ? chipActive : chip}>🗺️ 地圖選點</button>
      </div>
      )}

      {!value.no_location && (mode === 'quick' ? (
        <>
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="搜尋跑點或縣市，例：大安森林公園"
            style={{ ...inputStyle, fontSize: 13.5 }}
          />
          {busy && <div style={{ ...fieldHint, color: 'var(--tx-faint)' }}>搜尋中…</div>}
          {items.length > 0 && (
            <div style={{ ...cardBox, marginTop: 8, maxHeight: 190, overflowY: 'auto' }}>
              {items.map((it, i) => (
                <button
                  key={`${it.region}-${it.place}-${i}`}
                  type="button"
                  onClick={() => {
                    onChange({ region: it.region, place_label: it.place, lat: r6(it.lat), lng: r6(it.lng) })
                    setItems([]); setQ('')
                  }}
                  style={{ display: 'block', width: '100%', textAlign: 'left', background: 'none', border: 'none', borderBottom: '1px solid var(--line)', padding: '9px 12px', cursor: 'pointer', fontFamily: 'inherit' }}
                >
                  <span style={{ fontSize: 13, fontWeight: 800, color: 'var(--tx)' }}>{it.place}</span>
                  <span style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginLeft: 8 }}>{it.region}</span>
                </button>
              ))}
            </div>
          )}
          {q.trim() && !busy && items.length === 0 && (
            <div style={fieldHint}>找不到相符的跑點，可改用「地圖選點」或直接在下方手動填寫。</div>
          )}
        </>
      ) : (
        <>
          {mapErr ? (
            <div style={{ ...fieldHint, color: 'var(--hunt)' }}>{mapErr}</div>
          ) : (
            // touchAction:'none'：外層表單鎖 pan-y（見 ui.tsx RunMeetModal），交集規則下子元素只會更嚴不會更寬鬆，
            // 所以地圖容器要自己宣告 none（交集 pan-y∩none=none）——等同完全不讓瀏覽器接管手勢，全交給 Leaflet 自己的
            // 拖曳/縮放處理，才不會被外層的垂直鎖誤擋成只能上下滑。
            <div id="runmeet-pick-map" style={{ width: '100%', height: 220, borderRadius: 12, overflow: 'hidden', border: '1px solid var(--line-2)', touchAction: 'none' }} />
          )}
          <div style={fieldHint}>點地圖或拖曳標記可調整位置。</div>
          {nearby && (
            <div style={{ ...cardBox, marginTop: 8, padding: '9px 11px', display: 'flex', alignItems: 'center', gap: 8 }}>
              <div style={{ flex: 1, minWidth: 0, fontSize: 12, color: 'var(--tx-dim)', lineHeight: 1.6 }}>
                最近的跑點：<b style={{ color: 'var(--tx)' }}>{nearby.place}</b>（{nearby.region}）
              </div>
              <button type="button" onClick={() => onChange({ region: nearby.region, place_label: nearby.place })} style={tinyBtn}>套用</button>
            </div>
          )}
        </>
      ))}

      {/* 公開層（所有人可見）：no_location 時固定「不限」且唯讀，切回指定地點才能再編輯 */}
      <div style={{ marginTop: 12 }}>
        <label style={fieldLabel}>縣市・行政區（公開，必填）</label>
        <input
          value={value.region}
          onChange={(e) => onChange({ region: e.target.value })}
          placeholder="臺北市・大安區"
          maxLength={30}
          readOnly={value.no_location}
          style={value.no_location ? readOnlyInputStyle : inputStyle}
        />
      </div>
      <div style={{ marginTop: 10 }}>
        <label style={fieldLabel}>地點名稱（公開，必填）</label>
        <input
          value={value.place_label}
          onChange={(e) => onChange({ place_label: e.target.value })}
          placeholder="大安森林公園"
          maxLength={60}
          readOnly={value.no_location}
          style={value.no_location ? readOnlyInputStyle : inputStyle}
        />
        <div style={fieldHint}>
          {value.no_location
            ? '不限地點：這兩欄固定顯示「不限」，所有人都看得到。'
            : '這兩欄所有人都看得到（列表與詳情）。精確座標與集合細節只有加入的團員看得到。'}
        </div>
      </div>

      {/* 成員層（只有已加入成員看得到） */}
      <div style={{ marginTop: 12 }}>
        <label style={fieldLabel}>{value.no_location ? '補充說明（只有團員看得到，選填）' : '集合細節（只有團員看得到，選填）'}</label>
        <textarea
          value={value.meeting_detail}
          onChange={(e) => onChange({ meeting_detail: e.target.value })}
          placeholder={value.no_location ? '想怎麼進行、跑完在哪裡回報' : '2 號出口涼亭旁，我穿黃色風衣'}
          maxLength={200}
          style={{ ...textareaStyle, minHeight: 66 }}
        />
        {value.no_location ? (
          <div style={fieldHint}>不限地點的團練沒有集合座標；這裡可以說明想怎麼進行、大家跑完在哪裡回報。</div>
        ) : (
          <div style={fieldHint}>
            {hasPoint
              ? `已設定精確座標（${value.lat}, ${value.lng}）——只有成功加入的團員與後台看得到。`
              : '尚未設定精確座標；未設定時團員只會看到上方的公開地點文字。'}
            {hasPoint && (
              <button type="button" onClick={() => onChange({ lat: null, lng: null })} style={{ ...ghostBtn, padding: '3px 8px', fontSize: 11, marginLeft: 8 }}>清除座標</button>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
