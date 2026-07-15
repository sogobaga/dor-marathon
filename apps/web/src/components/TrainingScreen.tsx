'use client'

import { useMemo, useState } from 'react'
import useSWR from 'swr'
import { trainingApi, type WorkoutTemplate, type PaceLevel } from '@/lib/api'
import { resolveTemplate, saveFreetrainWorkout, totalKm, estMinutes, fmtDuration, segSummary, targetPaceBand } from '@/lib/workout'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'
import UpgradeVipModal from './UpgradeVipModal'

// 自主訓練（P1，VIP 專屬）：課表庫依分類列出，選配速等級後即時解析出總距離/預估時間；
// 「開始訓練」把解析後的分段橋接給 /track（sessionStorage，見 lib/workout.ts saveFreetrainWorkout）→
// 帶到 GPS 追蹤跑。不是挑戰制：跑步照常走 GPS 上傳自動發里程 EXP，不額外發星數/獎勵。
const CATEGORY_LABELS: Record<string, string> = {
  recovery: '恢復', easy: '輕鬆', lsd: '長距離 LSD', tempo: '節奏', threshold: '閾值',
  progression: '漸速', interval: '間歇', fartlek: '法特雷克', pyramid: '金字塔',
  norwegian: '挪威 4×4', yasso: '亞索 800', rep: '重複跑',
}

