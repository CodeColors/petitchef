// Parcours et contrôles d'affichage de PetitChef : 360 / 768 / 1280 px,
// thèmes clair et sombre, règles WCAG A et AA (axe, le moteur de
// Lighthouse). Le binaire est lancé sur une base vierge ; les captures
// sont écrites dans e2e/screenshots/ (non versionné, publié par la CI).
import { after, before, test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';
import { chromium } from 'playwright';
import AxeBuilder from '@axe-core/playwright';

const BIN = process.env.PETITCHEF_BIN ?? path.resolve(import.meta.dirname, '../bin/petitchef');
const PORT = process.env.E2E_PORT ?? '18091';
const BASE = `http://127.0.0.1:${PORT}`;
const SHOTS = path.join(import.meta.dirname, 'screenshots');
const USER = 'camille';
const PASSWORD = 'une phrase de passe solide';
const WIDTHS = [360, 768, 1280];
const SCHEMES = ['light', 'dark'];
const BACKGROUND = { light: 'rgb(246, 245, 241)', dark: 'rgb(18, 20, 18)' };
const TARGETS = 'button:not(.chart button), input:not([type=hidden]):not([type=checkbox]):not([type=radio]):not([type=range]), select, textarea, summary, a.btn, .tabbar a, .fab, .row-link, .chips a, .tabs a';

let server;
let dataDir;
let browser;
let session;
const ids = {};

before(async () => {
  dataDir = await mkdtemp(path.join(tmpdir(), 'petitchef-e2e-'));
  await mkdir(SHOTS, { recursive: true });
  server = spawn(BIN, [], { env: { ...process.env, PORT, DATA_DIR: dataDir, TZ: 'Europe/Paris' }, stdio: 'inherit' });
  await waitHealthy();
  browser = await chromium.launch();
  await prepare();
});

after(async () => {
  await browser?.close();
  if (server && server.exitCode === null) {
    server.kill('SIGTERM');
    await once(server, 'exit');
  }
  await rm(dataDir, { recursive: true, force: true });
});

async function waitHealthy() {
  for (let i = 0; i < 100; i++) {
    try {
      if ((await fetch(`${BASE}/healthz`)).ok) return;
    } catch {
      // pas encore prêt
    }
    await sleep(100);
  }
  throw new Error(`PetitChef ne répond pas sur ${BASE}`);
}

async function withPage(width, colorScheme, fn, storageState = session) {
  const context = await browser.newContext({ viewport: { width, height: 800 }, colorScheme, locale: 'fr-FR', storageState, serviceWorkers: 'allow' });
  try {
    await fn(await context.newPage(), context);
  } finally {
    await context.close();
  }
}

// Création du compte et d'un foyer de démonstration par l'interface.
async function prepare() {
  const context = await browser.newContext({ locale: 'fr-FR' });
  const page = await context.newPage();
  await page.goto(`${BASE}/`);
  assert.match(page.url(), /\/setup$/);
  await page.fill('#username', USER);
  await page.fill('#password', PASSWORD);
  await page.fill('#confirm', PASSWORD);
  await page.click('button[type=submit], form button');
  await page.waitForURL(`${BASE}/`);
  const post = (url, form) => page.request.post(`${BASE}${url}`, { form, headers: { Origin: BASE } });
  await post('/foyer', { name: 'Léa', portion: '1', diet_vegetarien: '1' });
  await post('/foyer', { name: 'Tom', portion: '0,5', all_arachides: '1' });
  await post('/magasins', { name: 'Hyper', default: '1' });
  const monday = new Date();
  monday.setDate(monday.getDate() - ((monday.getDay() + 6) % 7));
  ids.monday = monday.toISOString().slice(0, 10);
  await post('/menus/generer', { from: ids.monday, weeks: '1', template: '1', preset: 'equilibre', leftovers: '1' });
  for (const [produit, qty] of [['Courgette', '1000'], ['Lait', '2000'], ['Œufs', '6'], ['Crème fraîche épaisse', '200']]) {
    await post('/stock/ajouter', { produit, qty });
  }
  await post('/stock/modeles/salle-de-bain', {});
  await post('/courses/recalculer', {});
  const menu = await page.request.get(`${BASE}/menus?semaine=${ids.monday}`);
  const html = await menu.text();
  ids.meal = html.match(/\/menus\/repas\/(\d+)/)[1];
  ids.recipe = html.match(/\/recettes\/(\d+)/)[1];
  session = await context.storageState();
  await context.close();
}

// Contrôles communs : pas de défilement horizontal, cibles tactiles de
// 44 px, champs de 16 px, thème appliqué.
async function checkLayout(page, label, scheme) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  assert.equal(overflow, 0, `${label} : défilement horizontal de ${overflow} px`);
  const tooSmall = await page.$$eval(TARGETS, (els) => els
    .filter((el) => el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden' && !el.closest('[hidden], .sr-only')
      && !(el.closest('details:not([open])') && el.tagName !== 'SUMMARY'))
    .map((el) => ({ el, r: el.getBoundingClientRect() }))
    .filter(({ r }) => r.height < 43.5 || r.width < 43.5)
    .map(({ el, r }) => `${el.tagName.toLowerCase()}.${el.className} « ${(el.textContent || el.getAttribute('aria-label') || el.name || '').trim().slice(0, 30)} » ${Math.round(r.width)}×${Math.round(r.height)}`));
  assert.deepEqual(tooSmall, [], `${label} : cibles tactiles trop petites`);
  const smallFields = await page.$$eval('input:not([type=checkbox]):not([type=radio]):not([type=hidden]):not([type=range]), select, textarea',
    (els) => els.filter((el) => el.getClientRects().length > 0 && parseFloat(getComputedStyle(el).fontSize) < 16).map((el) => el.id || el.name));
  assert.deepEqual(smallFields, [], `${label} : champs sous 16 px`);
  const bg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
  assert.equal(bg, BACKGROUND[scheme], `${label} : thème ${scheme} non appliqué`);
}

