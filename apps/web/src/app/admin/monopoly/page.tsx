'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import { useRouter } from 'next/navigation'
import {
  adminMonopolyApi, adminImagesApi,
  type PoolEntry, type PoolEntryInput, type MonopolyPool, type MonopolyRewardType,
  type RedeemBatch, type RedeemCode, type AdminKnowledgeCard, type MonopolySettings,
  type AdminStickerGallery, type AdminStickerPiece, type FigureRedemption, type FigureRedemptionStatus,
} from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import GpCoin from '@/components/GpCoin'
import DpCoin from '@/components/DpCoin'

// 環台大富翁後台（C2）：獎勵池 / 兌換碼 / 知識卡 / 抽卡設定，四分頁。串接 C1 的 /admin/monopoly。

const REWARD_TYPE_LABEL: Record<MonopolyRewardType, string> = {
  gp: 'GP 點數',
  dp: 'DP 點數',
  vip_days: 'VIP 天數',
  knowledge_card: '知識卡（隨機）',
  sticker: '公仔貼紙（隨機）',
  redemption_code: '兌換碼（從批次抽）',
}
const REWARD_TYPES: MonopolyRewardType[] = ['gp', 'dp', 'vip_days', 'knowledge_card', 'sticker', 'redemption_code']
const POOL_LABEL: Record<MonopolyPool, string> = { chance: '機會', destiny: '命運' }

type Tab = 'pool' | 'redeem' | 'cards' | 'stickers' | 'redemptions' | 'settings'

const REDEMPTION_STATUS_LABEL: Record<FigureRedemptionStatus, string> = {
  pending: '待處理',
  fulfilled: '已寄出',
  rejected: '已拒絕',
}
const REDEMPTION_STATUSES: FigureRedemptionStatus[] = ['pending', 'fulfilled', 'rejected']

export default function AdminMonopolyPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [tab, setTab] = useState<Tab>('pool')
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const [entries, setEntries] = useState<PoolEntry[] | null>(null)
  const [batches, setBatches] = useState<RedeemBatch[] | null>(null)
  const [cards, setCards] = useState<AdminKnowledgeCard[] | null>(null)
  const [stickers, setStickers] = useState<AdminStickerGallery | null>(null)
  const [redemptions, setRedemptions] = useState<FigureRedemption[] | null>(null)
  const [settings, setSettings] = useState<MonopolySettings | null>(null)

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    const onErr = (e: any) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
      else if (e?.status === 403) setErr('無「環台大富翁」權限')
      else setErr(e?.message || '載入失敗')
    }
    adminMonopolyApi.poolList(t).then((r) => setEntries(r.entries)).catch(onErr)
    adminMonopolyApi.redeemBatches(t).then((r) => setBatches(r.batches)).catch(onErr)
    adminMonopolyApi.cards(t).then((r) => setCards(r.cards)).catch(onErr)
    adminMonopolyApi.stickers(t).then((r) => setStickers(r)).catch(onErr)
    adminMonopolyApi.redemptions(t).then((r) => setRedemptions(r.redemptions)).catch(onErr)
    adminMonopolyApi.settings(t).then((r) => setSettings(r)).catch(onErr)
  }, [router])
  useEffect(() => { load() }, [load])

  function flash(m: string) { setMsg(m); setErr(''); setTimeout(() => setMsg(''), 3500) }

  return (
    <div style={{ maxWidth: 1000, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>環台大富翁 · 後台管理</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: '0 0 14px', lineHeight: 1.7 }}>
        管理機會／命運抽卡獎勵池、兌換碼批次、知識卡（補圖／稀有度／上下架）、重複卡與兌換碼耗盡的補償設定。
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '8px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '8px 0', fontSize: 13 }}>{msg}</div>}

      {/* Tabs */}
      <div style={{ display: 'flex', gap: 6, marginBottom: 16, borderBottom: '1px solid var(--line)', flexWrap: 'wrap' }}>
        {([
          ['pool', `獎勵池 (${entries?.length ?? '—'})`],
          ['redeem', `兌換碼 (${batches?.length ?? '—'})`],
          ['cards', `知識卡 (${cards?.length ?? '—'})`],
          ['stickers', `公仔貼紙 (${stickers?.pieces.length ?? '—'})`],
          ['redemptions', `公仔兌換 (${redemptions?.length ?? '—'})`],
          ['settings', '設定'],
        ] as [Tab, string][]).map(([v, label]) => (
          <button
            key={v}
            onClick={() => setTab(v)}
            style={{
              padding: '8px 14px', border: 'none', background: 'none', cursor: 'pointer', fontSize: 14,
              color: tab === v ? 'var(--tx)' : 'var(--tx-dim)',
              borderBottom: tab === v ? '2px solid var(--fug)' : '2px solid transparent',
              fontWeight: tab === v ? 700 : 400,
            }}
          >
            {label}
          </button>
        ))}
      </div>

      {tab === 'pool' && token && (
        <PoolTab
          token={token}
          entries={entries}
          batches={batches ?? []}
          onCreated={(e) => setEntries((es) => [...(es ?? []), e])}
          onSaved={(e) => setEntries((es) => es?.map((x) => (x.id === e.id ? e : x)) ?? es)}
          onDeleted={(id) => setEntries((es) => es?.filter((x) => x.id !== id) ?? es)}
          onErr={setErr}
          flash={flash}
        />
      )}
      {tab === 'redeem' && token && (
        <RedeemTab
          token={token}
          batches={batches}
          reloadBatches={() => adminMonopolyApi.redeemBatches(token).then((r) => setBatches(r.batches)).catch((e) => setErr(e?.message || '載入失敗'))}
          onErr={setErr}
          flash={flash}
        />
      )}
      {tab === 'cards' && token && (
        <CardsTab
          token={token}
          cards={cards}
          onUpdated={(c) => setCards((cs) => cs?.map((x) => (x.id === c.id ? c : x)) ?? cs)}
          onErr={setErr}
        />
      )}
      {tab === 'stickers' && token && stickers && (
        <StickersTab
          token={token}
          gallery={stickers}
          onFigureSaved={(s) => {
            setStickers((g) => (g ? { ...g, figure_color_url: s.figure_color_url, figure_title: s.figure_title, line_oa: s.line_oa, landing_url: s.landing_url } : g))
            flash('✓ 彩圖設定已儲存')
          }}
          onPieceUpdated={(p) => setStickers((g) => (g ? { ...g, pieces: g.pieces.map((x) => (x.id === p.id ? p : x)) } : g))}
          onErr={setErr}
        />
      )}
      {tab === 'stickers' && !stickers && <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
      {tab === 'redemptions' && token && (
        <RedemptionsTab
          token={token}
          redemptions={redemptions}
          onUpdated={(r) => setRedemptions((rs) => rs?.map((x) => (x.id === r.id ? r : x)) ?? rs)}
          onErr={setErr}
          flash={flash}
        />
      )}
      {tab === 'settings' && token && settings && (
        <SettingsTab
          token={token}
          settings={settings}
          onSaved={(s) => { setSettings(s); flash('✓ 設定已儲存') }}
          onErr={setErr}
        />
      )}
      {tab === 'settings' && !settings && <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
    </div>
  )
}

// ============================== 分頁 a：獎勵池 ==============================

function PoolTab({ token, entries, batches, onCreated, onSaved, onDeleted, onErr, flash }: {
  token: string
  entries: PoolEntry[] | null
  batches: RedeemBatch[]
  onCreated: (e: PoolEntry) => void
  onSaved: (e: PoolEntry) => void
  onDeleted: (id: string) => void
  onErr: (m: string) => void
  flash: (m: string) => void
}) {
  if (!entries) return <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      {(['chance', 'destiny'] as MonopolyPool[]).map((p) => (
        <PoolSection
          key={p}
          token={token}
          poolKey={p}
          entries={entries.filter((e) => e.pool === p).sort((a, b) => a.sort_order - b.sort_order)}
          batches={batches}
          onCreated={onCreated}
          onSaved={onSaved}
          onDeleted={onDeleted}
          onErr={onErr}
          flash={flash}
        />
      ))}
    </div>
  )
}

