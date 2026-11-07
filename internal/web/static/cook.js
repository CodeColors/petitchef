// Mode cuisine : une étape à la fois, minuteries multiples, écran maintenu
// allumé (Screen Wake Lock) quand le navigateur le permet. Sans script,
// toutes les étapes restent affichées.
'use strict';
(() => {
  const root = document.querySelector('[data-cook]');
  if (!root) return;

  // Écran maintenu allumé.
  let lock = null;
  const keepAwake = async () => {
    try {
      lock = await navigator.wakeLock?.request('screen');
      if (lock) root.querySelector('[data-wakelock]').hidden = false;
    } catch { /* refusé ou indisponible */ }
  };
  keepAwake();
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'visible' && !lock?.released === false) keepAwake(); });

  // Étapes une par une.
  const steps = [...root.querySelectorAll('[data-step]')];
  const nav = root.querySelector('[data-cook-nav]');
  let current = 0;
  const showStep = () => steps.forEach((s, i) => { s.hidden = i !== current; });
  if (steps.length > 1) {
    nav.hidden = false;
    showStep();
    nav.querySelector('[data-prev]').addEventListener('click', () => { current = Math.max(current - 1, 0); showStep(); });
    nav.querySelector('[data-next]').addEventListener('click', () => { current = Math.min(current + 1, steps.length - 1); showStep(); });
  }

  // Minuteries multiples.
  const box = root.querySelector('[data-timers]');
  const fmt = (s) => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
  for (const button of root.querySelectorAll('[data-timer]')) {
    button.addEventListener('click', () => {
      const total = Number(button.dataset.timer);
      const end = Date.now() + total * 1000;
      const t = document.createElement('button');
      t.type = 'button';
      t.className = 'btn timer';
      t.title = 'Arrêter la minuterie';
      box.append(t);
      const label = button.closest('[data-step]')?.querySelector('p')?.textContent.slice(0, 30) ?? '';
      const tick = () => {
        const left = Math.max(0, Math.round((end - Date.now()) / 1000));
        t.textContent = `${fmt(left)} · ${label}`;
        if (left === 0) {
          t.classList.add('is-done');
          t.textContent = `Terminé · ${label}`;
          navigator.vibrate?.([300, 150, 300]);
          clearInterval(id);
        }
      };
      const id = setInterval(tick, 1000);
      tick();
      t.addEventListener('click', () => { clearInterval(id); t.remove(); });
    });
  }
})();