const PAGES = () => [
  ['accueil', '/'],
  ['menus', `/menus?semaine=${ids.monday}`],
  ['repas', `/menus/repas/${ids.meal}`],
  ['generer', '/menus/generer'],
  ['courses', '/courses'],
  ['magasin', '/courses/magasin'],
  ['stock', '/stock'],
  ['stock-ajouter', '/stock/ajouter'],
  ['stock-suivi', '/stock/suivi'],
  ['recettes', '/recettes'],
  ['recette', `/recettes/${ids.recipe}`],
  ['cuisine', `/recettes/${ids.recipe}/cuisine`],
  ['ce-soir', '/ce-soir'],
  ['catalogue', '/catalogue'],
  ['produit', '/catalogue/1'],
  ['magasins', '/magasins'],
  ['plus', '/plus'],
  ['statistiques', '/statistiques'],
  ['exports', '/exports'],
  ['creneaux', '/creneaux'],
  ['foyer', '/foyer'],
  ['alertes', '/alertes/reglages'],
];

test('connexion : page anonyme lisible dans les deux thèmes', async () => {
  for (const scheme of SCHEMES) {
    await withPage(360, scheme, async (page) => {
      await page.goto(`${BASE}/login`);
      await checkLayout(page, `connexion ${scheme}`, scheme);
      const axe = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
      assert.deepEqual(axe.violations.map((v) => v.id), [], `connexion ${scheme} : ${JSON.stringify(axe.violations.map((v) => [v.id, v.nodes[0]?.target]))}`);
    }, undefined);
  }
});

