// 效果資產型錄：把「先做出來的暫代 emoji / 合成音效」集中管理，方便逐一替換成正式素材。
// slug 對應程式內使用位置；後台「效果管理」上傳後覆寫，前台跑步引擎讀取生效。
import { effectsApi } from './api'
import { setSoundOverrides } from './sfx'

export interface EffectSpec {
  slug: string
  category: string
  label: string
  where: string // 使用位置
  type: 'image' | 'audio'
  placeholder: string // 目前暫代（emoji 或「合成音」）
  size: string // 建議尺寸/長度
  format: string // 建議格式
  maxKB: number // 建議檔案大小上限
  note?: string
}

export const EFFECT_SPECS: EffectSpec[] = [
  // --- 互動小遊戲圖示 ---
  { slug: 'interaction.tap.icon', category: '互動小遊戲', label: '點擊攻擊 圖示', where: '跑步中「連續點擊」小遊戲的中央圖示', type: 'image', placeholder: '👊', size: '512 × 512 px（正方）', format: 'PNG（去背透明）', maxKB: 120, note: '會顯示約 60px，建議簡潔、對比高' },
  { slug: 'interaction.defend.icon', category: '互動小遊戲', label: '防禦 圖示（按住中）', where: '「按住防禦」小遊戲，按住時的中央圖示', type: 'image', placeholder: '🛡️', size: '512 × 512 px（正方）', format: 'PNG（去背透明）', maxKB: 120 },
  { slug: 'interaction.idle.icon', category: '互動小遊戲', label: '待命 圖示（未按住）', where: '「按住防禦」小遊戲，尚未按住時的中央圖示', type: 'image', placeholder: '✋', size: '512 × 512 px（正方）', format: 'PNG（去背透明）', maxKB: 120 },
  { slug: 'interaction.tap.fx', category: '互動小遊戲', label: '點擊噴濺特效', where: '連續點擊時，手指位置彈出的粒子特效', type: 'image', placeholder: '💥 💧 ⭐ 🪨', size: '256 × 256 px（正方）', format: 'PNG（去背透明）', maxKB: 80, note: '目前為 4 種 emoji 輪替；上傳單張透明 PNG 取代（之後可擴充序列圖）' },
  // --- 音效 ---
  { slug: 'sound.event_alert', category: '音效', label: '事件來了 提示音', where: '事件任務／多人事件邀請「跳出」時', type: 'audio', placeholder: '合成音（beep-beep-BOOP）', size: '約 0.4–0.6 秒', format: 'MP3 或 OGG（單聲道即可）', maxKB: 40 },
  { slug: 'sound.event_complete', category: '音效', label: '事件完成 成功音', where: '事件任務完成結算時', type: 'audio', placeholder: '合成音（上行琶音）', size: '約 0.5–0.8 秒', format: 'MP3 或 OGG', maxKB: 40 },
  { slug: 'sound.tap_hit', category: '音效', label: '點擊打擊音', where: '連續點擊小遊戲，每次點擊', type: 'audio', placeholder: '合成音（打擊）', size: '約 0.08–0.15 秒（會連續快速播放，務必短）', format: 'MP3 或 OGG', maxKB: 15 },
  { slug: 'sound.defend', category: '音效', label: '按住防禦音', where: '按住防禦小遊戲，開始按住時', type: 'audio', placeholder: '合成音（起盾）', size: '約 0.2–0.4 秒', format: 'MP3 或 OGG', maxKB: 25 },
  { slug: 'sound.ding', category: '音效', label: '升級／集滿鐘聲', where: '成績結算 EXP 集滿、升級時', type: 'audio', placeholder: '合成音（鐘聲）', size: '約 0.8–1.2 秒', format: 'MP3 或 OGG', maxKB: 50 },
]

let cache: Record<string, string> = {}

// 前台跑步引擎載入覆寫（圖片存 cache、音效交給 sfx 解碼）
export async function loadEffectAssets(token: string): Promise<Record<string, string>> {
  try {
    const r = await effectsApi.get(token)
    cache = r.assets || {}
    await setSoundOverrides(cache)
  } catch { /* 失敗就用內建暫代 */ }
  return cache
}

export function effectUrl(slug: string): string { return cache[slug] || '' }
