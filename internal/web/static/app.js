/* Progressive enhancement only: every page works without this file (forms, links, native selects). */
(function () {
  'use strict';
  var doc = document, reduce = window.matchMedia && matchMedia('(prefers-reduced-motion: reduce)').matches;
  var ru = (doc.documentElement.lang || '').indexOf('ru') === 0;

  // Fade out, then go: pages feel like one app instead of hard reloads.
  function go(url, delay) {
    if (reduce) { location.href = url; return; }
    doc.body.classList.add('leaving');
    setTimeout(function () { location.href = url; }, delay || 170);
  }
  window.addEventListener('pageshow', function (e) { if (e.persisted) doc.body.classList.remove('leaving'); });

  // Mark the current page in the header.
  var path = location.pathname;
  doc.querySelectorAll('header nav a[href]').forEach(function (a) {
    var h = a.getAttribute('href');
    if (h.charAt(0) !== '/') return;
    var here = h === '/' ? (path === '/' || /^\/(servers|jobs)(\/|$)/.test(path)) : (path === h || path.indexOf(h + '/') === 0);
    if (here) a.setAttribute('aria-current', 'page');
  });

  // Theme: auto -> light -> dark. The server reads the same cookie, so the next page paints right.
  var NEXT = { auto: 'light', light: 'dark', dark: 'auto' };
  function setTheme(btn, theme) {
    var root = doc.documentElement;
    root.classList.add('theming');
    setTimeout(function () { root.classList.remove('theming'); }, 450);
    var secure = location.protocol === 'https:' ? '; secure' : '';
    if (theme === 'auto') {
      root.removeAttribute('data-theme');
      doc.cookie = 'theme=; path=/; max-age=0; samesite=lax' + secure;
    } else {
      root.setAttribute('data-theme', theme);
      doc.cookie = 'theme=' + theme + '; path=/; max-age=31536000; samesite=lax' + secure;
    }
    btn.dataset.themeCur = theme;
    btn.setAttribute('href', '/theme?set=' + NEXT[theme]);
    var label = btn.getAttribute('data-l-' + theme);
    if (label) { btn.setAttribute('aria-label', label); btn.setAttribute('title', label); }
  }

  // "..." menus are <details>: close one when something else is clicked or Esc is pressed.
  doc.addEventListener('keydown', function (e) {
    if (e.key !== 'Escape') return;
    doc.querySelectorAll('details.menu[open]').forEach(function (d) { d.removeAttribute('open'); });
  });
  doc.addEventListener('toggle', function (e) {
    if (!e.target.matches || !e.target.matches('details.menu') || !e.target.open) return;
    doc.querySelectorAll('details.menu[open]').forEach(function (d) { if (d !== e.target) d.removeAttribute('open'); });
  }, true);

  doc.addEventListener('click', function (e) {
    doc.querySelectorAll('details.menu[open]').forEach(function (d) { if (!d.contains(e.target)) d.removeAttribute('open'); });
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var th = e.target.closest && e.target.closest('a.theme');
    if (th) { e.preventDefault(); setTheme(th, NEXT[th.dataset.themeCur] || 'light'); return; }
    var a = e.target.closest && e.target.closest('a[href]');
    if (!a || a.target || a.hasAttribute('download') || a.origin !== location.origin) return;
    var sw = a.closest('.lang');
    if (sw) {
      sw.setAttribute('data-lang', a.getAttribute('data-lang'));
      sw.querySelectorAll('a').forEach(function (x) { x.setAttribute('aria-current', x === a ? 'true' : 'false'); });
      e.preventDefault();
      go(a.href, 280);
      return;
    }
    if (a.pathname === location.pathname && a.search === location.search) return; // same page or an anchor
    e.preventDefault();
    go(a.href);
  });

  // Dropdown over every native <select>, searchable once the list is long.
  function el(tag, cls) { var n = doc.createElement(tag); if (cls) n.className = cls; return n; }

  function enhance(sel) {
    if (sel.multiple || sel.size > 1 || sel.dataset.dd === 'off') return;
    var wrap = el('div', 'dd'), btn = el('button', 'dd-btn'), label = el('span');
    var list = el('div', 'dd-list'), box = el('div', 'dd-items'), search = null;
    var opts = [].slice.call(sel.options), items = [], visible = [], active = -1, isOpen = false;

    sel.parentNode.insertBefore(wrap, sel);
    wrap.appendChild(sel);
    sel.classList.add('dd-native');
    sel.tabIndex = -1;
    sel.setAttribute('aria-hidden', 'true');
    btn.type = 'button';
    btn.setAttribute('aria-haspopup', 'listbox');
    btn.setAttribute('aria-expanded', 'false');
    btn.appendChild(label);
    list.hidden = true;
    list.setAttribute('role', 'listbox');
    if (opts.length > 8) {
      search = el('input', 'dd-search');
      search.type = 'search';
      search.placeholder = ru ? 'Поиск…' : 'Search…';
      search.setAttribute('aria-label', search.placeholder);
      search.autocomplete = 'off';
      list.appendChild(search);
    }
    list.appendChild(box);
    wrap.appendChild(btn);
    wrap.appendChild(list);
    if (sel.id) {
      var lab = doc.querySelector('label[for="' + sel.id + '"]');
      if (lab) lab.addEventListener('click', function (e) { e.preventDefault(); btn.focus(); });
    }

    opts.forEach(function (o, i) {
      if (o.disabled) return;
      var it = el('div', 'dd-item');
      it.textContent = o.text;
      it.setAttribute('role', 'option');
      it.dataset.i = i;
      it.addEventListener('mousedown', function (e) { e.preventDefault(); });
      it.addEventListener('click', function () { choose(i); });
      it.addEventListener('mousemove', function () { setActive(visible.indexOf(it), true); });
      items.push(it);
    });
    var empty = el('div', 'dd-empty');
    empty.textContent = ru ? 'Ничего не найдено' : 'Nothing found';
    empty.hidden = true;
    box.appendChild(empty);
    items.forEach(function (it) { box.appendChild(it); });

    function sync() {
      var o = sel.options[sel.selectedIndex];
      label.textContent = o ? o.text : '';
      items.forEach(function (it) {
        var on = +it.dataset.i === sel.selectedIndex;
        it.classList.toggle('sel', on);
        it.setAttribute('aria-selected', on ? 'true' : 'false');
      });
    }
    function setActive(n, noScroll) {
      if (!visible.length) { active = -1; return; }
      n = Math.max(0, Math.min(visible.length - 1, n));
      if (active >= 0 && visible[active]) visible[active].classList.remove('active');
      active = n;
      visible[n].classList.add('active');
      if (!noScroll) visible[n].scrollIntoView({ block: 'nearest' });
    }
    function filter() {
      var q = search ? search.value.trim().toLowerCase() : '';
      visible = items.filter(function (it) {
        var show = !q || it.textContent.toLowerCase().indexOf(q) >= 0;
        it.hidden = !show;
        it.classList.remove('active');
        return show;
      });
      empty.hidden = visible.length > 0;
      active = -1;
      var cur = visible.filter(function (it) { return it.classList.contains('sel'); })[0];
      setActive(cur ? visible.indexOf(cur) : 0);
    }
    function open() {
      if (isOpen) return;
      isOpen = true;
      if (search) search.value = '';
      list.hidden = false;
      wrap.classList.add('open');
      btn.setAttribute('aria-expanded', 'true');
      var r = wrap.getBoundingClientRect();
      wrap.classList.toggle('up', innerHeight - r.bottom < 300 && r.top > innerHeight - r.bottom);
      filter();
      if (search) search.focus();
    }
    function close(refocus) {
      if (!isOpen) return;
      isOpen = false;
      list.hidden = true;
      wrap.classList.remove('open', 'up');
      btn.setAttribute('aria-expanded', 'false');
      if (refocus) btn.focus();
    }
    function choose(i) {
      if (sel.selectedIndex !== i) {
        sel.selectedIndex = i;
        sel.dispatchEvent(new Event('change', { bubbles: true }));
      }
      sync();
      close(true);
    }

    btn.addEventListener('click', function () { isOpen ? close(true) : open(); });
    if (search) search.addEventListener('input', filter);
    wrap.addEventListener('keydown', function (e) {
      var k = e.key;
      if (!isOpen) {
        if (e.target !== btn) return;
        if (k === 'ArrowDown' || k === 'ArrowUp') { e.preventDefault(); open(); }
        else if (search && k.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey && k !== ' ') { open(); }
        return;
      }
      if (k === 'ArrowDown') { e.preventDefault(); setActive(active + 1); }
      else if (k === 'ArrowUp') { e.preventDefault(); setActive(active - 1); }
      else if (k === 'Home') { e.preventDefault(); setActive(0); }
      else if (k === 'End') { e.preventDefault(); setActive(visible.length - 1); }
      else if (k === 'Enter' || (k === ' ' && !search)) { e.preventDefault(); if (visible[active]) choose(+visible[active].dataset.i); }
      else if (k === 'Escape') { e.preventDefault(); close(true); }
      else if (k === 'Tab') { close(false); }
      else if (!search && k.length === 1) { // type-ahead on a short list
        var q = k.toLowerCase(), from = active + 1;
        for (var n = 0; n < visible.length; n++) {
          var j = (from + n) % visible.length;
          if (visible[j].textContent.toLowerCase().indexOf(q) === 0) { setActive(j); break; }
        }
      }
    });
    doc.addEventListener('mousedown', function (e) { if (isOpen && !wrap.contains(e.target)) close(false); });
    sel.form && sel.form.addEventListener('reset', function () { setTimeout(sync, 0); });
    sync();
  }
  doc.querySelectorAll('select').forEach(enhance);
})();

