'use client';

// DORPG 技能／道具列（P0：純展示、只吃 props，不含 API／全域狀態）。
// 版面數字全部來自 @/lib/dorpg/assets 的 TRAY／SKILL_SLOT 常數，這裡只把邏輯 px 換成百分比與縮放係數，
// 讓同一份元件在 390 基準、480 封頂或緊湊／短屏的托盤高度下都能等比縮放而不必碰 3× 圖檔像素。
import type { CSSProperties } from 'react';
import type { Item, Skill, TrayMode } from '@/lib/dorpg/types';
import { PALETTE, SKILL_SLOT, TRAY, fracStyle, kitAsset } from '@/lib/dorpg/assets';
import styles from './SkillTray.module.css';

export type SkillTrayProps = {
  mode: TrayMode;
  /** 固定 8 格裝備欄（未裝備補 null）；不足 8 格時元件自行補 null。 */
  skills: (Skill | null)[];
  items: Item[];
  /** 五格視窗的起點 0–3（不循環）。 */
  offset: number;
  onOffset: (n: number) => void;
  /** 每個技能 id 的剩餘冷卻毫秒；>0 才畫 CD 圓底＋秒數。 */
  cooldownMs?: Record<string, number>;
  /**
   * P1：即使沒有冷卻也要顯示為不可用的 id（MP 不足、或玩家目前不是 idle——依 engine/dispatch.ts 的
   * 實際規則，charging/casting/guarding/recovering/dead 任何一種非 idle 狀態送 USE_SKILL/USE_ITEM
   * 都會被拒絕，不只契約文字例示的「casting/guarding」兩種）。跟 cooldownMs 分開一條路徑，因為這類
   * 不可用不需要顯示秒數倒數，只需要灰階＋disabled。
   */
  unavailableIds?: Set<string>;
  selectedId?: string | null;
  onPick: (id: string) => void;
  /** 邏輯寬高（預設 390×114）；元件內一律用百分比與 --s 縮放係數，不寫死 390。 */
  width?: number;
  height?: number;
};

/** 裝備欄固定 8 格（kit 說明：裝備列固定 8 格、畫面顯示 5 格、offset 0–3）。 */
const LOADOUT_SIZE = 8;

type CellEntry = { kind: 'skill'; skill: Skill } | { kind: 'item'; item: Item } | null;

// ---- 靜態幾何（全部由常數推導，模組載入時算一次） ----
// 五格＋箭頭的 lane：kit preview #skill-lane 的 left/right 2px、top 35px、高 75px，換成托盤 390×114 的比例。
const LANE_RECT = {
  x: TRAY.lane.inset / TRAY.width,
  y: TRAY.lane.top / TRAY.height,
  w: (TRAY.width - TRAY.lane.inset * 2) / TRAY.width,
  h: TRAY.lane.height / TRAY.height,
};
// 格子 54×74：上 54×54 是金框區、下方 20% 放名稱。以下座標皆相對於「格子」本身（x 對寬 54、y 對高 74）。
const FRAME_H = TRAY.cell.frameHeightRatio; // 54/74
const ICON_RECT = {
  x: SKILL_SLOT.contentRect.x,
  y: SKILL_SLOT.contentRect.y * FRAME_H,
  w: SKILL_SLOT.contentRect.w,
  h: SKILL_SLOT.contentRect.h * FRAME_H,
};
// CD 圓底 40 邏輯 px，置中於 54 金框內。
const CD_RECT = {
  x: (SKILL_SLOT.size - SKILL_SLOT.cooldownDisc) / 2 / SKILL_SLOT.size,
  y: ((SKILL_SLOT.size - SKILL_SLOT.cooldownDisc) / 2) / TRAY.cell.h,
  w: SKILL_SLOT.cooldownDisc / SKILL_SLOT.size,
  h: SKILL_SLOT.cooldownDisc / TRAY.cell.h,
};
// 數量小標 24×15，貼金框右下角、內縮 3 邏輯 px（避免壓到金框的斜角）。
const BADGE_INSET = 3;
const BADGE_RECT = {
  x: (SKILL_SLOT.size - SKILL_SLOT.quantityBadge.w - BADGE_INSET) / SKILL_SLOT.size,
  y: (SKILL_SLOT.size - SKILL_SLOT.quantityBadge.h - BADGE_INSET) / TRAY.cell.h,
  w: SKILL_SLOT.quantityBadge.w / SKILL_SLOT.size,
  h: SKILL_SLOT.quantityBadge.h / TRAY.cell.h,
};
// 名稱列：kit 的 top 76%／高 20%，左右各外擴 4% 讓 4 字名稱（如「復甦羽毛」）不被金框寬度截掉。
const NAME_STYLE: CSSProperties = {
  top: `${TRAY.cell.nameTop * 100}%`,
  height: `${TRAY.cell.nameHeight * 100}%`,
  left: '-4%',
  width: '108%',
};