function PoolSection({ token, poolKey, entries, batches, onCreated, onSaved, onDeleted, onErr, flash }: {
  token: string
  poolKey: MonopolyPool
  entries: PoolEntry[]
  batches: RedeemBatch[]
  onCreated: (e: PoolEntry) => void
  onSaved: (e: PoolEntry) => void
  onDeleted: (id: string) => void
  onErr: (m: string) => void
  flash: (m: string) => void
}) {
  const activeWeightSum = entries.filter((e) => e.is_active).reduce((s, e) => s + (e.weight || 0), 0)
  function probText(e: PoolEntry): string {
    if (!e.is_active || activeWeightSum <= 0) return '—'
    return `${((e.weight / activeWeightSum) * 100).toFixed(1)}%`
  }

  const [draft, setDraft] = useState<PoolEntryInput>(() => ({
    pool: poolKey, reward_type: 'gp', weight: 1, amount: 1, redemption_batch_key: '', note: '', is_active: true, sort_order: entries.length,
  }))
  const [adding, setAdding] = useState(false)

  async function addEntry() {
    setAdding(true)
    try {
      const e = await adminMonopolyApi.poolCreate(token, draft)
      onCreated(e)
      setDraft({ pool: poolKey, reward_type: 'gp', weight: 1, amount: 1, redemption_batch_key: '', note: '', is_active: true, sort_order: entries.length + 1 })
      flash(`✓ 已新增到「${POOL_LABEL[poolKey]}」池`)
    } catch (e: any) { onErr(e?.message || '新增失敗') } finally { setAdding(false) }
  }

  return (
    <div style={panel}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginBottom: 10 }}>
        <h2 style={{ fontSize: 16, fontWeight: 800, margin: 0 }}>{POOL_LABEL[poolKey]}池</h2>
        <span style={{ fontSize: 11.5, color: 'var(--tx-faint)' }}>共 {entries.length} 項 · 啟用中權重合計 {activeWeightSum}</span>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {entries.map((e) => (
          <PoolEntryRow key={e.id} token={token} entry={e} probText={probText(e)} batches={batches} onSaved={onSaved} onDeleted={onDeleted} onErr={onErr} flash={flash} />
        ))}
        {entries.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>尚無獎勵項目。</div>}
      </div>

      {/* 新增項目 */}
      <div style={{ marginTop: 14, paddingTop: 12, borderTop: '1px solid var(--line)' }}>
        <div style={{ fontSize: 12.5, fontWeight: 700, marginBottom: 8 }}>＋ 新增獎勵項目</div>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'flex-end' }}>
          <F label="獎勵類型">
            <select style={inp} value={draft.reward_type} onChange={(ev) => setDraft((d) => ({ ...d, reward_type: ev.target.value as MonopolyRewardType }))}>
              {REWARD_TYPES.map((rt) => <option key={rt} value={rt}>{REWARD_TYPE_LABEL[rt]}</option>)}
            </select>
          </F>
          <F label="權重"><input style={{ ...inp, width: 80 }} type="number" min={0} value={draft.weight} onChange={(ev) => setDraft((d) => ({ ...d, weight: parseInt(ev.target.value || '0', 10) }))} /></F>
          {(draft.reward_type === 'gp' || draft.reward_type === 'dp' || draft.reward_type === 'vip_days') && (
            <F label="數量（需 > 0）"><input style={{ ...inp, width: 90 }} type="number" min={1} value={draft.amount} onChange={(ev) => setDraft((d) => ({ ...d, amount: parseInt(ev.target.value || '0', 10) }))} /></F>
          )}
          {draft.reward_type === 'redemption_code' && (
            <F label="兌換碼批次">
              <select style={inp} value={draft.redemption_batch_key} onChange={(ev) => setDraft((d) => ({ ...d, redemption_batch_key: ev.target.value }))}>
                <option value="">請選擇批次</option>
                {batches.map((b) => <option key={b.batch_key} value={b.batch_key}>{b.batch_key}（{b.label}）剩 {b.remaining}</option>)}
              </select>
            </F>
          )}
          {(draft.reward_type === 'knowledge_card' || draft.reward_type === 'sticker') && (
            <span style={{ fontSize: 11.5, color: 'var(--tx-faint)', paddingBottom: 8 }}>此類型隨機從卡池/貼紙庫抽出，無需設定數量</span>
          )}
          <F label="備註"><input style={{ ...inp, width: 140 }} value={draft.note} onChange={(ev) => setDraft((d) => ({ ...d, note: ev.target.value }))} /></F>
          <F label="排序"><input style={{ ...inp, width: 70 }} type="number" value={draft.sort_order} onChange={(ev) => setDraft((d) => ({ ...d, sort_order: parseInt(ev.target.value || '0', 10) }))} /></F>
          <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12.5, paddingBottom: 9 }}>
            <input type="checkbox" checked={draft.is_active} onChange={(ev) => setDraft((d) => ({ ...d, is_active: ev.target.checked }))} /> 啟用
          </label>
          <button onClick={addEntry} disabled={adding} style={{ ...primaryBtn, marginBottom: 1 }}>{adding ? '新增中…' : '新增'}</button>
        </div>
      </div>
    </div>
  )
}

