// Offline-first client: IndexedDB (tasks + outbox) + autosave debounce + manual Save.
const DB='todo-pwa', VER=1;
let lastPulledAt = localStorage.getItem('last_pulled_at') || null;
let autosaveTimer=null, editingId=null, syncing=false;

document.documentElement.dataset.theme = localStorage.getItem('theme') || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : '');
document.getElementById('btn-theme').textContent = document.documentElement.dataset.theme==='dark' ? '☀️' : '🌙';

function idb(){return new Promise((res,rej)=>{
  const r=indexedDB.open(DB,VER);
  r.onupgradeneeded=()=>{const d=r.result;
    if(!d.objectStoreNames.contains('tasks'))d.createObjectStore('tasks',{keyPath:'id'});
    if(!d.objectStoreNames.contains('outbox'))d.createObjectStore('outbox',{keyPath:'cid',autoIncrement:true});
  };
  r.onsuccess=()=>res(r.result);r.onerror=()=>rej(r.error);
});}
async function tx(name,mode,fn){const db=await idb();return new Promise((res,rej)=>{
  const t=db.transaction(name,mode);const s=t.objectStore(name);const out=fn(s);
  t.oncomplete=()=>res(out&&out.result!==undefined?out.result:out);t.onerror=()=>rej(t.error);});}
const localAll=()=>tx('tasks','readonly',s=>s.getAll());
const localPut=t=>tx('tasks','readwrite',s=>s.put(t));
const localDel=id=>tx('tasks','readwrite',s=>s.delete(id));
const localGet=id=>tx('tasks','readonly',s=>s.get(id));
const outboxAll=()=>tx('outbox','readonly',s=>s.getAll());
const outboxAdd=op=>tx('outbox','readwrite',s=>s.add({...op,cid:crypto.randomUUID(),ts:new Date().toISOString()}));
const outboxClear=()=>tx('outbox','readwrite',s=>s.clear());

function token(){return sessionStorage.getItem('access_token')||'';}
async function api(path,opts={}){
  opts.headers={...(opts.headers||{}),'Content-Type':'application/json','Authorization':'Bearer '+token()};
  let res=await fetch(path,opts);
  if(res.status===401){
    const rr=await fetch('/api/auth/refresh',{method:'POST'});
    if(rr.ok){const d=await rr.json();sessionStorage.setItem('access_token',d.access_token);
      opts.headers.Authorization='Bearer '+d.access_token;res=await fetch(path,opts);
    } else {location.href='/login';throw new Error('session expired');}
  }
  return res;
}
const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
function toast(m,k=''){const box=document.getElementById('toasts');const t=document.createElement('div');t.className='toast '+k;t.textContent=m;box.appendChild(t);setTimeout(()=>t.remove(),3500);}
function dueChip(due){
  if(!due)return '';
  const d=new Date(due), now=new Date(); d.setHours(0,0,0,0); now.setHours(0,0,0,0);
  const diff=Math.round((d-now)/864e5);
  const label=d.toLocaleDateString();
  if(diff<0)return `<span class="chip overdue">⚠ overdue ${label}</span>`;
  if(diff===0)return `<span class="chip today">📅 today</span>`;
  return `<span class="chip">📅 ${label}</span>`;
}
function prChip(p){return p==1?'<span class="chip p1">▲ high</span>':p==2?'<span class="chip p2">● med</span>':'';}

