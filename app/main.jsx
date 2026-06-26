/* DOR app shell — tab bar + stack navigation */
const { useState } = React;

const ROOT = { home: 'HomeScreen', races: 'RaceListScreen', wheel: 'MileageScreen', me: 'ProfileScreen' };
const SCREENS = {
  HomeScreen: window.HomeScreen, RaceListScreen: window.RaceListScreen, MileageScreen: window.MileageScreen,
  ProfileScreen: window.ProfileScreen, raceDetail: window.RaceDetailScreen, progress: window.ProgressScreen,
  mission: window.MissionScreen, checkin: window.CheckinScreen, wheel: window.WheelScreen,
  stickers: window.StickerScreen, records: window.RecordsScreen,
};

const TABS = [
  { key: 'home', label: '戰局', icon: 'target' },
  { key: 'races', label: '賽事', icon: 'flag' },
  { key: 'wheel', label: '里程', icon: 'wheel' },
  { key: 'me', label: '我的', icon: 'user' },
];

function DORApp() {
  const [tab, setTabState] = useState('home');
  const [stack, setStack] = useState([]); // [{screen, params}]

  const nav = (screen, params = {}) => setStack(s => [...s, { screen, params }]);
  const back = () => setStack(s => s.slice(0, -1));
  const setTab = (t) => { setStack([]); setTabState(t); };

  const onRoot = stack.length === 0;
  const current = onRoot ? { screen: ROOT[tab], params: {} } : stack[stack.length - 1];
  const Comp = SCREENS[current.screen] || (() => <div style={{ padding: 40, color: 'var(--tx-dim)' }}>—</div>);

  return (
    <div className="dor-app">
      <Comp route={current.params} nav={nav} back={back} setTab={setTab} />
      {onRoot && <TabBar tab={tab} setTab={setTab} />}
    </div>
  );
}

function TabBar({ tab, setTab }) {
  return (
    <div style={{
      position: 'absolute', left: 0, right: 0, bottom: 0, zIndex: 30,
      paddingBottom: 'calc(env(safe-area-inset-bottom) + 22px)',
      paddingTop: 9, display: 'flex',
      background: 'linear-gradient(180deg, rgba(13,15,20,0), rgba(9,11,15,.96) 30%)',
      backdropFilter: 'blur(14px)',
      borderTop: '1px solid var(--line)',
    }}>
      {TABS.map(t => {
        const on = tab === t.key;
        return (
          <button key={t.key} className="tap" onClick={() => setTab(t.key)} style={{
            flex: 1, background: 'none', border: 'none', display: 'flex', flexDirection: 'column',
            alignItems: 'center', gap: 4, padding: '4px 0', color: on ? 'var(--fug)' : 'var(--tx-faint)',
          }}>
            <Icon name={t.icon} size={24} color={on ? 'var(--fug)' : 'var(--tx-faint)'} />
            <span style={{ fontSize: 10.5, fontWeight: on ? 700 : 500, fontFamily: 'var(--font-disp)' }}>{t.label}</span>
          </button>
        );
      })}
    </div>
  );
}

window.DORApp = DORApp;

/* mount inside the iOS device frame */
function DORPhone() {
  return (
    <window.IOSDevice dark width={390} height={844}>
      <DORApp />
    </window.IOSDevice>
  );
}
window.DORPhone = DORPhone;