// Pages that mark a block data-autorefresh="N" are re-fetched every N seconds and the block swapped in place.
(function () {
  var box = document.querySelector("[data-autorefresh]");
  if (!box || !window.fetch || !window.DOMParser) return;
  var secs = +box.getAttribute("data-autorefresh") || 10, busy = false;
  setInterval(function () {
    var ae = document.activeElement;
    if (document.hidden || busy || document.querySelector("details.menu[open], details.pop[open], dialog[open], .ctx")) return;
    if (ae && box.contains(ae) && /^(INPUT|TEXTAREA|SELECT)$/.test(ae.tagName)) return; // never wipe what is being typed
    if ([].some.call(box.querySelectorAll("input[type=text]"), function (i) { return i.value; })) return;
    busy = true;
    fetch(location.href, { credentials: "same-origin" }).then(function (r) { return r.text(); }).then(function (t) {
      var n = new DOMParser().parseFromString(t, "text/html").querySelector("[data-autorefresh]");
      if (!n || !box.isConnected) return;
      var pre = box.querySelector("pre.log"), atEnd = pre && pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 8;
      box.innerHTML = n.innerHTML;
      var np = box.querySelector("pre.log");
      if (np && atEnd) np.scrollTop = np.scrollHeight;
    }).catch(function () {}).then(function () { busy = false; });
  }, secs * 1000);
})();