function PoolEntryRow({ token, entry, probText, batches, onSaved, onDeleted, onErr, flash }: {
  token: string
  entry: PoolEntry
  probText: string
  batches: RedeemBatch[]
  onSaved: (e: PoolEntry) => void
  onDeleted: (id: string) => void
  onErr: (m: string) => void
  flash: (m: string) => void
}) {
  const [local, setLocal] = useState<PoolEntry>(entry)
  const [saving, setSaving] = useState(false)
  useEffect(() => { setLocal(entry) }, [entry])

  function set<K extends keyof PoolEntry>(k: K, v: PoolEntry[K]) { setLocal((l) => ({ ...l, [k]: v })) }

  async function save() {
    setSaving(true)
    try {
      const { id, ...body } = local
      const updated = await adminMonopolyApi.poolUpdate(token, id, body)
      onSaved(updated)
      flash('✓ 已儲存')
    } catch (e: any) { onErr(e?.message || '儲存失敗') } finally { setSaving(false) }
  }
  async function del() {
    if (!confirm('確定刪除此獎勵項目？')) return
    try { await adminMonopolyApi.poolDelete(token, entry.id); onDeleted(entry.id) }
    catch (e: any) { onErr(e?.message || '刪除失敗') }
  }

  return (
    <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'flex-end', background: 'var(--bg-0, #0d0f14)', border: '1px solid var(--line-2)', borderRadius: 8, padding: 8 }}>
      <F label="獎勵類型">
        <select style={inp} value={local.reward_type} onChange={(ev) => set('reward_type', ev.target.value as MonopolyRewardType)}>
          {REWARD_TYPES.map((rt) => <option key={rt} value={rt}>{REWARD_TYPE_LABEL[rt]}</option>)}
        </select>
      </F>
      <F label="權重"><input style={{ ...inp, width: 70 }} type="number" min={0} value={local.weight} onChange={(ev) => set('weight', parseInt(ev.target.value || '0', 10))} /></F>
      <F label="機率">
        <div style={{ ...inp, width: 70, background: 'transparent', border: 'none', color: 'var(--fug)', fontWeight: 700, padding: '9px 0' }}>{probText}</div>
      </F>
      {(local.reward_type === 'gp' || local.reward_type === 'dp' || local.reward_type === 'vip_days') && (
        <F label={`數量${local.reward_type === 'gp' ? ' GP' : local.reward_type === 'dp' ? ' DP' : ''}（需 > 0）`}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
            {local.reward_type === 'gp' && <GpCoin size={14} />}
            {local.reward_type === 'dp' && <DpCoin size={14} />}
            <input style={{ ...inp, width: 80 }} type="number" min={1} value={local.amount} onChange={(ev) => set('amount', parseInt(ev.target.value || '0', 10))} />
          </div>
        </F>
      )}
      {local.reward_type === 'redemption_code' && (
        <F label="兌換碼批次">
          <select style={inp} value={local.redemption_batch_key} onChange={(ev) => set('redemption_batch_key', ev.target.value)}>
            <option value="">請選擇批次</option>
            {batches.map((b) => <option key={b.batch_key} value={b.batch_key}>{b.batch_key}（{b.label}）剩 {b.remaining}</option>)}
          </select>
        </F>
      )}
      <F label="備註"><input style={{ ...inp, width: 130 }} value={local.note} onChange={(ev) => set('note', ev.target.value)} /></F>
      <F label="排序"><input style={{ ...inp, width: 60 }} type="number" value={local.sort_order} onChange={(ev) => set('sort_order', parseInt(ev.target.value || '0', 10))} /></F>
      <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12.5, paddingBottom: 9 }}>
        <input type="checkbox" checked={local.is_active} onChange={(ev) => set('is_active', ev.target.checked)} /> 啟用
      </label>
      <button onClick={save} disabled={saving} style={{ ...ghostBtn, marginBottom: 1 }}>{saving ? '儲存中…' : '儲存'}</button>
      <button onClick={del} style={{ ...ghostBtn, color: 'var(--hunt)', marginBottom: 1 }}>刪除</button>
    </div>
  )
}

