'use client'

// DORPG 戰場：場景背景＋怪物＋敵人 Lv/HP 小面板＋目標標記（光環、箭頭）。
// P0 純靜態：只吃 props、只有父層傳進來的選取狀態，不接 API／DB。
// 所有尺寸都是「邏輯 px」：父層決定 width/height（W390 基準、480 封頂），這裡以 width/390 等比縮放固定尺寸，
// 刻意不用 container query——規格要父層掌握寬度，元件自行量測會在 SSR 水合時閃一下。
import { Fragment, type CSSProperties } from 'react'
import type { Enemy, EnemySlotId, Scene, SceneSlot } from '@/lib/dorpg/types'
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
import s from './BattleStage.module.css'
import p from './EnemyPlate.module.css'

// 圖層（規格 §3 z-index 表；戰場 .stage 自成 stacking context）：
// z20 光環（腳底）→ z30+ 怪物與其小面板（後排在下、同排依腳點 y 由小到大往上疊；每隻怪佔兩層：
// 怪物 30+2i、自己的面板 31+2i）→ z60 目標箭頭。
// 2026-09-14 使用者定案：前排怪物要「遮住」後排怪的血量面板才有前後排的縱深感——所以面板不再是所有敵人
// 共用的最上層平面，而是緊貼在自己怪物之上、但在更前排的怪物之下。箭頭仍在最上層（在頭頂、不會被擋）。
const Z = { ring: 20, monsterBase: 30, chevron: 60 } as const
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

export type BattleStageProps = {
  scene: Scene
  enemies: Enemy[]
  /** 目前鎖定的敵人 id；null = 沒有目標（不畫光環／箭頭）。 */
  targetId: string | null
  onSelect: (id: string) => void
  /** 戰場邏輯寬（px，例 390）；所有固定尺寸都以 width/390 等比縮放。 */
  width: number
  /** 戰場高（px，例 244）：由父層依版面帶高算出（sceneHeightFor）。 */
  height: number
}

/** 後排先畫（在下），前排後畫（在上）。 */
function rowRank(slot: SceneSlot): number {
  return slot.row === 'front' ? 1 : 0
}

export default function BattleStage({ scene, enemies, targetId, onSelect, width, height }: BattleStageProps) {
  const k = width / LAYOUT.designWidth
  const slotById = new Map<EnemySlotId, SceneSlot>(scene.slots.map((sl) => [sl.id, sl]))

  // 找不到站位的敵人直接不畫：資料錯誤不該讓整個戰場崩掉。
  const placed: { enemy: Enemy; slot: SceneSlot }[] = []
  for (const enemy of enemies) {
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
    >
      {placed.map(({ enemy, slot }, idx) => {
        // content pack 擺放公式：怪物是 displayWidth 的正方形，
        // 左上角 = (腳點x − dw×0.5, 腳點y − dw×0.87890625)，讓畫布 (256,450) 的腳點落在站位上。
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
              aria-label={`選擇 ${enemy.name}，等級 ${enemy.level}，HP ${enemy.hp}／${enemy.hpMax}`}
              aria-pressed={selected}
              onClick={() => onSelect(enemy.id)}
              style={{ left: Math.round(left), top: Math.round(top), width: Math.round(dw), height: Math.round(dw), zIndex: zMonster(idx) }}
            >
              <img src={enemy.imageUrl} alt="" draggable={false} />
            </button>
            <EnemyPlate
              level={enemy.level}
              hp={enemy.hp}
              hpMax={enemy.hpMax}
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
    </div>
  )
}

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
}

export function EnemyPlate({ level, hp, hpMax, width = ENEMY_PLATE.w, className, style }: EnemyPlateProps) {
  const pk = width / ENEMY_PLATE.w
  const h = ENEMY_PLATE.h * pk
  const levelBox = fracStyle(ENEMY_PLATE.level)
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
        {level}
      </span>
      <span className={p.hp} style={fracStyle(ENEMY_PLATE.hpFill)}>
        <img src={kitAsset('bar_fill_enemy_red')} alt="" draggable={false} style={{ clipPath: barClipPath(hp, hpMax) }} />
      </span>
    </div>
  )
}
