// Tools & Providers — one desk for every search, embedding, rerank, vision,
// speech, vector and agent-tool provider.
//
// The page is one render pipeline over one state object, like the Memory &
// RAG desk next to it:
//   1. data     — GET /api/providerhub/catalog, POST /api/providerhub/save,
//                 POST /api/providerhub/test
//   2. motion   — still() before every programmatic animation, five duration
//                 steps, dots for state (never a colour bar), no scroll the
//                 reader did not ask for
//   3. words    — shelf labels and field labels are mapped from the ids and
//                 env names the backend sends, so every word is the plugin's
//                 own or one the core dictionary already says. Vendor notes
//                 are not shown: they are backend copy, not page copy.
//
// Nothing here invents a number: the catalogue counts come from the server,
// and a check's milliseconds are the server's measured call.

(function () {
  const SHELF_WORDS = {
    search: "Web search routes",
    embedding: "Query embedding models",
    rerank: "Rerank models",
    vision: "Image understanding",
    llm: "Answer models",
    stt: "Speech recognition",
    tts: "Speech output",
    vector: "Vector databases",
    tools: "Agent tool plugins"
  };

  let kit = null;
  let flashTimer = null;
  let modalKey = null;

  const phState = {
    catalog: null,
    error: "",
    catalogError: "",
    loading: false,
    shelf: "all",
    filter: "",
    openId: "",
    draft: {},
    cleared: {},
    results: {},
    testing: {},
    saving: false,
    modalError: "",
    flash: "",
    flashTone: "ok"
  };

  // ---------- words and small helpers ------------------------------------

  // T is the shell's t at call time: the plugin loads before app.js, so a
  // direct alias would capture nothing. Plugin words fall back to English
  // when a dictionary is missing; core words resolve through the shell.
  const T = (s, vars) => (typeof t === "function" ? t(s, vars) : s);

  function esc(s) {
    return String(s ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function still() {
    if (typeof calm === "function") return calm();
    return !!(window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  }

  function dot(tone) {
    return `<span class="ph-dot ph-dot-${tone}" aria-hidden="true"></span>`;
  }

  function shelves() {
    return (phState.catalog && phState.catalog.shelves) || [];
  }

  function allProviders() {
    return shelves().reduce((out, s) => out.concat(s.providers || []), []);
  }

  function providerOf(id) {
    return allProviders().find((p) => p.id === id) || null;
  }

  function fieldsOf(p) {
    return (p && p.fields) || [];
  }

  function fieldStateOf(p, env) {
    return ((p && p.values) || []).find((v) => v.env === env) || null;
  }

  function shelfWord(shelf) {
    return T(SHELF_WORDS[shelf.id] || shelf.label);
  }

  // The backend labels a field by its own English key ("API base", "DSN"),
  // and those strings live in a Go file the plugin dictionary may not name.
  // Map by env name instead; the words below are the page's own.
  function fieldWord(f) {
    const env = f.env || "";
    if (env === "TENCENT_SECRET_ID") return T("SecretId");
    if (env === "TENCENT_SECRET_KEY") return T("SecretKey");
    if (env === "TENCENT_WSA_API_KEY") return T("WSA MCP key");
    if (env === "SEARXNG_URL") return T("SearXNG address");
    if (env === "SEARXNG_API_KEY") return T("Optional key for your instance");
    if (env === "MOLA_VECTOR_PINECONE_URL") return T("Index address");
    if (/^MOLA_VECTOR_.*_URL$/.test(env)) return T("Vector store address");
    if (env === "MOLA_PG_DSN") return T("Connection string");
    if (env === "CONTEXT7_MCP_URL") return T("MCP server address");
    if (env === "OLLAMA_BASE_URL") return T("Base URL");
    if (/_API_BASE$/.test(env)) return T("API base address");
    if (/_MODEL$/.test(env)) return T("Model");
    if (/_API_KEY$/.test(env)) return T("API key");
    return f.key || env;
  }

  function stateWord(p) {
    if (!p.enabled) return T("Off for now");
    if (p.configured) return T("Ready to use");
    return T("Not configured");
  }

  function stateTone(p) {
    if (!p.enabled) return "muted";
    return p.configured ? "green" : "amber";
  }

  function chipsOf(p) {
    const out = [];
    if (p.wired && p.enabled) out.push([T("In a run"), "live"]);
    if (p.active) out.push([T("Fills the engine slot"), "slot"]);
    if (p.keyless) out.push([T("No key needed"), "free"]);
    return out;
  }

  function match(p, q) {
    if (!q) return true;
    const hay = [p.id, p.name, p.channel || "", ...fieldsOf(p).map((f) => f.env + " " + (f.key || ""))]
      .join(" ")
      .toLowerCase();
    return hay.includes(q);
  }

  function filtered(shelf) {
    return (shelf.providers || []).filter((p) => match(p, phState.filter));
  }

  function visibleCount() {
    return shelves().reduce((n, s) => n + filtered(s).length, 0);
  }

  // ---------- glyphs -----------------------------------------------------
  // Vendor-neutral marks, drawn here: no logo is copied, and each one is a
  // 24×24 stroke drawing that inherits the tile's ink.
  const GLYPHS = {
    grid: '<rect x="4" y="4" width="6.5" height="6.5" rx="1.6"/><rect x="13.5" y="4" width="6.5" height="6.5" rx="1.6"/><rect x="4" y="13.5" width="6.5" height="6.5" rx="1.6"/><rect x="13.5" y="13.5" width="6.5" height="6.5" rx="1.6"/>',
    spark: '<path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9Z"/>',
    search: '<circle cx="11" cy="11" r="6"/><path d="M15.6 15.6L20 20"/>',
    globe: '<circle cx="12" cy="12" r="8"/><path d="M4 12h16"/><path d="M12 4c2.6 2.2 3.9 5 3.9 8s-1.3 5.8-3.9 8c-2.6-2.2-3.9-5-3.9-8S9.4 6.2 12 4Z"/>',
    layers: '<path d="M12 4l8 4-8 4-8-4 8-4Z"/><path d="M4 12l8 4 8-4"/><path d="M4 16l8 4 8-4"/>',
    flame: '<path d="M12 3c3 4 5 6.2 5 9.2A5 5 0 0 1 7 13c0-2 1-3.4 2.3-4.4.1 1.5.8 2.3 1.7 2.3 0-3 .2-5.2 1-7.9Z"/>',
    code: '<path d="M9 7l-5 5 5 5"/><path d="M15 7l5 5-5 5"/>',
    cube: '<path d="M12 3l8 4.5v9L12 21l-8-4.5v-9L12 3Z"/><path d="M4 7.5l8 4.5 8-4.5"/><path d="M12 12v9"/>',
    eye: '<path d="M2.5 12S6 6 12 6s9.5 6 9.5 6-3.5 6-9.5 6-9.5-6-9.5-6Z"/><circle cx="12" cy="12" r="2.8"/>',
    brain:
      '<path d="M9.5 5A3.5 3.5 0 0 0 6 8.5v.6A3.4 3.4 0 0 0 5 15a3.4 3.4 0 0 0 4.5 4V5Z"/><path d="M14.5 5A3.5 3.5 0 0 1 18 8.5v.6A3.4 3.4 0 0 1 19 15a3.4 3.4 0 0 1-4.5 4V5Z"/><path d="M12 5v14"/>',
    mic: '<path d="M12 4a3 3 0 0 1 3 3v5a3 3 0 0 1-6 0V7a3 3 0 0 1 3-3Z"/><path d="M5 11a7 7 0 0 0 14 0"/><path d="M12 18v3"/>',
    bolt: '<path d="M13 3L5 14h6l-1 7 8-11h-6l1-7Z"/>',
    wave: '<path d="M3 12h2l2-5 3 10 3-14 3 14 2-5h3"/>',
    speaker: '<path d="M4 10v4h3l4 3V7l-4 3H4Z"/><path d="M16 9.5a4 4 0 0 1 0 5"/><path d="M18.5 7a7 7 0 0 1 0 10"/>',
    database:
      '<ellipse cx="12" cy="6" rx="8" ry="3"/><path d="M4 6v6c0 1.7 3.6 3 8 3s8-1.3 8-3V6"/><path d="M4 12v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6"/>',
    book: '<path d="M5 4h9a3 3 0 0 1 3 3v13H8a3 3 0 0 1-3-3V4Z"/><path d="M17 20V7"/><path d="M8 8h6"/><path d="M8 11h6"/>'
  };

  function glyph(name) {
    const path = GLYPHS[name] || GLYPHS.spark;
    return `<svg class="ph-glyph" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${path}</svg>`;
  }

  // ---------- data -------------------------------------------------------

  // phApi answers {error} instead of throwing, so a failed call is shown on
  // the page. A refusal carries its code so 409 can say which rule held.
  async function phApi(path, opts) {
    try {
      const res = await fetch("/api/" + path, {
        headers: { "Content-Type": "application/json" },
        ...opts
      });
      const body = await res.json().catch(() => null);
      if (!res.ok) {
        return {
          error: (body && body.error) || "HTTP " + res.status,
          code: body && body.code
        };
      }
      return body || {};
    } catch (e) {
      return { error: e && e.message ? e.message : String(e) };
    }
  }

  async function loadCatalog() {
    if (phState.loading) return;
    phState.loading = true;
    phState.catalogError = "";
    renderHero();
    renderContent();
    const res = await phApi("providerhub/catalog");
    phState.loading = false;
    if (res.error) {
      phState.catalogError = res.error;
      // A refresh that fails keeps the last good catalogue on screen; the
      // banner says it is the old one rather than blanking the page.
      if (phState.catalog) {
        phState.error = T("The page could not read the catalog: {error}", { error: res.error });
      }
      renderAll();
      return;
    }
    phState.catalog = res;
    phState.error = "";
    if (phState.shelf !== "all" && !shelves().some((s) => s.id === phState.shelf)) {
      phState.shelf = "all";
    }
    renderAll();
  }

  async function saveInput(id, values, clear, enabled) {
    const payload = { id, values: values || {}, clear: clear || [] };
    if (typeof enabled === "boolean") payload.enabled = enabled;
    return phApi("providerhub/save", { method: "POST", body: JSON.stringify(payload) });
  }

  async function setGate(p, on) {
    const res = await saveInput(p.id, {}, [], on);
    if (res.error) {
      phState.error = T("Save failed: {error}", { error: res.error });
      renderAll();
      return;
    }
    phState.error = "";
    // A check from before the gate moved no longer describes this provider.
    delete phState.results[p.id];
    if (res.catalog) phState.catalog = res.catalog;
    renderHero();
    renderContent();
  }

  async function testProvider(id) {
    if (phState.testing[id]) return;
    phState.testing[id] = true;
    paintResult(id);
    const res = await phApi("providerhub/test", { method: "POST", body: JSON.stringify({ id }) });
    phState.testing[id] = false;
    let r;
    if (res.error) {
      if (res.code === "not-configured") r = { ok: false, text: T("Not configured") };
      else if (res.code === "disabled") r = { ok: false, text: T("Off for now") };
      else r = { ok: false, text: T("Test failed: {error}", { error: res.error }) };
    } else if (res.ok) {
      r = { ok: true, text: T("Answered in {ms} ms", { ms: res.latencyMs }) };
    } else {
      r = { ok: false, text: T("No answer: {detail}", { detail: res.detail || "" }) };
    }
    phState.results[id] = r;
    paintResult(id);
  }

  // ---------- render: hero, tabs, cards ----------------------------------

  function flashLine() {
    if (!phState.flash) return "";
    return `<p class="ph-flash ${phState.flashTone}">${dot(phState.flashTone === "ok" ? "green" : "pink")}<span>${esc(phState.flash)}</span></p>`;
  }

  function renderHero() {
    const host = document.getElementById("phHero");
    if (!host) return;
    const cat = phState.catalog;
    const configured = cat ? cat.configured : 0;
    const total = cat ? cat.total : 0;
    const off = cat ? Math.max(0, cat.total - cat.enabled) : 0;
    const envFile = (cat && cat.envFile) || "";
    const error = phState.error || (cat && cat.error) || "";
    host.innerHTML = `
      <div class="ph-hero-main">
        <span class="ph-mark" aria-hidden="true">${glyph("grid")}</span>
        <div class="ph-hero-text">
          <h2 class="ph-title">${esc(T("Tools & Providers"))}</h2>
          <p class="ph-sub">${esc(T("Search, embedding, rerank, vision, speech, vector stores and agent tools: one registry, keys in one place, every check a real call"))}</p>
        </div>
      </div>
      <div class="ph-stats">
        <div class="ph-stat">${dot("green")}<b>${configured}</b><span>${esc(T("Providers connected"))}</span><em>${configured} / ${total}</em></div>
        <div class="ph-stat">${dot("amber")}<b>${off}</b><span>${esc(T("Turned off"))}</span></div>
        <div class="ph-stat path"><span>${esc(T("Keys are written to"))}</span><code title="${esc(envFile)}">${esc(envFile)}</code></div>
      </div>
      ${flashLine()}
      ${error ? `<p class="ph-warn">${dot("pink")}<span>${esc(error)}</span></p>` : ""}
    `;
  }

  function renderTabs() {
    const host = document.getElementById("phTabs");
    if (!host) return;
    const all = { id: "all", label: T("All shelves") };
    const rows = [all, ...shelves()];
    host.innerHTML = rows
      .map((s) => {
        const on = phState.shelf === s.id;
        const label = s.id === "all" ? T("All shelves") : shelfWord(s);
        const n = s.id === "all" ? visibleCount() : filtered(s).length;
        return `<button class="ph-tab${on ? " on" : ""}" type="button" role="tab" aria-selected="${on}" data-shelf="${esc(s.id)}">
          <span class="ph-tab-word">${esc(label)}</span><span class="ph-tab-n" data-count="${esc(s.id)}">${n}</span>
        </button>`;
      })
      .join("");
    host.querySelectorAll("[data-shelf]").forEach((b) => {
      b.onclick = () => pickShelf(b.dataset.shelf);
    });
  }

  function paintTabCounts() {
    document.querySelectorAll("[data-count]").forEach((el) => {
      const id = el.dataset.count;
      if (id === "all") {
        el.textContent = String(visibleCount());
        return;
      }
      const sh = shelves().find((s) => s.id === id);
      el.textContent = String(sh ? filtered(sh).length : 0);
    });
    document.querySelectorAll("[data-shelf]").forEach((b) => {
      const on = b.dataset.shelf === phState.shelf;
      b.classList.toggle("on", on);
      b.setAttribute("aria-selected", String(on));
    });
  }

  function pickShelf(id) {
    if (phState.shelf === id) return;
    phState.shelf = id;
    paintTabCounts();
    swapContent(renderContent);
  }

  function swapContent(paint) {
    const host = document.getElementById("phContent");
    if (!host || still()) {
      paint();
      return;
    }
    const out = host.animate(
      [
        { opacity: 1, transform: "translateY(0)" },
        { opacity: 0, transform: "translateY(4px)" }
      ],
      { duration: 110, easing: "cubic-bezier(.4, 0, 1, 1)" }
    );
    out.onfinish = () => {
      paint();
      host.animate(
        [
          { opacity: 0, transform: "translateY(6px)" },
          { opacity: 1, transform: "translateY(0)" }
        ],
        { duration: 220, easing: "cubic-bezier(.22, 1, .36, 1)" }
      );
    };
  }

  function wireRow(p, f) {
    const st = fieldStateOf(p, f.env);
    const on = !!(st && st.set);
    return `<div class="ph-wire${on ? " on" : ""}">
      <span class="ph-wire-dot" aria-hidden="true"></span>
      <span class="ph-wire-label">${esc(fieldWord(f))}</span>
      <code class="ph-env">${esc(f.env)}</code>
    </div>`;
  }

  function cardHtml(p, index) {
    const chips = chipsOf(p);
    const hasFields = fieldsOf(p).length > 0;
    const r = phState.results[p.id];
    const busy = !!phState.testing[p.id];
    const resultClass = busy ? " busy" : r ? (r.ok ? " ok" : " bad") : "";
    const resultHtml = busy
      ? `<span class="ph-spin" aria-hidden="true"></span><span>${esc(T("Testing…"))}</span>`
      : r
        ? `${dot(r.ok ? "green" : "pink")}<span>${esc(r.text)}</span>`
        : `${dot("muted")}<span>${esc(T("Not checked yet"))}</span>`;
    const docs = p.docs
      ? `<a class="ph-link" href="${esc(p.docs)}" target="_blank" rel="noreferrer noopener">${esc(T("Open docs"))}</a>`
      : "";
    return `<article class="ph-card${p.enabled ? "" : " off"}" style="--ph-i:${index}" data-id="${esc(p.id)}">
      <div class="ph-card-head">
        <span class="ph-tile ph-tone-${esc(p.tone || "ink")}" aria-hidden="true">${glyph(p.icon)}</span>
        <div class="ph-who">
          <h3 class="ph-name">${esc(p.name)}</h3>
          <span class="ph-state">${dot(stateTone(p))}<span>${esc(stateWord(p))}</span></span>
        </div>
        <span class="ph-gate" data-gate="${esc(p.id)}"></span>
      </div>
      ${chips.length ? `<div class="ph-chips">${chips.map((c) => `<span class="ph-chip ${c[1]}">${esc(c[0])}</span>`).join("")}</div>` : ""}
      ${hasFields ? `<div class="ph-wires">${fieldsOf(p).map((f) => wireRow(p, f)).join("")}</div>` : ""}
      <div class="ph-card-actions">
        ${hasFields ? `<button class="ph-btn" type="button" data-config="${esc(p.id)}">${esc(p.configured ? T("Edit") : T("Set the key"))}</button>` : ""}
        <button class="ph-btn ghost" type="button" data-test="${esc(p.id)}"${busy ? " disabled" : ""}>${esc(busy ? T("Testing…") : T("Test connection"))}</button>
        ${docs}
      </div>
      <p class="ph-result${resultClass}" data-result="${esc(p.id)}" role="status" aria-live="polite">${resultHtml}</p>
    </article>`;
  }

  function shelfBlock(shelf, indexBase) {
    const rows = filtered(shelf);
    if (!rows.length) return "";
    return `<section class="ph-shelf">
      <header class="ph-shelf-head">
        <h3>${esc(shelfWord(shelf))}</h3>
        <span class="ph-shelf-n">${rows.length}</span>
      </header>
      <div class="ph-grid">${rows.map((p, i) => cardHtml(p, indexBase + i)).join("")}</div>
    </section>`;
  }

  function renderContent() {
    const host = document.getElementById("phContent");
    if (!host) return;
    if (!phState.catalog && phState.loading) {
      host.innerHTML = `<p class="ph-loading">${dot("blue")}<span>${esc(T("Reading the catalog…"))}</span></p>
        <div class="ph-grid ghost-grid">${Array.from({ length: 6 }, (_, i) => `<div class="ph-card ghost" style="--ph-i:${i}"><span class="ph-skel w40"></span><span class="ph-skel w70"></span><span class="ph-skel w55"></span></div>`).join("")}</div>`;
      return;
    }
    if (!phState.catalog && phState.catalogError) {
      host.innerHTML = `<div class="ph-empty">${glyph("search")}<p>${esc(T("The page could not read the catalog: {error}", { error: phState.catalogError }))}</p><button class="ph-btn" type="button" id="phRetry">${esc(T("Try again"))}</button></div>`;
      const retry = document.getElementById("phRetry");
      if (retry) retry.onclick = loadCatalog;
      return;
    }
    if (!phState.catalog) {
      host.innerHTML = "";
      return;
    }
    const list = phState.shelf === "all" ? shelves() : shelves().filter((s) => s.id === phState.shelf);
    let base = 0;
    const blocks = list
      .map((s) => {
        const html = shelfBlock(s, base);
        base += filtered(s).length;
        return html;
      })
      .join("");
    const gates = phState.catalog.searchGates
      ? `<p class="ph-foot">${esc(T("Search channel gates live in {file}", { file: phState.catalog.searchGates }))}</p>`
      : "";
    if (!blocks.trim()) {
      const word = phState.filter ? T("Nothing matches this filter.") : T("No provider on this shelf.");
      host.innerHTML = `<div class="ph-empty">${glyph("search")}<p>${esc(word)}</p></div>${gates}`;
      return;
    }
    host.innerHTML = blocks + gates;
    bindCards(host);
  }

  function bindCards(host) {
    host.querySelectorAll("[data-config]").forEach((b) => {
      b.onclick = () => openConfig(b.dataset.config);
    });
    host.querySelectorAll("[data-test]").forEach((b) => {
      b.onclick = () => testProvider(b.dataset.test);
    });
    host.querySelectorAll("[data-gate]").forEach((slot) => {
      const p = providerOf(slot.dataset.gate);
      if (!p || !kit) return;
      slot.append(kit.toggle(p.enabled, p.name, (on) => setGate(p, on)));
    });
  }

  function paintResult(id) {
    const line = document.querySelector(`[data-result="${id}"]`);
    if (!line) return;
    const r = phState.results[id];
    const busy = !!phState.testing[id];
    line.className = "ph-result" + (busy ? " busy" : r ? (r.ok ? " ok" : " bad") : "");
    line.innerHTML = busy
      ? `<span class="ph-spin" aria-hidden="true"></span><span>${esc(T("Testing…"))}</span>`
      : r
        ? `${dot(r.ok ? "green" : "pink")}<span>${esc(r.text)}</span>`
        : `${dot("muted")}<span>${esc(T("Not checked yet"))}</span>`;
    const test = document.querySelector(`[data-test="${id}"]`);
    if (test) {
      test.disabled = busy;
      test.textContent = busy ? T("Testing…") : T("Test connection");
    }
    if (r && !busy && !still()) {
      line.animate(
        [
          { opacity: 0, transform: "translateY(3px)" },
          { opacity: 1, transform: "translateY(0)" }
        ],
        { duration: 180, easing: "cubic-bezier(.22, 1, .36, 1)" }
      );
    }
  }

  // ---------- the configure sheet ---------------------------------------

  function openConfig(id) {
    const p = providerOf(id);
    if (!p) return;
    phState.openId = id;
    phState.draft = {};
    phState.cleared = {};
    phState.modalError = "";
    phState.saving = false;
    for (const f of fieldsOf(p)) {
      const st = fieldStateOf(p, f.env);
      if (!f.secret && st && st.value) phState.draft[f.env] = st.value;
    }
    renderModal();
    if (modalKey) document.removeEventListener("keydown", modalKey);
    modalKey = (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        closeModal();
      }
    };
    document.addEventListener("keydown", modalKey);
    requestAnimationFrame(() => {
      const first = document.querySelector("#phModalHost input");
      if (first) first.focus();
    });
  }

  function closeModal(immediate) {
    if (modalKey) {
      document.removeEventListener("keydown", modalKey);
      modalKey = null;
    }
    const host = document.getElementById("phModalHost");
    phState.openId = "";
    if (!host) return;
    const panel = host.querySelector(".ph-modal");
    if (!panel || immediate || still()) {
      host.innerHTML = "";
      return;
    }
    const a = panel.animate(
      [
        { opacity: 1, transform: "translateY(0) scale(1)" },
        { opacity: 0, transform: "translateY(6px) scale(.985)" }
      ],
      { duration: 150, easing: "cubic-bezier(.4, 0, 1, 1)", fill: "forwards" }
    );
    const back = host.querySelector(".ph-backdrop");
    if (back) back.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 150, fill: "forwards" });
    a.onfinish = () => {
      if (!phState.openId) host.innerHTML = "";
    };
  }

  function fieldForm(p, f) {
    const st = fieldStateOf(p, f.env);
    const set = !!(st && st.set);
    const cleared = !!phState.cleared[f.env];
    const value = phState.draft[f.env] ?? (f.secret ? "" : (st && st.value) || "");
    const placeholder = f.secret && set ? st.masked : f.placeholder || "";
    const hint = f.secret
      ? cleared
        ? T("Will be cleared when saved")
        : set
          ? T("Leave empty to keep the saved key")
          : ""
      : "";
    return `<label class="ph-field">
      <span class="ph-field-top">
        <span class="ph-field-word">${esc(fieldWord(f))}</span>
        <code class="ph-env">${esc(f.env)}</code>
      </span>
      <input class="ph-input" data-env="${esc(f.env)}" type="${f.secret ? "password" : "text"}"
        value="${esc(value)}" placeholder="${esc(placeholder)}" autocomplete="off" spellcheck="false"${cleared ? " disabled" : ""}>
      ${f.secret && set ? `<button class="ph-clear${cleared ? " on" : ""}" type="button" data-clear="${esc(f.env)}" aria-pressed="${cleared}">${esc(T("Clear the saved key"))}</button>` : ""}
      ${hint ? `<span class="ph-hint">${esc(hint)}</span>` : ""}
    </label>`;
  }

  function renderModal() {
    const host = document.getElementById("phModalHost");
    if (!host) return;
    const p = phState.openId ? providerOf(phState.openId) : null;
    if (!p) {
      host.innerHTML = "";
      return;
    }
    const fields = fieldsOf(p);
    const body = fields.length
      ? fields.map((f) => fieldForm(p, f)).join("")
      : `<p class="ph-no-fields">${esc(T("This provider takes no fields."))}</p>`;
    host.innerHTML = `<div class="ph-backdrop">
      <section class="ph-modal" role="dialog" aria-modal="true" aria-labelledby="phModalTitle">
        <header class="ph-modal-head">
          <span class="ph-tile ph-tone-${esc(p.tone || "ink")}" aria-hidden="true">${glyph(p.icon)}</span>
          <div class="ph-who">
            <h3 class="ph-name" id="phModalTitle">${esc(T("Keys for {name}", { name: p.name }))}</h3>
            <span class="ph-state">${dot(stateTone(p))}<span>${esc(stateWord(p))}</span></span>
          </div>
          <button class="ph-x" type="button" data-close aria-label="${esc(T("Close"))}">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>
          </button>
        </header>
        <div class="ph-modal-body">${body}</div>
        ${phState.modalError ? `<p class="ph-modal-error">${dot("pink")}<span>${esc(phState.modalError)}</span></p>` : ""}
        <footer class="ph-modal-foot">
          <span class="ph-foot-label">${esc(T("Keys are written to"))} <code>${esc(phState.catalog ? phState.catalog.envFile : "")}</code></span>
          <div class="ph-foot-actions">
            <button class="ph-btn ghost" type="button" data-close>${esc(T("Cancel"))}</button>
            ${fields.length ? `<button class="ph-btn" type="button" id="phSave"${phState.saving ? " disabled" : ""}>${esc(phState.saving ? T("Saving…") : T("Save"))}</button>` : ""}
          </div>
        </footer>
      </section>
    </div>`;
    const back = host.querySelector(".ph-backdrop");
    if (back) {
      back.onclick = (e) => {
        if (e.target === back) closeModal();
      };
    }
    host.querySelectorAll("[data-close]").forEach((b) => {
      b.onclick = () => closeModal();
    });
    host.querySelectorAll("[data-env]").forEach((input) => {
      input.oninput = () => {
        phState.draft[input.dataset.env] = input.value;
      };
    });
    host.querySelectorAll("[data-clear]").forEach((b) => {
      b.onclick = () => {
        const env = b.dataset.clear;
        if (phState.cleared[env]) delete phState.cleared[env];
        else {
          phState.cleared[env] = true;
          delete phState.draft[env];
        }
        renderModal();
      };
    });
    const save = document.getElementById("phSave");
    if (save) save.onclick = submitModal;
    const panel = host.querySelector(".ph-modal");
    if (panel && !still()) {
      panel.animate(
        [
          { opacity: 0, transform: "translateY(8px) scale(.98)" },
          { opacity: 1, transform: "translateY(0) scale(1)" }
        ],
        { duration: 220, easing: "cubic-bezier(.22, 1, .36, 1)" }
      );
    }
  }

  async function submitModal() {
    const p = phState.openId ? providerOf(phState.openId) : null;
    if (!p || phState.saving) return;
    const values = {};
    const clear = [];
    for (const f of fieldsOf(p)) {
      const raw = (phState.draft[f.env] ?? "").trim();
      const st = fieldStateOf(p, f.env);
      const before = !f.secret && st && st.value ? st.value : "";
      if (raw && raw !== before) values[f.env] = raw;
      else if (!raw && phState.cleared[f.env]) clear.push(f.env);
      else if (!raw && st && st.set && !f.secret) clear.push(f.env);
    }
    if (!Object.keys(values).length && !clear.length) {
      closeModal();
      return;
    }
    // A slot's providers share the engine's field names; a partial set would
    // pair one vendor's address with another's key. Say it here, in the
    // page's words, before the backend refuses the same thing.
    if (Object.keys(values).length && p.slot) {
      const missing = fieldsOf(p).some((f) => {
        if (!f.required) return false;
        if ((phState.draft[f.env] ?? "").trim()) return false;
        const st = fieldStateOf(p, f.env);
        return !(f.secret && st && st.set && !phState.cleared[f.env]);
      });
      if (missing) {
        phState.modalError = T("This slot needs every field.");
        renderModal();
        return;
      }
    }
    phState.saving = true;
    phState.modalError = "";
    renderModal();
    const res = await saveInput(p.id, values, clear);
    phState.saving = false;
    if (res.error) {
      phState.modalError = T("Save failed: {error}", { error: res.error });
      renderModal();
      return;
    }
    if (res.catalog) phState.catalog = res.catalog;
    // The old check answered about the old keys; it no longer describes
    // this provider, so the card goes back to "not checked yet".
    delete phState.results[p.id];
    closeModal(true);
    phState.flash = clear.length && !Object.keys(values).length ? T("Key cleared") : T("Key saved");
    phState.flashTone = "ok";
    renderAll();
    if (flashTimer) clearTimeout(flashTimer);
    flashTimer = setTimeout(() => {
      phState.flash = "";
      renderHero();
    }, 4200);
  }

  // ---------- the page ---------------------------------------------------

  function renderAll() {
    renderHero();
    renderTabs();
    renderContent();
  }

  // The page's own html is built when the plugin registers, before the
  // shell's dictionary is ready; these are the few words it carries.
  function syncStaticWords() {
    const find = document.getElementById("phFind");
    if (find) {
      find.placeholder = T("Filter providers by name or key");
      find.setAttribute("aria-label", T("Filter providers by name or key"));
    }
    const tabs = document.getElementById("phTabs");
    if (tabs) tabs.setAttribute("aria-label", T("Provider categories"));
  }

  magpie.plugin("providerhub", (ctx) => {
    kit = ctx.ui;
    ctx.slots.add("page", {
      key: "providerhub",
      title: "Tools & Providers",
      html: `
        <div class="ph-wrap">
          <header class="ph-hero" id="phHero"></header>
          <div class="ph-bar">
            <nav class="ph-tabs" id="phTabs" role="tablist" aria-label="${esc(T("Provider categories"))}"></nav>
            <label class="ph-find-wrap">
              <svg class="ph-find-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><circle cx="11" cy="11" r="6"/><path d="M15.6 15.6L20 20"/></svg>
              <input id="phFind" class="ph-find" type="search" autocomplete="off" spellcheck="false"
                placeholder="${esc(T("Filter providers by name or key"))}" aria-label="${esc(T("Filter providers by name or key"))}">
            </label>
          </div>
          <div id="phContent" class="ph-content"></div>
          <div id="phModalHost"></div>
        </div>`,
      open() {
        syncStaticWords();
        const find = document.getElementById("phFind");
        if (find) {
          find.value = phState.filter;
          find.oninput = () => {
            phState.filter = find.value.trim().toLowerCase();
            paintTabCounts();
            renderContent();
          };
        }
        if (!phState.catalog) {
          if (!phState.loading) return loadCatalog();
          renderAll();
          return;
        }
        renderAll();
      },
      relang() {
        syncStaticWords();
        if (phState.catalog) renderAll();
      },
      refresh() {
        return loadCatalog();
      },
      reload() {
        return loadCatalog();
      },
      leave() {
        closeModal(true);
        phState.error = "";
        phState.flash = "";
      }
    });
  });
})();
