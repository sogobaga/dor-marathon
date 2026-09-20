'use client'

// DORPG 戰場：場景背景＋怪物精靈＋敵人 Lv/HP 小面板＋目標標記（光環、箭頭）＋攻擊特效層。
// P1：改吃引擎狀態（EnemyActor 形狀的簡化投影，見 StageEnemy）——怪物動作直接由 anim 欄位驅動，
// 不另外維護一份「顯示用」動畫狀態，避免兩份狀態機不同步。
// 所有尺寸都是「邏輯 px」：父層決定 width/height（W390 基準、480 封頂），這裡以 width/390 等比縮放固定尺寸，
// 刻意不用 container query——規格要父層掌握寬度，元件自行量測會在 SSR 水合時閃一下。
import { Fragment, forwardRef, useImperativeHandle, useRef, type CSSProperties } from 'react'
import type { EnemySlotId, Scene, SceneSlot } from '@/lib/dorpg/types'
import {
  ENEMY_PLATE,
  KIT_SIZES,
  LAYOUT,
  MONSTER_ANCHOR,
  PALETTE,
  SCENE_VIEWPORT,
  barClipPath,
  fracStyle,
  kitAsset,
} from '@/lib/dorpg/assets'
import MonsterSprite from './MonsterSprite'
import type { SpriteActionKind } from '@/lib/dorpg/useSpritePlayer'
import CombatFxLayer from './CombatFxLayer'
import type { CombatFxLayerHandle, CombatFxPlayOptions } from './CombatFxLayer'
import s from './BattleStage.module.css'
import p from './EnemyPlate.module.css'

// 圖層（規格 §3 z-index 表；戰場 .stage 自成 stacking context）：
// z20 光環（腳底）→ z30+ 怪物與其小面板（後排在下、同排依腳點 y 由小到大往上疊；每隻怪佔兩層：
// 怪物 30+2i、自己的面板 31+2i）→ z50 攻擊特效（疊在所有怪物/面板之上，斬擊/傷害數字才不會被怪物擋住）
// → z60 目標箭頭（頭頂，永遠看得到）。
// 2026-09-14 使用者定案：前排怪物要「遮住」後排怪的血量面板才有前後排的縱深感——所以面板不再是所有敵人
// 共用的最上層平面，而是緊貼在自己怪物之上、但在更前排的怪物之下。箭頭仍在最上層（在頭頂、不會被擋）。
const Z = { ring: 20, monsterBase: 30, fx: 50, chevron: 60 } as const
const zMonster = (idx: number) => Z.monsterBase + idx * 2
const zPlate = (idx: number) => Z.monsterBase + idx * 2 + 1

/** 目標箭頭顯示寬（邏輯 px）：manifest 給 40×36，但 kit 與 content pack 兩份預覽都縮到 ~24×22 才不會壓過怪物頭部；高依 40:36 等比。 */
const CHEVRON_W = 24
/** 箭頭底邊落在怪物方形畫布頂端往下 20% 處（抄 content pack preview `.mark{bottom:80%}`）：512 畫布上緣通常留白，直接貼畫布頂會飄太高。 */
const CHEVRON_HEAD_RATIO = 0.2
/** 光環寬 = 怪物顯示寬 × 0.8（content pack preview 用 left/right 20%），再以 manifest 150 邏輯寬封頂；高依 150:36 等比。 */
const RING_WIDTH_RATIO = 0.8
/** 小面板頂邊比腳點高 5 邏輯 px（content pack preview `translate(-50%,-5px)`）：面板中心落在腳點略下方，前排（y=0.89）在 244 高場景內不被裁掉。 */
const PLATE_LIFT = 5

/** P1：怪物動畫狀態（對齊 engine/types.ts 的 EnemyAnimState 字面量，但刻意不 import engine 型別——
 *  BattleStage 只需要知道「這幾種字串」，用結構相容的方式接住 BattleScreen 傳來的資料，不建立對 engine
 *  內部型別的硬依賴，換引擎實作只要字串值不變就不必動這個檔案）。 */
export type StageEnemyAnim = 'spawning' | 'idle' | 'windup' | 'attacking' | 'hitReaction' | 'dying' | 'removed'

