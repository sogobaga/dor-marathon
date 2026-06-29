'use client'

import { useEffect, useMemo, useState } from 'react'
import {
  racesApi,
  profileApi,
  type Race,
  type RaceDetail,
  type RaceGroup,
  type RegistrationState,
  type ParticipantField,
} from '@/lib/api'
import { getUserToken, withUserAuth, SessionExpiredError, useUser } from '@/lib/userAuth'

const FIELD_LABEL: Record<ParticipantField, string> = {
  real_name: '真實姓名', nickname: '暱稱', phone: '手機',
  address: '地址', birthday: '生日', gender: '性別',
}
const GENDER_OPTS = [{ v: '', t: '請選擇' }, { v: 'male', t: '男' }, { v: 'female', t: '女' }, { v: 'other', t: '其他' }]

function ntd(cents: number) {
  return 'NT$ ' + Math.round(cents / 100).toLocaleString('zh-TW')
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
  const [submitting, setSubmitting] = useState(false)
  const [done, setDone] = useState<{ group: string; revealed: boolean; paid: boolean; payable: number } | null>(null)

  const [promoCode, setPromoCode] = useState('')
  const [promoQuote, setPromoQuote] = useState<import('@/lib/api').PromoQuote | null>(null)
  const [promoBusy, setPromoBusy] = useState(false)

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
      }).catch(() => {})
    }
  }, [race.id])

  const selectedGroup = useMemo(
    () => detail?.groups.find((g) => g.id === groupId) || null,
    [detail, groupId]
  )

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

  // 加購變動時，已套用的優惠序號重算（payable 含加購）
  const payable = promoQuote?.valid ? promoQuote.payable_cents : total

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
          promo_code: promoCode.trim() || undefined,
        })
      )
      setDone({ group: res.assigned_group, revealed: res.group_revealed, paid: res.paid, payable: res.payable_cents })
    } catch (e: any) {
      setErr(e instanceof SessionExpiredError ? '登入已過期，請回上一頁重新登入' : e?.message || '報名失敗')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: '52px 22px 14px', flexShrink: 0 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <h1 style={{ margin: '10px 0 2px', fontSize: 22, fontWeight: 800, color: 'var(--tx)' }}>{race.title}</h1>
        <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>報名</div>
      </header>

      <div style={{ flex: 1, overflowY: 'auto', padding: '4px 18px 28px' }}>
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
              {done.paid ? '已使用優惠序號 0 元完成，無需付款' : `應繳金額：${ntd(done.payable)}（後續繳費）`}
            </div>
            <button onClick={onBack} style={{ ...primaryBtn, marginTop: 14 }}>回賽事列表</button>
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
            {/* 分組 */}
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
                  {detail.groups.map((g) => {
                    const rem = remaining(g)
                    const full = rem === 0
                    return (
                      <button
                        key={g.id}
                        disabled={full}
                        onClick={() => { setGroupId(g.id!); setGroupKey('') }}
                        style={{
                          ...groupRow, cursor: full ? 'not-allowed' : 'pointer', opacity: full ? 0.5 : 1,
                          border: groupId === g.id ? '1px solid var(--fug)' : '1px solid var(--line)',
                          background: groupId === g.id ? 'rgba(45,212,150,.08)' : 'var(--bg-1)', textAlign: 'left',
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
                        <span style={{ fontSize: 12, color: full ? 'var(--hunt)' : 'var(--tx-dim)' }}>
                          {rem == null ? '名額不限' : full ? '已額滿' : `剩 ${rem}`}
                        </span>
                      </button>
                    )
                  })}

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

                  {/* 開放跑團分組申請：前台自建分組 */}
                  {detail.allow_team_groups && detail.can_register && (
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

            {/* 優惠序號 */}
            <Section title="優惠序號">
              <div style={{ display: 'flex', gap: 8 }}>
                <input
                  value={promoCode}
                  onChange={(e) => { setPromoCode(e.target.value.toUpperCase()); setPromoQuote(null) }}
                  placeholder="輸入序號（選填）"
                  style={{ ...inp, flex: 1, textTransform: 'uppercase' }}
                />
                <button onClick={applyPromo} disabled={promoBusy} style={{ ...primaryBtn, width: 'auto', padding: '0 18px' }}>
                  {promoBusy ? '驗證中…' : '套用'}
                </button>
              </div>
              {promoQuote && (
                <div style={{ fontSize: 12.5, marginTop: 8, color: promoQuote.valid ? 'var(--fug)' : 'var(--hunt)' }}>
                  {promoQuote.valid
                    ? `✓ 已折抵 ${ntd(promoQuote.discount_cents)}${promoQuote.free ? '（0 元免付款）' : ''}`
                    : `✕ ${promoQuote.reason || '序號無效'}`}
                </div>
              )}
            </Section>

            <div style={{ paddingTop: 4 }}>
              {promoQuote?.valid && promoQuote.discount_cents > 0 && (
                <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12.5, color: 'var(--tx-dim)' }}>
                  <span>原價 {ntd(total)} · 折抵 −{ntd(promoQuote.discount_cents)}</span>
                </div>
              )}
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 4 }}>
                <span style={{ fontSize: 13, color: 'var(--tx-dim)' }}>應繳金額</span>
                <span style={{ fontSize: 18, fontWeight: 800, color: 'var(--gold)' }}>{ntd(payable)}</span>
              </div>
            </div>

            {err && <div style={{ color: 'var(--hunt)', fontSize: 13 }}>{err}</div>}
            <button onClick={submit} disabled={submitting} style={primaryBtn}>
              {submitting ? '送出中…' : isBattle ? '立即報名 – 隨機分組' : '確認報名'}
            </button>
            <div style={{ fontSize: 11, color: 'var(--tx-faint)', textAlign: 'center' }}>送出即保留名額，狀態為「待繳費」。</div>
          </div>
        )}

        {err && (loading || !detail) && <Hint color="var(--hunt)">{err}</Hint>}
      </div>
    </div>
  )
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
  background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none',
  borderRadius: 12, padding: '13px 20px', cursor: 'pointer', fontSize: 15, width: '100%',
}
const ghostBtn: React.CSSProperties = {
  background: 'transparent', color: 'var(--tx)', border: '1px dashed var(--line-2)',
  borderRadius: 10, padding: '10px 14px', cursor: 'pointer', fontSize: 13, fontFamily: 'inherit',
}
