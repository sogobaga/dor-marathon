'use client';

// DORPG 飄字（P1）：隊員卡「內部」由中段往上飄一小段顯示傷害／治療／護盾／未命中的瞬時回饋，
// 700ms 後自動消失（2026-09-14 審查修正：原本飄到卡片外會蓋住頂欄的 DORPG 標題，見 .module.css）。
// 消失時機由這裡的 setTimeout 控制，不依賴 CSS animationend——BattleScreen 的「減少動態」設定會對整棵樹
// 套用 animation:none!important（見 BattleScreen.module.css .root[data-reduce-motion]），若消失也靠 CSS
// 動畫結尾觸發，開了減少動態就會卡在畫面上飄字不散；改用 JS 計時器就不受那條全域規則影響。
// 要重播同一句飄字時，呼叫端只需換一個新的 `key`（見 PartyCardProps.floatText.key）讓 React 整個重新掛載本
// 元件，這個 effect 自然重跑，元件本身不必額外偵測 props 是否「看起來一樣」。
import { useEffect, useState } from 'react';
import { PALETTE } from '@/lib/dorpg/assets';
import styles from './FloatText.module.css';

export type FloatTextTone = 'damage' | 'heal' | 'shield' | 'miss';

export type FloatTextProps = {
  text: string;
  tone: FloatTextTone;
  /** 減少動態：不做位移／淡出動畫，700ms 到即直接消失（OS 層級的 prefers-reduced-motion 已由 CSS 處理，
   * 這個 prop 給呼叫端在需要依「app 內設定」而非系統設定強制切換時使用）。 */
  reducedMotion?: boolean;
};

const DURATION_MS = 700;

/** 四種語氣對應色票；miss 沒有專屬色票，借 textSecondary（灰）。 */
const TONE_COLOR: Record<FloatTextTone, string> = {
  damage: PALETTE.hpEnemy,
  heal: PALETTE.hpNormal,
  shield: PALETTE.borderGold,
  miss: PALETTE.textSecondary,
};

export default function FloatText({ text, tone, reducedMotion = false }: FloatTextProps) {
  const [visible, setVisible] = useState(true);

  useEffect(() => {
    const id = window.setTimeout(() => setVisible(false), DURATION_MS);
    return () => window.clearTimeout(id);
  }, []);

  if (!visible) return null;

  return (
    <div
      className={reducedMotion ? `${styles.text} ${styles.reduced}` : styles.text}
      style={{ color: TONE_COLOR[tone] }}
      aria-hidden="true"
    >
      {text}
    </div>
  );
}
