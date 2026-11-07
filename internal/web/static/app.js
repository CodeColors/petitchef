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
