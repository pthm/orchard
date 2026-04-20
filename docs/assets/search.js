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

  // cleanText strips markdown artefacts and HTML entities from plain
  // text so search previews read as prose, not raw markup.
  function cleanText(s) {
    // Use a temporary DOM element to decode ALL HTML entities
    // (named, numeric decimal &#34;, and hex &#x22;) in one pass.
    var el = document.createElement('textarea');
    el.innerHTML = s;
    s = el.value;
    return s
      // Strip markdown heading markers (# ## ### etc.)
      .replace(/^#{1,6}\s+/gm, '')
      // Strip markdown emphasis/bold markers
      .replace(/\*{1,3}([^*]+)\*{1,3}/g, '$1')
      .replace(/_{1,3}([^_]+)_{1,3}/g, '$1')
      // Strip markdown code backticks
      .replace(/`([^`]*)`/g, '$1')
      // Strip markdown link syntax [text](url) → text
      .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
      // Strip markdown table separators
      .replace(/^\|[-:| ]+\|$/gm, '')
      // Strip table pipes
      .replace(/\|/g, ' ')
      // Collapse multiple whitespace/newlines
      .replace(/\s+/g, ' ')
      .trim();
  }

  // stripHTML removes inline HTML tags from heading titles — Hugo's
  // .Fragments includes rendered markup like <code>.
  function stripHTML(s) {
    return s.replace(/<[^>]*>/g, '');
  }

  // splitCompound turns "formatdate" into "formatdate format date"
  // so both the compound name and its parts are searchable. Splits
  // at boundaries where a lowercase letter meets the next "word"
  // (heuristic: sequences of 3+ lowercase chars).
  function splitCompound(name) {
    // Split known compound patterns: lowercase runs of 3+ chars
    var parts = name.match(/[a-z]{3,}/g);
    if (!parts || parts.length <= 1) return name;
    return name + ' ' + parts.join(' ');
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
          tokenize: 'full',
          resolution: 9
        });

        var id = 0;
        pages.forEach(function (page) {
          var headings = page.headings || [];
          var plain = cleanText(page.content || '');

          if (headings.length === 0) {
            var entry = {
              id: id++,
              href: page.href,
              title: page.title,
              section: page.section,
              content: plain.slice(0, 500),
              preview: plain.slice(0, 500)
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
              content: plain.slice(0, Math.min(firstPos, 500)),
              preview: plain.slice(0, Math.min(firstPos, 500))
            };
            sections.push(intro);
            index.add(intro);
          }

          // Per-heading sections. Build a breadcrumb trail by
          // tracking the last heading at each level.
          var breadcrumbStack = {};
          breadcrumbStack[1] = page.title;

          for (var i = 0; i < headings.length; i++) {
            var h = headings[i];
            var cleanTitle = stripHTML(h.title);

            // Update the breadcrumb stack for this level.
            breadcrumbStack[h.level] = cleanTitle;
            // Clear deeper levels.
            for (var l = h.level + 1; l <= 6; l++) {
              delete breadcrumbStack[l];
            }

            // Build breadcrumb: page > h2 > h3 > ...
            var crumbs = [page.section];
            for (var l = 1; l <= h.level; l++) {
              if (breadcrumbStack[l]) crumbs.push(breadcrumbStack[l]);
            }
            var breadcrumb = crumbs.join(' > ');

            // Try to extract a content snippet around the heading.
            var rawSnippet = '';
            var pos = plain.indexOf(cleanTitle);
            if (pos >= 0) {
              var nextPos = plain.length;
              if (i + 1 < headings.length) {
                var nextTitle = stripHTML(headings[i + 1].title);
                var np = plain.indexOf(nextTitle, pos + cleanTitle.length);
                if (np > pos) nextPos = np;
              }
              rawSnippet = plain.slice(pos + cleanTitle.length, Math.min(pos + 500, nextPos)).trim();
            }

            // Index with split compound words in the title so
            // "format date" matches "formatdate".
            var indexTitle = splitCompound(cleanTitle);

            var sectionEntry = {
              id: id++,
              href: page.href + '#' + h.id,
              title: indexTitle,
              section: breadcrumb,
              content: rawSnippet || cleanTitle,
              preview: rawSnippet
            };
            sectionEntry._displayTitle = cleanTitle;
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

    // Boost results from the current page to the top so in-page
    // navigation via search feels natural.
    var currentPath = window.location.pathname;
    merged.sort(function (a, b) {
      var aLocal = a.href && a.href.split('#')[0] === currentPath ? 0 : 1;
      var bLocal = b.href && b.href.split('#')[0] === currentPath ? 0 : 1;
      return aLocal - bLocal;
    });

    merged.slice(0, 10).forEach(function (doc) {
      var item = document.createElement('a');
      item.href = doc.href;
      item.className = 'orchard-search-result';

      var title = document.createElement('div');
      title.className = 'orchard-search-result-title';
      // Use the original display title if available,
      // otherwise fall back to the indexed title.
      title.textContent = doc._displayTitle || doc.title;

      var section = document.createElement('div');
      section.className = 'orchard-search-result-section';
      section.textContent = doc.section;

      var previewText = (doc.preview || doc.content || '').trim();
      if (previewText) {
        var preview = document.createElement('div');
        preview.className = 'orchard-search-result-preview';
        preview.textContent = previewText.slice(0, 140) + (previewText.length > 140 ? '...' : '');
        item.appendChild(title);
        item.appendChild(section);
        item.appendChild(preview);
      } else {
        item.appendChild(title);
        item.appendChild(section);
      }

      resultsEl.appendChild(item);
    });

    resultsEl.classList.remove('hidden');
  }
})();
