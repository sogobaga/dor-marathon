'use client'

// DORPG P7（武器裝備，見 scratchpad/dorpg_p7/CONTRACT.md §2/§3、WIRE.md §REST）：沿用
// CharacterScreen 的元件與樣式語言（同一批 var(--bg)/var(--tx)/var(--gold) CSS 變數、同一種
// header/section/卡片排版），但比照 TavernScreen 的既有慣例——各畫面各自獨立成檔，不互相 import
// 對方的子元件或樣式常數，欄位也不共用（這裡只讀/寫 GET /rpg/equipment、PUT /rpg/equipment/weapon）。
//
// 畫面結構（任務 §6）：
//   1. 頂部「目前武器卡」——名稱／等級門檻／稀有度／屬性／關鍵效果文字／卸下。
//   2. 三個武器類型分頁（該職業的 weapon_types），每頁 10 級列表；屬性版武器另有七顆屬性 chip
//      可切換查看/裝備該屬性的同級武器。
// 全站規則：手機優先、金底白字、不新增 fixed 覆蓋層（整頁在 PhoneShell 的既有畫面切換鏈裡佔一格，
// 同 CharacterScreen/TavernScreen 的既有慣例）。
import { useCallback, useEffect, useState } from 'react'
import { rpgEquipmentApi, type RpgEquipmentResponse, type RpgElement, type WeaponDTO, type WeaponTypeDTO } from '@/lib/api'
import { getUserToken, withUserAuth } from '@/lib/userAuth'
import { ELEMENT_LABEL, ELEMENT_ORDER, RARITY_LABEL, RARITY_COLOR, elementLabel, formatWeaponProfile } from '@/lib/rpgMeta'

// PUT /rpg/equipment/weapon 400 錯誤碼 → 中文（WIRE §REST）。
const EQUIP_ERR_LABEL: Record<string, string> = {
  not_found: '找不到這件武器',
  wrong_job: '這件武器不屬於目前職業',
  level_too_low: '等級尚未達到裝備門檻',
}
function friendlyErr(e: any, table: Record<string, string>, fallback: string): string {
  const code = e?.message
  return (code && table[code]) || (typeof code === 'string' && code && code !== 'request failed' ? code : '') || fallback
}

