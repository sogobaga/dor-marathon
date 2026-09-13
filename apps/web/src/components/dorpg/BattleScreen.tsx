'use client';

// DORPG P0 靜態戰鬥畫面的組裝層：把 TopBar／PartyCard×5／BattleStage／TargetBar／SkillTray／CommandBar
// 依規格書 §3 表 3 的五個固定帶高垂直疊起來，場景吃剩餘高度。只有本地 UI 狀態（選取隊員／目標、托盤模式、
// 道具數量、按鈕按壓、示範用冷卻倒數、設定面板），不接 API／DB——正式戰鬥引擎接上時整份狀態換成 store 即可。
// 尺寸策略：自己量測父層（ResizeObserver）→ 寬以 480 封頂置中、高決定版面級別（standard/compact/short）；
// 高 < 520 或寬 < 360 時場景維持最小 120 高、整欄改可垂直捲動（規格：保留最小容器並可捲動）。
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from 'react';
import type { BattleSample, BtnState, Item, TrayMode } from '@/lib/dorpg/types';
import { KIT_SIZES, LAYOUT, PALETTE, kitAsset, layoutFor, nineSliceStyle, sceneHeightFor } from '@/lib/dorpg/assets';
import { SAMPLE_BATTLE } from '@/lib/dorpg/sampleBattle';
import TopBar from './TopBar';
import PartyCard from './PartyCard';
import BattleStage from './BattleStage';
import TargetBar from './TargetBar';
import SkillTray from './SkillTray';
import CommandBar from './CommandBar';
import styles from './BattleScreen.module.css';

export type BattleScreenProps = {
  onBack: () => void;
  /** 未給時用 SAMPLE_BATTLE（kit 測試資料＋content pack 素材）。 */
  sample?: BattleSample;
};

/** 隊伍列：左右留 6、卡距 4、五張（規格表 7）。 */
const PARTY_PAD_X = 6;
const PARTY_GAP = 4;
const PARTY_COUNT = 5;
/** 目標列寬 = W − 12（規格表 11、kit preview #target-row）。 */
const TARGET_INSET = 12;
/** 場景最小高：再矮就讓整欄捲動，不把怪物壓扁。 */
const MIN_STAGE_H = 120;
/** 規格：H<520 或 W<360 保留最小容器並可捲動。 */
const SCROLL_MIN_H = 520;
const SCROLL_MIN_W = 360;
/** icon_close 邏輯 44×44（manifest；KIT_SIZES 未列，與 icon_settings 同尺寸）。 */
const ICON_CLOSE = 44;

/** P0 示範冷卻：讓火球／冰槍兩格像組裝預覽圖一樣有 CD 圓底，掛載後以 rAF 本機倒數、到 0 就停在 0。 */
const DEMO_COOLDOWNS: Record<string, number> = { fireball: 12000, ice_lance: 6000 };

type SettingsValues = { music: number; sfx: number; vibrate: boolean; reduceMotion: boolean };
const DEFAULT_SETTINGS: SettingsValues = { music: 80, sfx: 80, vibrate: true, reduceMotion: false };

