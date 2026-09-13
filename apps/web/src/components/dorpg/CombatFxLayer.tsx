'use client'

// 攻擊特效畫布層：包住 vendor/dorpgCombatFx.js（素材包 runtime，只管畫面）。
// 音效不在這裡播放（見 vendor 檔頭與 audio.ts 的說明）——呼叫端（useBattle）在同一個攻擊事件
// 另外呼叫 battleAudio.playSfx，兩邊各司其職，避免同一次攻擊被兩套系統各播一次音效。

import { forwardRef, useEffect, useImperativeHandle, useRef } from 'react'
import { DORPGCombatFX } from '@/lib/dorpg/vendor/dorpgCombatFx'
import type { DORPGFxResult } from '@/lib/dorpg/vendor/dorpgCombatFx'
import { FX_MANIFEST } from '@/lib/dorpg/fxManifest'
import type { WeaponKind } from '@/lib/dorpg/types'
import styles from './CombatFxLayer.module.css'

export interface CombatFxPlayOptions {
  weapon: WeaponKind
  result: DORPGFxResult
  /** 最終傷害（整數）；miss/immune 必須為 0，critical 必須 > 0——與引擎的 attack 事件同一份數字，前端不再乘 2。 */
  damage: number
  /** 舞台座標系（BattleStage 內的 CSS px），通常是目標怪物的軀幹中心。 */
  x: number
  y: number
  targetId: string
}

export interface CombatFxLayerHandle {
  /** 排一次攻擊特效；回傳的 Promise 在動畫播到「接觸/掠過」那一格（impact）時 resolve，供受擊動作同步。 */
  play(opts: CombatFxPlayOptions): Promise<void>
  /** 清空所有進行中的特效（例如切場景／戰鬥結束）。 */
  cancelAll(): void
}

interface CombatFxLayerProps {
  /** CSS px；需與所疊放的戰鬥舞台同尺寸（由外層 BattleStage 決定並保持 position:relative）。 */
  width: number
  height: number
}

// impact 保底逾時：素材包最長效果是 critical（impactAtMs 105 + 720 文字動畫遠小於此值），
// 2.5s 遠大於任何一組效果的總時長。極端情況下（例如排程當下分頁被切到背景，或又剛好被
// cancelAll 清空佇列）onImpact 可能永遠不會觸發，逾時仍會 resolve，避免呼叫端卡住等待。
const IMPACT_TIMEOUT_MS = 2500

const CombatFxLayer = forwardRef<CombatFxLayerHandle, CombatFxLayerProps>(function CombatFxLayer(
  { width, height },
  ref,
) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const fxRef = useRef<DORPGCombatFX | null>(null)
  const seqRef = useRef(0)

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    // internalAudio:false —— 只畫特效，音效交給 battleAudio（見檔頭說明）。
    const fx = new DORPGCombatFX({ canvas, manifest: FX_MANIFEST, baseURL: '', internalAudio: false })
    fxRef.current = fx
    fx.ready()
      .then(() => Promise.all([fx.preload('sword'), fx.preload('staff')]))
      .catch((err) => {
        console.error('[CombatFxLayer] preload 失敗', err)
      })
    return () => {
      fx.dispose()
      fxRef.current = null
    }
    // FX_MANIFEST 是模組常數；width/height 只影響 canvas 顯示尺寸（由下面的 effect 處理），
    // 不需要因為它們變動就重建整個 DORPGCombatFX 實例（會遺失正在載入中的圖集快取）。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    // DPR 縮放的實際換算在 vendor runtime 的 resize()（於每次 tick 開頭呼叫）：它讀
    // canvas.getBoundingClientRect() 取得 CSS 尺寸再乘 devicePixelRatio 設定畫布內部解析度。
    // 這裡只需確保 CSS 尺寸跟外層舞台一致即可。
    const canvas = canvasRef.current
    if (!canvas) return
    canvas.style.width = `${width}px`
    canvas.style.height = `${height}px`
  }, [width, height])

  useImperativeHandle(
    ref,
    () => ({
      play(opts) {
        const fx = fxRef.current
        if (!fx) return Promise.resolve()
        const eventId = `fx_${Date.now()}_${seqRef.current++}`
        return new Promise<void>((resolve) => {
          let settled = false
          const settle = () => {
            if (settled) return
            settled = true
            resolve()
          }
          const timeout = setTimeout(settle, IMPACT_TIMEOUT_MS)
          fx
            .play({
              eventId,
              targetId: opts.targetId,
              weapon: opts.weapon,
              result: opts.result,
              damage: opts.damage,
              x: opts.x,
              y: opts.y,
              onImpact: () => {
                clearTimeout(timeout)
                settle()
              },
            })
            .then((queued) => {
              // queued=false：重複 eventId、已 dispose、分頁隱藏或被 cancelAll——不會有 onImpact，
              // 視為立即完成，避免呼叫端卡住等待一個永遠不會發生的 impact。
              if (!queued) {
                clearTimeout(timeout)
                settle()
              }
            })
            .catch((err) => {
              console.error('[CombatFxLayer] play 失敗', err)
              clearTimeout(timeout)
              settle()
            })
        })
      },
      cancelAll() {
        fxRef.current?.cancelAll()
      },
    }),
    [],
  )

  return <canvas ref={canvasRef} className={styles.canvas} width={width} height={height} />
})

CombatFxLayer.displayName = 'CombatFxLayer'
export default CombatFxLayer
