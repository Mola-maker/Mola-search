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
    { id: "habits", key: "Memory layers and habits" },
    { id: "vault", key: "Evidence vault" }
  ];

  const MEMORY_MODES = [
    { value: "persistent", key: "Persistent: five memory layers survive restarts" },
    { value: "ephemeral", key: "Ephemeral: nothing is written to memory" }
  ];

  const VERDICTS = ["UNVERIFIED", "VERIFIED", "REFUTED", "ABSTAIN"];

  let kit = null; // magpie.ui, for the app menu (never a native <select>)
  let viewSwap = null; // the running dual-segment tab animation, cancellable

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
        if (!ragState.backends.length) {
          return loadRagStoreData();
        }
        renderRagAll();
      },
      refresh() {
        return loadRagStoreData();
      },
      reload() {
        return loadRagStoreData();
      }
    });
  });
})();