// ============================== 分頁 b：兌換碼 ==============================

function RedeemTab({ token, batches, reloadBatches, onErr, flash }: {
  token: string
  batches: RedeemBatch[] | null
  reloadBatches: () => void
  onErr: (m: string) => void
  flash: (m: string) => void
}) {
  const [batchKey, setBatchKey] = useState('')
  const [kind, setKind] = useState<'line_point' | 'coupon'>('line_point')
  const [label, setLabel] = useState('')
  const [codesText, setCodesText] = useState('')
  const [saving, setSaving] = useState(false)
  const [result, setResult] = useState<{ inserted: number; skipped: number } | null>(null)

  const [expanded, setExpanded] = useState<string | null>(null)
  const [codesCache, setCodesCache] = useState<Record<string, RedeemCode[]>>({})
  const [loadingCodes, setLoadingCodes] = useState('')

  async function submit() {
    setResult(null)
    if (!batchKey.trim()) { onErr('請填批次代碼 batch_key'); return }
    if (!codesText.trim()) { onErr('請貼上至少一行兌換碼'); return }
    setSaving(true)
    try {
      const r = await adminMonopolyApi.redeemCreateBatch(token, { batch_key: batchKey.trim(), kind, label: label.trim(), codes_text: codesText })
      setResult(r)
      setCodesText('')
      flash(`✓ 新增 ${r.inserted} 筆，略過重複 ${r.skipped} 筆`)
      reloadBatches()
      // 若正展開這個批次，重新抓一次碼清單
      if (expanded === batchKey.trim()) {
        const { codes } = await adminMonopolyApi.redeemBatchCodes(token, batchKey.trim())
        setCodesCache((c) => ({ ...c, [batchKey.trim()]: codes }))
      }
    } catch (e: any) { onErr(e?.message || '新增失敗') } finally { setSaving(false) }
  }

  async function toggleExpand(key: string) {
    if (expanded === key) { setExpanded(null); return }
    setExpanded(key)
    if (!codesCache[key]) {
      setLoadingCodes(key)
      try {
        const { codes } = await adminMonopolyApi.redeemBatchCodes(token, key)
        setCodesCache((c) => ({ ...c, [key]: codes }))
      } catch (e: any) { onErr(e?.message || '載入碼清單失敗') } finally { setLoadingCodes('') }
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
      {/* 批次列表 */}
      <div style={panel}>
        <h2 style={{ fontSize: 16, fontWeight: 800, margin: '0 0 10px' }}>兌換碼批次</h2>
        {!batches && <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
        {batches && batches.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>尚無批次，請於下方新增。</div>}
        {batches && batches.length > 0 && (
          <div style={{ border: '1px solid var(--line)', borderRadius: 10, overflow: 'hidden' }}>
            <Row head><C w={2}>batch_key</C><C w={1}>kind</C><C w={2}>label</C><C w={1}>total</C><C w={1}>used</C><C w={1}>remaining</C><C w={1}> </C></Row>
            {batches.map((b) => (
              <div key={b.batch_key}>
                <Row>
                  <C w={2} mono>{b.batch_key}</C>
                  <C w={1}>{b.kind === 'line_point' ? 'LINE 點數' : '折價券'}</C>
                  <C w={2} dim>{b.label}</C>
                  <C w={1}>{b.total}</C>
                  <C w={1}>{b.used}</C>
                  <C w={1}>{b.remaining}</C>
                  <C w={1}><button onClick={() => toggleExpand(b.batch_key)} style={linkBtn}>{expanded === b.batch_key ? '收合' : '查看碼'}</button></C>
                </Row>
                {expanded === b.batch_key && (
                  <div style={{ padding: '10px 16px 14px', background: 'var(--bg-0, #0d0f14)', borderBottom: '1px solid var(--line)' }}>
                    {loadingCodes === b.batch_key && <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>載入中…</div>}
                    {codesCache[b.batch_key] && (
                      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, maxHeight: 220, overflowY: 'auto' }}>
                        {codesCache[b.batch_key].map((c) => (
                          <span key={c.code} style={{
                            fontFamily: 'monospace', fontSize: 12, padding: '3px 8px', borderRadius: 6,
                            background: c.is_used ? 'rgba(255,255,255,.05)' : 'rgba(45,212,150,.12)',
                            color: c.is_used ? 'var(--tx-faint)' : 'var(--fug)',
                            border: `1px solid ${c.is_used ? 'var(--line-2)' : 'var(--fug)'}`,
                            textDecoration: c.is_used ? 'line-through' : 'none',
                          }} title={c.used_at ? `使用於 ${new Date(c.used_at).toLocaleString('zh-TW')}` : '未使用'}>
                            {c.code}
                          </span>
                        ))}
                        {codesCache[b.batch_key].length === 0 && <span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>（空）</span>}
                      </div>
                    )}
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </div>

      {/* 新增兌換碼到批次 */}
      <div style={panel}>
        <h2 style={{ fontSize: 16, fontWeight: 800, margin: '0 0 10px' }}>新增兌換碼到批次</h2>
        <p style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 0 }}>
          batch_key 若已存在則追加碼到同一批次（同批次內重複碼會被自動略過）；若是新代碼則建立新批次。
        </p>
        <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap' }}>
          <F label="batch_key"><input style={inp} value={batchKey} onChange={(e) => setBatchKey(e.target.value)} placeholder="如 LINE_2026_Q1" /></F>
          <F label="kind">
            <select style={inp} value={kind} onChange={(e) => setKind(e.target.value as 'line_point' | 'coupon')}>
              <option value="line_point">LINE 點數</option>
              <option value="coupon">折價券</option>
            </select>
          </F>
          <F label="label（顯示名稱）"><input style={inp} value={label} onChange={(e) => setLabel(e.target.value)} placeholder="如 LINE Points 100 點" /></F>
        </div>
        <F label="codes_text（每行一碼）">
          <textarea style={{ ...ta, fontFamily: 'monospace' }} rows={8} value={codesText} onChange={(e) => setCodesText(e.target.value)} placeholder={'CODE0001\nCODE0002\nCODE0003'} />
        </F>
        {result && (
          <div style={{ fontSize: 13, color: 'var(--fug)', marginTop: 6 }}>
            ✓ 新增 {result.inserted} 筆，略過重複 {result.skipped} 筆
          </div>
        )}
        <button onClick={submit} disabled={saving} style={{ ...primaryBtn, marginTop: 12 }}>{saving ? '匯入中…' : '匯入兌換碼'}</button>
      </div>
    </div>
  )
}

// ============================== 分頁 c：知識卡 ==============================

const PAGE_SIZE = 40

function CardsTab({ token, cards, onUpdated, onErr }: {
  token: string
  cards: AdminKnowledgeCard[] | null
  onUpdated: (c: AdminKnowledgeCard) => void
  onErr: (m: string) => void
}) {
  const [theme, setTheme] = useState<'all' | 'training' | 'care'>('all')
  const [q, setQ] = useState('')
  const [page, setPage] = useState(1)

  const filtered = useMemo(() => {
    if (!cards) return []
    const qq = q.trim().toLowerCase()
    return cards.filter((c) => {
      if (theme !== 'all' && c.theme !== theme) return false
      if (qq && !c.code.toLowerCase().includes(qq) && !c.title.toLowerCase().includes(qq)) return false
      return true
    })
  }, [cards, theme, q])

  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))
  const curPage = Math.min(page, totalPages)
  const pageItems = filtered.slice((curPage - 1) * PAGE_SIZE, curPage * PAGE_SIZE)

  if (!cards) return <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>

  return (
    <div style={panel}>
      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end', marginBottom: 14 }}>
        <F label="主題">
          <select style={inp} value={theme} onChange={(e) => { setTheme(e.target.value as any); setPage(1) }}>
            <option value="all">全部</option>
            <option value="training">機會 · 訓練知識</option>
            <option value="care">命運 · 照護知識</option>
          </select>
        </F>
        <F label="搜尋（標題或代碼）">
          <input style={{ ...inp, width: 220 }} value={q} onChange={(e) => { setQ(e.target.value); setPage(1) }} placeholder="輸入代碼或標題關鍵字" />
        </F>
        <span style={{ fontSize: 12, color: 'var(--tx-faint)', paddingBottom: 9 }}>
          共 {cards.length} 張，符合 {filtered.length} 張
        </span>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))', gap: 10 }}>
        {pageItems.map((c) => (
          <CardRow key={c.id} token={token} card={c} onUpdated={onUpdated} onErr={onErr} />
        ))}
      </div>
      {pageItems.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', padding: '12px 0' }}>沒有符合的卡片。</div>}

      {totalPages > 1 && (
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', justifyContent: 'center', marginTop: 16 }}>
          <button onClick={() => setPage((p) => Math.max(1, p - 1))} disabled={curPage <= 1} style={{ ...ghostBtn, opacity: curPage <= 1 ? 0.5 : 1 }}>上一頁</button>
          <span style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>第 {curPage} / {totalPages} 頁</span>
          <button onClick={() => setPage((p) => Math.min(totalPages, p + 1))} disabled={curPage >= totalPages} style={{ ...ghostBtn, opacity: curPage >= totalPages ? 0.5 : 1 }}>下一頁</button>
        </div>
      )}
    </div>
  )
}

function CardRow({ token, card, onUpdated, onErr }: {
  token: string
  card: AdminKnowledgeCard
  onUpdated: (c: AdminKnowledgeCard) => void
  onErr: (m: string) => void
}) {
  const [imgBusy, setImgBusy] = useState(false)
  const [savingRarity, setSavingRarity] = useState(false)
  const [savingActive, setSavingActive] = useState(false)

  async function uploadImage(file: File) {
    setImgBusy(true)
    try {
      const { url } = await adminImagesApi.upload(token, file)
      const updated = await adminMonopolyApi.updateCard(token, card.id, { image_url: url })
      onUpdated(updated)
    } catch (e: any) { onErr(e?.message || '上傳失敗') } finally { setImgBusy(false) }
  }
  async function setRarity(rarity: 'common' | 'rare') {
    setSavingRarity(true)
    try { const updated = await adminMonopolyApi.updateCard(token, card.id, { rarity }); onUpdated(updated) }
    catch (e: any) { onErr(e?.message || '儲存失敗') } finally { setSavingRarity(false) }
  }
  async function setActive(is_active: boolean) {
    setSavingActive(true)
    try { const updated = await adminMonopolyApi.updateCard(token, card.id, { is_active }); onUpdated(updated) }
    catch (e: any) { onErr(e?.message || '儲存失敗') } finally { setSavingActive(false) }
  }

  return (
    <div style={{ background: 'var(--bg-0, #0d0f14)', border: '1px solid var(--line-2)', borderRadius: 10, padding: 10, opacity: card.is_active ? 1 : 0.6 }}>
      <div style={{ display: 'flex', gap: 8 }}>
        <div style={{ width: 52, height: 68, borderRadius: 6, overflow: 'hidden', background: 'var(--bg-2)', flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          {card.image_url ? <img src={card.image_url} alt="" style={{ width: '100%', height: '100%', objectFit: 'cover' }} /> : <span style={{ fontSize: 9, color: 'var(--tx-faint)' }}>無圖</span>}
        </div>
        <div style={{ minWidth: 0, flex: 1 }}>
          <div style={{ fontSize: 12.5, fontWeight: 700, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{card.title || '（未命名）'}</div>
          <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', fontFamily: 'monospace' }}>{card.code}</div>
          <div style={{ fontSize: 10.5, color: 'var(--tx-dim)' }}>{card.theme === 'training' ? '訓練' : '照護'} · {card.main_category}</div>
        </div>
      </div>
      <div style={{ display: 'flex', gap: 6, alignItems: 'center', marginTop: 8, flexWrap: 'wrap' }}>
        <select style={{ ...inp, width: 78, padding: '5px 6px', fontSize: 11.5 }} value={card.rarity} disabled={savingRarity} onChange={(e) => setRarity(e.target.value as 'common' | 'rare')}>
          <option value="common">common</option>
          <option value="rare">rare</option>
        </select>
        <label style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: 11 }}>
          <input type="checkbox" checked={card.is_active} disabled={savingActive} onChange={(e) => setActive(e.target.checked)} /> 上架
        </label>
      </div>
      <label style={{ ...ghostBtn, display: 'block', textAlign: 'center', marginTop: 8, cursor: imgBusy ? 'default' : 'pointer', opacity: imgBusy ? 0.6 : 1, fontSize: 11.5, padding: '6px 8px' }}>
        {imgBusy ? '上傳中…' : card.image_url ? '更換圖片' : '上傳圖片'}
        <input type="file" accept="image/*" disabled={imgBusy} style={{ display: 'none' }} onChange={(e) => { const f = e.target.files?.[0]; if (f) uploadImage(f); e.target.value = '' }} />
      </label>
    </div>
  )
}

// ============================== 分頁 d：公仔貼紙 ==============================

function StickersTab({ token, gallery, onFigureSaved, onPieceUpdated, onErr }: {
  token: string
  gallery: AdminStickerGallery
  onFigureSaved: (s: { figure_color_url: string; figure_title: string; line_oa: string; landing_url: string }) => void
  onPieceUpdated: (p: AdminStickerPiece) => void
  onErr: (m: string) => void
}) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      <FigureSettingsPanel
        token={token}
        figureColorUrl={gallery.figure_color_url}
        figureTitle={gallery.figure_title}
        lineOA={gallery.line_oa}
        landingUrl={gallery.landing_url}
        onSaved={onFigureSaved}
        onErr={onErr}
      />
      <div style={panel}>
        <h2 style={{ fontSize: 16, fontWeight: 800, margin: '0 0 4px' }}>九宮格拼圖（灰階片）</h2>
        <p style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 0, lineHeight: 1.7 }}>
          玩家透過機會／命運抽卡逐片收集，集滿 9 片即解鎖上方彩色完整公仔。
        </p>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(150px, 1fr))', gap: 10 }}>
          {gallery.pieces.map((p) => (
            <StickerPieceRow key={p.id} token={token} piece={p} onUpdated={onPieceUpdated} onErr={onErr} />
          ))}
        </div>
      </div>
    </div>
  )
}