const LANE_STYLE = fracStyle(LANE_RECT);
const COUNT_STYLE = fracStyle(TRAY.count);
const ICON_STYLE = fracStyle(ICON_RECT);
const CD_STYLE = fracStyle(CD_RECT);
const BADGE_STYLE = fracStyle(BADGE_RECT);

function padTo<T>(list: readonly (T | null)[], n: number): (T | null)[] {
  const out: (T | null)[] = list.slice(0, n);
  while (out.length < n) out.push(null);
  return out;
}

function clampOffset(n: number): number {
  if (!Number.isFinite(n)) return 0;
  return Math.max(0, Math.min(TRAY.maxOffset, Math.floor(n)));
}

export default function SkillTray({
  mode,
  skills,
  items,
  offset,
  onOffset,
  cooldownMs,
  unavailableIds,
  selectedId,
  onPick,
  width = TRAY.width,
  height = TRAY.height,
}: SkillTrayProps) {
  // 等比縮放係數：取寬／高兩向的較小者，確保格子在寬 480 或托盤變矮（緊湊 104／短屏 96）時都不會溢出 lane。
  const s = Math.min(width / TRAY.width, height / TRAY.height);
  const cur = clampOffset(offset);

  const loadout: CellEntry[] =
    mode === 'skills'
      ? padTo(skills, LOADOUT_SIZE).map((sk) => (sk ? { kind: 'skill', skill: sk } : null))
      : padTo(items, LOADOUT_SIZE).map((it) => (it ? { kind: 'item', item: it } : null));
  const visible = loadout.slice(cur, cur + TRAY.visibleCells);

  const counter =
    mode === 'skills'
      ? `已裝備 ${skills.filter(Boolean).length}/${LOADOUT_SIZE}`
      : `可用 ${items.filter((it) => it.quantity > 0).length} 種`;

  const prevDisabled = cur <= 0;
  const nextDisabled = cur >= TRAY.maxOffset;

  // 顏色只走規格色票（畫面根節點固定 data-skin="default"，不得讀前台 skin 變數）；縮放後的尺寸用 CSS 變數餵給 module。
  const rootStyle = {
    width: `${width}px`,
    height: `${height}px`,
    '--s': s,
    '--c-text': PALETTE.textPrimary,
    '--c-muted': PALETTE.textSecondary,
    '--arrow-col': `${TRAY.lane.arrowCol * s}px`,
    '--arrow-w': `${TRAY.arrow.w * s}px`,
    '--arrow-h': `${TRAY.arrow.h * s}px`,
    '--arrow-hit-h': `${TRAY.arrow.hitH * s}px`,
    '--lane-gap': `${TRAY.lane.gap * s}px`,
    '--cell-w': `${TRAY.cell.w * s}px`,
    '--frame-h': `${FRAME_H * 100}%`,
    '--fs-count': `${12 * s}px`,
    '--fs-name': `${13 * s}px`,
    '--fs-cd': `${18 * s}px`,
    '--fs-badge': `${12 * s}px`,
  } as CSSProperties;

  return (
    <div
      className={styles.tray}
      style={rootStyle}
      role="group"
      aria-label={mode === 'skills' ? '技能列' : '道具列'}
      data-mode={mode}
    >
      {/* 底板（「技能」／「道具」標題已烤進圖裡） */}
      <img
        className={styles.art}
        src={kitAsset(mode === 'skills' ? 'panel_tray_skills' : 'panel_tray_items')}
        alt=""
        draggable={false}
      />

      <div className={styles.count} style={COUNT_STYLE}>
        {counter}
      </div>

      <div className={styles.lane} style={LANE_STYLE}>
        <button
          type="button"
          className={styles.arrow}
          aria-label="前一格"
          disabled={prevDisabled}
          onClick={() => onOffset(Math.max(0, cur - 1))}
        >
          <img
            src={kitAsset(prevDisabled ? 'icon_arrow_left_disabled' : 'icon_arrow_left')}
            alt=""
            draggable={false}
          />
        </button>

        {visible.map((entry, i) => (
          <TrayCell
            key={`${mode}-${cur + i}`}
            entry={entry}
            cooldownMs={entry?.kind === 'skill' ? cooldownMs?.[entry.skill.id] ?? 0 : 0}
            forcedUnavailable={!!entry && !!unavailableIds?.has(entry.kind === 'skill' ? entry.skill.id : entry.item.id)}
            selected={
              !!selectedId &&
              ((entry?.kind === 'skill' && entry.skill.id === selectedId) ||
                (entry?.kind === 'item' && entry.item.id === selectedId))
            }
            onPick={onPick}
          />
        ))}

        <button
          type="button"
          className={styles.arrow}
          aria-label="後一格"
          disabled={nextDisabled}
          onClick={() => onOffset(Math.min(TRAY.maxOffset, cur + 1))}
        >
          <img
            src={kitAsset(nextDisabled ? 'icon_arrow_right_disabled' : 'icon_arrow_right')}
            alt=""
            draggable={false}
          />
        </button>
      </div>
    </div>
  );
}

