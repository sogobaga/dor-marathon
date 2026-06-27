'use client'

import RaceForm from './RaceForm'

export default function NewRaceModal({
  token,
  onClose,
  onCreated,
}: {
  token: string
  onClose: () => void
  onCreated: () => void
}) {
  return (
    <div style={overlay} onClick={onClose}>
      <div style={panel} onClick={(e) => e.stopPropagation()}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 14 }}>
          <h2 style={{ fontSize: 20, fontWeight: 800, margin: 0 }}>新增賽事</h2>
          <button onClick={onClose} style={iconBtn}>✕</button>
        </div>
        <div style={{ overflowY: 'auto', paddingRight: 4 }}>
          <RaceForm token={token} onCancel={onClose} onDone={() => onCreated()} />
        </div>
      </div>
    </div>
  )
}

const overlay: React.CSSProperties = {
  position: 'fixed', inset: 0, background: 'rgba(0,0,0,.55)',
  display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 50, padding: 20,
}
const panel: React.CSSProperties = {
  background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 16,
  padding: 22, width: '100%', maxWidth: 720, maxHeight: '90vh', overflow: 'hidden',
  display: 'flex', flexDirection: 'column',
}
const iconBtn: React.CSSProperties = {
  background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 18,
}