async function render(){
  const all=(await localAll()).filter(t=>!t.deleted_at);
  const done=all.filter(t=>t.status==='done').length;
  document.getElementById('st-total').textContent=all.length;
  document.getElementById('st-open').textContent=all.length-done;
  document.getElementById('st-done').textContent=done;
  document.getElementById('st-bar').style.width=(all.length?Math.round(done/all.length*100):0)+'%';
  document.getElementById('loading').style.display='none';

  const fs=document.getElementById('filter-status').value||'all';
  const q=(document.getElementById('search').value||'').toLowerCase();
  const list=all.filter(t=>(fs==='all'||t.status===fs)&&(!q||(t.title+' '+(t.description||'')).toLowerCase().includes(q)))
    .sort((a,b)=>((a.status==='done')-(b.status==='done'))||((b.priority||0)-(a.priority||0))||((a.position||0)-(b.position||0)));
  const el=document.getElementById('list');
  if(!list.length){el.innerHTML=`<div class="card empty"><div class="big">${q||fs!=='all'?'🔍':'🌱'}</div><b>${q||fs!=='all'?'No matches':'All clear — nothing to do'}</b><br/><span class="muted">${q?'Try a different search':'Add your first task above. It works offline too.'}</span></div>`;}
  else el.innerHTML=list.map(t=>`<li class="card task ${t.status==='done'?'done':''}" data-id="${t.id}">
    <button class="check ${t.status==='done'?'done':''}" data-act="toggle" title="toggle done">${t.status==='done'?'✓':''}</button>
    <div class="task-body"><div class="title">${esc(t.title)}</div>
      ${t.description?`<div class="desc">${esc(t.description)}</div>`:''}
      <div class="meta"><span class="chip">${t.status==='done'?'✅ done':t.status==='doing'?'⏳ doing':'📝 todo'}</span>${prChip(t.priority)}${dueChip(t.due_date||t.dueDate)}${t._dirty?'<span class="chip unsaved">● unsaved</span>':''}</div></div>
    <div class="actions"><button class="link" data-act="edit">✏️</button><button class="link danger" data-act="del">🗑️</button></div></li>`).join('');
  const pending=(await outboxAll()).length;
  const btn=document.getElementById('btn-save');
  btn.textContent=syncing?'⏳ Syncing…':(pending?`💾 Save · ${pending}`:'✓ Saved');
  btn.disabled=syncing||!pending;
  document.getElementById('dirty').textContent=pending?`● ${pending} unsaved`:'✓ saved';
}

async function mutate(id, patch){
  const cur=await localGet(id); if(!cur)return;
  const next={...cur,...patch,updated_at:new Date().toISOString(),_dirty:true};
  await localPut(next);
  await outboxAdd({kind:'upsert',task:strip(next)});
  await render();scheduleAutosave();
}
function strip(t){const {_dirty,...rest}=t;return rest;}

async function flushOutbox(silent=false){
  const ops=await outboxAll();
  if(!ops.length){await pull();return;}
  syncing=true;await render();setNet(true,true);
  const upserts=ops.filter(o=>o.kind==='upsert').map(o=>o.task);
  const deletes=ops.filter(o=>o.kind==='delete').map(o=>({id:o.id,updated_at:o.ts}));
  try{
    const res=await api('/api/tasks/sync',{method:'POST',body:JSON.stringify({upserts,deletes,last_pulled_at:lastPulledAt})});
    if(!res.ok)throw new Error('sync '+res.status);
    const data=await res.json();
    for(const t of [...(data.applied||[]),...(data.server_changes||[])])await localPut({...t,_dirty:false});
    if(data.conflicts?.length){
      const c=document.getElementById('conflicts');c.classList.remove('hidden');
      c.innerHTML=`<b>⚠️ ${data.conflicts.length} conflict(s)</b> — server version kept (Last-Write-Wins).`;
      toast(data.conflicts.length+' conflict(s): server kept','warn');
    } else document.getElementById('conflicts').classList.add('hidden');
    await outboxClear();
    lastPulledAt=data.server_time;localStorage.setItem('last_pulled_at',lastPulledAt);
    if(!silent)toast('All changes saved ✓','ok');
  }catch(e){setNet(false);if(!silent)toast('Offline — will retry when online','warn');syncing=false;await render();return;}
  syncing=false;setNet(true);await render();
}
async function pull(){
  try{
    const url='/api/tasks'+(lastPulledAt?`?since=${encodeURIComponent(lastPulledAt)}`:'');
    const res=await api(url);if(!res.ok)throw 0;
    const data=await res.json();
    const arr=Array.isArray(data)?data:(data.server_changes||data||[]);
    for(const t of arr)await localPut({...t,_dirty:false});
    lastPulledAt=new Date().toISOString();localStorage.setItem('last_pulled_at',lastPulledAt);
    setNet(true);
  }catch{setNet(false);}
  await render();
}
function scheduleAutosave(){
  if(!document.getElementById('autosave').checked)return;
  clearTimeout(autosaveTimer);autosaveTimer=setTimeout(()=>flushOutbox(true),1500);
}
function setNet(on,sync=false){const d=document.getElementById('net-dot'),n=document.getElementById('net');
  d.className='dot'+(sync?' syncing':on?'':' off');n.textContent=sync?'syncing…':on?'online':'offline';}

