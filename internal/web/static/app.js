// PetitChef : comportements facultatifs. Sans script, chaque écran reste
// utilisable (formulaires classiques).
'use strict';

// Confirmation avant toute action destructrice : <form data-confirm="…">.
document.addEventListener('submit', (event) => {
  const message = event.target.dataset.confirm;
  if (message && !window.confirm(message)) {
    event.preventDefault();
    event.stopImmediatePropagation();
  }
}, true);

// Glisser-déposer d'une liste ordonnée [data-sortable] (rayons d'un
// magasin) : l'ordre est envoyé au serveur ; sans script, les boutons
// monter et descendre font le même travail.
for (const list of document.querySelectorAll('[data-sortable]')) {
  let dragged = null;
  for (const item of list.querySelectorAll('li[data-id]')) {
    item.draggable = true;
    item.addEventListener('dragstart', () => { dragged = item; });
    item.addEventListener('dragover', (event) => { event.preventDefault(); item.classList.add('is-drop'); });
    item.addEventListener('dragleave', () => item.classList.remove('is-drop'));
    item.addEventListener('drop', (event) => {
      event.preventDefault();
      item.classList.remove('is-drop');
      if (!dragged || dragged === item) return;
      const after = dragged.compareDocumentPosition(item) & Node.DOCUMENT_POSITION_FOLLOWING;
      item.parentNode.insertBefore(dragged, after ? item.nextSibling : item);
      const order = [...list.querySelectorAll('li[data-id]')].map((li) => li.dataset.id).join(',');
      fetch(list.dataset.sortable, { method: 'POST', headers: { 'HX-Request': 'true' }, body: new URLSearchParams({ order }) })
        .then(() => window.location.reload());
    });
  }
}
