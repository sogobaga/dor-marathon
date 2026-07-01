'use client'

import { useEffect, useRef, useState, useCallback } from 'react'
import { activitiesApi, checkpointApi, type GpsPoint, type GpsRunResult, type ActiveCheckpoint } from '@/lib/api'
import { getUserToken, withUserAuth, useUser } from '@/lib/userAuth'
import { loadLeaflet } from '@/lib/leaflet'
import GoogleAuthProvider from '@/components/GoogleAuthProvider'
import { LoginModal } from '@/components/UserAuthBar'
import PhoneFrame from '@/components/PhoneFrame'
import ScrollArea from '@/components/ScrollArea'

/* eslint-disable @typescript-eslint/no-explicit-any */

const LS_KEY = 'dor_gps_run'
const MAX_ACC = 40 // 精度差於此（公尺）的點不採計距離
const MAX_SPEED = 1000 / 120 // 8.33 m/s（2:00/km）人類極限上限
const JITTER_MIN = 6 // 公尺：距上一個採納點移動不足此值視為原地抖動，不計距離
const PACE_MIN_KM = 0.005 // 累積達此距離（5m，約顯示 0.01km 時）即顯示平均配速

function haversineM(a: GpsPoint, b: GpsPoint) {
  const R = 6371000, rad = Math.PI / 180
  const dLat = (b.lat - a.lat) * rad, dLng = (b.lng - a.lng) * rad
  const x = Math.sin(dLat / 2) ** 2 + Math.cos(a.lat * rad) * Math.cos(b.lat * rad) * Math.sin(dLng / 2) ** 2
  return R * 2 * Math.atan2(Math.sqrt(x), Math.sqrt(1 - x))
}
function fmtTime(s: number) {
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = Math.floor(s % 60)
  const p = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${p(m)}:${p(sec)}` : `${p(m)}:${p(sec)}`
}
function fmtPace(s: number) {
  if (!s || !isFinite(s) || s <= 0) return '--:--'
  return `${Math.floor(s / 60)}:${String(Math.round(s % 60)).padStart(2, '0')}`
}

export default function TrackPage() {
  const user = useUser()
  const [status, setStatus] = useState<'idle' | 'tracking' | 'done'>('idle')
  const [distance, setDistance] = useState(0) // 公尺
  const [elapsed, setElapsed] = useState(0)
  const [splits, setSplits] = useState<number[]>([]) // 每公里配速（秒）
  const [anomalies, setAnomalies] = useState(0)
  const [warn, setWarn] = useState('')
  const [err, setErr] = useState('')
  const [result, setResult] = useState<GpsRunResult | null>(null)
  const [showLogin, setShowLogin] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [checkpoints, setCheckpoints] = useState<ActiveCheckpoint[]>([])
  const [curPos, setCurPos] = useState<{ lat: number; lng: number; acc: number } | null>(null)
  const [cpBusy, setCpBusy] = useState('') // 正在打卡的 checkpoint id
  const [cpMsg, setCpMsg] = useState('')

  const pointsRef = useRef<GpsPoint[]>([])
  const distRef = useRef(0)
  const splitMarkRef = useRef<number[]>([]) // 每跨整公里時的 elapsed 秒
  const startRef = useRef(0)
  const watchRef = useRef<number | null>(null)
  const wakeRef = useRef<any>(null)
  const timerRef = useRef<any>(null)
  const mapRef = useRef<any>(null)
  const lineRef = useRef<any>(null)
  const markRef = useRef<any>(null)
  const warnTimer = useRef<any>(null)
  const statusRef = useRef(status)
  statusRef.current = status
  const lastAccRef = useRef<GpsPoint | null>(null) // 上一個「採納」的點（過濾原地抖動用）

  const ensureMap = useCallback(async (lat: number, lng: number) => {
    const L = await loadLeaflet()
    if (mapRef.current) return
    const map = L.map('gps-map', { zoomControl: true }).setView([lat, lng], 16)
    L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', { maxZoom: 19, attribution: '© OpenStreetMap' }).addTo(map)
    lineRef.current = L.polyline([], { color: '#46E3A0', weight: 5 }).addTo(map)
    markRef.current = L.circleMarker([lat, lng], { radius: 7, color: '#fff', fillColor: '#46E3A0', fillOpacity: 1, weight: 2 }).addTo(map)
    mapRef.current = map
  }, [])

  const onPos = useCallback((pos: GeolocationPosition) => {
    const p: GpsPoint = { lat: pos.coords.latitude, lng: pos.coords.longitude, t: pos.timestamp, acc: pos.coords.accuracy ?? 0 }
    setCurPos({ lat: p.lat, lng: p.lng, acc: p.acc })
    ensureMap(p.lat, p.lng)
    const goodAcc = p.acc === 0 || p.acc <= MAX_ACC

    // 距離以「上一個採納點」為基準計算；移動不足門檻 → 視為原地抖動，不採納、不累積
    if (goodAcc) {
      const lastAcc = lastAccRef.current
      if (!lastAcc) {
        // 第一個有效點：當作起點
        lastAccRef.current = p
        pointsRef.current.push(p)
        if (lineRef.current) lineRef.current.addLatLng([p.lat, p.lng])
      } else {
        const d = haversineM(lastAcc, p)
        const dt = (p.t - lastAcc.t) / 1000
        if (d >= JITTER_MIN && dt > 0) {
          if (d / dt > MAX_SPEED) {
            setAnomalies((n) => n + 1)
            setWarn(`偵測到異常速度區段（${(d / dt).toFixed(1)} m/s），此筆將標記待審`)
            clearTimeout(warnTimer.current)
            warnTimer.current = setTimeout(() => setWarn(''), 4000)
          }
          distRef.current += d
          setDistance(distRef.current)
          // 每公里分段
          const km = Math.floor(distRef.current / 1000)
          const el = (p.t - startRef.current) / 1000
          while (splitMarkRef.current.length < km) {
            const prevEl = splitMarkRef.current.length ? splitMarkRef.current[splitMarkRef.current.length - 1] : 0
            splitMarkRef.current.push(el)
            setSplits((s) => [...s, el - prevEl])
          }
          lastAccRef.current = p
          pointsRef.current.push(p)
          if (lineRef.current) lineRef.current.addLatLng([p.lat, p.lng])
        }
      }
    }
    // 標記點與地圖永遠跟著「目前」位置（即時感），即使該點未被採納為距離
    if (markRef.current) markRef.current.setLatLng([p.lat, p.lng])
    if (mapRef.current) mapRef.current.panTo([p.lat, p.lng])
    // 防當掉：暫存採納後的軌跡
    localStorage.setItem(LS_KEY, JSON.stringify({ start: startRef.current, points: pointsRef.current.slice(-2000) }))
  }, [ensureMap])

  async function acquireWake() {
    try { wakeRef.current = await (navigator as any).wakeLock?.request('screen') } catch { /* ignore */ }
  }

  function start() {
    setErr('')
    if (!navigator.geolocation) { setErr('此裝置/瀏覽器不支援定位'); return }
    pointsRef.current = []; distRef.current = 0; splitMarkRef.current = []; lastAccRef.current = null
    setDistance(0); setElapsed(0); setSplits([]); setAnomalies(0); setResult(null)
    startRef.current = Date.now()
    setStatus('tracking')
    // ⚠️ iOS：定位權限提示必須在使用者手勢「同步」流程內直接請求，不能先 await 任何東西
    //（否則會失去使用者手勢 → Safari 直接判定拒絕，code 1）。故先請求定位，wake lock 之後再背景取得。
    watchRef.current = navigator.geolocation.watchPosition(
      onPos,
      (e) => {
        if (e.code === 1) setErr('定位權限被拒：Safari 跳出詢問時請按「允許」；若沒跳出，到 設定 → Apps → Safari → 位置 設為「詢問」或「允許」後重試。')
        else if (e.code === 3) setErr('定位逾時，請到較空曠處再試（室內 GPS 收訊較差）。')
        else setErr('定位失敗：' + (e.message || '請確認已開啟定位'))
      },
      { enableHighAccuracy: true, maximumAge: 0, timeout: 20000 },
    )
    timerRef.current = setInterval(() => setElapsed((Date.now() - startRef.current) / 1000), 250)
    acquireWake() // 不 await：wake lock 失敗或延遲都不影響定位
  }

  const cleanup = useCallback(() => {
    if (watchRef.current != null) navigator.geolocation.clearWatch(watchRef.current)
    watchRef.current = null
    clearInterval(timerRef.current)
    try { wakeRef.current?.release() } catch { /* ignore */ }
    wakeRef.current = null
  }, [])

  async function finish() {
    cleanup()
    setStatus('done')
    const pts = pointsRef.current
    if (pts.length < 2) { setErr('軌跡太短，未上傳'); localStorage.removeItem(LS_KEY); return }
    const token = getUserToken()
    if (!token) { setErr('未登入，無法上傳'); return }
    setUploading(true)
    try {
      const { result } = await withUserAuth((t) => activitiesApi.uploadGps(t, {
        started_at: new Date(startRef.current).toISOString(),
        ended_at: new Date(pts[pts.length - 1].t).toISOString(),
        points: pts,
      }))
      setResult(result)
      localStorage.removeItem(LS_KEY)
    } catch (e: any) {
      setErr(e?.message || '上傳失敗')
    } finally { setUploading(false) }
  }

  // 螢幕回到前景時重新取得 wake lock
  // 只在掛載/卸載執行：status 變動不可觸發 cleanup（否則會清掉 start 剛建立的計時器與定位）
  useEffect(() => {
    const onVis = () => { if (document.visibilityState === 'visible' && statusRef.current === 'tracking') acquireWake() }
    document.addEventListener('visibilitychange', onVis)
    return () => { document.removeEventListener('visibilitychange', onVis); cleanup() }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 打卡點：載入「進行中賽事 + 已報名」的可打卡點
  const fetchCheckpoints = useCallback(async () => {
    const token = getUserToken()
    if (!token) { setCheckpoints([]); return }
    try {
      const { checkpoints } = await checkpointApi.active(token)
      setCheckpoints(checkpoints)
    } catch { /* ignore */ }
  }, [])
  useEffect(() => { fetchCheckpoints() }, [fetchCheckpoints, user?.id])

  async function doCheckin(cp: ActiveCheckpoint) {
    setCpMsg('')
    const token = getUserToken()
    if (!token) { setShowLogin(true); return }
    setCpBusy(cp.id)
    try {
      let lat = curPos?.lat, lng = curPos?.lng, acc = curPos?.acc ?? 0
      // 非追蹤中（無 live 位置）→ 一次性定位
      if (status !== 'tracking' || lat == null) {
        const pos = await new Promise<GeolocationPosition>((res, rej) =>
          navigator.geolocation.getCurrentPosition(res, rej, { enableHighAccuracy: true, timeout: 20000, maximumAge: 0 }))
        lat = pos.coords.latitude; lng = pos.coords.longitude; acc = pos.coords.accuracy ?? 0
      }
      // 追蹤中附上近期軌跡作為佐證
      const points = status === 'tracking'
        ? pointsRef.current.slice(-40).map((p) => ({ lat: p.lat, lng: p.lng, t: p.t, acc: p.acc }))
        : []
      const { result } = await checkpointApi.checkin(token, cp.id, { lat: lat!, lng: lng!, acc, points })
      setCpMsg(result.message)
      await fetchCheckpoints()
    } catch (e: any) {
      setCpMsg(e?.code === 1 ? '需要定位權限才能打卡' : (e?.message || '打卡失敗，請重試'))
    } finally { setCpBusy('') }
  }

  const cpDist = (cp: ActiveCheckpoint): number | null =>
    curPos ? haversineM({ lat: curPos.lat, lng: curPos.lng, t: 0, acc: 0 }, { lat: cp.lat, lng: cp.lng, t: 0, acc: 0 }) : null

  const distKm = distance / 1000
  const avgPace = distKm >= PACE_MIN_KM ? elapsed / distKm : 0 // 未達門檻先顯示 --:--，避免爆數字

  return (
   <GoogleAuthProvider>
    <PhoneFrame>
      {showLogin && <LoginModal onClose={() => setShowLogin(false)} />}
      <header style={{ padding: '14px 18px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid var(--line)' }}>
        <a href="/" style={{ color: 'var(--tx-dim)', fontSize: 14, textDecoration: 'none' }}>← 返回</a>
        <strong style={{ fontSize: 16 }}>GPS 跑步追蹤</strong>
        <a href="/track/history" style={{ color: 'var(--fug)', fontSize: 13, textDecoration: 'none' }}>歷史</a>
      </header>

      {/* 地圖 */}
      <div id="gps-map" style={{ width: '100%', height: 280, background: 'var(--bg-2)' }} />

      <ScrollArea padding="16">
        {warn && <div style={{ background: 'rgba(255,90,90,.12)', border: '1px solid rgba(255,90,90,.4)', color: '#ff8a8a', borderRadius: 10, padding: '10px 12px', fontSize: 13, marginBottom: 12, wordBreak: 'break-word' }}>⚠️ {warn}</div>}
        {err && <div style={{ color: 'var(--hunt)', fontSize: 13, marginBottom: 12 }}>{err}</div>}

        {/* 即時數據 */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
          <Big label="距離" value={distKm.toFixed(2)} unit="km" />
          <Big label="時間" value={fmtTime(elapsed)} unit="" />
          <Big label="平均配速" value={fmtPace(avgPace)} unit="/km" />
          <Big label="異常區段" value={String(anomalies)} unit="段" warn={anomalies > 0} />
        </div>

        {/* 打卡點任務 */}
        {checkpoints.length > 0 && (
          <div style={{ marginTop: 16 }}>
            <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 6 }}>📍 打卡點任務</div>
            {cpMsg && <div style={{ fontSize: 12.5, color: 'var(--fug)', marginBottom: 8, wordBreak: 'break-word' }}>{cpMsg}</div>}
            {status !== 'tracking' && <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginBottom: 8 }}>建議按「開始跑步」邊跑邊打卡（有軌跡佐證，免審核）。</div>}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {checkpoints.map((cp) => {
                const d = cpDist(cp)
                const inRange = d != null && d <= cp.radius_m
                const busy = cpBusy === cp.id
                const blocked = busy || (curPos != null && !inRange)
                return (
                  <div key={cp.id} style={{ background: 'var(--bg-2)', borderRadius: 10, padding: '10px 12px', display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 10 }}>
                    <div style={{ minWidth: 0 }}>
                      <div style={{ fontSize: 13.5, fontWeight: 700, color: 'var(--tx)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{cp.title || '打卡點'}</div>
                      <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                        {cp.race_title}{cp.task_title ? ` · ${cp.task_title}` : ''}
                        {d != null && !cp.checked && <> · {d < 1000 ? `還有 ${Math.round(d)}m` : `${(d / 1000).toFixed(1)}km`}</>}
                      </div>
                    </div>
                    {cp.checked ? (
                      <span style={{ color: 'var(--fug)', fontWeight: 700, fontSize: 13, flexShrink: 0 }}>✓ 已打卡</span>
                    ) : cp.pending ? (
                      <span style={{ color: 'var(--gold)', fontSize: 12.5, flexShrink: 0 }}>審核中</span>
                    ) : (
                      <button onClick={() => doCheckin(cp)} disabled={blocked}
                        style={{ flexShrink: 0, background: 'var(--fug)', color: '#05140e', fontWeight: 800, border: 'none', borderRadius: 9, padding: '8px 14px', fontSize: 13, cursor: blocked ? 'default' : 'pointer', opacity: blocked ? 0.45 : 1 }}>
                        {busy ? '打卡中…' : curPos != null && !inRange ? '未到範圍' : '打卡'}
                      </button>
                    )}
                  </div>
                )
              })}
            </div>
          </div>
        )}

        {/* 分段 */}
        {splits.length > 0 && (
          <div style={{ marginTop: 16 }}>
            <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 6 }}>每公里分段</div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              {splits.map((s, i) => (
                <div key={i} style={{ display: 'flex', justifyContent: 'space-between', background: 'var(--bg-2)', borderRadius: 8, padding: '7px 12px', fontSize: 13 }}>
                  <span style={{ color: 'var(--tx-dim)' }}>第 {i + 1} km</span>
                  <span style={{ fontWeight: 700 }}>{fmtPace(s)} /km</span>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* 結果 */}
        {status === 'done' && result && (
          <div style={{ marginTop: 16, background: 'var(--bg-1)', border: `1px solid ${result.flagged ? 'rgba(255,90,90,.4)' : 'var(--line-2)'}`, borderRadius: 12, padding: 14, wordBreak: 'break-word' }}>
            <div style={{ fontWeight: 800, marginBottom: 8 }}>
              {result.too_short ? 'ℹ️ 移動距離不足，無法計算' : result.flagged ? '⚠️ 數據異常，已標記待審' : '✓ 已記錄'}
            </div>
            <div style={{ fontSize: 13, color: 'var(--tx-dim)', lineHeight: 1.8 }}>
              {result.too_short ? (
                <span>本次移動距離過短（{result.distance_km.toFixed(2)} km），未達可計算配速的最小距離，故不記錄。請實際移動一段距離後再試。</span>
              ) : (
                <>
                  距離 {result.distance_km.toFixed(2)} km · 時間 {fmtTime(result.duration_s)} · 平均配速 {fmtPace(result.avg_pace_s)}/km<br />
                  {result.flagged
                    ? <span style={{ color: '#ff8a8a' }}>原因：{result.flag_reason}（不發 EXP，待後台審核）</span>
                    : <span style={{ color: 'var(--fug)' }}>已進活動記錄{result.exp_awarded ? '，里程 EXP 將於數秒後發放' : ''}</span>}
                </>
              )}
            </div>
          </div>
        )}
      </ScrollArea>

      {/* 操作 */}
      <div style={{ padding: 16, borderTop: '1px solid var(--line)', position: 'sticky', bottom: 0, background: 'var(--bg)' }}>
        {status === 'idle' && (
          user
            ? <button onClick={start} style={btn}>▶ 開始跑步</button>
            : <button onClick={() => setShowLogin(true)} style={btn}>請先登入</button>
        )}
        {status === 'tracking' && <button onClick={finish} style={{ ...btn, background: 'var(--hunt)', color: '#fff' }}>■ 結束並上傳</button>}
        {status === 'done' && <button onClick={() => { setStatus('idle'); setElapsed(0); setDistance(0); setSplits([]); setAnomalies(0) }} style={{ ...btn, background: 'var(--bg-2)', color: 'var(--tx)' }}>再跑一次</button>}
        {status === 'tracking' && <div style={{ textAlign: 'center', fontSize: 11, color: 'var(--tx-faint)', marginTop: 8 }}>追蹤中請保持本頁在前景、螢幕勿關（背景追蹤瀏覽器不支援）{uploading ? ' · 上傳中…' : ''}</div>}
      </div>
    </PhoneFrame>
   </GoogleAuthProvider>
  )
}

function Big({ label, value, unit, warn }: { label: string; value: string; unit: string; warn?: boolean }) {
  return (
    <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '12px 14px' }}>
      <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</div>
      <div style={{ fontSize: 26, fontWeight: 900, color: warn ? 'var(--hunt)' : 'var(--tx)', fontVariantNumeric: 'tabular-nums' }}>
        {value}<span style={{ fontSize: 13, marginLeft: 3, color: 'var(--tx-dim)' }}>{unit}</span>
      </div>
    </div>
  )
}

const btn: React.CSSProperties = { width: '100%', background: 'var(--fug)', color: '#05140e', fontWeight: 800, border: 'none', borderRadius: 12, padding: '15px 20px', fontSize: 16, cursor: 'pointer' }