/** BattleStage 需要的敵人資料最小集合：由 BattleScreen 把 engine 的 EnemyActor 投影成這個形狀。 */
export type StageEnemy = {
  id: string
  name: string
  level: number
  hp: number
  hpMax: number
  slot: EnemySlotId
  imageUrl: string
  anim: StageEnemyAnim
  rank?: string
  attribute?: string
  size?: string
  race?: string
  /**
   * DORPG P11（WIRE §引擎：「enemy 新增 rankLabel/badgeColor/isSummoned」）：怪物強度徽章的中文
   * 標籤與底色（`rank` 本身已是既有欄位，這裡補顯示用的兩個新欄位）——三者缺任一個時 EnemyPlate
   * 直接不畫徽章（見該元件），不強行拼湊，維持既有無分級戰鬥的畫面零改動。
   */
  rankLabel?: string
  badgeColor?: string
  /** 這隻怪是不是戰鬥中途召喚出來的（契約 §1：特A/S/特S 會召喚 A～F 級怪物）；EnemyPlate 疊一個
   *  小字「召喚」區分，不影響戰鬥判斷，純顯示。 */
  isSummoned?: boolean
}

export type BattleStageHandle = {
  /** 排一次攻擊特效（見 CombatFxLayer）；回傳的 Promise 於 impact 時 resolve。 */
  play(opts: CombatFxPlayOptions): Promise<void>
  /** 清空所有進行中的特效（戰鬥結束／畫面卸載時呼叫）。 */
  cancelAll(): void
  /** 契約 §7：取得怪物「軀幹中心」座標（腳點 − dw×0.45）供特效/傷害數字定位；查無該敵人回 null。 */
  getEnemyAnchor(enemyId: string): { x: number; y: number } | null
}

export type BattleStageProps = {
  scene: Scene
  enemies: StageEnemy[]
  /** 目前鎖定的敵人 id；null = 沒有目標（不畫光環／箭頭）。 */
  targetId: string | null
  onSelect: (id: string) => void
  /** P1：點擊戰場空白處（非怪物）——用於「再點戰場空白 → CANCEL_TARGETING」。 */
  onBackgroundClick?: () => void
  /** 戰場邏輯寬（px，例 390）；所有固定尺寸都以 width/390 等比縮放。 */
  width: number
  /** 戰場高（px，例 244）：由父層依版面帶高算出（sceneHeightFor）。 */
  height: number
  /** 減少動態：轉給每隻 MonsterSprite，只顯示各動作第一格。 */
  reducedMotion?: boolean
}

/** 後排先畫（在下），前排後畫（在上）。 */
function rowRank(slot: SceneSlot): number {
  return slot.row === 'front' ? 1 : 0
}

/** 怪物 sprite 動作對應（契約 §7）：windup/attacking→attack、hitReaction→hit、dying→death，其餘 idle。
 *  'removed' 不會走到這裡（呼叫端在 placed 階段就濾掉），保留在型別裡只是因為它是 StageEnemyAnim 的成員。 */
function spriteActionForAnim(anim: StageEnemyAnim): SpriteActionKind {
  switch (anim) {
    case 'windup':
    case 'attacking':
      return 'attack'
    case 'hitReaction':
      return 'hit'
    case 'dying':
      return 'death'
    default:
      return 'idle'
  }
}

/**
 * content pack 擺放公式：怪物是 displayWidth 的正方形，左上角 = (腳點x − dw×0.5, 腳點y − dw×0.87890625)，
 * 讓畫布 (256,450) 的腳點落在站位上。抽成獨立函式讓 render 迴圈與 computeEnemyFxAnchor（給 BattleScreen
 * 算特效座標用）共用同一份幾何邏輯，不必抄兩次。
 */
function computeEnemyPlacement(slot: SceneSlot, width: number, height: number) {
  const k = width / LAYOUT.designWidth
  const dw = slot.scale * width
  const footX = slot.x * width
  // 站位 y 不是直接乘整個場景高：scene.json 的槽位是照 390×244 設計盒排的，而標準版面的場景高
  // 會長到 430（H−414），怪物又只隨寬度縮放——若 y 乘全高，前後排會被拉開、前排怪碰不到後排怪的
  // 小面板，就沒有前後排的縱深（2026-09-14 使用者定案：前排怪要遮住後排怪的血量面板）。
  // 做法：把 244×(width/390) 高的「站位帶」貼齊場景底部，多出來的高度全變成上方天空（背景 cover 本就會
  // 把畫面往上延伸）；場景比站位帶矮時（緊湊/短屏）退回整高壓縮，和以前一樣。
  const bandH = Math.min(height, SCENE_VIEWPORT.h * k)
  const footY = height - (1 - slot.y) * bandH
  const left = footX - dw * MONSTER_ANCHOR.x
  const top = footY - dw * MONSTER_ANCHOR.y
  return { dw, footX, footY, left, top }
}

