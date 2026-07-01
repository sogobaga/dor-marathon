'use client'

import useSWR from 'swr'
import { useEffect, useRef, useState } from 'react'
import { racesApi, checkpointApi, type Race } from '@/lib/api'
import { getUserToken } from '@/lib/userAuth'
import { loadLeaflet } from '@/lib/leaflet'

/* eslint-disable @typescript-eslint/no-explicit-any */

function havM(aLat: number, aLng: number, bLat: number, bLng: number) {
  const R = 6371000, rad = Math.PI / 180
  const dLat = (bLat - aLat) * rad, dLng = (bLng - aLng) * rad
  const x = Math.sin(dLat / 2) ** 2 + Math.cos(aLat * rad) * Math.cos(bLat * rad) * Math.sin(dLng / 2) ** 2
  return R * 2 * Math.atan2(Math.sqrt(x), Math.sqrt(1 - x))
}

type CP = { id: string; lat: number; lng: number; radius_m: number; title?: string; collected?: boolean; pending?: boolean; taskTitle: string }

// 賽事資訊頁「探索」頁籤：顯示此賽事的打卡點（地圖 + 狀態 + 就近打卡）
export function ExploreBody({ race }: { race: Race }) {
  const token = getUserToken() || undefined
  const { data, mutate } = useSWR(['progress', race.id], () => racesApi.progress(race.id, token), { refreshInterval: 30000 })
  const cpTasks = (data?.progress.tasks ?? []).filter((t) => t.metric_type === 'checkpoint')
  const points: CP[] = cpTasks.flatMap((t) => (t.checkpoints ?? []).map((c) => ({ ...c, id: c.id ?? '', taskTitle: t.title })))

  const [curPos, setCurPos] = useState<{ lat: number; lng: number; acc: number } | null>(null)
  const [locating, setLocating] = useState(false)
  const [busy, setBusy] = useState('')
  const [msg, setMsg] = useState('')
  const mapRef = useRef<any>(null)
  const layerRef = useRef<any>(null)
  const sig = points.map((p) => `${p.id}:${p.collected ? 1 : p.pending ? 2 : 0}`).join(',') + '|' + (curPos ? `${curPos.lat.toFixed(4)},${curPos.lng.toFixed(4)}` : '')

  useEffect(() => {
    if (points.length === 0) return
    let cancelled = false
    loadLeaflet().then((L) => {
      if (cancelled) return
      if (!mapRef.current) {
        const c = points.find((p) => p.lat || p.lng) || { lat: 25.0376, lng: 121.5645 }
        const map = L.map('explore-map').setView([c.lat, c.lng], 15)
        L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', { maxZoom: 19, attribution: '© OpenStreetMap' }).addTo(map)
        layerRef.current = L.layerGroup().addTo(map)
        mapRef.current = map
      }
      const layer = layerRef.current
      layer.clearLayers()
      const bounds: [number, number][] = []
      points.forEach((p, i) => {
        if (!p.lat && !p.lng) return
        const color = p.collected ? '#46E3A0' : p.pending ? '#FFC24B' : '#9aa0a6'
        L.circle([p.lat, p.lng], { radius: p.radius_m || 20, color, weight: 1.5, fillOpacity: 0.12 }).addTo(layer)
        L.circleMarker([p.lat, p.lng], { radius: 8, color: '#fff', weight: 2, fillColor: color, fillOpacity: 1 }).addTo(layer)
          .bindTooltip(`${i + 1}. ${p.title || '打卡點'}`)
        bounds.push([p.lat, p.lng])
      })
      if (curPos) {
        L.circleMarker([curPos.lat, curPos.lng], { radius: 7, color: '#fff', weight: 2, fillColor: '#3b82f6', fillOpacity: 1 }).addTo(layer).bindTooltip('你')
        bounds.push([curPos.lat, curPos.lng])
      }
      if (bounds.length > 1) mapRef.current.fitBounds(bounds, { padding: [30, 30], maxZoom: 16 })
    })
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sig, points.length])

  function locate() {
    setLocating(true); setMsg('')
    navigator.geolocation.getCurrentPosition(
      (pos) => { setCurPos({ lat: pos.coords.latitude, lng: pos.coords.longitude, acc: pos.coords.accuracy ?? 0 }); setLocating(false) },
      (e) => { setMsg(e.code === 1 ? '需要定位權限才能打卡' : '定位失敗，請再試'); setLocating(false) },
      { enableHighAccuracy: true, timeout: 20000, maximumAge: 0 },
    )
  }
  async function checkin(cp: CP) {
    const t = getUserToken()
    if (!t) { setMsg('請先登入'); return }
    if (!curPos) { locate(); return }
    setBusy(cp.id); setMsg('')
    try {
      const { result } = await checkpointApi.checkin(t, cp.id, { lat: curPos.lat, lng: curPos.lng, acc: curPos.acc })
      setMsg(result.message)
      mutate()
    } catch (e: any) { setMsg(e?.message || '打卡失敗') } finally { setBusy('') }
  }

  if (!data) return <div style={{ padding: '30px 0', textAlign: 'center', color: 'var(--tx-dim)' }}>載入中…</div>
  if (points.length === 0) return <div style={{ padding: '40px 20px', textAlign: 'center', color: 'var(--tx-faint)', fontSize: 13.5 }}>此賽事目前沒有打卡點。</div>

  const collected = points.filter((p) => p.collected).length

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div style={{ fontSize: 13.5, color: 'var(--tx-dim)' }}>賽事的打卡點分布，集滿即完成任務。</div>
        <span style={{ fontSize: 13, fontWeight: 800, color: 'var(--fug)' }}>集章 {collected}/{points.length}</span>
      </div>

      <div id="explore-map" style={{ width: '100%', height: 260, borderRadius: 12, overflow: 'hidden', background: 'var(--bg-2)' }} />

      {/* 打卡請到「開始跑步」邊跑邊進行（有 GPS 軌跡佐證＝免審核；且中途離開會中斷追蹤） */}
      <a href="/track" style={{ display: 'block', textAlign: 'center', background: 'rgba(70,227,160,.1)', border: '1px solid rgba(70,227,160,.4)', color: 'var(--fug)', fontWeight: 800, borderRadius: 12, padding: '12px', fontSize: 14, textDecoration: 'none' }}>🏃 到「開始跑步」邊跑邊打卡</a>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        <button onClick={locate} disabled={locating} style={locBtn}>{locating ? '定位中…' : '📍 更新我的位置（看距離）'}</button>
        <span style={{ fontSize: 11.5, color: 'var(--tx-faint)' }}>在此打卡沒有軌跡佐證，需主辦審核</span>
      </div>
      {msg && <div style={{ fontSize: 12.5, color: 'var(--fug)', wordBreak: 'break-word' }}>{msg}</div>}

      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {points.map((cp, i) => {
          const dist = curPos ? havM(curPos.lat, curPos.lng, cp.lat, cp.lng) : null
          const inRange = dist != null && dist <= cp.radius_m
          const busyThis = busy === cp.id
          return (
            <div key={cp.id || i} style={row}>
              <div style={{ minWidth: 0 }}>
                <div style={{ fontSize: 13.5, fontWeight: 700, color: 'var(--tx)' }}>{i + 1}. {cp.title || '打卡點'}</div>
                <div style={{ fontSize: 11.5, color: 'var(--tx-faint)' }}>
                  {cp.taskTitle}{dist != null && !cp.collected ? ` · 距離 ${dist < 1000 ? `${Math.round(dist)}m` : `${(dist / 1000).toFixed(1)}km`}` : ''}
                </div>
              </div>
              {cp.collected ? <span style={{ color: 'var(--fug)', fontWeight: 700, fontSize: 13, flexShrink: 0 }}>✓ 已打卡</span>
                : cp.pending ? <span style={{ color: 'var(--gold)', fontSize: 12.5, flexShrink: 0 }}>審核中</span>
                  : <button onClick={() => checkin(cp)} disabled={busyThis || (curPos != null && !inRange)}
                    style={{ ...cpBtn, opacity: busyThis || (curPos != null && !inRange) ? 0.45 : 1 }}>
                    {busyThis ? '打卡中…' : !curPos ? '定位打卡' : inRange ? '打卡' : '未到範圍'}
                  </button>}
            </div>
          )
        })}
      </div>
    </div>
  )
}

const row: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 10, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 13px' }
const cpBtn: React.CSSProperties = { flexShrink: 0, background: 'var(--fug)', color: '#05140e', fontWeight: 800, border: 'none', borderRadius: 9, padding: '8px 14px', fontSize: 13, cursor: 'pointer' }
const locBtn: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '7px 12px', color: 'var(--tx)', fontSize: 12.5, cursor: 'pointer', flexShrink: 0 }
