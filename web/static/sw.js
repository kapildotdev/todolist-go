const CACHE='todo-v2';
const SHELL=['/app','/login','/static/style.css','/static/app.js','/static/icon.svg','/static/manifest.webmanifest'];
self.addEventListener('install',e=>{e.waitUntil(caches.open(CACHE).then(c=>c.addAll(SHELL)).then(()=>self.skipWaiting()));});
self.addEventListener('activate',e=>{e.waitUntil(self.clients.claim());});
// Cache-first for shell/static, network-first for /api (fails offline -> client uses IndexedDB).
self.addEventListener('fetch',e=>{
  const u=new URL(e.request.url);
  if(u.pathname.startsWith('/api/'))return; // let it fail fast offline; app.js handles queue
  e.respondWith(caches.match(e.request).then(hit=>hit||fetch(e.request).then(res=>{
    const copy=res.clone();caches.open(CACHE).then(c=>c.put(e.request,copy));return res;
  }).catch(()=>caches.match('/app'))));
});