/** 特效座標＝怪物軀幹中心：腳點往上 dw×0.45（比腳底高，落在軀幹中段而非腳跟）。契約 §7 原文用詞。 */
function computeEnemyFxAnchor(slot: SceneSlot, width: number, height: number): { x: number; y: number } {
  const { footX, footY, dw } = computeEnemyPlacement(slot, width, height)
  return { x: footX, y: footY - dw * 0.45 }
}

/**
 * ASSETS 的 monsterAnim.ts / cdn.ts 用「怪物圖鑑 id」（如 DOR-MON-A-67000200001）當 key，但契約給
 * BattleStage 的 Enemy/EnemyActor 只有「本場戰鬥的實例 id」（如 enemy_1，同一隻怪可能多次出場）——
 * 兩者不是同一個 id，且契約的 Enemy/EnemyActor 型別（ENGINE/ASSETS 兩位第一輪工作者已定案、非本輪
 * ASSEMBLE 可寫範圍）沒有另外開一個「圖鑑 id」欄位可查。sampleBattle.ts 的 imageUrl 是用
 * `monsterPoster(圖鑑id)` 組出來的（/ui/dorpg/mon/<圖鑑id>.webp），等於已經把圖鑑 id 編碼進 URL 裡，
 * 這裡反解回來，避免為了這一個欄位去動不屬於本輪 ASSEMBLE 可寫清單的 types.ts/sampleBattle.ts。
 * 解不出來（正式資料改了 imageUrl 命名規則）時原樣傳回整段 URL 當 key：MonsterSprite 內部找不到
 * 對應的 MONSTER_ANIMS 會自動退回 FALLBACK_ANIMS＋poster，不會炸畫面，只是動畫播不出來。
 */
