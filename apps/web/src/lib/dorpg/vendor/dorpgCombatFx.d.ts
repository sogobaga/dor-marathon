// 型別宣告：對照 dorpgCombatFx.js（複製自素材包 runtime）與
// source/music/Battle_FX/DORPG_Combat_FX_Pack_v12/README.md 的 API 說明手寫而成。
// manifest 相關介面只鎖定 runtime 實際會讀取的必要欄位，刻意「不」加 `[key: string]: unknown`
// 這種索引簽章尾巴——TS 規則是「target 有索引簽章、source 沒有」會直接判定不相容（即使每個
// 欄位值都滿足 unknown），反而會擋掉 ASSETS 工作者 fxManifest.ts 匯出的具體型別（FxEffect/
// FxFrame/…，欄位更多但沒有索引簽章）。這裡改成單純的必要欄位子集，讓多欄位的具體型別可以
// 結構相容地指派進來（見 CombatFxLayer.tsx 的 `new DORPGCombatFX({ manifest: FX_MANIFEST })`）。

export type DORPGFxWeapon = 'sword' | 'staff' | 'bow' | 'greatsword'
export type DORPGFxResult = 'normal' | 'critical' | 'miss' | 'immune'

export interface DORPGFxRect {
  x: number
  y: number
  width: number
  height: number
}

export interface DORPGFxFrame {
  index: number
  x: number
  y: number
  w: number
  h: number
  durationMs: number
}

/** manifest.effects[]：16 組 weapon×result 的攻擊動畫定義（含音效 id，僅 internalAudio:true 時使用）。 */
export interface DORPGFxEffectDef {
  id: string
  weapon: DORPGFxWeapon
  result: DORPGFxResult
  atlas: string
  displayWidthCssPx: number
  impactAtMs: number
  hasContact?: boolean
  audioId: string
  frames: DORPGFxFrame[]
}

/** manifest.labels[]：index 0 固定 critical、1 固定 miss（runtime tick() 用 li=0/1 直接索引）。 */
export interface DORPGFxLabelDef {
  id: string
  image: string
  width: number
  height: number
}

export interface DORPGFxGlyph {
  x: number
  y: number
  w: number
  h: number
  xadvance?: number
}

/** manifest.font：點陣傷害數字圖集（0–9），數字排版靠 glyphs + pairAdvance。 */
export interface DORPGFxFont {
  atlas: string
  cellHeight: number
  glyphs: Record<string, DORPGFxGlyph>
  pairAdvance?: Record<string, number>
  advance?: number
}

/** manifest.audio[]：18 種音效（16 武器結果音 + Critical/Miss 文字音）。web 為同源 24kHz wav 路徑。 */
export interface DORPGFxAudioDef {
  id: string
  web: string
  wav?: string
}

/** new DORPGCombatFX({ manifest }) 期望的形狀；對齊 fxManifest.ts 匯出的 FX_MANIFEST。 */
export interface DORPGCombatFxManifest {
  effects: DORPGFxEffectDef[]
  labels: DORPGFxLabelDef[]
  font: DORPGFxFont
  audio: DORPGFxAudioDef[]
}

export interface DORPGFxPlayEvent {
  /** 幂等鍵：重複 eventId 不會重播（runtime 內部用 Set 記錄，見 dorpgCombatFx.js）。 */
  eventId: string
  targetId: string
  weapon: DORPGFxWeapon
  result: DORPGFxResult
  /** 最終傷害（整數，0–999999999）；miss/immune 必須為 0，critical 必須 > 0。前端不再乘 2。 */
  damage: number
  /** canvas 內的 CSS px 座標（非螢幕座標）；miss 時實際落點依 targetBounds 或 (x, y+32) 計算。 */
  x: number
  y: number
  /** 提供則 miss 的掠空軌跡改用目標可見軀幹矩形的 (50%, 62%) 定位。 */
  targetBounds?: DORPGFxRect
  /** true 時額外播 Critical/Miss 文字提示音（僅 internalAudio:true 有效；預設不疊加）。 */
  labelSound?: boolean
  /** 動畫進行到 impactAtMs 那一格時觸發一次；miss/immune 的 hasContact 為 false。 */
  onImpact?(info: { eventId: string; hasContact?: boolean; result: DORPGFxResult }): void
}

export interface DORPGCombatFxOptions {
  canvas: HTMLCanvasElement
  manifest: DORPGCombatFxManifest
  /** 資源路徑解析的基準 URL；fxManifest.ts 已把路徑改寫成絕對 URL，故 CombatFxLayer 傳 ''。 */
  baseURL?: string
  /** audioId → 直接可 fetch 的 URL 覆寫表；internalAudio:false 時不會用到。 */
  audioData?: Record<string, string>
  volume?: number
  muted?: boolean
  reducedMotion?: boolean
  /**
   * 預設 true（素材包原行為：畫面+音效一起播）。DORPG 專案一律傳 false —— 音效統一交給
   * src/lib/dorpg/audio.ts 的 battleAudio 播放，避免與這支 runtime 雙重播放（見 .js 檔頭註解）。
   */
  internalAudio?: boolean
}

export declare class DORPGCombatFX {
  constructor(opts: DORPGCombatFxOptions)
  readonly canvas: HTMLCanvasElement
  reducedMotion: boolean
  /** 最近一次音訊操作失敗的錯誤訊息（除錯用）；internalAudio:false 時不會被設定。 */
  lastAudioError?: string
  /** 等待字型與 Critical/Miss 文字圖集載入完成。 */
  ready(): Promise<this>
  /** 必須在使用者手勢內呼叫。internalAudio:false 時直接回傳 false、不建立 AudioContext。 */
  unlockAudio(): Promise<boolean>
  setVolume(v: number): void
  setMuted(v: boolean): void
  /** 依 manifest.audio 的 id 解碼 AudioBuffer 並快取；internalAudio:false 時恆回傳 null。 */
  buffer(id: string): Promise<AudioBuffer | null>
  /** 預先載入某武器（或全部）的 6 格攻擊圖集，缺省載入全部 16 組。 */
  preload(weapon?: DORPGFxWeapon): Promise<void>
  /**
   * 播放一次攻擊特效；重複 eventId 或（disposed／分頁隱藏／期間被 cancelAll）時回傳 false
   * 且不會排入播放佇列——此時 onImpact 不會被呼叫，呼叫端需自行視為「立即完成」以免卡住。
   */
  play(event: DORPGFxPlayEvent): Promise<boolean>
  /** 清空所有進行中的特效與音效尾音（不觸發個別 onImpact）；切場景時呼叫。 */
  cancelAll(): void
  /** 永久卸載：cancelAll() + 解除 visibilitychange 監聽 + 釋放圖片/音訊快取 + 關閉 AudioContext。 */
  dispose(): void
}
