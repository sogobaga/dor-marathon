'use client';

// DORPG 主操作列（道具／防禦／攻擊）。P0 只負責：依 props 換四態按鈕圖、把 pointer 事件原樣轉給回呼。
// P1：長按／蓄氣手勢狀態機移到 useHoldGesture（由外部 useBattle/ASSEMBLE 呼叫），這裡只多加「有 holdProps
// 就直接展開、不再自己綁 pointer 事件」的分支；沒有 holdProps 時完全沿用 P0 的 holdHandlers 行為，舊呼叫端
// （只給 onAttackDown/onAttackUp）不必改一行。
import type { CSSProperties } from 'react';
import type { BtnState } from '@/lib/dorpg/types';
import type { HoldGestureProps } from '@/lib/dorpg/useHoldGesture';
import { COMMAND_BTN, LAYOUT, SKILL_SLOT, barClipPath, fracStyle, kitAsset } from '@/lib/dorpg/assets';
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
  /** 0–1 蓄氣比例；給值才畫攻擊鈕上緣的金色蓄氣條（bar_fill_charge_gold）。 */
  charge?: number;
  /** P1：攻擊鈕的普攻冷卻；給值且 remainingMs>0 才在攻擊鈕上疊 overlay_cooldown_disc＋秒數（與技能格同款）。 */
  attackCooldown?: { remainingMs: number; totalMs: number };
  /** P1：由外部 useHoldGesture(...) 產生，直接展開到對應按鈕；有給的那顆鈕不再自行綁 onPointerDown/Up。 */
  holdProps?: { attack?: HoldGestureProps; guard?: HoldGestureProps };
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

// 攻擊鈕的冷卻圓底：沿用技能格同一顆 overlay_cooldown_disc（40 邏輯 px），置中於攻擊鈕的 122×74 畫布。
// x/w 用按鈕邏輯寬 122 當分母、y/h 用邏輯高 74 當分母（跟 SkillTray 的 CD_RECT 同一手法）：按鈕本身
// 用 aspect-ratio 鎖住 122:74，兩種分母各自縮放後仍會合成正圓，不會被非方形容器拉扁。
const ATTACK_CD_RECT = {
  x: (COMMAND_BTN.w - SKILL_SLOT.cooldownDisc) / 2 / COMMAND_BTN.w,
  y: (COMMAND_BTN.h - SKILL_SLOT.cooldownDisc) / 2 / COMMAND_BTN.h,
  w: SKILL_SLOT.cooldownDisc / COMMAND_BTN.w,
  h: SKILL_SLOT.cooldownDisc / COMMAND_BTN.h,
} as const;
const ATTACK_CD_STYLE = fracStyle(ATTACK_CD_RECT);

/** P0 的簡化版手勢 props：回傳型別故意收斂成 HoldGestureProps 的子集合，讓它能跟 holdProps 走同一個
 * `Partial<HoldGestureProps>` 分支、直接展開到按鈕，不必為兩種來源另外包一層型別轉接。 */
function holdHandlers(state: BtnState, down?: () => void, up?: () => void): Partial<HoldGestureProps> {
  if (state === 'disabled') return {};
  return {
    onPointerDown: (e) => {
      // 滑鼠只認左鍵；觸控／筆的 button 也是 0，不受影響。
      if (e.button !== 0) return;
      down?.();
    },
    onPointerUp: () => up?.(),
    onPointerCancel: () => up?.(),
    // 注意：pointerleave 在未按下時（滑鼠移出）也會觸發 Up，這是 P0 的簡化行為；P1 若改走 holdProps
    // （useHoldGesture）就不會有這個問題，因為那邊用 pointerId 比對過才會 finish。
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
  attackCooldown,
  holdProps,
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

  // 有 holdProps 就用它（外部 useHoldGesture 的完整手勢 props），否則沿用 P0 的簡化版；
  // 兩邊回傳型別都收斂成 Partial<HoldGestureProps>，展開到按鈕上完全等價。
  const attackHandlers = holdProps?.attack ?? holdHandlers(attack, onAttackDown, onAttackUp);
  const guardHandlers = holdProps?.guard ?? holdHandlers(guard, onGuardDown, onGuardUp);

  const cooldownMs = attackCooldown?.remainingMs ?? 0;
  const cooldownActive = cooldownMs > 0;
  // 預設沿用 P0 的靜態 aria-label（"攻擊"）；只有冷卻中才追加秒數，沒給 attackCooldown 的舊呼叫端字串不變。
  const attackAriaLabel = cooldownActive ? `攻擊，冷卻中 ${Math.ceil(cooldownMs / 1000)} 秒` : '攻擊';

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
        {...guardHandlers}
      >
        <img className={styles.art} src={kitAsset(`button_guard_${guard}`)} alt="" draggable={false} />
      </button>

      <button
        type="button"
        className={`${styles.btn} ${styles.hold}`}
        aria-label={attackAriaLabel}
        data-state={attack}
        disabled={attack === 'disabled'}
        {...attackHandlers}
      >
        <img className={styles.art} src={kitAsset(`button_attack_${attack}`)} alt="" draggable={false} />
        {showCharge && (
          <div className={styles.chargeTrack} aria-hidden="true">
            {/* 金色蓄氣條鋪滿軌道、只裁右側（與血條同一套 clip-path 作法）；改貼攻擊鈕上緣（見 .chargeTrack）。 */}
            <img
              src={kitAsset('bar_fill_charge_gold')}
              alt=""
              draggable={false}
              style={{ clipPath: barClipPath(charge, 1) }}
            />
          </div>
        )}
        {cooldownActive && (
          <div className={styles.cooldown} style={ATTACK_CD_STYLE} aria-hidden="true">
            {/* 半透明圓底＋秒數，跟 SkillTray 的技能格冷卻覆層同一顆素材、同一種疊法。 */}
            <img src={kitAsset('overlay_cooldown_disc')} alt="" draggable={false} />
            <span>{Math.ceil(cooldownMs / 1000)}s</span>
          </div>
        )}
      </button>
    </div>
  );
}
