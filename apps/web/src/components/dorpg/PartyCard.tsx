'use client';

// DORPG 隊員卡（P0 靜態畫面＋P1 手勢/飄字）：panel_party_empty 底圖 → 頭像 → 姓名/Lv/HP/MP 文字＋血條
// → 選取/瀕死/死亡覆層 → （P1）可選為目標的金框脈動、飄字。
// 所有子層都用 PARTY_SLOTS 的百分比絕對定位、字級再乘 (width/72)，因此換任何卡寬都整體等比，不必碰 3× 的 PNG 像素。
// 「HP」「MP」兩個標籤已烤進底圖（kit 說明），這裡只畫動態欄位；member 為 null 時只留底圖、不畫 0。
//
// P1 新增一層外層 <div>（非 overflow:hidden）包住原本的 <button>：FloatText 要飄在「卡片上方」，也就是卡片
// 外面，但 .card 本身有 overflow:hidden（讓底圖/頭像/覆層四個角落乾淨地被裁掉），飄字若還是 .card 的子層
// 就會被一起裁掉、永遠看不到。外層 wrap 尺寸與原本 button 完全相同，不影響既有版面（BattleScreen 的隊伍列
// 只把 PartyCard 當一顆固定寬高的方塊排，不在意它內部是不是恰好只有一個 <button>）。
import type { CSSProperties } from 'react';
import type { BuffDebuffStat, PartyMember } from '@/lib/dorpg/types';
import { ANIM, CRITICAL_RATIO, PALETTE, PARTY_SLOTS, barClipPath, fracStyle, kitAsset } from '@/lib/dorpg/assets';
import { skillStatLabel } from '@/lib/rpgMeta';
import FloatText, { type FloatTextTone } from './FloatText';
import styles from './PartyCard.module.css';

export type PartyCardProps = {
  member: PartyMember | null;
  selected?: boolean;
  onSelect?: () => void;
  /** 邏輯寬 px（高固定為 2 倍）；預設 72 = panel_party_empty 的邏輯尺寸。 */
  width?: number;
  /** P1：處於 chooseAlly 選目標模式——金框脈動＋可點（reduced-motion 時脈動改常駐靜態）。 */
  targetable?: boolean;
  /** P1：targetable 時點卡片觸發（取代 onSelect，選目標與瀏覽隊員是兩件事）。 */
  onPick?: () => void;
  /** P1：掛在卡片上方的飄字（傷害／治療／護盾／未命中）；key 換新值即重播同一句文字。 */
  floatText?: { text: string; tone: FloatTextTone; key: number };
  /** P5 POLISH：目前生效中的 buff/debuff（engine PartyActor.activeEffects 的精簡投影，見
   *  BattleScreen.tsx 的 partyStatusTags）；只顯示、不做任何戰鬥判斷。未給或空陣列＝不畫標籤列。 */
  statusTags?: { stat: BuffDebuffStat; value: number }[];
  /**
   * DORPG P10（CONTRACT §3/§5）：這位隊員目前是否處於「守護狀態」（tauntUntil 尚未到期，或正在
   * 按防禦且學過守護本能）——敵人本回合只會攻擊這個角色。跟 statusTags 一樣純顯示投影，不做任何
   * 戰鬥判斷（BattleScreen 用 inGuardianState 的邏輯投影出這個布林值，見 partyGuardian）。
   */
  guardian?: boolean;
};

/** 規格書表5（W390 基準）：姓名 14/600、Lv 12/500、HP/MP 主數值 18/700；實際字級再乘 width/72。 */
const FONT = { name: 14, level: 12, value: 18 } as const;

/** HP/MP 實值插槽只有 41 邏輯 px 寬；≥4 位數逐級縮字避免截斷（kit 範例同樣依位數縮小）。 */
function valueFontPx(text: string): number {
  if (text.length >= 5) return 12;
  if (text.length === 4) return 15;
  return FONT.value;
}

