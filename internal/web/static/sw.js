// Service worker minimal de PetitChef, limité à l'écran « en magasin » de
// la liste de courses (voir docs/DECISIONS.md) : la page est servie depuis
// le réseau quand il répond, sinon depuis la dernière copie ; les fichiers
// statiques versionnés qu'elle charge sont gardés en cache. Aucune autre
// requête n'est interceptée.
'use strict';
const CACHE = 'petitchef-magasin-v1';
const PAGE = '/courses/magasin';

self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(CACHE).then((c) => c.add(PAGE)).catch(() => {}).then(() => self.skipWaiting()));
});

self.addEventListener('activate', (event) => {
  event.waitUntil(caches.keys()
    .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
    .then(() => self.clients.claim()));
});

self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url);
  if (event.request.method !== 'GET' || url.origin !== self.location.origin) return;
  if (url.pathname === PAGE) {
    event.respondWith(fetch(event.request).then((res) => {
      if (res.ok && !res.redirected) {
        const copy = res.clone();
        caches.open(CACHE).then((c) => c.put(PAGE, copy));
      }
      return res;
    }).catch(() => caches.match(PAGE)));
    return;
  }
  if (url.pathname.startsWith('/static/')) {
    event.respondWith(caches.match(event.request).then((hit) => hit || fetch(event.request).then((res) => {
      if (res.ok) {
        const copy = res.clone();
        caches.open(CACHE).then((c) => c.put(event.request, copy));
      }
      return res;
    })));
  }
});
