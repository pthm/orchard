'use strict';

{{ $searchDataFile := printf "%s.search-data.json" .Language.Name }}
{{ $searchData := resources.Get "search-data.json" | resources.ExecuteAsTemplate $searchDataFile . | resources.Minify | resources.Fingerprint }}

(function () {
  var searchDataURL = '{{ partial "docs/links/resource-precache" $searchData }}';

  var input = document.querySelector('#book-search-input');
  var resultsEl = document.querySelector('#book-search-results');
  var hint = document.querySelector('.orchard-search-hint');

  if (!input) return;

  var index = null;
  var sections = [];

  input.addEventListener('focus', init);
  input.addEventListener('input', search);
  input.addEventListener('keydown', handleKey);
  document.addEventListener('keypress', focusOnHotkey);
  document.addEventListener('click', closeOnClickOutside);

  function focusOnHotkey(e) {
    if (e.target.value !== undefined) return;
    if (input === document.activeElement) return;
    var hotkeys = input.getAttribute('data-hotkeys') || '';
    if (hotkeys.indexOf(String.fromCharCode(e.charCode)) >= 0) {
      input.focus();
      e.preventDefault();
    }
  }

  function closeOnClickOutside(e) {
    if (!e.target.closest('.orchard-search')) {
      resultsEl.classList.add('hidden');
    }
  }

  function handleKey(e) {
    if (e.key === 'Escape') {
      input.blur();
      resultsEl.classList.add('hidden');
    }
  }

  function init() {
    input.removeEventListener('focus', init);
    if (hint) hint.style.display = 'none';
    input.required = true;

    fetch(searchDataURL)
      .then(function (r) { return r.json(); })
      .then(function (pages) {
        index = new FlexSearch.Document({
          document: {
            id: 'id',
            index: ['title', 'content'],
            store: true
          },
          tokenize: 'forward',
          resolution: 9
        });

        var id = 0;
        pages.forEach(function (page) {
          var headings = page.headings || [];
          var plain = page.content || '';

          if (headings.length === 0) {
            var entry = {
              id: id++,
              href: page.href,
              title: page.title,
              section: page.section,
              content: plain.slice(0, 500)
            };
            sections.push(entry);
            index.add(entry);
            return;
          }

          // Page intro before first heading.
          var firstPos = plain.indexOf(headings[0].title);
          if (firstPos > 50) {
            var intro = {
              id: id++,
              href: page.href,
              title: page.title,
              section: page.section,
              content: plain.slice(0, Math.min(firstPos, 500))
            };
            sections.push(intro);
            index.add(intro);
          }

          // Per-heading sections.
          for (var i = 0; i < headings.length; i++) {
            var h = headings[i];
            var pos = plain.indexOf(h.title);
            if (pos < 0) continue;

            var nextPos = plain.length;
            if (i + 1 < headings.length) {
              var np = plain.indexOf(headings[i + 1].title, pos + h.title.length);
              if (np > pos) nextPos = np;
            }
            var snippet = plain.slice(pos, Math.min(pos + 400, nextPos));

            var sectionEntry = {
              id: id++,
              href: page.href + '#' + h.id,
              title: h.title,
              section: page.title,
              content: snippet
            };
            sections.push(sectionEntry);
            index.add(sectionEntry);
          }
        });
      })
      .then(function () { input.required = false; })
      .then(search);
  }

  function search() {
    resultsEl.innerHTML = '';

    if (!input.value || !index) {
      resultsEl.classList.add('hidden');
      return;
    }

    var hits = index.search(input.value, { limit: 15, enrich: true });

    var seen = {};
    var merged = [];
    hits.forEach(function (fieldResult) {
      fieldResult.result.forEach(function (entry) {
        if (!seen[entry.id]) {
          seen[entry.id] = true;
          merged.push(entry.doc);
        }
      });
    });

    if (merged.length === 0) {
      resultsEl.innerHTML = '<div class="orchard-search-empty">No results</div>';
      resultsEl.classList.remove('hidden');
      return;
    }

    merged.slice(0, 10).forEach(function (doc) {
      var item = document.createElement('a');
      item.href = doc.href;
      item.className = 'orchard-search-result';

      var title = document.createElement('div');
      title.className = 'orchard-search-result-title';
      title.textContent = doc.title;

      var section = document.createElement('div');
      section.className = 'orchard-search-result-section';
      section.textContent = doc.section;

      var preview = document.createElement('div');
      preview.className = 'orchard-search-result-preview';
      var text = (doc.content || '').replace(doc.title, '').trim();
      preview.textContent = text.slice(0, 140) + (text.length > 140 ? '...' : '');

      item.appendChild(title);
      item.appendChild(section);
      if (text) item.appendChild(preview);
      resultsEl.appendChild(item);
    });

    resultsEl.classList.remove('hidden');
  }
})();
