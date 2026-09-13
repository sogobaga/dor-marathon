// DORPG 大型戰鬥素材的 CDN 路徑輔助（P1 ASSETS）。
// 只管「怎麼組出 R2 網址／同源音效路徑」這件事——實際數值（動畫幀、特效參數）分別在
// monsterAnim.ts / fxManifest.ts；上傳流程與來源檔案清單見 scripts/dorpg-upload/upload_dorpg.py。
//
// 與既有 lib/dorpg/assets.ts 的差異：assets.ts 的 monsterPoster()/sceneImage() 指向本機
// public/ui/dorpg/{mon,scene}/（P0 就有的靜態小圖，供 UI 版面預覽用）；這裡的 monsterSheetUrl() 等
// 指向 R2 的「戰鬥用」大圖集/音檔（sprite 動畫、BGM、特效 atlas），兩邊路徑刻意不共用、互不影響。

/** R2 公開網域下的 dorpg/ 前綴（見 CONTRACT.md §3；bucket 設定在 repo 根 r2.env，不在此檔）。 */
export const DORPG_CDN = 'https://img.dor.tw/dorpg';

/** 怪物四動作 sprite 圖集（1536×1024，3 欄 × 2 列 × 512² frame）：dorpg/mon/<monsterId>/<action>.webp */
export function monsterSheetUrl(monsterId: string, action: 'idle' | 'attack' | 'hit' | 'death'): string {
  return `${DORPG_CDN}/mon/${monsterId}/${action}.webp`;
}

/** 戰鬥 BGM：一般場 master、boss 場 boss（BattleSample.sceneKind 決定播哪首）。 */
export function bgmUrl(kind: 'master' | 'boss'): string {
  return `${DORPG_CDN}/bgm/${kind}.mp3`;
}

/** 攻擊特效圖集：dorpg/fx/effects/<weapon>/<result>/atlas.webp（4 武器 × 4 結果 = 16 張）。 */
export function fxAtlasUrl(weapon: 'sword' | 'staff' | 'bow' | 'greatsword', result: 'normal' | 'critical' | 'miss' | 'immune'): string {
  return `${DORPG_CDN}/fx/effects/${weapon}/${result}/atlas.webp`;
}

/**
 * 爆擊／Miss 文字標籤圖：'atlas' 是舊版逐格 pop 動畫圖集（目前 runtime v12 未使用，僅保留擴充/預覽用），
 * 'label' 是 runtime 實際會畫的完整標籤圖（見 fxManifest.ts 的 FxLabel.image 說明）。
 */
export function fxLabelUrl(kind: 'critical' | 'miss', variant: 'atlas' | 'label'): string {
  return `${DORPG_CDN}/fx/labels/${kind}/${variant}.webp`;
}

/** 傷害數字字型圖集（0–9，ivory_gold 樣式；DORPG_Combat_FX_Pack_v12 目前只有這一種樣式，故不需 style 參數）。 */
export function fxDigitsUrl(): string {
  return `${DORPG_CDN}/fx/digits/atlas.webp`;
}

/**
 * 特效音效（18 個 .web.wav，24kHz、合計 ≈344KB）：同源 /ui/dorpg/audio/<id>.wav，不進 R2、免 CORS。
 * id 例如 'sfx_sword_normal'、'sfx_label_critical'（完整清單見 fxManifest.ts 的 FX_MANIFEST.audio[].id）。
 */
export function fxAudioUrl(id: string): string {
  return `/ui/dorpg/audio/${id}.wav`;
}
