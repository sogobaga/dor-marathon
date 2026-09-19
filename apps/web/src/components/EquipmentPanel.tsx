'use client'

// DORPG P9（傭兵裝備比照玩家，見 scratchpad/dorpg_p9/CONTRACT.md §1、WIRE.md §5）：把原本內嵌在
// EquipmentScreen 的「裝備欄八格＋目前這格卡片＋依格子類型分岔的清單（武器三分頁＋屬性 chip／
// 防具本職業 10 級／飾品 18 組×5 級）」抽成這個共用元件，讓 EquipmentScreen（玩家自己的裝備頁）
// 與 TavernScreen 腳本編輯器（傭兵腳本草稿的「裝備」區）共用同一套 UI／互動邏輯——兩邊只有「資料
// 從哪裡來」與「選擇後要不要立刻打 API」不同：EquipmentScreen 每次 onEquip 都是一次真正的 PUT
// 請求；TavernScreen 只是把選到的 item_id 記進草稿 state，存檔時才整包送出。這裡完全不知道呼叫端
// 要怎麼處理選擇結果，onEquip(slot, itemId|null) 純粹回報「使用者選了這格要裝這個（或卸下）」。
//
// 跟原本 EquipmentScreen 的行為差異：無——這裡就是原本那份 JSX/邏輯搬過來，只把「整包 data:
// RpgEquipmentResponse」拆成明確的 props（equipped/weaponTypes/weapons/armorItems/jobId），並把
// 原本的「busyId 是不是就是這個 itemId」判斷邏輯原封不動保留。選格子／選武器分頁／選屬性 chip
// 這幾個純 UI 選取狀態留在元件內部，呼叫端不需要知道使用者目前在看哪一格。
import { useState } from 'react'
import type { ArmorDTO, EquipmentSlot, EquippedGearDTO, RpgElement, WeaponDTO, WeaponTypeDTO } from '@/lib/api'
import {
  ACCESSORY_EFFECT_LABEL, ACCESSORY_EFFECT_ORDER, ARMOR_EQUIP_SLOTS, ELEMENT_LABEL, ELEMENT_ORDER, EQUIP_SLOT_LABEL,
  EQUIP_SLOT_ORDER, RARITY_LABEL, RARITY_COLOR, accessoryEffectKey, elementLabel, formatArmorProfile, formatWeaponProfile,
} from '@/lib/rpgMeta'

export type EquipmentPanelProps = {
  /** 八格目前裝備（各格 WeaponDTO|ArmorDTO|null）。 */
  equipped: EquippedGearDTO
  weaponTypes: WeaponTypeDTO[]
  weapons: WeaponDTO[]
  armorItems: ArmorDTO[]
  /** 這份配置的等級（玩家＝effective_level；腳本＝草稿等級）——只用於畫面上的一句提示文字，
   *  can_equip／level_req 門檻一律信任呼叫端傳進來的 weapons/armorItems 各自欄位，這裡不重算。 */
  level: number
  /** 目前職業 id；null＝未選職業（武器／防具兩區塊會改顯示「先選職業」提示，飾品不受影響，
   *  同原本 EquipmentScreen 的既有行為）。 */
  jobId: string | null
  /** 處理中的品項 id，或 'unequip'（正在卸下目前這格）——沿用 EquipmentScreen 既有的 busyId 慣例：
   *  只鎖住「這一個品項的按鈕」與「目前這格卡片的卸下鈕」，不影響其他列。 */
  busy: string | null
  onEquip: (slot: EquipmentSlot, itemId: string | null) => void
  /**
   * 各格驗證錯誤文案（DORPG P9：腳本編輯器 validate 回應的 equipment.<slot> 錯誤，呼叫端已轉成
   * 中文）。只有「目前選中格」會顯示在目前這格卡片下方；其餘格子若也有錯誤，SlotBar 的格子上會
   * 疊一個小紅點提醒（不佔版面、不搶目前這格的視覺重量）。EquipmentScreen 不會有這種草稿驗證錯誤
   * （每次點擊都是真正送出的 PUT，成功/失敗直接反映在 equipped 上），故不傳、恆為 undefined。
   */
  errors?: Partial<Record<EquipmentSlot, string>>
  /**
   * 整個面板暫時唯讀（例如腳本編輯器存檔中）。EquipmentScreen 的每次點擊本身就是獨立、互不影響的
   * PUT 請求（見上面 busy 說明），從不需要整面板一起鎖住，故不傳、預設 false。
   */
  disabled?: boolean
}

