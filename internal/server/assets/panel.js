/* Open Go Panel: progressive HTMX enhancement and page-local controls.
   No framework or persistent browser data. Native links/forms remain the fallback. */
(() => {
  'use strict';

  const unsafePath = (path) =>
    path.startsWith('/db-admin') ||
    path === '/terminal' ||
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
          form.enctype === 'multipart/form-data' || path.startsWith('/db-admin/') ||
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
    const nav = document.querySelector('.nav');
    if (!nav || nav.dataset.ogpNavReady === '1') return;
    nav.dataset.ogpNavReady = '1';
    if (matchMedia('(max-width:1050px)').matches) {
      for (const group of nav.querySelectorAll('details.current')) group.open = false;
    }
  }

  function closeMenus(except) {
    document.querySelectorAll('.fileman-more[open]').forEach((details) => {
      if (details !== except) details.open = false;
    });
  }

  function positionMenu(details) {
    if (!details.open) return;
    const trigger = details.querySelector('summary');
    const popup = details.querySelector('.fileman-popover');
    if (!trigger || !popup) return;
    const rect = trigger.getBoundingClientRect();
    const gutter = 12;
    const width = Math.min(340, innerWidth - gutter * 2);
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
    root.querySelectorAll('.fileman-more').forEach((details) => {
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

  function initUI(root = document) {
    enhanceLinks(root);
    initTables(root);
    initSidebar();
    initFileMenus(root);
    initFileSelection(root);
  }

  document.addEventListener('click', (event) => {
    const nav = document.querySelector('.nav');
    if (nav && !nav.contains(event.target) && matchMedia('(max-width:1050px)').matches) {
      nav.querySelectorAll('details[open]').forEach((details) => { details.open = false; });
    }
    if (!event.target.closest('.fileman-more')) closeMenus();
  });
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') closeMenus();
  });
  addEventListener('scroll', () => {
    document.querySelectorAll('.fileman-more[open]').forEach(positionMenu);
  }, true);
  addEventListener('resize', () => {
    document.querySelectorAll('.fileman-more[open]').forEach(positionMenu);
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
