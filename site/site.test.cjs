const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');

const source = fs.readFileSync(path.join(__dirname, 'site.js'), 'utf8');
const languages = JSON.parse(fs.readFileSync(path.join(__dirname, 'languages.json'), 'utf8'));
const english = fs.readFileSync(path.join(__dirname, 'install.html'), 'utf8');
const ids = (html) => [...html.matchAll(/\bid="([^"]+)"/g)].map((match) => match[1]);
const commands = (html) => [...html.matchAll(/<pre><code>([\s\S]*?)<\/code><\/pre>/g)].map((match) => match[1]);

function element(tag) {
  return {
    tag, children: [], attrs: {}, listeners: {}, textContent: '', className: '',
    classList: { add() {}, remove() {} },
    setAttribute(name, value) { this.attrs[name] = value; },
    getAttribute(name) { return this.attrs[name] ?? null; },
    addEventListener(name, callback) { this.listeners[name] = callback; },
    appendChild(child) {
      if (child.parentNode) child.parentNode.children.splice(child.parentNode.children.indexOf(child), 1);
      child.parentNode = this;
      this.children.push(child);
    },
    insertBefore(child, reference) {
      child.parentNode = this;
      this.children.splice(this.children.indexOf(reference), 0, child);
    },
    querySelector(selector) { return this.querySelectorAll(selector)[0] ?? null; },
    querySelectorAll(selector) {
      return this.children.flatMap((child) => {
        const matches = selector.startsWith('.')
          ? child.className.split(' ').includes(selector.slice(1))
          : selector.startsWith('#')
            ? child.id === selector.slice(1)
            : child.tag === selector;
        return [...(matches ? [child] : []), ...child.querySelectorAll(selector)];
      });
    },
  };
}

function runtime(clipboard, pathname = '/ash/install.html') {
  const root = element('root');
  const guide = element('article');
  guide.setAttribute('data-copy-label', 'Copy commands');
  guide.setAttribute('data-copied-label', 'Copied!');
  guide.setAttribute('data-copy-error', 'Copy failed; copy manually.');
  root.appendChild(guide);
  for (const command of ['first command\n', 'second command\n']) {
    const block = element('pre');
    block.textContent = command;
    guide.appendChild(block);
  }
  const picker = element('select');
  picker.options = languages.map((language) => ({ value: language.path || 'en' }));
  const timers = [];
  let assigned;
  vm.runInNewContext(source, {
    URL,
    document: {
      createElement: element,
      querySelector(selector) {
        if (selector === '[data-site-script]') return { src: 'http://localhost/ash/site.js' };
        if (selector === '#site-language') return picker;
        if (selector === '.install-guide .docs-content') return guide;
        return root.querySelector(selector);
      },
      querySelectorAll: (selector) => root.querySelectorAll(selector),
    },
    navigator: { clipboard },
    window: { location: { pathname, search: '?channel=beta', hash: '#updates', assign(url) { assigned = url; } } },
    setTimeout(callback) { timers.push(callback); },
  });
  return { root, picker, timers, assigned: () => assigned };
}

test('copy buttons target each block and announce success', async () => {
  let copied;
  const app = runtime({ writeText(text) { copied = text; return Promise.resolve(); } });
  const buttons = app.root.querySelectorAll('.copy-button');
  assert.equal(buttons.length, 2);
  assert.notEqual(buttons[0].getAttribute('data-copy-target'), buttons[1].getAttribute('data-copy-target'));
  buttons[1].listeners.click();
  await new Promise(setImmediate);
  assert.equal(copied, 'second command');
  assert.equal(buttons[1].parentNode.querySelector('.copy-status').textContent, 'Copied!');
  assert.equal(buttons[1].disabled, true);
  app.timers[0]();
  assert.equal(buttons[1].textContent, 'Copy commands');
  assert.equal(buttons[1].disabled, false);
});

test('copy failure is visible for a denied or unavailable clipboard', async () => {
  for (const clipboard of [undefined, { writeText() { return Promise.reject(new Error('denied')); } }]) {
    const app = runtime(clipboard);
    const button = app.root.querySelector('.copy-button');
    button.listeners.click();
    await new Promise(setImmediate);
    assert.equal(button.parentNode.querySelector('.copy-status').textContent, 'Copy failed; copy manually.');
    assert.equal(button.textContent, 'Copy commands');
  }
});