function FigureSettingsPanel({ token, figureColorUrl, figureTitle, lineOA, landingUrl, onSaved, onErr }: {
  token: string
  figureColorUrl: string
  figureTitle: string
  lineOA: string
  landingUrl: string
  onSaved: (s: { figure_color_url: string; figure_title: string; line_oa: string; landing_url: string }) => void
  onErr: (m: string) => void
}) {
  const [url, setUrl] = useState(figureColorUrl)
  const [title, setTitle] = useState(figureTitle)
  const [lineOAValue, setLineOAValue] = useState(lineOA)
  const [landingUrlValue, setLandingUrlValue] = useState(landingUrl)
  const [uploading, setUploading] = useState(false)
  const [saving, setSaving] = useState(false)
  useEffect(() => { setUrl(figureColorUrl); setTitle(figureTitle); setLineOAValue(lineOA); setLandingUrlValue(landingUrl) }, [figureColorUrl, figureTitle, lineOA, landingUrl])

  async function uploadFigure(file: File) {
    setUploading(true)
    try {
      const { url: newUrl } = await adminImagesApi.upload(token, file)
      setUrl(newUrl)
    } catch (e: any) { onErr(e?.message || '上傳失敗') } finally { setUploading(false) }
  }
  async function save() {
    if (!url.trim()) { onErr('請先上傳彩圖'); return }
    if (!title.trim()) { onErr('請填標題'); return }
    setSaving(true)
    try {
      const r = await adminMonopolyApi.setFigureSettings(token, {
        figure_color_url: url.trim(), figure_title: title.trim(),
        line_oa: lineOAValue.trim(), landing_url: landingUrlValue.trim(),
      })
      onSaved(r)
    } catch (e: any) { onErr(e?.message || '儲存失敗') } finally { setSaving(false) }
  }

  return (
    <div style={panel}>
      <h2 style={{ fontSize: 16, fontWeight: 800, margin: '0 0 10px' }}>完整彩圖</h2>
      <div style={{ display: 'flex', gap: 14, flexWrap: 'wrap', alignItems: 'flex-start' }}>
        <div style={{ width: 120, height: 120, borderRadius: 10, overflow: 'hidden', background: 'var(--bg-2)', flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          {url ? <img src={url} alt="" style={{ width: '100%', height: '100%', objectFit: 'cover' }} /> : <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>無圖</span>}
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10, flex: 1, minWidth: 220 }}>
          <label style={{ ...ghostBtn, display: 'inline-block', textAlign: 'center', cursor: uploading ? 'default' : 'pointer', opacity: uploading ? 0.6 : 1, width: 'fit-content' }}>
            {uploading ? '上傳中…' : '更換彩圖'}
            <input type="file" accept="image/*" disabled={uploading} style={{ display: 'none' }} onChange={(e) => { const f = e.target.files?.[0]; if (f) uploadFigure(f); e.target.value = '' }} />
          </label>
          <F label="標題">
            <input style={{ ...inp, width: 260 }} value={title} onChange={(e) => setTitle(e.target.value)} />
          </F>
          <F label="LINE OA">
            <input style={{ ...inp, width: 260 }} value={lineOAValue} onChange={(e) => setLineOAValue(e.target.value)} placeholder="如 @855xfwqe" />
          </F>
          <F label="完賽公仔 Landing URL">
            <input style={{ ...inp, width: 260 }} value={landingUrlValue} onChange={(e) => setLandingUrlValue(e.target.value)} placeholder="https://runner-figure.bravelog.tw/" />
          </F>
          <button onClick={save} disabled={saving} style={{ ...primaryBtn, width: 'fit-content' }}>{saving ? '儲存中…' : '儲存彩圖設定'}</button>
        </div>
      </div>
    </div>
  )
}