function catalogIdFromPosterUrl(url: string): string {
  const m = /\/mon\/([^/]+)\.webp(?:[?#].*)?$/.exec(url)
  return m ? m[1] : url
}

const BattleStage = forwardRef<BattleStageHandle, BattleStageProps>(function BattleStage(
  { scene, enemies, targetId, onSelect, onBackgroundClick, width, height, reducedMotion },
  ref,
) {
  const fxRef = useRef<CombatFxLayerHandle>(null)
  // getEnemyAnchor 用 ref 存最新的幾何相關 props：useImperativeHandle 的 deps 是 []（identity 穩定，
  // BattleScreen 不必因為拿到新的 ref 物件就重新處理），實際資料一律從這份 ref 讀最新值。
  const geomRef = useRef({ scene, enemies, width, height })
  geomRef.current = { scene, enemies, width, height }

  useImperativeHandle(
    ref,
    () => ({
      play: (opts) => fxRef.current?.play(opts) ?? Promise.resolve(),
      cancelAll: () => fxRef.current?.cancelAll(),
      getEnemyAnchor: (enemyId) => {
        const g = geomRef.current
        const enemy = g.enemies.find((e) => e.id === enemyId)
        if (!enemy) return null
        const slot = g.scene.slots.find((sl) => sl.id === enemy.slot)
        if (!slot) return null
        return computeEnemyFxAnchor(slot, g.width, g.height)
      },
    }),
    [],
  )

  const k = width / LAYOUT.designWidth
  const slotById = new Map<EnemySlotId, SceneSlot>(scene.slots.map((sl) => [sl.id, sl]))

  // 找不到站位、或已經播完死亡動畫（anim==='removed'）的敵人直接不畫：
  // 契約允許「不渲染」或「淡出後不渲染」兩種做法之一，這裡選前者（較單純，死亡動畫本身已經有
  // enemyDeathMs 的播放時間讓玩家看到倒下，removed 之後留在畫面上沒有additional 資訊價值）。
  const placed: { enemy: StageEnemy; slot: SceneSlot }[] = []
  for (const enemy of enemies) {
    if (enemy.anim === 'removed') continue
    const slot = slotById.get(enemy.slot)
    if (slot) placed.push({ enemy, slot })
  }
  placed.sort((a, b) => rowRank(a.slot) - rowRank(b.slot) || a.slot.y - b.slot.y)

  const ringAspect = KIT_SIZES.target_ground_ring.h / KIT_SIZES.target_ground_ring.w
  const chevAspect = KIT_SIZES.target_chevrons.h / KIT_SIZES.target_chevrons.w

  return (
    <div
      className={s.stage}
      role="group"
      aria-label={`戰場：${scene.name}`}
      style={{ width, height, backgroundColor: PALETTE.surfaceBase, backgroundImage: `url("${scene.imageUrl}")` }}
      onClick={(e) => {
        // 只有直接點在戰場背景（沒有經過任何子層按鈕冒泡）才算「點空白」；怪物按鈕自己的 onClick
        // 沒有 stopPropagation，但 e.target 會是那顆 button 而不是這個 div，用這個差異區分兩種點擊。
        if (e.target === e.currentTarget) onBackgroundClick?.()
      }}
    >
      {placed.map(({ enemy, slot }, idx) => {
        const { dw, footX, footY, left, top } = computeEnemyPlacement(slot, width, height)
        const selected = enemy.id === targetId

        const plateW = ENEMY_PLATE.w * k
        const ringW = Math.min(KIT_SIZES.target_ground_ring.w * k, dw * RING_WIDTH_RATIO)
        const ringH = ringW * ringAspect
        const chevW = CHEVRON_W * k
        const chevH = chevW * chevAspect

        return (
          <Fragment key={enemy.id}>
            {selected && (
              <img
                className={s.ring}
                src={kitAsset('target_ground_ring')}
                alt=""
                draggable={false}
                style={{
                  left: Math.round(footX - ringW / 2),
                  top: Math.round(footY - ringH / 2),
                  width: Math.round(ringW),
                  height: Math.round(ringH),
                  zIndex: Z.ring,
                }}
              />
            )}
            <button
              type="button"
              className={s.monster}
              aria-label={`選擇 ${enemy.name}，等級 ${Math.floor(enemy.level)}，HP ${Math.floor(enemy.hp)}／${Math.floor(enemy.hpMax)}${enemy.rankLabel ? `，強度 ${enemy.rankLabel}` : ''}${enemy.isSummoned ? '（召喚）' : ''}`}
              aria-pressed={selected}
              onClick={() => onSelect(enemy.id)}
              style={{ left: Math.round(left), top: Math.round(top), width: Math.round(dw), height: Math.round(dw), zIndex: zMonster(idx) }}
            >
              <MonsterSprite
                monsterId={catalogIdFromPosterUrl(enemy.imageUrl)}
                action={spriteActionForAnim(enemy.anim)}
                size={Math.round(dw)}
                posterUrl={enemy.imageUrl}
                reducedMotion={reducedMotion}
              />
            </button>
            <EnemyPlate
              level={enemy.level}
              hp={enemy.hp}
              hpMax={enemy.hpMax}
              rank={enemy.rank}
              rankLabel={enemy.rankLabel}
              badgeColor={enemy.badgeColor}
              isSummoned={enemy.isSummoned}
              width={plateW}
              className={s.plate}
              // position 用 inline 寫死：EnemyPlate.module.css 的 .plate{position:relative} 與本檔 .plate{position:absolute}
              // 同權重、誰後載入誰贏——曾讓面板變成 in-flow 元素逐個往下堆，前排三張面板被推出場景底部被裁掉（2026-09-13）。
              style={{ position: 'absolute', left: Math.round(footX - plateW / 2), top: Math.round(footY - PLATE_LIFT * k), zIndex: zPlate(idx) }}
            />
            {selected && (
              <img
                className={s.chevron}
                src={kitAsset('target_chevrons')}
                alt=""
                draggable={false}
                style={{
                  left: Math.round(footX - chevW / 2),
                  // 寬屏（480）時 scale×width 讓後排大怪頭頂衝出 244 高的場景，箭頭至少留在場景內 2px 處才看得到
                  top: Math.round(Math.max(2 * k, top + dw * CHEVRON_HEAD_RATIO - chevH)),
                  width: Math.round(chevW),
                  height: Math.round(chevH),
                  zIndex: Z.chevron,
                }}
              />
            )}
          </Fragment>
        )
      })}

      {/* 攻擊特效層：疊在所有怪物/面板之上（Z.fx=50），斬擊/暴擊字樣/傷害數字才不會被怪物擋住。
          CombatFxLayerProps 沒有開放 style/zIndex，用一個純定位 wrapper 包住即可，不必為此改 AUDIO_FX 的檔案。 */}
      <div className={s.fxLayer} style={{ zIndex: Z.fx }}>
        <CombatFxLayer ref={fxRef} width={width} height={height} />
      </div>
    </div>
  )
})

BattleStage.displayName = 'BattleStage'
export default BattleStage

// ---------------------------------------------------------------------------
// EnemyPlate：怪物 Lv／HP 小面板（panel_enemy_empty 邏輯 82×25）。
// 「Lv.」已烤在底圖，只疊等級數字（Georgia 12px/700，靠左緊接 Lv.）與 bar_fill_enemy_red 的 clip-path 填色。
// 純裝飾（aria-hidden）：HP／等級已在怪物按鈕的 aria-label 朗讀。
// ---------------------------------------------------------------------------
export type EnemyPlateProps = {
  level: number
  hp: number
  hpMax: number
  /** 面板邏輯寬（預設 82）；高固定 82:25 等比，字級隨寬縮放。 */
  width?: number
  className?: string
  /** 由父層決定定位（position/left/top/zIndex）；元件本身只是 relative 盒。 */
  style?: CSSProperties
  /**
   * DORPG P11（契約 §4：「敵人名旁徽章」；WIRE §引擎 enemy.rank/rankLabel/badgeColor）：怪物強度——
   * 三者缺任一個（既有無分級戰鬥／舊版後端）就不畫徽章，維持原本面貌零改動。`rank`（如 'S'）顯示
   * 在徽章本體，`rankLabel`（如 'S級'）只當 title/aria 說明用（面板本身是 aria-hidden，靠呼叫端
   * BattleStage.tsx 把它併進怪物按鈕的 aria-label）。
   */
  rank?: string
  rankLabel?: string
  badgeColor?: string
  /** 這隻怪是不是召喚出來的（契約 §1）；true 時在面板另一角疊一個「召喚」小字。 */
  isSummoned?: boolean
}

export function EnemyPlate({ level, hp, hpMax, rank, rankLabel, badgeColor, isSummoned, width = ENEMY_PLATE.w, className, style }: EnemyPlateProps) {
  const pk = width / ENEMY_PLATE.w
  const h = ENEMY_PLATE.h * pk
  const levelBox = fracStyle(ENEMY_PLATE.level)
  // DORPG P6（契約 §1：HP 務必整數；§2：怪物等級制 Lv.N 顯示）：Math.floor 是顯示層最後一道保險，
  // 同 PartyCard.tsx 的處理方式——引擎/bootstrap 理論上已整數化，這裡不信任上游、自己再保一次。
  const levelI = Math.floor(level)
  const hpI = Math.floor(hp)
  const hpMaxI = Math.floor(hpMax)
  return (
    <div
      className={className ? `${p.plate} ${className}` : p.plate}
      aria-hidden="true"
      style={{ width, height: h, color: PALETTE.textPrimary, ...style }}
    >
      <img className={p.base} src={kitAsset('panel_enemy_empty')} alt="" draggable={false} />
      <span
        className={p.level}
        style={{
          left: levelBox.left,
          top: levelBox.top,
          width: levelBox.width,
          fontSize: 12 * pk,
          // 行高＝插槽高，讓數字垂直落在插槽內；字級略大於插槽是刻意的（同 kit 預覽），不裁切
          lineHeight: `${ENEMY_PLATE.level.h * h}px`,
        }}
      >
        {levelI}
      </span>
      <span className={p.hp} style={fracStyle(ENEMY_PLATE.hpFill)}>
        <img src={kitAsset('bar_fill_enemy_red')} alt="" draggable={false} style={{ clipPath: barClipPath(hpI, hpMaxI) }} />
      </span>
      {/* DORPG P11：強度徽章——貼在面板左上角外緣（純 inline style，不新增 CSS class；.plate 本身
          position:relative 且沒有 overflow:hidden，見 EnemyPlate.module.css，微幅溢出不會被裁掉）。 */}
      {rank && badgeColor && (
        <span
          title={rankLabel || rank}
          style={{
            position: 'absolute',
            left: -2 * pk,
            top: -7 * pk,
            minWidth: 14 * pk,
            padding: `0 ${2 * pk}px`,
            height: 12 * pk,
            lineHeight: `${12 * pk}px`,
            borderRadius: 999,
            background: badgeColor,
            color: '#fff',
            fontSize: 8 * pk,
            fontWeight: 800,
            textAlign: 'center',
            boxShadow: '0 1px 2px rgba(0,0,0,.6)',
          }}
        >
          {rank}
        </span>
      )}
      {isSummoned && (
        <span
          style={{
            position: 'absolute',
            right: -2 * pk,
            top: -7 * pk,
            padding: `0 ${3 * pk}px`,
            height: 12 * pk,
            lineHeight: `${12 * pk}px`,
            borderRadius: 3,
            background: 'rgba(0,0,0,.65)',
            color: '#fff',
            fontSize: 8 * pk,
            fontWeight: 700,
            whiteSpace: 'nowrap',
          }}
        >
          召喚
        </span>
      )}
    </div>
  )
}