export default function TrainingScreen({ onBack }: { onBack: () => void }) {
  const user = useUser()
  const uid = user?.id ?? null
  const { data, error } = useSWR(
    uid && getUserToken() ? ['training-templates', uid] : null,
    () => withUserAuth((t) => trainingApi.templates(t)),
  )
  const vipLocked = !!error && error?.status === 403 && error?.message === 'vip_only'
  const loadFailed = !!error && !vipLocked

  const [levelId, setLevelId] = useState<number | null>(null)
  const [navigating, setNavigating] = useState(false)
  const [showUpgrade, setShowUpgrade] = useState(false)

  const levels = useMemo(() => data?.pace_levels ?? [], [data])
  const level: PaceLevel | null = useMemo(() => {
    if (!levels.length) return null
    if (levelId != null) return levels.find((l) => l.id === levelId) ?? null
    return levels.find((l) => l.id === 5) ?? levels[Math.floor(levels.length / 2)]
  }, [levels, levelId])

  // 依 category 分組，保留課表庫原本的 sort_order
  const groups = useMemo(() => {
    const map = new Map<string, WorkoutTemplate[]>()
    for (const t of data?.templates ?? []) {
      const arr = map.get(t.category)
      if (arr) arr.push(t)
      else map.set(t.category, [t])
    }
    return Array.from(map.entries())
  }, [data])

  // 「開始訓練」：解析成 WorkoutSegment[] → 橋接給 /track → 導頁開跑（比照個人任務/探索的轉場淡出）
  function startTemplate(t: WorkoutTemplate) {
    if (!level) return
    const segments = resolveTemplate(t.segments, level)
    saveFreetrainWorkout(t.code, t.name, segments)
    setNavigating(true)
    setTimeout(() => { window.location.href = '/track' }, 380)
  }

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>🏃 自主訓練</span>
      </header>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '6px 18px 28px' }}>
        {!user ? (
          <div style={emptyBox}>請先登入以使用自主訓練</div>
        ) : vipLocked ? (
          <div style={emptyBox}>
            <div style={{ fontSize: 32 }}>🔒</div>
            <div style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)', marginTop: 8 }}>VIP 專屬功能</div>
            <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 6, lineHeight: 1.7 }}>
              自主訓練提供完整課表庫（恢復／輕鬆／節奏／閾值／間歇…）與配速等級解析，<br />升級 VIP 即可解鎖，依你的能力自訂訓練。
            </div>
            <button onClick={() => setShowUpgrade(true)} style={{ marginTop: 14, background: 'var(--gold)', color: '#fff', fontWeight: 800, border: 'none', borderRadius: 10, padding: '10px 22px', fontSize: 13.5, cursor: 'pointer' }}>✦ 升級 VIP</button>
          </div>
        ) : loadFailed ? (
          <div style={emptyBox}>課表庫載入失敗，請稍後再試</div>
        ) : !data ? (
          <div style={emptyBox}>載入中…</div>
        ) : (
          <>
            <p style={{ fontSize: 12.5, color: 'var(--tx-dim)', margin: '4px 2px 12px', lineHeight: 1.7 }}>
              選擇配速等級，課表庫即自動換算成你的實際配速。挑一份「開始訓練」帶到 GPS 追蹤跑——完成即照常記錄跑步、累計里程 EXP。
            </p>

            {/* 配速等級選擇 */}
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 14px', marginBottom: 16 }}>
              <span style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--tx)', flexShrink: 0 }}>配速等級</span>
              <select
                value={level?.id ?? ''}
                onChange={(e) => setLevelId(Number(e.target.value))}
                style={levelSelect}
              >
                {levels.map((l) => <option key={l.id} value={l.id}>Lv.{l.id} · {l.label}</option>)}
              </select>
            </div>

            {groups.length === 0 && <div style={emptyBox}>目前尚無課表庫內容</div>}

            <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
              {groups.map(([cat, templates]) => (
                <div key={cat}>
                  <div style={{ fontSize: 12, fontWeight: 800, color: 'var(--fug)', marginBottom: 8, letterSpacing: '.05em' }}>{CATEGORY_LABELS[cat] || cat}</div>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                    {templates.map((t) => {
                      const resolved = level ? resolveTemplate(t.segments, level) : []
                      return (
                        <div key={t.code} style={tplCard}>
                          <div style={{ fontSize: 14.5, fontWeight: 800, color: 'var(--tx)' }}>{t.name}</div>
                          {t.description && <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.6 }}>{t.description}</div>}
                          {segSummary(resolved) && <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginTop: 8, lineHeight: 1.6, padding: '7px 10px', background: 'var(--bg-2)', borderRadius: 8 }}>📋 {segSummary(resolved)}</div>}
                          {targetPaceBand(resolved) && <div style={{ fontSize: 12, color: 'var(--fug)', fontWeight: 700, marginTop: 6, fontVariantNumeric: 'tabular-nums' }}>🎯 目標配速 {targetPaceBand(resolved)}</div>}
                          <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 5, fontVariantNumeric: 'tabular-nums' }}>總距離 {totalKm(resolved)} K · 預估 {fmtDuration(estMinutes(resolved))}</div>
                          <button onClick={() => startTemplate(t)} disabled={!level} style={{ ...startBtn, opacity: level ? 1 : 0.5 }}>▶ 開始訓練</button>
                        </div>
                      )
                    })}
                  </div>
                </div>
              ))}
            </div>
          </>
        )}
      </div>

      {navigating && (
        <div style={{ position: 'absolute', inset: 0, zIndex: 3400, background: 'var(--bg)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 14, animation: 'fadeIn .3s ease' }}>
          <div style={{ fontSize: 30 }}>🏃‍♂️</div>
          <div style={{ fontSize: 14, fontWeight: 800, color: 'var(--tx)' }}>前往 GPS 跑步追蹤…</div>
          <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>準備開始自主訓練</div>
        </div>
      )}

      {showUpgrade && <UpgradeVipModal onClose={() => setShowUpgrade(false)} />}
    </div>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }
const emptyBox: React.CSSProperties = { color: 'var(--tx-dim)', fontSize: 13.5, lineHeight: 1.9, padding: '32px 10px', textAlign: 'center' }
const levelSelect: React.CSSProperties = { flex: 1, minWidth: 0, background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '8px 10px', color: 'var(--tx)', fontSize: 13, fontFamily: 'inherit' }
const tplCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '11px 13px' }
const startBtn: React.CSSProperties = { marginTop: 10, width: '100%', background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '9px 0', cursor: 'pointer', fontSize: 13.5, fontFamily: 'inherit' }
