// DOR Web Push Service Worker（純 JS、非 module）。由 PushToggle.tsx 在使用者開啟通知時註冊（/sw.js）。
// 收到推播 payload：{ title, body, url?, icon? }（見後端 /admin/push/broadcast）

self.addEventListener('push', (e) => {
  const d = (e.data && e.data.json()) || {}
  e.waitUntil(
    self.registration.showNotification(d.title || 'DOR', {
      body: d.body || '',
      icon: d.icon || '/icon-192.png',
      data: { url: d.url || '/' },
    })
  )
})

self.addEventListener('notificationclick', (e) => {
  e.notification.close()
  const url = (e.notification.data && e.notification.data.url) || '/'
  e.waitUntil(
    clients.matchAll({ type: 'window', includeUncontrolled: true }).then((cs) => {
      for (const c of cs) {
        if ('focus' in c) {
          if (c.navigate) c.navigate(url)
          return c.focus()
        }
      }
      return clients.openWindow(url)
    })
  )
})

self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()))