function StickerPieceRow({ token, piece, onUpdated, onErr }: {
  token: string
  piece: AdminStickerPiece
  onUpdated: (p: AdminStickerPiece) => void
  onErr: (m: string) => void
}) {
  const [imgBusy, setImgBusy] = useState(false)
  const [savingRarity, setSavingRarity] = useState(false)
  const [savingActive, setSavingActive] = useState(false)

  async function uploadImage(file: File) {
    setImgBusy(true)
    try {
      const { url } = await adminImagesApi.upload(token, file)
      const updated = await adminMonopolyApi.updateSticker(token, piece.id, { image_url: url })
      onUpdated(updated)
    } catch (e: any) { onErr(e?.message || '上傳失敗') } finally { setImgBusy(false) }
  }
  async function setRarity(rarity: 'common' | 'rare') {
    setSavingRarity(true)
    try { const updated = await adminMonopolyApi.updateSticker(token, piece.id, { rarity }); onUpdated(updated) }
    catch (e: any) { onErr(e?.message || '儲存失敗') } finally { setSavingRarity(false) }
  }
  async function setActive(is_active: boolean) {
    setSavingActive(true)
    try { const updated = await adminMonopolyApi.updateSticker(token, piece.id, { is_active }); onUpdated(updated) }
    catch (e: any) { onErr(e?.message || '儲存失敗') } finally { setSavingActive(false) }
  }

  return (
    <div style={{ background: 'var(--bg-0, #0d0f14)', border: '1px solid var(--line-2)', borderRadius: 10, padding: 10, opacity: piece.is_active ? 1 : 0.6 }}>
      <div style={{ display: 'flex', gap: 8 }}>
        <div style={{ width: 52, height: 52, borderRadius: 6, overflow: 'hidden', background: 'var(--bg-2)', flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          {piece.image_url ? <img src={piece.image_url} alt="" style={{ width: '100%', height: '100%', objectFit: 'cover' }} /> : <span style={{ fontSize: 9, color: 'var(--tx-faint)' }}>無圖</span>}
        </div>
        <div style={{ minWidth: 0, flex: 1 }}>
          <div style={{ fontSize: 12.5, fontWeight: 700 }}>第 {piece.position} 片</div>
          <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{piece.title || '（未命名）'}</div>
        </div>
      </div>
      <div style={{ display: 'flex', gap: 6, alignItems: 'center', marginTop: 8, flexWrap: 'wrap' }}>
        <select style={{ ...inp, width: 78, padding: '5px 6px', fontSize: 11.5 }} value={piece.rarity} disabled={savingRarity} onChange={(e) => setRarity(e.target.value as 'common' | 'rare')}>
          <option value="common">common</option>
          <option value="rare">rare</option>
        </select>
        <label style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: 11 }}>
          <input type="checkbox" checked={piece.is_active} disabled={savingActive} onChange={(e) => setActive(e.target.checked)} /> 啟用
        </label>
      </div>
      <label style={{ ...ghostBtn, display: 'block', textAlign: 'center', marginTop: 8, cursor: imgBusy ? 'default' : 'pointer', opacity: imgBusy ? 0.6 : 1, fontSize: 11.5, padding: '6px 8px' }}>
        {imgBusy ? '上傳中…' : piece.image_url ? '更換圖片' : '上傳圖片'}
        <input type="file" accept="image/*" disabled={imgBusy} style={{ display: 'none' }} onChange={(e) => { const f = e.target.files?.[0]; if (f) uploadImage(f); e.target.value = '' }} />
      </label>
    </div>
  )
}