export default function EquipmentPanel({ equipped, weaponTypes, weapons, armorItems, level, jobId, busy, onEquip, errors, disabled }: EquipmentPanelProps) {
  const [activeSlot, setActiveSlot] = useState<EquipmentSlot>('weapon')
  const [activeTypeId, setActiveTypeId] = useState<string | null>(weaponTypes[0]?.id ?? null)
  // 屬性版武器：每個類型各自記住目前選到哪個屬性 chip（含 'neutral' 代表「無屬性版」），
  // key 是 type_id。切分頁不清空，回到同一分頁時記得上次選的屬性。
  const [elementByType, setElementByType] = useState<Record<string, RpgElement>>({})

  // weaponTypes 可能因為呼叫端換了職業（Tavern 編輯器換傭兵）整包換掉——目前選到的分頁如果已經
  // 不在新清單裡，退回第一個；用 render 期間算而不是另開 effect，避免多一次重繪落差。
  const resolvedTypeId = activeTypeId && weaponTypes.some((t) => t.id === activeTypeId) ? activeTypeId : (weaponTypes[0]?.id ?? null)

  function handleEquip(itemId: string) {
    if (disabled) return
    onEquip(activeSlot, itemId)
  }
  function handleUnequip() {
    if (disabled) return
    onEquip(activeSlot, null)
  }

  const activeType: WeaponTypeDTO | null = weaponTypes.find((t) => t.id === resolvedTypeId) ?? null
  const activeElement: RpgElement = (resolvedTypeId && elementByType[resolvedTypeId]) || 'neutral'
  const listForActiveType = activeType
    ? weapons
        .filter((w) => w.type_id === activeType.id && (activeType.elemental_capable ? w.element === activeElement : true))
        .sort((a, b) => a.tier - b.tier)
    : []
  const isArmorSlot = ARMOR_EQUIP_SLOTS.includes(activeSlot)
  const isAccessorySlot = activeSlot === 'accessory1' || activeSlot === 'accessory2'
  const currentItem: WeaponDTO | ArmorDTO | null = (equipped as any)[activeSlot] ?? null
  const currentErr = errors?.[activeSlot] ?? null

  return (
    <div style={{ opacity: disabled ? 0.6 : 1 }}>
      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginBottom: 8 }}>依目前等級 Lv.{level} 判斷可裝備門檻</div>

      {/* ---- 裝備欄八格 ---- */}
      <SlotBar equipped={equipped} activeSlot={activeSlot} onSelect={disabled ? () => {} : setActiveSlot} errors={errors} />

      {/* ---- 目前這格卡片 ---- */}
      <CurrentSlotCard slot={activeSlot} item={currentItem} busy={busy === 'unequip' || !!disabled} onUnequip={handleUnequip} errorText={currentErr} />

      {/* ---- 武器格：三分頁＋屬性 chip ---- */}
      {activeSlot === 'weapon' && (
        <>
          {!jobId && <Hint>先選擇職業，才能查看與裝備武器</Hint>}

          {jobId && weaponTypes.length > 0 && (
            <>
              <div style={{ display: 'flex', gap: 6, marginTop: 20, borderBottom: '1px solid var(--line)', overflowX: 'auto' }}>
                {weaponTypes.map((wt) => (
                  <button
                    key={wt.id}
                    onClick={() => setActiveTypeId(wt.id)}
                    style={{
                      padding: '8px 14px', border: 'none', background: 'none', cursor: 'pointer', fontSize: 13.5,
                      color: wt.id === resolvedTypeId ? 'var(--tx)' : 'var(--tx-dim)',
                      borderBottom: wt.id === resolvedTypeId ? '2px solid var(--fug)' : '2px solid transparent',
                      fontWeight: wt.id === resolvedTypeId ? 700 : 400, flexShrink: 0, whiteSpace: 'nowrap', fontFamily: 'inherit',
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
                      <WeaponRow key={w.id} weapon={w} busy={busy === w.id || !!disabled} onEquip={() => handleEquip(w.id)} />
                    ))}
                  </div>
                </div>
              )}
            </>
          )}

          {jobId && weaponTypes.length === 0 && <Hint>此職業暫無可裝備的武器類型資料</Hint>}
        </>
      )}

      {/* ---- 防具格：該部位本職業 10 級 ---- */}
      {isArmorSlot && (
        !jobId ? (
          <Hint>先選擇職業，才能查看與裝備防具</Hint>
        ) : (
          <ArmorList slot={activeSlot} items={armorItems} busyId={disabled ? '*' : busy} onEquip={handleEquip} />
        )
      )}

      {/* ---- 飾品格：18 組×5 級 ---- */}
      {isAccessorySlot && (
        <AccessoryList activeSlot={activeSlot as 'accessory1' | 'accessory2'} items={armorItems} busyId={disabled ? '*' : busy} onEquip={handleEquip} />
      )}
    </div>
  )
}

// ============================== 子元件 ==============================