export default function EquipmentScreen({ onBack }: { onBack: () => void }) {
  const [data, setData] = useState<RpgEquipmentResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadErr, setLoadErr] = useState('')
  const [actionErr, setActionErr] = useState('')
  const [flash, setFlash] = useState('')
  const [busyId, setBusyId] = useState<string | null>(null) // 裝備中的武器 id，或 'unequip'
  const [activeTypeId, setActiveTypeId] = useState<string | null>(null)
  // 屬性版武器：每個類型各自記住目前選到哪個屬性 chip（含 'neutral' 代表「無屬性版」），
  // key 是 type_id。切分頁不清空，回到同一分頁時記得上次選的屬性。
  const [elementByType, setElementByType] = useState<Record<string, RpgElement>>({})

  function showErr(msg: string) {
    setActionErr(msg)
    window.setTimeout(() => setActionErr(''), 3500)
  }
  function showFlash(msg: string) {
    setFlash(msg)
    window.setTimeout(() => setFlash(''), 1800)
  }

  const load = useCallback(() => {
    if (!getUserToken()) { setLoading(false); setLoadErr('請先登入'); return }
    setLoading(true)
    withUserAuth((t) => rpgEquipmentApi.get(t))
      .then((r) => {
        setData(r)
        setActiveTypeId((cur) => cur && r.weapon_types.some((wt) => wt.id === cur) ? cur : (r.weapon_types[0]?.id ?? null))
        setLoadErr('')
      })
      .catch((e: any) => setLoadErr(e?.status === 403 ? '此功能尚未開放給你的帳號' : e?.message || '載入失敗'))
      .finally(() => setLoading(false))
  }, [])
  useEffect(() => { load() }, [load])

  async function equip(weaponId: string) {
    if (busyId) return
    setBusyId(weaponId)
    try {
      const next = await withUserAuth((t) => rpgEquipmentApi.setWeapon(t, weaponId))
      setData(next)
      const w = next.weapons.find((x) => x.id === weaponId)
      showFlash(`已裝備「${w?.name ?? weaponId}」`)
    } catch (e: any) {
      showErr(friendlyErr(e, EQUIP_ERR_LABEL, '裝備失敗，請稍後再試'))
    } finally {
      setBusyId(null)
    }
  }

  async function unequip() {
    if (busyId) return
    setBusyId('unequip')
    try {
      const next = await withUserAuth((t) => rpgEquipmentApi.setWeapon(t, null))
      setData(next)
      showFlash('已卸下武器')
    } catch (e: any) {
      showErr(friendlyErr(e, EQUIP_ERR_LABEL, '卸下失敗，請稍後再試'))
    } finally {
      setBusyId(null)
    }
  }

  const activeType: WeaponTypeDTO | null = data?.weapon_types.find((t) => t.id === activeTypeId) ?? null
  const activeElement: RpgElement = (activeTypeId && elementByType[activeTypeId]) || 'neutral'
  const listForActiveType = data && activeType
    ? data.weapons
        .filter((w) => w.type_id === activeType.id && (activeType.elemental_capable ? w.element === activeElement : true))
        .sort((a, b) => a.tier - b.tier)
    : []

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <h1 style={{ margin: '10px 0 2px', fontSize: 23, fontWeight: 800, color: 'var(--tx)' }}>🗡️ 裝備</h1>
        <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>武器（測試階段）</div>
      </header>

      <div style={{ flex: 1, overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', padding: '14px 18px 28px' }}>
        {loading && <Hint>載入中…</Hint>}
        {!loading && (!data || loadErr) && <Hint>{loadErr || '此功能尚未開放給你的帳號。'}</Hint>}

        {!loading && data && (
          <>
            {actionErr && <div style={errBanner}>{actionErr}</div>}
            {flash && <div style={okBanner}>✓ {flash}</div>}

            {/* ---- 目前武器卡 ---- */}
            <CurrentWeaponCard weapon={data.equipped.weapon} busy={busyId === 'unequip'} onUnequip={unequip} />

            {!data.job && <Hint>先在「角色」頁選擇職業，才能查看與裝備武器</Hint>}

            {data.job && data.weapon_types.length > 0 && (
              <>
                {/* ---- 類型分頁 ---- */}
                <div style={{ display: 'flex', gap: 6, marginTop: 20, borderBottom: '1px solid var(--line)', overflowX: 'auto' }}>
                  {data.weapon_types.map((wt) => (
                    <button
                      key={wt.id}
                      onClick={() => setActiveTypeId(wt.id)}
                      style={{
                        padding: '8px 14px', border: 'none', background: 'none', cursor: 'pointer', fontSize: 13.5,
                        color: wt.id === activeTypeId ? 'var(--tx)' : 'var(--tx-dim)',
                        borderBottom: wt.id === activeTypeId ? '2px solid var(--fug)' : '2px solid transparent',
                        fontWeight: wt.id === activeTypeId ? 700 : 400, flexShrink: 0, whiteSpace: 'nowrap', fontFamily: 'inherit',
                      }}
                    >
                      {wt.name}
                    </button>
                  ))}
                </div>

                {activeType && (
                  <div style={{ marginTop: 12 }}>
                    {activeType.description && (
                      <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', lineHeight: 1.5, marginBottom: 10 }}>{activeType.description}</div>
                    )}

                    {/* ---- 屬性版七顆 chip（僅該類型可有屬性時顯示） ---- */}
                    {activeType.elemental_capable && (
                      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginBottom: 12 }}>
                        {(['neutral', ...ELEMENT_ORDER] as RpgElement[]).map((el) => {
                          const on = activeElement === el
                          return (
                            <button
                              key={el}
                              onClick={() => setElementByType((prev) => ({ ...prev, [activeType.id]: el }))}
                              style={{ ...elementChip, ...(on ? elementChipActive : {}) }}
                            >
                              {ELEMENT_LABEL[el]}
                            </button>
                          )
                        })}
                      </div>
                    )}

                    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                      {listForActiveType.length === 0 && <div style={{ fontSize: 11.5, color: 'var(--tx-faint)' }}>（此類型暫無武器資料）</div>}
                      {listForActiveType.map((w) => (
                        <WeaponRow key={w.id} weapon={w} busy={busyId === w.id} onEquip={() => equip(w.id)} />
                      ))}
                    </div>
                  </div>
                )}
              </>
            )}

            {data.job && data.weapon_types.length === 0 && <Hint>此職業暫無可裝備的武器類型資料</Hint>}
          </>
        )}
      </div>
    </div>
  )
}

// ============================== 子元件 ==============================

function CurrentWeaponCard({ weapon, busy, onUnequip }: { weapon: WeaponDTO | null; busy: boolean; onUnequip: () => void }) {
  if (!weapon) {
    return (
      <div style={currentCard}>
        <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>尚未裝備武器</div>
      </div>
    )
  }
  const effect = formatWeaponProfile(weapon.profile)
  return (
    <div style={currentCard}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 8 }}>
        <div style={{ minWidth: 0 }}>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 6, flexWrap: 'wrap' }}>
            <span style={{ fontSize: 16, fontWeight: 800, color: 'var(--tx)' }}>{weapon.name}</span>
            <RarityTag rarity={weapon.rarity} />
            {weapon.element !== 'neutral' && <ElementTag element={weapon.element} />}
          </div>
          <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 3 }}>
            需 Lv.{weapon.level_req}
            {weapon.profile.atk ? `・ATK ${weapon.profile.atk}` : ''}
            {weapon.profile.matk ? `・MATK ${weapon.profile.matk}` : ''}
          </div>
          {effect && <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginTop: 6, lineHeight: 1.6 }}>{effect}</div>}
        </div>
        <button onClick={onUnequip} disabled={busy} style={{ ...dangerGhostBtn, flexShrink: 0 }}>{busy ? '卸下中…' : '卸下'}</button>
      </div>
    </div>
  )
}