// ============================== 分頁 e：公仔兌換 ==============================

function RedemptionsTab({ token, redemptions, onUpdated, onErr, flash }: {
  token: string
  redemptions: FigureRedemption[] | null
  onUpdated: (r: FigureRedemption) => void
  onErr: (m: string) => void
  flash: (m: string) => void
}) {
  if (!redemptions) return <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>
  return (
    <div style={panel}>
      <h2 style={{ fontSize: 16, fontWeight: 800, margin: '0 0 4px' }}>完賽公仔兌換申請</h2>
      <p style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 0, marginBottom: 14, lineHeight: 1.7 }}>
        玩家集滿 9 片拼圖後送出兌換申請，於此確認寄送狀態；狀態異動即時儲存。
      </p>
      {redemptions.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>目前沒有兌換申請</div>}
      {redemptions.length > 0 && (
        <div style={{ border: '1px solid var(--line)', borderRadius: 10, overflow: 'hidden' }}>
          <Row head><C w={2}>帳號編碼</C><C w={2}>顯示名</C><C w={3}>Email</C><C w={2}>申請時間</C><C w={2}>狀態</C><C w={3}>備註</C></Row>
          {redemptions.map((r) => (
            <RedemptionRow key={r.id} token={token} redemption={r} onUpdated={onUpdated} onErr={onErr} flash={flash} />
          ))}
        </div>
      )}
    </div>
  )
}

