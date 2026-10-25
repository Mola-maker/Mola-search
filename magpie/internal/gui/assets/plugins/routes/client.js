// Search routes — every reliable search path, the truth about its keys, and
// the last request that really went through it.
//
// One render pipeline over one state object:
//   1. data   — GET /api/routes, GET/POST /api/hotplug, POST /api/routes/*
//   2. motion — calm()/still() before every animation; a route's flow is a
//               rail whose fill and ticks come from the server's measured
//               steps (offset and milliseconds), never from a template
//   3. words  — every string through t(); this page's dictionaries are in
//               i18n.js, and a word the core already says is not repeated
//
// No number here is invented: a route that has not run says so, a scan's
// count is what the folders held, and a swarm's duplicates and conflicts
// are what the resolution found.

(function () {
  const MODES = [
    { v: "M01", name: "M01 Direct" },
    { v: "M11", name: "M11 Self-RAG" },
    { v: "M13", name: "M13 Debate" },
    { v: "M17", name: "M17 Temporal" },
    { v: "M23", name: "M23 Code forensics" }
  ];

  let kit = null;
  let page = null;

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
    return `<span class="rt-dot rt-dot-${tone}" aria-hidden="true"></span>`;
  }

  const st = {
    tab: "routes",
    routes: [],
    flows: {},
    stacks: [],
    schedules: [],
    hot: [],
    assetsFromDisk: false,
    scanAt: "",
    error: "",
    busy: {},
    probe: {},
    hotBusy: {},
    hotError: "",
    action: "",
    draft: {},
    swarmQuery: "",
    swarmMode: "M11",
    selected: null,
    swarm: null,
    swarmBusy: false,
    scheduleQuery: "",
    scheduleEvery: 3600,
    scheduleMode: "M11",
    scheduleBusy: false
  };

  // ---------- data ------------------------------------------------------

  function applyRoutes(j) {
    if (!j) return;
    st.routes = j.routes || [];
    st.flows = {};
    for (const f of j.flows || []) st.flows[f.routeId] = f;
    st.stacks = j.stacks || [];
    st.schedules = j.schedules || [];
    st.assetsFromDisk = !!j.assetsFromDisk;
    st.scanAt = j.scanAt || "";
    st.error = j.errors && (j.errors.stacks || j.errors.scan) ? (j.errors.stacks || j.errors.scan) : "";
    if (!st.selected) {
      st.selected = new Set(st.stacks.filter((s) => (s.routes || []).length).map((s) => s.agent));
    }
  }

  function applyHot(j) {
    if (!j) return;
    st.hot = j.plugins || [];
    if (typeof j.assetsFromDisk === "boolean") st.assetsFromDisk = j.assetsFromDisk;
  }

  async function load() {
    try {
      const [routes, hot] = await Promise.all([api("routes"), api("hotplug")]);
      applyRoutes(routes);
      applyHot(hot);
      st.error = "";
    } catch (err) {
      st.error = err.message;
    }
    draw();
  }

  // ---------- small render helpers --------------------------------------

  function routeTone(r) {
    if (!r.enabled) return "off";
    if (!r.configured) return "warn";
    return "ok";
  }

  function flowRouteId(r) {
    return r.channel || r.id;
  }

  // flowHTML is a route's last real request: the rail's ticks sit at the
  // steps' own offsets and the fill takes the measured time. Nothing runs
  // when no request has happened.
  function flowHTML(r) {
    const f = st.flows[flowRouteId(r)];
    if (!f) return `<div class="rt-flow idle"><span>${esc(T("No request has run yet"))}</span></div>`;
    const dur = Math.max(f.durationMs || 0, 1);
    const ticks = (f.steps || [])
      .map((s) => {
        const left = Math.min(100, Math.max(0, ((s.atMs || 0) / dur) * 100));
        return `<i class="rt-tick ${s.state === "failed" ? "bad" : ""}" style="left:${left.toFixed(2)}%"></i>`;
      })
      .join("");
    const tone = f.state === "ok" ? "ok" : "bad";
    return `<div class="rt-flow ${tone}${still() ? " still" : ""}">
      <div class="rt-rail" role="img" aria-label="${esc(T("Its flow"))}">
        <span class="rt-fill" style="--dur:${Math.min(dur, 1800)}ms"></span>${ticks}
      </div>
      <div class="rt-flow-meta">${dot(tone)}<span>${esc(f.source || "")}</span>
        <span>${esc(T("Its last request took {ms} ms", { ms: f.durationMs || 0 }))}</span>
        <span>${esc(T("{n} items came back", { n: f.hits || 0 }))}</span></div>
      ${f.error ? `<p class="rt-err">${esc(f.error)}</p>` : ""}
    </div>`;
  }

  function tabsHTML() {
    const tabs = [
      ["routes", "Route list"],
      ["agents", "Per-agent tech stacks"],
      ["swarm", "Swarm and conflict resolution"],
      ["schedule", "Scheduled searches"],
      ["hot", "Plugins at run time"]
    ];
    return `<nav class="rt-tabs" role="tablist" aria-label="${esc(T("Route list"))}">${tabs
      .map(
        ([id, word]) =>
          `<button type="button" role="tab" data-rtab="${id}" aria-selected="${st.tab === id}" class="${st.tab === id ? "on" : ""}">${esc(T(word))}</button>`
      )
      .join("")}</nav>`;
  }

  // ---------- views -----------------------------------------------------

  function drawRoutes() {
    if (!st.routes.length) return `<p class="rt-empty">${esc(T("Reading the routes…"))}</p>`;
    const cards = st.routes.map((r) => {
      const tone = routeTone(r);
      const action = st.probe[r.id];
      const keys = (r.keyEnvs || []).length
        ? `<span class="rt-keys">${esc(r.keyEnvs.join(" · "))}</span>`
        : "";
      return `<article class="rt-card" data-route="${esc(r.id)}">
        <header>
          ${dot(tone)}
          <div class="rt-who"><span class="rt-name">${esc(r.name || r.id)}</span>
            <span class="rt-sub">${esc(r.kind === "marketplace" ? T("The agent's own folders") : r.channel)}</span></div>
          <span class="rt-chip">${esc(r.kind === "marketplace" ? T("Runs without a key") : r.configured ? T("Its keys are set") : T("No keys are set"))}</span>
          ${r.keyed ? `<span class="rt-chip">${esc(T("Needs its own key"))}</span>` : ""}
          ${!r.enabled ? `<span class="rt-chip off">${esc(T("Off"))}</span>` : ""}
          ${r.detail ? `<span class="rt-chip">${esc(r.detail)}</span>` : ""}
          <button type="button" class="text rt-test" data-test="${esc(r.id)}" ${action ? "disabled" : ""}>${esc(action ? T("Testing…") : T("Check this route"))}</button>
        </header>
        ${keys}
        ${flowHTML(r)}
      </article>`;
    });
    return `<section class="rt-list">${cards.join("")}</section>`;
  }

  function stackCard(s) {
    const draft = st.draft[s.agent] || new Set(s.routes || []);
    const groups = {};
    for (const r of st.routes) {
      const g = r.kind === "marketplace" ? "marketplace" : "engine";
      (groups[g] ||= []).push(r);
    }
    const chips = (list) =>
      list
        .map(
          (r) =>
            `<button type="button" class="rt-toggle${draft.has(r.id) ? " on" : ""}" data-stack="${esc(s.agent)}" data-route="${esc(r.id)}" aria-pressed="${draft.has(r.id)}">${esc(r.name || r.id)}</button>`
        )
        .join("");
    return `<article class="rt-card">
      <header>${dot((s.routes || []).length ? "ok" : "warn")}
        <div class="rt-who"><span class="rt-name">${esc(s.name || s.agent)}</span>
          <span class="rt-sub">${esc(s.source === "user" ? T("Set by you") : T("Derived from this computer"))}</span></div>
        ${(s.evidence || []).length ? `<span class="rt-keys">${esc(s.evidence.join(" · "))}</span>` : ""}
        <button type="button" class="text rt-save" data-save="${esc(s.agent)}">${esc(T("Save stack"))}</button>
      </header>
      <div class="rt-group"><span class="rt-gl">${esc(T("Route list"))}</span>${chips(groups.engine || [])}</div>
      <div class="rt-group"><span class="rt-gl">${esc(T("The agent's own folders"))}</span>${chips(groups.marketplace || [])}</div>
    </article>`;
  }

  function drawAgents() {
    if (!st.stacks.length) return `<p class="rt-empty">${esc(T("No agent on this computer has a route stack yet."))}</p>`;
    return `<p class="rt-say">${esc(T("Which routes each agent uses. A stack is derived from the agent's own folders until you set one."))}</p>
      <section class="rt-list">${st.stacks.map(stackCard).join("")}</section>`;
  }

  function drawSwarm() {
    const res = st.swarm;
    const agents = st.stacks.filter((s) => (s.routes || []).length);
    const chips = agents
      .map(
        (s) =>
          `<button type="button" class="rt-toggle${st.selected.has(s.agent) ? " on" : ""}" data-swarm-agent="${esc(s.agent)}" aria-pressed="${st.selected.has(s.agent)}">${esc(s.name || s.agent)}</button>`
      )
      .join("");
    let out = `<p class="rt-say">${esc(T("Several agents search the same topic; each page is kept once and each claim at one page."))}</p>
      <div class="rt-form">
        <label class="rt-field"><span>${esc(T("Words to look for"))}</span><input id="rtQuery" type="text" value="${esc(st.swarmQuery)}" autocomplete="off"></label>
        <span class="rt-field"><span>${esc(T("Mode of this search"))}</span><span id="rtMode"></span></span>
        <button type="button" class="text primary" id="rtRun" ${st.swarmBusy ? "disabled" : ""}>${esc(st.swarmBusy ? T("The swarm is running…") : T("Run the swarm"))}</button>
      </div>
      <div class="rt-group"><span class="rt-gl">${esc(T("Agents"))}</span>${chips || `<span class="rt-empty">${esc(T("No agent on this computer has a route stack yet."))}</span>`}</div>`;
    if (!res) return out;
    const runs = (res.runs || [])
      .map(
        (r) => `<div class="rt-run">${dot(r.error ? "bad" : r.citations && r.citations.length ? "ok" : "warn")}
          <span class="rt-name">${esc(r.agent)}</span>
          <span class="rt-sub">${esc(r.error || (r.verdict || "") + (r.latencyMs ? " · " + T("Its last request took {ms} ms", { ms: r.latencyMs }) : ""))}</span></div>`
      )
      .join("");
    const cites = (res.resolution.citations || [])
      .slice(0, 24)
      .map(
        (c) => `<li><a href="${esc(c.url)}" target="_blank" rel="noopener noreferrer">${esc(c.title || c.url)}</a>
          <span class="rt-sub">${esc((c.agents || []).join(" · "))}${(c.routes || []).length ? " · " + esc((c.routes || []).join(" · ")) : ""}</span></li>`
      )
      .join("");
    const conflicts = (res.resolution.conflicts || [])
      .map(
        (c) => `<li><span class="rt-name">${esc(c.claim)}</span>
          <span class="rt-sub">${esc(c.winner)} ${esc("≠")} ${esc((c.losers || []).join(" · "))}</span></li>`
      )
      .join("");
    out += `<section class="rt-result">
      ${runs}
      <div class="rt-meta-row">
        <span class="rt-chip">${esc(T("Pages merged"))}: ${esc(res.resolution.duplicates || 0)}</span>
        <span class="rt-chip">${esc(T("Claims in conflict"))}: ${esc((res.resolution.conflicts || []).length)}</span>
      </div>
      ${cites ? `<ul class="rt-cites">${cites}</ul>` : `<p class="rt-empty">${esc(T("No request has run yet"))}</p>`}
      ${conflicts ? `<h4>${esc(T("Claims in conflict"))}</h4><ul class="rt-cites">${conflicts}</ul>` : `<p class="rt-sub">${esc(T("No claim is in conflict"))}</p>`}
    </section>`;
    return out;
  }

  function scheduleRow(s) {
    const next = s.paused ? T("Its schedule is paused") : s.nextRunAt ? s.nextRunAt.replace("T", " ").slice(0, 19) : T("It has never run");
    return `<article class="rt-card">
      <header>${dot(s.paused ? "off" : s.lastError ? "bad" : "ok")}
        <div class="rt-who"><span class="rt-name">${esc(s.name || s.query)}</span>
          <span class="rt-sub">${esc(s.query)} · ${esc(T("Repeat every"))} ${esc(s.everySec)} ${esc(T("seconds between runs"))}</span></div>
        <span class="rt-chip">${esc(T("Times run"))}: ${esc(s.runs || 0)}</span>
        ${s.lastVerdict ? `<span class="rt-chip">${esc(T("Its last verdict"))}: ${esc(s.lastVerdict)}</span>` : ""}
        ${s.lastLatencyMs ? `<span class="rt-chip">${esc(T("Its last request took {ms} ms", { ms: s.lastLatencyMs }))}</span>` : ""}
        <span class="rt-chip">${esc(T("Its next run"))}: ${esc(next)}</span>
      </header>
      ${s.lastError ? `<p class="rt-err">${esc(s.lastError)}</p>` : ""}
      <div class="rt-actions">
        <button type="button" class="text" data-sched="pause" data-id="${esc(s.id)}" ${s.paused ? "hidden" : ""}>${esc(T("Pause the schedule"))}</button>
        <button type="button" class="text" data-sched="resume" data-id="${esc(s.id)}" ${s.paused ? "" : "hidden"}>${esc(T("Resume"))}</button>
        <button type="button" class="text" data-sched="run" data-id="${esc(s.id)}">${esc(T("Run it now"))}</button>
        <button type="button" class="text" data-sched="cancel" data-id="${esc(s.id)}">${esc(T("Cancel"))}</button>
      </div>
    </article>`;
  }

  function drawSchedule() {
    return `<p class="rt-say">${esc(T("Run this search again on its interval, through the engine."))}</p>
      <div class="rt-form">
        <label class="rt-field"><span>${esc(T("Words to look for"))}</span><input id="rtSchedQuery" type="text" value="${esc(st.scheduleQuery)}" autocomplete="off"></label>
        <label class="rt-field"><span>${esc(T("Repeat every"))}</span><input id="rtSchedEvery" type="number" min="1" step="1" value="${esc(st.scheduleEvery)}"> <span>${esc(T("seconds between runs"))}</span></label>
        <span class="rt-field"><span>${esc(T("Mode of this search"))}</span><span id="rtSchedMode"></span></span>
        <button type="button" class="text primary" id="rtSchedAdd" ${st.scheduleBusy ? "disabled" : ""}>${esc(T("Schedule this search"))}</button>
      </div>
      <section class="rt-list">${st.schedules.map(scheduleRow).join("") || `<p class="rt-empty">${esc(T("No request has run yet"))}</p>`}</section>`;
  }

  function hotRow(p) {
    const busy = st.hotBusy[p.id];
    const tone = p.state === "active" ? "ok" : p.state === "failed" ? "bad" : p.state === "pending" ? "warn" : "off";
    const self = p.id === "routes";
    const loadable = p.state !== "active";
    return `<article class="rt-card">
      <header>${dot(tone)}
        <div class="rt-who"><span class="rt-name">${esc(p.name || p.id)}</span>
          <span class="rt-sub">${esc(p.id)} · ${esc(T(p.state === "active" ? "The plugin is loaded" : p.state === "pending" ? "Waiting for a service" : p.state === "failed" ? "Failed" : "The plugin is unloaded"))}${p.revision ? " · rev " + esc(p.revision) : ""}</span></div>
        ${(p.parts || []).map((part) => `<span class="rt-chip">${esc(part.Key || part.key || "")}</span>`).join("")}
        ${p.inUse ? `<span class="rt-chip">${esc(T("In use"))}: ${esc(p.use ? T(p.use, { n: p.inUse }) : p.inUse)}</span>` : ""}
        ${p.why ? `<span class="rt-chip">${esc(p.why)}</span>` : ""}
        <div class="rt-actions">
          <button type="button" class="text" data-hot="load" data-id="${esc(p.id)}" ${busy || !loadable ? "disabled" : ""}>${esc(busy === "load" ? T("Testing…") : T("Load the plugin"))}</button>
          <button type="button" class="text" data-hot="unload" data-id="${esc(p.id)}" ${busy || p.inUse || loadable || self ? "disabled" : ""}>${esc(busy === "unload" ? T("Testing…") : T("Unload the plugin"))}</button>
          <button type="button" class="text" data-hot="reload" data-id="${esc(p.id)}" ${busy || self || loadable ? "disabled" : ""} title="${esc(self ? T("This plugin is the page you are on: it cannot reload itself.") : T("Reload re-runs the plugin and fetches its client files again."))}">${esc(busy === "reload" ? T("Testing…") : T("Reload the plugin"))}</button>
        </div>
      </header>
      ${p.client && p.client.length ? `<span class="rt-keys">${esc(p.client.join(" · "))}${p.clientVersion ? " · " + esc(p.clientVersion) : ""}</span>` : ""}
    </article>`;
  }

  function drawHot() {
    const source = T(st.assetsFromDisk ? "the files on disk (a dev build)" : "the app's own files (a shipped build)");
    return `<p class="rt-say">${esc(T("Load, unload and reload a built-in plugin without restarting MolaSearch."))}</p>
      <p class="rt-say">${esc(T("Client files are served from {source}.", { source }))}</p>
      ${st.hotError ? `<p class="rt-err">${esc(st.hotError)}</p>` : ""}
      <section class="rt-list">${st.hot.map(hotRow).join("")}</section>`;
  }

  function draw() {
    if (!page) return;
    const body = page.querySelector("#rtBody");
    if (!body) return;
    let html = "";
    switch (st.tab) {
      case "agents": html = drawAgents(); break;
      case "swarm": html = drawSwarm(); break;
      case "schedule": html = drawSchedule(); break;
      case "hot": html = drawHot(); break;
      default: html = drawRoutes(); break;
    }
    const tabs = page.querySelector("#rtTabs");
    if (tabs) tabs.innerHTML = tabsHTML();
    body.innerHTML = (st.error ? `<p class="rt-err">${esc(T("Could not read the routes: {error}", { error: st.error }))}</p>` : "") + html;
    bind();
    mountMode();
  }

  // ---------- controls --------------------------------------------------

  function bind() {
    for (const b of page.querySelectorAll("[data-rtab]")) b.onclick = () => { st.tab = b.dataset.rtab; draw(); };
    for (const b of page.querySelectorAll("[data-test]")) b.onclick = () => probe(b.dataset.test);
    for (const b of page.querySelectorAll("[data-stack]")) {
      b.onclick = () => {
        const agent = b.dataset.stack;
        const set = (st.draft[agent] ||= new Set((st.stacks.find((s) => s.agent === agent) || {}).routes || []));
        if (set.has(b.dataset.route)) set.delete(b.dataset.route); else set.add(b.dataset.route);
        draw();
      };
    }
    for (const b of page.querySelectorAll("[data-save]")) b.onclick = () => saveStack(b.dataset.save);
    for (const b of page.querySelectorAll("[data-swarm-agent]")) {
      b.onclick = () => {
        if (st.selected.has(b.dataset.swarmAgent)) st.selected.delete(b.dataset.swarmAgent);
        else st.selected.add(b.dataset.swarmAgent);
        draw();
      };
    }
    for (const b of page.querySelectorAll("[data-sched]")) b.onclick = () => scheduleAction(b.dataset.sched, b.dataset.id);
    for (const b of page.querySelectorAll("[data-hot]")) b.onclick = () => hotAction(b.dataset.id, b.dataset.hot);
    const q = page.querySelector("#rtQuery");
    if (q) q.oninput = () => { st.swarmQuery = q.value; };
    const run = page.querySelector("#rtRun");
    if (run) run.onclick = runSwarm;
    const sq = page.querySelector("#rtSchedQuery");
    if (sq) sq.oninput = () => { st.scheduleQuery = sq.value; };
    const se = page.querySelector("#rtSchedEvery");
    if (se) se.oninput = () => { st.scheduleEvery = Math.max(1, Number(se.value) || 1); };
    const add = page.querySelector("#rtSchedAdd");
    if (add) add.onclick = addSchedule;
  }

  // mountMode draws the app's own menu, never a native select.
  function mountMode() {
    const host = page.querySelector("#rtMode");
    const mode = st.tab === "schedule" ? st.scheduleMode : st.swarmMode;
    if (host && kit && kit.menu) {
      host.replaceChildren(kit.menu(MODES, mode, T("Mode of this search"), (v) => {
        if (st.tab === "schedule") st.scheduleMode = v; else st.swarmMode = v;
      }));
    }
    const shost = page.querySelector("#rtSchedMode");
    if (shost && kit && kit.menu) {
      shost.replaceChildren(kit.menu(MODES, st.scheduleMode, T("Mode of this search"), (v) => { st.scheduleMode = v; }));
    }
  }

  async function probe(id) {
    st.probe[id] = true;
    draw();
    try {
      const j = await api("routes/probe", { id });
      if (j.flow) st.flows[j.flow.routeId] = j.flow;
      if (j.routes) st.routes = j.routes;
      if (j.error) status(j.error, "err");
    } catch (err) {
      status(T("The action failed: {error}", { error: err.message }), "err");
    }
    st.probe[id] = false;
    draw();
  }

  async function saveStack(agent) {
    const routes = [...(st.draft[agent] || new Set())];
    try {
      const j = await api("routes/stack", { agent, routes });
      applyRoutes(j);
      status(T("Save"), "ok", 1500);
    } catch (err) {
      status(T("The action failed: {error}", { error: err.message }), "err");
    }
    draw();
  }

  async function runSwarm() {
    if (!st.swarmQuery.trim()) return;
    st.swarmBusy = true;
    draw();
    try {
      st.swarm = await api("routes/swarm", {
        query: st.swarmQuery,
        modeId: st.swarmMode,
        agents: [...(st.selected || new Set())]
      });
      if (st.swarm && st.swarm.routes) st.routes = st.swarm.routes;
    } catch (err) {
      status(T("The action failed: {error}", { error: err.message }), "err");
    }
    st.swarmBusy = false;
    draw();
  }

  async function addSchedule() {
    if (!st.scheduleQuery.trim()) return;
    st.scheduleBusy = true;
    draw();
    try {
      const j = await api("routes/schedules", {
        action: "add", query: st.scheduleQuery, modeId: st.scheduleMode, everySec: st.scheduleEvery
      });
      st.schedules = j.schedules || [];
      st.scheduleQuery = "";
    } catch (err) {
      status(T("The action failed: {error}", { error: err.message }), "err");
    }
    st.scheduleBusy = false;
    draw();
  }

  async function scheduleAction(action, id) {
    try {
      const j = await api("routes/schedules", { action, id });
      st.schedules = j.schedules || [];
    } catch (err) {
      status(T("The action failed: {error}", { error: err.message }), "err");
    }
    draw();
  }

  async function hotAction(id, action) {
    st.hotBusy[id] = action;
    st.hotError = "";
    draw();
    try {
      const j = await api("hotplug", { id, action });
      applyHot(j);
      const p = st.hot.find((x) => x.id === id);
      if (action === "unload") {
        if (window.magpie && magpie.unload) magpie.unload(id);
      } else if (p && p.client && p.client.length && window.magpie && magpie.reload) {
        // the client files as the server has them now, then the config the
        // runtime holds (the script registered with boot.js's copy)
        await magpie.reload(id, p.client, p.clientVersion);
        if (p.config && magpie.configure) magpie.configure(id, p.config);
      }
      status(T(action === "load" ? "The plugin is loaded" : action === "unload" ? "The plugin is unloaded" : "The plugin reloaded"), "ok", 1500);
    } catch (err) {
      st.hotError = err.message;
      status(T("The action failed: {error}", { error: err.message }), "err");
    }
    st.hotBusy[id] = "";
    draw();
  }

  // ---------- the page --------------------------------------------------

  magpie.plugin("routes", (ctx) => {
    kit = ctx.ui;
    ctx.slots.add("page", {
      key: "routes",
      title: "Search routes",
      html: `
        <div class="rt-wrap">
          <header class="rt-hero">
            <h1 class="rt-title" data-t>Search routes</h1>
            <p class="rt-hero-sub" data-t>Every reliable path, the state of its keys, and the last real request</p>
          </header>
          <div id="rtTabs"></div>
          <div id="rtBody" class="rt-body"></div>
        </div>`,
      open() {
        page = document.getElementById("view-routes");
        return load();
      },
      refresh() {
        return load();
      },
      relang() {
        draw();
      },
      leave() {}
    });
  });
})();
