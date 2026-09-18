'use client'

// DORPG P7/P8（裝備，見 scratchpad/dorpg_p8/CONTRACT.md §4/§5、WIRE.md §REST）：沿用
// CharacterScreen 的元件與樣式語言（同一批 var(--bg)/var(--tx)/var(--gold) CSS 變數、同一種
// header/section/卡片排版），但比照 TavernScreen 的既有慣例——各畫面各自獨立成檔，不互相 import
// 對方的子元件或樣式常數，欄位也不共用（這裡讀/寫 GET /rpg/equipment、PUT /rpg/equipment/weapon、
// PUT /rpg/equipment/{防具格}）。
//
// 畫面結構（P8 任務 §2 改版）：
//   1. 頂部「裝備欄八格」——武器、頭盔、手套、衣服、褲裙、鞋子、飾品1、飾品2；顯示名稱或「空」，
//      點格子切換下方清單，選中格高亮（金底白字）。
//   2. 「目前這格」卡片——名稱／等級門檻／稀有度／（武器才有）屬性／效果文字／卸下鈕；未裝備顯示
//      「尚未裝備」。
//   3. 下方清單依選中格類型分岔：
//      - 武器格：沿用 P7 既有的三個武器類型分頁＋屬性版七顆 chip。
//      - 防具格（頭盔/手套/衣服/褲裙/鞋子）：該部位本職業 10 級清單；未選職業顯示提示。
//      - 飾品格（飾品1/飾品2）：依效果分 18 組列出，每組 5 級，按鈕文字＝「裝到飾品1」/「裝到
//        飾品2」（依目前選中的是哪一格），已裝在另一飾品格的顯示「已裝於飾品X」並停用（後端仍會
//        以 duplicate_accessory 擋，這裡只是預先給提示，不是唯一防線）。
// 全站規則：手機優先、金底白字、不新增 fixed 覆蓋層（整頁在 PhoneShell 的既有畫面切換鏈裡佔一格，
// 同 CharacterScreen/TavernScreen 的既有慣例）。
import { useCallback, useEffect, useState } from 'react'
import {
  rpgEquipmentApi,
  type ArmorDTO, type ArmorEquipSlot, type EquipmentSlot, type RpgElement, type RpgEquipmentResponse,
  type WeaponDTO, type WeaponTypeDTO,
} from '@/lib/api'
import { getUserToken, withUserAuth } from '@/lib/userAuth'
import {
  ACCESSORY_EFFECT_LABEL, ACCESSORY_EFFECT_ORDER, ARMOR_EQUIP_SLOTS, ELEMENT_LABEL, ELEMENT_ORDER, EQUIP_SLOT_LABEL,
  EQUIP_SLOT_ORDER, RARITY_LABEL, RARITY_COLOR, accessoryEffectKey, elementLabel, formatArmorProfile, formatWeaponProfile,
} from '@/lib/rpgMeta'

