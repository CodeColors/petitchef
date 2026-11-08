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