// events
document.getElementById('new-form').addEventListener('submit',async e=>{
  e.preventDefault();
  const title=document.getElementById('new-title').value.trim();if(!title)return;
  const t={id:crypto.randomUUID(),title,description:document.getElementById('new-desc').value.trim(),
    status:document.getElementById('new-status').value,priority:+document.getElementById('new-priority').value,
    due_date:document.getElementById('new-due').value?new Date(document.getElementById('new-due').value).toISOString():null,
    position:Date.now(),updated_at:new Date().toISOString(),_dirty:true};
  await localPut(t);await outboxAdd({kind:'upsert',task:strip(t)});
  e.target.reset();document.getElementById('new-title').focus();
  await render();scheduleAutosave();toast('Task added');
});
document.getElementById('list').addEventListener('click',async e=>{
  const li=e.target.closest('li');if(!li)return;const id=li.dataset.id;
  const act=e.target.closest('[data-act]')?.dataset.act;if(!act)return;
  if(act==='del'){if(!confirm('Delete this task?'))return;await localDel(id);await outboxAdd({kind:'delete',id});await render();scheduleAutosave();toast('Deleted');}
  if(act==='toggle'){const cur=await localGet(id);await mutate(id,{status:cur.status==='done'?'todo':'done'});}
  if(act==='edit'){openModal(id);}
});
document.getElementById('filter-status').addEventListener('change',render);
document.getElementById('search').addEventListener('input',render);
document.getElementById('btn-save').addEventListener('click',()=>flushOutbox(false));
document.getElementById('btn-reload').addEventListener('click',pull);
document.getElementById('autosave').addEventListener('change',e=>{localStorage.setItem('autosave',e.target.checked?'1':'0');if(e.target.checked)scheduleAutosave();});
document.getElementById('btn-theme').addEventListener('click',e=>{const h=document.documentElement;h.dataset.theme=h.dataset.theme==='dark'?'':'dark';localStorage.setItem('theme',h.dataset.theme);e.target.textContent=h.dataset.theme==='dark'?'☀️':'🌙';});
document.getElementById('btn-logout').addEventListener('click',async()=>{await fetch('/api/auth/logout',{method:'POST'});sessionStorage.clear();location.href='/login';});
window.addEventListener('online',()=>flushOutbox(true));
window.addEventListener('offline',()=>setNet(false));
document.addEventListener('keydown',e=>{
  if((e.ctrlKey||e.metaKey)&&e.key==='s'){e.preventDefault();flushOutbox(false);}
  if(e.key==='Escape')document.getElementById('modal').classList.add('hidden');
});
// modal
async function openModal(id){editingId=id;const t=await localGet(id);if(!t)return;
  document.getElementById('m-title').value=t.title||'';
  document.getElementById('m-desc').value=t.description||'';
  document.getElementById('m-status').value=t.status||'todo';
  document.getElementById('m-priority').value=t.priority||0;
  const due=t.due_date||t.dueDate;document.getElementById('m-due').value=due?new Date(due).toISOString().slice(0,10):'';
  document.getElementById('modal').classList.remove('hidden');}
document.getElementById('m-close').addEventListener('click',()=>document.getElementById('modal').classList.add('hidden'));
document.getElementById('m-save').addEventListener('click',async()=>{
  const due=document.getElementById('m-due').value;
  await mutate(editingId,{title:document.getElementById('m-title').value.trim()||'(untitled)',
    description:document.getElementById('m-desc').value,status:document.getElementById('m-status').value,
    priority:+document.getElementById('m-priority').value,due_date:due?new Date(due).toISOString():null});
  document.getElementById('modal').classList.add('hidden');toast('Task updated');});

if(localStorage.getItem('autosave')==='0')document.getElementById('autosave').checked=false;
if(!token())location.href='/login';
setNet(navigator.onLine);pull();