// PUT /rpg/equipment/{slot} 400 錯誤碼 → 中文（WIRE §REST：not_found／wrong_job／wrong_slot／
// level_too_low／duplicate_accessory，P7 既有三個 + P8 新增兩個）。
const EQUIP_ERR_LABEL: Record<string, string> = {
  not_found: '找不到這件裝備',
  wrong_job: '這件裝備不屬於目前職業',
  wrong_slot: '這件裝備不能裝在這個格子',
  level_too_low: '等級尚未達到裝備門檻',
  duplicate_accessory: '兩個飾品格不能裝同一件',
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
  const [busyId, setBusyId] = useState<string | null>(null) // 裝備中的品項 id，或 'unequip'
  const [activeSlot, setActiveSlot] = useState<EquipmentSlot>('weapon')
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

  async function equip(itemId: string) {
    if (busyId) return
    setBusyId(itemId)
    try {
      const next = activeSlot === 'weapon'
        ? await withUserAuth((t) => rpgEquipmentApi.setWeapon(t, itemId))
        : await withUserAuth((t) => rpgEquipmentApi.setSlot(t, activeSlot as ArmorEquipSlot, itemId))
      setData(next)
      const name = activeSlot === 'weapon'
        ? next.weapons.find((w) => w.id === itemId)?.name
        : next.armor_items.find((a) => a.id === itemId)?.name
      showFlash(`已裝備「${name ?? itemId}」`)
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
      const next = activeSlot === 'weapon'
        ? await withUserAuth((t) => rpgEquipmentApi.setWeapon(t, null))
        : await withUserAuth((t) => rpgEquipmentApi.setSlot(t, activeSlot as ArmorEquipSlot, null))
      setData(next)
      showFlash(`已卸下${EQUIP_SLOT_LABEL[activeSlot]}`)
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
  const isArmorSlot = ARMOR_EQUIP_SLOTS.includes(activeSlot)
  const isAccessorySlot = activeSlot === 'accessory1' || activeSlot === 'accessory2'
  const currentItem: WeaponDTO | ArmorDTO | null = data ? (data.equipped as any)[activeSlot] ?? null : null

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <h1 style={{ margin: '10px 0 2px', fontSize: 23, fontWeight: 800, color: 'var(--tx)' }}>🗡️ 裝備</h1>
        <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>武器／防具／飾品（測試階段）</div>
      </header>

      <div style={{ flex: 1, overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', padding: '14px 18px 28px' }}>
        {loading && <Hint>載入中…</Hint>}
        {!loading && (!data || loadErr) && <Hint>{loadErr || '此功能尚未開放給你的帳號。'}</Hint>}

        {!loading && data && (
          <>
            {actionErr && <div style={errBanner}>{actionErr}</div>}
            {flash && <div style={okBanner}>✓ {flash}</div>}

            {/* ---- 裝備欄八格 ---- */}
            <SlotBar equipped={data.equipped} activeSlot={activeSlot} onSelect={setActiveSlot} />

            {/* ---- 目前這格卡片 ---- */}
            <CurrentSlotCard slot={activeSlot} item={currentItem} busy={busyId === 'unequip'} onUnequip={unequip} />

            {/* ---- 武器格：沿用 P7 三分頁 ---- */}
            {activeSlot === 'weapon' && (
              <>
                {!data.job && <Hint>先在「角色」頁選擇職業，才能查看與裝備武器</Hint>}

                {data.job && data.weapon_types.length > 0 && (
                  <>
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

            {/* ---- 防具格：該部位本職業 10 級 ---- */}
            {isArmorSlot && (
              !data.job ? (
                <Hint>先在「角色」頁選擇職業，才能查看與裝備防具</Hint>
              ) : (
                <ArmorList slot={activeSlot} items={data.armor_items} busyId={busyId} onEquip={equip} />
              )
            )}

            {/* ---- 飾品格：18 組×5 級 ---- */}
            {isAccessorySlot && (
              <AccessoryList activeSlot={activeSlot as 'accessory1' | 'accessory2'} items={data.armor_items} busyId={busyId} onEquip={equip} />
            )}
          </>
        )}
      </div>
    </div>
  )
}

// ============================== 子元件 ==============================

function SlotBar({ equipped, activeSlot, onSelect }: { equipped: RpgEquipmentResponse['equipped']; activeSlot: EquipmentSlot; onSelect: (s: EquipmentSlot) => void }) {
  return (
    <div style={{ display: 'flex', gap: 8, overflowX: 'auto', paddingBottom: 4, marginBottom: 14, WebkitOverflowScrolling: 'touch' }}>
      {EQUIP_SLOT_ORDER.map((s) => {
        const item = (equipped as any)[s] as WeaponDTO | ArmorDTO | null
        const on = s === activeSlot
        return (
          <button key={s} onClick={() => onSelect(s)} style={{ ...slotChip, ...(on ? slotChipActive : {}) }}>
            <div style={{ fontSize: 9.5, opacity: 0.85 }}>{EQUIP_SLOT_LABEL[s]}</div>
            <div style={{ fontSize: 11, fontWeight: 700, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', maxWidth: 66 }}>
              {item?.name ?? '空'}
            </div>
          </button>
        )
      })}
    </div>
  )
}

function CurrentSlotCard({ slot, item, busy, onUnequip }: { slot: EquipmentSlot; item: WeaponDTO | ArmorDTO | null; busy: boolean; onUnequip: () => void }) {
  if (!item) {
    return (
      <div style={currentCard}>
        <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginBottom: 2 }}>{EQUIP_SLOT_LABEL[slot]}</div>
        <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>尚未裝備</div>
      </div>
    )
  }
  const isWeapon = slot === 'weapon'
  const w = isWeapon ? (item as WeaponDTO) : null
  const a = !isWeapon ? (item as ArmorDTO) : null
  const effect = isWeapon ? formatWeaponProfile(w!.profile) : formatArmorProfile(a!.profile)
  return (
    <div style={currentCard}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 8 }}>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginBottom: 2 }}>{EQUIP_SLOT_LABEL[slot]}</div>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 6, flexWrap: 'wrap' }}>
            <span style={{ fontSize: 16, fontWeight: 800, color: 'var(--tx)' }}>{item.name}</span>
            <RarityTag rarity={item.rarity} />
            {w && w.element !== 'neutral' && <ElementTag element={w.element} />}
          </div>
          <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 3 }}>
            需 Lv.{item.level_req}
            {w?.profile.atk ? `・ATK ${w.profile.atk}` : ''}
            {w?.profile.matk ? `・MATK ${w.profile.matk}` : ''}
            {a?.profile.def ? `・DEF ${a.profile.def}` : ''}
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

/** 防具格清單（頭盔/手套/衣服/褲裙/鞋子）：該部位本職業 10 級，依 tier 排序。 */
function ArmorList({ slot, items, busyId, onEquip }: { slot: EquipmentSlot; items: ArmorDTO[]; busyId: string | null; onEquip: (id: string) => void }) {
  const list = items.filter((a) => a.slot === slot).sort((a, b) => a.tier - b.tier)
  if (list.length === 0) return <Hint>此部位暫無防具資料</Hint>
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 14 }}>
      {list.map((a) => <ArmorRow key={a.id} item={a} busy={busyId === a.id} onEquip={() => onEquip(a.id)} />)}
    </div>
  )
}

