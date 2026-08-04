'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import { useRouter } from 'next/navigation'
import Link from 'next/link'
import { adminExploreApi, type ExploreBoss } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import BossAvgPreview from '@/components/BossAvgPreview'

// 城市探索後台：AVG 合成預覽牆（QA 用）。一次列出全部關主的「場景圖＋角色立繪」合成外觀，
// 讓管理員能快速掃過大量關主（目前 572 位）找出缺圖/合成跑版的問題，而不用一個個點開前台挑戰彈窗檢查。
// 合成畫面與前台 BossChallengePanel 共用同一個 BossAvgPreview 元件，確保像素一致。

type FilterMode = 'all' | 'missing' | 'missingMaster' | 'missingScene'

export default function AdminExplorePreviewPage() {
  const router = useRouter()
  const [bosses, setBosses] = useState<ExploreBoss[] | null>(null)
  const [err, setErr] = useState('')
  const [q, setQ] = useState('')
  const [filterMode, setFilterMode] = useState<FilterMode>('all')
  const [lightbox, setLightbox] = useState<ExploreBoss | null>(null)

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    adminExploreApi.list(t)
      .then((r) => setBosses(r.bosses))
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
        else if (e?.status === 403) setErr('無「事件任務」權限')
        else setErr(e?.message || '載入失敗')
      })
  }, [router])
  useEffect(() => { load() }, [load])

  // 只看真關主（純打卡點 checkin_only 沒有立繪，不適用合成畫面）
  const realBosses = useMemo(() => (bosses ?? []).filter((b) => !b.checkin_only), [bosses])
  const missingCount = useMemo(() => realBosses.filter((b) => !b.scene_image_url || !b.master_image_url).length, [realBosses])

  const filtered = useMemo(() => {
    let list = realBosses
    const kw = q.trim().toLowerCase()
    if (kw) list = list.filter((b) => b.code?.toLowerCase().includes(kw) || b.name?.toLowerCase().includes(kw))
    if (filterMode === 'missing') list = list.filter((b) => !b.scene_image_url || !b.master_image_url)
    else if (filterMode === 'missingMaster') list = list.filter((b) => !b.master_image_url)
    else if (filterMode === 'missingScene') list = list.filter((b) => !b.scene_image_url)
    return list
  }, [realBosses, q, filterMode])

  return (
    <div style={{ maxWidth: 1200, margin: '0 auto' }}>
      <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', flexWrap: 'wrap', gap: 8 }}>
        <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>城市探索 · AVG 合成預覽（QA）</h1>
        <Link href="/admin/explore" style={{ color: 'var(--fug)', fontSize: 13 }}>← 關主管理</Link>
      </div>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: '0 0 14px', lineHeight: 1.7 }}>
        一次檢查全部關主的「場景圖＋角色立繪」合成外觀，與前台關主挑戰彈窗完全一致，用來抓缺圖或合成跑版。
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '8px 0', fontSize: 13 }}>{err}</div>}

      {/* 篩選列 */}
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 10, marginBottom: 6 }}>
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="搜尋 code 或名稱…"
          style={{ background: 'var(--bg-0, #0d0f14)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 13, fontFamily: 'inherit', minWidth: 200 }}
        />
        <div style={{ display: 'flex', gap: 6 }}>
          <Chip label="全部" active={filterMode === 'all'} onClick={() => setFilterMode('all')} />
          <Chip label="缺圖" active={filterMode === 'missing'} onClick={() => setFilterMode('missing')} />
          <Chip label="缺立繪" active={filterMode === 'missingMaster'} onClick={() => setFilterMode('missingMaster')} />
          <Chip label="缺場景" active={filterMode === 'missingScene'} onClick={() => setFilterMode('missingScene')} />
        </div>
      </div>
      <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginBottom: 12 }}>
        共 {realBosses.length} 位關主，缺圖 {missingCount} 位
        {bosses === null && !err && ' · 載入中…'}
      </div>

      {/* 預覽牆 */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(240px, 1fr))', gap: 12 }}>
        {filtered.map((b) => {
          const missScene = !b.scene_image_url
          const missMaster = !b.master_image_url
          return (
            <div key={b.id} onClick={() => setLightbox(b)} style={{ cursor: 'pointer' }}>
              <BossAvgPreview scene={b.scene_image_url} master={b.master_image_url} name={b.name} stars={b.difficulty_stars} title={b.title} radius={12} lazy />
              <div style={{ marginTop: 4, fontSize: 12, color: 'var(--tx-dim)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {b.code} · {b.name}
              </div>
              {(missScene || missMaster) && (
                <div style={{ fontSize: 11, color: 'var(--hunt)', marginTop: 1 }}>
                  {missScene && <span>缺場景</span>}
                  {missScene && missMaster && <span> · </span>}
                  {missMaster && <span>缺立繪</span>}
                </div>
              )}
            </div>
          )
        })}
        {bosses !== null && filtered.length === 0 && (
          <div style={{ fontSize: 13, color: 'var(--tx-dim)', gridColumn: '1 / -1' }}>查無符合條件的關主。</div>
        )}
      </div>

      {/* Lightbox：點某格放大檢查 */}
      {lightbox && (
        <div
          onClick={() => setLightbox(null)}
          style={{ position: 'fixed', inset: 0, zIndex: 3000, background: 'rgba(0,0,0,.8)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 18 }}
        >
          <div onClick={(e) => e.stopPropagation()} style={{ width: '100%', maxWidth: 380 }}>
            <BossAvgPreview scene={lightbox.scene_image_url} master={lightbox.master_image_url} name={lightbox.name} stars={lightbox.difficulty_stars} title={lightbox.title} radius={16} />
            <div style={{ marginTop: 8, fontSize: 13, color: '#fff' }}>{lightbox.code} · {lightbox.name}</div>
            <div style={{ fontSize: 12, color: 'rgba(255,255,255,.6)', marginTop: 2 }}>{lightbox.region} {lightbox.place}</div>
            {(!lightbox.scene_image_url || !lightbox.master_image_url) && (
              <div style={{ fontSize: 12, color: '#ff6b6b', marginTop: 4 }}>
                {!lightbox.scene_image_url && <span>缺場景</span>}
                {!lightbox.scene_image_url && !lightbox.master_image_url && <span> · </span>}
                {!lightbox.master_image_url && <span>缺立繪</span>}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

function Chip({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      style={{
        background: active ? 'var(--fug)' : 'rgba(255,255,255,.05)',
        color: active ? 'var(--fug-ink)' : 'var(--tx)',
        fontWeight: active ? 800 : 600,
        border: '1px solid ' + (active ? 'var(--fug)' : 'var(--line-2)'),
        borderRadius: 999,
        padding: '6px 14px',
        cursor: 'pointer',
        fontSize: 12.5,
        fontFamily: 'inherit',
      }}
    >
      {label}
    </button>
  )
}
