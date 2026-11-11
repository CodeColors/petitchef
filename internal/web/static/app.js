// PetitChef : petits conforts facultatifs. Chaque écran fonctionne aussi
// sans script, avec des formulaires classiques.
'use strict';

// Demande de confirmation avant une action irréversible : <form data-confirm>.
document.addEventListener('submit', (event) => {
  const question = event.target.dataset.confirm;
  if (question && !window.confirm(question)) {
    event.preventDefault();
    event.stopImmediatePropagation();
  }
}, true);

// Le menu « Affichage » se referme avec Échap ou en cliquant ailleurs.
const prefs = document.querySelector('.prefs');
if (prefs) {
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && prefs.open) {
      prefs.open = false;
      prefs.querySelector('summary').focus();
    }
  });
  document.addEventListener('click', (event) => {
    if (prefs.open && !prefs.contains(event.target)) prefs.open = false;
  });
}

// Rayons d'un magasin : glisser-déposer pour réordonner (les boutons
// monter et descendre font de même, au clavier et sans script).
for (const list of document.querySelectorAll('[data-sortable]')) {
  let moving = null;
  for (const item of list.querySelectorAll('li[data-id]')) {
    item.draggable = true;
    item.addEventListener('dragstart', () => { moving = item; });
    item.addEventListener('dragover', (event) => { event.preventDefault(); item.classList.add('is-drop'); });
    item.addEventListener('dragleave', () => item.classList.remove('is-drop'));
    item.addEventListener('drop', async (event) => {
      event.preventDefault();
      item.classList.remove('is-drop');
      if (!moving || moving === item) return;
      const below = moving.compareDocumentPosition(item) & Node.DOCUMENT_POSITION_FOLLOWING;
      item.parentNode.insertBefore(moving, below ? item.nextSibling : item);
      const order = [...list.querySelectorAll('li[data-id]')].map((li) => li.dataset.id).join(',');
      await fetch(list.dataset.sortable, { method: 'POST', headers: { 'HX-Request': 'true' }, body: new URLSearchParams({ order }) });
      window.location.reload();
    });
  }
}

// Menus : échanger deux plats par glisser-déposer (sur ordinateur).
for (const week of document.querySelectorAll('[data-swap]')) {
  let moving = null;
  for (const dish of week.querySelectorAll('.dish[data-entry]:not(.is-cooked)')) {
    dish.draggable = true;
    dish.addEventListener('dragstart', () => { moving = dish; });
    dish.addEventListener('dragover', (event) => { if (moving && moving !== dish) { event.preventDefault(); dish.classList.add('is-drop'); } });
    dish.addEventListener('dragleave', () => dish.classList.remove('is-drop'));
    dish.addEventListener('drop', async (event) => {
      event.preventDefault();
      dish.classList.remove('is-drop');
      if (!moving || moving === dish) return;
      await fetch(`${week.dataset.swap}/${moving.dataset.entry}/echanger`, {
        method: 'POST', headers: { 'HX-Request': 'true' }, body: new URLSearchParams({ with: dish.dataset.entry }),
      });
      window.location.reload();
    });
  }
}

// Imprimer (menu, liste) et copier la liste de courses.
for (const button of document.querySelectorAll('[data-print]')) {
  button.addEventListener('click', () => window.print());
}
if (navigator.clipboard) {
  for (const button of document.querySelectorAll('[data-copy]')) {
    button.hidden = false;
    button.addEventListener('click', async () => {
      await navigator.clipboard.writeText(await (await fetch(button.dataset.copy)).text());
      button.textContent = 'Liste copiée ✓';
    });
  }
}

// Ingrédients d'une recette : « Ajouter » et « Retirer » agissent sur place
// (sans script, le serveur renvoie le formulaire avec une ligne de plus ou
// de moins).
const ingList = document.getElementById('ing-list');
const ingTemplate = document.getElementById('ing-template');
if (ingList && ingTemplate) {
  const renumber = () => {
    ingList.querySelectorAll(':scope > .ing').forEach((row, k) => {
      row.querySelectorAll('.ing-n').forEach((el) => { el.textContent = 'Ingrédient ' + (k + 1); });
      row.querySelectorAll('.ing-remove .sr-only').forEach((el) => { el.textContent = ' l\'ingrédient ' + (k + 1); });
      const group = row.querySelector('.ing-kind');
      if (group) group.setAttribute('aria-label', 'Nature de l\'ingrédient ' + (k + 1));
    });
  };
  let next = 0;
  for (const row of ingList.querySelectorAll('.ing')) {
    next = Math.max(next, Number(row.id.replace('ing-', '')) + 1);
  }
  document.querySelector('[data-ing-add]')?.addEventListener('click', (event) => {
    event.preventDefault();
    const html = ingTemplate.innerHTML.replaceAll('__I__', String(next++)).replaceAll('__N__', '');
    ingList.insertAdjacentHTML('beforeend', html);
    const row = ingList.lastElementChild;
    if (window.htmx) window.htmx.process(row);
    renumber();
    row.querySelector('input').focus();
  });
  ingList.addEventListener('click', (event) => {
    const button = event.target.closest('[data-ing-remove]');
    if (!button) return;
    event.preventDefault();
    const row = button.closest('.ing');
    const rows = ingList.querySelectorAll(':scope > .ing');
    const index = [...rows].indexOf(row);
    if (rows.length === 1) {
      // Dernière ligne : elle est vidée plutôt que retirée.
      row.querySelectorAll('input[type=text], input:not([type]), select').forEach((f) => { f.value = f.tagName === 'SELECT' ? f.options[0].value : ''; });
      row.querySelector('.ing-unit').value = 'g';
      row.querySelector('input[value="produit"]').checked = true;
      row.querySelectorAll('input[type=checkbox]').forEach((c) => { c.checked = false; });
      row.querySelector('input').focus();
      return;
    }
    row.remove();
    renumber();
    const after = ingList.querySelectorAll(':scope > .ing');
    after[Math.min(index, after.length - 1)].querySelector('input').focus();
  });
}
