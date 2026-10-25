// The window's plugin kernel (docs/subsystems/plugin-runtime.md, P1). A
// plugin's page is a part it adds to the "page" slot: its markup, put in the
// page where index.html's <template data-slot="page"> is, and its hooks,
// which the shell calls through magpie.call. A hook that throws breaks only
// its own page: the page says so, with a Try again, and the other pages go
// on (the error boundary). ctx.ui is the kit a plugin draws with, and
// ui.form draws a plugin's settings from the schema the Go side made of
// them (pluginrt.SchemaOf).
//
// Loaded after i18n.js and before the plugins' scripts and app.js. A
// plugin's words come with it (its i18n.js, magpie.strings). Its scripts
// register at once, so its page is in the page before app.js looks for
// it, but run nothing of app.js's then: mount runs at magpie.start, which
// app.js calls once it is set up.
(() => {
  const plugins = new Map(); // id → { id, config, onConfig: [], disposers: [] }
  const pages = new Map(); // key → { plugin, part, main, mounted, broken }
  // A plugin turned off at run time leaves the window: its pages, the words
  // its i18n.js added, and those pages' nav entries. Turned back on, its
  // client files are fetched again (their list came with boot.js).
  const pluginWords = new Map(); // id → [{ lang, key }]
  const removedNav = new Map(); // id → [page key]
  const unloaded = new Set(); // ids whose client code is not registered now
  let started = false;

  // scriptPlugin is the plugin whose client file is running: a plugin's
  // i18n.js is served from plugins/<id>/, so its words are known by id and
  // can be taken back out when it is unloaded.
  function scriptPlugin() {
    const src = (document.currentScript && document.currentScript.src) || "";
    const m = src.match(/\/plugins\/([A-Za-z0-9_.-]+)\//);
    return m ? m[1] : "";
  }

  // navButton is the nav entry a plugin page added.
  const navButton = (key) => document.querySelector(`#nav button[data-view="${key}"]`);

  function context(p) {
    return {
      id: p.id,
      get config() { return p.config; },
      // a change of the plugin's settings (Plugins › Built-in), after
      // ctx.config has it
      onConfig(fn) { p.onConfig.push(fn); },
      onDispose(fn) { p.disposers.push(fn); },
      // a page of the plugin's that broke after its hook returned (a
      // frame's own error, one that never answered): it shows what broke,
      // with a Try again, as a hook that throws does
      broke(key, err) {
        const page = pages.get(key);
        if (page && page.plugin === p && !page.broken) fail(page, "open", [], err);
      },
      slots: { add: (slot, part) => add(p, slot, part) },
      ui,
    };
  }

  function add(p, slot, part) {
    if (slot !== "page") throw new Error(`no slot ${slot}`);
    if (!/^[a-z][a-z0-9-]*$/.test(part.key || "") || pages.has(part.key) || document.getElementById("view-" + part.key)) {
      throw new Error(`page ${part.key} is taken`);
    }
    const main = document.createElement("main");
    main.className = "view";
    main.id = "view-" + part.key;
    main.hidden = true;
    main.dataset.feature = p.id;
    main.innerHTML = part.html || "";
    window.magpie?.fillSlots?.(main);
    const anchor = document.querySelector('template[data-slot="page"]');
    if (anchor) anchor.before(main);
    else document.body.append(main);
    const page = { plugin: p, part, main, mounted: false, broken: null };
    pages.set(part.key, page);
    if (started) run(page, "mount", [main]);
  }

  // The error boundary: a hook that throws (or whose promise fails) is
  // caught here, and the page shows what broke instead of what it had.
  function fail(page, hook, args, err) {
    console.error(`[magpie] ${page.plugin.id}: ${page.part.key}.${hook}`, err);
    page.broken = { hook, args };
    const card = document.createElement("div");
    card.className = "list slot-error";
    card.setAttribute("role", "alert");
    const row = document.createElement("div");
    row.className = "row";
    const who = document.createElement("div");
    who.className = "who";
    const name = document.createElement("div");
    name.className = "name";
    name.textContent = t("This page stopped working");
    const sub = document.createElement("div");
    sub.className = "sub";
    sub.textContent = String(err?.message || err);
    who.append(name, sub);
    const again = document.createElement("button");
    again.type = "button";
    again.className = "text slot-again";
    again.textContent = t("Try again");
    again.onclick = () => retry(page);
    row.append(who, again);
    card.append(row);
    page.main.classList.remove("loading");
    page.main.removeAttribute("aria-busy");
    page.main.replaceChildren(card);
  }

  // Try again: the page's markup as it came, mounted again, and the hook
  // that broke run again with what it was given.
  function retry(page) {
    const { hook, args } = page.broken;
    page.broken = null;
    page.mounted = false;
    page.main.innerHTML = page.part.html || "";
    window.magpie?.fillSlots?.(page.main);
    window.translateStatic?.();
    if (hook !== "mount") run(page, "mount", [page.main]);
    run(page, hook, args);
  }

  function run(page, hook, args) {
    if (page.broken) return undefined;
    if (hook === "mount") {
      if (page.mounted) return undefined;
      page.mounted = true;
    } else if (!page.mounted && started) {
      run(page, "mount", [page.main]);
      if (page.broken) return undefined;
    }
    const fn = page.part[hook];
    if (typeof fn !== "function") return undefined;
    try {
      const out = fn.apply(page.part, args);
      if (out && typeof out.then === "function") {
        return out.catch((err) => { if (!page.broken) fail(page, hook, args, err); });
      }
      return out;
    } catch (err) {
      fail(page, hook, args, err);
      return undefined;
    }
  }

  // ---------- the kit ----------
  // What a plugin draws with, as the rest of the window draws: el, the app
  // menu (never a native <select>), the switch, the settings row.
  const ui = {
    el: (tag, cls, text) => el(tag, cls, text),
    button(label, onClick, cls = "text") {
      const b = el("button", cls, label);
      b.type = "button";
      b.onclick = onClick;
      return b;
    },
    toggle(on, label, onChange) {
      const b = el("button", "lib-switch use-credits" + (on ? " on" : ""));
      b.type = "button";
      b.setAttribute("role", "switch");
      b.setAttribute("aria-checked", String(on));
      b.setAttribute("aria-label", label);
      b.append(el("i"));
      b.onclick = () => {
        on = !on;
        b.classList.toggle("on", on);
        b.setAttribute("aria-checked", String(on));
        onChange(on);
      };
      return b;
    },
    // a pick of one of opts ([{ v, name }]), in the app's menu
    menu(opts, value, head, onPick) {
      const b = el("button", "proto pick");
      b.type = "button";
      const paint = () => {
        const o = opts.find((x) => x.v === value);
        b.replaceChildren(el("span", "", o ? o.name : String(value)), svg(CHEV, 11, 1.6));
      };
      paint();
      b.onclick = (ev) => {
        ev.stopPropagation();
        openProtoMenu(b, opts.map((o) => ({ ...o, literalName: true, note: "" })), value, (v) => {
          value = v;
          paint();
          onPick(v);
        }, head, "", "right");
      };
      return b;
    },
    // a settings row: what it is, and its control at the end
    row(title, description, control) {
      const r = el("div", "row pref");
      const who = el("div", "who");
      who.append(el("div", "name", title));
      if (description) who.append(el("div", "sub", description));
      r.append(who, control);
      return r;
    },
    // The rows of a plugin's settings, drawn from its schema: true or false
    // a switch, one of a few a menu, a number a field kept to its range,
    // text a field. onChange gets { key: value } for the one changed.
    form(schema, values, onChange) {
      const rows = [];
      for (const key of schema?.["x-order"] || Object.keys(schema?.properties || {})) {
        const p = schema.properties[key];
        if (!p) continue;
        const value = values?.[key] ?? p.default;
        const title = t(p.title), say = p.description ? t(p.description) : "";
        let control;
        if (p.type === "boolean") control = ui.toggle(!!value, title, (v) => onChange({ [key]: v }));
        else if (p.enum?.length) {
          control = ui.menu(p.enum.map((v) => ({ v, name: typeof v === "number" ? v.toLocaleString(locale) : t(String(v)) })), value, p.title, (v) => onChange({ [key]: v }));
        } else {
          const number = p.type === "integer" || p.type === "number";
          control = el("input", "set-field");
          control.type = number ? "number" : "text";
          control.value = value ?? "";
          control.setAttribute("aria-label", title);
          if (number) {
            if (p.minimum !== undefined) control.min = p.minimum;
            if (p.maximum !== undefined) control.max = p.maximum;
            if (p.type === "integer") control.step = 1;
          }
          control.onchange = () => {
            let v = control.value;
            if (number) {
              v = Number(v);
              const bad = control.value === "" || !Number.isFinite(v) || (p.type === "integer" && !Number.isInteger(v))
                || (p.minimum !== undefined && v < p.minimum) || (p.maximum !== undefined && v > p.maximum);
              control.setAttribute("aria-invalid", String(bad));
              if (bad) return;
            }
            onChange({ [key]: v });
          };
        }
        const r = ui.row(title, say, control);
        r.dataset.setting = key;
        rows.push(r);
      }
      return rows;
    },
  };

  // unload takes a plugin out of the window without a restart: its pages
  // leave the document (not only out of sight), its disposers run, and the
  // words its i18n.js added are taken back out. Its scripts stay loaded;
  // loadClient fetches them again when it is turned back on.
  function unload(id) {
    const p = plugins.get(id);
    const keys = [];
    for (const [key, page] of pages) {
      if (page.plugin === p || (p && page.plugin.id === id)) {
        // the page's own leave runs, as it does when the reader goes to
        // another page: its timers and frames stop
        if (page.part && typeof page.part.leave === "function") {
          try { page.part.leave(); } catch (err) { console.error(`[magpie] ${id}: leave`, err); }
        }
        page.main.remove();
        pages.delete(key);
        keys.push(key);
      }
    }
    if (p) {
      for (const fn of p.disposers) {
        try { fn(); } catch (err) { console.error(`[magpie] ${id}: dispose`, err); }
      }
      for (const { lang, key } of pluginWords.get(id) || []) delete (I18N[lang] || {})[key];
      pluginWords.delete(id);
      plugins.delete(id);
    }
    if (keys.length) removedNav.set(id, keys);
    for (const key of keys) { const b = navButton(key); if (b) b.hidden = true; }
    unloaded.add(id);
    return keys;
  }

  // loadClient fetches a plugin's client files as they are served now (the
  // version in the URL keeps a cache from answering with the old ones) and
  // runs them in order: its i18n.js first, then its client.js. The scripts
  // register the plugin again, as they did at page load.
  function loadClient(id, files, version) {
    if (!files || !files.length) {
      unloaded.delete(id);
      for (const key of removedNav.get(id) || []) { const b = navButton(key); if (b) b.hidden = false; }
      return Promise.resolve([]);
    }
    const v = version || String(Date.now());
    let chain = Promise.resolve();
    for (const file of files) {
      chain = chain.then(() => new Promise((resolve, reject) => {
        const url = "plugins/" + id + "/" + file + "?v=" + encodeURIComponent(v);
        if (/\.css$/.test(file)) {
          const old = document.querySelector(`link[data-plugin-css="${id}"]`);
          if (old) old.remove();
          const l = document.createElement("link");
          l.rel = "stylesheet"; l.href = url; l.dataset.pluginCss = id;
          l.onload = resolve;
          l.onerror = () => reject(new Error("could not load " + file));
          document.head.append(l);
          return;
        }
        const s = document.createElement("script");
        s.src = url; s.async = false;
        s.onload = resolve;
        s.onerror = () => reject(new Error("could not load " + file));
        document.body.append(s);
      }));
    }
    return chain.then(() => {
      unloaded.delete(id);
      for (const key of removedNav.get(id) || []) { const b = navButton(key); if (b) b.hidden = false; }
      return files;
    });
  }

  // clientFilesOf is a plugin's client files as boot.js knew them; the
  // hot-plug API's answer carries the same list with a current version.
  const clientFilesOf = (id) => ((window.bootPrefs && window.bootPrefs.pluginClients) || {})[id] || [];

  window.magpie = {
    ui,
    plugin(id, setup) {
      if (plugins.has(id)) throw new Error(`plugin ${id} is registered twice`);
      const p = { id, config: { ...(window.bootPrefs?.pluginConfig?.[id] || {}) }, onConfig: [], disposers: [] };
      plugins.set(id, p);
      try {
        setup(context(p));
      } catch (err) {
        console.error(`[magpie] ${id}: setup`, err);
      }
    },
    // app.js, once it is set up: every page is mounted
    start() {
      if (started) return;
      started = true;
      for (const page of pages.values()) run(page, "mount", [page.main]);
    },
    // a page's hook, behind the boundary; nothing for a page no plugin has
    call(key, hook, ...args) {
      const page = pages.get(key);
      return page ? run(page, hook, args) : undefined;
    },
    // a plugin's words in each language ({ zh: { English: 中文 }, … }),
    // added to i18n.js's; one already there is a mistake, said and not
    // taken, as the later one would silently win
    strings(dict) {
      const id = scriptPlugin();
      for (const [lang, words] of Object.entries(dict)) {
        const to = (I18N[lang] ||= {});
        for (const [k, v] of Object.entries(words)) {
          if (k in to) console.error(`[magpie] ${lang} ${JSON.stringify(k)} is said twice`);
          else {
            to[k] = v;
            if (id) {
              const list = pluginWords.get(id) || [];
              list.push({ lang, key: k });
              pluginWords.set(id, list);
            }
          }
        }
      }
    },
    page: (key) => pages.get(key)?.part,
    pages: () => [...pages.keys()],
    // a plugin turned off at run time leaves the window (see unload above)
    unload,
    loadClient,
    has: (id) => plugins.has(id),
    pagesOf(id) {
      const out = [];
      for (const [key, page] of pages) if (page.plugin && page.plugin.id === id) out.push(key);
      return out;
    },
    // reload takes a plugin's pages out and fetches its client files again.
    // files and version are the hot-plug API's answer; without them the
    // list boot.js carried is used, with a fresh version for the cache.
    reload(id, files, version) {
      const keys = unload(id);
      return loadClient(id, files || clientFilesOf(id), version).then((loaded) => ({ keys, files: loaded }));
    },
    // setActive is the shell's switch: off unloads (its page and nav leave),
    // on loads a plugin whose code was unloaded.
    setActive(id, on) {
      if (!on) return unload(id);
      if (plugins.has(id)) return null;
      return loadClient(id, clientFilesOf(id), String(Date.now()));
    },
    // the plugins' settings as Plugins › Built-in or boot.js has them
    configure(id, config) {
      const p = plugins.get(id);
      if (!p || !config) return;
      if (JSON.stringify(p.config) === JSON.stringify(config)) return;
      p.config = { ...config };
      for (const fn of p.onConfig) {
        try { fn(p.config); } catch (err) { console.error(`[magpie] ${id}: config`, err); }
      }
    },
  };
})();