test('language switching preserves the guide, query, and section hash', () => {
  const app = runtime(undefined, '/ash/fr/install.html');
  assert.equal(app.picker.value, 'fr');
  app.picker.value = 'ar';
  app.picker.listeners.change();
  assert.equal(app.assigned(), 'http://localhost/ash/ar/install.html?channel=beta#updates');
  app.picker.value = 'en';
  app.picker.listeners.change();
  assert.equal(app.assigned(), 'http://localhost/ash/install.html?channel=beta#updates');
});

test('all installation guides preserve anchors, commands, and locale metadata', () => {
  const sitemap = fs.readFileSync(path.join(__dirname, 'sitemap.xml'), 'utf8');
  for (const language of languages) {
    const relative = path.join(language.path, 'install.html');
    const html = fs.readFileSync(path.join(__dirname, relative), 'utf8');
    const pageIds = ids(html);
    assert.equal(new Set(pageIds).size, pageIds.length, `${relative}: duplicate IDs`);
    assert.deepEqual(pageIds, ids(english), `${relative}: anchors differ`);
    assert.deepEqual(commands(html), commands(english), `${relative}: commands differ`);
    const url = `https://jonconradt.github.io/ash/${language.path ? language.path + '/' : ''}install.html`;
    assert.ok(html.includes(`rel="canonical" href="${url}"`), `${relative}: canonical`);
    assert.ok(sitemap.includes(`<loc>${url}</loc>`), `${relative}: sitemap`);
    assert.equal([...html.matchAll(/rel="alternate" hreflang="/g)].length, languages.length + 1, `${relative}: duplicate or missing language links`);
    for (const alternate of languages) {
      const alternateURL = `https://jonconradt.github.io/ash/${alternate.path ? alternate.path + '/' : ''}install.html`;
      assert.ok(html.includes(`hreflang="${alternate.htmlLang}" href="${alternateURL}"`), `${relative}: missing reciprocal language link`);
    }
    for (const attr of ['data-copy-label', 'data-copied-label', 'data-copy-error']) {
      assert.match(html, new RegExp(`${attr}="[^"]+"`), `${relative}: missing copy label`);
    }
    if (language.path) {
      assert.ok(html.includes('href="../styles.css"'), `${relative}: shared styles`);
      assert.ok(html.includes('src="../site.js"'), `${relative}: shared script`);
    }
    if (language.path === 'ar') assert.match(html, /<html[^>]+dir="rtl"/);
  }
  assert.match(fs.readFileSync(path.join(__dirname, 'generate_locales.py'), 'utf8'), /PAGES = \([^\n]*"install.html"/);
});

test('installation and compatibility links resolve to existing pages and anchors', () => {
  for (const language of languages) {
    for (const page of ['install.html', 'index.html', 'docs.html', 'faq.html', 'developer.html']) {
      const file = path.join(__dirname, language.path, page);
      const html = fs.readFileSync(file, 'utf8');
      for (const [, href] of html.matchAll(/href="([^"]+)"/g)) {
        if (/^(?:https?:|mailto:)/.test(href)) continue;
        const [relative, anchor] = href.split('#');
        let target = relative ? path.resolve(path.dirname(file), relative) : file;
        if (href.split('#')[0].endsWith('/')) target = path.join(target, 'index.html');
        assert.ok(fs.existsSync(target), `${file}: missing ${href}`);
        if (anchor) assert.ok(ids(fs.readFileSync(target, 'utf8')).includes(anchor), `${file}: missing anchor ${href}`);
      }
    }
  }
});

test('every FAQ includes the same macOS Rust plugin installation commands', () => {
  const faq = fs.readFileSync(path.join(__dirname, 'faq.html'), 'utf8');
  const install = commands(faq).find((command) => command.includes('plugins_src/flip_a_coin'));
  assert.ok(install, 'English FAQ must include manual plugin installation commands');
  assert.ok(install.includes('build BUILD_DIR="$plugin_dir"'));
  assert.ok(install.includes('${XDG_DATA_HOME:-$HOME/.local/share}/ash/plugins'));
  assert.ok(install.includes('"$plugin_dir/flip_a_coin" --ai-docs'));
  for (const language of languages) {
    const file = path.join(__dirname, language.path, 'faq.html');
    const html = fs.readFileSync(file, 'utf8');
    assert.ok(ids(html).includes('macos-rust-plugin'), `${file}: missing Rust plugin FAQ`);
    assert.ok(commands(html).includes(install), `${file}: inconsistent installation commands`);
  }
});
