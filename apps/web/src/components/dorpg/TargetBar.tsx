// DORPG 目標列（32px）：panel_target_empty 底圖已烤入「目標」字樣＋準星，這裡只在 TARGET_PANEL.contentRect
// （底圖 x=63 起、寬 300）內畫「{完整名稱} Lv.{level}」：名稱可縮、單行省略號；Lv 永不被截（規格書表 11）。
// 純展示元件、無事件，server / client 兩邊都可渲染。
import { PALETTE, TARGET_PANEL, fracStyle, kitAsset } from '@/lib/dorpg/assets';
import styles from './TargetBar.module.css';

export type TargetBarProps = {
  target: { name: string; level: number } | null;
  /** 邏輯高 px，預設 32；寬度跟隨容器（W−12），字級以 32 高為基準等比。 */
  height?: number;
};

/** 規格書表 5：目標列 14px（32 高時）。 */
const FONT_PX = 14;

export default function TargetBar({ target, height = TARGET_PANEL.h }: TargetBarProps) {
  const scale = height / TARGET_PANEL.h;
  const text = target ? `${target.name} Lv.${target.level}` : '';

  return (
    // role=status：換目標時輔助技術會播報「目標 ○○ Lv.n」；title 讓長名稱被省略時仍可懸停看全名
    <div className={styles.bar} style={{ height, color: PALETTE.textPrimary }} role="status" title={text || undefined}>
      {/* 底圖 object-fit:fill：邏輯 378×32、容器寬 = W−12。W=390 時零變形；480 封頂時橫向拉 24%
          （manifest 標 uniform、nineSlice null，任意寬需改 frame_text_long 九宮格＋label_target，P0 先接受）。 */}
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img className={styles.art} src={kitAsset('panel_target_empty')} alt="" draggable={false} />
      {target ? (
        <div className={styles.content} style={{ ...fracStyle(TARGET_PANEL.contentRect), fontSize: FONT_PX * scale }}>
          {/* 「目標」二字在底圖裡，對輔助技術補一份看不見的文字 */}
          <span className={styles.srOnly}>目標 </span>
          <span className={styles.name}>{target.name}</span>
          <span className={styles.level}>Lv.{target.level}</span>
        </div>
      ) : null}
    </div>
  );
}