type TrayCellProps = {
  entry: CellEntry;
  cooldownMs: number;
  /** P1：MP 不足或玩家忙碌中，即使沒有冷卻也顯示不可用（見 SkillTrayProps.unavailableIds）。 */
  forcedUnavailable: boolean;
  selected: boolean;
  onPick: (id: string) => void;
};

function TrayCell({ entry, cooldownMs, forcedUnavailable, selected, onPick }: TrayCellProps) {
  // 空格：只畫 frame_skill_empty，不可點。
  if (!entry) {
    return (
      <button type="button" className={styles.cell} aria-label="空格" disabled>
        <img className={styles.frame} src={kitAsset('frame_skill_empty')} alt="" draggable={false} />
      </button>
    );
  }

  const id = entry.kind === 'skill' ? entry.skill.id : entry.item.id;
  const name = entry.kind === 'skill' ? entry.skill.name : entry.item.name;
  const iconUrl = entry.kind === 'skill' ? entry.skill.iconUrl : entry.item.iconUrl;
  const quantity = entry.kind === 'item' ? entry.item.quantity : null;
  const cooling = cooldownMs > 0;
  const depleted = quantity !== null && quantity <= 0;
  // 冷卻中／數量 0／（P1）MP 不足或玩家忙碌中皆不可點，圖示降飽和表示不可用。
  const disabled = cooling || depleted || forcedUnavailable;

  const cellClass = [
    styles.cell,
    disabled ? styles.isDimmed : '',
    selected ? styles.isSelected : '',
  ]
    .filter(Boolean)
    .join(' ');

  const ariaLabel = cooling
    ? `${name}，冷卻中 ${Math.ceil(cooldownMs / 1000)} 秒`
    : forcedUnavailable
      ? `${name}，目前無法使用`
      : quantity !== null
        ? `${name}，數量 ${quantity}`
        : name;

  return (
    <button
      type="button"
      className={cellClass}
      aria-label={ariaLabel}
      aria-pressed={selected || undefined}
      disabled={disabled}
      onClick={() => onPick(id)}
    >
      {/* 疊層由下往上：插畫 → 透明金框 → 選取覆層 → CD 圓底＋秒數 → 數量小標（kit 說明第 5 層） */}
      <img className={styles.icon} style={ICON_STYLE} src={iconUrl} alt="" draggable={false} />
      <img className={styles.frame} src={kitAsset('frame_skill_overlay')} alt="" draggable={false} />
      {selected && (
        <img className={styles.frame} src={kitAsset('overlay_skill_selected')} alt="" draggable={false} />
      )}
      {cooling && (
        <div className={styles.cooldown} style={CD_STYLE} aria-hidden="true">
          <img src={kitAsset('overlay_cooldown_disc')} alt="" draggable={false} />
          <span>{Math.ceil(cooldownMs / 1000)}s</span>
        </div>
      )}
      {quantity !== null && (
        <div className={styles.badge} style={BADGE_STYLE} aria-hidden="true">
          <img src={kitAsset('badge_quantity_empty')} alt="" draggable={false} />
          <span>{quantity}</span>
        </div>
      )}
      <div className={styles.name} style={NAME_STYLE}>
        {name}
      </div>
    </button>
  );
}