function WeaponRow({ weapon, busy, onEquip }: { weapon: WeaponDTO; busy: boolean; onEquip: () => void }) {
  const effect = formatWeaponProfile(weapon.profile)
  const label = weapon.equipped ? '已裝備' : weapon.can_equip ? '裝備' : `需 Lv.${weapon.level_req}`
  const enabled = !weapon.equipped && weapon.can_equip && !busy
  return (
    <div style={{ ...weaponRowStyle, opacity: weapon.can_equip || weapon.equipped ? 1 : 0.55 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 6, flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 6, minWidth: 0 }}>
          <span style={{ fontSize: 13.5, fontWeight: 800, color: 'var(--tx)' }}>{weapon.name}</span>
          <span style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>Lv.{weapon.level_req}</span>
        </div>
        <div style={{ display: 'flex', gap: 4, alignItems: 'center', flexShrink: 0 }}>
          <RarityTag rarity={weapon.rarity} />
          {weapon.element !== 'neutral' && <ElementTag element={weapon.element} />}
        </div>
      </div>

      <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 4 }}>
        {weapon.profile.atk ? `ATK ${weapon.profile.atk}` : ''}
        {weapon.profile.atk && weapon.profile.matk ? '・' : ''}
        {weapon.profile.matk ? `MATK ${weapon.profile.matk}` : ''}
      </div>
      {effect && <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 3, lineHeight: 1.5 }}>{effect}</div>}

      <div style={{ marginTop: 8 }}>
        <button
          disabled={!enabled}
          onClick={onEquip}
          style={{ ...equipBtn, opacity: enabled ? 1 : weapon.equipped ? 0.9 : 0.4, cursor: enabled ? 'pointer' : 'default' }}
        >
          {busy ? '處理中…' : label}
        </button>
      </div>
    </div>
  )
}

function RarityTag({ rarity }: { rarity: WeaponDTO['rarity'] }) {
  return <span style={{ ...tagStyle, background: RARITY_COLOR[rarity], color: '#fff' }}>{RARITY_LABEL[rarity]}</span>
}
function ElementTag({ element }: { element: string }) {
  return <span style={{ ...tagStyle, background: 'var(--bg-2)', color: 'var(--tx)' }}>{elementLabel(element)}</span>
}

function Hint({ children }: { children: React.ReactNode }) {
  return <div style={{ textAlign: 'center', color: 'var(--tx-dim)', fontSize: 13, padding: '18px 0' }}>{children}</div>
}

// 金底白字（全站通則）
const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', fontSize: 13, padding: 0, cursor: 'pointer', fontFamily: 'inherit' }
const errBanner: React.CSSProperties = { background: 'rgba(244,98,58,.12)', color: '#f4623a', border: '1px solid rgba(244,98,58,.3)', borderRadius: 10, padding: '8px 12px', fontSize: 12.5, marginBottom: 10 }
const okBanner: React.CSSProperties = { background: 'rgba(45,212,150,.12)', color: 'var(--fug)', border: '1px solid rgba(45,212,150,.3)', borderRadius: 10, padding: '8px 12px', fontSize: 12.5, marginBottom: 10 }
const currentCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--gold)', borderRadius: 14, padding: '12px 14px' }
const weaponRowStyle: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 12px' }
const dangerGhostBtn: React.CSSProperties = { background: 'none', border: '1px solid rgba(244,98,58,.4)', color: '#f4623a', borderRadius: 8, padding: '6px 12px', fontSize: 11.5, fontWeight: 700, fontFamily: 'inherit', cursor: 'pointer' }
const tagStyle: React.CSSProperties = { fontSize: 9.5, fontWeight: 700, borderRadius: 999, padding: '2px 7px', whiteSpace: 'nowrap' }
// 金底白字（全站通則）：裝備按鈕用金底。
const equipBtn: React.CSSProperties = { background: 'var(--gold)', color: '#fff', border: 'none', borderRadius: 8, padding: '6px 14px', fontSize: 12, fontWeight: 800, fontFamily: 'inherit' }
// 審查#6：這裡原本用 shorthand `border`，elementChipActive 疊加時只覆寫 longhand `borderColor`——
// React 對「同一個 style 物件在不同 render 之間混用 shorthand／longhand 同一種屬性」會印警告
// （shorthand 與 longhand 在 DOM style 更新時的覆寫順序不保證，可能造成沒真的套上新顏色的視覺
// bug）。改成 elementChip 也拆成 longhand（borderWidth/borderStyle/borderColor），兩邊統一只用
// longhand，覆寫時單純是 borderColor 蓋 borderColor，沒有混用問題。
const elementChip: React.CSSProperties = { background: 'var(--bg-1)', borderWidth: 1, borderStyle: 'solid', borderColor: 'var(--line)', color: 'var(--tx-dim)', borderRadius: 999, padding: '5px 12px', fontSize: 12, fontWeight: 700, fontFamily: 'inherit', cursor: 'pointer' }
// 金底白字（全站通則）：選中的屬性 chip 用金底，文字強制白色。
const elementChipActive: React.CSSProperties = { background: 'var(--gold)', borderColor: 'var(--gold)', color: '#fff' }
