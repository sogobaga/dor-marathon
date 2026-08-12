'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import {
  adminRacesApi, adminRewardMerchantsApi, adminRewardGroupsApi,
  type Race, type RewardMerchant, type RewardSerialGroup, type RewardSerialGroupWriteBody,
  type RewardSerial, type RewardUseLimitType, type RewardSerialImportResult,
} from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

// 活動獎勵系統 P1：序號庫存管理。合作商家 → 活動序號組（面額/期限/使用次數限制/配發數/對應活動）→ 序號
// （手動貼上或 .csv 匯入、全系統唯一去重、狀態：未發送／已發送／註銷）。設計見 memory activity-reward-system。
// 即時獎勵 roll(P2)、玩家錢包(P3)、到期提醒(P4) 待後續上線，本頁只管序號庫存。

const USE_LIMIT_LABEL: Record<RewardUseLimitType, string> = {
  single: '單次使用',
  repeat: '可重複使用',
  unlimited: '無使用次數限制',
}
const STATUS_LABEL: Record<string, string> = { available: '未發送', issued: '已發送', void: '已註銷' }
const SERIAL_PAGE = 50

type MerchantForm = { id?: string; name: string; note: string }
const EMPTY_MERCHANT: MerchantForm = { name: '', note: '' }

type GroupForm = {
  id?: string
  merchant_id: string
  name: string
  item_label: string
  is_line_point: boolean
  valid_until: string // datetime-local；空=無期限
  use_limit_type: RewardUseLimitType
  use_limit_count: string
  grant_count: string
  applies_all_races: boolean
  race_ids: string[]
  usage_note: string  // 獎勵詳情：使用說明（活動獎勵系統 P2）
  icon_url: string    // 獎勵詳情：獎勵圖示
  description: string // 獎勵詳情：活動/獎勵說明
}
const EMPTY_GROUP: GroupForm = {
  merchant_id: '', name: '', item_label: '', is_line_point: false, valid_until: '',
  use_limit_type: 'single', use_limit_count: '', grant_count: '1', applies_all_races: true, race_ids: [],
  usage_note: '', icon_url: '', description: '',
}

