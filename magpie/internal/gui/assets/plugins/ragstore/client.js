// Memory & RAG — the memory bus.
//
// The page is one render pipeline over one state object, like the Detective
// desk next to it:
//   1. data     — /api/rag/{status,habits,hotswap,coldswap,records,search}
//   2. motion   — calm() before every programmatic animation, the eight
//                 patterns from 视觉样式与动画设计参考.md, and no scroll the
//                 reader did not ask for
//   3. views    — swap, habits, vault; each renders from the same state and
//                 binds its own controls
//
// Nothing here invents a figure: every count and every phase time came back
// from the backend, and a failed call is shown as the backend's error.

(function () {
  const RAG_TABS = [
    { id: "swap", key: "Hot-swap databases" },
    { id: "panels", key: "Database panels" },
    { id: "habits", key: "Memory layers and habits" },
    { id: "vault", key: "Evidence vault" }
  ];

  // One panel per backend, each drawn in its own idiom. The backend id is
  // the one /api/rag/status reports; the panel's own data comes from
  // /api/rag/panels and, for search, from the real /api/rag/search path.
  const DB_PANELS = [
    { id: "markdown", backend: "markdown_vault", key: "Markdown notes" },
    { id: "graph", backend: "neo4j_graphiti", key: "Graph topology" },
    { id: "vector", backend: "milvus_zilliz", key: "Vector space" },
    { id: "document", backend: "elasticsearch", key: "Document index" },
    { id: "relational", backend: "postgres_pgvector", key: "Relational tables" },
    { id: "search", backend: "sqlite_hybrid", key: "Full-text search" }
  ];

  const MEMORY_MODES = [
    { value: "persistent", key: "Persistent: five memory layers survive restarts" },
    { value: "ephemeral", key: "Ephemeral: nothing is written to memory" }
  ];

  const VERDICTS = ["UNVERIFIED", "VERIFIED", "REFUTED", "ABSTAIN"];

  let kit = null; // magpie.ui, for the app menu (never a native <select>)
  let viewSwap = null; // the running dual-segment tab animation, cancellable
  let feedSource = null; // the EventSource, when the browser has one
  let feedTimer = 0; // the polling fallback
  let feedStarted = false; // one feed per page, however often open() runs
  let panelMotion = []; // stop functions for the current panel's canvas

  let ragState = {
    subtab: "swap",
    // The swap request's own values. They start at what the server accepts
    // with no extra configuration and are replaced by /api/rag/status.
    embeddingModel: "local-ngram-1024",
    rerankerModel: "",
    memoryMode: "persistent",
    verdict: "UNVERIFIED",
    // What the backend reported, kept apart from the picker values above so
    // the "backend now" line never shows a choice a swap hasn't applied.
    backendModels: { embeddingModel: "", rerankerModel: "", memoryMode: "" },
    activeDbId: "",
    swapProgress: null,
    swapReport: null,
    syncMessage: "",
    syncTone: "ok",
    importMessage: "",
    vaultFilter: "",
    backends: [],
    habits: [],
    records: [],
    // The six database panels read one payload, loaded when the tab opens
    // and refreshed on demand. Switching between the six never reloads it.
    panelId: "markdown",
    panels: null,
    panelsBusy: false,
    panelsError: "",
    // The live operation feed: real events from /api/rag/events (or the
    // same operations polled from /api/rag/operations where SSE is not
    // available), and the agent registry they are drawn with.
    operations: [],
    opsCursor: 0,
    opsLive: false,
    opsError: "",
    agents: [],
    panelSearchHits: null,
    panelSearchTookMs: 0,
    panelSearchBusy: false,
    panelSearchQuery: "",
    panelSearchError: "",
    searchHits: null,
    searchTookMs: 0,
    searchBusy: false,
    searchQuery: "",
    searchError: "",
    flash: "",
    error: ""
  };

  // ---------- words and small helpers ------------------------------------

  // T is the shell's t at call time. The plugin loads before app.js, so a
  // direct alias would capture nothing; every string is looked up when shown.
  const T = (s, vars) => (typeof t === "function" ? t(s, vars) : s);

  function esc(s) {
    return String(s ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  // still is the reduced-motion answer. The shell's calm() when it exists,
  // the media query otherwise (a plugin test that never loads app.js).
  function still() {
    if (typeof calm === "function") return calm();
    return !!(window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  }

  function dot(tone, extra) {
    return `<span class="rag-dot rag-dot-${tone}${extra ? " " + extra : ""}" aria-hidden="true"></span>`;
  }

  function num(v, fallback) {
    const n = Number(v);
    return Number.isFinite(n) ? n : fallback;
  }

  function list(v) {
    return Array.isArray(v) ? v : [];
  }

  function toneClass(tone) {
    return tone ? " rag-badge-" + tone : "";
  }

  function badge(text, tone) {
    return `<span class="rag-badge${toneClass(tone)}">${esc(text)}</span>`;
  }

  function dbName(id) {
    const db = (ragState.backends || []).find((b) => b.id === id);
    return (db && db.name) || id || "—";
  }

  // ---------- data -------------------------------------------------------

  // ragApi answers {error} instead of null, so a failed call is visible on
  // the page rather than silently doing nothing.
  async function ragApi(path, opts) {
    try {
      const res = await fetch("/api/" + path, {
        headers: { "Content-Type": "application/json" },
        ...opts
      });
      const body = await res.json().catch(() => null);
      if (!res.ok) {
        return { error: (body && body.error) || "HTTP " + res.status };
      }
      return body || {};
    } catch (e) {
      return { error: e && e.message ? e.message : String(e) };
    }
  }

  function errText(res) {
    return (res && res.error) || T("The backend sent no answer");
  }

  function applyStatus(res) {
    if (!res || res.error) return false;
    ragState.activeDbId = res.activeDbId || ragState.activeDbId;
    // Only what the server reports feeds the request, so the page can never
    // send an id the backend did not name.
    ragState.embeddingModel = res.embeddingModel || ragState.embeddingModel;
    ragState.rerankerModel = res.rerankerModel || ragState.rerankerModel;
    if (res.memoryMode === "persistent" || res.memoryMode === "ephemeral") {
      ragState.memoryMode = res.memoryMode;
    }
    ragState.backendModels = {
      embeddingModel: res.embeddingModel || ragState.backendModels.embeddingModel,
      rerankerModel: res.rerankerModel || ragState.backendModels.rerankerModel,
      memoryMode: res.memoryMode || ragState.backendModels.memoryMode
    };
    ragState.backends = list(res.backends);
    ragState.records = list(res.records);
    // /api/rag/status now carries the habits, so one read fills both views.
    if (Array.isArray(res.habits)) ragState.habits = res.habits;
    return true;
  }

  async function loadRagStoreData() {
    const [statusRes, habitsRes] = await Promise.all([ragApi("rag/status"), ragApi("rag/habits")]);
    const errs = [];
    if (!applyStatus(statusRes)) {
      errs.push(T("Status failed: {error}", { error: errText(statusRes) }));
    }
    // Status carries habits already; the habits endpoint is the fallback for
    // a server that does not, and its failure alone is not a page error.
    if (habitsRes && !habitsRes.error) {
      if (!Array.isArray(ragState.habits) || !ragState.habits.length) {
        ragState.habits = list(habitsRes.habits);
      }
    }
    ragState.error = errs.join(" · ");
    renderRagAll();
    // A refresh reloads the panels too when they are what is on screen; a
    // first open is already handled by bindPanels, which loads them when
    // they are absent.
    if (ragState.subtab === "panels" && ragState.panels) loadPanelData();
  }

  async function refreshStatus() {
    const res = await ragApi("rag/status");
    if (!applyStatus(res)) {
      ragState.error = T("Status failed: {error}", { error: errText(res) });
      ragState.syncTone = "danger";
    }
    renderRagAll();
  }

  // ---------- actions ----------------------------------------------------

  async function triggerHotSwap(targetDbId) {
    ragState.error = "";
    ragState.swapReport = null;
    ragState.flash = "";
    ragState.swapProgress = {
      targetDbId,
      running: true,
      pct: null
    };
    renderRagAll();

    const res = await ragApi("rag/hotswap", {
      method: "POST",
      body: JSON.stringify({
        targetDbId,
        embeddingModel: ragState.embeddingModel || "local-ngram-1024",
        rerankerModel: ragState.rerankerModel,
        memoryMode: ragState.memoryMode === "ephemeral" ? "ephemeral" : "persistent"
      })
    });

    if (!res || res.error) {
      // A non-2xx answer means the swap did not happen: the backend leaves
      // the old one serving, so the page shows the server's error and
      // leaves activeDbId exactly as it was.
      ragState.error = T("Hot-swap failed: {error}", { error: errText(res) });
      ragState.swapProgress = null;
      renderRagAll();
      return;
    }

    applyStatus(res);
    ragState.panels = null; // the corpus changed: the six views are stale
    const swap = res.swap || null;
    ragState.swapReport = swap;
    ragState.swapProgress = {
      targetDbId,
      running: false,
      pct: swap ? 100 : null,
      swap
    };
    ragState.flash = swap ? "swap" : "";
    renderRagAll();
  }

  async function triggerColdExport() {
    const res = await ragApi("rag/coldswap/export", { method: "POST" });
    if (!res || res.error) {
      ragState.syncTone = "danger";
      ragState.syncMessage = T("Export failed: {error}", { error: errText(res) });
      ragState.flash = "sync";
      renderRagAll();
      return;
    }
    ragState.syncTone = "ok";
    ragState.syncMessage = T("Cold bundle exported: {path} ({n} records)", {
      path: res.bundlePath || "—",
      n: num(res.recordCount, 0)
    });
    ragState.error = "";
    ragState.flash = "sync";
    renderRagAll();
  }

  async function triggerColdImport(file) {
    if (!file) return;
    const text = await file.text();
    ragState.importMessage = T("Importing {name}…", { name: file.name });
    ragState.flash = "";
    renderRagAll();
    const res = await ragApi("rag/coldswap/import", {
      method: "POST",
      body: JSON.stringify({ jsonl: text })
    });
    if (!res || res.error) {
      ragState.importMessage = "";
      ragState.error = T("Import failed: {error}", { error: errText(res) });
      renderRagAll();
      return;
    }
    ragState.importMessage = T("Imported {n} memories from {name}.", {
      n: num(res.imported, 0),
      name: file.name
    });
    ragState.error = "";
    ragState.flash = "import";
    ragState.panels = null;
    await refreshStatus();
  }

  async function triggerSyncAgentFiles() {
    const res = await ragApi("rag/habits/sync-agent-files", { method: "POST" });
    if (!res || res.error) {
      ragState.syncTone = "danger";
      ragState.syncMessage = T("Sync failed: {error}", { error: errText(res) });
    } else if (Array.isArray(res.updatedFiles) && res.updatedFiles.length) {
      ragState.syncTone = "ok";
      ragState.syncMessage = T("Memories and habits synced to {files}", {
        files: res.updatedFiles.join(", ")
      });
    } else {
      ragState.syncTone = "ok";
      ragState.syncMessage = T("Sync finished.");
    }
    ragState.flash = "sync";
    renderRagAll();
  }

  function readField(id) {
    const node = document.getElementById(id);
    return node && node.value ? node.value : "";
  }

  async function submitRagRecord() {
    const topic = readField("ragAddTopic");
    const claim = readField("ragAddClaim");
    const evidenceText = readField("ragAddEvidence");
    const sourceUrl = readField("ragAddSource");
    if (!topic.trim() || !evidenceText.trim()) {
      ragState.error = T("Topic and evidence are required.");
      renderRagAll();
      return;
    }
    const rec = {
      topic: topic.trim(),
      claim: claim.trim(),
      evidenceText: evidenceText.trim(),
      verdict: ragState.verdict || "UNVERIFIED",
      sourceUrl: sourceUrl.trim(),
      relationEdge: "SUPERSEDES",
      validFrom: new Date().toISOString()
    };
    const res = await ragApi("rag/records", { method: "POST", body: JSON.stringify(rec) });
    if (!res || res.error) {
      ragState.error = T("Writing the memory failed: {error}", { error: errText(res) });
      renderRagAll();
      return;
    }
    ragState.syncTone = "ok";
    ragState.syncMessage = T("Memory saved: {topic}", {
      topic: (res.record && res.record.topic) || rec.topic
    });
    ragState.error = "";
    ragState.flash = "sync";
    ragState.panels = null; // the corpus grew: the panels read it again
    await refreshStatus();
  }

  async function runRagSearch() {
    const input = document.getElementById("ragSearchBox");
    const query = input ? input.value.trim() : "";
    if (!query) {
      ragState.error = T("Search query is required.");
      renderRagAll();
      return;
    }
    ragState.searchBusy = true;
    ragState.searchQuery = query;
    ragState.searchError = "";
    paintSearchBusy();
    const results = document.getElementById("ragSearchResults");
    if (results) results.innerHTML = searchSkeletonHTML();
    const res = await ragApi("rag/search", { method: "POST", body: JSON.stringify({ query, topK: 5 }) });
    ragState.searchBusy = false;
    if (!res || res.error) {
      ragState.searchHits = null;
      ragState.searchError = T("Search failed: {error}", { error: errText(res) });
      if (results) results.innerHTML = searchResultsHTML();
      paintSearchBusy();
      return;
    }
    ragState.searchHits = list(res.hits);
    ragState.searchTookMs = num(res.tookMs, 0);
    ragState.error = "";
    if (results) {
      results.innerHTML = searchResultsHTML();
      if (!still()) {
        const first = results.querySelector(".rag-hit");
        if (first) {
          first.animate(
            [{ opacity: 0, transform: "translateY(-4px)" }, { opacity: 1, transform: "translateY(0)" }],
            { duration: 240, easing: "cubic-bezier(.22, 1, .36, 1)" }
          );
        }
      }
    }
    paintSearchBusy();
  }

  function paintSearchBusy() {
    const btn = document.getElementById("ragSearchBtn");
    if (btn) {
      btn.disabled = ragState.searchBusy;
      btn.textContent = ragState.searchBusy ? T("Searching the vault…") : T("Search the vault");
    }
    const box = document.getElementById("ragSearchBox");
    if (box && box.value !== ragState.searchQuery) box.value = ragState.searchQuery;
  }

  // ---------- motion -----------------------------------------------------

  // swapView is the tab change: out in 75 ms on the in curve, swap, in in
  // 190 ms on the out curve, with the direction following the tab order.
  // A second click cancels the first, so the content cannot land twice.
  function swapView(next, ev) {
    if (next === ragState.subtab) return;
    const order = RAG_TABS.map((x) => x.id);
    const dir = order.indexOf(next) > order.indexOf(ragState.subtab) ? 1 : -1;
    const content = document.getElementById("ragContent");
    if (still() || !content || typeof content.animate !== "function") {
      ragState.subtab = next;
      renderRagAll();
      return;
    }
    if (viewSwap) {
      viewSwap.cancel();
      viewSwap = null;
    }
    const out = content.animate(
      [{ opacity: 1, transform: "translate3d(0,0,0)" }, { opacity: 0, transform: `translate3d(${-dir * 5}px,0,0)` }],
      { duration: 75, easing: "cubic-bezier(.4, 0, 1, 1)", fill: "forwards" }
    );
    viewSwap = out;
    out.finished
      .then(() => {
        if (viewSwap !== out) return;
        viewSwap = null;
        out.cancel();
        ragState.subtab = next;
        renderRagAll();
        const fresh = document.getElementById("ragContent");
        if (fresh) {
          fresh.animate(
            [{ opacity: 0, transform: `translate3d(${dir * 7}px,0,0)` }, { opacity: 1, transform: "translate3d(0,0,0)" }],
            { duration: 190, easing: "cubic-bezier(.22, 1, .36, 1)" }
          );
        }
      })
      .catch(() => {});
  }

  // flashClass hands a one-shot success animation to the message the action
  // just produced, then forgets it so a later render stays quiet.
  function flashClass(name) {
    if (ragState.flash !== name) return "";
    ragState.flash = "";
    return still() ? "" : " rag-flash";
  }

  // ---------- view pieces ------------------------------------------------

  function heroHTML() {
    const active = ragState.activeDbId ? dbName(ragState.activeDbId) : "—";
    return `
      <div class="rag-hero-body">
        <div class="rag-brand">
          <span class="rag-brand-mark" aria-hidden="true"></span>
          <div>
            <h1 class="rag-brand-name">${esc(T("Memory & RAG"))}</h1>
            <p class="rag-brand-sub">${esc(
              T("One memory bus for the evidence, the five layers and every database backend.")
            )}</p>
          </div>
        </div>
        <div class="rag-stats">
          <div class="rag-stat">
            ${dot(ragState.activeDbId ? "ok" : "muted")}
            <span>${esc(T("Active database"))}</span>
            <b>${esc(active)}</b>
          </div>
          <div class="rag-stat">
            <span class="rag-stat-n">${ragState.records.length}</span>
            <span>${esc(T("Memories kept"))}</span>
          </div>
          <div class="rag-stat">
            <span class="rag-stat-n">${habitRows().length}</span>
            <span>${esc(T("Learned habits"))}</span>
          </div>
        </div>
      </div>`;
  }

  function tabsHTML() {
    return RAG_TABS.map((tab) => {
      const on = ragState.subtab === tab.id;
      const badgeText =
        tab.id === "swap"
          ? ragState.activeDbId || "—"
          : tab.id === "panels"
            ? String(list(ragState.backends).length)
            : tab.id === "habits"
              ? String(habitRows().length)
              : String(ragState.records.length);
      return `
        <button type="button" class="rag-tab${on ? " on" : ""}" role="tab" id="ragTab-${tab.id}"
          aria-selected="${on}" aria-controls="ragContent" data-rag-tab="${tab.id}">
          <span>${esc(T(tab.key))}</span>
          <span class="rag-tab-badge">${esc(badgeText)}</span>
        </button>`;
    }).join("");
  }

  function errorBanner() {
    if (!ragState.error) return "";
    return `<div class="rag-alert" role="alert">${dot("pink")}<span>${esc(ragState.error)}</span></div>`;
  }

  function messageHTML(text, tone, flash) {
    if (!text) return "";
    return `<div class="rag-msg rag-msg-${tone}${flash}" role="status">${dot(tone === "ok" ? "ok" : "pink")}<span>${esc(
      text
    )}</span></div>`;
  }

  function modelFieldsHTML() {
    return `
      <div class="rag-fields">
        <div class="rag-field">
          <label>${esc(T("Embedding model"))}</label>
          <div class="rag-menu-slot" id="ragEmbedSlot"></div>
          <p class="rag-hint">${esc(T("Backend now: {model}", { model: ragState.backendModels.embeddingModel || "—" }))}</p>
        </div>
        <div class="rag-field">
          <label>${esc(T("Cross-encoder reranker"))}</label>
          <div class="rag-readonly">${esc(ragState.rerankerModel || "—")}</div>
          <p class="rag-hint">${esc(T("Chosen by the backend and sent back with a swap."))}</p>
        </div>
        <div class="rag-field">
          <label>${esc(T("Memory mode"))}</label>
          <div class="rag-menu-slot" id="ragMemSlot"></div>
          <p class="rag-hint">${esc(T("Backend now: {mode}", { mode: ragState.backendModels.memoryMode || "—" }))}</p>
        </div>
      </div>`;
  }

  function progressHTML() {
    const p = ragState.swapProgress;
    if (!p) return "";
    const swap = p.swap || ragState.swapReport;
    const pct = p.running ? null : num(p.pct, null);
    const head = p.running
      ? T("Hot-swap engine")
      : T("Swap complete");
    const line = p.running
      ? T("Running the three phases; times come from the backend.")
      : swap
        ? T("Migrated {from} → {to} · {n} records · embedder {model}", {
            from: swap.from || "—",
            to: swap.to || p.targetDbId || "—",
            n: num(swap.records, 0),
            model: swap.embeddingModel || "—"
          })
        : T("Switched to {db}; the backend sent no phase detail.", { db: ragState.activeDbId || p.targetDbId });
    const phases = swap
      ? `<div class="rag-phases">
          <div><span>${esc(T("Phase 1 · dual write"))}</span><b>${num(swap.phase1WriteMs, 0)} ms</b></div>
          <div><span>${esc(T("Phase 2 · embed"))}</span><b>${num(swap.phase2EmbedMs, 0)} ms</b></div>
          <div><span>${esc(T("Phase 3 · atomic switch"))}</span><b>${num(swap.phase3SwitchMs, 0)} ms</b></div>
          <div><span>${esc(T("Total time"))}</span><b>${num(swap.totalMs, 0)} ms</b></div>
        </div>
        <div class="rag-kv">
          <span>${esc(T("Backup digest (SHA-256)"))}: ${esc(swap.sha256 || "—")}</span>
          <span>${esc(T("Backup before the swap"))}: ${esc(swap.backupPath || "—")}</span>
        </div>
        ${
          Array.isArray(swap.log) && swap.log.length
            ? `<details class="rag-log"><summary>${esc(T("Swap log"))}</summary><pre>${esc(
                swap.log.join("\n")
              )}</pre></details>`
            : ""
        }`
      : "";
    const flash = flashClass("swap");
    return `
      <section class="rag-card rag-progress-card${flash}" aria-live="polite">
        <div class="rag-sec-head">
          <h2 class="rag-h2">${esc(head)}</h2>
          <span class="rag-tag">${p.running ? dot("blue", "rag-pulse") : dot("ok")}${
            p.running ? esc(T("In progress")) : esc(T("Swap finished"))
          }</span>
        </div>
        <p class="rag-note">${esc(line)}</p>
        <div class="rag-progress${p.running ? " is-running" : ""}">
          <div class="rag-progress-fill" style="width:${pct === null ? "0" : pct}%;"></div>
        </div>
        ${phases}
      </section>`;
  }

  function backendCard(db) {
    const isActive = db.id === ragState.activeDbId;
    return `
      <article class="rag-db-card${isActive ? " active" : ""}">
        <div class="rag-db-head">
          <div class="rag-db-title">
            ${dot(isActive ? "ok" : "muted")}
            <div>
              <b>${esc(db.name || db.id)}</b>
              <span class="rag-mono">${esc(db.engineType || "")}</span>
            </div>
          </div>
          ${badge(isActive ? T("Active backend") : T("Standby backend"), isActive ? "ok" : "muted")}
        </div>
        <p class="rag-note">${esc(db.description || "")}</p>
        <div class="rag-kv">
          <span>${esc(T("Storage path or DSN"))}: ${esc(db.dsn || "—")}</span>
          <span>${esc(T("Records synced"))}: ${num(db.recordCount, 0)} · ${esc(
            T("Index latency")
          )}: ${esc(db.avgLatency || "—")}</span>
        </div>
        <div class="rag-db-foot">
          <div class="rag-chiplist">${
            db.capabilities
              ? String(db.capabilities)
                  .split("·")
                  .map((c) => badge(c.trim(), "soft"))
                  .join("")
              : ""
          }</div>
          ${
            isActive
              ? `<button type="button" class="rag-btn" disabled>${esc(T("Serving reads and writes"))}</button>`
              : `<button type="button" class="rag-btn primary" data-hotswap-db="${esc(db.id)}">${esc(
                  T("Hot-swap here")
                )}</button>`
          }
        </div>
      </article>`;
  }

  function searchSkeletonHTML() {
    if (still()) return `<p class="rag-empty">${esc(T("Searching the vault…"))}</p>`;
    return `<div class="rag-skeleton" aria-hidden="true"><i></i><i></i><i></i></div>`;
  }

  function searchResultsHTML() {
    if (ragState.searchError) {
      return `<div class="rag-alert" role="alert">${dot("pink")}<span>${esc(ragState.searchError)}</span></div>`;
    }
    if (ragState.searchHits === null) return "";
    const hits = list(ragState.searchHits);
    if (!hits.length) return `<p class="rag-empty">${esc(T("No memory matched."))}</p>`;
    return `
      <div class="rag-result-meta">${esc(
        T("{n} hits · {ms} ms", { n: hits.length, ms: ragState.searchTookMs })
      )}</div>
      <div class="rag-hits">
        ${hits
          .map(
            (h, i) => `
          <div class="rag-hit">
            <div class="rag-hit-head">
              <b>[${i + 1}] ${esc((h.record && h.record.topic) || "—")}</b>
              ${badge(
                (h.record && h.record.verdict) || "—",
                h.record && h.record.verdict === "REFUTED" ? "pink" : "ok"
              )}
            </div>
            <p class="rag-note">${esc((h.record && (h.record.claim || h.record.evidenceText)) || "")}</p>
            <div class="rag-mono">cosine ${num(h.cosine, 0).toFixed(4)} · bm25 ${num(h.bm25, 0).toFixed(
              4
            )} · rrf ${num(h.rrf, 0).toFixed(4)}</div>
            ${
              h.record && h.record.sourceUrl
                ? `<a class="rag-cite" href="${esc(h.record.sourceUrl)}" target="_blank" rel="noopener noreferrer">${esc(
                    h.record.sourceDomain || h.record.sourceUrl
                  )} ↗</a>`
                : ""
            }
          </div>`
          )
          .join("")}
      </div>`;
  }

  function swapHTML() {
    const backends = list(ragState.backends);
    return `
      ${errorBanner()}
      <section class="rag-card">
        <div class="rag-sec-head">
          <div>
            <h2 class="rag-h2">${esc(T("Models and memory mode"))}</h2>
            <p class="rag-note">${esc(
              T("Choose the embedder, the reranker and how much memory survives a restart. A swap moves the records between backends over USMF.")
            )}</p>
          </div>
          <div class="rag-actions">
            <button type="button" class="rag-btn" id="ragColdExportBtn">${esc(T("Export a cold bundle"))}</button>
            <button type="button" class="rag-btn" id="ragColdImportBtn">${esc(T("Import a cold bundle"))}</button>
            <input type="file" accept=".jsonl,.json,application/json" id="ragImportInput" hidden />
            <button type="button" class="rag-btn" id="ragSyncBtn">${esc(T("Write memories to agent files"))}</button>
          </div>
        </div>
        ${modelFieldsHTML()}
        ${ragState.importMessage ? messageHTML(ragState.importMessage, "ok", flashClass("import")) : ""}
        ${ragState.syncMessage ? messageHTML(ragState.syncMessage, ragState.syncTone, flashClass("sync")) : ""}
      </section>

      ${progressHTML()}

      <div class="rag-sec-head rag-sec-plain">
        <h2 class="rag-h2">${esc(T("Database backends"))}</h2>
        <p class="rag-note">${esc(T("Six pluggable backends; the active one serves every read and write."))}</p>
      </div>
      <div class="rag-db-grid">
        ${backends.length ? backends.map(backendCard).join("") : `<p class="rag-empty">${esc(T("No backend reported."))}</p>`}
      </div>

      <section class="rag-card">
        <div class="rag-sec-head">
          <div>
            <h2 class="rag-h2">${esc(T("Hybrid retrieval test"))}</h2>
            <p class="rag-note">${esc(T("Search the stored memories with dense, BM25 and RRF scores."))}</p>
          </div>
          <span class="rag-tag">topK = 5</span>
        </div>
        <div class="rag-search-row">
          <input id="ragSearchBox" class="rag-input" type="search" autocomplete="off"
            value="${esc(ragState.searchQuery)}" placeholder="${esc(T("Ask the vault…"))}" />
          <button type="button" class="rag-btn primary" id="ragSearchBtn">${esc(
            ragState.searchBusy ? T("Searching the vault…") : T("Search the vault")
          )}</button>
        </div>
        <div id="ragSearchResults" class="rag-search-results">${searchResultsHTML()}</div>
      </section>

      <section class="rag-card" id="ragAddCard">
        <div class="rag-sec-head">
          <div>
            <h2 class="rag-h2">${esc(T("Write a memory"))}</h2>
            <p class="rag-note">${esc(T("A canonical record with its source, verdict and bi-temporal window."))}</p>
          </div>
        </div>
        <div class="rag-write-grid">
          <label class="rag-field"><span>${esc(T("Memory topic"))}</span><input id="ragAddTopic" class="rag-input" type="text" autocomplete="off" /></label>
          <label class="rag-field"><span>${esc(T("Memory claim"))}</span><input id="ragAddClaim" class="rag-input" type="text" autocomplete="off" /></label>
          <label class="rag-field"><span>${esc(T("Evidence source URL"))}</span><input id="ragAddSource" class="rag-input" type="text" autocomplete="off" /></label>
          <label class="rag-field"><span>${esc(T("Record verdict"))}</span><div class="rag-menu-slot" id="ragVerdictSlot"></div></label>
        </div>
        <label class="rag-field"><span>${esc(T("Verified evidence"))}</span><input id="ragAddEvidence" class="rag-input" type="text" autocomplete="off" /></label>
        <div class="rag-actions">
          <button type="button" class="rag-btn primary" id="ragAddSubmit">${esc(T("Save memory"))}</button>
        </div>
      </section>`;
  }

  function habitRows() {
    return Array.isArray(ragState.habits) ? ragState.habits : [];
  }

  function layersHTML() {
    const tiers = [
      { key: "L0 · Working and redaction memory", desc: "Redaction placeholders live in this process only, and are gone when it stops.", stat: "" },
      { key: "L1 · Episodic search trajectory", desc: "Sub-queries, SimHash fingerprints and URLs from the current investigation, so one task never pays twice.", stat: "" },
      { key: "L2 · Bi-temporal fact graph", desc: "Every verified fact carries valid_from / invalid_at and SUPPORTS, CONTRADICTS or SUPERSEDES edges.", stat: T("{n} facts kept", { n: ragState.records.length }) },
      { key: "L3 · Search habits and procedural skills", desc: "Which mode wins for which question, the trusted domains and the content farms to skip.", stat: T("{n} habits learned", { n: habitRows().length }) },
      { key: "L4 · Archival USMF vault", desc: "A permanent .magpie-rag.jsonl archive that survives a backend swap.", stat: `${ragState.activeDbId || "—"} · ${ragState.records.length}` }
    ];
    return `
      <section class="rag-card">
        <div class="rag-sec-head">
          <div>
            <h2 class="rag-h2">${esc(T("Five memory layers"))}</h2>
            <p class="rag-note">${esc(T("Each layer is separate: what is redacted, what is episodic, what is evidence, what is learned."))}</p>
          </div>
        </div>
        <div class="rag-tier-list">
          ${tiers
            .map(
              (tier, i) => `
            <div class="rag-tier">
              <span class="rag-tier-index">${i}</span>
              <div class="rag-tier-body">
                <b>${esc(T(tier.key))}</b>
                <p class="rag-note">${esc(T(tier.desc))}</p>
              </div>
              ${tier.stat ? badge(tier.stat, "soft") : ""}
            </div>`
            )
            .join("")}
        </div>
      </section>`;
  }

  function habitsHTML() {
    const habits = habitRows();
    const shown = habits.slice(0, 5);
    // The two domain lists are the union across every learned pattern, as
    // the server keys them per habit row.
    const union = (key) => {
      const out = [];
      for (const h of habits) {
        for (const d of h[key] || []) if (d && !out.includes(d)) out.push(d);
      }
      return out;
    };
    const trustedAll = union("trustedDomains");
    const blockedAll = union("blockedDomains");
    const noHabits = shown.length === 0 && trustedAll.length === 0 && blockedAll.length === 0;

    return `
      ${errorBanner()}
      ${layersHTML()}

      <section class="rag-card">
        <div class="rag-sec-head">
          <div>
            <h2 class="rag-h2">${esc(T("Learned search habits"))}</h2>
            <p class="rag-note">${esc(T("Updated after each investigation; the five strongest are shown."))}</p>
          </div>
          <button type="button" class="rag-btn primary" id="ragSyncHabitBtn">${esc(T("Write habits to agent files"))}</button>
        </div>
        ${
          noHabits
            ? `<p class="rag-empty">${esc(T("No habits yet. Run one investigation first."))}</p>`
            : `<div class="rag-habit-grid">
                <div class="rag-subcard">
                  <b>${esc(T("Mode affinity"))}</b>
                  <div class="rag-affinity">
                    ${
                      shown.length
                        ? shown
                            .map((h) => {
                              const modes = Object.entries(h.modeAffinity || {})
                                .sort((a, b) => Number(b[1]) - Number(a[1]))
                                .slice(0, 3);
                              return `
                        <div class="rag-affinity-row">
                          <div class="rag-affinity-head">
                            <b>${esc(h.pattern)}</b>
                            <span class="rag-mono">${esc(h.updatedAt || "—")}</span>
                          </div>
                          <div class="rag-affinity-modes">
                            ${
                              modes.length
                                ? modes
                                    .map(
                                      ([m, w]) =>
                                        `<span class="rag-affinity-mode">${esc(m)} <b>${Math.round(
                                          Number(w) * 100
                                        )}%</b></span>`
                                    )
                                    .join("")
                                : `<span class="rag-note">${esc(T("No affinity yet."))}</span>`
                            }
                          </div>
                        </div>`;
                            })
                            .join("")
                        : `<p class="rag-empty">${esc(T("No habit patterns yet."))}</p>`
                    }
                  </div>
                </div>
                <div class="rag-domain-stack">
                  <div class="rag-subcard">
                    <div class="rag-subhead">${dot("ok")}<b>${esc(T("Trusted domains"))}</b></div>
                    <div class="rag-chiplist">${
                      trustedAll.length
                        ? trustedAll.map((d) => badge(d, "ok")).join("")
                        : `<span class="rag-note">${esc(T("None yet."))}</span>`
                    }</div>
                  </div>
                  <div class="rag-subcard">
                    <div class="rag-subhead">${dot("pink")}<b>${esc(T("Blocked domains"))}</b></div>
                    <div class="rag-chiplist">${
                      blockedAll.length
                        ? blockedAll.map((d) => badge(d, "pink")).join("")
                        : `<span class="rag-note">${esc(T("None yet."))}</span>`
                    }</div>
                  </div>
                </div>
              </div>`
        }
        ${ragState.syncMessage ? messageHTML(ragState.syncMessage, ragState.syncTone, flashClass("sync")) : ""}
      </section>`;
  }

  function vaultListHTML() {
    const recs = list(ragState.records);
    const q = (ragState.vaultFilter || "").trim().toLowerCase();
    const filtered = !q
      ? recs
      : recs.filter((r) =>
          ((r.claim || "") + " " + (r.evidenceText || "") + " " + (r.topic || "") + " " + (r.sourceDomain || ""))
            .toLowerCase()
            .includes(q)
        );
    if (!filtered.length) {
      return `<p class="rag-empty">${esc(q ? T("No matching record.") : T("No record is stored yet."))}</p>`;
    }
    return filtered
      .map(
        (rec) => `
      <article class="rag-record">
        <div class="rag-record-head">
          <div class="rag-record-ids">
            ${badge(rec.memoryId || "—", "soft")}
            ${badge(rec.verdict || "—", rec.verdict === "REFUTED" ? "pink" : "ok")}
            ${rec.relationEdge ? badge(rec.relationEdge, "muted") : ""}
            <b>${esc(rec.topic || "—")}</b>
          </div>
          <span class="rag-mono">${esc(
            T("valid from {from} · {to}", {
              from: rec.validFrom || "—",
              to: rec.invalidAt || T("no end date")
            })
          )}</span>
        </div>
        <div class="rag-record-claim">${esc(rec.claim || "—")}</div>
        <p class="rag-note">${esc(rec.evidenceText || "")}</p>
        <div class="rag-record-foot">
          <span class="rag-mono">${esc(rec.embeddingModel || "—")} · ${esc(
            T("Authority L{n}", { n: num(rec.authorityLevel, 0) })
          )}</span>
          ${
            rec.sourceUrl
              ? `<a class="rag-cite" href="${esc(rec.sourceUrl)}" target="_blank" rel="noopener noreferrer">${esc(
                  rec.sourceDomain || rec.sourceUrl
                )} ↗</a>`
              : ""
          }
        </div>
      </article>`
      )
      .join("");
  }

  function vaultHTML() {
    return `
      ${errorBanner()}
      <section class="rag-card">
        <div class="rag-sec-head">
          <div>
            <h2 class="rag-h2">${esc(T("Evidence and memories across databases"))}</h2>
            <p class="rag-note">${esc(
              T("Active database: {db} · embedder {model}", {
                db: ragState.activeDbId ? dbName(ragState.activeDbId) : "—",
                model: ragState.backendModels.embeddingModel || "—"
              })
            )}</p>
          </div>
          <input id="ragVaultSearch" class="rag-input rag-vault-filter" type="search" autocomplete="off"
            value="${esc(ragState.vaultFilter)}" placeholder="${esc(T("Filter the stored evidence…"))}" />
        </div>
        <div id="ragVaultList" class="rag-vault-list">${vaultListHTML()}</div>
      </section>`;
  }

  // ---------- database panels --------------------------------------------

  // Every panel below draws only what /api/rag/panels or /api/rag/search
  // answered. A backend the status call could not reach is shown as
  // unavailable, and the local mirror it writes is named as such rather than
  // passed off as the server's own answer.

  function safeJSON(text) {
    try {
      return JSON.parse(text);
    } catch (e) {
      return null;
    }
  }

  function excerpt(text, n) {
    const s = String(text || "").replace(/\s+/g, " ").trim();
    return s.length > n ? s.slice(0, n - 1) + "…" : s;
  }

  function fmtBytes(n) {
    const v = num(n, 0);
    if (v < 1024) return v + " B";
    if (v < 1024 * 1024) return (v / 1024).toFixed(1) + " KB";
    return (v / (1024 * 1024)).toFixed(1) + " MB";
  }

  function fmtTime(iso) {
    const d = new Date(iso);
    if (isNaN(d.getTime())) return iso ? String(iso) : "—";
    return d.toLocaleTimeString([], { hour12: false });
  }

  function validityText(r) {
    const from = r.validFrom || "—";
    const to = r.invalidAt || T("no end date");
    return T("valid from {from} · {to}", { from, to });
  }

  // An edge carries the same bi-temporal window as the record that wrote it;
  // shown when the backend recorded one, never filled in otherwise.
  function edgeWindow(e) {
    if (!e.validFrom && !e.invalidAt) return "";
    return `<span class="rag-mono rag-edge-window">${esc(e.validFrom || "—")} → ${esc(
      e.invalidAt || T("no end date")
    )}</span>`;
  }

  function verdictTone(verdict) {
    if (verdict === "REFUTED") return "pink";
    if (verdict === "VERIFIED") return "ok";
    return "muted";
  }

  function panelById(id) {
    return DB_PANELS.find((p) => p.id === id) || DB_PANELS[0];
  }

  function panelBackend(panel) {
    return list(ragState.backends).find((b) => b.id === panel.backend) || null;
  }

  function panelStateBadge(panel) {
    const b = panelBackend(panel);
    if (!b) return badge(T("No backend reported."), "muted");
    return b.reachable === false ? badge(T("Backend unavailable"), "pink") : badge(T("Reachable now"), "ok");
  }

  function panelHeadHTML() {
    const p = panelById(ragState.panelId);
    const b = panelBackend(p);
    return `
      <div>
        <h3 class="rag-panel-title" id="ragPanelTitle">${esc(T(p.key))}</h3>
        <p class="rag-note">${esc(T(PANEL_SUB[p.id] || ""))}</p>
      </div>
      <div class="rag-panel-state">
        ${panelStateBadge(p)}
        ${b ? `<span class="rag-mono">${esc(T("{n} records · {latency}", { n: num(b.recordCount, 0), latency: b.avgLatency || "—" }))}</span>` : ""}
      </div>`;
  }

  const PANEL_SUB = {
    markdown: "Minimal typography over the vault's real notes.",
    graph: "A live 3D topology of the relation and temporal edges.",
    vector: "The stored embeddings, projected onto their three widest dimensions.",
    document: "The evidence documents this store indexes.",
    relational: "The SQL schema and validity windows behind the mirror.",
    search: "FTS5 or in-process BM25, with the three real scores."
  };

  function panelTabHTML(panel) {
    const on = ragState.panelId === panel.id;
    const b = panelBackend(panel);
    const down = b && b.reachable === false;
    return `
      <button type="button" class="rag-panel-tab${on ? " on" : ""}${down ? " is-down" : ""}" role="tab"
        id="ragPanelTab-${panel.id}" aria-selected="${on}" aria-controls="ragPanelBody"
        tabindex="${on ? "0" : "-1"}" data-rag-panel="${panel.id}">
        <span class="rag-panel-dot${down ? " down" : ""}" aria-hidden="true"></span>
        <span>${esc(T(panel.key))}</span>
      </button>`;
  }

  function downBannerHTML() {
    const b = panelBackend(panelById(ragState.panelId));
    if (!b || b.reachable !== false) return "";
    return `<div class="rag-panel-down" role="status">${dot("pink")}<b>${esc(T("Backend unavailable"))}</b>
      <span>${esc(T("The server did not answer; this panel reads the local mirror."))}</span>
      <span class="rag-mono">${esc(b.dsn || "")}</span></div>`;
  }

  function panelError(message) {
    return `<div class="rag-panel-down" role="status">${dot("pink")}<b>${esc(T("Backend unavailable"))}</b>
      <span class="rag-mono">${esc(message || "")}</span></div>`;
  }

  function statTile(label, value) {
    return `<div class="rag-stat-tile"><span class="rag-stat-n">${esc(String(value))}</span><span>${esc(label)}</span></div>`;
  }

  function panelsHTML() {
    const p = panelById(ragState.panelId);
    return `
      ${errorBanner()}
      <div class="rag-panels-layout">
        <section class="rag-card rag-panels-main">
          <div class="rag-sec-head">
            <div>
              <h2 class="rag-h2">${esc(T("Database panels"))}</h2>
              <p class="rag-note">${esc(T("Six backends, six readings: each panel draws what that store really holds."))}</p>
            </div>
            <button type="button" class="rag-btn" id="ragPanelsRefresh"${ragState.panelsBusy ? " disabled" : ""}>${esc(
              ragState.panelsBusy ? T("Refreshing the panels…") : T("Refresh panels")
            )}</button>
          </div>
          <div class="rag-panel-tabs" id="ragPanelTabs" role="tablist" aria-label="${esc(T("Database panels"))}">
            ${DB_PANELS.map(panelTabHTML).join("")}
          </div>
          <div class="rag-panel-head" id="ragPanelHead">${panelHeadHTML()}</div>
          <div id="ragPanelDownWrap">${downBannerHTML()}</div>
          <div id="ragPanelBody" class="rag-panel-body" role="tabpanel" aria-labelledby="ragPanelTab-${p.id}">
            ${panelBodyHTML()}
          </div>
        </section>
        <aside class="rag-card rag-feed" aria-labelledby="ragFeedTitle">
          <div class="rag-sec-head">
            <div>
              <h2 class="rag-h2" id="ragFeedTitle">${esc(T("Live operations"))}</h2>
              <p class="rag-note">${esc(T("Reads and writes as they happen; each row is one real backend call."))}</p>
            </div>
            <span class="rag-tag" id="ragFeedState">${ragState.opsLive ? dot("ok", "rag-pulse") : dot("muted")}<span>${esc(
              ragState.opsLive ? T("Live feed") : ragState.opsError ? T("Backend unavailable") : T("Waiting for the backend…")
            )}</span></span>
          </div>
          ${ragState.opsError && !ragState.opsLive ? `<p class="rag-note rag-feed-error">${esc(ragState.opsError)}</p>` : ""}
          <div id="ragFeedList" class="rag-feed-list">${feedHTML()}</div>
          <p id="ragFeedAnnounce" class="rag-sr-only" aria-live="polite"></p>
        </aside>
      </div>`;
  }

  function panelBodyHTML() {
    const d = ragState.panels;
    if (ragState.panelsError) {
      return `<p class="rag-empty">${esc(T("Panel data failed: {error}", { error: ragState.panelsError }))}</p>`;
    }
    if (!d) {
      return `<div class="rag-panel-loading">${still() ? dot("muted") : dot("blue", "rag-pulse")}<span>${esc(
        T("Loading the backend views…")
      )}</span></div>`;
    }
    if (ragState.panelId === "graph") return graphPanelHTML(d);
    if (ragState.panelId === "vector") return vectorPanelHTML(d);
    if (ragState.panelId === "document") return documentPanelHTML(d);
    if (ragState.panelId === "relational") return relationalPanelHTML(d);
    if (ragState.panelId === "search") return searchPanelHTML(d);
    return markdownPanelHTML(d);
  }

  // --- markdown: minimal typography, real notes ---------------------------

  function markdownPanelHTML(d) {
    const recs = list(d.records);
    const docs = d.documents || {};
    const notes = list(docs.notes);
    if (!recs.length) return `<p class="rag-empty">${esc(T("No memory note is stored yet."))}</p>`;
    const byMem = new Map(notes.map((n) => [n.memoryId, n]));
    const shown = recs.slice(0, 8);
    const cards = shown
      .map((r, i) => {
        const note = byMem.get(r.memoryId);
        return `
        <article class="rag-md-card" tabindex="0" style="--rag-md-i:${i}">
          <div class="rag-md-top">
            <span class="rag-md-verdict">${esc(r.verdict || "—")}</span>
            <span class="rag-md-domain">${esc(r.sourceDomain || "—")}</span>
          </div>
          <h4 class="rag-md-title">${esc(r.topic || "—")}</h4>
          <p class="rag-md-claim">${esc(r.claim || "")}</p>
          <p class="rag-md-evidence">${esc(excerpt(r.evidenceText, 170))}</p>
          <div class="rag-md-foot">
            <span class="rag-mono">${esc(validityText(r))}</span>
            ${
              note
                ? `<span class="rag-mono">${esc(note.file)} · ${esc(fmtBytes(note.bytes))}</span>`
                : `<span class="rag-mono">${esc(T("Not in the vault"))}</span>`
            }
          </div>
        </article>`;
      })
      .join("");
    return `
      <div class="rag-md-stage">${cards}</div>
      ${
        recs.length > shown.length
          ? `<p class="rag-note">${esc(T("Showing {n} of {total} notes.", { n: shown.length, total: recs.length }))}</p>`
          : ""
      }
      ${
        docs.available === false
          ? panelError(docs.error)
          : notes.length
            ? `<details class="rag-panel-details">
                <summary>${esc(T("Note files in the vault ({n})", { n: notes.length }))}</summary>
                <div class="rag-note-files">${notes
                  .slice(0, 24)
                  .map(
                    (n) =>
                      `<div class="rag-note-file"><span>${esc(n.file)}</span><span class="rag-mono">${esc(
                        fmtBytes(n.bytes)
                      )} · ${esc(fmtTime(n.modified))}</span></div>`
                  )
                  .join("")}</div>
              </details>`
            : `<p class="rag-empty">${esc(T("No note file is in the vault yet."))}</p>`
      }`;
  }

  // --- graph: live 3D topology of the real edges --------------------------

  function graphPanelHTML(d) {
    const g = d.graph || {};
    if (g.available === false) return panelError(g.error);
    const nodes = list(g.nodes);
    const edges = list(g.edges);
    if (!nodes.length) return `<p class="rag-empty">${esc(T("No memory node is stored yet."))}</p>`;
    return `
      <div class="rag-canvas-wrap rag-graph-wrap">
        <canvas id="ragGraphCanvas" class="rag-canvas" role="img"
          aria-label="${esc(T("Relation topology of {n} nodes and {e} edges.", { n: nodes.length, e: edges.length }))}"></canvas>
      </div>
      <p class="rag-note">${esc(
        T("{n} nodes · {e} edges · {x} edges point outside the corpus", {
          n: nodes.length,
          e: edges.length,
          x: num(g.externalEdges, 0)
        })
      )}</p>
      <details class="rag-panel-details"${edges.length ? "" : " open"}>
        <summary>${esc(T("Relation edges ({n})", { n: edges.length }))}</summary>
        ${
          edges.length
            ? `<ul class="rag-edge-list">${edges
                .slice(0, 60)
                .map(
                  (e) =>
                    `<li><span class="rag-mono">${esc(e.src)}</span><b>${esc(e.type)}</b><span class="rag-mono">${esc(
                      e.dst
                    )}</span>${edgeWindow(e)}</li>`
                )
                .join("")}</ul>`
            : `<p class="rag-empty">${esc(T("No relation edge is recorded."))}</p>`
        }
      </details>
      <details class="rag-panel-details">
        <summary>${esc(T("Nodes ({n})", { n: nodes.length }))}</summary>
        <ul class="rag-node-list">${nodes
          .map(
            (n) =>
              `<li>${dot(verdictTone(n.verdict))}<span>${esc(n.topic || n.id)}</span><span class="rag-mono">${esc(
                n.verdict || "—"
              )}</span></li>`
          )
          .join("")}</ul>
      </details>`;
  }

  // --- vector: the real embeddings, projected -----------------------------

  function vectorPanelHTML(d) {
    const v = d.vectors || {};
    if (v.available === false) {
      if (v.error === "fewer than two vectors") {
        return `<p class="rag-empty">${esc(T("Fewer than two vectors is not a space to draw."))}</p>`;
      }
      return panelError(v.error);
    }
    const points = list(v.points);
    return `
      <div class="rag-vector-stats">
        ${statTile(T("Vector points"), points.length)}
        ${statTile(T("Vector dimensions"), num(v.dims, 0))}
        ${statTile(T("Stored vectors"), num(v.stored, 0))}
        ${statTile(T("Computed now"), num(v.computed, 0))}
      </div>
      <div class="rag-canvas-wrap rag-vector-wrap">
        <canvas id="ragVectorCanvas" class="rag-canvas" role="img"
          aria-label="${esc(T("Embedding projection of {n} vectors.", { n: points.length }))}"></canvas>
      </div>
      <div class="rag-axis-legend">${list(v.axes)
        .map(
          (a, i) =>
            `<span class="rag-axis"><b>${"XYZ"[i] || ""}</b> <span class="rag-mono">${esc(
              T("dimension {n}", { n: num(a.dim, 0) })
            )} · σ² ${esc(num(a.variance, 0).toFixed(4))}</span></span>`
        )
        .join("")}</div>
      <p class="rag-note">${esc(
        T("Embedded with {model}; the axes are the three dimensions with the widest measured spread.", {
          model: list(v.models).length ? list(v.models).join(", ") : v.model || "—"
        })
      )}</p>
      <details class="rag-panel-details">
        <summary>${esc(T("Projected points ({n})", { n: points.length }))}</summary>
        <ul class="rag-point-list">${points
          .map(
            (p) =>
              `<li>${dot(verdictTone(p.verdict))}<span>${esc(p.topic || p.memoryId)}</span><span class="rag-mono">${num(
                p.x,
                0
              ).toFixed(2)} / ${num(p.y, 0).toFixed(2)} / ${num(p.z, 0).toFixed(2)} · ${esc(p.source)}</span></li>`
          )
          .join("")}</ul>
      </details>`;
  }

  // --- document: the evidence documents this store indexes ----------------

  function documentPanelHTML(d) {
    const recs = list(d.records);
    if (!recs.length) return `<p class="rag-empty">${esc(T("No evidence document is indexed yet."))}</p>`;
    const shown = recs.slice(0, 16);
    return `
      <div class="rag-doc-list">${shown
        .map(
          (r) => `
        <article class="rag-doc-row">
          <span class="rag-doc-id rag-mono">${esc(r.memoryId || "—")}</span>
          <div class="rag-doc-main">
            <b>${esc(r.topic || "—")}</b>
            <p class="rag-note">${esc(excerpt(r.claim || r.evidenceText, 150))}</p>
          </div>
          <div class="rag-doc-meta">
            ${badge(r.verdict || "—", verdictTone(r.verdict))}
            <span class="rag-mono">${esc(r.sourceDomain || "—")}</span>
            <span class="rag-mono">${esc(validityText(r))}</span>
          </div>
        </article>`
        )
        .join("")}</div>
      ${
        recs.length > shown.length
          ? `<p class="rag-note">${esc(T("Showing {n} of {total} documents.", { n: shown.length, total: recs.length }))}</p>`
          : ""
      }
      <p class="rag-note">${esc(
        T("Documents are the evidence records this store indexes, with the source and validity they were written with.")
      )}</p>`;
  }

  // --- relational: the mirror's schema and validity windows ---------------

  function relationalPanelHTML(d) {
    const r = d.relational || {};
    if (r.available === false) return panelError(r.error);
    const tables = list(r.tables);
    if (!tables.length) return `<p class="rag-empty">${esc(T("No table is reported."))}</p>`;
    const verdicts = list(r.verdicts);
    const top = verdicts.reduce((m, v) => Math.max(m, num(v.count, 0)), 0);
    return `
      <div class="rag-rel-grid">${tables
        .map(
          (t) => `
        <article class="rag-rel-table">
          <div class="rag-rel-head"><b class="rag-mono">${esc(t.name)}</b>${badge(
            T("{n} stored rows", { n: num(t.rows, 0) }),
            "soft"
          )}</div>
          <div class="rag-chiplist">${list(t.columns)
            .map((c) => `<span class="rag-col rag-mono">${esc(c)}</span>`)
            .join("")}</div>
        </article>`
        )
        .join("")}</div>
      <div class="rag-rel-lower">
        <section class="rag-subcard">
          <b>${esc(T("Validity windows"))}</b>
          <div class="rag-validity">
            ${validityRow(T("Current facts"), r.current, "ok")}
            ${validityRow(T("Expired facts"), r.expired, "muted")}
            ${validityRow(T("Future facts"), r.future, "blue")}
          </div>
        </section>
        <section class="rag-subcard">
          <b>${esc(T("Verdicts"))}</b>
          <div class="rag-verdicts">${
            verdicts.length
              ? verdicts
                  .map(
                    (v) => `
              <div class="rag-verdict-row">
                <span>${esc(v.verdict || T("No verdict"))}</span>
                <div class="rag-bar"><i style="width:${top > 0 ? Math.round((num(v.count, 0) / top) * 100) : 0}%"></i></div>
                <b>${num(v.count, 0)}</b>
              </div>`
                  )
                  .join("")
              : `<span class="rag-note">${esc(T("No verdict is recorded."))}</span>`
          }</div>
        </section>
      </div>`;
  }

  function validityRow(label, value, tone) {
    return `<div class="rag-validity-row">${dot(tone)}<span>${esc(label)}</span><b>${num(value, 0)}</b></div>`;
  }

  // --- search: the full-text index and the three real scores --------------

  function searchPanelHTML(d) {
    const l = d.lexical || {};
    if (l.available === false) return panelError(l.error);
    return `
      <div class="rag-vector-stats">
        ${statTile(T("Memory records"), num(l.records, 0))}
        ${statTile(T("Stored vectors"), num(l.vectors, 0))}
        ${statTile(T("Relation edges"), num(l.edges, 0))}
        ${statTile(T("Learned habits"), num(l.habits, 0))}
      </div>
      <p class="rag-note">${esc(
        T("Lexical index: {state} · embedding {model} ({dims} dimensions)", {
          state: l.fts5 ? "FTS5" : "BM25",
          model: l.embeddingModel || "—",
          dims: num(l.dims, 0)
        })
      )}</p>
      <div class="rag-search-row">
        <input id="ragPanelSearchBox" class="rag-input" type="search" autocomplete="off"
          value="${esc(ragState.panelSearchQuery)}" placeholder="${esc(T("Ask the index…"))}" />
        <button type="button" class="rag-btn primary" id="ragPanelSearchBtn"${ragState.panelSearchBusy ? " disabled" : ""}>${esc(
          ragState.panelSearchBusy ? T("Searching the index…") : T("Search the index")
        )}</button>
      </div>
      <div id="ragPanelSearchResults" class="rag-search-results">${panelSearchResultsHTML()}</div>`;
  }

  function panelSearchResultsHTML() {
    if (ragState.panelSearchError) {
      return `<div class="rag-alert" role="alert">${dot("pink")}<span>${esc(ragState.panelSearchError)}</span></div>`;
    }
    if (ragState.panelSearchHits === null) return "";
    const hits = list(ragState.panelSearchHits);
    if (!hits.length) return `<p class="rag-empty">${esc(T("No hit in the index."))}</p>`;
    return `
      <div class="rag-result-meta">${esc(
        T("{n} hits · {ms} ms", { n: hits.length, ms: ragState.panelSearchTookMs })
      )}</div>
      <div class="rag-hits">${hits
        .map((h, i) => {
          const record = h.record || {};
          const cosine = num(h.cosine, 0);
          const bm25 = num(h.bm25, 0);
          const rrf = num(h.rrf, 0);
          return `
          <div class="rag-hit">
            <div class="rag-hit-head">
              <b>[${i + 1}] ${esc(record.topic || "—")}</b>
              ${badge(record.verdict || "—", verdictTone(record.verdict))}
            </div>
            <div class="rag-score-bars">
              ${scoreRow("cosine", cosine, 1)}
              ${scoreRow("bm25", bm25, Math.max(1, Math.abs(bm25)))}
              ${scoreRow("rrf", rrf, Math.max(0.001, Math.abs(rrf)))}
            </div>
            <p class="rag-note">${esc(excerpt(record.claim || record.evidenceText || "", 200))}</p>
          </div>`;
        })
        .join("")}</div>`;
  }

  function scoreRow(label, value, top) {
    const pct = top > 0 ? Math.max(0, Math.min(100, (value / top) * 100)) : 0;
    return `<div class="rag-score-row"><span class="rag-mono">${esc(label)}</span>
      <div class="rag-bar"><i style="width:${pct}%"></i></div>
      <span class="rag-mono">${value.toFixed(4)}</span></div>`;
  }

  // --- the live operation feed --------------------------------------------

  function opDetailKey(op) {
    if (op.detail === "records") return "Reading records";
    if (op.detail === "search") return "Hybrid search";
    if (op.detail === "memory") return "Memory write";
    if (op.detail === "swap") return "Backend swap";
    if (op.detail === "reachability") return "Reachability probe";
    if (op.detail === "habits") return "Reading habits";
    if (op.detail === "export") return "Corpus export";
    if (op.detail === "import") return "Corpus import";
    return "Backend call";
  }

  function agentForActor(actor) {
    const agents = list(ragState.agents);
    const fallback =
      agents.find((a) => a.id === "magpie") ||
      agents[0] || { id: "magpie", name: "magpie", iconUrl: "icons/magpie.svg" };
    const needle = String(actor || "").trim().toLowerCase();
    if (!needle) return fallback;
    const namesOf = (a) => [a.id, a.name, ...(a.aliases || [])].map((s) => String(s || "").toLowerCase()).filter(Boolean);
    for (const a of agents) {
      if (namesOf(a).includes(needle)) return a;
    }
    // An origin such as "MCP/claude-code 2.0": the longest registry name it
    // really contains wins. Nothing is invented when no name matches.
    let best = null;
    for (const a of agents) {
      for (const name of namesOf(a)) {
        if (name.length >= 3 && needle.includes(name) && (!best || name.length > best.len)) {
          best = { agent: a, len: name.length };
        }
      }
    }
    return best ? best.agent : fallback;
  }

  function operationRowHTML(op) {
    const agent = agentForActor(op.actor);
    const initial = String(agent.name || agent.id || "?").slice(0, 1).toUpperCase();
    const tone =
      op.ok === false ? "is-failed" : op.op === "write" ? "is-write" : op.op === "probe" ? "is-probe" : "is-read";
    return `
      <article class="rag-op ${tone}" data-op-id="${esc(String(op.id))}">
        <span class="rag-op-agent">
          <img class="rag-agent-img" src="${esc(agent.iconUrl || "icons/magpie.svg")}" alt=""
            data-initial="${esc(initial)}" loading="lazy" />
        </span>
        <div class="rag-op-body">
          <div class="rag-op-line"><b>${esc(T(opDetailKey(op)))}</b>
            <span class="rag-op-backend">${esc(dbName(op.backend))}</span></div>
          <div class="rag-op-meta rag-mono">
            <span>${esc(op.actor || "magpie")}</span>
            ${op.count ? `<span>${esc(T("{n} rows returned", { n: num(op.count, 0) }))}</span>` : ""}
            <span>${num(op.durationMs, 0)} ms</span>
            <span>${esc(fmtTime(op.at))}</span>
          </div>
        </div>
        <span class="rag-op-badge">${esc(String(op.op || "").toUpperCase())}</span>
      </article>`;
  }

  function feedHTML() {
    const ops = list(ragState.operations);
    if (!ops.length) return `<p class="rag-empty">${esc(T("No operation yet. A read or write appears here as it happens."))}</p>`;
    return ops.slice(0, 40).map(operationRowHTML).join("");
  }

  // mergeOperations keeps the list newest-first, deduped by the backend's
  // own id, and returns the newest operation it had not seen (null when the
  // answer brought nothing new). That is what the row animation marks.
  function mergeOperations(ops) {
    const seen = new Set(ragState.operations.map((op) => op.id));
    let newest = null;
    for (const op of list(ops)) {
      if (!op || op.id == null || seen.has(op.id)) continue;
      seen.add(op.id);
      ragState.operations.push(op);
      if (newest === null || num(op.id, 0) > newest) newest = num(op.id, 0);
      if (num(op.id, 0) > ragState.opsCursor) ragState.opsCursor = num(op.id, 0);
    }
    ragState.operations.sort((a, b) => num(b.id, 0) - num(a.id, 0));
    if (ragState.operations.length > 40) ragState.operations.length = 40;
    return newest;
  }

  function paintFeed(justArrivedId) {
    if (ragState.subtab !== "panels") return;
    const listNode = document.getElementById("ragFeedList");
    if (listNode) {
      listNode.innerHTML = feedHTML();
      bindAgentImages(listNode);
    }
    const stateNode = document.getElementById("ragFeedState");
    if (stateNode) {
      stateNode.innerHTML = `${ragState.opsLive ? dot("ok", "rag-pulse") : dot("muted")}<span>${esc(
        ragState.opsLive ? T("Live feed") : ragState.opsError ? T("Backend unavailable") : T("Waiting for the backend…")
      )}</span>`;
    }
    if (justArrivedId != null && listNode && !still()) {
      const row = listNode.querySelector(`[data-op-id="${justArrivedId}"]`);
      if (row && typeof row.animate === "function") {
        row.animate(
          [
            { opacity: 0, transform: "translate3d(0,-6px,0)" },
            { opacity: 1, transform: "translate3d(0,0,0)" }
          ],
          { duration: 260, easing: "cubic-bezier(.22, 1, .36, 1)" }
        );
        const ic = row.querySelector(".rag-op-agent");
        if (ic && typeof ic.animate === "function") {
          ic.animate(
            [
              { boxShadow: "0 0 0 0 var(--rag-glow)" },
              { boxShadow: "0 0 0 8px transparent" }
            ],
            { duration: 900, easing: "cubic-bezier(.2, .8, .2, 1)" }
          );
        }
      }
    }
    // One announcement per real operation, rather than the whole list on
    // every repaint.
    const announce = document.getElementById("ragFeedAnnounce");
    if (announce && justArrivedId != null) {
      const op = ragState.operations.find((x) => x.id === justArrivedId);
      if (op) announce.textContent = `${op.actor || "magpie"} · ${T(opDetailKey(op))} · ${dbName(op.backend)}`;
    }
  }

  // The app's own window cannot stream a response on Windows: Wails' writer
  // buffers the whole body until the handler returns. `magpie web` serves
  // over net/http, where Server-Sent Events reach the page frame by frame.
  // bootPrefs.web says which one this page is, so the feed polls in the
  // window and streams in the browser — both read the same real operations.
  function canStreamOperations() {
    return !!(window.bootPrefs && window.bootPrefs.web) && typeof EventSource === "function";
  }

  async function startOperationFeed() {
    if (feedStarted) return;
    feedStarted = true;
    const res = await pollOperations(false);
    if (!feedStarted) return; // disposed while the first read was in flight
    if (res && canStreamOperations()) {
      openOperationStream();
      return;
    }
    // Polling is the feed in the app's own window, and the fallback when the
    // stream cannot start. The interval also retries a failed first poll.
    feedTimer = setInterval(() => pollOperations(true), 4000);
  }

  function openOperationStream() {
    const src = new EventSource("/api/rag/events");
    feedSource = src;
    src.addEventListener("hello", (ev) => {
      const data = safeJSON(ev.data);
      if (!data) return;
      ragState.agents = list(data.agents);
      mergeOperations(list(data.operations));
      ragState.opsLive = true;
      ragState.opsError = "";
      paintFeed();
    });
    src.addEventListener("op", (ev) => {
      const op = safeJSON(ev.data);
      if (!op) return;
      const added = mergeOperations([op]);
      ragState.opsLive = true;
      ragState.opsError = "";
      paintFeed(added);
    });
    src.onopen = () => {
      ragState.opsLive = true;
      ragState.opsError = "";
      paintFeed();
    };
    src.onerror = () => {
      // A stream that cannot stay open is replaced by the polling feed
      // rather than leaving the page waiting on a connection that the
      // platform will never flush.
      if (feedSource === src) {
        src.close();
        feedSource = null;
      }
      if (!feedTimer) feedTimer = setInterval(() => pollOperations(true), 4000);
      ragState.opsLive = false;
      ragState.opsError = T("The live stream is reconnecting…");
      paintFeed();
    };
  }

  function stopOperationFeed() {
    feedStarted = false;
    if (feedSource) {
      feedSource.close();
      feedSource = null;
    }
    if (feedTimer) {
      clearInterval(feedTimer);
      feedTimer = 0;
    }
  }

  // pollOperations reads the same operations the stream carries and returns
  // the answer (null on failure), so the caller can tell whether the
  // backend is there before choosing a feed.
  async function pollOperations(animate) {
    const res = await ragApi("rag/operations?since=" + encodeURIComponent(ragState.opsCursor || 0));
    if (!res || res.error) {
      ragState.opsLive = false;
      ragState.opsError = T("The operation feed is unavailable: {error}", { error: errText(res) });
      paintFeed();
      return null;
    }
    ragState.agents = list(res.agents);
    const added = mergeOperations(list(res.operations));
    ragState.opsCursor = Math.max(ragState.opsCursor, num(res.latest, 0));
    ragState.opsLive = true;
    ragState.opsError = "";
    paintFeed(animate ? added : null);
    return res;
  }

  // --- panel data and panel switching -------------------------------------

  async function loadPanelData() {
    ragState.panelsBusy = true;
    ragState.panelsError = "";
    const btn = document.getElementById("ragPanelsRefresh");
    if (btn) {
      btn.disabled = true;
      btn.textContent = T("Refreshing the panels…");
    }
    const res = await ragApi("rag/panels");
    ragState.panelsBusy = false;
    if (!res || res.error) {
      ragState.panelsError = errText(res);
    } else {
      ragState.panels = res;
      if (Array.isArray(res.records)) ragState.records = res.records;
      if (Array.isArray(res.backends) && res.backends.length) ragState.backends = res.backends;
      if (res.activeDbId) ragState.activeDbId = res.activeDbId;
    }
    renderTabs();
    const hero = document.getElementById("ragHero");
    if (hero) hero.innerHTML = heroHTML();
    if (ragState.subtab !== "panels") return;
    if (btn) {
      btn.disabled = false;
      btn.textContent = T("Refresh panels");
    }
    const head = document.getElementById("ragPanelHead");
    if (head) head.innerHTML = panelHeadHTML();
    const down = document.getElementById("ragPanelDownWrap");
    if (down) down.innerHTML = downBannerHTML();
    const body = document.getElementById("ragPanelBody");
    if (body) {
      stopPanelMotion();
      body.innerHTML = panelBodyHTML();
      bindPanelBody(body);
    }
    // A failure belongs in the body, which was just repainted.
  }

  function selectPanel(id) {
    if (!DB_PANELS.some((p) => p.id === id) || id === ragState.panelId) return;
    stopPanelMotion();
    ragState.panelId = id;
    document.querySelectorAll("#ragPanelTabs [data-rag-panel]").forEach((b) => {
      const on = b.dataset.ragPanel === id;
      b.classList.toggle("on", on);
      b.setAttribute("aria-selected", String(on));
      b.tabIndex = on ? 0 : -1;
    });
    const head = document.getElementById("ragPanelHead");
    if (head) head.innerHTML = panelHeadHTML();
    const down = document.getElementById("ragPanelDownWrap");
    if (down) down.innerHTML = downBannerHTML();
    const body = document.getElementById("ragPanelBody");
    if (!body) return;
    body.setAttribute("aria-labelledby", "ragPanelTab-" + id);
    body.innerHTML = panelBodyHTML();
    bindPanelBody(body);
    if (!still() && typeof body.animate === "function") {
      body.animate(
        [
          { opacity: 0, transform: "translate3d(0,5px,0)" },
          { opacity: 1, transform: "translate3d(0,0,0)" }
        ],
        { duration: 190, easing: "cubic-bezier(.22, 1, .36, 1)" }
      );
    }
  }

  // --- canvas drawing (no dependency) -------------------------------------

  function stopPanelMotion() {
    for (const stop of panelMotion) {
      try {
        stop();
      } catch (e) {
        /* a canvas that was already removed has nothing to stop */
      }
    }
    panelMotion = [];
  }

  function canvasColors(canvas) {
    const style = getComputedStyle(canvas);
    return {
      blue: style.getPropertyValue("--rag-blue").trim() || "#246eb6",
      pink: style.getPropertyValue("--rag-pink").trim() || "#d967a7",
      green: style.getPropertyValue("--rag-green").trim() || "#2f8f6b",
      muted: style.getPropertyValue("--rag-muted").trim() || "#637c97",
      line: style.getPropertyValue("--rag-line").trim() || "#dce7f2",
      ink: style.getPropertyValue("--rag-ink").trim() || "#163f70"
    };
  }

  function sizeCanvas(canvas) {
    const rect = canvas.getBoundingClientRect();
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    canvas.width = Math.max(1, Math.round(rect.width * dpr));
    canvas.height = Math.max(1, Math.round(rect.height * dpr));
    const ctx = canvas.getContext("2d");
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    return { ctx, w: rect.width, h: rect.height };
  }

  function startGraphCanvas() {
    const canvas = document.getElementById("ragGraphCanvas");
    const g = ragState.panels && ragState.panels.graph;
    if (!canvas || !g || !canvas.getContext) return;
    const nodes = list(g.nodes);
    const edges = list(g.edges);
    if (!nodes.length) return;
    const index = new Map(nodes.map((n, i) => [n.id, i]));
    const points = nodes.map((_, i) => {
      const y = nodes.length > 1 ? 1 - (i / (nodes.length - 1)) * 2 : 0;
      const r = Math.sqrt(Math.max(0, 1 - y * y));
      const phi = i * 2.399963229728653;
      return { x: Math.cos(phi) * r, y, z: Math.sin(phi) * r };
    });
    let angle = 0.55;
    let raf = 0;
    let stopped = false;
    let view = { ctx: null, w: 0, h: 0 };

    const paint = () => {
      const { ctx, w, h } = view;
      if (!ctx || !w || !h) return;
      const c = canvasColors(canvas);
      ctx.clearRect(0, 0, w, h);
      const fov = 2.8;
      const project = (p) => {
        const x = p.x * Math.cos(angle) + p.z * Math.sin(angle);
        const z = -p.x * Math.sin(angle) + p.z * Math.cos(angle);
        const scale = fov / (fov + z);
        return { x: w / 2 + x * scale * w * 0.34, y: h / 2 + p.y * scale * h * 0.34, z, scale };
      };
      const projected = points.map(project);
      // Edges first, so the nodes sit over them. An edge whose other end is
      // not a stored record is in the count the page shows, not drawn as a
      // node that does not exist.
      edges.forEach((e) => {
        const a = index.get(e.src);
        const b = index.get(e.dst);
        if (a == null || b == null) return;
        const p = projected[a];
        const q = projected[b];
        ctx.strokeStyle = c.line;
        ctx.globalAlpha = 0.35 + Math.min(0.45, (p.scale + q.scale) * 0.12);
        ctx.beginPath();
        ctx.moveTo(p.x, p.y);
        ctx.lineTo(q.x, q.y);
        ctx.stroke();
      });
      ctx.globalAlpha = 1;
      const named = w > 560 && nodes.length <= 40;
      nodes.forEach((n, i) => {
        const p = projected[i];
        const tone =
          n.verdict === "VERIFIED" ? c.green : n.verdict === "REFUTED" ? c.pink : c.blue;
        ctx.fillStyle = n.invalidAt ? c.muted : tone;
        ctx.beginPath();
        ctx.arc(p.x, p.y, 2.6 + p.scale * 3.2, 0, Math.PI * 2);
        ctx.fill();
        if (named) {
          ctx.globalAlpha = Math.max(0.35, p.scale - 0.45);
          ctx.fillStyle = c.ink;
          ctx.font = "11px system-ui, sans-serif";
          const label = (n.topic || n.id || "").slice(0, 22);
          ctx.fillText(label, p.x + 6, p.y - 4);
          ctx.globalAlpha = 1;
        }
      });
    };

    const resize = () => {
      view = sizeCanvas(canvas);
      paint();
    };
    const observer = typeof ResizeObserver === "function" ? new ResizeObserver(resize) : null;
    if (observer) observer.observe(canvas);
    resize();
    if (!still()) {
      const loop = () => {
        if (stopped) return;
        angle += 0.0035;
        paint();
        raf = requestAnimationFrame(loop);
      };
      raf = requestAnimationFrame(loop);
    }
    panelMotion.push(() => {
      stopped = true;
      cancelAnimationFrame(raf);
      if (observer) observer.disconnect();
    });
  }

  function startVectorCanvas() {
    const canvas = document.getElementById("ragVectorCanvas");
    const v = ragState.panels && ragState.panels.vectors;
    if (!canvas || !v || !canvas.getContext) return;
    const points = list(v.points);
    const paint = () => {
      const { ctx, w, h } = sizeCanvas(canvas);
      if (!w || !h) return;
      const c = canvasColors(canvas);
      ctx.clearRect(0, 0, w, h);
      // The projection's own axes: X right, Y up, Z into the screen as a
      // smaller, fainter point.
      const px = (p) => w / 2 + num(p.x, 0) * w * 0.36;
      const py = (p) => h / 2 - num(p.y, 0) * h * 0.36;
      ctx.strokeStyle = c.line;
      ctx.globalAlpha = 0.7;
      ctx.beginPath();
      ctx.moveTo(w * 0.08, h / 2);
      ctx.lineTo(w * 0.92, h / 2);
      ctx.moveTo(w / 2, h * 0.08);
      ctx.lineTo(w / 2, h * 0.92);
      ctx.stroke();
      ctx.globalAlpha = 1;
      const named = w > 560 && points.length <= 30;
      points.forEach((p) => {
        const depth = (num(p.z, 0) + 1) / 2;
        const tone = p.verdict === "VERIFIED" ? c.green : p.verdict === "REFUTED" ? c.pink : c.blue;
        ctx.fillStyle = tone;
        ctx.globalAlpha = 0.45 + depth * 0.55;
        ctx.beginPath();
        ctx.arc(px(p), py(p), 3 + depth * 3.5, 0, Math.PI * 2);
        ctx.fill();
        if (named) {
          ctx.globalAlpha = 0.85;
          ctx.fillStyle = c.ink;
          ctx.font = "11px system-ui, sans-serif";
          ctx.fillText((p.topic || p.memoryId || "").slice(0, 18), px(p) + 6, py(p) - 4);
        }
      });
      ctx.globalAlpha = 1;
    };
    const observer = typeof ResizeObserver === "function" ? new ResizeObserver(() => paint()) : null;
    if (observer) observer.observe(canvas);
    paint();
    panelMotion.push(() => {
      if (observer) observer.disconnect();
    });
  }

  // --- panel bindings -----------------------------------------------------

  // A bundled agent icon that is missing (a PNG-only agent reached as SVG)
  // becomes its initial, not a broken image. Bound after every paint so a
  // cached failure is handled too.
  function bindAgentImages(root) {
    root.querySelectorAll("img.rag-agent-img").forEach((img) => {
      img.onerror = () => {
        const span = document.createElement("span");
        span.className = "rag-agent-fallback";
        span.textContent = img.dataset.initial || "?";
        img.replaceWith(span);
      };
    });
  }

  function bindPanelBody(root) {
    if (ragState.panelId === "search") {
      const box = document.getElementById("ragPanelSearchBox");
      const btn = document.getElementById("ragPanelSearchBtn");
      if (btn) btn.onclick = () => runPanelSearch();
      if (box) {
        box.oninput = () => (ragState.panelSearchQuery = box.value);
        box.onkeydown = (e) => {
          if (e.key === "Enter") runPanelSearch();
        };
      }
    }
    if (ragState.panelId === "graph") startGraphCanvas();
    if (ragState.panelId === "vector") startVectorCanvas();
  }

  async function runPanelSearch() {
    const box = document.getElementById("ragPanelSearchBox");
    const query = (box ? box.value : ragState.panelSearchQuery).trim();
    if (!query) {
      ragState.panelSearchError = T("Search query is required.");
      const results = document.getElementById("ragPanelSearchResults");
      if (results) results.innerHTML = panelSearchResultsHTML();
      return;
    }
    ragState.panelSearchBusy = true;
    ragState.panelSearchQuery = query;
    ragState.panelSearchError = "";
    const btn = document.getElementById("ragPanelSearchBtn");
    if (btn) {
      btn.disabled = true;
      btn.textContent = T("Searching the index…");
    }
    const results = document.getElementById("ragPanelSearchResults");
    if (results) results.innerHTML = searchSkeletonHTML();
    const res = await ragApi("rag/search", { method: "POST", body: JSON.stringify({ query, topK: 8 }) });
    ragState.panelSearchBusy = false;
    if (!res || res.error) {
      ragState.panelSearchHits = null;
      ragState.panelSearchError = T("Search failed: {error}", { error: errText(res) });
    } else {
      ragState.panelSearchHits = list(res.hits);
      ragState.panelSearchTookMs = num(res.tookMs, 0);
      ragState.panelSearchError = "";
    }
    if (btn) {
      btn.disabled = false;
      btn.textContent = T("Search the index");
    }
    if (results) {
      results.innerHTML = panelSearchResultsHTML();
      if (!still()) {
        const first = results.querySelector(".rag-hit");
        if (first && typeof first.animate === "function") {
          first.animate(
            [
              { opacity: 0, transform: "translateY(-4px)" },
              { opacity: 1, transform: "translateY(0)" }
            ],
            { duration: 240, easing: "cubic-bezier(.22, 1, .36, 1)" }
          );
        }
      }
    }
  }

  function bindPanels(content) {
    const refresh = document.getElementById("ragPanelsRefresh");
    if (refresh) refresh.onclick = () => loadPanelData();
    content.querySelectorAll("#ragPanelTabs [data-rag-panel]").forEach((b) => {
      b.onclick = () => selectPanel(b.dataset.ragPanel);
      b.onkeydown = (e) => {
        if (e.key !== "ArrowRight" && e.key !== "ArrowLeft" && e.key !== "Home" && e.key !== "End") return;
        e.preventDefault();
        const order = DB_PANELS.map((p) => p.id);
        const at = order.indexOf(ragState.panelId);
        let next;
        if (e.key === "Home") next = order[0];
        else if (e.key === "End") next = order[order.length - 1];
        else next = order[(at + (e.key === "ArrowRight" ? 1 : order.length - 1)) % order.length];
        selectPanel(next);
        const want = document.getElementById("ragPanelTab-" + next);
        if (want) want.focus();
      };
    });
    const feed = document.getElementById("ragFeedList");
    if (feed) bindAgentImages(feed);
    bindPanelBody(content);
    startOperationFeed();
    if (!ragState.panels && !ragState.panelsBusy) loadPanelData();
  }

  // ---------- render -----------------------------------------------------

  function renderTabs() {
    const nav = document.getElementById("ragTabs");
    if (nav) nav.innerHTML = tabsHTML();
  }

  function renderRagAll() {
    const hero = document.getElementById("ragHero");
    if (hero) hero.innerHTML = heroHTML();
    renderTabs();
    const content = document.getElementById("ragContent");
    if (!content) return;
    if (ragState.subtab === "swap") content.innerHTML = swapHTML();
    else if (ragState.subtab === "panels") content.innerHTML = panelsHTML();
    else if (ragState.subtab === "habits") content.innerHTML = habitsHTML();
    else if (ragState.subtab === "vault") content.innerHTML = vaultHTML();
    bindContent(content);
  }

  // ---------- bindings ---------------------------------------------------

  function bindContent(content) {
    document.querySelectorAll("#ragTabs button[data-rag-tab]").forEach((b) => {
      b.onclick = () => swapView(b.dataset.ragTab);
      b.onkeydown = (e) => {
        if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
        e.preventDefault();
        const order = RAG_TABS.map((x) => x.id);
        const at = order.indexOf(ragState.subtab);
        const next = order[(at + (e.key === "ArrowRight" ? 1 : order.length - 1)) % order.length];
        swapView(next);
        const want = document.getElementById("ragTab-" + next);
        if (want) want.focus();
      };
    });
    if (ragState.subtab === "swap") bindSwap(content);
    else if (ragState.subtab === "panels") bindPanels(content);
    else if (ragState.subtab === "habits") bindHabits(content);
    else if (ragState.subtab === "vault") bindVault(content);
  }

  function paintMenus() {
    if (!kit) return;
    const embedSlot = document.getElementById("ragEmbedSlot");
    if (embedSlot) {
      const opts = [ragState.embeddingModel || "local-ngram-1024"]
        .filter((v, i, a) => v && a.indexOf(v) === i)
        .map((v) => ({ v, name: v }));
      if (!opts.some((o) => o.v === "local-ngram-1024")) opts.push({ v: "local-ngram-1024", name: "local-ngram-1024" });
      const b = kit.menu(opts, ragState.embeddingModel || "local-ngram-1024", T("Embedding model"), (v) => {
        ragState.embeddingModel = v;
      });
      b.classList.add("rag-menu");
      embedSlot.replaceChildren(b);
    }
    const memSlot = document.getElementById("ragMemSlot");
    if (memSlot) {
      const opts = MEMORY_MODES.map((m) => ({ v: m.value, name: T(m.key) }));
      if (!opts.some((o) => o.v === ragState.memoryMode)) {
        opts.push({ v: ragState.memoryMode, name: ragState.memoryMode });
      }
      const b = kit.menu(opts, ragState.memoryMode, T("Memory mode"), (v) => {
        ragState.memoryMode = v;
      });
      b.classList.add("rag-menu");
      memSlot.replaceChildren(b);
    }
    const verdictSlot = document.getElementById("ragVerdictSlot");
    if (verdictSlot) {
      const opts = VERDICTS.map((v) => ({ v, name: v }));
      const b = kit.menu(opts, ragState.verdict, T("Record verdict"), (v) => {
        ragState.verdict = v;
      });
      b.classList.add("rag-menu");
      verdictSlot.replaceChildren(b);
    }
  }

  function bindSwap(content) {
    paintMenus();

    const coldBtn = document.getElementById("ragColdExportBtn");
    if (coldBtn) coldBtn.onclick = () => triggerColdExport();

    const importBtn = document.getElementById("ragColdImportBtn");
    const importInput = document.getElementById("ragImportInput");
    if (importBtn && importInput) {
      importBtn.onclick = () => importInput.click();
      importInput.onchange = () => {
        const f = importInput.files && importInput.files[0];
        if (f) triggerColdImport(f);
      };
    }

    const syncBtn = document.getElementById("ragSyncBtn");
    if (syncBtn) syncBtn.onclick = () => triggerSyncAgentFiles();

    const searchBtn = document.getElementById("ragSearchBtn");
    if (searchBtn) searchBtn.onclick = () => runRagSearch();
    const searchBox = document.getElementById("ragSearchBox");
    if (searchBox) {
      searchBox.oninput = () => (ragState.searchQuery = searchBox.value);
      searchBox.onkeydown = (e) => {
        if (e.key === "Enter") runRagSearch();
      };
    }

    const addSubmit = document.getElementById("ragAddSubmit");
    if (addSubmit) addSubmit.onclick = () => submitRagRecord();

    content.querySelectorAll("button[data-hotswap-db]").forEach((btn) => {
      btn.onclick = () => triggerHotSwap(btn.dataset.hotswapDb);
    });
  }

  function bindHabits(content) {
    const b = document.getElementById("ragSyncHabitBtn");
    if (b) b.onclick = () => triggerSyncAgentFiles();
  }

  function bindVault(content) {
    const input = document.getElementById("ragVaultSearch");
    const list = document.getElementById("ragVaultList");
    if (!input || !list) return;
    input.oninput = () => {
      ragState.vaultFilter = input.value;
      list.innerHTML = vaultListHTML();
      // The field keeps focus: only the list below it was replaced.
    };
  }

  magpie.plugin("ragstore", (ctx) => {
    kit = ctx.ui;
    ctx.onDispose(stopOperationFeed);
    ctx.slots.add("page", {
      key: "ragstore",
      title: "Memory & RAG",
      html: `
        <div class="rag-wrap">
          <header class="rag-hero" id="ragHero"></header>
          <nav class="rag-tabs" id="ragTabs" role="tablist"></nav>
          <div id="ragContent" class="rag-content"></div>
        </div>`,
      open() {
        startOperationFeed();
        if (!ragState.backends.length) {
          return loadRagStoreData();
        }
        renderRagAll();
      },
      refresh() {
        startOperationFeed();
        return loadRagStoreData();
      },
      reload() {
        startOperationFeed();
        return loadRagStoreData();
      }
    });
  });
})();
