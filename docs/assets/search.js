'use strict';

{{ $searchDataFile := printf "%s.search-data.json" .Language.Name }}
{{ $searchData := resources.Get "search-data.json" | resources.ExecuteAsTemplate $searchDataFile . | resources.Minify | resources.Fingerprint }}

(function () {
  const searchDataURL = '{{ partial "docs/links/resource-precache" $searchData }}';

  const input = document.querySelector('#book-search-input');
  const results = document.querySelector('#book-search-results');

  if (!input) {
    return;
  }

  let index = null;
  let pages = null;

  input.addEventListener('focus', init);
  input.addEventListener('keyup', search);
  document.addEventListener('keypress', focusSearchFieldOnKeyPress);

  function focusSearchFieldOnKeyPress(event) {
    if (event.target.value !== undefined) return;
    if (input === document.activeElement) return;
    const ch = String.fromCharCode(event.charCode);
    const hotkeys = input.getAttribute('data-hotkeys') || '';
    if (hotkeys.indexOf(ch) >= 0) {
      input.focus();
      event.preventDefault();
    }
  }

  function init() {
    input.removeEventListener('focus', init);
    input.required = true;

    fetch(searchDataURL)
      .then(r => r.json())
      .then(data => {
        pages = {};
        index = new FlexSearch.Document({
          document: {
            id: 'id',
            index: ['title', 'content'],
            store: true
          },
          tokenize: 'forward',
          resolution: 9
        });

        data.forEach(function (page, i) {
          pages[i] = page;
          index.add({
            id: i,
            title: page.title || '',
            content: page.content || '',
            section: page.section || '',
            href: page.href || ''
          });
        });
      })
      .then(() => { input.required = false; })
      .then(search);
  }

  function search() {
    while (results.firstChild) {
      results.removeChild(results.firstChild);
    }

    if (!input.value || !index) {
      return;
    }

    const hits = index.search(input.value, { limit: 10, enrich: true });

    // FlexSearch returns results per field; merge + deduplicate by id.
    const seen = {};
    const merged = [];
    hits.forEach(function (fieldResult) {
      fieldResult.result.forEach(function (entry) {
        const id = entry.id;
        if (!seen[id]) {
          seen[id] = true;
          merged.push(entry.doc);
        }
      });
    });

    merged.slice(0, 10).forEach(function (doc) {
      const li = element('<li><a href></a><small></small></li>');
      const a = li.querySelector('a');
      const small = li.querySelector('small');

      a.href = doc.href;
      a.textContent = doc.title;
      small.textContent = doc.section;

      results.appendChild(li);
    });
  }

  function element(content) {
    const div = document.createElement('div');
    div.innerHTML = content;
    return div.firstChild;
  }
})();
