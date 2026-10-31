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