function ArmorRow({ item, busy, onEquip }: { item: ArmorDTO; busy: boolean; onEquip: () => void }) {
  const effect = formatArmorProfile(item.profile)
  const equipped = item.equipped_in != null
  const label = equipped ? '已裝備' : item.can_equip ? '裝備' : `需 Lv.${item.level_req}`
  const enabled = !equipped && item.can_equip && !busy
  return (
    <div style={{ ...weaponRowStyle, opacity: item.can_equip || equipped ? 1 : 0.55 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 6, flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 6, minWidth: 0 }}>
          <span style={{ fontSize: 13.5, fontWeight: 800, color: 'var(--tx)' }}>{item.name}</span>
          <span style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>Lv.{item.level_req}</span>
        </div>
        <RarityTag rarity={item.rarity} />
      </div>

      {item.profile.def ? <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 4 }}>DEF {item.profile.def}</div> : null}
      {effect && <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 3, lineHeight: 1.5 }}>{effect}</div>}

      <div style={{ marginTop: 8 }}>
        <button
          disabled={!enabled}
          onClick={onEquip}
          style={{ ...equipBtn, opacity: enabled ? 1 : equipped ? 0.9 : 0.4, cursor: enabled ? 'pointer' : 'default' }}
        >
          {busy ? '處理中…' : label}
        </button>
      </div>
    </div>
  )
}

/** 飾品格清單（飾品1/飾品2）：依效果分 18 組，每組列出 5 級。 */
function AccessoryList({ activeSlot, items, busyId, onEquip }: { activeSlot: 'accessory1' | 'accessory2'; items: ArmorDTO[]; busyId: string | null; onEquip: (id: string) => void }) {
  const accessories = items.filter((a) => a.slot === 'accessory')
  const groups = ACCESSORY_EFFECT_ORDER
    .map((key) => ({
      key,
      label: ACCESSORY_EFFECT_LABEL[key],
      tiers: accessories.filter((a) => accessoryEffectKey(a.profile) === key).sort((a, b) => a.tier - b.tier),
    }))
    .filter((g) => g.tiers.length > 0)

  if (groups.length === 0) return <Hint>暫無飾品資料</Hint>

  return (
    <div style={{ marginTop: 14 }}>
      {groups.map((g) => (
        <div key={g.key} style={{ marginBottom: 14 }}>
          <div style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--gold)', margin: '0 0 6px' }}>{g.label}</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {g.tiers.map((t) => (
              <AccessoryRow key={t.id} item={t} activeSlot={activeSlot} busy={busyId === t.id} onEquip={() => onEquip(t.id)} />
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}

function AccessoryRow({ item, activeSlot, busy, onEquip }: { item: ArmorDTO; activeSlot: 'accessory1' | 'accessory2'; busy: boolean; onEquip: () => void }) {
  const effect = formatArmorProfile(item.profile)
  const equippedHere = item.equipped_in === activeSlot
  const equippedElsewhere = item.equipped_in != null && !equippedHere
  const otherLabel = item.equipped_in === 'accessory1' ? '飾品1' : item.equipped_in === 'accessory2' ? '飾品2' : ''
  const targetLabel = activeSlot === 'accessory1' ? '裝到飾品1' : '裝到飾品2'
  const label = equippedHere ? '已裝備' : equippedElsewhere ? `已裝於${otherLabel}` : item.can_equip ? targetLabel : `需 Lv.${item.level_req}`
  const enabled = !equippedHere && !equippedElsewhere && item.can_equip && !busy
  return (
    <div style={{ ...weaponRowStyle, opacity: item.can_equip || equippedHere ? 1 : 0.55 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 6, flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 6, minWidth: 0 }}>
          <span style={{ fontSize: 13.5, fontWeight: 800, color: 'var(--tx)' }}>{item.name}</span>
          <span style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>Lv.{item.level_req}</span>
        </div>
        <RarityTag rarity={item.rarity} />
      </div>
      {effect && <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.5 }}>{effect}</div>}
      <div style={{ marginTop: 8 }}>
        <button
          disabled={!enabled}
          onClick={onEquip}
          style={{ ...equipBtn, opacity: enabled ? 1 : equippedHere ? 0.9 : 0.4, cursor: enabled ? 'pointer' : 'default' }}
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
  return <span style={{ ...tagStyle, background: 'var(--bg-1)', color: 'var(--tx)' }}>{elementLabel(element)}</span>
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
// P8：裝備欄八格。同樣拆成 longhand 邊框屬性，理由同上 elementChip/elementChipActive。
const slotChip: React.CSSProperties = {
  background: 'var(--bg-1)', borderWidth: 1, borderStyle: 'solid', borderColor: 'var(--line)', color: 'var(--tx-dim)',
  borderRadius: 10, padding: '6px 10px', minWidth: 68, textAlign: 'center', flexShrink: 0, cursor: 'pointer', fontFamily: 'inherit',
}
// 金底白字（全站通則）：選中的格子用金底，文字強制白色。
const slotChipActive: React.CSSProperties = { background: 'var(--gold)', borderColor: 'var(--gold)', color: '#fff' }
