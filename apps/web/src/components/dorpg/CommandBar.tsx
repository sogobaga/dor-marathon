'use client';

// DORPG 主操作列（道具／防禦／攻擊）。P0 只負責：依 props 換四態按鈕圖、把 pointer 事件原樣轉給回呼。
// 長按／蓄氣／互斥等手勢狀態機留給 P1（kit 的 bindHold 邏輯），這裡刻意不持有任何狀態。
import type { CSSProperties, PointerEvent } from 'react';
import type { BtnState } from '@/lib/dorpg/types';
import { COMMAND_BTN, LAYOUT, barClipPath, kitAsset } from '@/lib/dorpg/assets';
import styles from './CommandBar.module.css';

export type CommandBarProps = {
  item: BtnState;
  guard: BtnState;
  attack: BtnState;
  onItem?: () => void;
  onGuardDown?: () => void;
  onGuardUp?: () => void;
  onAttackDown?: () => void;
  onAttackUp?: () => void;
  /** 0–1 蓄氣比例；給值才畫攻擊鈕內的金色蓄氣條（bar_fill_charge_gold）。 */
  charge?: number;
  /** 邏輯寬高（預設 390×80）。 */
  width?: number;
  height?: number;
};

/** kit preview #actions：左右內距 6、按鈕間距 6、上下內距 3 → (390−24)/3 = 122 寬、80−6 = 74 高。 */
const PAD_X = 6;
const PAD_Y = 3;
const GAP = 6;
const BTN_COUNT = 3;
const DEFAULT_HEIGHT = LAYOUT.standard.commands;

type HoldHandlers = {
  onPointerDown?: (e: PointerEvent<HTMLButtonElement>) => void;
  onPointerUp?: () => void;
  onPointerCancel?: () => void;
  onPointerLeave?: () => void;
};

/** 把 down/up 回呼接到 pointer 事件；disabled 時整組不接（原生 disabled 之外再保險一層，touch 在部分瀏覽器仍會派發）。 */
function holdHandlers(state: BtnState, down?: () => void, up?: () => void): HoldHandlers {
  if (state === 'disabled') return {};
  return {
    onPointerDown: (e) => {
      // 滑鼠只認左鍵；觸控／筆的 button 也是 0，不受影響。
      if (e.button !== 0) return;
      down?.();
    },
    onPointerUp: () => up?.(),
    onPointerCancel: () => up?.(),
    // 注意：pointerleave 在未按下時（滑鼠移出）也會觸發 Up，P1 加 pointer capture 後改由手勢層過濾。
    onPointerLeave: () => up?.(),
  };
}

export default function CommandBar({
  item,
  guard,
  attack,
  onItem,
  onGuardDown,
  onGuardUp,
  onAttackDown,
  onAttackUp,
  charge,
  width = LAYOUT.designWidth,
  height = DEFAULT_HEIGHT,
}: CommandBarProps) {
  // 內距／間距換成相對於容器寬（padding、gap 的百分比都以容器寬為準）；按鈕寬 = (內容寬 − 2×gap)/3。
  const padXPct = (PAD_X / LAYOUT.designWidth) * 100;
  const contentW = LAYOUT.designWidth - PAD_X * 2;
  const gapPct = (GAP / contentW) * 100;
  const rootStyle = {
    width: `${width}px`,
    height: `${height}px`,
    '--pad-x': `${padXPct}%`,
    '--pad-y': `${PAD_Y}px`,
    '--gap': `${gapPct}%`,
    '--btn-w': `calc((100% - ${gapPct * (BTN_COUNT - 1)}%) / ${BTN_COUNT})`,
    '--btn-ratio': `${COMMAND_BTN.w} / ${COMMAND_BTN.h}`,
  } as CSSProperties;

  const showCharge = typeof charge === 'number' && Number.isFinite(charge);

  return (
    <div className={styles.bar} style={rootStyle} role="group" aria-label="主操作">
      <button
        type="button"
        className={styles.btn}
        aria-label="道具"
        aria-pressed={item === 'active' || undefined}
        data-state={item}
        disabled={item === 'disabled'}
        onClick={item === 'disabled' ? undefined : onItem}
      >
        <img className={styles.art} src={kitAsset(`button_item_${item}`)} alt="" draggable={false} />
      </button>

      <button
        type="button"
        className={`${styles.btn} ${styles.hold}`}
        aria-label="防禦"
        aria-pressed={guard === 'active' || undefined}
        data-state={guard}
        disabled={guard === 'disabled'}
        {...holdHandlers(guard, onGuardDown, onGuardUp)}
      >
        <img className={styles.art} src={kitAsset(`button_guard_${guard}`)} alt="" draggable={false} />
      </button>

      <button
        type="button"
        className={`${styles.btn} ${styles.hold}`}
        aria-label="攻擊"
        data-state={attack}
        disabled={attack === 'disabled'}
        {...holdHandlers(attack, onAttackDown, onAttackUp)}
      >
        <img className={styles.art} src={kitAsset(`button_attack_${attack}`)} alt="" draggable={false} />
        {showCharge && (
          <div className={styles.chargeTrack} aria-hidden="true">
            {/* 金色蓄氣條鋪滿軌道、只裁右側（與血條同一套 clip-path 作法） */}
            <img
              src={kitAsset('bar_fill_charge_gold')}
              alt=""
              draggable={false}
              style={{ clipPath: barClipPath(charge, 1) }}
            />
          </div>
        )}
      </button>
    </div>
  );
}
