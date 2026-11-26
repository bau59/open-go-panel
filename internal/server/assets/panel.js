/* Open Go Panel: progressive HTMX enhancement and page-local controls.
   No framework. Only the selected section is kept in session storage; native links/forms remain the fallback. */
(() => {
  'use strict';

  const unsafePath = (path) =>
    path.startsWith('/db-admin') ||
    path === '/terminal' ||
    path.startsWith('/terminal/background') ||
    path.startsWith('/assets/') ||
    /\/download(?:\/|$)/.test(path) ||
    path === '/backup/panel.json' ||
    path === '/backup/software.sh' ||
    path === '/backup/docker.json' ||
    /^\/backup\/users\/[^/]+\/(?:config|archive)$/.test(path);

  function enhanceLinks(root) {
    root.querySelectorAll('a[href]').forEach((link) => {
      if (link.hasAttribute('download') || link.target === '_blank') {
        link.setAttribute('hx-boost', 'false');
        return;
      }
      let url;
      try {
        url = new URL(link.href, document.baseURI);
      } catch (_) {
        link.setAttribute('hx-boost', 'false');
        return;
      }
      if (url.origin !== location.origin || unsafePath(url.pathname)) {
        link.setAttribute('hx-boost', 'false');
      }
    });

    root.querySelectorAll('form').forEach((form) => {
      const path = new URL(form.action, document.baseURI).pathname;
      if (path === '/logout' || path === '/login' || path === '/panel/close' ||
          form.enctype === 'multipart/form-data' || path.startsWith('/db-admin/') || path.startsWith('/terminal/background/') ||
          path.endsWith('/files') || form.querySelector('input[type="file"]')) {
        form.setAttribute('hx-boost', 'false');
      }
    });
  }

  function initTables(root) {
    root.querySelectorAll('table').forEach((table) => {
      if (table.dataset.noPager === '1' || table.dataset.ogpPagerReady === '1') return;
      const tbody = table.tBodies[0];
      if (!tbody) return;
      const allRows = Array.from(tbody.rows);
      if (allRows.length <= 25) return;
      table.dataset.ogpPagerReady = '1';
      let page = 1;
      let perPage = 25;
      let query = '';

      const tools = document.createElement('div');
      tools.className = 'table-tools';
      tools.innerHTML = '<input class="table-tools-search" type="search" placeholder="Search this table" aria-label="Search table"><div class="table-tools-right"><span class="pager-info"></span><select aria-label="Rows per page"><option>25</option><option>50</option><option>100</option></select><button type="button" class="secondary" data-prev>Previous</button><button type="button" class="secondary" data-next>Next</button></div>';
      table.parentNode.insertBefore(tools, table);

      const search = tools.querySelector('input');
      const select = tools.querySelector('select');
      const info = tools.querySelector('.pager-info');
      const prev = tools.querySelector('[data-prev]');
      const next = tools.querySelector('[data-next]');

      const render = () => {
        const filtered = allRows.filter((row) => row.textContent.toLowerCase().includes(query));
        const pages = Math.max(1, Math.ceil(filtered.length / perPage));
        page = Math.min(Math.max(1, page), pages);
        const start = (page - 1) * perPage;
        const visible = new Set(filtered.slice(start, start + perPage));
        allRows.forEach((row) => { row.hidden = !visible.has(row); });
        table.dispatchEvent(new Event('ogp:table-render'));
        info.textContent = filtered.length + ' items · page ' + page + ' of ' + pages;
        prev.disabled = page <= 1;
        next.disabled = page >= pages;
      };

      search.addEventListener('input', () => {
        query = search.value.trim().toLowerCase();
        page = 1;
        render();
      });
      select.addEventListener('change', () => {
        perPage = Number(select.value) || 25;
        page = 1;
        render();
      });
      prev.addEventListener('click', () => { if (page > 1) page--; render(); });
      next.addEventListener('click', () => { page++; render(); });
      render();
    });
  }

  function initSidebar() {
    const header = document.querySelector('.topbar');
    const toggle = header?.querySelector('.mobile-nav-toggle');
    if (!toggle || header.dataset.mobileReady === '1') return;
    header.dataset.mobileReady = '1';
    toggle.addEventListener('click', () => {
      const open = header.classList.toggle('mobile-nav-open');
      toggle.setAttribute('aria-expanded', String(open));
    });
  }

  function closeMobileNav() {
    const header = document.querySelector('.topbar');
    if (!header) return;
    header.classList.remove('mobile-nav-open');
    header.querySelector('.mobile-nav-toggle')?.setAttribute('aria-expanded', 'false');
  }

  function closeMenus(except) {
    document.querySelectorAll('.fileman-more[open], .db-action-menu[open]').forEach((details) => {
      if (details !== except) details.open = false;
    });
  }

  function positionMenu(details) {
    if (!details.open) return;
    const trigger = details.querySelector('summary');
    const popup = details.querySelector('.fileman-popover, .inline-popover');
    if (!trigger || !popup) return;
    const rect = trigger.getBoundingClientRect();
    const gutter = 12;
    const width = Math.min(details.matches(".db-action-menu") ? 520 : 340, innerWidth - gutter * 2);
    popup.style.width = width + 'px';
    popup.style.left = Math.max(gutter, Math.min(rect.right - width, innerWidth - width - gutter)) + 'px';
    popup.style.top = gutter + 'px';
    const height = popup.getBoundingClientRect().height;
    const below = innerHeight - rect.bottom - gutter;
    const above = rect.top - gutter;
    const top = below >= Math.min(height, 300) || below >= above
      ? Math.min(rect.bottom + 8, innerHeight - gutter - height)
      : Math.max(gutter, rect.top - 8 - height);
    popup.style.top = Math.max(gutter, top) + 'px';
  }

  function initFileMenus(root) {
    root.querySelectorAll('.fileman-more, .db-action-menu').forEach((details) => {
      if (details.dataset.ogpMenuReady === '1') return;
      details.dataset.ogpMenuReady = '1';
      details.addEventListener('toggle', () => {
        if (!details.open) return;
        closeMenus(details);
        positionMenu(details);
      });
    });
  }

  function initFileSelection(root) {
    root.querySelectorAll('.fileman-panel').forEach((panel) => {
      const master = panel.querySelector('#fileman-select-all');
      if (!master || master.dataset.ogpSelectionReady === '1') return;
      master.dataset.ogpSelectionReady = '1';
      const items = Array.from(panel.querySelectorAll('.fileman-select-item'));
      const count = panel.querySelector('#fileman-selection-count');
      const form = panel.querySelector('#fileman-bulk-form');
      const table = panel.querySelector('.fileman-table table');
      if (!count || !form || !table) return;
      const buttons = Array.from(form.querySelectorAll('button[name="action"]'));
      const update = () => {
        const visible = items.filter((item) => !item.closest('tr').hidden);
        const selected = items.filter((item) => item.checked).length;
        const visibleSelected = visible.filter((item) => item.checked).length;
        count.textContent = selected + ' selected';
        master.checked = visible.length > 0 && visibleSelected === visible.length;
        master.indeterminate = visibleSelected > 0 && visibleSelected < visible.length;
        buttons.forEach((button) => { button.disabled = selected === 0; });
      };
      master.addEventListener('change', () => {
        items.forEach((item) => {
          if (!item.closest('tr').hidden) item.checked = master.checked;
        });
        update();
      });
      items.forEach((item) => item.addEventListener('change', update));
      table.addEventListener('ogp:table-render', update);
      update();
    });
  }

  function initDatabaseOffsiteForm(root) {
    root.querySelectorAll('#db-offsite-provider').forEach((select) => {
      if (select.dataset.ogpReady === '1') return;
      select.dataset.ogpReady = '1';
      const form = select.closest('form');
      if (!form) return;
      const update = () => {
        const provider = select.value;
        const s3 = ['s3', 'r2', 'b2'].includes(provider);
        const sftp = provider === 'sftp';
        const groups = [
          [form.querySelector('[data-offsite-s3]'), s3],
          [form.querySelector('[data-offsite-sftp]'), sftp],
          [form.querySelector('[data-offsite-yandex]'), provider === 'yandex']
        ];
        groups.forEach(([group, visible]) => {
          if (!group) return;
          group.hidden = !visible;
          group.querySelectorAll('input,select').forEach((input) => { input.disabled = !visible; });
        });
      };
      select.addEventListener('change', update);
      update();
    });
  }

  // Section tabs are presentation only. All original forms stay in the DOM;
  // without JavaScript all sections remain available as ordinary panels.
  const tabLabels = {
    overview:'Overview', deploy:'Deploy', databases:'Databases', cron:'Scheduled tasks',
    chatgpt:'ChatGPT access', settings:'Settings', domains:'Domains',
    configuration:'Configuration', tools:'Tools', blocked:'Blocked IPs',
    alerts:'Alerts', trusted:'Trusted IPs', keys:'Keys', software:'Software',
    php:'PHP runtimes', containers:'Containers', create:'Create container',
    versions:'Previous versions', routes:'Slow routes', requests:'Request log',
    retention:'Retention', sources:'Logging sources'
  };
  const tabControllers = new WeakMap();
  function fragmentTab(main) {
    let hash;
    try { hash = decodeURIComponent(location.hash.slice(1)); } catch (_) { return ''; }
    if (hash.startsWith('tab-')) return hash.slice(4);
    const target = hash && document.getElementById(hash);
    return target && main.contains(target) ? target.closest('[data-panel-tab]')?.dataset.panelTab || '' : '';
  }
  function initPageTabs() {
    const main = document.querySelector('main[data-panel-tabs]');
    if (!main || tabControllers.has(main)) return;
    const panels = Array.from(main.querySelectorAll('[data-panel-tab]'));
    const keys = main.dataset.panelTabs.split(',').filter(key => panels.some(panel => panel.dataset.panelTab === key));
    main.querySelectorAll(':scope > .service-tabs').forEach(nav => nav.remove());
    if (keys.length < 2) { panels.forEach(panel => { panel.hidden = false; }); return; }
    const storageKey = 'ogp.tab.' + main.dataset.panelKey;
    const nav = document.createElement('nav');
    nav.className = 'backup-tabs service-tabs';
    nav.setAttribute('aria-label', 'Page sections');
    const buttons = new Map();
    let active;
    function select(key, remember = true) {
      if (!keys.includes(key)) return;
      active = key;
      panels.forEach(panel => { panel.hidden = panel.dataset.panelTab !== key; });
      buttons.forEach((button, id) => {
        button.classList.toggle('active', id === key);
        button.setAttribute('aria-pressed', String(id === key));
      });
      if (remember) {
        try { sessionStorage.setItem(storageKey, key); } catch (_) {}
      }
    }
    keys.forEach(key => {
      const button = document.createElement('button');
      button.type = 'button';
      button.textContent = tabLabels[key] || key;
      button.addEventListener('click', () => {
        select(key);
        history.replaceState(history.state, '', '#tab-' + key);
      });
      buttons.set(key, button);
      nav.appendChild(button);
    });
    nav.addEventListener('keydown', event => {
      const index = keys.indexOf(active);
      const next = event.key === 'ArrowRight' ? (index + 1) % keys.length :
        event.key === 'ArrowLeft' ? (index + keys.length - 1) % keys.length : -1;
      if (next < 0) return;
      event.preventDefault();
      buttons.get(keys[next]).click();
      buttons.get(keys[next]).focus();
    });
    let first = panels[0];
    while (first.parentElement !== main) first = first.parentElement;
    main.insertBefore(nav, first);
    let saved = '';
    try { saved = sessionStorage.getItem(storageKey) || ''; } catch (_) {}
    const fragment = fragmentTab(main);
    const initial = main.dataset.panelInitial;
    select(keys.includes(fragment) ? fragment : keys.includes(initial) ? initial : keys.includes(saved) ? saved : keys[0]);
    tabControllers.set(main, {select, keys});
  }
  function revealTabFragment() {
    const main = document.querySelector('main[data-panel-tabs]');
    if (!main) return;
    const controller = tabControllers.get(main);
    const key = fragmentTab(main);
    if (controller?.keys.includes(key)) controller.select(key);
  }
  document.addEventListener('click', event => {
    const link = event.target.closest('a[href^="#"]');
    if (!link) return;
    let id;
    try { id = decodeURIComponent(link.getAttribute('href').slice(1)); } catch (_) { return; }
    const target = document.getElementById(id);
    const main = target?.closest('main[data-panel-tabs]');
    const panel = target?.closest('[data-panel-tab]');
    if (main && panel) tabControllers.get(main)?.select(panel.dataset.panelTab);
  }, true);
  addEventListener('hashchange', revealTabFragment);
  document.addEventListener('htmx:afterSettle', revealTabFragment);
  document.addEventListener('htmx:historyRestore', () => { initPageTabs(); revealTabFragment(); });

  let editorLoad;
  function initCodeEditor() {
    if (!document.querySelector('form[data-code-editor]')) return;
    if (window.ogpInitCodeEditors) { window.ogpInitCodeEditors(); return; }
    if (!editorLoad) {
      const stylesheet = document.createElement('link');
      stylesheet.rel = 'stylesheet'; stylesheet.href = '/assets/editor.css';
      const script = document.createElement('script');
      script.src = '/assets/editor.js';
      editorLoad = Promise.all([
        new Promise((resolve,reject) => { stylesheet.onload=resolve; stylesheet.onerror=reject; document.head.appendChild(stylesheet); }),
        new Promise((resolve,reject) => { script.onload=resolve; script.onerror=reject; document.head.appendChild(script); })
      ]).catch(() => {
        stylesheet.remove(); script.remove(); editorLoad = null;
        // Keep the original textarea usable if assets cannot load.
        return false;
      });
    }
    editorLoad.then(result => { if (result !== false) window.ogpInitCodeEditors?.(); });
  }

  function initUI(root = document) {
    enhanceLinks(root);
    initTables(root);
    initSidebar();
    initFileMenus(root);
    initFileSelection(root);
    initDatabaseOffsiteForm(root);
    initPageTabs();
    initCodeEditor();
  }

  document.addEventListener('click', (event) => {
    if (!event.target.closest('.topbar') || event.target.closest('.nav a')) closeMobileNav();
    if (!event.target.closest('.fileman-more, .db-action-menu')) closeMenus();
  });
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') { closeMenus(); closeMobileNav(); }
  });
  addEventListener('scroll', () => {
    document.querySelectorAll('.fileman-more[open], .db-action-menu[open]').forEach(positionMenu);
  }, true);
  addEventListener('resize', () => {
    document.querySelectorAll('.fileman-more[open], .db-action-menu[open]').forEach(positionMenu);
  });

  const showError = (message) => {
    let notice = document.getElementById('panel-notice');
    if (!notice) {
      notice = document.createElement('div');
      notice.id = 'panel-notice';
      notice.setAttribute('role', 'alert');
      document.body.appendChild(notice);
    }
    notice.textContent = message;
    notice.hidden = false;
  };

  document.addEventListener('htmx:responseError', (event) => {
    const xhr = event.detail.xhr;
    let message = 'Request failed (' + xhr.status + '). No changes were applied to this view.';
    const html = new DOMParser().parseFromString(xhr.responseText || '', 'text/html');
    const alert = html.querySelector('.alert');
    if (alert?.textContent?.trim()) {
      message = alert.textContent.trim();
    } else if (html.body?.textContent?.trim() && html.body.textContent.trim().length < 450) {
      message = html.body.textContent.trim();
    }
    showError(message);
  });
  document.addEventListener('htmx:sendError', () => {
    showError('Connection failed. Check panel availability and retry.');
  });
  document.addEventListener('htmx:beforeSwap', (event) => {
    if (event.detail.xhr?.responseURL) {
      try {
        const url = new URL(event.detail.xhr.responseURL);
        if (url.pathname === '/login' && location.pathname !== '/login') {
          event.detail.shouldSwap = false;
          location.assign('/login');
        }
      } catch (_) {}
    }
  });
  document.addEventListener('htmx:afterSwap', (event) => {
    if (event.detail.target === document.body) {
      initUI(document);
    } else {
      initUI(event.detail.target);
    }
  });
  document.addEventListener('DOMContentLoaded', () => {
    initUI(document);
  });
})();