export default function AdminRewardSerialsPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [races, setRaces] = useState<Race[]>([])
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  // 合作商家
  const [merchants, setMerchants] = useState<RewardMerchant[] | null>(null)
  const [merchantForm, setMerchantForm] = useState<MerchantForm>(EMPTY_MERCHANT)
  const [merchantBusy, setMerchantBusy] = useState(false)

  // 序號組
  const [groups, setGroups] = useState<RewardSerialGroup[] | null>(null)
  const [groupForm, setGroupForm] = useState<GroupForm>(EMPTY_GROUP)
  const [groupBusy, setGroupBusy] = useState(false)

  // 序號清單／匯入（依所選序號組）
  const [selectedGroupId, setSelectedGroupId] = useState<string | null>(null)
  const [serials, setSerials] = useState<RewardSerial[] | null>(null)
  const [serialsTotal, setSerialsTotal] = useState(0)
  const [serialStatus, setSerialStatus] = useState<'' | 'available' | 'issued' | 'void'>('')
  const [serialOffset, setSerialOffset] = useState(0)
  const [importText, setImportText] = useState('')
  const [importBusy, setImportBusy] = useState(false)
  const [importResult, setImportResult] = useState<RewardSerialImportResult | null>(null)
  const [importMode, setImportMode] = useState<'code' | 'link'>('code') // 'link'＝純貼領取連結(每行一個，以連結本身當 code 去重)

  const loadMerchants = useCallback(() => {
    const t = getToken()
    if (!t) return
    adminRewardMerchantsApi.list(t).then((r) => setMerchants(r.merchants)).catch((e) => setErr(e?.message || '載入商家失敗'))
  }, [])
  const loadGroups = useCallback(() => {
    const t = getToken()
    if (!t) return
    adminRewardGroupsApi.list(t).then((r) => setGroups(r.groups)).catch((e) => setErr(e?.message || '載入序號組失敗'))
  }, [])

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminRacesApi.list(t).then((r) => setRaces(r.races)).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '載入賽事失敗')
    })
    loadMerchants()
    loadGroups()
  }, [router, loadMerchants, loadGroups])

  const loadSerials = useCallback((groupId: string, status: string, offset: number) => {
    const t = getToken()
    if (!t) return
    setSerials(null)
    adminRewardGroupsApi.serials(t, groupId, { status: status || undefined, limit: SERIAL_PAGE, offset })
      .then((r) => { setSerials(r.serials); setSerialsTotal(r.count) })
      .catch((e) => setErr(e?.message || '載入序號清單失敗'))
  }, [])

  useEffect(() => {
    if (selectedGroupId) loadSerials(selectedGroupId, serialStatus, serialOffset)
  }, [selectedGroupId, serialStatus, serialOffset, loadSerials])
  // LINE POINT 序號組預設「純連結模式」(領取連結批次貼入)；其餘預設序號模式。切換序號組時重設。
  useEffect(() => {
    const g = groups?.find((x) => x.id === selectedGroupId)
    setImportMode(g?.is_line_point ? 'link' : 'code')
  }, [selectedGroupId, groups])

  // --- 合作商家 ---

  async function saveMerchant() {
    if (!token) return
    const name = merchantForm.name.trim()
    if (!name) { setErr('請填商家名稱'); return }
    setMerchantBusy(true); setErr(''); setMsg('')
    try {
      if (merchantForm.id) {
        await adminRewardMerchantsApi.update(token, merchantForm.id, { name, note: merchantForm.note.trim() })
      } else {
        await adminRewardMerchantsApi.create(token, { name, note: merchantForm.note.trim() })
      }
      setMsg(`✓ 已儲存商家「${name}」`)
      setMerchantForm(EMPTY_MERCHANT)
      loadMerchants()
    } catch (e: any) { setErr(e?.message || '儲存商家失敗') } finally { setMerchantBusy(false) }
  }
  async function deleteMerchant(m: RewardMerchant) {
    if (!token || !confirm(`確定刪除商家「${m.name}」？`)) return
    setErr(''); setMsg('')
    try {
      await adminRewardMerchantsApi.remove(token, m.id)
      setMsg('已刪除商家')
      if (merchantForm.id === m.id) setMerchantForm(EMPTY_MERCHANT)
      loadMerchants()
    } catch (e: any) { setErr(e?.message || '刪除失敗（此商家可能仍有序號組使用中）') }
  }

  // --- 序號組 ---

  function editGroup(g: RewardSerialGroup) {
    setGroupForm({
      id: g.id,
      merchant_id: g.merchant_id ?? '',
      name: g.name,
      item_label: g.item_label,
      is_line_point: g.is_line_point,
      valid_until: g.valid_until ? toLocalInput(g.valid_until) : '',
      use_limit_type: g.use_limit_type,
      use_limit_count: g.use_limit_count != null ? String(g.use_limit_count) : '',
      grant_count: String(g.grant_count),
      applies_all_races: g.applies_all_races,
      race_ids: g.race_ids,
      usage_note: g.usage_note,
      icon_url: g.icon_url,
      description: g.description,
    })
    setErr(''); setMsg('')
    setSelectedGroupId(g.id)
    setServiceStateReset()
  }
  function freshGroup() {
    setGroupForm(EMPTY_GROUP)
    setErr(''); setMsg('')
    setSelectedGroupId(null)
    setServiceStateReset()
  }
  function setServiceStateReset() {
    setSerials(null); setSerialsTotal(0); setSerialStatus(''); setSerialOffset(0)
    setImportText(''); setImportResult(null)
  }
  function toggleRace(id: string) {
    setGroupForm((f) => ({ ...f, race_ids: f.race_ids.includes(id) ? f.race_ids.filter((r) => r !== id) : [...f.race_ids, id] }))
  }

  async function saveGroup() {
    if (!token) return
    const name = groupForm.name.trim()
    if (!name) { setErr('請填序號組名稱'); return }
    if (!groupForm.applies_all_races && groupForm.race_ids.length === 0) { setErr('未勾選「全部活動」時需至少指定一場活動'); return }
    if (groupForm.use_limit_type === 'repeat' && !(parseInt(groupForm.use_limit_count || '0', 10) > 0)) {
      setErr('選擇「可重複使用」需填使用次數（正整數）'); return
    }
    setGroupBusy(true); setErr(''); setMsg('')
    try {
      const body: RewardSerialGroupWriteBody = {
        merchant_id: groupForm.merchant_id || null,
        name,
        item_label: groupForm.item_label.trim(),
        is_line_point: groupForm.is_line_point,
        valid_until: groupForm.valid_until ? new Date(groupForm.valid_until).toISOString() : null,
        use_limit_type: groupForm.use_limit_type,
        use_limit_count: groupForm.use_limit_type === 'repeat' ? parseInt(groupForm.use_limit_count || '0', 10) : null,
        grant_count: Math.max(1, parseInt(groupForm.grant_count || '1', 10)),
        applies_all_races: groupForm.applies_all_races,
        race_ids: groupForm.applies_all_races ? [] : groupForm.race_ids,
        usage_note: groupForm.usage_note.trim(),
        icon_url: groupForm.icon_url.trim(),
        description: groupForm.description.trim(),
      }
      if (groupForm.id) {
        await adminRewardGroupsApi.update(token, groupForm.id, body)
      } else {
        const { group } = await adminRewardGroupsApi.create(token, body)
        setGroupForm((f) => ({ ...f, id: group.id }))
        setSelectedGroupId(group.id)
      }
      setMsg(`✓ 已儲存序號組「${name}」`)
      loadGroups()
    } catch (e: any) { setErr(e?.message || '儲存序號組失敗') } finally { setGroupBusy(false) }
  }
  async function deleteGroup(g: RewardSerialGroup) {
    if (!token) return
    if (!confirm(`確定刪除序號組「${g.name}」？底下所有序號（共 ${g.total_count} 筆，含已發送／已使用者）將一併刪除，此操作無法復原。`)) return
    setErr(''); setMsg('')
    try {
      await adminRewardGroupsApi.remove(token, g.id)
      setMsg('已刪除序號組')
      if (groupForm.id === g.id) freshGroup()
      if (selectedGroupId === g.id) setSelectedGroupId(null)
      loadGroups()
    } catch (e: any) { setErr(e?.message || '刪除失敗') }
  }

  // --- 序號清單 ---

  async function voidSerial(s: RewardSerial) {
    if (!token || !selectedGroupId) return
    if (!confirm(`確定註銷序號「${s.code}」？`)) return
    setErr(''); setMsg('')
    try {
      await adminRewardGroupsApi.voidSerial(token, selectedGroupId, s.id)
      setMsg('已註銷序號')
      loadSerials(selectedGroupId, serialStatus, serialOffset)
      loadGroups()
    } catch (e: any) { setErr(e?.message || '註銷失敗') }
  }

  // --- 匯入 ---

  function parseManualText(text: string): { code: string; link: string }[] {
    return text.split(/\r?\n/).map((l) => l.trim()).filter(Boolean).map((l) => {
      // 只切「第一個」分隔符：序號在前、其餘整段當連結，避免連結網址內含逗號(LINE POINT/LIFF/地圖)被截斷
      const idx = l.search(/[,\t]/)
      const code = idx === -1 ? l : l.slice(0, idx)
      const link = idx === -1 ? '' : l.slice(idx + 1)
      return { code: code.trim(), link: link.trim() }
    })
  }

  // 純連結模式：每行一個領取連結（LINE POINT 等）→ 以連結本身當 code（去重＝連結唯一）＋存進 link。
  function parseLinks(text: string): { code: string; link: string }[] {
    return text.split(/\r?\n/).map((l) => l.trim()).filter(Boolean).map((l) => ({ code: l, link: l }))
  }

  async function doImport(items: { code: string; link: string }[]) {
    if (!token || !selectedGroupId) return
    const clean = items.filter((it) => it.code)
    if (clean.length === 0) { setErr('沒有可匯入的序號'); return }
    setImportBusy(true); setErr(''); setMsg(''); setImportResult(null)
    try {
      const res = await adminRewardGroupsApi.importSerials(token, selectedGroupId, clean)
      setImportResult(res)
      setMsg(`✓ 匯入完成：新增 ${res.imported} 筆${res.skipped ? `，跳過重複 ${res.skipped} 筆（全系統序號需唯一）` : ''}`)
      setImportText('')
      loadSerials(selectedGroupId, serialStatus, serialOffset)
      loadGroups()
    } catch (e: any) { setErr(e?.message || '匯入失敗') } finally { setImportBusy(false) }
  }
  async function importFromTextarea() { await doImport(importMode === 'link' ? parseLinks(importText) : parseManualText(importText)) }

  async function importFromCsv(file: File) {
    setErr('')
    try {
      const XLSX = await import('xlsx')
      const wb = XLSX.read(await file.arrayBuffer(), { type: 'array' })
      const ws = wb.Sheets[wb.SheetNames[0]]
      if (!ws) throw new Error('檔案內沒有工作表')
      const aoa = XLSX.utils.sheet_to_json<(string | number)[]>(ws, { header: 1, blankrows: false })
      if (aoa.length === 0) throw new Error('檔案是空的')
      const header = (aoa[0] ?? []).map((h) => String(h ?? '').trim())
      const hasHeader = header.some((h) => /code|序號|link|連結|網址/i.test(h))
      const find = (re: RegExp, fb: number) => { const i = header.findIndex((h) => re.test(h)); return i >= 0 ? i : fb }
      const iCode = hasHeader ? find(/code|序號/i, 0) : 0
      const iLink = hasHeader ? find(/link|連結|網址/i, 1) : 1
      const rows = hasHeader ? aoa.slice(1) : aoa
      const items = rows.filter(Boolean).map((row) => ({ code: String(row[iCode] ?? '').trim(), link: String(row[iLink] ?? '').trim() })).filter((it) => it.code)
      if (items.length === 0) throw new Error('沒有讀到有效序號（請確認欄位：序號 code / 連結 link）')
      await doImport(items)
    } catch (e: any) { setErr(e?.message || '匯入失敗，請確認是 .csv/.xlsx 檔且欄位正確') }
  }

  const selectedGroup = groups?.find((g) => g.id === selectedGroupId) ?? null
  const serialPageCount = serialsTotal === 0 ? 0 : Math.ceil(serialsTotal / SERIAL_PAGE)
  const serialPageIdx = Math.floor(serialOffset / SERIAL_PAGE)

  return (
    <div style={{ maxWidth: 1100, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>序號 / 獎勵管理</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: '0 0 14px', lineHeight: 1.7 }}>
        活動獎勵系統 P1：合作商家 → 活動序號組（面額/使用期限/使用次數限制/配發數/對應活動）→ 序號（手動貼上或 .csv 匯入，全系統唯一去重）。
        即時獎勵抽獎與玩家錢包規劃於後續版本上線。
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '8px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '8px 0', fontSize: 13 }}>{msg}</div>}

      {/* 合作商家 */}
      <div style={card}>
        <h2 style={{ fontSize: 16, fontWeight: 800, margin: '0 0 10px' }}>合作商家（{merchants?.length ?? '—'}）</h2>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginBottom: 12 }}>
          {merchants?.map((m) => (
            <div key={m.id} style={{ ...chip, borderColor: merchantForm.id === m.id ? 'var(--fug)' : 'var(--line)' }}>
              <span onClick={() => setMerchantForm({ id: m.id, name: m.name, note: m.note })} style={{ cursor: 'pointer', fontWeight: 700 }}>{m.name}</span>
              {m.note && <span style={{ color: 'var(--tx-faint)', marginLeft: 6 }}>· {m.note}</span>}
              <button onClick={() => deleteMerchant(m)} style={{ ...tinyBtn, marginLeft: 8, color: 'var(--hunt)' }}>刪除</button>
            </div>
          ))}
          {merchants && merchants.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>尚無合作商家。</div>}
        </div>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'flex-end' }}>
          <F label="商家名稱"><input style={{ ...inp, width: 200 }} value={merchantForm.name} onChange={(e) => setMerchantForm((f) => ({ ...f, name: e.target.value }))} placeholder="如：喬利健康" /></F>
          <F label="備註（選填）"><input style={{ ...inp, width: 260 }} value={merchantForm.note} onChange={(e) => setMerchantForm((f) => ({ ...f, note: e.target.value }))} placeholder="聯絡窗口/合作條件等" /></F>
          <button onClick={saveMerchant} disabled={merchantBusy} style={{ ...primaryBtn, opacity: merchantBusy ? 0.6 : 1 }}>{merchantForm.id ? '儲存修改' : '＋ 新增商家'}</button>
          {merchantForm.id && <button onClick={() => setMerchantForm(EMPTY_MERCHANT)} style={ghostBtn}>取消編輯</button>}
        </div>
      </div>

      {/* 序號組 */}
      <div style={{ ...card, marginTop: 16 }}>
        <h2 style={{ fontSize: 16, fontWeight: 800, margin: '0 0 10px' }}>活動序號組</h2>
        <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,280px) minmax(0,1fr)', gap: 16, alignItems: 'start' }}>
          {/* 列表 */}
          <div>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
              <b style={{ fontSize: 13 }}>序號組（{groups?.length ?? '—'}）</b>
              <button onClick={freshGroup} style={primaryBtn}>＋ 新增</button>
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              {groups?.map((g) => (
                <div key={g.id} onClick={() => editGroup(g)} style={{ ...rowCard, borderColor: groupForm.id === g.id ? 'var(--fug)' : 'var(--line)', cursor: 'pointer' }}>
                  <div style={{ fontSize: 13, fontWeight: 700 }}>{g.name}</div>
                  <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 2 }}>
                    {g.merchant_name || '（未指定商家）'}{g.item_label ? ` · ${g.item_label}` : ''}
                  </div>
                  <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 3 }}>
                    可用 {g.available_count} · 已發送 {g.issued_count} · 註銷 {g.void_count} ／ 共 {g.total_count}
                  </div>
                </div>
              ))}
              {groups && groups.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>尚無序號組，按「新增」建立。</div>}
            </div>
          </div>

          {/* 表單 */}
          <div style={innerCard}>
            <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1fr) minmax(0,1fr)', gap: 10 }}>
              <F label="名稱（必填）"><input style={inp} value={groupForm.name} onChange={(e) => setGroupForm((f) => ({ ...f, name: e.target.value }))} placeholder="如：週年慶 100 元序號" /></F>
              <F label="合作商家">
                <select style={inp} value={groupForm.merchant_id} onChange={(e) => setGroupForm((f) => ({ ...f, merchant_id: e.target.value }))}>
                  <option value="">（未指定）</option>
                  {merchants?.map((m) => <option key={m.id} value={m.id}>{m.name}</option>)}
                </select>
              </F>
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1fr) minmax(0,1fr)', gap: 10, marginTop: 8 }}>
              <F label="面額/品項"><input style={inp} value={groupForm.item_label} onChange={(e) => setGroupForm((f) => ({ ...f, item_label: e.target.value }))} placeholder="如：100元 / 咖啡兌換" /></F>
              <F label="使用期限（空=無期限）"><input style={inp} type="datetime-local" value={groupForm.valid_until} onChange={(e) => setGroupForm((f) => ({ ...f, valid_until: e.target.value }))} /></F>
            </div>
            <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, marginTop: 10 }}>
              <input type="checkbox" checked={groupForm.is_line_point} onChange={(e) => setGroupForm((f) => ({ ...f, is_line_point: e.target.checked }))} />
              LINE POINT 序號（僅供標記顯示，序號欄位仍統一為 code + link）
            </label>

            {/* 獎勵詳情（活動獎勵系統 P2：中獎配發序號時 denormalize 進玩家錢包 user_rewards 供顯示） */}
            <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1fr) minmax(0,1fr)', gap: 10, marginTop: 10 }}>
              <F label="獎勵圖示網址（選填）"><input style={inp} value={groupForm.icon_url} onChange={(e) => setGroupForm((f) => ({ ...f, icon_url: e.target.value }))} placeholder="https://…" /></F>
              <F label="使用說明（選填）"><input style={inp} value={groupForm.usage_note} onChange={(e) => setGroupForm((f) => ({ ...f, usage_note: e.target.value }))} placeholder="如：至門市出示序號兌換" /></F>
            </div>
            <F label="活動/獎勵說明（選填）">
              <textarea style={ta} rows={2} value={groupForm.description} onChange={(e) => setGroupForm((f) => ({ ...f, description: e.target.value }))} placeholder="顯示於玩家活動獎勵錢包的獎勵詳情" />
            </F>

            <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1fr) minmax(0,1fr) minmax(0,1fr)', gap: 10, marginTop: 10 }}>
              <F label="使用次數限制">
                <select style={inp} value={groupForm.use_limit_type} onChange={(e) => setGroupForm((f) => ({ ...f, use_limit_type: e.target.value as RewardUseLimitType }))}>
                  {(Object.keys(USE_LIMIT_LABEL) as RewardUseLimitType[]).map((k) => <option key={k} value={k}>{USE_LIMIT_LABEL[k]}</option>)}
                </select>
              </F>
              {groupForm.use_limit_type === 'repeat' && (
                <F label="可重複次數"><input style={inp} type="number" min={1} value={groupForm.use_limit_count} onChange={(e) => setGroupForm((f) => ({ ...f, use_limit_count: e.target.value }))} /></F>
              )}
              <F label="每次中獎配發序號數"><input style={inp} type="number" min={1} value={groupForm.grant_count} onChange={(e) => setGroupForm((f) => ({ ...f, grant_count: e.target.value }))} /></F>
            </div>

            <div style={{ marginTop: 12 }}>
              <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, fontWeight: 700 }}>
                <input type="checkbox" checked={groupForm.applies_all_races} onChange={(e) => setGroupForm((f) => ({ ...f, applies_all_races: e.target.checked }))} />
                對應全部活動
              </label>
              {!groupForm.applies_all_races && (
                <div style={{ marginTop: 8, maxHeight: 180, overflowY: 'auto', border: '1px solid var(--line-2)', borderRadius: 8, padding: 8 }}>
                  {races.map((rc) => (
                    <label key={rc.id} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12.5, padding: '3px 0' }}>
                      <input type="checkbox" checked={groupForm.race_ids.includes(rc.id)} onChange={() => toggleRace(rc.id)} />
                      {rc.title}
                    </label>
                  ))}
                  {races.length === 0 && <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>尚無活動</div>}
                </div>
              )}
            </div>

            <div style={{ display: 'flex', gap: 10, marginTop: 14 }}>
              <button onClick={saveGroup} disabled={groupBusy} style={{ ...primaryBtn, opacity: groupBusy ? 0.6 : 1 }}>{groupBusy ? '儲存中…' : groupForm.id ? '儲存修改' : '建立序號組'}</button>
              {groupForm.id && <button onClick={() => { const g = groups?.find((x) => x.id === groupForm.id); if (g) deleteGroup(g) }} style={dangerBtn}>刪除此序號組</button>}
            </div>
          </div>
        </div>
      </div>

      {/* 序號清單 + 匯入 */}
      {selectedGroup && (
        <div style={{ ...card, marginTop: 16 }}>
          <h2 style={{ fontSize: 16, fontWeight: 800, margin: '0 0 4px' }}>序號清單 · {selectedGroup.name}</h2>
          <p style={{ fontSize: 12, color: 'var(--tx-dim)', margin: '0 0 12px' }}>
            可用 {selectedGroup.available_count} · 已發送 {selectedGroup.issued_count} · 已註銷 {selectedGroup.void_count} ／ 共 {selectedGroup.total_count} 筆
          </p>

          {/* 匯入 */}
          <div style={innerCard}>
            <div style={{ fontSize: 13, fontWeight: 700, marginBottom: 6 }}>匯入序號</div>
            <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', marginBottom: 8, fontSize: 12.5 }}>
              <label style={{ display: 'flex', alignItems: 'center', gap: 5, cursor: 'pointer' }}>
                <input type="radio" name="importMode" checked={importMode === 'code'} onChange={() => setImportMode('code')} />
                序號模式（序號 或 序號,連結）
              </label>
              <label style={{ display: 'flex', alignItems: 'center', gap: 5, cursor: 'pointer' }}>
                <input type="radio" name="importMode" checked={importMode === 'link'} onChange={() => setImportMode('link')} />
                純連結模式（每行一個領取連結）
              </label>
            </div>
            <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginBottom: 8, lineHeight: 1.7 }}>
              {importMode === 'link'
                ? '每行貼一個「領取連結」（適合 LINE POINT 批次連結）：系統以連結本身作為唯一序號去重。'
                : '每行一筆，格式為「序號」或「序號,連結」（也可用 Tab 分隔）；亦可上傳 .csv／.xlsx（欄位：序號 code / 連結 link，或依序取前兩欄）。'}
              　序號**全系統唯一**，撞碼（含跨其他序號組、本次批次內重複）會被跳過不建立，匯入結果會列出跳過清單。
            </div>
            <textarea style={{ ...ta, fontFamily: 'monospace', fontSize: 12.5 }} rows={6} value={importText} onChange={(e) => setImportText(e.target.value)} placeholder={importMode === 'link' ? 'https://line.me/R/xxxxx\nhttps://line.me/R/yyyyy' : 'ABC123,https://line.me/xxx\nABC124'} />
            <div style={{ display: 'flex', gap: 8, marginTop: 8, alignItems: 'center' }}>
              <button onClick={importFromTextarea} disabled={importBusy || !importText.trim()} style={{ ...primaryBtn, opacity: importBusy ? 0.6 : 1 }}>{importBusy ? '匯入中…' : '匯入貼上內容'}</button>
              <label style={{ ...ghostBtn, cursor: 'pointer', opacity: importBusy ? 0.6 : 1, display: 'inline-block' }}>
                上傳 .csv / .xlsx
                <input type="file" accept=".csv,.xlsx,text/csv,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" disabled={importBusy} style={{ display: 'none' }}
                  onChange={(e) => { const f = e.target.files?.[0]; if (f) importFromCsv(f); e.target.value = '' }} />
              </label>
            </div>
            {importResult && (
              <div style={{ marginTop: 10, fontSize: 12, color: 'var(--tx-dim)' }}>
                新增 {importResult.imported} 筆・跳過重複 {importResult.skipped} 筆
                {importResult.duplicates.length > 0 && (
                  <div style={{ marginTop: 4, fontFamily: 'monospace', fontSize: 11, color: 'var(--tx-faint)', wordBreak: 'break-all' }}>
                    重複序號：{importResult.duplicates.join(', ')}
                  </div>
                )}
              </div>
            )}
          </div>

          {/* 狀態篩選 */}
          <div style={{ display: 'flex', gap: 8, margin: '14px 0 10px' }}>
            {(['', 'available', 'issued', 'void'] as const).map((s) => (
              <button key={s} onClick={() => { setSerialStatus(s); setSerialOffset(0) }} style={serialStatus === s ? tabBtnActive : tabBtn}>
                {s === '' ? '全部' : STATUS_LABEL[s]}
              </button>
            ))}
          </div>

          {/* 列表 */}
          {!serials && <div style={{ color: 'var(--tx-dim)', fontSize: 13, padding: '8px 0' }}>載入中…</div>}
          {serials && serials.length === 0 && <div style={{ color: 'var(--tx-dim)', fontSize: 13, padding: '8px 0' }}>尚無序號</div>}
          {serials && serials.length > 0 && (
            <div style={{ border: '1px solid var(--line)', borderRadius: 10, overflow: 'hidden' }}>
              <div style={{ ...serialRow, background: 'var(--bg-1)', fontSize: 11, color: 'var(--tx-faint)', textTransform: 'uppercase' }}>
                <div style={{ flex: 2 }}>序號</div><div style={{ flex: 2 }}>連結</div><div style={{ flex: 1 }}>狀態</div><div style={{ flex: 2 }}>建立時間</div><div style={{ flex: 1 }} />
              </div>
              {serials.map((s) => (
                <div key={s.id} style={serialRow}>
                  <div style={{ flex: 2, fontFamily: 'monospace', fontSize: 12.5, wordBreak: 'break-all' }}>{s.code}</div>
                  <div style={{ flex: 2, fontSize: 11.5, color: 'var(--tx-dim)', wordBreak: 'break-all' }}>{s.link || '—'}</div>
                  <div style={{ flex: 1, fontSize: 12 }}>
                    <span style={{ color: s.status === 'void' ? 'var(--hunt)' : s.status === 'issued' ? 'var(--fug)' : 'var(--tx-dim)' }}>{STATUS_LABEL[s.status] || s.status}</span>
                    {s.used && <span style={{ color: 'var(--tx-faint)' }}>・已使用</span>}
                  </div>
                  <div style={{ flex: 2, fontSize: 11.5, color: 'var(--tx-faint)' }}>{new Date(s.created_at).toLocaleString('zh-TW')}</div>
                  <div style={{ flex: 1, textAlign: 'right' }}>
                    {s.status !== 'void' && <button onClick={() => voidSerial(s)} style={{ ...tinyBtn, color: 'var(--hunt)' }}>註銷</button>}
                  </div>
                </div>
              ))}
            </div>
          )}
          {serialPageCount > 1 && (
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 10, fontSize: 12.5 }}>
              <button onClick={() => setSerialOffset(Math.max(0, serialOffset - SERIAL_PAGE))} disabled={serialPageIdx === 0} style={{ ...tinyBtn, opacity: serialPageIdx === 0 ? 0.4 : 1 }}>上一頁</button>
              <span style={{ color: 'var(--tx-dim)' }}>第 {serialPageIdx + 1} / {serialPageCount} 頁（共 {serialsTotal} 筆）</span>
              <button onClick={() => setSerialOffset(serialOffset + SERIAL_PAGE)} disabled={serialPageIdx + 1 >= serialPageCount} style={{ ...tinyBtn, opacity: serialPageIdx + 1 >= serialPageCount ? 0.4 : 1 }}>下一頁</button>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function F({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}

// ISO → datetime-local（本地時間）
function toLocalInput(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const off = d.getTimezoneOffset() * 60000
  return new Date(d.getTime() - off).toISOString().slice(0, 16)
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 18 }
const innerCard: React.CSSProperties = { background: 'var(--bg-0, #0d0f14)', border: '1px solid var(--line-2)', borderRadius: 12, padding: 14 }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '9px 11px', color: 'var(--tx)', fontSize: 13.5, width: '100%', boxSizing: 'border-box', fontFamily: 'inherit' }
const ta: React.CSSProperties = { ...inp, resize: 'vertical', lineHeight: 1.6, width: '100%' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none', borderRadius: 10, padding: '9px 16px', cursor: 'pointer', fontSize: 13, fontFamily: 'inherit' }
const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '9px 14px', cursor: 'pointer', fontSize: 13, fontFamily: 'inherit' }
const dangerBtn: React.CSSProperties = { background: 'transparent', color: 'var(--hunt)', border: '1px solid var(--hunt)', borderRadius: 10, padding: '9px 16px', cursor: 'pointer', fontSize: 13, fontFamily: 'inherit' }
const tinyBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', border: '1px solid var(--line-2)', borderRadius: 6, color: 'var(--tx)', cursor: 'pointer', fontSize: 11.5, padding: '3px 8px', fontFamily: 'inherit' }
const chip: React.CSSProperties = { border: '1px solid var(--line)', borderRadius: 20, padding: '5px 10px', fontSize: 12.5, display: 'flex', alignItems: 'center', background: 'var(--bg-0, #0d0f14)' }
const rowCard: React.CSSProperties = { background: 'var(--bg-0, #0d0f14)', border: '1px solid var(--line)', borderRadius: 8, padding: 10 }
const tabBtn: React.CSSProperties = { background: 'var(--bg-1)', color: 'var(--tx-dim)', border: '1px solid var(--line)', borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 12.5, fontWeight: 700, fontFamily: 'inherit' }
const tabBtnActive: React.CSSProperties = { ...tabBtn, background: 'var(--fug)', color: 'var(--fug-ink)', borderColor: 'var(--fug)' }
const serialRow: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px', borderBottom: '1px solid var(--line)' }
