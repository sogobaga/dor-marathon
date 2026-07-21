'use client'

import { useEffect, useMemo, useState } from 'react'
import {
  racesApi,
  profileApi,
  paymentsApi,
  METRIC_BY_KEY,
  type Race,
  type RaceDetail,
  type RaceGroup,
  type RaceTask,
  type RegistrationState,
  type ParticipantField,
  type RecommendRow,
  type InvoiceInfo,
} from '@/lib/api'
import { getUserToken, withUserAuth, SessionExpiredError, useUser } from '@/lib/userAuth'
import { useDashboard } from '@/lib/useDashboard'
import { submitEcpayForm } from '@/lib/ecpay'
import { track } from '@/lib/analytics'
import ScrollArea from './ScrollArea'

const FIELD_LABEL: Record<ParticipantField, string> = {
  real_name: '真實姓名', nickname: '暱稱', phone: '手機',
  address: '地址', birthday: '生日', gender: '性別',
}
const GENDER_OPTS = [{ v: '', t: '請選擇' }, { v: 'male', t: '男' }, { v: 'female', t: '女' }, { v: 'other', t: '其他' }]

// 統一編號檢查碼驗證
function isValidTwTaxId(id: string): boolean {
  if (!/^\d{8}$/.test(id)) return false
  const weights = [1, 2, 1, 2, 1, 2, 4, 1]
  const digits = id.split('').map(Number)
  let sum = 0
  for (let i = 0; i < 8; i++) {
    const product = digits[i] * weights[i]
    sum += Math.floor(product / 10) + (product % 10)
  }
  if (sum % 5 === 0) return true
  if (digits[6] === 7 && (sum + 1) % 5 === 0) return true
  return false
}
const CARRIER_ID_RE = /^\/[0-9A-Z.+-]{7}$/
const LOVE_CODE_RE = /^[0-9]{3,7}$/

function ntd(cents: number) {
  return 'NT$ ' + Math.round(cents / 100).toLocaleString('zh-TW')
}

// 配速秒數 → m:ss
function paceFmt(sec: number) {
  return `${Math.floor(sec / 60)}:${String(Math.round(sec % 60)).padStart(2, '0')}`
}
// 任務目標 → 人類可讀字串
function taskTarget(t: RaceTask): string {
  const m = METRIC_BY_KEY[t.metric_type]
  if (!m) return ''
  if (m.kind === 'range') {
    if (t.metric_type === 'avg_pace_range') return `${paceFmt(t.range_lo ?? 0)}–${paceFmt(t.range_hi ?? 0)} /km`
    return `${t.range_lo ?? '—'}–${t.range_hi ?? '—'} ${m.unit}`
  }
  return `≥ ${t.target_value ?? '—'} ${m.unit}`
}