function RedemptionRow({ token, redemption, onUpdated, onErr, flash }: {
  token: string
  redemption: FigureRedemption
  onUpdated: (r: FigureRedemption) => void
  onErr: (m: string) => void
  flash: (m: string) => void
}) {
  const [note, setNote] = useState(redemption.note)
  const [saving, setSaving] = useState(false)
  useEffect(() => { setNote(redemption.note) }, [redemption.note])

  async function patch(status: FigureRedemptionStatus, noteValue: string) {
    setSaving(true)
    try {
      const updated = await adminMonopolyApi.updateRedemption(token, redemption.id, { status, note: noteValue })
      onUpdated(updated)
      flash('✓ 已更新')
    } catch (e: any) { onErr(e?.message || '更新失敗') } finally { setSaving(false) }
  }

  return (
    <Row>
      <C w={2} mono>{redemption.account_code}</C>
      <C w={2}>{redemption.nickname}</C>
      <C w={3} dim>{redemption.email}</C>
      <C w={2} dim>{new Date(redemption.created_at).toLocaleString('zh-TW')}</C>
      <div style={{ flex: 2, minWidth: 0 }}>
        <select
          style={{ ...inp, width: '100%', padding: '6px 8px', fontSize: 12.5 }}
          value={redemption.status}
          disabled={saving}
          onChange={(e) => patch(e.target.value as FigureRedemptionStatus, note)}
        >
          {REDEMPTION_STATUSES.map((s) => <option key={s} value={s}>{REDEMPTION_STATUS_LABEL[s]}</option>)}
        </select>
      </div>
      <div style={{ flex: 3, minWidth: 0, display: 'flex', gap: 6 }}>
        <input
          style={{ ...inp, flex: 1, minWidth: 0, padding: '6px 8px', fontSize: 12.5 }}
          value={note}
          placeholder="備註（選填）"
          onChange={(e) => setNote(e.target.value)}
          onBlur={() => { if (note !== redemption.note) patch(redemption.status, note) }}
        />
      </div>
    </Row>
  )
}

// ============================== 分頁 f：設定 ==============================

function SettingsTab({ token, settings, onSaved, onErr }: {
  token: string
  settings: MonopolySettings
  onSaved: (s: MonopolySettings) => void
  onErr: (m: string) => void
}) {
  const [common, setCommon] = useState(settings.dup_gp?.common ?? 5)
  const [rare, setRare] = useState(settings.dup_gp?.rare ?? 20)
  const [fallback, setFallback] = useState(settings.redeem_fallback_gp ?? 10)
  const [saving, setSaving] = useState(false)

  async function save() {
    setSaving(true)
    try {
      const body: MonopolySettings = {
        dup_gp: { ...settings.dup_gp, common: Number(common), rare: Number(rare) },
        redeem_fallback_gp: Number(fallback),
      }
      const r = await adminMonopolyApi.setSettings(token, body)
      onSaved(r)
    } catch (e: any) { onErr(e?.message || '儲存失敗') } finally { setSaving(false) }
  }

  return (
    <div style={panel}>
      <h2 style={{ fontSize: 16, fontWeight: 800, margin: '0 0 4px' }}>抽卡設定</h2>
      <p style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 0, lineHeight: 1.7 }}>
        抽到已擁有的知識卡（重複卡）時，改發對應稀有度的 GP；兌換碼批次庫存耗盡時，改發「兌換碼耗盡補償 GP」。
      </p>
      <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', marginTop: 10 }}>
        <F label="重複卡補償 GP（common）">
          <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <GpCoin size={16} />
            <input style={{ ...inp, width: 100 }} type="number" min={0} value={common} onChange={(e) => setCommon(parseInt(e.target.value || '0', 10))} />
          </div>
        </F>
        <F label="重複卡補償 GP（rare）">
          <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <GpCoin size={16} />
            <input style={{ ...inp, width: 100 }} type="number" min={0} value={rare} onChange={(e) => setRare(parseInt(e.target.value || '0', 10))} />
          </div>
        </F>
        <F label="兌換碼耗盡補償 GP">
          <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <GpCoin size={16} />
            <input style={{ ...inp, width: 100 }} type="number" min={0} value={fallback} onChange={(e) => setFallback(parseInt(e.target.value || '0', 10))} />
          </div>
        </F>
      </div>
      <button onClick={save} disabled={saving} style={{ ...primaryBtn, marginTop: 16 }}>{saving ? '儲存中…' : '儲存設定'}</button>
    </div>
  )
}

// ============================== 共用小元件／樣式 ==============================

function F({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}
function Row({ children, head }: { children: React.ReactNode; head?: boolean }) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 12, padding: '10px 16px', borderBottom: '1px solid var(--line)',
      background: head ? 'var(--bg-1)' : 'transparent', fontSize: head ? 11 : 13.5,
      color: head ? 'var(--tx-faint)' : 'var(--tx)', textTransform: head ? 'uppercase' : 'none',
    }}>{children}</div>
  )
}
function C({ children, w, dim, mono }: { children: React.ReactNode; w: number; dim?: boolean; mono?: boolean }) {
  return <div style={{ flex: w, minWidth: 0, color: dim ? 'var(--tx-dim)' : undefined, fontSize: dim ? 12 : undefined, fontFamily: mono ? 'monospace' : undefined, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{children}</div>
}

const panel: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 18 }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '9px 11px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit' }
const ta: React.CSSProperties = { ...inp, resize: 'vertical', lineHeight: 1.5, width: '100%', boxSizing: 'border-box' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none', borderRadius: 10, padding: '9px 16px', cursor: 'pointer', fontSize: 13.5, fontFamily: 'inherit' }
const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '7px 12px', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit' }
const linkBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--fug)', cursor: 'pointer', fontSize: 13, padding: 0, fontFamily: 'inherit' }
