'use client'

// 開發預覽的 client 外殼：BattleScreen 用 ResizeObserver／useLayoutEffect 量自己，只能在瀏覽器渲染，
// 故 ssr:false；position:fixed 鋪滿視窗，模擬 PhoneShell 內容區「填滿父層」的條件。
//
// P2（契約 §4／§7.4）：支援 ?code=<encounter> 直接用 fixture.ts 離線組裝進戰鬥（不打 API），也支援
// 沒帶 code 時先看一個「選單」（EncounterPicker 套 loadOverride 注入下面從 fixture.ts 內容表格衍生出的
// 遭遇清單）——讓 verify_p2.mjs 能在沒有後端/DB 的情況下走完整個「選單→戰鬥→結算→再戰一場→換一場→
// 返回選單」迴圈。DEV_ENCOUNTERS 直接由 fixture.ts 的 RPG_ENCOUNTERS/RPG_MONSTERS/RPG_SCENES 衍生
// （不手動抄一份），跟正式的 migration 176 seed 保證同一個資料來源、不會兩邊兜不起來。
import { useCallback, useEffect, useState } from 'react'
import dynamic from 'next/dynamic'
import { PALETTE } from '@/lib/dorpg/assets'
import type { RpgBattleEncounters } from '@/lib/api'
import type { BattleReportStats } from '@/components/dorpg/BattleScreen'
import { RPG_ENCOUNTERS, RPG_MONSTERS, RPG_SCENES, buildFixtureSample, buildRankDemoBundle, loadRefPlayerTable } from '@/lib/dorpg/fixture'
import type { FixtureBundle } from '@/lib/dorpg/fixture'
import { battleAudio } from '@/lib/dorpg/audio'

const BattleScreen = dynamic(() => import('@/components/dorpg/BattleScreen'), {
  ssr: false,
  loading: () => <div style={{ position: 'fixed', inset: 0, background: PALETTE.surfaceBase }} />,
})
const EncounterPicker = dynamic(() => import('@/components/dorpg/EncounterPicker'), { ssr: false })

function monsterBrief(id: string): { name: string; poster_url: string; is_boss: boolean } {
  const m = RPG_MONSTERS.find((mm) => mm.id === id)
  return m ? { name: m.name, poster_url: m.posterUrl, is_boss: m.isBoss } : { name: id, poster_url: '', is_boss: false }
}

/** 直接由 fixture.ts 的內容表格衍生遭遇卡片清單，不手動另抄一份（保證跟 buildFixtureSample 同源）。 */
const DEV_ENCOUNTERS_LEGACY: RpgBattleEncounters['encounters'] = RPG_ENCOUNTERS.map((enc) => ({
  code: enc.code,
  title: enc.title,
  subtitle: enc.subtitle,
  scene_id: enc.sceneId,
  scene_image_url: RPG_SCENES.find((s) => s.id === enc.sceneId)?.imageUrl ?? '',
  scene_kind: enc.sceneKind,
  difficulty: enc.difficulty,
  can_escape: enc.canEscape,
  monster_level: enc.monsterLevel,
  monsters: enc.monsters.map((em) => ({ slot: em.slot, ...monsterBrief(em.monsterId) })),
  stats: { plays: 0, wins: 0, best_ms: 0, last_outcome: '' },
}))

// 審查 PLAUSIBLE 補項：P11 rank 分級模式（buildRankDemoBundle）沒有離線示範入口，選單只看得到
// legacy 六場。這裡沒有另抄一份怪物/召喚資料，卡片文字寫死、實際內容仍在點下去後由
// buildRankDemoBundle() 現算（見下方 bundle 載入邏輯），code 跟該函式回傳的 encounter.code
// 逐字一致才能讓 ?code=rank_demo_f_x1_s_x1 深連結對上同一場。
const RANK_DEMO_CODE = 'rank_demo_f_x1_s_x1'
const RANK_DEMO_ENCOUNTER: RpgBattleEncounters['encounters'][number] = {
  code: RANK_DEMO_CODE,
  title: '強度挑戰示範（S 級召喚）',
  subtitle: '離線 fixture 示範，非正式 20 場強度挑戰列表的一部分',
  scene_id: 'scene_taipei_101',
  scene_image_url: RPG_SCENES.find((s) => s.id === 'scene_taipei_101')?.imageUrl ?? '',
  scene_kind: 'boss',
  difficulty: 9,
  can_escape: false,
  monster_level: 30,
  monsters: [
    { slot: 'front_left', ...monsterBrief('DOR-MON-E-0052') },
    { slot: 'front_right', ...monsterBrief('DOR-MON-A-67000200001') },
  ],
  stats: { plays: 0, wins: 0, best_ms: 0, last_outcome: '' },
}
const DEV_ENCOUNTERS: RpgBattleEncounters['encounters'] = [...DEV_ENCOUNTERS_LEGACY, RANK_DEMO_ENCOUNTER]

/** 擁有者現況的縮影（34 點未配，見契約 §1 D1）：只為了讓 /dev 能看到並點擊「未配點提醒」。 */
const DEV_CHARACTER: RpgBattleEncounters['character'] = {
  base_level: 12, free_points: 34, power: 118, max_hp: 300, max_mp: 100, unspent_hint: true,
}

