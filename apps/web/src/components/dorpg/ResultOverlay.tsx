'use client';

// DORPG 戰鬥結算彈窗（P1）：frame_dialog 九宮格＋依 outcome 給標題與摘要列、「返回」主按鈕（button_confirm 圖）。
// 本身只是 fixed inset:0 的內容盒＋遮罩，不寫死 z-index——契約 §6：實際疊在哪一層由 BattleScreen 組裝時
// 包住本元件的外層 class 決定，這裡只負責內容本身置中與遮罩。
// BattleOutcome 直接照契約字面量宣告，不 import engine/types（ENGINE 那邊的型別結構相同、之後接上是
// 結構相容，不需要在這裡建立跨工作者的檔案依賴）。
import { useEffect, useId } from 'react';
import { DIALOG_BTN, PALETTE, kitAsset, nineSliceStyle } from '@/lib/dorpg/assets';
import styles from './ResultOverlay.module.css';

export type BattleOutcome = 'victory' | 'defeat' | 'draw' | 'escaped';

export type ResultOverlaySummary = {
  enemiesDefeated: number;
  damageDealt: number;
  durationMs: number;
  /** 預估經驗值；未給時不畫該列。 */
  expPreview?: number;
};

export type ResultOverlayProps = {
  outcome: BattleOutcome;
  summary: ResultOverlaySummary;
  onClose: () => void;
};

const TITLE: Record<BattleOutcome, string> = {
  victory: '勝利',
  defeat: '戰敗',
  draw: '雙方倒下',
  escaped: '成功逃離',
};

/** 標題色：勝利／逃離走金，戰敗／平手走較低調的灰藍——沿用規格色票，不另定新色。 */
const TITLE_COLOR: Record<BattleOutcome, string> = {
  victory: PALETTE.borderGold,
  defeat: PALETTE.textSecondary,
  draw: PALETTE.textSecondary,
  escaped: PALETTE.borderGold,
};

function formatDuration(ms: number): string {
  const total = Math.max(0, Math.round(ms / 1000));
  const m = Math.floor(total / 60);
  const s = total % 60;
  return `${m}:${String(s).padStart(2, '0')}`;
}

export default function ResultOverlay({ outcome, summary, onClose }: ResultOverlayProps) {
  const titleId = useId();

  // Escape 關閉（契約：Escape／主鈕 → onClose）。scrim 點擊刻意不列入關閉方式，避免使用者誤觸略過結算摘要。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const rows: { label: string; value: string }[] = [
    { label: '擊敗敵人', value: `${summary.enemiesDefeated}` },
    { label: '造成傷害', value: `${summary.damageDealt}` },
    { label: '戰鬥時間', value: formatDuration(summary.durationMs) },
  ];
  if (typeof summary.expPreview === 'number') {
    rows.push({ label: '預估經驗', value: `+${summary.expPreview}` });
  }

  return (
    <div className={styles.overlay} style={{ color: PALETTE.textPrimary }}>
      <div className={styles.scrim} style={{ background: PALETTE.overlayScrim }} aria-hidden="true" />
      <div
        className={styles.dialog}
        style={nineSliceStyle('frame_dialog')}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
      >
        <h2 id={titleId} className={styles.title} style={{ color: TITLE_COLOR[outcome] }}>
          {TITLE[outcome]}
        </h2>

        <ul className={styles.rows}>
          {rows.map((r) => (
            <li key={r.label} className={styles.row}>
              <span className={styles.rowLabel} style={{ color: PALETTE.textSecondary }}>
                {r.label}
              </span>
              <span className={styles.rowValue}>{r.value}</span>
            </li>
          ))}
        </ul>

        {/* button_confirm 圖上烤的固定字是「確認」，但這顆鈕語意是「返回」戰鬥外層——aria-label 蓋掉圖上文字，
            img 本身 alt="" 當純裝飾（同 CommandBar／SkillTray 的既有作法）。 */}
        <button
          type="button"
          className={styles.confirm}
          style={{ width: DIALOG_BTN.w, height: DIALOG_BTN.h }}
          aria-label="返回"
          onClick={onClose}
        >
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src={kitAsset('button_confirm')} alt="" draggable={false} />
        </button>
      </div>
    </div>
  );
}