// Server hub, servers list and file manager. Everything is delegated to the
// document, so blocks swapped in by the auto-refresh keep working.
(function () {
  'use strict';
  var doc = document, root = doc.documentElement;
  var ru = (root.lang || '').indexOf('ru') === 0;
  root.classList.add('js');

  // Toolbar popovers (<details class="pop">): one open at a time, closed by Esc or a click outside.
  doc.addEventListener('toggle', function (e) {
    var d = e.target;
    if (!d.matches || !d.matches('details.pop') || !d.open) return;
    doc.querySelectorAll('details.pop[open]').forEach(function (x) { if (x !== d) x.removeAttribute('open'); });
    var f = d.querySelector('input:not([type=hidden])');
    if (f) setTimeout(function () { f.focus(); }, 30);
  }, true);
  doc.addEventListener('click', function (e) {
    doc.querySelectorAll('details.pop[open]').forEach(function (d) { if (!d.contains(e.target)) d.removeAttribute('open'); });
  });

  // Copy buttons.
  function copy(text, btn) {
    var done = function () {
      if (!btn) return;
      btn.classList.add('copied');
      var t = btn.getAttribute('title');
      btn.setAttribute('title', ru ? 'Скопировано' : 'Copied');
      setTimeout(function () { btn.classList.remove('copied'); if (t) btn.setAttribute('title', t); }, 1400);
    };
    if (navigator.clipboard && window.isSecureContext) { navigator.clipboard.writeText(text).then(done, done); return; }
    var ta = doc.createElement('textarea');
    ta.value = text; doc.body.appendChild(ta); ta.select();
    try { doc.execCommand('copy'); } catch (err) {}
    doc.body.removeChild(ta); done();
  }
  doc.addEventListener('click', function (e) {
    var b = e.target.closest && e.target.closest('[data-copy]');
    if (!b) return;
    e.preventDefault();
    copy(b.getAttribute('data-copy'), b);
    closeCtx();
  });

  // Forms with data-confirm ask first, in a small dialog instead of the browser's prompt.
  function ask(text, ok) {
    if (!window.HTMLDialogElement) { if (window.confirm(text)) ok(); return; }
    var dlg = doc.createElement('dialog');
    dlg.className = 'confirm';
    var p = doc.createElement('p'); p.textContent = text;
    var row = doc.createElement('div'); row.className = 'btns';
    var no = doc.createElement('button'); no.type = 'button'; no.className = 'btn'; no.textContent = ru ? 'Отмена' : 'Cancel';
    var yes = doc.createElement('button'); yes.type = 'button'; yes.className = 'btn stop'; yes.textContent = ru ? 'Да' : 'Yes';
    row.appendChild(no); row.appendChild(yes); dlg.appendChild(p); dlg.appendChild(row);
    doc.body.appendChild(dlg);
    var close = function () { dlg.close(); dlg.remove(); };
    no.addEventListener('click', close);
    yes.addEventListener('click', function () { close(); ok(); });
    dlg.addEventListener('cancel', function () { dlg.remove(); });
    dlg.showModal();
    yes.focus();
  }
  doc.addEventListener('submit', function (e) {
    var f = e.target;
    if (!f.hasAttribute || !f.hasAttribute('data-confirm') || f.dataset.confirmed) return;
    e.preventDefault();
    ask(f.getAttribute('data-confirm'), function () { f.dataset.confirmed = '1'; f.submit(); });
  }, true);

  // A click on a server row (not on a control) opens its hub.
  doc.addEventListener('click', function (e) {
    var row = e.target.closest && e.target.closest('.srv[data-href]');
    if (!row || e.defaultPrevented || e.button !== 0 || e.target.closest('a, button, summary, form, details, input')) return;
    location.href = row.getAttribute('data-href');
  });

  // File manager: selection, download of one selected file, context menu, drag and drop upload.
  var list = doc.getElementById('fm-list');
  function selection() {
    return [].slice.call(doc.querySelectorAll('#fm-list tr.fm-row input[type=checkbox]:checked')).map(function (c) { return c.closest('tr'); });
  }
  function syncSel() {
    doc.querySelectorAll('#fm-list tr.fm-row').forEach(function (tr) {
      var c = tr.querySelector('input[type=checkbox]');
      tr.classList.toggle('sel', !!(c && c.checked));
    });
    var dl = doc.querySelector('[data-fm-dl]'), sel = selection();
    if (dl) dl.disabled = !(sel.length === 1 && sel[0].hasAttribute('data-dl'));
  }
  doc.addEventListener('change', function (e) { if (e.target.closest && e.target.closest('#fm-list')) syncSel(); });
  doc.addEventListener('click', function (e) {
    var dl = e.target.closest && e.target.closest('[data-fm-dl]');
    if (!dl) return;
    var sel = selection();
    if (sel.length === 1 && sel[0].hasAttribute('data-dl')) location.href = sel[0].getAttribute('data-dl');
  });
  syncSel();

  var ctx = null;
  function closeCtx() { if (ctx) { ctx.remove(); ctx = null; } }
  function openCtx(row, x, y) {
    closeCtx();
    var src = row.querySelector('.menu-list');
    if (!src) return;
    ctx = doc.createElement('div');
    ctx.className = 'ctx';
    ctx.setAttribute('role', 'menu');
    [].forEach.call(src.children, function (it) {
      var c = it.cloneNode(true);
      c.setAttribute('role', 'menuitem');
      c.tabIndex = -1;
      ctx.appendChild(c);
    });
    doc.body.appendChild(ctx);
    var w = ctx.offsetWidth, h = ctx.offsetHeight;
    ctx.style.left = Math.max(8, Math.min(x, innerWidth - w - 8)) + 'px';
    ctx.style.top = Math.max(8, Math.min(y, innerHeight - h - 8)) + 'px';
    var first = ctx.querySelector('.menu-item');
    if (first) first.focus();
  }
  doc.addEventListener('contextmenu', function (e) {
    var row = e.target.closest && e.target.closest('#fm-list tr.fm-row');
    if (!row) { closeCtx(); return; }
    e.preventDefault();
    openCtx(row, e.clientX, e.clientY);
  });
  doc.addEventListener('keydown', function (e) {
    if (ctx) {
      var items = [].slice.call(ctx.querySelectorAll('.menu-item')), i = items.indexOf(doc.activeElement);
      if (e.key === 'Escape') { e.preventDefault(); closeCtx(); return; }
      if (e.key === 'ArrowDown') { e.preventDefault(); items[(i + 1) % items.length].focus(); return; }
      if (e.key === 'ArrowUp') { e.preventDefault(); items[(i - 1 + items.length) % items.length].focus(); return; }
      if (e.key === 'Tab') { closeCtx(); return; }
    }
    if (e.key === 'Escape') doc.querySelectorAll('details.pop[open]').forEach(function (d) { d.removeAttribute('open'); });
    if (e.key === 'ContextMenu' || (e.key === 'F10' && e.shiftKey)) {
      var row = doc.activeElement && doc.activeElement.closest && doc.activeElement.closest('#fm-list tr.fm-row');
      if (!row) return;
      e.preventDefault();
      var r = row.getBoundingClientRect();
      openCtx(row, r.left + 40, r.bottom - 4);
    }
  });
  doc.addEventListener('click', function (e) { if (ctx && !ctx.contains(e.target)) closeCtx(); });
  addEventListener('scroll', closeCtx, true);
  addEventListener('resize', closeCtx);

  var drop = doc.querySelector('.fm-drop[data-upload]');
  if (drop && window.FormData && window.XMLHttpRequest) {
    var depth = 0;
    var hasFiles = function (e) { return e.dataTransfer && [].indexOf.call(e.dataTransfer.types || [], 'Files') >= 0; };
    drop.addEventListener('dragenter', function (e) { if (!hasFiles(e)) return; e.preventDefault(); depth++; drop.classList.add('over'); });
    drop.addEventListener('dragover', function (e) { if (hasFiles(e)) { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; } });
    drop.addEventListener('dragleave', function () { if (--depth <= 0) { depth = 0; drop.classList.remove('over'); } });
    drop.addEventListener('drop', function (e) {
      if (!hasFiles(e)) return;
      e.preventDefault();
      depth = 0; drop.classList.remove('over');
      var files = e.dataTransfer.files;
      if (!files.length) return;
      // the server reads the CSRF field first and streams the rest, so the order matters
      var fd = new FormData();
      fd.append('csrf', drop.getAttribute('data-csrf'));
      fd.append('path', drop.getAttribute('data-path'));
      for (var i = 0; i < files.length; i++) fd.append('file', files[i], files[i].name);
      var bar = doc.createElement('div'); bar.className = 'fm-progress';
      var label = doc.createElement('span'); label.textContent = drop.getAttribute('data-up-label') + '…';
      var prog = doc.createElement('progress'); prog.max = 100; prog.value = 0;
      bar.appendChild(label); bar.appendChild(prog);
      drop.parentNode.insertBefore(bar, drop);
      var xhr = new XMLHttpRequest();
      xhr.open('POST', drop.getAttribute('data-upload'));
      xhr.upload.onprogress = function (ev) {
        if (!ev.lengthComputable) return;
        prog.value = Math.round(100 * ev.loaded / ev.total);
        label.textContent = drop.getAttribute('data-up-label') + ' ' + prog.value + '%';
      };
      xhr.onloadend = function () { location.href = xhr.responseURL || location.href; };
      xhr.send(fd);
    });
  }
  if (list) list.addEventListener('dblclick', function (e) {
    var row = e.target.closest('tr.fm-row');
    if (!row || e.target.closest('input, a, summary, button')) return;
    var a = row.querySelector('.fm-name a');
    if (a) location.href = a.href;
  });
})();
