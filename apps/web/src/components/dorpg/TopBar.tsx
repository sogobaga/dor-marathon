'use client';

// DORPG 頂欄（44px）：左＝逃跑鈕（button_escape_<state> 視覺 76×36、點擊區 44 高）＋逃跑結果文字；
// 中＝DORPG 標誌；右＝設定齒輪（44×44）。用三欄 grid 讓標誌真正置中（規格書第 06 章 R01–R05）。
import type { CSSProperties } from 'react';
import type { EscapeState } from '@/lib/dorpg/types';
import { ESCAPE_BTN, KIT_SIZES, PALETTE, kitAsset } from '@/lib/dorpg/assets';
import styles from './TopBar.module.css';

export type TopBarProps = {
  escapeState: EscapeState;
  /** 逃跑結果（如「逃跑失敗」）。kit 註記：失敗原因不烤進按鈕，另外以文字顯示。 */
  escapeMessage?: string;
  onEscape: () => void;
  onSettings: () => void;
  /** 邏輯高 px，預設 44（LAYOUT.*.toolbar）。 */
  height?: number;
};

/** logo_dorpg 邏輯 144×28；kit 組裝預覽把它縮成 132 寬（contain 不變形）讓出逃跑訊息空間，這裡照抄以對齊預覽圖。 */
const LOGO = { w: 132, h: KIT_SIZES.logo_dorpg.h } as const;

export default function TopBar({
  escapeState,
  escapeMessage,
  onEscape,
  onSettings,
  height = KIT_SIZES.panel_header_blank.h,
}: TopBarProps) {
  // 顏色只從 PALETTE 來；focus 環顏色以自訂變數交給 CSS。
  const rootStyle = {
    height,
    background: PALETTE.surfaceBase,
    color: PALETTE.textSecondary,
    '--dorpg-focus': PALETTE.targetGold,
  } as CSSProperties;
  const settings = KIT_SIZES.icon_settings;

  return (
    <div className={styles.bar} style={rootStyle}>
      <div className={styles.left}>
        <button
          type="button"
          className={styles.btn}
          // manifest minimumHitSize：視覺 76×36，但點擊區至少要 76×44
          style={{ width: ESCAPE_BTN.w, height: ESCAPE_BTN.minHit }}
          onClick={onEscape}
          disabled={escapeState === 'disabled'}
          aria-label="逃跑"
        >
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src={kitAsset(`button_escape_${escapeState}`)} width={ESCAPE_BTN.w} height={ESCAPE_BTN.h} alt="" draggable={false} />
        </button>
        {/* 常駐的 live region：訊息出現時螢幕閱讀器才會播報（區塊若動態新增，多數 AT 不會唸） */}
        <span className={styles.message} role="status" aria-live="polite">
          {escapeMessage ?? ''}
        </span>
      </div>

      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img className={styles.logo} src={kitAsset('logo_dorpg')} width={LOGO.w} height={LOGO.h} alt="DORPG" draggable={false} />

      <div className={styles.right}>
        <button
          type="button"
          className={styles.btn}
          style={{ width: settings.w, height: settings.h }}
          onClick={onSettings}
          aria-label="設定"
        >
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src={kitAsset('icon_settings')} width={settings.w} height={settings.h} alt="" draggable={false} />
        </button>
      </div>
    </div>
  );
}