export default function PartyCard({
  member,
  selected = false,
  onSelect,
  width = PARTY_SLOTS.w,
  targetable = false,
  onPick,
  floatText,
  statusTags,
  guardian = false,
}: PartyCardProps) {
  const scale = width / PARTY_SLOTS.w;
  // DORPG P6（契約 §1：HP/MP 出現小數務必整數）：引擎/bootstrap 理論上已經整數化，這裡是顯示層
  // 最後一道保險（Math.floor，向下取整同引擎規則）——即使上游哪天不小心漏 floor，畫面也絕不出現
  // 小數點。hpMax/mpMax 同樣整數化，血條 clip-path 與 aria-label 都改吃這幾個整數變數。
  const hp = member ? Math.floor(member.hp) : 0;
  const hpMax = member ? Math.floor(member.hpMax) : 0;
  const mp = member ? Math.floor(member.mp) : 0;
  const mpMax = member ? Math.floor(member.mpMax) : 0;
  const dead = member !== null && hp <= 0;
  // 瀕死門檻 0 < hp/hpMax ≤ 0.2（kit 說明）；hp = 0 走死亡態，不再算瀕死。
  const critical = member !== null && !dead && hpMax > 0 && hp / hpMax <= CRITICAL_RATIO;
  const hpText = member ? String(Math.max(0, hp)) : '';
  const mpText = member ? String(Math.max(0, mp)) : '';

  const ariaLabel = member
    ? `${member.name}，等級 ${member.level}，HP ${hpText}／${hpMax}，MP ${mpText}／${mpMax}${dead ? '，已倒下' : critical ? '，瀕死' : ''}${targetable ? '，可選為目標' : ''}`
    : '空白隊員欄位';

  // targetable 時點卡片＝選目標（onPick），不是瀏覽隊員（onSelect）；沒給 onPick 就退回 onSelect，
  // 讓只加了 targetable 忘記接 onPick 的呼叫端至少還有反應，而不是點了完全沒事。
  const handleClick = targetable ? (onPick ?? onSelect) : onSelect;

  // 顏色只從 assets.ts 的 PALETTE 來（畫面固定暗色，不讀前台 skin 變數）；focus 環顏色、可選目標金框顏色
  // 都以自訂變數交給 CSS。rootStyle 留在 button 上（不是外層 wrap），因為 :focus-visible 與 --dorpg-* 都是
  // 給 button 自己與其內部覆層用的。
  const rootStyle = {
    width,
    height: width * 2,
    color: PALETTE.textPrimary,
    '--dorpg-focus': PALETTE.targetGold,
    '--dorpg-target-gold': PALETTE.targetGold,
  } as CSSProperties;

  return (
    <div className={styles.wrap} style={{ width, height: width * 2 }}>
      <button
        type="button"
        className={styles.card}
        style={rootStyle}
        onClick={handleClick}
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
                style={{ clipPath: barClipPath(hp, hpMax) }}
                alt=""
                draggable={false}
              />
            </div>
            <div className={styles.bar} style={fracStyle(PARTY_SLOTS.mpFill)}>
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img
                className={styles.fill}
                src={kitAsset('bar_fill_mp_blue')}
                style={{ clipPath: barClipPath(mp, mpMax) }}
                alt=""
                draggable={false}
              />
            </div>

            {/* P5 POLISH：buff/debuff 標籤列——最多顯示 3 個，超出以 +N 表示（任務規格）。貼在卡片
                頂端、疊在頭像插槽之上；深底淺字不需要為每個 tag 分色，箭頭（↑增益／↓減益，看 value
                正負，跟 buff/debuff 分類是兩件事——見 skillStatLabel 呼叫處）已經足夠表達方向。
                一整行用 overflow:hidden + ellipsis 兜底，不會因為中文字數不同而撐破卡寬。 */}
            {/* DORPG P10（CONTRACT §5）：守護狀態標籤跟 buff/debuff 共用同一個標籤列容器（單行
                ellipsis），文字上金色凸顯、排在最前面——守護是「敵人只打這個人」的關鍵戰場資訊，
                不該被 +N 折疊掉，故不佔用 statusTags 的 3 個顯示額度，獨立疊在最前面。 */}
            {guardian || (statusTags && statusTags.length > 0) ? (
              <div className={styles.tags} style={{ fontSize: 9 * scale }}>
                {guardian ? <span style={{ color: PALETTE.targetGold }}>守護</span> : null}
                {guardian && statusTags && statusTags.length > 0 ? ' ' : ''}
                {statusTags && statusTags.length > 0
                  ? statusTags
                      .slice(0, 3)
                      .map((t) => `${t.value >= 0 ? '↑' : '↓'}${skillStatLabel(t.stat)}`)
                      .join(' ') + (statusTags.length > 3 ? ` +${statusTags.length - 3}` : '')
                  : null}
              </div>
            ) : null}
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

        {/* 可選為目標（chooseAlly）：金框脈動。kit 資產清單沒有專屬覆層圖（本工作者只拿得到 frame_dialog／
            button_confirm／bar_fill_charge_gold／overlay_cooldown_disc 四張），改用 CSS box-shadow 模擬金框，
            週期沿用瀕死脈動同一個 1200ms（ANIM.criticalPulseMs），reduced-motion 時常駐不脈動。 */}
        {member && targetable ? <div className={styles.targetableGlow} aria-hidden="true" /> : null}
      </button>

      {/* FloatText 必須是 wrap 的子層、button 的手足層級，否則會被 .card 的 overflow:hidden 裁掉
          （見檔頭註解）。key 換新值時 React 整個重新掛載 FloatText，動畫／700ms 計時器自然重播。 */}
      {floatText ? <FloatText key={floatText.key} text={floatText.text} tone={floatText.tone} /> : null}
    </div>
  );
}
