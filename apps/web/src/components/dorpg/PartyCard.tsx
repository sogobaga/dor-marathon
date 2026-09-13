'use client';

// DORPG 隊員卡（P0 靜態畫面）：panel_party_empty 底圖 → 頭像 → 姓名/Lv/HP/MP 文字＋血條 → 選取/瀕死/死亡覆層。
// 所有子層都用 PARTY_SLOTS 的百分比絕對定位、字級再乘 (width/72)，因此換任何卡寬都整體等比，不必碰 3× 的 PNG 像素。
// 「HP」「MP」兩個標籤已烤進底圖（kit 說明），這裡只畫動態欄位；member 為 null 時只留底圖、不畫 0。
import type { CSSProperties } from 'react';
import type { PartyMember } from '@/lib/dorpg/types';
import { ANIM, CRITICAL_RATIO, PALETTE, PARTY_SLOTS, barClipPath, fracStyle, kitAsset } from '@/lib/dorpg/assets';
import styles from './PartyCard.module.css';

export type PartyCardProps = {
  member: PartyMember | null;
  selected?: boolean;
  onSelect?: () => void;
  /** 邏輯寬 px（高固定為 2 倍）；預設 72 = panel_party_empty 的邏輯尺寸。 */
  width?: number;
};

/** 規格書表5（W390 基準）：姓名 14/600、Lv 12/500、HP/MP 主數值 18/700；實際字級再乘 width/72。 */
const FONT = { name: 14, level: 12, value: 18 } as const;

/** HP/MP 實值插槽只有 41 邏輯 px 寬；≥4 位數逐級縮字避免截斷（kit 範例同樣依位數縮小）。 */
function valueFontPx(text: string): number {
  if (text.length >= 5) return 12;
  if (text.length === 4) return 15;
  return FONT.value;
}

export default function PartyCard({ member, selected = false, onSelect, width = PARTY_SLOTS.w }: PartyCardProps) {
  const scale = width / PARTY_SLOTS.w;
  const dead = member !== null && member.hp <= 0;
  // 瀕死門檻 0 < hp/hpMax ≤ 0.2（kit 說明）；hp = 0 走死亡態，不再算瀕死。
  const critical = member !== null && !dead && member.hpMax > 0 && member.hp / member.hpMax <= CRITICAL_RATIO;
  const hpText = member ? String(Math.max(0, member.hp)) : '';
  const mpText = member ? String(Math.max(0, member.mp)) : '';

  const ariaLabel = member
    ? `${member.name}，等級 ${member.level}，HP ${hpText}／${member.hpMax}，MP ${mpText}／${member.mpMax}${dead ? '，已倒下' : critical ? '，瀕死' : ''}`
    : '空白隊員欄位';

  // 顏色只從 assets.ts 的 PALETTE 來（畫面固定暗色，不讀前台 skin 變數）；focus 環顏色以自訂變數交給 CSS。
  const rootStyle = {
    width,
    height: width * 2,
    color: PALETTE.textPrimary,
    '--dorpg-focus': PALETTE.targetGold,
  } as CSSProperties;

  return (
    <button
      type="button"
      className={styles.card}
      style={rootStyle}
      onClick={onSelect}
      disabled={!member}
      aria-label={ariaLabel}
      aria-pressed={member ? selected : undefined}
    >
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img className={styles.art} src={kitAsset('panel_party_empty')} alt="" draggable={false} />

      {member?.portraitUrl ? (
        // eslint-disable-next-line @next/next/no-img-element
        <img
          className={dead ? `${styles.portrait} ${styles.portraitDead}` : styles.portrait}
          style={fracStyle(PARTY_SLOTS.portrait)}
          src={member.portraitUrl}
          alt=""
          draggable={false}
        />
      ) : null}

      {member ? (
        <>
          <div className={`${styles.field} ${styles.name}`} style={{ ...fracStyle(PARTY_SLOTS.name), fontSize: FONT.name * scale }}>
            {member.name}
          </div>
          <div className={`${styles.field} ${styles.level}`} style={{ ...fracStyle(PARTY_SLOTS.level), fontSize: FONT.level * scale }}>
            Lv.{member.level}
          </div>
          <div
            className={`${styles.field} ${styles.value}`}
            style={{ ...fracStyle(PARTY_SLOTS.hp), fontSize: valueFontPx(hpText) * scale, color: critical ? PALETTE.hpCritical : undefined }}
          >
            {hpText}
          </div>
          <div className={`${styles.field} ${styles.value}`} style={{ ...fracStyle(PARTY_SLOTS.mp), fontSize: valueFontPx(mpText) * scale }}>
            {mpText}
          </div>
          {/* 血條：填色圖鋪滿插槽、只用 clip-path 裁右側（kit：不要替每個百分比出圖）。hp = 0 時 barClipPath 自然裁掉 100% ＝ 空槽。 */}
          <div className={styles.bar} style={fracStyle(PARTY_SLOTS.hpFill)}>
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              className={styles.fill}
              src={kitAsset(critical ? 'bar_fill_hp_critical' : 'bar_fill_hp_green')}
              style={{ clipPath: barClipPath(member.hp, member.hpMax) }}
              alt=""
              draggable={false}
            />
          </div>
          <div className={styles.bar} style={fracStyle(PARTY_SLOTS.mpFill)}>
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              className={styles.fill}
              src={kitAsset('bar_fill_mp_blue')}
              style={{ clipPath: barClipPath(member.mp, member.mpMax) }}
              alt=""
              draggable={false}
            />
          </div>
        </>
      ) : null}

      {/* 狀態覆層（純疊圖、pointer-events:none）：選取金框 → 瀕死紅光（1200ms 脈動）→ 死亡灰幕＋「倒下」。 */}
      {member && selected ? (
        // eslint-disable-next-line @next/next/no-img-element
        <img className={styles.art} src={kitAsset('overlay_party_selected')} alt="" draggable={false} />
      ) : null}
      {critical ? (
        // eslint-disable-next-line @next/next/no-img-element
        <img
          className={`${styles.art} ${styles.critical}`}
          style={{ animationDuration: `${ANIM.criticalPulseMs}ms` }}
          src={kitAsset('overlay_party_critical')}
          alt=""
          draggable={false}
        />
      ) : null}
      {dead ? (
        <>
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img className={styles.art} src={kitAsset('overlay_party_dead')} alt="" draggable={false} />
          {/* 「倒下」置於頭像插槽正中；aria-label 已含「已倒下」，故對輔助技術隱藏避免重複播報 */}
          <div className={styles.down} style={{ ...fracStyle(PARTY_SLOTS.portrait), fontSize: FONT.level * scale }} aria-hidden="true">
            <span style={{ background: PALETTE.overlayScrim }}>倒下</span>
          </div>
        </>
      ) : null}
    </button>
  );
}
