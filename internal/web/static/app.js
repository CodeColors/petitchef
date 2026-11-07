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

// Impression (menu de la semaine, liste de courses) : <button data-print>.
for (const button of document.querySelectorAll('[data-print]')) {
  button.addEventListener('click', () => window.print());
}

// Copier la liste de courses en texte (messagerie) : <button data-copy>.
if (navigator.clipboard) {
  for (const button of document.querySelectorAll('[data-copy]')) {
    button.hidden = false;
    button.addEventListener('click', async () => {
      const text = await (await fetch(button.dataset.copy)).text();
      await navigator.clipboard.writeText(text);
      button.textContent = 'Liste copiée';
    });
  }
}

// Échange de deux plats du menu par glisser-déposer (ordinateur) ; sur
// mobile et sans script, le bouton « Échanger » du repas fait de même.
for (const week of document.querySelectorAll('[data-swap]')) {
  let dragged = null;
  for (const dish of week.querySelectorAll('.dish[data-entry]')) {
    if (dish.classList.contains('is-cooked')) continue;
    dish.draggable = true;
    dish.addEventListener('dragstart', () => { dragged = dish; });
    dish.addEventListener('dragover', (event) => { if (dragged && dragged !== dish) { event.preventDefault(); dish.classList.add('is-drop'); } });
    dish.addEventListener('dragleave', () => dish.classList.remove('is-drop'));
    dish.addEventListener('drop', async (event) => {
      event.preventDefault();
      dish.classList.remove('is-drop');
      if (!dragged || dragged === dish) return;
      await fetch(`${week.dataset.swap}/${dragged.dataset.entry}/echanger`, {
        method: 'POST', headers: { 'HX-Request': 'true' }, body: new URLSearchParams({ with: dish.dataset.entry }),
      });
      window.location.reload();
    });
  }
}
