// Scan de code-barres, chargé uniquement sur l'écran de scan : API
// BarcodeDetector du navigateur quand elle existe (aucune bibliothèque
// tierce), sinon la saisie du code reste disponible.
'use strict';
(() => {
  const root = document.querySelector('[data-scan]');
  if (!root || !('BarcodeDetector' in window) || !navigator.mediaDevices) return;
  const start = root.querySelector('[data-scan-start]');
  const video = root.querySelector('video');
  const status = root.querySelector('[data-scan-status]');
  const input = root.querySelector('#code');
  start.hidden = false;
  start.addEventListener('click', async () => {
    let stream;
    try {
      const detector = new window.BarcodeDetector({ formats: ['ean_13', 'ean_8', 'upc_a', 'upc_e'] });
      stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: 'environment' } });
      video.srcObject = stream;
      video.hidden = false;
      await video.play();
      status.textContent = 'Visez le code-barres…';
      const tick = async () => {
        const codes = await detector.detect(video).catch(() => []);
        if (codes.length > 0) {
          stream.getTracks().forEach((t) => t.stop());
          input.value = codes[0].rawValue;
          input.form.requestSubmit();
          return;
        }
        requestAnimationFrame(tick);
      };
      tick();
    } catch {
      stream?.getTracks().forEach((t) => t.stop());
      status.textContent = 'Appareil photo indisponible : saisissez le code.';
    }
  });
})();
