'use client'

// DORPG P7/P8（裝備，見 scratchpad/dorpg_p8/CONTRACT.md §4/§5、WIRE.md §REST）：沿用
// CharacterScreen 的元件與樣式語言（同一批 var(--bg)/var(--tx)/var(--gold) CSS 變數、同一種
// header/section/卡片排版），但比照 TavernScreen 的既有慣例——各畫面各自獨立成檔，不互相 import
// 對方的子元件或樣式常數，欄位也不共用（這裡讀/寫 GET /rpg/equipment、PUT /rpg/equipment/weapon、
// PUT /rpg/equipment/{防具格}）。
//
// DORPG P9（見 scratchpad/dorpg_p9/CONTRACT.md §5）：畫面本體（裝備欄八格／目前這格卡片／依格子
// 類型分岔的清單）已抽成共用元件 EquipmentPanel（見該檔案），讓 TavernScreen 腳本編輯器的「裝備」
// 區可以重用同一套 UI。本檔現在只負責：載入/儲存資料（GET /rpg/equipment、PUT .../weapon、
// PUT .../{防具格}）、頁面殼（header／載入中／錯誤/成功 banner）、把 equip/unequip 結果轉成
// EquipmentPanel 要的 props——行為與 P8 版本完全不變，純粹是把畫面主體搬進共用元件。
import { useCallback, useEffect, useState } from 'react'
import { rpgEquipmentApi, type EquipmentSlot, type RpgEquipmentResponse } from '@/lib/api'
import { getUserToken, withUserAuth } from '@/lib/userAuth'
import { EQUIP_SLOT_LABEL } from '@/lib/rpgMeta'
import EquipmentPanel from './EquipmentPanel'

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
        setLoadErr('')
      })
      .catch((e: any) => setLoadErr(e?.status === 403 ? '此功能尚未開放給你的帳號' : e?.message || '載入失敗'))
      .finally(() => setLoading(false))
  }, [])
  useEffect(() => { load() }, [load])

  async function handleEquip(slot: EquipmentSlot, itemId: string | null) {
    if (busyId) return
    setBusyId(itemId ?? 'unequip')
    try {
      const next = slot === 'weapon'
        ? await withUserAuth((t) => rpgEquipmentApi.setWeapon(t, itemId))
        : await withUserAuth((t) => rpgEquipmentApi.setSlot(t, slot, itemId))
      setData(next)
      if (itemId) {
        const name = slot === 'weapon'
          ? next.weapons.find((w) => w.id === itemId)?.name
          : next.armor_items.find((a) => a.id === itemId)?.name
        showFlash(`已裝備「${name ?? itemId}」`)
      } else {
        showFlash(`已卸下${EQUIP_SLOT_LABEL[slot]}`)
      }
    } catch (e: any) {
      showErr(friendlyErr(e, EQUIP_ERR_LABEL, itemId ? '裝備失敗，請稍後再試' : '卸下失敗，請稍後再試'))
    } finally {
      setBusyId(null)
    }
  }

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

            <EquipmentPanel
              equipped={data.equipped}
              weaponTypes={data.weapon_types}
              weapons={data.weapons}
              armorItems={data.armor_items}
              level={data.effective_level}
              jobId={data.job?.id ?? null}
              busy={busyId}
              onEquip={handleEquip}
            />
          </>
        )}
      </div>
    </div>
  )
}

function Hint({ children }: { children: React.ReactNode }) {
  return <div style={{ textAlign: 'center', color: 'var(--tx-dim)', fontSize: 13, padding: '18px 0' }}>{children}</div>
}

// 金底白字（全站通則）
const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', fontSize: 13, padding: 0, cursor: 'pointer', fontFamily: 'inherit' }
const errBanner: React.CSSProperties = { background: 'rgba(244,98,58,.12)', color: '#f4623a', border: '1px solid rgba(244,98,58,.3)', borderRadius: 10, padding: '8px 12px', fontSize: 12.5, marginBottom: 10 }
const okBanner: React.CSSProperties = { background: 'rgba(45,212,150,.12)', color: 'var(--fug)', border: '1px solid rgba(45,212,150,.3)', borderRadius: 10, padding: '8px 12px', fontSize: 12.5, marginBottom: 10 }