// 任務清單（團體/個人/集體共用）
function TaskList({ label, items }: { label: string; items: RaceTask[] }) {
  if (items.length === 0) return null
  return (
    <div style={{ marginTop: 10 }}>
      <div style={{ fontSize: 12, fontWeight: 700, color: 'var(--tx)', marginBottom: 6 }}>{label}</div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        {items.map((t, i) => {
          const m = METRIC_BY_KEY[t.metric_type]
          return (
            <div key={t.id ?? i} style={{ background: 'var(--bg-2)', border: '1px solid var(--line)', borderRadius: 10, padding: '8px 11px' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', gap: 10, alignItems: 'baseline' }}>
                <span style={{ fontSize: 13, fontWeight: 600 }}>{t.title || m?.label || '任務'}</span>
                <span style={{ fontSize: 12, color: 'var(--fug)', whiteSpace: 'nowrap' }}>{taskTarget(t)}</span>
              </div>
              <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 2 }}>
                {m?.label}{t.description ? ` · ${t.description}` : ''}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
function remaining(g: RaceGroup) {
  if (g.slot_limit == null) return null
  return Math.max(0, g.slot_limit - (g.slots_taken ?? 0))
}

export default function RegistrationScreen({ race, onBack }: { race: Race; onBack: () => void }) {
  const [detail, setDetail] = useState<RaceDetail | null>(null)
  const [existing, setExisting] = useState<RegistrationState | null>(null)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(true)

  const [groupId, setGroupId] = useState('')
  const [groupKey, setGroupKey] = useState('') // 加入需鑰匙的分組時輸入
  const [canCreate, setCanCreate] = useState(false) // 此會員是否獲准建立跑團分組
  const [recommends, setRecommends] = useState<RecommendRow[]>([]) // 追蹤者中也報名此賽事（前三）
  const [showAllGroups, setShowAllGroups] = useState(false) // 分組過多時的「全部分組」選單
  const [previewId, setPreviewId] = useState<string | null>(null) // 選單內展開預覽任務的分組
  const [expandedId, setExpandedId] = useState<string | null>(null) // 內嵌清單中展開任務的分組（可再點收合）
  const [qty, setQty] = useState<Record<string, number>>({})

  // 自建跑團分組表單
  const [showCreate, setShowCreate] = useState(false)
  const [tgName, setTgName] = useState('')
  const [tgDesc, setTgDesc] = useState('')
  const [tgRequiresKey, setTgRequiresKey] = useState(false)
  const [tgKey, setTgKey] = useState('')
  const [tgBusy, setTgBusy] = useState(false)
  const [tgErr, setTgErr] = useState('')
  const [participant, setParticipant] = useState<Record<ParticipantField, string>>({
    real_name: '', nickname: '', phone: '', address: '', birthday: '', gender: '',
  })
  const [invoice, setInvoice] = useState<InvoiceInfo>({
    buyer_type: 'personal', tax_id: '', title: '', carrier_type: '', carrier_id: '', love_code: '',
  })
  const [submitting, setSubmitting] = useState(false)
  const [done, setDone] = useState<{ group: string; revealed: boolean; paid: boolean; payable: number; orderId: string } | null>(null)
  const [paying, setPaying] = useState(false)

  const [promoCode, setPromoCode] = useState('')
  const [promoQuote, setPromoQuote] = useState<import('@/lib/api').PromoQuote | null>(null)
  const [promoBusy, setPromoBusy] = useState(false)
  const [useCoupon, setUseCoupon] = useState(false) // 使用 VIP 活動優惠券($100)

  const { dash } = useDashboard()
  const isVip = !!dash?.is_vip
  const couponBal = dash?.activity_coupon_balance ?? 0
  const COUPON_CENTS = 10000 // NT$100

  const loggedIn = !!useUser()
  const isBattle = race.event_mode === 'faction_battle'

  useEffect(() => {
    const hasToken = !!getUserToken()
    const loadDetail = hasToken
      ? withUserAuth((t) => racesApi.detail(race.id, t)).catch(() => racesApi.detail(race.id))
      : racesApi.detail(race.id)
    loadDetail
      .then((r) => {
        setDetail(r.race)
        setExisting(r.registration)
        setCanCreate(!!r.can_create_team_group)
      })
      .catch((e) => setErr(e?.message || '載入失敗'))
      .finally(() => setLoading(false))
    if (hasToken) {
      withUserAuth((t) => profileApi.getMe(t)).then((r) => {
        setParticipant((prev) => ({
          ...prev,
          real_name: r.profile.real_name || '',
          nickname: r.profile.nickname || '',
          phone: r.profile.phone || '',
          address: r.profile.address || '',
          birthday: r.profile.birthday || '',
          gender: r.profile.gender || '',
        }))
        const inv = r.profile.invoice
        if (inv && (inv.buyer_type === 'company' || inv.buyer_type === 'donation' || inv.buyer_type === 'personal')) {
          setInvoice(inv)
        }
      }).catch(() => {})
      withUserAuth((t) => profileApi.recommendations(t, race.id)).then((r) => setRecommends(r.recommendations)).catch(() => {})
    }
  }, [race.id])

  const selectedGroup = useMemo(
    () => detail?.groups.find((g) => g.id === groupId) || null,
    [detail, groupId]
  )

  // 賽事任務分層：集體（全體合計）/ 所有分組共同（group scope 無 group_id）/ 各分組專屬
  const collectiveTasks = useMemo(
    () => (detail?.tasks ?? []).filter((t) => t.scope === 'race_collective'),
    [detail]
  )
  const allGroupsTasks = useMemo(() => {
    const all = detail?.tasks ?? []
    return {
      team: all.filter((t) => t.scope === 'group_team' && !t.group_id),
      individual: all.filter((t) => t.scope === 'group_individual' && !t.group_id),
    }
  }, [detail])
  const hasAllGroupsTasks = allGroupsTasks.team.length + allGroupsTasks.individual.length > 0
  function groupSpecific(gid: string) {
    const all = detail?.tasks ?? []
    return {
      team: all.filter((t) => t.scope === 'group_team' && t.group_id === gid),
      individual: all.filter((t) => t.scope === 'group_individual' && t.group_id === gid),
    }
  }
  // 某分組的專屬任務內嵌顯示（accordion / 選單 / 摘要共用）
  function renderGroupSpecific(gid: string) {
    const s = groupSpecific(gid)
    if (s.team.length === 0 && s.individual.length === 0) {
      return hasAllGroupsTasks
        ? <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', padding: '6px 2px' }}>本組無專屬任務（僅共同任務）</div>
        : null
    }
    return (
      <div style={{ paddingLeft: 2 }}>
        <TaskList label="本組團體任務（全組加總達標）" items={s.team} />
        <TaskList label="本組個人任務（每人各自達成）" items={s.individual} />
      </div>
    )
  }

  // 必填欄位：賽事設定 + 所選分組限制
  const requiredSet = useMemo(() => {
    const s = new Set<ParticipantField>((detail?.required_fields as ParticipantField[]) || [])
    const g = selectedGroup
    if (g) {
      if (g.gender_limit && g.gender_limit !== 'any') s.add('gender')
      if (g.age_min != null || g.age_max != null) s.add('birthday')
    }
    return s
  }, [detail, selectedGroup])

  const addonsList = useMemo(
    () => Object.entries(qty).filter(([, q]) => q > 0).map(([addon_id, q]) => ({ addon_id, qty: q })),
    [qty]
  )

  const total = useMemo(() => {
    let t = race.entry_fee
    for (const a of detail?.addons || []) t += (qty[a.id!] || 0) * a.price_cents
    return t
  }, [detail, qty, race.entry_fee])

  // 折抵來源：VIP 優惠券($100，只折報名費) 或 優惠序號；擇一。加購不折。
  const couponDiscount = useCoupon ? Math.min(COUPON_CENTS, race.entry_fee) : 0
  const addonsTotal = total - race.entry_fee
  const payable = useCoupon
    ? Math.max(0, race.entry_fee - couponDiscount) + addonsTotal
    : (promoQuote?.valid ? promoQuote.payable_cents : total)

  async function applyPromo() {
    const code = promoCode.trim()
    if (!code) {
      setPromoQuote(null)
      return
    }
    setPromoBusy(true)
    try {
      const quote = await withUserAuth((t) => racesApi.promoCheck(race.id, t, { code, addons: addonsList }))
      setPromoQuote(quote)
    } catch (e: any) {
      setPromoQuote({ valid: false, discount_cents: 0, payable_cents: total, free: false, reason: e instanceof SessionExpiredError ? '登入已過期' : (e?.message || '驗證失敗') })
    } finally {
      setPromoBusy(false)
    }
  }

  async function refreshDetail(selectId?: string) {
    const hasToken = !!getUserToken()
    const p = hasToken
      ? withUserAuth((t) => racesApi.detail(race.id, t)).catch(() => racesApi.detail(race.id))
      : racesApi.detail(race.id)
    const r = await p
    setDetail(r.race)
    setCanCreate(!!r.can_create_team_group)
    if (selectId) setGroupId(selectId)
  }

  async function createTeamGroup() {
    setTgErr('')
    if (!tgName.trim()) {
      setTgErr('請輸入跑團分組名稱')
      return
    }
    if (tgRequiresKey && !tgKey.trim()) {
      setTgErr('請設定跑團鑰匙')
      return
    }
    setTgBusy(true)
    try {
      const g = await withUserAuth((t) =>
        racesApi.createTeamGroup(race.id, t, {
          name: tgName.trim(),
          description: tgDesc.trim() || undefined,
          requires_key: tgRequiresKey,
          group_key: tgRequiresKey ? tgKey.trim() : undefined,
        })
      )
      await refreshDetail(g.id)
      setGroupKey(tgRequiresKey ? tgKey.trim() : '') // 自動帶入建立者的鑰匙，方便接著報名
      setShowCreate(false)
      setTgName(''); setTgDesc(''); setTgRequiresKey(false); setTgKey('')
    } catch (e: any) {
      setTgErr(e instanceof SessionExpiredError ? '登入已過期，請回上一頁重新登入' : e?.message || '建立失敗')
    } finally {
      setTgBusy(false)
    }
  }

  async function submit() {
    setErr('')
    if (!isBattle && !groupId) {
      setErr('請選擇分組')
      return
    }
    if (!isBattle && selectedGroup?.requires_key && !groupKey.trim()) {
      setErr('此分組需要「跑團鑰匙」才能報名')
      return
    }
    for (const f of requiredSet) {
      if (!participant[f]?.trim()) {
        setErr(`請填寫「${FIELD_LABEL[f]}」`)
        return
      }
    }
    if (invoice.buyer_type === 'company') {
      if (!invoice.tax_id.trim() || !isValidTwTaxId(invoice.tax_id.trim())) {
        setErr('統一編號有誤，請確認')
        return
      }
      if (!invoice.title.trim()) {
        setErr('請填寫「發票抬頭」')
        return
      }
    } else if (invoice.buyer_type === 'donation') {
      if (!LOVE_CODE_RE.test(invoice.love_code.trim())) {
        setErr('愛心碼格式有誤，請輸入 3-7 位數字')
        return
      }
    } else if (invoice.buyer_type === 'personal') {
      if (invoice.carrier_type === 'mobile' && !CARRIER_ID_RE.test(invoice.carrier_id.trim())) {
        setErr('手機條碼載具格式有誤，請確認（例如 /ABC1234）')
        return
      }
    }
    setSubmitting(true)
    try {
      const addons = Object.entries(qty)
        .filter(([, q]) => q > 0)
        .map(([addon_id, q]) => ({ addon_id, qty: q }))
      const res = await withUserAuth((token) =>
        racesApi.register(race.id, token, {
          group_id: isBattle ? undefined : groupId,
          group_key: !isBattle && selectedGroup?.requires_key ? groupKey.trim() : undefined,
          addons,
          participant,
          invoice,
          promo_code: useCoupon ? undefined : (promoCode.trim() || undefined),
          use_coupon: useCoupon || undefined,
        })
      )
      setDone({ group: res.assigned_group, revealed: res.group_revealed, paid: res.paid, payable: res.payable_cents, orderId: res.order.id })
      track('register_complete', { race_id: race.id, race_title: race.title, value: res.payable_cents / 100, currency: 'TWD', paid: res.paid })
    } catch (e: any) {
      setErr(e instanceof SessionExpiredError ? '登入已過期，請回上一頁重新登入' : e?.message || '報名失敗')
    } finally {
      setSubmitting(false)
    }
  }

  // 報名完成頁「前往繳費」：直接用該筆訂單向綠界結帳（帶自身 origin，付款後回本網域）
  async function goPay(orderId: string) {
    setErr(''); setPaying(true)
    track('begin_checkout', { race_id: race.id, value: (done?.payable ?? 0) / 100, currency: 'TWD' })
    try {
      const { action_url, params } = await withUserAuth((t) => paymentsApi.ecpayCheckout(t, orderId))
      submitEcpayForm(action_url, params) // 導去綠界，不會 return
    } catch (e: any) {
      setErr(e instanceof SessionExpiredError ? '登入已過期，請重新登入' : e?.message || '無法前往付款')
      setPaying(false)
    }
  }

  // 單一分組選擇卡片（內嵌清單與「全部分組」選單共用）
  // 該分組是否有可展開的任務內容（專屬 or 共同）
  function groupHasDetail(gid: string): boolean {
    const s = groupSpecific(gid)
    return s.team.length + s.individual.length > 0 || hasAllGroupsTasks
  }
  function groupCard(g: RaceGroup, onPick: () => void, expanded?: boolean) {
    const rem = remaining(g)
    const full = rem === 0
    const selected = groupId === g.id
    return (
      <button
        key={g.id}
        disabled={full}
        onClick={onPick}
        style={{
          ...groupRow, cursor: full ? 'not-allowed' : 'pointer', opacity: full ? 0.5 : 1,
          border: selected ? '1.5px solid var(--fug)' : '1.5px solid rgba(255,255,255,.28)',
          background: selected ? 'rgba(45,212,150,.1)' : 'var(--bg-2)', textAlign: 'left',
          boxShadow: selected ? '0 0 0 3px rgba(45,212,150,.12)' : 'none',
        }}
      >
        <div>
          <div style={{ fontWeight: 600 }}>
            {g.requires_key ? '🔒 ' : ''}{g.name}
            {g.is_user_created ? <span style={{ fontSize: 10, color: 'var(--tx-faint)', marginLeft: 6 }}>跑團</span> : null}
          </div>
          <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
            {g.target_distance_km != null ? `${g.target_distance_km}K` : ''}
            {g.gender_limit && g.gender_limit !== 'any' ? ` · 限${g.gender_limit === 'male' ? '男' : '女'}` : ''}
            {g.age_min != null || g.age_max != null ? ` · 年齡${g.age_min ?? ''}–${g.age_max ?? ''}` : ''}
            {g.requires_key ? ' · 需鑰匙' : ''}
          </div>
        </div>
        <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={{ fontSize: 12, color: full ? 'var(--hunt)' : 'var(--tx-dim)' }}>
            {rem == null ? '名額不限' : full ? '已額滿' : `剩 ${rem}`}
          </span>
          {expanded !== undefined && (
            <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{expanded ? '▾ 收合' : '▸ 任務'}</span>
          )}
        </span>
      </button>
    )
  }
  const manyGroups = (detail?.groups.length ?? 0) > 8

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)', position: 'relative' }}>
      <header style={{ padding: 'var(--app-top) 22px 14px', flexShrink: 0 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <h1 style={{ margin: '10px 0 2px', fontSize: 22, fontWeight: 800, color: 'var(--tx)' }}>{race.title}</h1>
        <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>報名</div>
      </header>

      <ScrollArea padding="4px 18px 28px">
        {loading && <Hint>載入中…</Hint>}

        {!loading && !loggedIn && (
          <Hint>請先用右上角「Google 登入」後再報名。</Hint>
        )}

        {!loading && loggedIn && done && (
          <div style={card}>
            <div style={{ fontSize: 18, fontWeight: 800, color: 'var(--fug)' }}>
              ✓ 報名完成{done.paid ? '' : ' · 待繳費'}
            </div>
            <div style={{ fontSize: 13, color: 'var(--tx-dim)', marginTop: 8 }}>
              {done.revealed ? `分組：${done.group}` : '已隨機分組，賽事當天公布所屬分組'}
            </div>
            <div style={{ fontSize: 13, color: 'var(--tx-dim)', marginTop: 4 }}>
              {done.paid ? '已使用優惠序號 0 元完成，無需付款' : `應繳金額：${ntd(done.payable)}`}
            </div>
            {!done.paid && done.payable > 0 && (
              <button onClick={() => goPay(done.orderId)} disabled={paying} style={{ ...primaryBtn, marginTop: 14, background: 'var(--gold)', color: '#fff' }}>
                {paying ? '前往綠界…' : '前往繳費'}
              </button>
            )}
            <button onClick={onBack} style={{ ...primaryBtn, marginTop: 10, background: 'rgba(255,255,255,.06)', color: 'var(--tx)' }}>回活動列表</button>
          </div>
        )}

        {!loading && loggedIn && !done && existing && (
          <div style={card}>
            <div style={{ fontSize: 16, fontWeight: 800, color: 'var(--tx)' }}>您已報名此賽事</div>
            <div style={{ fontSize: 13, color: 'var(--tx-dim)', marginTop: 8 }}>
              狀態：{existing.status === 'paid' ? '已完成' : '待繳費'}
              {existing.group_revealed === false ? '（分組賽事當天公布）' : ''}
            </div>
          </div>
        )}

        {!loading && loggedIn && !done && !existing && detail && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
            {/* 追蹤的跑者也報名了 */}
            {recommends.length > 0 && (
              <div style={{ background: 'rgba(45,212,150,.06)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 12px' }}>
                <div style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--fug)', marginBottom: 8 }}>你追蹤的跑者也報名了</div>
                <div style={{ display: 'flex', gap: 14 }}>
                  {recommends.map((rc) => (
                    <div key={rc.user_id} style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 4, width: 64 }}>
                      <div style={{ width: 44, height: 44, borderRadius: '50%', overflow: 'hidden', background: 'var(--bg-2)', border: '1px solid var(--line-2)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                        {rc.avatar_url
                          // eslint-disable-next-line @next/next/no-img-element
                          ? <img src={rc.avatar_url} alt="" style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
                          : <span style={{ fontWeight: 800, color: 'var(--tx-dim)' }}>{(rc.nickname || '?').slice(0, 1)}</span>}
                      </div>
                      <span style={{ fontSize: 11, color: 'var(--tx-dim)', maxWidth: 64, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{rc.nickname}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* 分組（分組選擇置頂） */}
            <Section title="選擇分組">
              {isBattle ? (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  <div style={hint}>分組對抗模式：報名後隨機分組，賽事當天才公布。以下為對抗分組：</div>
                  {detail.groups.map((g) => (
                    <div key={g.id} style={groupRow}>
                      <span style={{ fontWeight: 600 }}>{g.name}</span>
                      {g.target_distance_km != null && <span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>{g.target_distance_km}K</span>}
                    </div>
                  ))}
                </div>
              ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  {!manyGroups ? (
                    // 分組 ≤ 8：直接內嵌全部；點選即選用並展開任務，再點同一組則收合（保留選用）
                    detail.groups.map((g) => (
                      <div key={g.id}>
                        {groupCard(g, () => {
                          setGroupId(g.id!); setGroupKey('')
                          setExpandedId((cur) => (cur === g.id ? null : g.id!))
                        }, groupHasDetail(g.id!) ? expandedId === g.id : undefined)}
                        {expandedId === g.id && (
                          <div style={{ margin: '6px 0 2px' }}>{renderGroupSpecific(g.id!)}</div>
                        )}
                      </div>
                    ))
                  ) : (
                    // 分組 > 8：顯示已選 + 「全部分組」按鈕；已選組就地展開專屬任務
                    <>
                      <div style={{ ...groupRow, cursor: 'default', alignItems: 'flex-start' }}>
                        <div>
                          <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginBottom: 2 }}>目前已選擇分組</div>
                          {selectedGroup ? (
                            <div style={{ fontWeight: 600 }}>
                              {selectedGroup.requires_key ? '🔒 ' : ''}{selectedGroup.name}
                              {selectedGroup.is_user_created ? <span style={{ fontSize: 10, color: 'var(--tx-faint)', marginLeft: 6 }}>跑團</span> : null}
                            </div>
                          ) : (
                            <div style={{ color: 'var(--tx-dim)' }}>尚未選擇</div>
                          )}
                        </div>
                        <button onClick={() => setShowAllGroups(true)} style={{ ...ghostBtn, whiteSpace: 'nowrap' }}>
                          {selectedGroup ? '更換分組' : '全部分組'}
                        </button>
                      </div>
                      {selectedGroup && <div style={{ margin: '2px 0' }}>{renderGroupSpecific(selectedGroup.id!)}</div>}
                      <button onClick={() => setShowAllGroups(true)} style={ghostBtn}>
                        全部分組（共 {detail.groups.length} 組）
                      </button>
                    </>
                  )}

                  {/* 選到需鑰匙的分組 → 要求輸入跑團鑰匙 */}
                  {selectedGroup?.requires_key && (
                    <div style={{ marginTop: 2 }}>
                      <div style={hint}>「{selectedGroup.name}」需要跑團鑰匙才能加入：</div>
                      <input
                        style={inp} type="text" value={groupKey}
                        onChange={(e) => setGroupKey(e.target.value)}
                        placeholder="請輸入跑團鑰匙"
                      />
                    </div>
                  )}

                  {/* 開放跑團分組申請：前台自建分組（限獲准會員） */}
                  {detail.allow_team_groups && detail.can_register && canCreate && (
                    showCreate ? (
                      <div style={{ ...groupRow, flexDirection: 'column', alignItems: 'stretch', gap: 8, cursor: 'default' }}>
                        <div style={{ fontWeight: 600, fontSize: 13 }}>建立跑團分組</div>
                        <input style={inp} value={tgName} onChange={(e) => setTgName(e.target.value)} placeholder="跑團分組名稱（例：清晨跑團）" />
                        <input style={inp} value={tgDesc} onChange={(e) => setTgDesc(e.target.value)} placeholder="說明（選填）" />
                        <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--tx)' }}>
                          <input type="checkbox" checked={tgRequiresKey} onChange={(e) => setTgRequiresKey(e.target.checked)} />
                          設定跑團鑰匙（需鑰匙才能加入）
                        </label>
                        {tgRequiresKey && (
                          <input style={inp} value={tgKey} onChange={(e) => setTgKey(e.target.value)} placeholder="設定鑰匙密碼，分享給團員" />
                        )}
                        {tgErr && <div style={{ fontSize: 12, color: 'var(--hunt)' }}>{tgErr}</div>}
                        <div style={{ display: 'flex', gap: 8 }}>
                          <button onClick={createTeamGroup} disabled={tgBusy} style={{ ...primaryBtn, flex: 1, opacity: tgBusy ? 0.6 : 1 }}>
                            {tgBusy ? '建立中…' : '建立並選用'}
                          </button>
                          <button onClick={() => { setShowCreate(false); setTgErr('') }} style={ghostBtn}>取消</button>
                        </div>
                      </div>
                    ) : (
                      <button onClick={() => setShowCreate(true)} style={ghostBtn}>＋ 建立跑團分組</button>
                    )
                  )}
                </div>
              )}
            </Section>

            {/* 賽事集體任務（全體參賽者）+ 所有分組共同任務（移到集體任務下方） */}
            {(collectiveTasks.length > 0 || hasAllGroupsTasks) && (
              <Section title="賽事任務（全體參賽者）">
                {collectiveTasks.length > 0 && <TaskList label="集體任務（全員合計達標）" items={collectiveTasks} />}
                {hasAllGroupsTasks && (
                  <div style={{ background: 'rgba(45,212,150,.06)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 12px', marginTop: 10 }}>
                    <div style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--fug)' }}>所有分組共同任務</div>
                    <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 2 }}>不論加入哪一組都需達成</div>
                    <TaskList label="團體任務（全組加總達標）" items={allGroupsTasks.team} />
                    <TaskList label="個人任務（每人各自達成）" items={allGroupsTasks.individual} />
                  </div>
                )}
              </Section>
            )}

            {/* 加購 */}
            {detail.addons.length > 0 && (
              <Section title="加購項目">
                {detail.addons.map((a) => (
                  <div key={a.id} style={groupRow}>
                    <div>
                      <div style={{ fontWeight: 600 }}>{a.name}</div>
                      <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{ntd(a.price_cents)}{a.description ? ` · ${a.description}` : ''}</div>
                    </div>
                    <input
                      type="number" min={0} max={a.per_user_limit ?? undefined}
                      value={qty[a.id!] || 0}
                      onChange={(e) => setQty((q) => ({ ...q, [a.id!]: Math.max(0, parseInt(e.target.value || '0', 10)) }))}
                      style={{ ...inp, width: 64 }}
                    />
                  </div>
                ))}
              </Section>
            )}

            {/* 參賽者資料 */}
            <Section title="參賽者資料">
              {(['real_name', 'nickname', 'phone', 'address', 'birthday', 'gender'] as ParticipantField[]).map((f) => (
                <label key={f} style={{ display: 'flex', flexDirection: 'column', gap: 5, marginBottom: 10 }}>
                  <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
                    {FIELD_LABEL[f]}{requiredSet.has(f) ? <span style={{ color: 'var(--hunt)' }}> *</span> : ''}
                  </span>
                  {f === 'gender' ? (
                    <select style={inp} value={participant.gender} onChange={(e) => setParticipant((p) => ({ ...p, gender: e.target.value }))}>
                      {GENDER_OPTS.map((o) => <option key={o.v} value={o.v}>{o.t}</option>)}
                    </select>
                  ) : (
                    <input
                      style={inp} type={f === 'birthday' ? 'date' : 'text'} value={participant[f]}
                      onChange={(e) => setParticipant((p) => ({ ...p, [f]: e.target.value }))}
                    />
                  )}
                </label>
              ))}
            </Section>

            {/* 電子發票 */}
            <Section title="電子發票">
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, minmax(0, 1fr))', gap: 8, marginBottom: 12 }}>
                {([
                  { v: 'personal', t: '個人（二聯式）' },
                  { v: 'company', t: '公司（三聯式，可報帳）' },
                  { v: 'donation', t: '捐贈發票' },
                ] as const).map((o) => (
                  <button
                    key={o.v}
                    type="button"
                    onClick={() => setInvoice({ buyer_type: o.v, tax_id: '', title: '', carrier_type: '', carrier_id: '', love_code: '' })}
                    style={{
                      ...ghostBtn, textAlign: 'center',
                      border: invoice.buyer_type === o.v ? '1.5px solid var(--fug)' : '1px dashed var(--line-2)',
                      background: invoice.buyer_type === o.v ? 'rgba(45,212,150,.1)' : 'transparent',
                      color: invoice.buyer_type === o.v ? 'var(--fug)' : 'var(--tx)',
                    }}
                  >
                    {o.t}
                  </button>
                ))}
              </div>

              {invoice.buyer_type === 'personal' && (
                <label style={{ display: 'flex', flexDirection: 'column', gap: 5, marginBottom: 10 }}>
                  <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>手機條碼載具</span>
                  <input
                    style={inp} type="text" value={invoice.carrier_id}
                    onChange={(e) => {
                      const v = e.target.value
                      setInvoice((p) => ({ ...p, carrier_id: v, carrier_type: v.trim() ? 'mobile' : '' }))
                    }}
                    placeholder="例如 /ABC1234"
                  />
                  {invoice.carrier_id.trim() === '' ? (
                    <span style={hint}>未填寫將開立雲端發票存證</span>
                  ) : !CARRIER_ID_RE.test(invoice.carrier_id.trim()) ? (
                    <span style={{ fontSize: 12, color: 'var(--hunt)' }}>手機條碼載具格式有誤，請確認（例如 /ABC1234）</span>
                  ) : null}
                </label>
              )}

              {invoice.buyer_type === 'company' && (
                <>
                  <label style={{ display: 'flex', flexDirection: 'column', gap: 5, marginBottom: 10 }}>
                    <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
                      統一編號<span style={{ color: 'var(--hunt)' }}> *</span>
                    </span>
                    <input
                      style={inp} type="text" value={invoice.tax_id}
                      onChange={(e) => setInvoice((p) => ({ ...p, tax_id: e.target.value }))}
                    />
                    {invoice.tax_id.trim() !== '' && !isValidTwTaxId(invoice.tax_id.trim()) && (
                      <span style={{ fontSize: 12, color: 'var(--hunt)' }}>統一編號有誤，請確認</span>
                    )}
                  </label>
                  <label style={{ display: 'flex', flexDirection: 'column', gap: 5, marginBottom: 10 }}>
                    <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
                      發票抬頭<span style={{ color: 'var(--hunt)' }}> *</span>
                    </span>
                    <input
                      style={inp} type="text" value={invoice.title}
                      onChange={(e) => setInvoice((p) => ({ ...p, title: e.target.value }))}
                    />
                  </label>
                </>
              )}

              {invoice.buyer_type === 'donation' && (
                <label style={{ display: 'flex', flexDirection: 'column', gap: 5, marginBottom: 10 }}>
                  <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
                    愛心碼<span style={{ color: 'var(--hunt)' }}> *</span>
                  </span>
                  <input
                    style={inp} type="text" value={invoice.love_code}
                    onChange={(e) => setInvoice((p) => ({ ...p, love_code: e.target.value }))}
                  />
                  {invoice.love_code.trim() !== '' && !LOVE_CODE_RE.test(invoice.love_code.trim()) && (
                    <span style={{ fontSize: 12, color: 'var(--hunt)' }}>愛心碼格式有誤，請輸入 3-7 位數字</span>
                  )}
                </label>
              )}
            </Section>

            {/* 優惠序號 */}
            <Section title="優惠序號">
              <div style={{ display: 'flex', gap: 8 }}>
                <input
                  value={promoCode}
                  onChange={(e) => { setPromoCode(e.target.value.toUpperCase()); setPromoQuote(null) }}
                  placeholder={useCoupon ? '已使用活動優惠券' : '輸入序號（選填）'}
                  disabled={useCoupon}
                  style={{ ...inp, flex: 1, textTransform: 'uppercase', opacity: useCoupon ? 0.5 : 1 }}
                />
                <button onClick={applyPromo} disabled={promoBusy || useCoupon} style={{ ...primaryBtn, width: 'auto', padding: '0 18px', opacity: useCoupon ? 0.5 : 1 }}>
                  {promoBusy ? '驗證中…' : '套用'}
                </button>
              </div>
              {promoQuote && !useCoupon && (
                <div style={{ fontSize: 12.5, marginTop: 8, color: promoQuote.valid ? 'var(--fug)' : 'var(--hunt)' }}>
                  {promoQuote.valid
                    ? `✓ 已折抵 ${ntd(promoQuote.discount_cents)}${promoQuote.free ? '（0 元免付款）' : ''}`
                    : `✕ ${promoQuote.reason || '序號無效'}`}
                </div>
              )}
            </Section>

            {/* VIP 活動優惠券（$100，只折報名費，與序號擇一） */}
            {isVip && race.entry_fee > 0 && (
              <Section title="活動優惠券">
                <label style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10, fontSize: 13.5, color: 'var(--tx)', opacity: couponBal > 0 || useCoupon ? 1 : 0.5 }}>
                  <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <input
                      type="checkbox"
                      checked={useCoupon}
                      disabled={couponBal <= 0 || promoCode.trim() !== ''}
                      onChange={(e) => { const on = e.target.checked; setUseCoupon(on); if (on) { setPromoCode(''); setPromoQuote(null) } }}
                    />
                    使用活動優惠券（折 {ntd(COUPON_CENTS)}）
                  </span>
                  <span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>持有數量：{couponBal}</span>
                </label>
                <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4 }}>
                  VIP 專屬，每月補齊 3 張；與優惠序號擇一，不可並用。
                </div>
              </Section>
            )}

            <div style={{ paddingTop: 4 }}>
              {useCoupon && couponDiscount > 0 ? (
                <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12.5, color: 'var(--tx-dim)' }}>
                  <span>原價 {ntd(total)} · 優惠券折抵 −{ntd(couponDiscount)}</span>
                </div>
              ) : promoQuote?.valid && promoQuote.discount_cents > 0 ? (
                <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12.5, color: 'var(--tx-dim)' }}>
                  <span>原價 {ntd(total)} · 折抵 −{ntd(promoQuote.discount_cents)}</span>
                </div>
              ) : null}
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 4 }}>
                <span style={{ fontSize: 13, color: 'var(--tx-dim)' }}>應繳金額</span>
                <span style={{ fontSize: 18, fontWeight: 800, color: 'var(--gold)' }}>{ntd(payable)}</span>
              </div>
            </div>

            {err && <div style={{ color: 'var(--hunt)', fontSize: 13 }}>{err}</div>}
            <button onClick={submit} disabled={submitting} style={primaryBtn}>
              {submitting ? '送出中…' : isBattle ? '立即報名 – 隨機分組' : '確認報名'}
            </button>
            <div style={{ fontSize: 11, color: 'var(--tx-faint)', textAlign: 'center', lineHeight: 1.6 }}>
              送出即保留名額，狀態為「待繳費」。<br />
              城市探索為線上活動，<b style={{ color: 'var(--tx-dim)' }}>報名繳費後恕不退款、不適用七天鑑賞期</b>。送出即表示同意 <a href="/terms" target="_blank" rel="noreferrer" style={{ color: 'var(--fug)' }}>服務條款</a>。
            </div>
          </div>
        )}

        {err && (loading || !detail) && <Hint color="var(--hunt)">{err}</Hint>}
      </ScrollArea>

      {/* 全部分組 選單（分組過多時）：可逐組展開比較任務目標再選用 */}
      {showAllGroups && detail && (
        <div style={pickerOverlay} onClick={() => { setShowAllGroups(false); setPreviewId(null) }}>
          <div style={pickerSheet} onClick={(e) => e.stopPropagation()}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
              <div style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>全部分組（{detail.groups.length}）</div>
              <button onClick={() => { setShowAllGroups(false); setPreviewId(null) }} style={{ ...ghostBtn, padding: '6px 12px' }}>關閉</button>
            </div>
            {hasAllGroupsTasks && (
              <div style={{ background: 'rgba(45,212,150,.06)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 12px', marginBottom: 10 }}>
                <div style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--fug)' }}>所有分組共同任務</div>
                <TaskList label="團體任務（全組加總）" items={allGroupsTasks.team} />
                <TaskList label="個人任務（每人達成）" items={allGroupsTasks.individual} />
              </div>
            )}
            <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginBottom: 6 }}>點分組可展開查看該組專屬任務，再「選用此分組」。</div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8, overflowY: 'auto' }}>
              {detail.groups.map((g) => (
                <div key={g.id}>
                  {groupCard(g, () => setPreviewId(previewId === g.id ? null : g.id!), groupHasDetail(g.id!) ? previewId === g.id : undefined)}
                  {previewId === g.id && (
                    <div style={{ margin: '6px 0 2px' }}>
                      {renderGroupSpecific(g.id!)}
                      <button
                        onClick={() => { setGroupId(g.id!); setGroupKey(''); setShowAllGroups(false); setPreviewId(null) }}
                        style={{ ...primaryBtn, marginTop: 8 }}
                      >
                        選用此分組
                      </button>
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

const pickerOverlay: React.CSSProperties = {
  position: 'absolute', inset: 0, background: 'rgba(0,0,0,.55)', zIndex: 60,
  display: 'flex', alignItems: 'flex-end', justifyContent: 'center',
}
const pickerSheet: React.CSSProperties = {
  width: '100%', maxHeight: '78%', background: 'var(--bg)', borderTopLeftRadius: 18, borderTopRightRadius: 18,
  border: '1px solid var(--line)', padding: '16px 16px 22px', display: 'flex', flexDirection: 'column',
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <div style={{ fontSize: 14, fontWeight: 800, color: 'var(--tx)', marginBottom: 8 }}>{title}</div>
      {children}
    </div>
  )
}
function Hint({ children, color = 'var(--tx-dim)' }: { children: React.ReactNode; color?: string }) {
  return <div style={{ textAlign: 'center', padding: '50px 20px', fontSize: 13.5, color }}>{children}</div>
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0 }
const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 16, padding: 20 }
const groupRow: React.CSSProperties = {
  display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10,
  background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '12px 14px', width: '100%',
}
const hint: React.CSSProperties = { fontSize: 12, color: 'var(--tx-dim)', marginBottom: 4 }
const inp: React.CSSProperties = {
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10,
  padding: '10px 12px', color: 'var(--tx)', fontSize: 14, fontFamily: 'inherit',
}
const primaryBtn: React.CSSProperties = {
  background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none',
  borderRadius: 12, padding: '13px 20px', cursor: 'pointer', fontSize: 15, width: '100%',
}
const ghostBtn: React.CSSProperties = {
  background: 'transparent', color: 'var(--tx)', border: '1px dashed var(--line-2)',
  borderRadius: 10, padding: '10px 14px', cursor: 'pointer', fontSize: 13, fontFamily: 'inherit',
}