function SlotBar({ equipped, activeSlot, onSelect, errors }: {
  equipped: EquippedGearDTO
  activeSlot: EquipmentSlot
  onSelect: (s: EquipmentSlot) => void
  errors?: Partial<Record<EquipmentSlot, string>>
}) {
  return (
    <div style={{ display: 'flex', gap: 8, overflowX: 'auto', paddingBottom: 4, marginBottom: 14, WebkitOverflowScrolling: 'touch' }}>
      {EQUIP_SLOT_ORDER.map((s) => {
        const item = (equipped as any)[s] as WeaponDTO | ArmorDTO | null
        const on = s === activeSlot
        const hasErr = !!errors?.[s]
        return (
          <button key={s} onClick={() => onSelect(s)} style={{ ...slotChip, ...(on ? slotChipActive : {}), position: 'relative' }}>
            {hasErr && <span style={slotErrDot} />}
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

function CurrentSlotCard({ slot, item, busy, onUnequip, errorText }: {
  slot: EquipmentSlot; item: WeaponDTO | ArmorDTO | null; busy: boolean; onUnequip: () => void; errorText?: string | null
}) {
  if (!item) {
    return (
      <div style={currentCard}>
        <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginBottom: 2 }}>{EQUIP_SLOT_LABEL[slot]}</div>
        <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>尚未裝備</div>
        {errorText && <div style={fieldErr}>{errorText}</div>}
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
          {errorText && <div style={fieldErr}>{errorText}</div>}
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

/** 防具格清單（頭盔/手套/衣服/褲裙/鞋子）：該部位本職業 10 級，依 tier 排序。busyId='*'＝面板整體
 *  disabled（見 EquipmentPanel 呼叫處），此時全部按鈕視為忙碌，不會有任何品項的 id 剛好是 '*'。 */
function ArmorList({ slot, items, busyId, onEquip }: { slot: EquipmentSlot; items: ArmorDTO[]; busyId: string | null; onEquip: (id: string) => void }) {
  const list = items.filter((a) => a.slot === slot).sort((a, b) => a.tier - b.tier)
  if (list.length === 0) return <Hint>此部位暫無防具資料</Hint>
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 14 }}>
      {list.map((a) => <ArmorRow key={a.id} item={a} busy={busyId === a.id || busyId === '*'} onEquip={() => onEquip(a.id)} />)}
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
              <AccessoryRow key={t.id} item={t} activeSlot={activeSlot} busy={busyId === t.id || busyId === '*'} onEquip={() => onEquip(t.id)} />
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

const fieldErr: React.CSSProperties = { fontSize: 10.5, color: '#f4623a', marginTop: 4, lineHeight: 1.4 }
const currentCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--gold)', borderRadius: 14, padding: '12px 14px' }
const weaponRowStyle: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 12px' }
const dangerGhostBtn: React.CSSProperties = { background: 'none', border: '1px solid rgba(244,98,58,.4)', color: '#f4623a', borderRadius: 8, padding: '6px 12px', fontSize: 11.5, fontWeight: 700, fontFamily: 'inherit', cursor: 'pointer' }
const tagStyle: React.CSSProperties = { fontSize: 9.5, fontWeight: 700, borderRadius: 999, padding: '2px 7px', whiteSpace: 'nowrap' }
// 金底白字（全站通則）：裝備按鈕用金底。
const equipBtn: React.CSSProperties = { background: 'var(--gold)', color: '#fff', border: 'none', borderRadius: 8, padding: '6px 14px', fontSize: 12, fontWeight: 800, fontFamily: 'inherit' }
// 拆成 longhand 邊框屬性（不與 shorthand 混用同一屬性，避免不同 render 之間覆寫順序不保證的 React 警告，
// 沿用原本 EquipmentScreen 審查#6 的既有結論）。
const elementChip: React.CSSProperties = { background: 'var(--bg-1)', borderWidth: 1, borderStyle: 'solid', borderColor: 'var(--line)', color: 'var(--tx-dim)', borderRadius: 999, padding: '5px 12px', fontSize: 12, fontWeight: 700, fontFamily: 'inherit', cursor: 'pointer' }
// 金底白字（全站通則）：選中的屬性 chip 用金底，文字強制白色。
const elementChipActive: React.CSSProperties = { background: 'var(--gold)', borderColor: 'var(--gold)', color: '#fff' }
const slotChip: React.CSSProperties = {
  background: 'var(--bg-1)', borderWidth: 1, borderStyle: 'solid', borderColor: 'var(--line)', color: 'var(--tx-dim)',
  borderRadius: 10, padding: '6px 10px', minWidth: 68, textAlign: 'center', flexShrink: 0, cursor: 'pointer', fontFamily: 'inherit',
}
// 金底白字（全站通則）：選中的格子用金底，文字強制白色。
const slotChipActive: React.CSSProperties = { background: 'var(--gold)', borderColor: 'var(--gold)', color: '#fff' }
// DORPG P9：格子有驗證錯誤時的小紅點提示（不佔版面，只疊在格子右上角）。
const slotErrDot: React.CSSProperties = { position: 'absolute', top: 4, right: 4, width: 6, height: 6, borderRadius: '50%', background: '#f4623a' }
