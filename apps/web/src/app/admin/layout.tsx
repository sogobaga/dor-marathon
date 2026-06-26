export default function AdminLayout({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        height: '100vh',
        overflowY: 'auto',
        background: 'var(--bg)',
        color: 'var(--tx)',
      }}
    >
      {children}
    </div>
  )
}