export default function BattleScreen({ onBack, sample = SAMPLE_BATTLE }: BattleScreenProps) {
  // ---- 量測自身尺寸（clientWidth/Height 是版面 px，不受桌機 .phone-shell 的 transform 影響） ----
  const rootRef = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState<{ w: number; h: number }>({ w: 0, h: 0 });
  useLayoutEffect(() => {
    const el = rootRef.current;
    if (!el) return;
    const measure = () => {
      const w = Math.min(LAYOUT.maxWidth, Math.round(el.clientWidth));
      const h = Math.round(el.clientHeight);
      setSize((prev) => (prev.w === w && prev.h === h ? prev : { w, h }));
    };
    measure();
    if (typeof ResizeObserver === 'undefined') {
      window.addEventListener('resize', measure);
      return () => window.removeEventListener('resize', measure);
    }
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // ---- 本地 UI 狀態 ----
  const [memberIdx, setMemberIdx] = useState(0);
  const [targetId, setTargetId] = useState<string | null>(sample.initialTargetId);
  const [trayMode, setTrayMode] = useState<TrayMode>('skills');
  const [trayOffset, setTrayOffset] = useState(0);
  const [selectedSkillId, setSelectedSkillId] = useState<string | null>(null);
  // 道具數量要能在本機遞減，故複製一份而不直接用 props。
  const [items, setItems] = useState<Item[]>(() => sample.items.map((it) => ({ ...it })));
  const [guardHeld, setGuardHeld] = useState(false);
  const [attackHeld, setAttackHeld] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
  const [settings, setSettings] = useState<SettingsValues>(DEFAULT_SETTINGS);
  const [cooldownMs, setCooldownMs] = useState<Record<string, number>>(() => ({ ...DEMO_COOLDOWNS }));

  // 示範冷卻倒數：每格只在「顯示秒數」變動時才 setState（SkillTray 顯示 ceil(ms/1000)），避免 60fps 重繪整個畫面。
  useEffect(() => {
    const ids = Object.keys(DEMO_COOLDOWNS).filter((id) => DEMO_COOLDOWNS[id] > 0);
    if (ids.length === 0) return;
    const start = performance.now();
    let raf = 0;
    let lastKey = '';
    const tick = (now: number) => {
      const elapsed = now - start;
      const next: Record<string, number> = {};
      let anyLeft = false;
      for (const id of ids) {
        const rem = Math.max(0, DEMO_COOLDOWNS[id] - elapsed);
        next[id] = rem;
        if (rem > 0) anyLeft = true;
      }
      const key = ids.map((id) => Math.ceil(next[id] / 1000)).join(',');
      if (key !== lastKey) {
        lastKey = key;
        setCooldownMs(next);
      }
      // 全部歸 0 就不再排程：值停在 0，SkillTray 對 0 不畫 CD 覆層。
      if (anyLeft) raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, []);

  // ---- 版面 ----
  const measured = size.w > 0 && size.h > 0;
  const w = size.w;
  const h = size.h;
  const tier = layoutFor(h);
  const bands = LAYOUT[tier];
  const stageH = Math.max(MIN_STAGE_H, sceneHeightFor(h, tier));
  const scrollable = h < SCROLL_MIN_H || w < SCROLL_MIN_W;
  // 卡寬 = (W − 12 − 4×4)/5，再以帶高/2 封頂：卡片素材固定 1:2，W390 標準版 72.4 會比 144 帶高多 0.8px、
  // 緊湊／短屏帶高 128／120 時更會溢出到場景，故取兩者較小者（W390 標準 → 72×144，與 kit 預覽一致）。
  const cardW = Math.min((w - PARTY_PAD_X * 2 - PARTY_GAP * (PARTY_COUNT - 1)) / PARTY_COUNT, bands.party / 2);

  const target = useMemo(() => sample.enemies.find((e) => e.id === targetId) ?? null, [sample.enemies, targetId]);

  // ---- 事件 ----
  const toggleItems = () => {
    setTrayMode((m) => (m === 'items' ? 'skills' : 'items'));
    setTrayOffset(0);
  };
  const onPick = (id: string) => {
    if (trayMode === 'skills') {
      setSelectedSkillId(id);
      return;
    }
    // 道具：本機扣 1 並回技能列（規格：成功回技能列）。P0 沒有效果結算。
    setItems((prev) => prev.map((it) => (it.id === id ? { ...it, quantity: Math.max(0, it.quantity - 1) } : it)));
    setTrayMode('skills');
    setTrayOffset(0);
    setSelectedSkillId(null);
  };

  const itemState: BtnState = trayMode === 'items' ? 'active' : 'normal';
  const guardState: BtnState = guardHeld ? 'pressed' : 'normal';
  const attackState: BtnState = attackHeld ? 'pressed' : 'normal';

  // 顏色只從 PALETTE 來（根節點固定 data-skin="default" 暗色，不讀前台 skin 變數）；CSS 只透過這些自訂變數取色。
  const rootStyle = {
    background: PALETTE.surfaceBase,
    color: PALETTE.textPrimary,
    '--c-gold': PALETTE.borderGold,
    '--c-panel': PALETTE.surfacePanel,
    '--c-text': PALETTE.textPrimary,
    '--c-muted': PALETTE.textSecondary,
    '--c-focus': PALETTE.targetGold,
    '--c-disabled': PALETTE.disabledText,
  } as CSSProperties;

  return (
    <div
      ref={rootRef}
      className={styles.root}
      style={rootStyle}
      data-skin="default"
      data-reduce-motion={settings.reduceMotion ? 'true' : undefined}
    >
      {measured && (
        <div className={scrollable ? `${styles.column} ${styles.scrollable}` : styles.column} style={{ width: w }}>
          <div className={styles.band}>
            <TopBar escapeState="normal" onEscape={onBack} onSettings={() => setShowSettings(true)} height={bands.toolbar} />
          </div>

          <div
            className={`${styles.band} ${styles.party}`}
            style={{ height: bands.party, padding: `0 ${PARTY_PAD_X}px`, gap: PARTY_GAP }}
            role="group"
            aria-label="隊伍"
          >
            {Array.from({ length: PARTY_COUNT }, (_, i) => (
              <PartyCard
                key={sample.party[i]?.id ?? `empty-${i}`}
                member={sample.party[i] ?? null}
                selected={i === memberIdx}
                onSelect={() => setMemberIdx(i)}
                width={cardW}
              />
            ))}
          </div>

          <div className={styles.band} style={{ height: stageH }}>
            <BattleStage scene={sample.scene} enemies={sample.enemies} targetId={targetId} onSelect={setTargetId} width={w} height={stageH} />
          </div>

          <div className={`${styles.band} ${styles.targetRow}`} style={{ height: bands.target }}>
            <div style={{ width: w - TARGET_INSET }}>
              <TargetBar target={target ? { name: target.name, level: target.level } : null} height={bands.target} />
            </div>
          </div>

          <div className={styles.band} style={{ height: bands.tray }}>
            <SkillTray
              mode={trayMode}
              skills={sample.skills}
              items={items}
              offset={trayOffset}
              onOffset={setTrayOffset}
              cooldownMs={cooldownMs}
              selectedId={trayMode === 'skills' ? selectedSkillId : null}
              onPick={onPick}
              width={w}
              height={bands.tray}
            />
          </div>

          <div className={styles.band} style={{ height: bands.commands }}>
            <CommandBar
              item={itemState}
              guard={guardState}
              attack={attackState}
              onItem={toggleItems}
              onGuardDown={() => setGuardHeld(true)}
              onGuardUp={() => setGuardHeld(false)}
              onAttackDown={() => setAttackHeld(true)}
              onAttackUp={() => setAttackHeld(false)}
              width={w}
              height={bands.commands}
            />
          </div>
        </div>
      )}

      {measured && showSettings && (
        <SettingsSheet width={w} values={settings} onChange={setSettings} onClose={() => setShowSettings(false)} />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// 設定面板（P0 佔位）：底部抽屜，frame_dialog 九宮格當面板底；音樂／音效 0–100 滑桿、震動／減少動態開關、關閉。
// 全部只是本機狀態（規格第 14 章：開啟時戰鬥不暫停；正式版再接偏好儲存）。
// ---------------------------------------------------------------------------
type SettingsSheetProps = {
  width: number;
  values: SettingsValues;
  onChange: (next: SettingsValues) => void;
  onClose: () => void;
};

function SettingsSheet({ width, values, onChange, onClose }: SettingsSheetProps) {
  // Esc 關閉（桌機預覽方便）；點遮罩亦關閉。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const set = <K extends keyof SettingsValues>(key: K, value: SettingsValues[K]) => onChange({ ...values, [key]: value });
  const divider = KIT_SIZES.ornament_divider;

  return (
    <div className={styles.overlay}>
      <button type="button" className={styles.scrim} style={{ background: PALETTE.overlayScrim }} aria-label="關閉設定" onClick={onClose} />
      <div className={styles.sheet} style={{ width }} role="dialog" aria-modal="true" aria-labelledby="dorpg-settings-title">
        {/* frame_dialog 九宮格：border 16 + padding 4 = manifest contentRect 內縮 20 */}
        <div className={styles.sheetPanel} style={nineSliceStyle('frame_dialog')}>
          <div className={styles.sheetHead}>
            <h2 id="dorpg-settings-title" className={styles.sheetTitle}>
              設定
            </h2>
            <button type="button" className={styles.iconBtn} style={{ width: ICON_CLOSE, height: ICON_CLOSE }} onClick={onClose} aria-label="關閉">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={kitAsset('icon_close')} width={ICON_CLOSE} height={ICON_CLOSE} alt="" draggable={false} />
            </button>
          </div>
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img className={styles.divider} src={kitAsset('ornament_divider')} width={divider.w} height={divider.h} alt="" draggable={false} />

          <label className={styles.row}>
            <span className={styles.rowLabel}>音樂</span>
            <input
              className={styles.slider}
              type="range"
              min={0}
              max={100}
              value={values.music}
              onChange={(e) => set('music', Number(e.target.value))}
              style={{ accentColor: PALETTE.borderGold }}
            />
            <output className={styles.rowValue}>{values.music}</output>
          </label>
          <label className={styles.row}>
            <span className={styles.rowLabel}>音效</span>
            <input
              className={styles.slider}
              type="range"
              min={0}
              max={100}
              value={values.sfx}
              onChange={(e) => set('sfx', Number(e.target.value))}
              style={{ accentColor: PALETTE.borderGold }}
            />
            <output className={styles.rowValue}>{values.sfx}</output>
          </label>
          <div className={styles.row}>
            <span id="dorpg-set-vibrate" className={styles.rowLabel}>
              震動
            </span>
            <Switch on={values.vibrate} labelledBy="dorpg-set-vibrate" onToggle={() => set('vibrate', !values.vibrate)} />
          </div>
          <div className={styles.row}>
            <span id="dorpg-set-motion" className={styles.rowLabel}>
              減少動態
            </span>
            <Switch on={values.reduceMotion} labelledBy="dorpg-set-motion" onToggle={() => set('reduceMotion', !values.reduceMotion)} />
          </div>

          <button type="button" className={styles.closeText} onClick={onClose}>
            關閉
          </button>
          <p className={styles.note}>P0 佔位：設定只存在本機畫面狀態，不會儲存。</p>
        </div>
      </div>
    </div>
  );
}

function Switch({ on, labelledBy, onToggle }: { on: boolean; labelledBy: string; onToggle: () => void }) {
  return (
    <button type="button" role="switch" aria-checked={on} aria-labelledby={labelledBy} className={styles.switch} data-on={on ? 'true' : undefined} onClick={onToggle}>
      <span className={styles.knob} />
    </button>
  );
}
