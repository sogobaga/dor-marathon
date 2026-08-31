// 上傳前在瀏覽器壓縮圖片。
//
// 背景（使用者原話）：「使用者的使用情境是手機，隨手打開的是手機的照片，阻擋 5MB 不能上傳是違反
// 人性的設計，應該上傳後我們進行壓縮，不是讓使用者自己壓縮後才上傳」——iPhone 相簿原圖常見 3–12MB，
// 舊版前端不壓縮直接送給後端，等於必被舊的 5MB 硬擋。改成：選檔後先在瀏覽器壓縮，壓完才送出。
//
// 設計原則：
// - **全程不 throw**：解碼失敗、canvas 被污染、toBlob 回 null、瀏覽器不支援 createImageBitmap/canvas
//   等任何一步出錯，一律 `return file`（回傳原始檔案），交給後端的完整驗證與更嚴格檢查去處理
//   （後端會回具體錯誤訊息，例如「不支援的圖片格式」，呼叫端要把這個錯誤原樣顯示，不能吞掉）。
// - **不放大**：長邊已經 <= maxDim 的圖片不放大，只在超過時才縮小。
// - **不逆向膨脹**：壓縮後的檔案如果反而比原檔大（例如原檔已經是高度壓縮的小圖），改回傳原檔。
// - 純邏輯（尺寸換算 computeTargetSize / 副檔名處理 toJpegFileName / 是否採用壓縮結果
//   shouldUseCompressed）抽成獨立函式，可在 Node 下用 scripts/verify-image-compress.mjs 驗證；
//   實際解碼與 canvas 繪製依賴瀏覽器 API，無法在 Node 測（腳本開頭有說明）。

export interface CompressOptions {
  /** 長邊上限（像素）。預設 1600，小於此值的圖片不放大。 */
  maxDim?: number
  /** JPEG 品質（0–1）。預設 0.82。 */
  quality?: number
}

export const DEFAULT_MAX_DIM = 1600
export const DEFAULT_QUALITY = 0.82

/**
 * 依長邊上限計算縮放後的寬高：長邊 <= maxDim 時原樣回傳（不放大）；超過時等比縮小到長邊 = maxDim。
 * 純函式，不碰任何瀏覽器 API，可在 Node 下單元測試。
 */
export function computeTargetSize(width: number, height: number, maxDim: number = DEFAULT_MAX_DIM): { width: number; height: number } {
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0 || !Number.isFinite(maxDim) || maxDim <= 0) {
    return { width: Math.round(width) || 0, height: Math.round(height) || 0 }
  }
  const longSide = Math.max(width, height)
  if (longSide <= maxDim) return { width: Math.round(width), height: Math.round(height) }
  const scale = maxDim / longSide
  return { width: Math.max(1, Math.round(width * scale)), height: Math.max(1, Math.round(height * scale)) }
}

/** 檔名沿用（去除原副檔名），副檔名一律換成 .jpg（壓縮輸出固定 image/jpeg）。純函式。 */
export function toJpegFileName(originalName: string): string {
  const stripped = (originalName || '').trim().replace(/\.[^./\\]+$/, '')
  const base = stripped || 'image'
  return `${base}.jpg`
}

/**
 * 壓縮後是否採用新檔：只有嚴格變小才採用；相等或反而變大（已是高度壓縮的小圖）一律回傳 false，
 * 呼叫端據此改用原檔。compressedSize 為 0（toBlob 失敗等）視為不可用。純函式。
 */
export function shouldUseCompressed(originalSize: number, compressedSize: number): boolean {
  return Number.isFinite(compressedSize) && compressedSize > 0 && compressedSize < originalSize
}

/** 是否需要壓縮處理：長邊超過 maxDim 就一定要縮小；即使長邊已在門檻內，仍照樣重新編碼一次
 *  （目的是用 quality 壓掉檔案大小），所以這個函式目前只用來標記「是否會被縮放」，供測試/除錯用。 */
export function needsDownscale(width: number, height: number, maxDim: number = DEFAULT_MAX_DIM): boolean {
  return Math.max(width, height) > maxDim
}

/**
 * 上傳前壓縮圖片。失敗一律 fallback 回傳原檔（見檔頭說明），不會 throw。
 *
 * 解碼順序：優先用 `createImageBitmap(file, { imageOrientation: 'from-image' })`
 * （瀏覽器原生解碼，自動套用 EXIF 方向，不會把直拍照片轉橫的）；不支援或失敗時退回
 * `new Image()` + `URL.createObjectURL(file)`。兩者都失敗（例如瀏覽器完全不支援、或 HEIC 這類
 * canvas 解不了的格式）就回傳原檔，讓後端的格式驗證接手並回錯誤訊息。
 */
export async function compressImageFile(file: File, opts?: CompressOptions): Promise<File> {
  const maxDim = opts?.maxDim ?? DEFAULT_MAX_DIM
  const quality = opts?.quality ?? DEFAULT_QUALITY

  if (typeof document === 'undefined') return file // SSR/非瀏覽器環境安全網，理論上不會被呼叫到

  let source: ImageBitmap | HTMLImageElement | null = null
  let objectUrl: string | null = null

  try {
    // ① 優先：createImageBitmap（原生解碼，from-image 自動套用 EXIF 旋轉）
    if (typeof createImageBitmap === 'function') {
      try {
        source = await createImageBitmap(file, { imageOrientation: 'from-image' })
      } catch {
        source = null
      }
    }

    // ② fallback：<img> + objectURL（不支援 createImageBitmap，或該格式 createImageBitmap 解不了）
    if (!source) {
      try {
        objectUrl = URL.createObjectURL(file)
        const img = new Image()
        await new Promise<void>((resolve, reject) => {
          img.onload = () => resolve()
          img.onerror = () => reject(new Error('decode failed'))
          img.src = objectUrl as string
        })
        source = img
      } catch {
        source = null
      }
    }

    if (!source) return file // 兩種解碼都失敗（例如 HEIC）→ 原檔交給後端判斷

    const sourceW = 'naturalWidth' in source ? source.naturalWidth : source.width
    const sourceH = 'naturalHeight' in source ? source.naturalHeight : source.height
    if (!sourceW || !sourceH) return file

    const { width, height } = computeTargetSize(sourceW, sourceH, maxDim)
    if (!width || !height) return file

    const canvas = document.createElement('canvas')
    canvas.width = width
    canvas.height = height
    const ctx = canvas.getContext('2d')
    if (!ctx) return file
    ctx.drawImage(source, 0, 0, width, height)

    const blob = await new Promise<Blob | null>((resolve) => {
      try {
        canvas.toBlob((b) => resolve(b), 'image/jpeg', quality)
      } catch {
        resolve(null) // canvas 被污染（tainted）等同步例外
      }
    })
    if (!blob) return file
    if (!shouldUseCompressed(file.size, blob.size)) return file

    return new File([blob], toJpegFileName(file.name), { type: 'image/jpeg', lastModified: Date.now() })
  } catch {
    return file // 任何未預期例外一律 fallback 回原檔，不擋使用者
  } finally {
    if (objectUrl) { try { URL.revokeObjectURL(objectUrl) } catch { /* ignore */ } }
    if (source && typeof (source as ImageBitmap).close === 'function') {
      try { (source as ImageBitmap).close() } catch { /* ignore */ }
    }
  }
}
