// MolaSearch Detective — the investigation desk.
//
// The page is one render pipeline over one state object:
//   1. data      — the five APIs the engine serves (state, modes, investigate,
//                  settings/channels, stakeout), plus image upload and keys
//   2. motion    — calm() before every programmatic animation, the eight
//                  patterns from 视觉样式与动画设计参考.md, and no scroll the
//                  reader did not ask for
//   3. views     — lab, trajectory, modes, proof, stakeout; each renders from
//                  the same state and binds its own controls
//
// Nothing here invents a result: a template fills the query box, and every
// number shown came back from /api/detective/*.

(function () {
  const AGENT_PLATFORMS = ["claude", "dsh", "codex", "gemini", "opencode", "cursor", "antigravity"];

  // Query templates fill the box and pick a mode. They carry no result.
  const TEMPLATES = [
    {
      id: "t_rumor",
      label: "Check a rumor",
      query: "家族群都在转『喝隔夜茶会致癌、亚硝酸盐超标几十倍』是真的吗？有没有权威实验数据？",
      mode: "M13"
    },
    {
      id: "t_latest",
      label: "Follow a developing story",
      query: "请查一下最近一周内发生的重大公共事件的最新官方通报与进展",
      mode: "M17"
    },
    {
      id: "t_drama",
      label: "Find a show I half remember",
      query: "找一部小时候看过的国产悬疑古装剧：男主右手有六指印记，破案用铜钱卜卦，有一集在枯井里发现前朝密诏，到底是哪部剧第几集？",
      mode: "M06"
    },
    {
      id: "t_weather",
      label: "Verify a weather video",
      query: "网传视频称『今天下午北京朝阳公园突降鸡蛋大冰雹砸毁数十辆车』，请结合实时天气与地图信息核实真伪",
      mode: "M25"
    }
  ];

  const TABS = [
    { id: "lab", key: "Investigation desk" },
    { id: "trajectory", key: "Live trajectory and network" },
    { id: "modes", key: "Thirty search modes" },
    { id: "proof", key: "Channels and gates" },
    { id: "stakeout", key: "Stakeout watch list" }
  ];

  const VERDICTS = {
    VERIFIED: { key: "Verified: authoritative sources agree", tone: "blue" },
    REFUTED: { key: "Refuted: the authoritative evidence says otherwise", tone: "ink" },
    CONTESTED: { key: "Contested: sources disagree, so both sides are shown", tone: "ink" },
    UNVERIFIED: { key: "Unverified: the evidence does not settle it", tone: "muted" },
    ABSTAIN: { key: "Abstain: no evidence, so no answer", tone: "pink" }
  };

  let kit = null; // magpie.ui, for the app menu and its switches
  let uploadedImage = null;
  let netGraphStop = null;
  let viewSwap = null; // the running dual-segment tab animation, cancellable
  let detState = {
    subtab: "lab",
    query: "",
    selectedMode: "M13",
    modeTouched: false,
    audience: "human",
    sdkLang: "ts",
    debateOn: true,
    cuOn: true,
    familyFilter: "ALL",
    busy: false,
    modes: [],
    stakeouts: [],
    currentResult: null,
    freshResult: false,
    newUrls: new Set(),
    netView: "flow",
    channels: [],
    settings: null,
    settingsDefaults: null,
    settingsFile: "",
    engine: { envFile: "" },
    injected: {},
    injectNote: "",
    stakeDiff: "",
    foldHidden: false,
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
    return `<span class="det-dot det-dot-${tone}${extra ? " " + extra : ""}" aria-hidden="true"></span>`;
  }

  function shortPath(p, keep) {
    const s = String(p || "");
    const n = keep || 46;
    return s.length <= n ? s : "…" + s.slice(s.length - n);
  }

  function channelHitsText(r) {
    const hits = (r && r.channelHits) || {};
    const nameOf = (id) => {
      const c = (detState.channels || []).find((x) => x.id === id);
      return c ? c.name : id;
    };
    const parts = Object.entries(hits)
      .sort((a, b) => b[1] - a[1])
      .slice(0, 4)
      .map(([id, n]) => `${nameOf(id)} ${n}`);
    return parts.length ? parts.join(" · ") : "0";
  }

  function pctLabel(kind) {
    const map = {
      pctRedact: "Privacy pass",
      pctPlan: "Query planning",
      pctLocal: "Local index",
      pctNet: "Network search",
      pctRerank: "Reranking"
    };
    return T(map[kind] || kind);
  }

  function stageColor(kind) {
    return {
      pctRedact: "var(--det-sky)",
      pctPlan: "var(--det-violet)",
      pctLocal: "var(--det-green)",
      pctNet: "var(--det-amber)",
      pctRerank: "var(--det-pink)"
    }[kind];
  }

  // ---------- the engine's APIs ------------------------------------------

  // detApi answers {error} instead of throwing, so every caller can show what
  // went wrong rather than failing silently.
  async function detApi(path, opts) {
    try {
      const res = await fetch("/api/" + path, {
        headers: { "Content-Type": "application/json" },
        ...opts
      });
      const body = await res.json().catch(() => null);
      if (!res.ok) return { error: (body && body.error) || "HTTP " + res.status };
      return body || {};
    } catch (e) {
      return { error: e && e.message ? e.message : String(e) };
    }
  }

  async function loadEngineState() {
    const res = await detApi("detective/state");
    if (res && !res.error) {
      detState.engine = { envFile: res.envFile || "" };
      detState.channels = Array.isArray(res.channels) ? res.channels : [];
      detState.settings = res.settings || null;
      detState.settingsDefaults = res.defaults || detState.settingsDefaults;
      detState.settingsFile = res.file || detState.settingsFile;
      if (res.settingsError) detState.error = T("Could not load the engine state: {error}", { error: res.settingsError });
    }
    return res;
  }

  async function loadInitialDetectiveData() {
    const [modesRes, stakeRes, stateRes] = await Promise.all([
      detApi("detective/modes"),
      detApi("detective/stakeout"),
      loadEngineState()
    ]);
    const errs = [];
    if (modesRes && Array.isArray(modesRes.modes)) detState.modes = modesRes.modes;
    else if (modesRes && modesRes.error) errs.push(T("Could not load the mode catalog: {error}", { error: modesRes.error }));
    if (stakeRes && Array.isArray(stakeRes.jobs)) detState.stakeouts = stakeRes.jobs;
    else if (stakeRes && stakeRes.error) errs.push(T("Could not load the watches: {error}", { error: stakeRes.error }));
    if (stateRes && stateRes.error) errs.push(T("Could not load the engine state: {error}", { error: stateRes.error }));
    detState.error = errs.join(" · ");
    renderDetectiveAll();
  }

  async function runInvestigation(customQuery, modeId) {
    if (customQuery !== undefined) detState.query = customQuery;
    if (modeId !== undefined) detState.selectedMode = modeId;
    if (!String(detState.query || "").trim()) {
      detState.error = T("Enter a question first, or pick a template.");
      renderDetectiveAll();
      return;
    }
    detState.busy = true;
    detState.error = "";
    renderDetectiveAll();

    const body = {
      query: detState.query,
      modeId: detState.selectedMode,
      debate: detState.debateOn,
      enableCU: detState.cuOn
    };
    if (uploadedImage) {
      body.imageBase64 = uploadedImage.base64;
      body.imageName = uploadedImage.name;
    }

    const invRes = await detApi("detective/investigate", {
      method: "POST",
      body: JSON.stringify(body)
    });
    detState.busy = false;
    if (invRes && !invRes.error) {
      detState.currentResult = invRes;
      detState.freshResult = true;
      detState.newUrls = new Set((invRes.citations || []).map((c) => c.url).filter(Boolean));
      detState.foldHidden = false;
    } else {
      detState.error = T("Investigation failed: {error}", { error: (invRes && invRes.error) || T("No response from the engine") });
    }
    renderDetectiveAll();
  }

  async function toggleChannel(id, enabled) {
    const res = await detApi("detective/channels", {
      method: "POST",
      body: JSON.stringify({ id, enabled })
    });
    if (res && res.error) detState.error = T("Could not switch the provider: {error}", { error: res.error });
    else detState.error = "";
    await loadEngineState();
    renderDetectiveAll();
  }

  async function saveSettings(values) {
    const res = await detApi("detective/settings", {
      method: "POST",
      body: JSON.stringify(values)
    });
    if (res && res.error) detState.error = T("Could not save the gates: {error}", { error: res.error });
    else {
      detState.error = "";
      if (res && res.settings) detState.settings = res.settings;
      await loadEngineState();
    }
    renderDetectiveAll();
  }

  async function uploadDetectiveImage(file) {
    detState.error = "";
    const dataUrl = await new Promise((resolve, reject) => {
      const fr = new FileReader();
      fr.onload = () => resolve(String(fr.result || ""));
      fr.onerror = () => reject(fr.error || new Error("read failed"));
      fr.readAsDataURL(file);
    }).catch((e) => {
      detState.error = T("Could not read the image: {error}", { error: e && e.message ? e.message : e });
      return "";
    });
    if (!dataUrl) {
      renderDetectiveAll();
      return;
    }
    const fd = new FormData();
    fd.append("image", file, file.name);
    let res;
    try {
      const raw = await fetch("/api/detective/upload-image", { method: "POST", body: fd });
      const body = await raw.json().catch(() => null);
      res = raw.ok ? body || {} : { error: (body && body.error) || "HTTP " + raw.status };
    } catch (e) {
      res = { error: e && e.message ? e.message : String(e) };
    }
    if (res && res.error) {
      detState.error = T("Could not upload the image: {error}", { error: res.error });
      renderDetectiveAll();
      return;
    }
    const comma = dataUrl.indexOf(",");
    uploadedImage = {
      name: file.name,
      dataUrl,
      base64: comma >= 0 ? dataUrl.slice(comma + 1) : dataUrl,
      card: (res && res.card) || null
    };
    if (!detState.modeTouched) detState.selectedMode = "M26";
    renderDetectiveAll();
  }

  // ---------- motion ----------------------------------------------------

  // swapView is the tab change: out in 75 ms on the in curve, swap, in in
  // 190 ms on the out curve, with the direction following the tab order.
  // A second click cancels the first, so the content cannot land twice.
  function swapView(next, ev) {
    if (next === detState.subtab) return;
    const order = TABS.map((x) => x.id);
    const dir = order.indexOf(next) > order.indexOf(detState.subtab) ? 1 : -1;
    const content = document.getElementById("detContent");
    if (still() || !content || typeof content.animate !== "function") {
      detState.subtab = next;
      renderDetectiveAll();
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
        detState.subtab = next;
        renderDetectiveAll();
        const fresh = document.getElementById("detContent");
        if (fresh) {
          fresh.animate(
            [{ opacity: 0, transform: `translate3d(${dir * 7}px,0,0)` }, { opacity: 1, transform: "translate3d(0,0,0)" }],
            { duration: 190, easing: "cubic-bezier(.22, 1, .36, 1)" }
          );
        }
      })
      .catch(() => {});
  }

  // announceResult plays the verdict's one-shot feedback: a held flash on the
  // dot (the state is a dot, never a colour bar), and the small celebration
  // only when the evidence actually verified something. Reduced motion gets
  // the final state and no class.
  function announceResult() {
    const dotEl = document.querySelector(".det-verdict-dot");
    if (!dotEl) return;
    dotEl.classList.add("det-flash");
    dotEl.addEventListener("animationend", () => dotEl.classList.remove("det-flash"), { once: true });
    const verdict = (detState.currentResult || {}).verdict;
    if (verdict === "VERIFIED" && !still()) {
      dotEl.classList.add("det-cheer");
      dotEl.addEventListener("animationend", () => dotEl.classList.remove("det-cheer"), { once: true });
    }
  }
  // replayTrajectory re-plays the waterfall in order. It never scrolls: the
  // reader's viewport is theirs. Each step fades in with a 60 ms ladder,
  // capped so a long run still finishes within a breath.
  function replayTrajectory() {
    const steps = document.querySelectorAll("#detContent .det-step");
    if (!steps.length) return;
    if (still()) return;
    steps.forEach((el, i) => {
      if (typeof el.animate !== "function") return;
      el.classList.remove("det-enter");
      void el.offsetWidth;
      el.style.setProperty("--det-i", String(Math.min(i, 8)));
      el.animate(
        [
          { opacity: 0, transform: "translateY(5px)" },
          { opacity: 1, transform: "translateY(0)" }
        ],
        { duration: 240, delay: Math.min(i, 8) * 60, easing: "cubic-bezier(.22, 1, .36, 1)", fill: "both" }
      );
    });
  }

  // toggleFold closes the near-duplicate cluster with the pop-out (0.6 of
  // its entry distance), and the count stays as the record of what folded.
  function toggleFold(ev) {
    const why = document.getElementById("detFoldWhy");
    const card = document.getElementById("detFold");
    if (!why || !card) return;
    if (detState.foldHidden) {
      detState.foldHidden = false;
      why.hidden = false;
      why.classList.remove("det-pop-in");
      void why.offsetWidth;
      why.classList.add("det-pop-in");
    } else {
      detState.foldHidden = true;
      if (still() || typeof why.animate !== "function") {
        why.hidden = true;
      } else {
        const out = why.animate(
          [
            { opacity: 1, transform: "translateY(0) scale(1)" },
            { opacity: 0, transform: "translateY(3px) scale(.96)" }
          ],
          { duration: 180, easing: "cubic-bezier(.4, 0, 1, 1)", fill: "forwards" }
        );
        out.finished.then(() => {
          why.hidden = true;
          out.cancel();
        }).catch(() => {});
      }
    }
    const btn = document.getElementById("detFoldBtn");
    if (btn) btn.setAttribute("aria-expanded", String(!detState.foldHidden));
    const chev = btn && btn.querySelector(".det-chev");
    if (chev) chev.textContent = detState.foldHidden ? "▸" : "▾";
  }

  function flashCopied(btn) {
    if (!btn) return;
    btn.classList.add("det-copied");
    btn.addEventListener("animationend", () => btn.classList.remove("det-copied"), { once: true });
    const old = btn.textContent;
    btn.textContent = T("Copied to the clipboard");
    setTimeout(() => {
      btn.textContent = old;
    }, 1200);
  }

  // ---------- modals ----------------------------------------------------

  function openModal(html) {
    const host = document.getElementById("detModalHost");
    if (!host) return;
    host.hidden = false;
    host.innerHTML = `
      <div class="det-modal-backdrop" id="detModalBg">
        <div class="det-modal det-pop-in" role="dialog" aria-modal="true">${html}</div>
      </div>`;
    const close = () => closeModal();
    const bg = document.getElementById("detModalBg");
    if (bg) bg.onclick = (e) => { if (e.target.id === "detModalBg") close(); };
    document.querySelectorAll("[data-det-close]").forEach((b) => (b.onclick = close));
    const onKey = (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        close();
      }
    };
    document.addEventListener("keydown", onKey, { once: true });
    host._detKey = onKey;
  }

  function closeModal() {
    const host = document.getElementById("detModalHost");
    const bg = document.getElementById("detModalBg");
    if (!host) return;
    if (host._detKey) document.removeEventListener("keydown", host._detKey);
    const done = () => {
      host.hidden = true;
      host.innerHTML = "";
    };
    if (!bg || still() || typeof bg.firstElementChild?.animate !== "function") {
      done();
      return;
    }
    const panel = bg.firstElementChild;
    const out = panel.animate(
      [
        { opacity: 1, transform: "translateY(0) scale(1)" },
        { opacity: 0, transform: "translateY(4px) scale(.96)" }
      ],
      { duration: 170, easing: "cubic-bezier(.4, 0, 1, 1)", fill: "forwards" }
    );
    out.finished.then(done).catch(done);
  }

  function copyText(btn, text) {
    const fallback = () => {
      const ta = document.createElement("textarea");
      ta.value = text;
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.append(ta);
      ta.select();
      try {
        document.execCommand("copy");
      } catch (_) {}
      ta.remove();
    };
    if (navigator.clipboard?.writeText) navigator.clipboard.writeText(text).catch(fallback);
    else fallback();
    flashCopied(btn);
  }

  // ---------- shared view pieces ----------------------------------------

  function errorBanner() {
    if (!detState.error) return "";
    return `<div class="det-banner det-enter" role="alert">${dot("pink")}<span>${esc(detState.error)}</span></div>`;
  }

  function onlineCount() {
    const chans = detState.channels || [];
    return {
      online: chans.filter((c) => c.enabled && !c.breakerOpen).length,
      total: chans.length
    };
  }

  function renderHero() {
    const host = document.getElementById("detHeroStats");
    if (!host) return;
    const { online, total } = onlineCount();
    const r = detState.currentResult;
    const trace = r && r.traceId ? r.traceId : "";
    host.innerHTML = `
      <span class="det-stat">${dot(total && online === total ? "blue" : online ? "amber" : "pink")}${online}/${total || "?"} ${T("providers online now")}</span>
      ${trace ? `<span class="det-stat det-stat-mono">${T("Trace id")} ${esc(trace)}</span>` : ""}
      ${r && r.totalLatencyMs ? `<span class="det-stat det-stat-mono">${r.totalLatencyMs} ms</span>` : ""}
      <button type="button" class="det-ghost" id="detKeysBtn">${T("Search key status")}</button>`;
    const keys = document.getElementById("detKeysBtn");
    if (keys) keys.onclick = () => showKeysPanel();
  }

  function renderTabs() {
    const nav = document.getElementById("detSubtabs");
    if (!nav) return;
    const { online, total } = onlineCount();
    const r = detState.currentResult || {};
    const badge = {
      lab: r.verdict || "—",
      trajectory: String((r.trajectory || []).length || "—"),
      modes: String((detState.modes || []).length || "—"),
      proof: `${online}/${total || "?"}`,
      stakeout: String((detState.stakeouts || []).length || "0")
    };
    nav.innerHTML = TABS.map((tab) => {
      const on = detState.subtab === tab.id;
      return `<button type="button" role="tab" class="det-tab${on ? " on" : ""}" data-det-tab="${tab.id}" aria-selected="${on}">
        <span>${esc(T(tab.key))}</span><span class="det-tab-badge">${esc(badge[tab.id])}</span>
      </button>`;
    }).join("");
    nav.querySelectorAll("button[data-det-tab]").forEach((btn) => {
      btn.onclick = (ev) => swapView(btn.dataset.detTab, ev);
    });
  }

  function mountModePicker(host) {
    if (!host) return;
    if (!kit) {
      host.textContent = detState.selectedMode;
      return;
    }
    const opts = (detState.modes || []).map((m) => ({ v: m.id, name: `${m.id} · ${m.nameZh}` }));
    if (!opts.length) {
      host.textContent = detState.selectedMode;
      return;
    }
    const b = kit.menu(opts, detState.selectedMode, T("Search mode picker"), (v) => {
      detState.selectedMode = v;
      detState.modeTouched = true;
    });
    b.classList.add("det-mode-btn");
    host.replaceChildren(b);
  }

  function searchCard() {
    return `
      <section class="det-card det-search det-drop" id="detDrop">
        <div class="det-search-row">
          <label class="det-field">
            <span class="det-sr">${esc(T("Your question"))}</span>
            <input id="detQueryInput" class="det-input" type="search" autocomplete="off" spellcheck="false"
              value="${esc(detState.query)}" placeholder="${esc(T("Ask something to verify…"))}" />
          </label>
          <div class="det-mode-slot" id="detModeSlot"></div>
          <button type="button" class="det-btn det-btn-primary" id="detRunBtn" ${detState.busy ? "disabled" : ""}>
            ${detState.busy ? `<span class="det-spinner" aria-hidden="true"></span>${T("Investigating live…")}` : T("Start the investigation")}
          </button>
        </div>
        <div class="det-search-sub">
          <div class="det-chips" role="group" aria-label="${esc(T("Example questions"))}">
            ${TEMPLATES.map((tpl) => `<button type="button" class="det-chip" data-template-id="${tpl.id}">${esc(T(tpl.label))}</button>`).join("")}
          </div>
          <div class="det-upload">
            <input type="file" accept="image/*" id="detImageInput" hidden />
            <button type="button" class="det-chip" id="detPickImageBtn">${T("Add a screenshot")}</button>
            ${
              uploadedImage
                ? `<span class="det-chip det-chip-on">${esc(uploadedImage.name)}<button type="button" class="det-chip-x" id="detClearImageBtn" aria-label="${esc(T("Remove this image"))}">×</button></span>`
                : ""
            }
          </div>
        </div>
        <p class="det-hint">${T("Drop an image here, or choose one. EXIF is stripped before any model sees it.")}</p>
        ${uploadedImage ? `<p class="det-hint det-hint-on">${T("Screenshot attached")}: ${esc(uploadedImage.name)}${detState.modeTouched ? "" : ` · ${T("A screenshot uses the image-provenance mode.")}`}</p>` : ""}
      </section>`;
  }

  function metric(label, value, tone) {
    return `<span class="det-metric">${tone ? dot(tone) : ""}<span class="det-metric-label">${esc(label)}</span><span class="det-metric-value">${esc(value)}</span></span>`;
  }

  function answerCard(r) {
    const a = r.answer || {};
    if (a.status === "ok" && a.text) {
      const cites = (a.citeIndexes || []).map((i) => (r.citations || [])[i]).filter(Boolean);
      const domains = cites.map((c) => c.domain).filter(Boolean).slice(0, 6).join(" · ");
      return `<section class="det-card det-answer" data-answer="ok">
        <h3 class="det-answer-head">${T("Stated answer")} <span class="det-answer-model">${esc(a.model || "")}</span></h3>
        <p class="det-answer-text">${esc(a.text)}</p>
        <p class="det-answer-cites">${T("Built from {n} citations", { n: cites.length })}${domains ? ": " + esc(domains) : ""}</p>
      </section>`;
    }
    if (a.status === "insufficient") {
      return `<section class="det-card det-answer" data-answer="insufficient">
        <h3 class="det-answer-head">${T("Stated answer")}</h3>
        <p class="det-answer-text">${T("The evidence did not support an answer.")}</p>
      </section>`;
    }
    if (a.status === "disabled") {
      return `<section class="det-card det-answer" data-answer="disabled">
        <h3 class="det-answer-head">${T("Stated answer")}</h3>
        <p class="det-answer-text">${T("Answer synthesis is off. Turn it on in the gates.")}</p>
      </section>`;
    }
    if (a.status === "error" && a.error) {
      return `<section class="det-card det-answer" data-answer="error">
        <h3 class="det-answer-head">${T("Stated answer")}</h3>
        <p class="det-answer-text">${T("Answer failed: {detail}", { detail: a.error })}</p>
      </section>`;
    }
    return "";
  }

  function verdictCard(r) {
    const v = VERDICTS[r.verdict] || { key: r.verdict || "UNVERIFIED", tone: "muted" };
    const score = Number.isFinite(Number(r.trustScore)) ? Math.max(0, Math.min(100, Number(r.trustScore))) : 0;
    const guard = r.outputGuard || {};
    return `
      <section class="det-card det-verdict" data-verdict="${esc(r.verdict || "")}">
        <div class="det-verdict-head">
          <span class="det-verdict-dot-wrap">${dot(v.tone, "det-verdict-dot")}</span>
          <div class="det-verdict-copy">
            <h2 class="det-verdict-title">${esc(T(v.key))}</h2>
            <p class="det-verdict-sub">${esc(r.executiveSummary || "")}</p>
            ${r.abstainReason ? `<p class="det-verdict-reason">${esc(r.abstainReason)}</p>` : ""}
          </div>
          <div class="det-trust" style="--det-trust:${score}">
            <span class="det-trust-ring" aria-hidden="true"></span>
            <span class="det-trust-num">${score}</span>
            <span class="det-trust-label">${T("Trust score")}</span>
          </div>
        </div>
        <div class="det-metrics">
          ${metric(T("Evidence items"), String((r.citations || []).length), "blue")}
          ${metric(T("Answered by"), r.primaryEngine || "—")}
          ${metric(T("Duplicates folded"), String(r.collapsedSpamCount ?? 0), "pink")}
          ${metric(T("Total latency"), r.totalLatencyMs ? r.totalLatencyMs + " ms" : "—")}
          ${metric(T("Trace id"), r.traceId || "—")}
          ${guard.dropped ? metric(T("Red-line results dropped"), String(guard.dropped), "pink") : ""}
        </div>
      </section>`;
  }

  function guardStrip(r) {
    const a = r.redactAudit || {};
    const g = r.outputGuard || {};
    const kinds = Object.entries(g.kinds || {})
      .sort((x, y) => y[1] - x[1])
      .map(([k, n]) => `${k} ${n}`)
      .join(" · ");
    const dropped = Number(g.dropped || 0);
    return `
      <section class="det-guard" aria-label="${esc(T("Privacy gates"))}">
        <span class="det-guard-item">${dot(a.complianceStatus === "BLOCKED_REDLINE" ? "pink" : "blue")}<b>${T("Input privacy gate")}</b>
          <span>${esc(a.complianceStatus || "—")}</span>
          ${a.maskedCount ? `<span class="det-guard-count">${a.maskedCount} ${T("values replaced")}</span>` : ""}
        </span>
        <span class="det-guard-item">${dot(dropped ? "pink" : "blue")}<b>${T("Output privacy gate")}</b>
          <span>${g.kept ?? (r.citations || []).length}/${g.scanned ?? (r.citations || []).length}</span>
          ${g.maskedValues ? `<span class="det-guard-count">${g.maskedValues} ${T("values replaced")}</span>` : ""}
          ${dropped ? `<span class="det-guard-count det-guard-drop">${dropped} ${T("sources dropped")}</span>` : ""}
          ${kinds ? `<span class="det-guard-kinds">${esc(kinds)}</span>` : `<span class="det-guard-kinds">${T("No sensitive findings")}</span>`}
        </span>
      </section>`;
  }

  function audiencePanel(r) {
    if (!r.traceId) return "";
    if (detState.audience === "agent") {
      return `
        <section class="det-card det-enter" style="--det-i:2">
          <header class="det-sec-head">
            <h3>${T("Evidence envelope for agents")}</h3>
            <span class="det-sec-note">${T("Agent envelope is generated from this run.")}</span>
            <button type="button" class="det-ghost" id="detCopyXmlBtn">${T("Copy this text")}</button>
          </header>
          <pre class="det-pre det-pre-code" id="detXmlBox">${esc(r.agentEnvelopeXml || "")}</pre>
        </section>`;
    }
    if (detState.audience === "developer") {
      const snippet =
        detState.sdkLang === "py"
          ? `from molasearch import MolaSearchClient\n\nclient = MolaSearchClient(base_url="http://127.0.0.1:${esc(location.port || "8080")}")\ninv = client.investigate(\n    query=${JSON.stringify(detState.query)},\n    mode_id=${JSON.stringify(detState.selectedMode)},\n    debate=True,\n)\nprint(inv["verdict"], inv["trustScore"])\nfor c in inv["citations"]:\n    print(c["authorityLevel"], c["title"], c["url"])`
          : detState.sdkLang === "curl"
            ? `curl -s http://127.0.0.1:${esc(location.port || "8080")}/api/detective/investigate \\\n  -H 'Content-Type: application/json' \\\n  -d '${JSON.stringify({ query: detState.query, modeId: detState.selectedMode, debate: true }, null, 0)}'`
            : `import { MolaSearchClient } from "./sdk/ts/molasearch";\n\nconst client = new MolaSearchClient({ baseUrl: "http://127.0.0.1:${esc(location.port || "8080")}" });\nconst inv = await client.investigate({\n  query: ${JSON.stringify(detState.query)},\n  modeId: ${JSON.stringify(detState.selectedMode)},\n  debate: true,\n});\nconsole.log(inv.verdict, inv.trustScore, inv.citations.length);`;
      return `
        <section class="det-card det-enter" style="--det-i:2">
          <header class="det-sec-head">
            <h3>${T("Developer SDK")}</h3>
            <div class="det-mini-seg" id="detSdkSeg">
              ${["ts", "py", "curl"].map((lang) => `<button type="button" class="${detState.sdkLang === lang ? "on" : ""}" data-sdk="${lang}">${lang === "ts" ? "TypeScript" : lang === "py" ? "Python" : "HTTP"}</button>`).join("")}
            </div>
            <button type="button" class="det-ghost" id="detCopySdkBtn">${T("Copy this text")}</button>
          </header>
          <pre class="det-pre det-pre-code">${esc(snippet)}</pre>
          <p class="det-sec-note">${T("The page calls the same POST; the SDKs under sdk/ are the shipped clients.")}</p>
        </section>`;
    }
    return humanPanel(r);
  }

  function claimList(r) {
    const rows = r.claimMatrix || [];
    return `
      <section class="det-card det-enter" style="--det-i:2">
        <header class="det-sec-head"><h3>${T("Claim matrix")}</h3><span class="det-tab-badge">${rows.length}</span></header>
        ${
          rows.length
            ? `<div class="det-claims">${rows
                .map(
                  (cm) => `<div class="det-claim">
                    ${dot(cm.status === "VERIFIED" ? "blue" : cm.status === "REFUTED" ? "ink" : "pink")}
                    <div class="det-claim-body">
                      <div class="det-claim-text">${esc(cm.claim)}</div>
                      <div class="det-claim-fact">${esc(cm.fact)}</div>
                    </div>
                    <span class="det-tab-badge">${esc(cm.status)}</span>
                    ${cm.sourceUrl ? `<a class="det-ghost" href="${esc(cm.sourceUrl)}" target="_blank" rel="noopener noreferrer">${T("Open the source")}</a>` : ""}
                  </div>`
                )
                .join("")}</div>`
            : `<p class="det-empty">${T("No claims recorded.")}</p>`
        }
      </section>`;
  }

  function humanPanel(r) {
    return `
      <div class="det-lab-grid">
        <div class="det-col-main">
          ${claimList(r)}
          <section class="det-card det-enter" style="--det-i:3">
            <header class="det-sec-head">
              <h3>${T("Shareable briefing")}</h3>
              <button type="button" class="det-ghost" id="detCopyBriefBtn">${T("Copy this text")}</button>
            </header>
            <pre class="det-pre det-brief">${esc(r.markdownBriefing || "")}</pre>
          </section>
          ${sourcesSection(r)}
        </div>
        <aside class="det-rail">
          ${temporalSection(r)}
          ${foldSection(r)}
          ${debateSection(r)}
          ${multimodalSection(r)}
        </aside>
      </div>`;
  }

  function sourcesSection(r) {
    const cites = r.citations || [];
    return `
      <section class="det-card det-enter" style="--det-i:4">
        <header class="det-sec-head"><h3>${T("Primary sources")}</h3><span class="det-tab-badge">${cites.length}</span></header>
        ${
          cites.length
            ? `<div class="det-sources">${cites.map((c, i) => sourceRow(c, i)).join("")}</div>`
            : `<p class="det-empty">${T("No sources matched.")}</p>`
        }
      </section>`;
  }

  function sourceRow(c, i) {
    const fresh = detState.newUrls && detState.newUrls.has(c.url);
    return `
      <article class="det-src${fresh ? " det-new" : ""}" style="--det-i:${Math.min(i, 8)}">
        <span class="det-new-dot" aria-hidden="true"></span>
        <div class="det-src-body">
          <div class="det-src-head">
            <span class="det-idx">${i + 1}</span>
            <a class="det-src-title" href="${esc(c.url)}" target="_blank" rel="noopener noreferrer">${esc(c.title)}</a>
          </div>
          <p class="det-src-snippet">${esc(c.snippet)}</p>
          <div class="det-src-meta">
            ${dot(c.authorityLevel >= 4 ? "blue" : c.authorityLevel >= 3 ? "ink" : "pink")}
            <span>${esc(c.sourceName || c.domain)}</span>
            <span>${T("Source tier")} L${c.authorityLevel ?? 0}</span>
            <span>${esc(c.channel || "")}</span>
            ${c.publishedAt ? `<span>${esc(c.publishedAt)}</span>` : ""}
          </div>
        </div>
        <a class="det-open" href="${esc(c.url)}" target="_blank" rel="noopener noreferrer">${T("Open the source")}</a>
      </article>`;
  }

  function temporalSection(r) {
    const nodes = r.temporalTree || [];
    return `
      <section class="det-card det-enter" style="--det-i:3">
        <header class="det-sec-head"><h3>${T("Temporal tree")}</h3><span class="det-tab-badge">${nodes.length}</span></header>
        ${
          nodes.length
            ? `<ol class="det-tree">${nodes
                .map(
                  (n, i) => `<li class="det-tree-node" style="--det-i:${Math.min(i, 8)}">
                    <span class="det-tree-dot det-dot-${n.edgeType === "REFUTED" ? "ink" : n.edgeType === "SUPERSEDES" ? "blue" : "pink"}"></span>
                    <div class="det-tree-head"><time>${esc(n.timestamp)}</time><span class="det-tab-badge">${esc(n.edgeType || "")}</span></div>
                    <b>${esc(n.title)}</b>
                    <p>${esc(n.detail)}</p>
                    ${n.url ? `<a class="det-ghost" href="${esc(n.url)}" target="_blank" rel="noopener noreferrer">${T("Open the source")}</a>` : ""}
                  </li>`
                )
                .join("")}</ol>`
            : `<p class="det-empty">${T("No dated events found.")}</p>`
        }
      </section>`;
  }

  function foldSection(r) {
    const n = Number(r.collapsedSpamCount || 0);
    if (!n) return "";
    return `
      <section class="det-card det-fold det-enter" id="detFold" style="--det-i:4">
        <button type="button" class="det-fold-btn" id="detFoldBtn" aria-expanded="${!detState.foldHidden}">
          <span class="det-fold-count">${n}</span>
          <span>${T("Folded as near-duplicates")}</span>
          <span class="det-chev" aria-hidden="true">${detState.foldHidden ? "▸" : "▾"}</span>
        </button>
        <p class="det-fold-why" id="detFoldWhy" ${detState.foldHidden ? "hidden" : ""}>${T("Near-identical pages are folded by SimHash so one story is not counted twice.")}</p>
      </section>`;
  }

  function debateSection(r) {
    const d = r.debate || { pro: [], con: [], judgeSummary: "" };
    if (!(d.pro || []).length && !(d.con || []).length && !d.judgeSummary) return "";
    const col = (title, tone, items) => `
      <div class="det-debate-col">
        <h4>${dot(tone)}${esc(title)}</h4>
        ${(items || []).map((x) => `<p>${esc(x)}</p>`).join("") || `<p class="det-empty">—</p>`}
      </div>`;
    return `
      <section class="det-card det-enter" style="--det-i:5">
        <header class="det-sec-head"><h3>${T("Pro and con")}</h3></header>
        <div class="det-debate">
          ${col(T("For the claim"), "blue", d.pro)}
          ${col(T("Against the claim"), "pink", d.con)}
          <div class="det-debate-col"><h4>${dot("ink")}${T("Judge's reading")}</h4><p>${esc(d.judgeSummary || "—")}</p></div>
        </div>
      </section>`;
  }

  function multimodalSection(r) {
    const cards = (r.multimodalCards || []).slice();
    if (uploadedImage && uploadedImage.card && !cards.some((c) => c.title === uploadedImage.card.title)) {
      cards.unshift(uploadedImage.card);
    }
    if (!cards.length) return "";
    return `
      <section class="det-card det-enter" style="--det-i:6">
        <header class="det-sec-head"><h3>${T("Multimodal checks")}</h3><span class="det-tab-badge">${cards.length}</span></header>
        <div class="det-mm">
          ${cards
            .map(
              (c) => `<article class="det-mm-card">
                <header><b>${esc(c.title)}</b><span class="det-tab-badge">${esc(c.badge || c.status || "")}</span></header>
                <p>${esc(c.summary)}</p>
                <pre class="det-pre det-pre-tech">${esc(c.techDetail)}</pre>
                ${
                  Array.isArray(c.keyframes) && c.keyframes.length
                    ? `<div class="det-frames">${c.keyframes
                        .map((kf) => `<div class="det-frame${kf.anomaly ? " hit" : ""}"><b>${esc(kf.ts)}</b><span>${esc(kf.desc)}</span></div>`)
                        .join("")}</div>`
                    : ""
                }
                ${c.sourceUrl ? `<a class="det-ghost" href="${esc(c.sourceUrl)}" target="_blank" rel="noopener noreferrer">${T("Open the source")} · ${esc(c.provider || "")}</a>` : ""}
              </article>`
            )
            .join("")}
        </div>
      </section>`;
  }

  // ---------- lab view ---------------------------------------------------

  function labHTML() {
    const r = detState.currentResult;
    const hasResult = !!(r && r.traceId);
    return `
      ${errorBanner()}
      ${searchCard()}
      ${detState.busy && !hasResult ? skeletonCard() : ""}
      ${hasResult ? verdictCard(r) : emptyCard()}
      ${hasResult ? answerCard(r) : ""}
      ${hasResult ? guardStrip(r) : ""}
      ${
        hasResult
          ? `<div class="det-audience">
              <div class="det-seg" id="detAudSeg" style="--det-seg:${detState.audience === "human" ? 0 : detState.audience === "agent" ? 1 : 2}">
                <span class="det-seg-thumb" aria-hidden="true"></span>
                <button type="button" data-aud="human" class="${detState.audience === "human" ? "on" : ""}">${T("For people")}</button>
                <button type="button" data-aud="agent" class="${detState.audience === "agent" ? "on" : ""}">${T("For agents")}</button>
                <button type="button" data-aud="developer" class="${detState.audience === "developer" ? "on" : ""}">${T("For developers")}</button>
              </div>
              <span class="det-sec-note">${T("One evidence graph, compiled for three readers.")}</span>
            </div>
            <div id="detAudiencePanel">${audiencePanel(r)}</div>`
          : ""
      }
      ${detState.busy && hasResult ? `<div class="det-scan" aria-hidden="true"></div>` : ""}`;
  }

  function emptyCard() {
    return `
      <section class="det-card det-empty-card det-enter" style="--det-i:1">
        <span class="det-empty-mark" aria-hidden="true"></span>
        <h2>${T("No investigation yet")}</h2>
        <p>${T("No result yet. Ask a question; the answer is built from live pages.")}</p>
      </section>`;
  }

  function skeletonCard() {
    return `
      <section class="det-card det-skeleton-card" aria-busy="true">
        <div class="det-skeleton det-skeleton-title"></div>
        <div class="det-skeleton"></div>
        <div class="det-skeleton det-skeleton-short"></div>
        <p class="det-hint">${T("Searching live providers…")}</p>
      </section>`;
  }

  function bindLab(box) {
    const qInput = document.getElementById("detQueryInput");
    const runBtn = document.getElementById("detRunBtn");
    const modeSlot = document.getElementById("detModeSlot");
    mountModePicker(modeSlot);
    if (runBtn) {
      runBtn.onclick = () => runInvestigation(qInput ? qInput.value : detState.query, detState.selectedMode);
    }
    if (qInput) {
      qInput.oninput = () => {
        detState.query = qInput.value;
      };
      qInput.onkeydown = (e) => {
        if (e.key === "Enter") runInvestigation(qInput.value, detState.selectedMode);
      };
    }
    box.querySelectorAll("[data-template-id]").forEach((chip) => {
      chip.onclick = () => {
        const tpl = TEMPLATES.find((x) => x.id === chip.dataset.templateId);
        if (!tpl) return;
        detState.query = tpl.query;
        detState.selectedMode = tpl.mode;
        detState.modeTouched = false;
        if (qInput) qInput.value = tpl.query;
        if (modeSlot) mountModePicker(modeSlot);
      };
    });

    const drop = document.getElementById("detDrop");
    const input = document.getElementById("detImageInput");
    const pick = document.getElementById("detPickImageBtn");
    if (pick && input) {
      pick.onclick = () => input.click();
      input.onchange = () => {
        const f = input.files && input.files[0];
        if (f) uploadDetectiveImage(f);
      };
    }
    if (drop) {
      drop.ondragover = (e) => {
        e.preventDefault();
        drop.classList.add("over");
      };
      drop.ondragleave = () => drop.classList.remove("over");
      drop.ondrop = (e) => {
        e.preventDefault();
        drop.classList.remove("over");
        const f = e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files[0];
        if (f && /^image\//.test(f.type)) uploadDetectiveImage(f);
      };
    }
    const clear = document.getElementById("detClearImageBtn");
    if (clear) {
      clear.onclick = () => {
        uploadedImage = null;
        renderDetectiveAll();
      };
    }

    box.querySelectorAll("#detAudSeg button[data-aud]").forEach((btn) => {
      btn.onclick = () => {
        detState.audience = btn.dataset.aud;
        const seg = document.getElementById("detAudSeg");
        if (seg) seg.style.setProperty("--det-seg", String(["human", "agent", "developer"].indexOf(detState.audience)));
        box.querySelectorAll("#detAudSeg button").forEach((b) => b.classList.toggle("on", b === btn));
        const panel = document.getElementById("detAudiencePanel");
        const r = detState.currentResult;
        if (panel && r) {
          panel.innerHTML = audiencePanel(r);
          bindAudience(panel, r);
        }
      };
    });
    bindAudience(document.getElementById("detAudiencePanel"), detState.currentResult || {});
    const foldBtn = document.getElementById("detFoldBtn");
    if (foldBtn) foldBtn.onclick = toggleFold;
  }

  function bindAudience(panel, r) {
    if (!panel) return;
    const copyBrief = document.getElementById("detCopyBriefBtn");
    if (copyBrief) copyBrief.onclick = () => copyText(copyBrief, r.markdownBriefing || "");
    const copyXml = document.getElementById("detCopyXmlBtn");
    if (copyXml) copyXml.onclick = () => copyText(copyXml, r.agentEnvelopeXml || "");
    const copySdk = document.getElementById("detCopySdkBtn");
    if (copySdk) {
      copySdk.onclick = () => {
        const pre = panel.querySelector(".det-pre");
        copyText(copySdk, pre ? pre.textContent : "");
      };
    }
    panel.querySelectorAll("#detSdkSeg button[data-sdk]").forEach((btn) => {
      btn.onclick = () => {
        detState.sdkLang = btn.dataset.sdk;
        if (detState.currentResult) {
          panel.innerHTML = audiencePanel(detState.currentResult);
          bindAudience(panel, detState.currentResult);
        }
      };
    });
  }

  // ---------- trajectory view -------------------------------------------

  function trajectoryHeader(r) {
    return `
      <section class="det-card det-traj-head det-enter" style="--det-i:0">
        <div>
          <h2 class="det-h2">${T("Live trajectory")}</h2>
          <p class="det-sec-note">${T("Each step is a real request: where it came from, how long it took, what it returned.")}</p>
        </div>
        <div class="det-traj-actions">
          <div class="det-mini-seg" id="detNetSeg">
            <button type="button" data-net="flow" class="${detState.netView === "flow" ? "on" : ""}">${T("Flow view")}</button>
            <button type="button" data-net="graph" class="${detState.netView === "graph" ? "on" : ""}">${T("Force graph")}</button>
          </div>
          <button type="button" class="det-btn" id="detReplayBtn" ${(r.trajectory || []).length ? "" : "disabled"}>${T("Replay the trajectory")}</button>
        </div>
      </section>`;
  }

  function trajectoryLegend() {
    const keys = ["pctRedact", "pctPlan", "pctLocal", "pctNet", "pctRerank"];
    return `<div class="det-legend">${keys
      .map((k) => `<span class="det-legend-item"><i style="background:${stageColor(k)}"></i>${esc(pctLabel(k))}</span>`)
      .join("")}</div>`;
  }

  function stepsHTML(r) {
    const steps = r.trajectory || [];
    if (!steps.length) return `<section class="det-card"><p class="det-empty">${T("No trajectory yet.")}</p></section>`;
    return `
      <section class="det-card det-enter" style="--det-i:2">
        <header class="det-sec-head"><h3>${T("Request waterfall")}</h3>${trajectoryLegend()}</header>
        <ol class="det-steps">
          ${steps
            .map(
              (s, i) => `<li class="det-step" style="--det-i:${Math.min(i, 8)}">
                <span class="det-step-rail" aria-hidden="true">${dot(i === 0 ? "blue" : "ink")}</span>
                <div class="det-step-body">
                  <div class="det-step-top">
                    <span class="det-step-origin">${esc(s.originAgent || "")} · ${esc(s.modeId || "")}</span>
                    <span class="det-step-ms">${s.durationMs} ms</span>
                  </div>
                  <div class="det-step-params"><span>${esc(s.gateway || "")}</span><code>${esc(s.params || "")}</code></div>
                  <div class="det-wf" role="img" aria-label="${esc(T("Stage share of the request"))}">
                    ${["pctRedact", "pctPlan", "pctLocal", "pctNet", "pctRerank"]
                      .map((k) => `<span class="det-wf-seg" style="width:${Number(s[k] || 0)}%;background:${stageColor(k)}" title="${esc(pctLabel(k))}"></span>`)
                      .join("")}
                  </div>
                  <div class="det-step-foot">
                    <span class="det-step-return">${esc(s.returnSummary || "")}</span>
                    ${s.topUrl ? `<a class="det-ghost" href="${esc(s.topUrl)}" target="_blank" rel="noopener noreferrer">${esc(s.topDomain || T("Open the source"))}</a>` : ""}
                  </div>
                </div>
              </li>`
            )
            .join("")}
        </ol>
      </section>`;
  }

  const NET_COLS = [
    { key: "origins", title: "Query origin", tone: "blue", r: 11 },
    { key: "subQueries", title: "Planned queries", tone: "violet", r: 10 },
    { key: "gateways", title: "Channels queried", tone: "green", r: 11 },
    { key: "evidenceNodes", title: "Evidence nodes", tone: "amber", r: 9 },
    { key: "claims", title: "Claim nodes", tone: "pink", r: 11 }
  ];

  function netNodeLabel(key, row) {
    const raw = key === "subQueries" ? row.role || row.q : row.title || row.name || "";
    const s = String(raw || "").trim() || "—";
    return s.length > 18 ? s.slice(0, 17) + "…" : s;
  }

  // flowHTML draws the same five columns the page has always shown, with the
  // measured links as ant-lines and the layout wiring as dotted lines. A
  // channel that answered gets the hit shake; one that did not stays still.
  function flowHTML(net) {
    const cap = 5;
    const nodes = [];
    NET_COLS.forEach((col, ci) => {
      const rows = (net[col.key] || []).slice(0, cap);
      rows.forEach((row, i) => {
        nodes.push({
          col: ci,
          i,
          label: netNodeLabel(col.key, row),
          badge: String(row.badge ?? row.latency ?? row.hits ?? ""),
          sub: String(row.sub ?? row.q ?? row.endpoint ?? row.snippet ?? row.detail ?? ""),
          url: String(row.url || ""),
          hits: parseInt(String(row.hits ?? "0"), 10) || 0,
          primary: !!row.primary,
          spam: !!row.isSpamCluster,
          total: (net[col.key] || []).length
        });
      });
    });
    if (!nodes.length) return `<div class="det-card"><p class="det-empty">${T("No network to draw yet.")}</p></div>`;
    const W = 1060, top = 46, rowH = 54;
    const maxRows = Math.max(...NET_COLS.map((c) => Math.min(cap, (net[c.key] || []).length)), 1);
    const H = top * 2 + (maxRows - 1) * rowH;
    const colX = (ci) => 90 + ci * ((W - 180) / (NET_COLS.length - 1));
    const rowY = (n) => top + n.i * rowH + Math.max(0, (maxRows - Math.min(cap, n.total)) * rowH) / 2;
    const pos = (n) => ({ x: colX(n.col), y: rowY(n) });
    const byColRow = (ci, i) => nodes.find((n) => n.col === ci && n.i === i);

    const edges = [];
    for (let ci = 0; ci < NET_COLS.length - 1; ci++) {
      const from = nodes.filter((n) => n.col === ci);
      const to = nodes.filter((n) => n.col === ci + 1);
      from.forEach((n, i) => {
        if (to.length) edges.push({ a: n, b: to[i % to.length], real: false });
      });
    }
    (net.edges || []).forEach((e) => {
      const a = byColRow(3, Number(e.from));
      const b = byColRow(3, Number(e.to));
      if (a && b) edges.push({ a, b, real: true, weight: Number(e.weight) || 1 });
    });

    const path = (e) => {
      const A = pos(e.a), B = pos(e.b);
      const dx = Math.max(24, Math.abs(B.x - A.x) * 0.4);
      return `M ${A.x} ${A.y} C ${A.x + dx} ${A.y}, ${B.x - dx} ${B.y}, ${B.x} ${B.y}`;
    };
    const edgePath = edges
      .map((e) => `<path class="det-flow-edge ${e.real ? "live" : "wire"}" d="${path(e)}"${e.real ? ` style="--det-weight:${e.weight}"` : ""} />`)
      .join("");
    const nodeMarkup = nodes
      .map((n) => {
        const p = pos(n);
        const tone = NET_COLS[n.col].tone;
        const cls = ["det-flow-node", `tone-${tone}`];
        if (n.hits > 0) cls.push("hit");
        if (n.primary) cls.push("primary");
        if (n.spam) cls.push("spam");
        return `<g class="${cls.join(" ")}" data-url="${esc(n.url)}" tabindex="${n.url ? "0" : "-1"}" role="${n.url ? "link" : "img"}">
          <title>${esc(n.label)}${n.sub ? " — " + esc(n.sub) : ""}</title>
          <circle class="det-flow-ring" cx="${p.x}" cy="${p.y}" r="${NET_COLS[n.col].r + 5}" />
          <circle class="det-flow-dot" cx="${p.x}" cy="${p.y}" r="${NET_COLS[n.col].r}" />
          <text class="det-flow-label" x="${p.x}" y="${p.y + NET_COLS[n.col].r + 13}" text-anchor="middle">${esc(n.label)}</text>
          ${n.badge ? `<text class="det-flow-badge" x="${p.x}" y="${p.y - NET_COLS[n.col].r - 7}" text-anchor="middle">${esc(n.badge)}</text>` : ""}
        </g>`;
      })
      .join("");
    const colHeads = NET_COLS.map((c, ci) => `<text class="det-flow-col" x="${colX(ci)}" y="20" text-anchor="middle">${esc(T(c.title))}</text>`).join("");
    return `
      <section class="det-card det-enter" style="--det-i:1">
        <header class="det-sec-head"><h3>${T("Fusion network")}</h3><span class="det-sec-note">${T("Measured links are ant-lines; dotted lines are layout only.")}</span></header>
        <div class="det-flow-scroll">
          <svg class="det-flow" viewBox="0 0 ${W} ${H}" role="img" aria-label="${esc(T("Fusion network"))}">
            <g class="det-flow-edges">${edgePath}</g>
            <g class="det-flow-nodes">${nodeMarkup}</g>
            ${colHeads}
          </svg>
        </div>
        <div class="det-legend">
          ${NET_COLS.map((c) => `<span class="det-legend-item">${dot(c.tone)}${esc(T(c.title))}</span>`).join("")}
          <span class="det-legend-item"><i class="det-line live"></i>${T("Measured links")}</span>
          <span class="det-legend-item"><i class="det-line wire"></i>${T("Drawn for layout only")}</span>
        </div>
      </section>`;
  }

  function trajectoryHTML() {
    const r = detState.currentResult || {};
    const net = r.fusionNetwork || { origins: [], subQueries: [], gateways: [], evidenceNodes: [], claims: [] };
    return `
      ${errorBanner()}
      ${r.traceId ? trajectoryHeader(r) : `<section class="det-card"><p class="det-empty">${T("No trajectory yet.")}</p></section>`}
      ${r.traceId && detState.netView === "flow" ? flowHTML(net) : ""}
      ${
        r.traceId && detState.netView === "graph"
          ? `<section class="det-card det-enter" style="--det-i:1">
              <div class="det-graph-stage">
                <canvas class="det-graph-canvas" id="detGraphCanvas" role="img" aria-label="${esc(T("Force graph"))}"></canvas>
                <div class="det-graph-tip" id="detGraphTip" hidden></div>
              </div>
              <div class="det-graph-legend" id="detGraphLegend"></div>
              <div class="det-graph-note" id="detGraphNote" hidden></div>
            </section>`
          : ""
      }
      ${r.traceId ? stepsHTML(r) : ""}`;
  }

  function bindTrajectory(box) {
    const r = detState.currentResult || {};
    const replay = document.getElementById("detReplayBtn");
    if (replay) replay.onclick = replayTrajectory;
    box.querySelectorAll("#detNetSeg button[data-net]").forEach((btn) => {
      btn.onclick = () => {
        detState.netView = btn.dataset.net;
        renderDetectiveAll();
      };
    });
    box.querySelectorAll(".det-flow-node").forEach((g) => {
      const url = g.dataset.url;
      if (!url) return;
      const open = () => window.open(url, "_blank", "noopener");
      g.addEventListener("click", open);
      g.addEventListener("keydown", (e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          open();
        }
      });
    });
    if (detState.netView === "graph") startNetGraph(box, (r.fusionNetwork || {}));
  }

  // ---------- modes view ------------------------------------------------

  function modesHTML() {
    const modes = detState.modes || [];
    const families = ["ALL", ...Array.from(new Set(modes.map((m) => m.family)))];
    const filtered = detState.familyFilter === "ALL" ? modes : modes.filter((m) => m.family === detState.familyFilter);
    return `
      ${errorBanner()}
      <section class="det-card det-enter" style="--det-i:0">
        <div class="det-sec-head">
          <div>
            <h2 class="det-h2">${T("Search mode catalog")}</h2>
            <p class="det-sec-note">${T("30 modes in seven families. A mode changes the pipeline, not just a badge.")}</p>
          </div>
          <span class="det-tab-badge">${modes.length}</span>
        </div>
        <div class="det-inject">
          <span class="det-sec-note">${T("Write the skill into")}:</span>
          ${AGENT_PLATFORMS.map(
            (ag) => `<button type="button" class="det-chip ${detState.injected[ag] ? "on" : ""}" data-inject-agent="${ag}" title="${esc(detState.injected[ag] ? T("Remove the search skills from this agent") : T("Write the search skills and MCP config into this agent"))}">
              ${detState.injected[ag] ? "✓ " : "+ "}${esc(ag)}
            </button>`
          ).join("")}
        </div>
        ${detState.injectNote ? `<p class="det-hint">${esc(detState.injectNote)}</p>` : ""}
      </section>
      <div class="det-chips det-fam">
        ${families
          .map((fam) => `<button type="button" class="det-chip ${detState.familyFilter === fam ? "on" : ""}" data-fam="${esc(fam)}">${fam === "ALL" ? T("All families") : esc(fam)}</button>`)
          .join("")}
      </div>
      <div class="det-modes">
        ${filtered
          .map(
            (m, i) => `<article class="det-card det-mode det-enter" style="--det-i:${Math.min(i, 8)}">
              <div class="det-mode-head">
                ${dot("pink")}<span class="det-tab-badge">${esc(m.id)}</span>
                <b>${esc(m.nameZh)}</b><span class="det-sec-note">${esc(m.nameEn)}</span>
              </div>
              <p class="det-mode-meta">${esc(m.family)} · ${esc(m.latencyBudget)}</p>
              <p><b>${T("Mode pipeline")}</b> <code>${esc(m.topology)}</code></p>
              <p class="det-sec-note">${T("Provider mix")}: ${esc((m.suppliers || []).join(" · ") || "—")}</p>
              <footer class="det-mode-foot">
                <button type="button" class="det-ghost" data-use-mode="${esc(m.id)}">${T("Use this search mode")}</button>
                <button type="button" class="det-ghost" data-skill="${esc(m.id)}">${T("View the skill file")}</button>
              </footer>
            </article>`
          )
          .join("")}
      </div>`;
  }

  function bindModes(box) {
    box.querySelectorAll("button[data-fam]").forEach((btn) => {
      btn.onclick = () => {
        detState.familyFilter = btn.dataset.fam;
        renderDetectiveAll();
      };
    });
    box.querySelectorAll("button[data-use-mode]").forEach((btn) => {
      btn.onclick = () => {
        detState.selectedMode = btn.dataset.useMode;
        detState.modeTouched = true;
        detState.subtab = "lab";
        renderDetectiveAll();
      };
    });
    box.querySelectorAll("button[data-skill]").forEach((btn) => {
      btn.onclick = () => {
        const m = (detState.modes || []).find((x) => x.id === btn.dataset.skill);
        if (m) showSkillModal(m);
      };
    });
    box.querySelectorAll("button[data-inject-agent]").forEach((btn) => {
      btn.onclick = async () => {
        const ag = btn.dataset.injectAgent;
        const next = !detState.injected[ag];
        btn.disabled = true;
        const res = await detApi("detective/modes/inject", {
          method: "POST",
          body: JSON.stringify({ agent: ag, enabled: next })
        });
        btn.disabled = false;
        if (res && res.error) {
          detState.error = T("Could not inject into {agent} ({error})", { agent: ag, error: res.error });
        } else {
          detState.injected = { ...detState.injected, [ag]: next };
          const paths = next ? res.written || [] : res.removed || [];
          const label = paths.length
            ? paths.slice(0, 3).map((p) => shortPath(p)).join("  ·  ") + (paths.length > 3 ? ` +${paths.length - 3}` : "")
            : next
              ? T("The backend wrote no files.")
              : T("The backend had no files to remove.");
          detState.injectNote = (next ? T("Skills injected") + " " : T("Skills removed") + " ") + ag + ": " + label;
          detState.error = "";
        }
        renderDetectiveAll();
      };
    });
  }

  // ---------- proof view ------------------------------------------------

  const GATE_FIELDS = [
    ["topK", "Citations per verdict", 1, 100, 1],
    ["perChannel", "Results per channel", 1, 20, 1],
    ["mergeCap", "Merge pool size", 1, 500, 1],
    ["foldGamma", "Duplicate decay gamma", 0, 1, 0.01],
    ["channelTimeoutMs", "Channel timeout in milliseconds", 1000, 120000, 100],
    ["freshDays", "Freshness window in days", 1, 3650, 1],
    ["authorityHigh", "Authority level counted high", 1, 5, 1],
    ["authorityLow", "Authority level counted low", 1, 5, 1],
    ["verdictMinSignals", "Signals needed for a verdict", 1, 10, 1],
    ["supportFloor", "Minimum query support", 0, 1, 0.01],
    ["breakerFails", "Failures before the breaker opens", 1, 20, 1],
    ["breakerCooldownSec", "Breaker cooldown in seconds", 10, 3600, 10],
    ["gapRoundsCap", "AREX gap rounds allowed", 0, 5, 1],
    ["noveltyRoundsCap", "RAAC novelty rounds allowed", 0, 5, 1]
  ];

  function providerRows() {
    return (detState.channels || [])
      .map((c) => {
        const tone = !c.enabled ? "muted" : c.breakerOpen ? "amber" : "blue";
        return `<div class="det-prov" data-prov="${esc(c.id)}">
          ${dot(tone)}
          <div class="det-prov-name">
            <b>${esc(c.name)}</b><code>${esc(c.id)}</code>
            <span>${c.calls ?? 0} ${T("Calls made")} · ${c.hits ?? 0} ${T("Results hit")}${c.lastError ? " · " + esc(c.lastError) : ""}</span>
          </div>
          <span class="det-tab-badge">${c.keyed ? T("Keyed provider") : T("Keyless provider")}</span>
          <span class="det-tab-badge ${c.enabled ? "" : "off"}">${c.enabled ? T("This provider is on") : T("Provider is off")}</span>
          ${c.breakerOpen ? `<span class="det-tab-badge warn">${T("Breaker is open")} ${esc(c.breakerOpenUntil || "")}</span>` : ""}
          <span class="det-prov-switch" id="detSw-${esc(c.id)}"></span>
        </div>`;
      })
      .join("");
  }

  function proofHTML() {
    const s = detState.settings || detState.settingsDefaults || {};
    const { online, total } = onlineCount();
    return `
      ${errorBanner()}
      <section class="det-card det-enter" style="--det-i:0">
        <div class="det-sec-head">
          <div>
            <h2 class="det-h2">${T("Channels and gates")}</h2>
            <p class="det-sec-note">${T("Every provider has the same standing. Turning one off leaves the rest serving the run.")}</p>
          </div>
          <div class="det-traj-actions">
            <span class="det-stat">${dot(online === total && total ? "blue" : "amber")}${online}/${total || "?"}</span>
            <button type="button" class="det-ghost" id="detReloadBtn">${T("Refresh the provider state")}</button>
          </div>
        </div>
        <p class="det-hint">${T("Measured calls and hits are what this process saw; no vendor quota is invented.")}</p>
      </section>
      <section class="det-card det-enter" style="--det-i:1">
        <header class="det-sec-head"><h3>${T("Search providers")}</h3><span class="det-tab-badge">${total}</span></header>
        <div class="det-providers">${providerRows() || `<p class="det-empty">${T("No provider state was returned.")}</p>`}</div>
      </section>
      <section class="det-card det-enter" style="--det-i:2">
        <header class="det-sec-head">
          <h3>${T("Gate settings")}</h3>
          <div class="det-traj-actions">
            <button type="button" class="det-ghost" id="detResetBtn">${T("Reset the gates")}</button>
            <button type="button" class="det-btn det-btn-primary" id="detSaveBtn">${T("Save the gates")}</button>
          </div>
        </header>
        <div class="det-gates">
          ${GATE_FIELDS.map(
            ([key, label, min, max, step]) => `<label class="det-gate">
              <span>${esc(T(label))}</span><code>${esc(key)}</code>
              <input class="det-input det-gate-input" data-setting="${esc(key)}" type="number" min="${min}" max="${max}" step="${step}" value="${esc(s[key] ?? "")}" />
            </label>`
          ).join("")}
        </div>
        <div class="det-answer-gate">
          <label class="det-gate det-gate-switch">
            <span>${T("State the answer from the evidence")}</span>
            <code>answerEnabled</code>
            <input id="detAnswerOn" type="checkbox" ${(s.answerEnabled ?? true) ? "checked" : ""} />
          </label>
          <label class="det-gate">
            <span>${T("Answer model name")}</span>
            <code>answerModel</code>
            <input id="detAnswerModel" class="det-input" type="text" value="${esc(s.answerModel ?? "deepseek-chat")}" />
          </label>
        </div>
        <p class="det-hint">${T("Top citations bounds what a verdict may read; the merge pool is what ranking sees; the fold decay is how fast age discounts a near-duplicate; the freshness window drops only dated results, never undated ones.")}</p>
        <p class="det-hint det-stat-mono">${T("Gate settings file")}: ${esc(detState.settingsFile || "—")}</p>
      </section>`;
  }

  function bindProof(box) {
    const reload = document.getElementById("detReloadBtn");
    if (reload) reload.onclick = async () => {
      await loadEngineState();
      renderDetectiveAll();
    };
    (detState.channels || []).forEach((c) => {
      const host = document.getElementById("detSw-" + c.id);
      if (!host) return;
      if (kit) {
        host.replaceChildren(
          kit.toggle(!!c.enabled, T("This provider is on"), (on) => toggleChannel(c.id, on))
        );
      } else {
        const b = document.createElement("button");
        b.type = "button";
        b.className = "det-ghost";
        b.textContent = c.enabled ? T("Turn this provider off") : T("Turn this provider on");
        b.onclick = () => toggleChannel(c.id, !c.enabled);
        host.replaceChildren(b);
      }
    });
    const save = document.getElementById("detSaveBtn");
    if (save) {
      save.onclick = () => {
        const values = {};
        box.querySelectorAll("input[data-setting]").forEach((inp) => {
          values[inp.dataset.setting] = Number(inp.value);
        });
        saveSettings(Object.assign({}, detState.settingsDefaults || {}, detState.settings || {}, values));
      };
    }
    const reset = document.getElementById("detResetBtn");
    if (reset) {
      reset.onclick = () => {
        if (!detState.settingsDefaults) return;
        const restored = Object.assign({}, detState.settingsDefaults, {
          disabledChannels: (detState.settings && detState.settings.disabledChannels) || []
        });
        saveSettings(restored);
      };
    }
  }

  // ---------- stakeout view ---------------------------------------------

  function cronTextFor(seconds) {
    const s = Math.max(60, Math.round(Number(seconds) || 3600));
    if (s % 86400 === 0) return "每 " + s / 86400 + " 天";
    if (s % 3600 === 0) return "每 " + s / 3600 + " 小时";
    if (s % 60 === 0) return "每 " + s / 60 + " 分钟";
    return "每 " + s + " 秒";
  }

  function stakeoutHTML() {
    const jobs = detState.stakeouts || [];
    return `
      ${errorBanner()}
      <section class="det-card det-enter" style="--det-i:0">
        <div class="det-sec-head">
          <div>
            <h2 class="det-h2">${T("Stakeout watch list")}</h2>
            <p class="det-sec-note">${T("Watches re-run on a schedule and diff their snapshots.")}</p>
          </div>
          <button type="button" class="det-btn det-btn-primary" id="detCheckAllBtn">${T("Check every watch")}</button>
        </div>
      </section>
      <section class="det-card det-enter" style="--det-i:1">
        <header class="det-sec-head"><h3>${T("New stakeout watch")}</h3></header>
        <div class="det-watch-form">
          <label class="det-gate"><span>${T("Watch title")}</span><input class="det-input" id="detStakeTitle" type="text" placeholder="${esc(T("Optional title"))}" /></label>
          <label class="det-gate"><span>${T("Watch query")}</span><input class="det-input" id="detStakeQuery" type="text" placeholder="${esc(T("What should the watch re-check?"))}" /></label>
          <label class="det-gate"><span>${T("Check interval in seconds")}</span><input class="det-input" id="detStakeInterval" type="number" min="60" step="60" value="3600" /></label>
          <button type="button" class="det-btn det-btn-primary" id="detCreateBtn">${T("Create this watch")}</button>
        </div>
        ${detState.stakeDiff ? `<pre class="det-pre det-brief">${esc(detState.stakeDiff)}</pre>` : ""}
      </section>
      <div class="det-watches">
        ${jobs.length ? jobs.map((j, i) => watchCard(j, i)).join("") : `<section class="det-card"><p class="det-empty">${T("No watches yet.")}</p></section>`}
      </div>`;
  }

  function watchCard(j, i) {
    return `
      <article class="det-card det-watch det-enter" style="--det-i:${Math.min(i, 8)}">
        <div class="det-watch-head">
          ${dot(j.diffDetected ? "pink" : "blue")}
          <b>${esc(j.title)}</b>
          <span class="det-tab-badge">${esc(j.statusBadge || (j.diffDetected ? T("Snapshot changed") : T("Snapshot unchanged")))}</span>
        </div>
        <p class="det-watch-query"><code>${esc(j.query)}</code></p>
        <p class="det-hint">${esc(j.cron || "")} · ${T("Last checked at")}: ${esc(j.lastCheckedAt || "—")} · ${esc(j.searchParams || "—")}</p>
        <div class="det-watch-diff">
          <span class="det-sec-note">${T("Latest watch result")}</span>
          <p>${esc(j.latestDiff || T("Snapshot unchanged"))}</p>
        </div>
        <footer class="det-mode-foot">
          ${j.sourceUrl ? `<a class="det-ghost" href="${esc(j.sourceUrl)}" target="_blank" rel="noopener noreferrer">${T("Open the source")}</a>` : `<span></span>`}
          <div class="det-traj-actions">
            <button type="button" class="det-ghost" data-stake-check="${esc(j.id)}">${T("Check this watch now")}</button>
            <button type="button" class="det-ghost danger" data-stake-delete="${esc(j.id)}">${T("Delete this watch")}</button>
          </div>
        </footer>
      </article>`;
  }

  function applyCheckResponse(res) {
    if (res && res.error) {
      detState.error = T("Could not check the watch: {error}", { error: res.error });
      return;
    }
    if (res && Array.isArray(res.jobs)) {
      const byId = new Map((detState.stakeouts || []).map((j) => [j.id, j]));
      for (const j of res.jobs) byId.set(j.id, j);
      detState.stakeouts = [...byId.values()];
    }
    const parts = [];
    if (res && res.diff) parts.push(res.diff.Summary || JSON.stringify(res.diff, null, 2));
    if (res && Array.isArray(res.diffs)) for (const d of res.diffs) parts.push(d.Summary || JSON.stringify(d, null, 2));
    detState.stakeDiff = parts.filter(Boolean).join("\n");
    detState.error = "";
  }

  function bindStakeout(box) {
    async function reloadStakeouts() {
      const res = await detApi("detective/stakeout");
      if (res && Array.isArray(res.jobs)) detState.stakeouts = res.jobs;
      else if (res && res.error) detState.error = T("Could not load the watches: {error}", { error: res.error });
      renderDetectiveAll();
    }
    const checkAll = document.getElementById("detCheckAllBtn");
    if (checkAll) {
      checkAll.onclick = async () => {
        checkAll.disabled = true;
        const res = await detApi("detective/stakeout/check", { method: "POST", body: JSON.stringify({}) });
        applyCheckResponse(res);
        renderDetectiveAll();
      };
    }
    const create = document.getElementById("detCreateBtn");
    if (create) {
      create.onclick = async () => {
        const title = (document.getElementById("detStakeTitle") || {}).value || "";
        const query = (document.getElementById("detStakeQuery") || {}).value || "";
        const interval = Number((document.getElementById("detStakeInterval") || {}).value || 3600);
        if (!query.trim()) {
          detState.error = T("Please enter a query to watch.");
          renderDetectiveAll();
          return;
        }
        create.disabled = true;
        const res = await detApi("detective/stakeout", {
          method: "POST",
          body: JSON.stringify({ title: title.trim(), query: query.trim(), cron: cronTextFor(interval), interval })
        });
        create.disabled = false;
        if (res && res.error) {
          detState.error = T("Could not create the watch: {error}", { error: res.error });
          renderDetectiveAll();
          return;
        }
        detState.error = "";
        detState.stakeDiff = "";
        await reloadStakeouts();
      };
    }
    box.querySelectorAll("button[data-stake-check]").forEach((b) => {
      b.onclick = async () => {
        b.disabled = true;
        const res = await detApi("detective/stakeout/check", {
          method: "POST",
          body: JSON.stringify({ jobId: b.dataset.stakeCheck })
        });
        applyCheckResponse(res);
        renderDetectiveAll();
      };
    });
    box.querySelectorAll("button[data-stake-delete]").forEach((b) => {
      b.onclick = async () => {
        b.disabled = true;
        const res = await detApi("detective/stakeout/" + encodeURIComponent(b.dataset.stakeDelete), { method: "DELETE" });
        if (res && res.error) {
          detState.error = T("Could not delete the watch: {error}", { error: res.error });
          renderDetectiveAll();
          return;
        }
        detState.error = "";
        await reloadStakeouts();
      };
    });
  }

  // ---------- modal content ---------------------------------------------

  async function showKeysPanel() {
    openModal(`
      <header class="det-sec-head">
        <h3>${T("Engine keys")}</h3>
        <button type="button" class="det-ghost" data-det-close>${T("Close this panel")}</button>
      </header>
      <div id="detKeysBody" class="det-sec-note">${T("Reading the key state…")}</div>`);
    const res = await detApi("detective/keys");
    const body = document.getElementById("detKeysBody");
    if (!body) return;
    if (res && res.error) {
      body.innerHTML = `<p class="det-empty">${esc(T("Could not read the keys: {error}", { error: res.error }))}</p>`;
      return;
    }
    const envFile = (res && res.envFile) || detState.engine.envFile;
    const keys = (res && res.keys) || [];
    body.innerHTML = `
      <p class="det-hint det-stat-mono">${T("Keys file")}: ${esc(envFile || "—")}</p>
      <div class="det-keys">
        ${
          keys.length
            ? keys
                .map(
                  (k) => `<div class="det-key">
                    <div><b>${esc(k.name)}</b><code>${esc(k.env || "")}</code><span>${esc(k.hint || "")}</span></div>
                    <span class="det-tab-badge ${k.set ? "" : "warn"}">${esc(k.set ? T("Key configured") : T("Key not configured"))}</span>
                  </div>`
                )
                .join("")
            : `<p class="det-empty">${T("No engine keys were returned.")}</p>`
        }
      </div>`;
  }

  function showSkillModal(mode) {
    openModal(`
      <header class="det-sec-head">
        <h3>${esc(mode.id)} · ${esc(mode.nameZh)}</h3>
        <button type="button" class="det-ghost" data-det-close>${T("Close this panel")}</button>
      </header>
      <pre class="det-pre det-pre-code">---
name: molasearch-mode-${esc(mode.slug)}
description: "${esc(mode.id)} ${esc(mode.nameZh)} (${esc(mode.nameEn)}) — MolaSearch multi-source search"
version: 5.0.0
mode_id: ${esc(mode.id)}
family: "${esc(mode.family)}"
latency_budget: "${esc(mode.latencyBudget)}"
---

# ${esc(mode.id)}: ${esc(mode.nameZh)}

- topology: \`${esc(mode.topology)}\`
- mcp_tool: \`magpie_${esc(String(mode.id).toLowerCase())}_${esc(String(mode.slug).replace(/-/g, "_"))}\``);
  }

  // ---------- the graph (force view) ------------------------------------

  const NET_CAP = 10;
  const NET_GRAPH_COLS = [
    { key: "origins", title: "Origin", color: "--c1", r: 15 },
    { key: "subQueries", title: "Plan", color: "--c2", r: 12 },
    { key: "gateways", title: "Channels", color: "--c3", r: 13 },
    { key: "evidenceNodes", title: "Evidence", color: "--c4", r: 11 },
    { key: "claims", title: "Claims", color: "--c5", r: 13 }
  ];

  function stopNetGraph() {
    if (netGraphStop) netGraphStop();
    netGraphStop = null;
  }

  function netGraphModel(net) {
    const nodes = [], edges = [], capped = [];
    NET_GRAPH_COLS.forEach((col, ci) => {
      const rows = net[col.key] || [];
      if (rows.length > NET_CAP) capped.push(`${col.title} ${rows.length} → ${NET_CAP}`);
      rows.slice(0, NET_CAP).forEach((row, i) => {
        nodes.push({
          col: ci,
          i,
          label: netNodeLabel(col.key, row),
          sub: String(row.sub ?? row.q ?? row.endpoint ?? row.snippet ?? row.detail ?? ""),
          badge: String(row.badge ?? row.mode ?? row.latency ?? ""),
          url: String(row.url || ""),
          spam: !!row.isSpamCluster,
          primary: !!row.primary,
          r: col.r,
          x: 0, y: 0, vx: 0, vy: 0
        });
      });
    });
    for (let ci = 0; ci < NET_GRAPH_COLS.length - 1; ci++) {
      const from = nodes.filter((n) => n.col === ci);
      const to = nodes.filter((n) => n.col === ci + 1);
      from.forEach((n, i) => {
        if (to.length) edges.push({ a: n, b: to[i % to.length], real: false, weight: 1 });
      });
    }
    (net.edges || []).forEach((e) => {
      const a = nodes.find((n) => n.col === 3 && n.i === Number(e.from));
      const b = nodes.find((n) => n.col === 3 && n.i === Number(e.to));
      if (a && b) edges.push({ a, b, real: true, weight: Number(e.weight) || 1 });
    });
    return { nodes, edges, capped };
  }

  function startNetGraph(box, net) {
    stopNetGraph();
    const canvas = box.querySelector("#detGraphCanvas");
    const stage = canvas && canvas.parentElement;
    const tip = box.querySelector("#detGraphTip");
    const legend = box.querySelector("#detGraphLegend");
    const note = box.querySelector("#detGraphNote");
    if (!canvas || !stage) return;

    const model = netGraphModel(net);
    const nodes = model.nodes, edges = model.edges;

    if (legend) {
      legend.innerHTML =
        NET_GRAPH_COLS.map((col, ci) => {
          const count = nodes.filter((n) => n.col === ci).length;
          return `<span class="det-graph-key"><i style="background:var(${col.color})"></i>${esc(T(col.title))} <b>${count}</b></span>`;
        }).join("") +
        (edges.some((e) => e.real) ? `<span class="det-graph-key"><i class="measured"></i>${T("Measured links")}</span>` : "") +
        `<span class="det-graph-key"><i class="wired"></i>${T("Drawn for layout only")}</span>`;
    }
    if (note) {
      note.hidden = !model.capped.length;
      note.textContent = model.capped.length ? model.capped.join(" · ") : "";
    }

    if (!nodes.length) {
      if (tip) tip.hidden = true;
      const ctx0 = canvas.getContext("2d");
      if (ctx0) {
        canvas.width = 640;
        canvas.height = 200;
        ctx0.fillStyle = "rgba(128,128,140,.9)";
        ctx0.font = "13px system-ui, sans-serif";
        ctx0.fillText(T("No network to draw yet."), 24, 60);
      }
      return;
    }

    const css = getComputedStyle(document.documentElement);
    const varOf = (name, fallback) => (css.getPropertyValue(name) || "").trim() || fallback;
    const colors = NET_GRAPH_COLS.map((c) => varOf(c.color, "#4f46e5"));
    const inkFg = varOf("--fg", "#1c1c21");
    const inkLine = varOf("--line", "rgba(120,120,140,.35)");
    const inkMuted = varOf("--muted", "#85858d");
    const inkAccent = varOf("--accent", "#4f46e5");

    let W = 0, H = 0, dpr = 1, raf = 0, frames = 0, alive = true, hover = null;

    function measure() {
      const rect = stage.getBoundingClientRect();
      W = Math.max(280, Math.round(rect.width));
      H = Math.max(300, Math.round(rect.height));
      dpr = Math.min(3, window.devicePixelRatio || 1);
      canvas.width = Math.round(W * dpr);
      canvas.height = Math.round(H * dpr);
      canvas.style.width = W + "px";
      canvas.style.height = H + "px";
    }

    function seed() {
      const pad = 52;
      NET_GRAPH_COLS.forEach((col, ci) => {
        const band = nodes.filter((n) => n.col === ci);
        band.forEach((n, i) => {
          n.x = pad + ((W - pad * 2) * ci) / (NET_GRAPH_COLS.length - 1) + (Math.random() - 0.5) * 20;
          n.y = pad + ((H - pad * 2) * (i + 1)) / (band.length + 1) + (Math.random() - 0.5) * 20;
          n.vx = 0;
          n.vy = 0;
        });
      });
    }

    const REPULSION = 2600, SPRING = 0.02, SPRING_LEN = 130, CENTRE = 0.012, DAMPING = 0.86;

    function step() {
      for (let i = 0; i < nodes.length; i++) {
        const a = nodes[i];
        for (let j = i + 1; j < nodes.length; j++) {
          const b = nodes[j];
          let dx = b.x - a.x, dy = b.y - a.y;
          let d2 = dx * dx + dy * dy;
          if (d2 < 1) { dx = Math.random() - 0.5; dy = Math.random() - 0.5; d2 = 1; }
          const d = Math.sqrt(d2);
          const f = REPULSION / d2;
          const ux = dx / d, uy = dy / d;
          a.vx -= ux * f; a.vy -= uy * f;
          b.vx += ux * f; b.vy += uy * f;
        }
      }
      for (const e of edges) {
        const dx = e.b.x - e.a.x, dy = e.b.y - e.a.y;
        const d = Math.sqrt(dx * dx + dy * dy) || 1;
        const f = (d - SPRING_LEN) * SPRING * (e.real ? 1.4 : 1);
        const ux = dx / d, uy = dy / d;
        e.a.vx += ux * f; e.a.vy += uy * f;
        e.b.vx -= ux * f; e.b.vy -= uy * f;
      }
      const cx = W / 2, cy = H / 2;
      let energy = 0;
      for (const n of nodes) {
        n.vx += (cx - n.x) * CENTRE;
        n.vy += (cy - n.y) * CENTRE;
        n.vx *= DAMPING; n.vy *= DAMPING;
        n.x = Math.max(n.r + 8, Math.min(W - n.r - 8, n.x + n.vx));
        n.y = Math.max(n.r + 8, Math.min(H - n.r - 8, n.y + n.vy));
        energy += n.vx * n.vx + n.vy * n.vy;
      }
      return energy;
    }

    const linkedTo = (n) => hover && edges.some((e) => (e.a === hover && e.b === n) || (e.b === hover && e.a === n));

    function draw() {
      const ctx = canvas.getContext("2d");
      if (!ctx) return;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.clearRect(0, 0, W, H);
      for (const e of edges) {
        const near = !hover || e.a === hover || e.b === hover;
        ctx.globalAlpha = near ? (e.real ? 0.55 : 0.38) : 0.1;
        ctx.strokeStyle = e.real ? inkAccent : inkLine;
        ctx.lineWidth = e.real ? Math.min(3.4, 0.9 + e.weight * 0.5) : 1;
        ctx.beginPath();
        ctx.moveTo(e.a.x, e.a.y);
        ctx.lineTo(e.b.x, e.b.y);
        ctx.stroke();
      }
      for (const n of nodes) {
        const dim = hover && hover !== n && !linkedTo(n);
        ctx.globalAlpha = dim ? 0.28 : 1;
        ctx.beginPath();
        ctx.arc(n.x, n.y, n.r, 0, Math.PI * 2);
        ctx.fillStyle = colors[n.col];
        ctx.globalAlpha *= 0.22;
        ctx.fill();
        ctx.globalAlpha = dim ? 0.28 : 1;
        ctx.lineWidth = n.spam ? 2 : 1.4;
        ctx.setLineDash(n.spam ? [3, 3] : []);
        ctx.strokeStyle = n.spam ? inkMuted : colors[n.col];
        ctx.stroke();
        ctx.setLineDash([]);
        if (n.primary) {
          ctx.beginPath();
          ctx.arc(n.x, n.y, n.r + 4, 0, Math.PI * 2);
          ctx.strokeStyle = inkAccent;
          ctx.lineWidth = 1.4;
          ctx.stroke();
        }
        if (hover === n) {
          ctx.beginPath();
          ctx.arc(n.x, n.y, n.r + 6, 0, Math.PI * 2);
          ctx.strokeStyle = inkAccent;
          ctx.lineWidth = 1;
          ctx.stroke();
        }
        ctx.globalAlpha = dim ? 0.3 : 1;
        ctx.fillStyle = inkFg;
        ctx.font = "10.5px system-ui, -apple-system, 'Segoe UI', sans-serif";
        ctx.textAlign = "center";
        ctx.textBaseline = "top";
        ctx.fillText(n.label, n.x, n.y + n.r + 5, 124);
      }
      ctx.globalAlpha = 1;
      ctx.textAlign = "start";
      ctx.textBaseline = "alphabetic";
    }

    function hit(px, py) {
      let best = null, bestD = Infinity;
      for (const n of nodes) {
        const dx = px - n.x, dy = py - n.y;
        const d = Math.sqrt(dx * dx + dy * dy);
        if (d <= n.r + 7 && d < bestD) { best = n; bestD = d; }
      }
      return best;
    }

    function onMove(ev) {
      const rect = canvas.getBoundingClientRect();
      const found = hit(ev.clientX - rect.left, ev.clientY - rect.top);
      canvas.style.cursor = found && found.url ? "pointer" : "default";
      if (found !== hover) {
        hover = found;
        draw();
      }
      if (!tip) return;
      if (!found) { tip.hidden = true; return; }
      tip.replaceChildren();
      const title = document.createElement("b");
      title.textContent = found.label;
      tip.append(title);
      if (found.badge) {
        const badge = document.createElement("span");
        badge.className = "det-graph-tip-badge";
        badge.textContent = found.badge;
        tip.append(badge);
      }
      if (found.sub) {
        const sub = document.createElement("div");
        sub.className = "det-graph-tip-sub";
        sub.textContent = found.sub;
        tip.append(sub);
      }
      if (found.url) {
        const url = document.createElement("div");
        url.className = "det-graph-tip-url";
        url.textContent = T("Open the source") + " ↗";
        tip.append(url);
      }
      tip.hidden = false;
      const left = Math.min(Math.max(8, found.x + found.r + 10), Math.max(8, W - 240));
      const top = Math.min(Math.max(8, found.y - 12), Math.max(8, H - 96));
      tip.style.left = left + "px";
      tip.style.top = top + "px";
    }

    function onLeave() {
      hover = null;
      canvas.style.cursor = "default";
      if (tip) tip.hidden = true;
      draw();
    }

    function onClick() {
      if (hover && hover.url) window.open(hover.url, "_blank", "noopener");
    }

    const reduced = still();

    function kick() {
      if (reduced) return;
      if (alive && !raf && !document.hidden) raf = requestAnimationFrame(tick);
    }

    function tick() {
      raf = 0;
      if (!alive || document.hidden) return;
      frames++;
      const energy = step();
      draw();
      if (frames < 600 && !(frames > 30 && energy < 0.4)) raf = requestAnimationFrame(tick);
    }

    const onVis = () => {
      if (document.hidden) {
        if (raf) cancelAnimationFrame(raf);
        raf = 0;
      } else kick();
    };

    function onResize() {
      if (!alive) return;
      const before = W + "x" + H;
      measure();
      if (before === W + "x" + H) return;
      seed();
      frames = 0;
      if (reduced) for (let i = 0; i < 200; i++) step();
      draw();
      kick();
    }

    measure();
    seed();
    if (reduced) {
      for (let i = 0; i < 200; i++) step();
      draw();
    } else {
      draw();
      kick();
    }

    const ro = typeof ResizeObserver === "function" ? new ResizeObserver(onResize) : null;
    if (ro) ro.observe(stage);
    else window.addEventListener("resize", onResize);
    canvas.addEventListener("pointermove", onMove);
    canvas.addEventListener("pointerleave", onLeave);
    canvas.addEventListener("click", onClick);
    document.addEventListener("visibilitychange", onVis);

    netGraphStop = () => {
      alive = false;
      if (raf) cancelAnimationFrame(raf);
      raf = 0;
      if (ro) ro.disconnect();
      else window.removeEventListener("resize", onResize);
      document.removeEventListener("visibilitychange", onVis);
      canvas.removeEventListener("pointermove", onMove);
      canvas.removeEventListener("pointerleave", onLeave);
      canvas.removeEventListener("click", onClick);
    };
  }

  // ---------- render ----------------------------------------------------

  function renderDetectiveAll() {
    stopNetGraph();
    const brandSub = document.getElementById("detBrandSub");
    if (brandSub) {
      brandSub.textContent = T("Live multi-source investigation. Every citation is a real page; no evidence means an honest abstention.");
    }
    const tabList = document.getElementById("detSubtabs");
    if (tabList) tabList.setAttribute("aria-label", T("Investigation desk"));
    renderHero();
    renderTabs();
    const content = document.getElementById("detContent");
    if (!content) return;
    if (detState.subtab === "lab") {
      content.innerHTML = labHTML();
      bindLab(content);
      if (detState.freshResult) {
        announceResult();
        detState.freshResult = false;
        detState.newUrls = new Set();
      }
    } else if (detState.subtab === "trajectory") {
      content.innerHTML = trajectoryHTML();
      bindTrajectory(content);
    } else if (detState.subtab === "modes") {
      content.innerHTML = modesHTML();
      bindModes(content);
    } else if (detState.subtab === "proof") {
      content.innerHTML = proofHTML();
      bindProof(content);
    } else if (detState.subtab === "stakeout") {
      content.innerHTML = stakeoutHTML();
      bindStakeout(content);
    }
  }

  magpie.plugin("detective", (ctx) => {
    kit = ctx.ui;
    ctx.slots.add("page", {
      key: "detective",
      title: "Detective",
      html: `
        <div class="det-wrap">
          <header class="det-hero">
            <div class="det-brand">
              <span class="det-brand-mark" aria-hidden="true"></span>
              <div>
                <h1 class="det-brand-name">MolaSearch</h1>
                <p class="det-brand-sub" id="detBrandSub"></p>
              </div>
            </div>
            <div class="det-hero-stats" id="detHeroStats"></div>
          </header>
          <nav class="det-tabs" id="detSubtabs" role="tablist" aria-label="${esc(T("Investigation desk"))}"></nav>
          <div id="detContent" class="det-content"></div>
          <div id="detModalHost" hidden></div>
        </div>`,
      open() {
        if (!detState.currentResult) return loadInitialDetectiveData();
        renderDetectiveAll();
      },
      refresh() {
        return loadInitialDetectiveData();
      },
      reload() {
        return loadInitialDetectiveData();
      },
      leave() {
        stopNetGraph();
      }
    });
  });
})();