async function loadDevEncounters(): Promise<RpgBattleEncounters> {
  return { encounters: DEV_ENCOUNTERS, character: DEV_CHARACTER }
}

type View = { mode: 'picker' } | { mode: 'battle'; code: string; nonce: number }

export default function Preview() {
  const [view, setView] = useState<View>({ mode: 'picker' })

  // ?code= 深連結：放進 effect（而非 useState 初始化器）避免 SSR 階段讀不到 window 造成的 hydration
  // 不一致——這頁本來就只在瀏覽器跑得動（BattleScreen 自己量尺寸），晚一個 tick 切換沒有體驗代價。
  useEffect(() => {
    const code = new URLSearchParams(window.location.search).get('code')
    if (code) setView({ mode: 'battle', code, nonce: 0 })
  }, [])

  // 2026-09-14 SCREENS 接線：這個頁面本身就是整個戰鬥流程（picker↔battle 沒有頁內的「流程外」狀態），
  // 「離開整個戰鬥流程」對應到離開這個頁面（元件卸載）——BGM 要跨 picker/battle/再戰一場連續播放，
  // 只在卸載時才停，對齊 PhoneShell 那層「battleView 變回 null 才 stopBgm」的同一個決策。
  useEffect(() => {
    return () => {
      battleAudio.stopBgm()
    }
  }, [])

  const handleReport = useCallback((stats: BattleReportStats) => {
    // /dev 沒有後端可打，直接印出來讓 verify 腳本／人眼核對統計數字（契約 §4：上層打 API 失敗只
    // console.warn，這裡連打都沒打，用 console.info 標明是預覽環境的預期行為，不是漏接）。
    console.info('[dorpg-dev] report', stats)
  }, [])

  // buildFixtureSample 對未知 code 會 throw（見 fixture.ts）——理論上不會發生（picker 給的 code 一定
  // 來自 DEV_ENCOUNTERS＝RPG_ENCOUNTERS 本身），但 ?code= 是使用者手打的網址，還是要接住。
  // RANK_DEMO_CODE 這場走 buildRankDemoBundle（P11 rank 分級模式示範），需要先 await
  // loadRefPlayerTable()，所以改用 useEffect+useState 而不是同步 useMemo；legacy 六場維持原本
  // buildFixtureSample 同步路徑不受影響。
  const [bundle, setBundle] = useState<FixtureBundle | ReturnType<typeof buildRankDemoBundle> | null>(null)
  useEffect(() => {
    if (view.mode !== 'battle') {
      setBundle(null)
      return
    }
    let cancelled = false
    ;(async () => {
      try {
        if (view.code === RANK_DEMO_CODE) {
          const refTable = await loadRefPlayerTable()
          if (!refTable) throw new Error('refPlayerTable.json 尚未就緒，無法組出 rank 示範')
          if (!cancelled) setBundle(buildRankDemoBundle({ refTable, level: 30 }))
          return
        }
        const b = buildFixtureSample(view.code)
        if (!cancelled) setBundle(b)
      } catch (e) {
        console.error('[dorpg-dev] build battle bundle failed', e)
        if (!cancelled) setBundle(null)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [view])

  if (view.mode === 'picker' || !bundle) {
    return (
      <div style={{ position: 'fixed', inset: 0, background: PALETTE.surfaceBase }}>
        <EncounterPicker
          onBack={() => {}}
          onOpenCharacter={() => {}}
          onPick={(code) => setView({ mode: 'battle', code, nonce: 0 })}
          loadOverride={loadDevEncounters}
        />
      </div>
    )
  }

  return (
    <div style={{ position: 'fixed', inset: 0, background: PALETTE.surfaceBase }}>
      {/* debugAllowed=true：這頁本來就只在 DORPG_DEV=1 才存在（見 app/dev/dorpg/page.tsx 的 notFound()
          守門），是唯一允許 ?dorpgDebug=1 生效的地方——2026-09-14 審查修正：正式戰鬥入口不會傳這個
          prop，就算網址加了 query 也不會有任何效果。key={view.nonce}：「再戰一場」靠這個強制重建
          BattleScreen（契約 §4：不重整、不重打任何東西；view.code 不變時上面的 bundle 載入
          effect 仍會因 view 物件參照改變而重算一次，但 buildFixtureSample/buildRankDemoBundle
          本身成本都可忽略，不值得為此另外加防抖）。 */}
      <BattleScreen
        key={view.nonce}
        sample={bundle.sample}
        encounter={{ code: view.code, title: bundle.encounter.title }}
        onBack={() => setView({ mode: 'picker' })}
        onRestart={() => setView((v) => (v.mode === 'battle' ? { ...v, nonce: v.nonce + 1 } : v))}
        onNext={() => setView({ mode: 'picker' })}
        onReport={handleReport}
        // rank 示範 bundle 才有這四欄（見 buildRankDemoBundle）；legacy 六場的 bundle 沒有這些
        // 欄位，'in' 窄化型別後保持 undefined，BattleScreen 對應 prop 本來就是可選。
        summonPool={'summonPool' in bundle ? bundle.summonPool : undefined}
        scalingMode={'scalingMode' in bundle ? bundle.scalingMode : undefined}
        levelMode={'levelMode' in bundle ? bundle.levelMode : undefined}
        monsterLevel={'monsterLevel' in bundle ? bundle.monsterLevel : undefined}
        debugAllowed
      />
    </div>
  )
}