for (const width of WIDTHS) {
  for (const scheme of SCHEMES) {
    test(`écrans clés à ${width} px, thème ${scheme}`, async () => {
      await withPage(width, scheme, async (page) => {
        for (const [name, url] of PAGES()) {
          const res = await page.goto(`${BASE}${url}`);
          assert.equal(res.status(), 200, `${name} : statut ${res.status()}`);
          await checkLayout(page, `${name} ${width}px ${scheme}`, scheme);
          if (width !== 768) {
            await page.screenshot({ path: path.join(SHOTS, `${name}-${width}-${scheme}.png`), fullPage: true });
          }
          if (width === 1280 || (width === 360 && ['magasin', 'cuisine', 'stock-ajouter', 'menus'].includes(name))) {
            const axe = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
            assert.deepEqual(axe.violations.map((v) => v.id), [],
              `${name} ${width}px ${scheme} : ${JSON.stringify(axe.violations.map((v) => [v.id, v.nodes.slice(0, 3).map((n) => n.target)]))}`);
          }
        }
      });
    });
  }
}

test('ajout au stock depuis le téléphone, puis annulation', async () => {
  await withPage(360, 'light', async (page) => {
    await page.goto(`${BASE}/stock/ajouter`);
    await page.fill('#produit', 'Poireau');
    await page.press('#produit', 'Tab'); // le détail du produit (conditionnements, date proposée) se met à jour
    await page.waitForSelector('#package');
    await page.fill('#qty', '750');
    await page.click('form[action="/stock/ajouter"] button.btn-primary');
    await page.waitForURL(`${BASE}/stock/ajouter`);
    assert.match(await page.textContent('.flash'), /Ajouté : 750\s?g de Poireau/);
    await page.click('form[action="/stock/annuler"] button');
    assert.match(await page.textContent('.flash'), /annulée/);
  });
});

test('courses en magasin, y compris hors ligne', async () => {
  await withPage(360, 'light', async (page, context) => {
    await page.goto(`${BASE}/courses/magasin`);
    await page.evaluate(() => navigator.serviceWorker.ready);
    await page.reload(); // la page est désormais servie (et mise en cache) par le service worker
    const lines = page.locator('[data-item]');
    const count = await lines.count();
    assert.ok(count >= 3, `${count} lignes`);
    const first = lines.nth(0);
    await first.locator('.shop-check').click();
    await page.waitForFunction(() => document.querySelector('[data-item]').classList.contains('is-checked'));
    await page.waitForFunction(() => (localStorage.getItem('petitchef-courses-file') ?? '[]') === '[]');

    // Hors ligne : la coche est gardée sur l'appareil.
    await context.setOffline(true);
    const second = lines.nth(1);
    const secondId = await second.getAttribute('data-item');
    await second.locator('.shop-check').click();
    await page.waitForFunction(() => document.body.classList.contains('is-offline'));
    assert.match(await page.evaluate(() => localStorage.getItem('petitchef-courses-file')), new RegExp(secondId));
    // La page se rouvre sans réseau, coches comprises.
    await page.reload();
    assert.ok(await page.locator(`[data-item="${secondId}"]`).evaluate((el) => el.classList.contains('is-checked')));
    await page.screenshot({ path: path.join(SHOTS, 'magasin-hors-ligne-360.png'), fullPage: true });

    // Retour du réseau : la file est envoyée au serveur.
    await context.setOffline(false);
    await page.evaluate(() => window.dispatchEvent(new Event('online')));
    await page.waitForFunction(() => (localStorage.getItem('petitchef-courses-file') ?? '[]') === '[]');
    const html = await (await page.request.get(`${BASE}/courses`)).text();
    assert.match(html, new RegExp(`class="shop-line is-checked" id="ligne-${secondId}"`));
  });
});

test('mode cuisine et génération en trois clics', async () => {
  await withPage(1280, 'light', async (page) => {
    await page.goto(`${BASE}/menus?semaine=${ids.monday}`);
    await page.click('a.btn-primary:has-text("Générer")');
    await page.click('form[action="/menus/generer"] button.btn-primary');
    await page.waitForURL(/\/menus\?semaine=/);
    assert.match(await page.textContent('.flash'), /Menu généré en \d+ ms/);
    await page.goto(`${BASE}/recettes/${ids.recipe}/cuisine`);
    assert.ok(await page.locator('[data-step]').count() >= 1);
  });
});
