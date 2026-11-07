// Mode magasin : cocher d'un geste, y compris sans réseau. Les coches sont
// appliquées tout de suite à l'écran, mémorisées sur l'appareil et envoyées
// au serveur dès que possible. Le service worker (sw.js) garde une copie de
// cet écran pour l'ouvrir hors ligne.
'use strict';
(() => {
  const KEY = 'petitchef-courses-file';
  const root = document.querySelector('[data-store]');
  if (!root) return;
  if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js').catch(() => {});

  const load = () => { try { return JSON.parse(localStorage.getItem(KEY) || '[]'); } catch { return []; } };
  const save = (queue) => { try { localStorage.setItem(KEY, JSON.stringify(queue)); } catch { /* stockage indisponible */ } };

  const counter = document.querySelector('[data-left]');
  const refresh = () => {
    if (!counter) return;
    const left = root.querySelectorAll('.shop-line:not(.is-checked)').length;
    counter.textContent = `${left} article${left > 1 ? 's' : ''} à prendre`;
  };
  const show = (line, checked) => {
    line.classList.toggle('is-checked', checked);
    line.querySelector('.shop-check').setAttribute('aria-pressed', String(checked));
    line.querySelector('.shop-toggle input[name="checked"]').value = checked ? '0' : '1';
  };
  const offline = (state) => document.body.classList.toggle('is-offline', state);

  let flushing = false;
  const flush = async () => {
    if (flushing) return;
    flushing = true;
    let queue = load();
    while (queue.length > 0) {
      const op = queue[0];
      try {
        const res = await fetch(`/courses/lignes/${op.id}/cocher`, {
          method: 'POST', headers: { 'X-Requested-With': 'fetch' }, body: new URLSearchParams({ checked: op.checked ? '1' : '0' }),
        });
        if (!res.ok && res.status !== 404) throw new Error(String(res.status));
        offline(false);
      } catch {
        offline(true);
        break;
      }
      queue = load().slice(1);
      save(queue);
    }
    flushing = false;
  };

  // Coches faites hors ligne et pas encore envoyées : réappliquées à la
  // page (qui peut venir du cache).
  for (const op of load()) {
    const line = root.querySelector(`[data-item="${op.id}"]`);
    if (line) show(line, op.checked);
  }
  refresh();

  root.addEventListener('submit', (event) => {
    const form = event.target.closest('.shop-toggle');
    if (!form) return;
    event.preventDefault();
    const line = form.closest('[data-item]');
    const checked = !line.classList.contains('is-checked');
    show(line, checked);
    refresh();
    navigator.vibrate?.(15);
    save([...load().filter((op) => op.id !== line.dataset.item), { id: line.dataset.item, checked }]);
    flush();
  });
  window.addEventListener('online', flush);
  window.addEventListener('offline', () => offline(true));
  if (!navigator.onLine) offline(true);
  flush();
})();
