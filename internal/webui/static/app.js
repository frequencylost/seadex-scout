// seadex-scout built-in dashboard. Plain JavaScript, no dependencies: the page
// polls /api/state and renders it. Every value from the log is untrusted, so it
// only ever reaches the page through textContent and http(s)-only links.
"use strict";

const REFRESH_MS = 30000;
const SEADEX = "https://seadex.moe/";
const HOUR = 3600e3;
const DAY = 24 * HOUR;

const state = {
  snap: null,
  tab: "overview",
  upgradesSort: { key: "rank", asc: true },
  report: null,
  reportVerdict: "",
};

// ---------- helpers ----------

const $ = (id) => document.getElementById(id);

function el(tag, props, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") node.className = v;
    else if (k === "text") node.textContent = v;
    else if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v === true ? "" : String(v));
  }
  for (const c of children.flat()) {
    if (c === undefined || c === null || c === false) continue;
    node.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return node;
}

function safeURL(u) {
  if (typeof u !== "string" || u === "") return null;
  try {
    const parsed = new URL(u);
    return parsed.protocol === "https:" || parsed.protocol === "http:" ? parsed.href : null;
  } catch {
    return null;
  }
}

function link(href, text, cls) {
  const url = safeURL(href);
  if (!url) return document.createTextNode(text);
  return el("a", { href: url, target: "_blank", rel: "noopener noreferrer", class: cls }, text);
}

function num(v) {
  const n = typeof v === "number" ? v : Number(v);
  return Number.isFinite(n) ? n : null;
}

function fmtBytes(b, signed) {
  const n = num(b);
  if (n === null) return "–";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = Math.abs(n);
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  const s = (v >= 100 || i === 0 ? v.toFixed(0) : v.toFixed(1)) + " " + units[i];
  if (!signed) return s;
  return (n > 0 ? "+" : n < 0 ? "−" : "") + s;
}

function ago(t, now) {
  if (!t) return "never";
  const ms = (now || Date.now()) - new Date(t).getTime();
  if (!Number.isFinite(ms)) return "–";
  const abs = Math.abs(ms);
  const units = [[DAY, "d"], [HOUR, "h"], [60e3, "m"], [1e3, "s"]];
  for (const [size, u] of units) {
    if (abs >= size) {
      const n = Math.floor(abs / size);
      return ms >= 0 ? `${n}${u} ago` : `in ${n}${u}`;
    }
  }
  return "just now";
}

function fmtTime(t) {
  if (!t) return "–";
  const d = new Date(t);
  if (Number.isNaN(d.getTime())) return "–";
  return d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

function fmtDuration(s) {
  // Go duration strings, such as "24m13.2s" or "1h2m0s".
  if (typeof s !== "string") return "–";
  const m = s.match(/^(?:(\d+)h)?(?:(\d+)m)?(?:([\d.]+)s)?$/);
  if (!m) return s;
  const h = Number(m[1] || 0), mi = Number(m[2] || 0), se = Number(m[3] || 0);
  if (h) return `${h}h ${mi}m`;
  if (mi) return `${mi}m ${Math.round(se)}s`;
  return `${se < 10 ? se.toFixed(1) : Math.round(se)}s`;
}

function durationSeconds(s) {
  if (typeof s !== "string") return null;
  let total = 0, matched = false;
  for (const [, v, u] of s.matchAll(/([\d.]+)(h|ms|m|s|µs|us|ns)/g)) {
    matched = true;
    const n = Number(v);
    total += u === "h" ? n * 3600 : u === "m" ? n * 60 : u === "s" ? n : u === "ms" ? n / 1e3 : 0;
  }
  return matched ? total : null;
}

function arrName(arr) {
  return arr === "radarr" ? "Radarr" : arr === "sonarr" ? "Sonarr" : arr || "";
}

function seasonLabel(a) {
  if (a.episodes) return a.episodes;
  const s = num(a.season);
  if (a.arr === "radarr") return "Film";
  if (s === null) return "";
  return s === 0 ? "Specials" : `Season ${s}`;
}

function setRows(tbody, rows, cols, emptyText) {
  tbody.replaceChildren(...(rows.length ? rows : [el("tr", {}, el("td", { colspan: cols, class: "empty", text: emptyText }))]));
}

function groupWithRevision(group, rev) {
  if (!group) return "–";
  return rev ? `${group} ${rev}` : group;
}

// ---------- theme and tabs ----------

function initTheme() {
  let saved = null;
  try { saved = localStorage.getItem("theme"); } catch { /* storage blocked */ }
  if (saved === "light" || saved === "dark") document.documentElement.dataset.theme = saved;
  $("theme-toggle").addEventListener("click", () => {
    const current = document.documentElement.dataset.theme ||
      (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
    const next = current === "dark" ? "light" : "dark";
    document.documentElement.dataset.theme = next;
    try { localStorage.setItem("theme", next); } catch { /* storage blocked */ }
  });
}

function showTab(name) {
  if (!document.getElementById("tab-" + name)) name = "overview";
  state.tab = name;
  for (const b of document.querySelectorAll(".tabs button")) {
    b.setAttribute("aria-selected", String(b.dataset.tab === name));
  }
  for (const p of document.querySelectorAll(".tab-panel")) p.hidden = p.id !== "tab-" + name;
  if (location.hash !== "#" + name) history.replaceState(null, "", "#" + name);
  if (name === "log") loadLogs();
  if (name === "reports") loadReports();
}

function initTabs() {
  for (const b of document.querySelectorAll(".tabs button")) {
    b.addEventListener("click", () => showTab(b.dataset.tab));
  }
  showTab(location.hash.slice(1) || "overview");
  window.addEventListener("hashchange", () => showTab(location.hash.slice(1)));
}

// ---------- data ----------

async function getJSON(url) {
  const res = await fetch(url, { headers: { Accept: "application/json" }, cache: "no-store" });
  if (!res.ok) throw new Error(`${url}: ${res.status}`);
  return res.json();
}

async function refresh() {
  try {
    state.snap = await getJSON("api/state");
    render();
  } catch (err) {
    const pill = $("status-pill");
    pill.className = "pill bad";
    pill.textContent = "Dashboard offline";
    $("subtitle").textContent = "Could not reach seadex-scout. Retrying…";
    console.error(err);
  }
}

function findings(snap, msg) {
  return (snap.findings || []).filter((f) => f.msg === msg);
}

function upgrades(snap) {
  return findings(snap, "better release available");
}

function checks(snap) {
  return (snap.findings || []).filter((f) => f.msg.endsWith("manual review"));
}

function infos(snap) {
  return (snap.findings || []).filter((f) =>
    f.msg !== "better release available" && !f.msg.endsWith("manual review"));
}

// ---------- attention ----------

function attentionItems(snap) {
  const now = new Date(snap.now).getTime();
  const started = new Date(snap.started_at).getTime();
  const items = [];

  const lastCheck = Math.max(
    snap.last_check ? new Date(snap.last_check.time).getTime() : 0,
    snap.reconcile_started ? new Date(snap.reconcile_started).getTime() : 0,
  );
  if (now - Math.max(lastCheck, started) > 3 * HOUR) {
    items.push({ cls: "bad", title: "Last check", text: `No check has finished or started in 3 hours (last ${ago(lastCheck || null, now)}).` });
  }
  const lastFull = snap.reconcile_complete ? new Date(snap.reconcile_complete).getTime() : 0;
  if (now - Math.max(lastFull, started) > 72 * HOUR) {
    items.push({ cls: "bad", title: "Last full check", text: `No daily full check has finished in 72 hours (last ${ago(lastFull || null, now)}).` });
  }
  const errors = recentErrors(snap);
  if (errors.length) {
    items.push({ cls: "warn", title: `Errors in 7 days: ${errors.length}`, text: errors[errors.length - 1].msg, tab: "health" });
  }
  for (const [cond, line] of Object.entries(snap.conditions || {})) {
    if (now - new Date(line.time).getTime() > HOUR) continue;
    const lib = cond.startsWith("library-");
    const arr = line.attrs && line.attrs.arr ? ` (${arrName(line.attrs.arr)})` : "";
    items.push({
      cls: lib ? "bad" : "info",
      title: lib ? `Library problem${arr}` : "Upstream outage",
      text: `${cond}: ${line.msg}${lib ? "" : ". Nothing to do; existing findings are kept."}`,
      tab: "health",
    });
  }
  const feed = snap.indexer && snap.indexer.last_snapshot;
  if (feed && num(feed.attrs && feed.attrs.ab_releases_skipped) > 0) {
    items.push({ cls: "warn", title: "AnimeBytes feed", text: `${feed.attrs.ab_releases_skipped} AnimeBytes releases held back: indexer.ab_passkey is not set.` });
  }
  return items;
}

function recentErrors(snap) {
  const now = new Date(snap.now).getTime();
  return (snap.errors || []).filter((e) => now - new Date(e.time).getTime() <= 7 * DAY);
}

// ---------- render: header ----------

function render() {
  const snap = state.snap;
  if (!snap) return;
  const items = attentionItems(snap);

  const pill = $("status-pill");
  if (items.some((i) => i.cls === "bad")) { pill.className = "pill bad"; pill.textContent = "Needs attention"; }
  else if (items.length) { pill.className = "pill warn"; pill.textContent = "Check health"; }
  else if (!snap.pass || !snap.pass.id) { pill.className = "pill info"; pill.textContent = "Waiting for first check"; }
  else { pill.className = "pill good"; pill.textContent = "Healthy"; }

  const sub = [];
  if (snap.pass && snap.pass.id) sub.push(`Findings from ${ago(snap.pass.at, new Date(snap.now).getTime())}`);
  else sub.push("No findings yet. The first full check can take 25 minutes or more");
  $("subtitle").textContent = sub.join(" · ");
  $("footer-text").textContent = `Updated ${fmtTime(snap.now)} · refreshes every ${REFRESH_MS / 1000}s`;

  const box = $("attention");
  box.hidden = items.length === 0;
  box.replaceChildren(...items.map((i) => {
    const node = el("div", { class: "alert " + i.cls }, el("strong", { text: i.title }), el("span", { text: i.text }));
    if (i.tab) { node.dataset.tab = i.tab; node.addEventListener("click", () => showTab(i.tab)); }
    return node;
  }));

  renderOverview(snap);
  renderActivity(snap);
  renderHealth(snap);
  if (state.tab === "reports") renderReportJob(snap.report_job);
}

// ---------- render: overview ----------

function whyOf(a) {
  if (a.status === "newer_revision") return { text: "Newer version", cls: "info" };
  if (a.current_tier === "alt") return { text: "SeaDex alt", cls: "good" };
  return { text: "Neither best nor alt", cls: "warn" };
}

function renderOverview(snap) {
  const now = new Date(snap.now).getTime();
  const showOptional = $("upgrades-optional").checked;
  const all = upgrades(snap);
  const visible = all.filter((f) => showOptional || f.attrs.current_tier !== "alt");

  $("kpi-upgrades").textContent = snap.pass && snap.pass.id ? visible.length : "–";
  const optional = all.length - all.filter((f) => f.attrs.current_tier !== "alt").length;
  $("kpi-upgrades-sub").textContent = optional ? `${optional} more optional (you have an alt)` : "better releases and newer versions";
  $("kpi-upgrades").parentElement.className = "kpi" + (visible.length ? " warn" : snap.pass && snap.pass.id ? " good" : "");

  const chk = checks(snap);
  $("kpi-checks").textContent = snap.pass && snap.pass.id ? chk.length : "–";

  const lib = snap.library && snap.library.attrs;
  if (lib && num(lib.anime_items)) {
    const pct = Math.round((100 * num(lib.items_all_best_or_alt)) / num(lib.anime_items));
    $("kpi-bestalt").textContent = pct + "%";
    $("kpi-bestalt-sub").textContent = `${lib.items_all_best_or_alt} of ${lib.anime_items} anime · ${lib.items_all_best} at best`;
  } else {
    $("kpi-bestalt").textContent = "–";
    $("kpi-bestalt-sub").textContent = "after the first full check";
  }

  const lc = snap.last_check;
  $("kpi-lastcheck").textContent = lc ? ago(lc.time, now) : "–";
  $("kpi-lastcheck-sub").textContent = lc
    ? (lc.msg.includes("degraded") ? `${lc.msg} (${lc.attrs.reason || "?"})` : lc.msg.startsWith("cycle") ? "full check" : "quick check")
    : "running…";
  $("kpi-lastcheck").parentElement.className = "kpi" + (lc && lc.msg.includes("degraded") ? " warn" : "");

  renderLibrary(snap, now);
  renderUpgrades(visible, now);
  renderChecks(chk, now);
  renderInfos(infos(snap));
}

const LIBRARY_BARS = [
  ["have_best", "At SeaDex best", "--bar-best"],
  ["have_alt", "At a SeaDex alt", "--bar-alt"],
  ["have_older_revision", "Older version of best", "--bar-older"],
  ["have_unlisted", "Neither best nor alt", "--bar-unlisted"],
  ["no_file", "Season not found", "--bar-other"],
  ["unverified", "Untagged release", "--bar-other"],
  ["unattributed", "Episode not known", "--bar-other"],
  ["withheld", "Withheld", "--bar-other"],
  ["not_on_seadex", "Not on SeaDex", "--bar-other"],
];

function renderLibrary(snap, now) {
  const lib = snap.library;
  const box = $("library");
  if (!lib) {
    $("library-asof").textContent = "";
    box.replaceChildren(el("p", { class: "empty", text: "Shown after the first daily full check." }));
    return;
  }
  $("library-asof").textContent = `From the full check ${ago(lib.time, now)} · ${lib.attrs.rows} SeaDex entries`;
  const max = Math.max(1, ...LIBRARY_BARS.map(([k]) => num(lib.attrs[k]) || 0));
  box.replaceChildren(...LIBRARY_BARS
    .filter(([k]) => lib.attrs[k] !== undefined)
    .map(([k, label, color]) => {
      const v = num(lib.attrs[k]) || 0;
      const fill = el("div", { class: "bar-fill" });
      fill.style.width = `${(100 * v) / max}%`;
      fill.style.background = `var(${color})`;
      return el("div", { class: "bar-row" },
        el("span", { class: "label", text: label, title: label }),
        el("div", { class: "bar-track" }, fill),
        el("span", { class: "num", text: v.toLocaleString() }));
    }));
}

function upgradeSortValue(f, key, showOptional) {
  const a = f.attrs;
  switch (key) {
    case "title": return (a.title || "").toLowerCase();
    case "why": return whyOf(a).text;
    case "current": return (a.current_group || "").toLowerCase();
    case "best": return (a.recommended_group || "").toLowerCase();
    case "size": return num(a.recommended_bytes) ?? -Infinity;
    case "change": return num(a.size_change_bytes) ?? -Infinity;
    case "first": return num(a.first_seen) ?? 0;
    default: {
      const r = num(showOptional ? a.rank_none : a.rank_alt);
      return r && r > 0 ? r : Infinity;
    }
  }
}

function renderUpgrades(list, now) {
  const q = $("upgrades-search").value.trim().toLowerCase();
  const arr = $("upgrades-arr").value;
  const showOptional = $("upgrades-optional").checked;
  const { key, asc } = state.upgradesSort;
  const rows = list
    .filter((f) => !arr || f.attrs.arr === arr)
    .filter((f) => !q || [f.attrs.title, f.attrs.current_group, f.attrs.recommended_group, f.attrs.alt_groups]
      .some((s) => typeof s === "string" && s.toLowerCase().includes(q)))
    .sort((x, y) => {
      const a = upgradeSortValue(x, key, showOptional), b = upgradeSortValue(y, key, showOptional);
      const c = a < b ? -1 : a > b ? 1 : 0;
      return asc ? c : -c;
    });

  $("upgrades-count").textContent = rows.length;
  for (const th of document.querySelectorAll("#upgrades th[data-sort]")) {
    th.classList.toggle("sorted", th.dataset.sort === key);
    th.classList.toggle("asc", th.dataset.sort === key && asc);
  }

  setRows($("upgrades").tBodies[0], rows.map((f) => {
    const a = f.attrs;
    const why = whyOf(a);
    const change = num(a.size_change_bytes);
    const links = [];
    if (a.nyaa_url) links.push(link(a.nyaa_url, "Nyaa"));
    if (a.public_url) links.push(link(a.public_url, a.public_tracker || "Tracker"));
    if (a.ab_url) links.push(link(a.ab_url, a.ab_tracker || "AnimeBytes"));
    if (!links.length && a.release_url) links.push(link(a.release_url, a.tracker || "Release"));
    const tags = a.seadex_tags ? el("span", { class: "sub", text: a.seadex_tags }) : null;
    return el("tr", {},
      el("td", {}, link(a.arr_url, a.title || "?"), el("span", { class: "sub", text: `${arrName(a.arr)} · ${seasonLabel(a)}` })),
      el("td", {}, el("span", { class: "tag " + why.cls, text: why.text })),
      el("td", { text: groupWithRevision(a.current_group, a.status === "newer_revision" ? a.current_revision : "") }),
      el("td", {}, link(a.al_id ? SEADEX + a.al_id + "/" : "", groupWithRevision(a.recommended_group, a.status === "newer_revision" ? a.recommended_revision : "")), tags),
      el("td", { text: a.alt_groups || "–" }),
      el("td", { class: "num", text: fmtBytes(a.recommended_bytes) }),
      el("td", { class: "num " + (change > 0 ? "pos" : change < 0 ? "neg" : ""), text: fmtBytes(a.size_change_bytes, true) }),
      el("td", {}, el("div", { class: "links" }, links)),
      el("td", { class: "num", text: a.first_seen ? ago(num(a.first_seen), now) : "–" }));
  }), 9, state.snap.pass && state.snap.pass.id ? "No upgrades. Your library is on SeaDex's picks." : "Waiting for the first check.");
}

function renderChecks(list, now) {
  $("checks-count").textContent = list.length;
  setRows($("checks").tBodies[0], list.map((f) => {
    const a = f.attrs;
    return el("tr", {},
      el("td", {}, link(a.arr_url, a.title || "?"), el("span", { class: "sub", text: `${arrName(a.arr)} · ${seasonLabel(a)}` })),
      el("td", { text: a.status === "mixed_group_manual" ? "Several release groups" : "Group could not be read" }),
      el("td", { text: a.current_group || "–" }),
      el("td", {}, link(a.al_id ? SEADEX + a.al_id + "/" : "", a.recommended_group || "–")),
      el("td", { text: a.alt_groups || "–" }),
      el("td", { class: "num", text: a.first_seen ? ago(num(a.first_seen), now) : "–" }));
  }), 6, "Nothing to check by hand.");
}

function renderInfos(list) {
  $("info-count").textContent = list.length;
  setRows($("infos").tBodies[0], list.map((f) => {
    const a = f.attrs;
    return el("tr", {},
      el("td", {}, link(a.arr_url, a.title || "?"), el("span", { class: "sub", text: `${arrName(a.arr)} · ${seasonLabel(a)}` })),
      el("td", { text: f.msg }),
      el("td", { text: a.current_group || "–" }),
      el("td", {}, link(a.al_id ? SEADEX + a.al_id + "/" : "", a.recommended_group || "–")));
  }), 4, "None.");
}

// ---------- render: activity ----------

function renderActivity(snap) {
  const now = new Date(snap.now).getTime();
  const events = (snap.events || []).slice().reverse();
  setRows($("events").tBodies[0], events.map((e) => {
    const a = e.attrs || {};
    const found = e.msg === "upgrade found";
    const label = found ? (a.after_start ? "Found at start" : "New upgrade") : "Resolved";
    return el("tr", {},
      el("td", { title: fmtTime(e.time), text: ago(e.time, now) }),
      el("td", {}, el("span", { class: "tag " + (found ? (a.after_start ? "" : "warn") : "good"), text: label })),
      el("td", {}, link(a.arr_url, a.title || "?"), el("span", { class: "sub", text: `${arrName(a.arr)} · ${seasonLabel(a)}` })),
      el("td", { text: a.current_group || "–" }),
      el("td", { text: a.recommended_group || "–" }));
  }), 5, "No upgrade has come or gone yet.");
}

// ---------- render: health ----------

function renderHealth(snap) {
  const now = new Date(snap.now).getTime();
  $("h-started").textContent = ago(snap.started_at, now).replace(" ago", "");
  $("h-started-sub").textContent = fmtTime(snap.started_at);

  const full = snap.last_full_check;
  $("h-full").textContent = snap.reconcile_complete ? ago(snap.reconcile_complete, now) : "–";
  $("h-full-sub").textContent = full
    ? `${full.msg}${full.attrs.duration ? " in " + fmtDuration(full.attrs.duration) : ""}${full.attrs.library_items ? " · " + full.attrs.library_items + " library items" : ""}`
    : "runs once a day";

  const errs = recentErrors(snap);
  $("h-errors").textContent = errs.length;
  $("h-errors").parentElement.className = "kpi" + (errs.length ? " warn" : " good");

  const ix = snap.indexer || {};
  $("h-indexer").textContent = (ix.requests || 0).toLocaleString();
  $("h-indexer-sub").textContent = ix.last_request ? `last ${ago(ix.last_request, now)}` : "indexer not used yet";

  const conds = Object.entries(snap.conditions || {}).sort((a, b) => b[1].time.localeCompare(a[1].time));
  $("conditions").replaceChildren(...(conds.length ? conds.map(([cond, line]) => {
    const active = now - new Date(line.time).getTime() <= HOUR;
    return el("div", { class: "alert " + (active ? (cond.startsWith("library-") ? "bad" : "info") : "cleared") },
      el("strong", { text: `${cond}${active ? "" : " (cleared)"}` }),
      el("span", { text: `${line.msg} · last logged ${ago(line.time, now)}` }));
  }) : [el("p", { class: "empty", text: "None. SeaDex, AniList, the ID map and your library reads are all fine." })]));

  const cfg = snap.config && snap.config.attrs;
  const kv = $("config");
  if (cfg) {
    const yes = (v) => (v === true ? "yes" : v === false ? "no" : String(v ?? "–"));
    kv.replaceChildren(...[
      ["Sonarr", yes(cfg.sonarr_enabled)], ["Radarr", yes(cfg.radarr_enabled)],
      ["Check interval", cfg.poll_interval], ["AnimeBytes", yes(cfg.animebytes)],
      ["Leave out remuxes", yes(cfg.exclude_remux)], ["Require dual audio", yes(cfg.require_dual_audio)],
      ["Leave out specials", yes(cfg.exclude_specials)], ["Ignored shows", cfg.ignored_findings],
      ["Include tags", cfg.include_tags], ["Exclude tags", cfg.exclude_tags],
    ].flatMap(([k, v]) => [el("dt", { text: k }), el("dd", { text: String(v ?? "–") })]));
  } else {
    kv.replaceChildren(el("dd", { text: "Not logged since this start." }));
  }

  renderCycles(snap.cycles || []);

  setRows($("errors").tBodies[0], (snap.errors || []).slice().reverse().map((e) => {
    const detail = Object.entries(e.attrs || {}).map(([k, v]) => `${k}=${typeof v === "string" ? v : JSON.stringify(v)}`).join("  ");
    return el("tr", {},
      el("td", { title: fmtTime(e.time), text: ago(e.time, now) }),
      el("td", { text: e.msg }),
      el("td", { class: "small", text: detail }));
  }), 3, "No errors logged.");

  setRows($("degraded").tBodies[0], (snap.degraded || []).slice().reverse().map((e) => el("tr", {},
    el("td", { title: fmtTime(e.time), text: ago(e.time, now) }),
    el("td", { text: e.msg.startsWith("cycle") ? "Full check" : "Quick check" }),
    el("td", { text: (e.attrs && e.attrs.reason) || "–" }))), 3, "No degraded checks.");

  const snapLine = ix.last_snapshot && ix.last_snapshot.attrs;
  const scopes = Object.entries(ix.by_scope || {}).map(([k, v]) => `${k}: ${v}`).join(", ");
  $("indexer").replaceChildren(...[
    ["Requests", (ix.requests || 0).toLocaleString()],
    ["RSS polls and tests", (ix.feed_requests || 0).toLocaleString()],
    ["Results returned", (ix.returned || 0).toLocaleString()],
    ["By tracker", scopes || "–"],
    ["Last request", ix.last_request ? fmtTime(ix.last_request) : "–"],
    ["Feed last rebuilt", ix.last_snapshot ? fmtTime(ix.last_snapshot.time) : "–"],
    ["Nyaa releases in feed", snapLine ? snapLine.nyaa_feed : "–"],
    ["AnimeBytes releases in feed", snapLine ? snapLine.ab_feed : "–"],
  ].flatMap(([k, v]) => [el("dt", { text: k }), el("dd", { text: String(v ?? "–") })]));
}

function renderCycles(cycles) {
  const box = $("cycles-chart");
  const pts = cycles.map((c) => ({ t: c.time, s: durationSeconds(c.attrs && c.attrs.duration), bad: c.msg === "cycle degraded" }))
    .filter((p) => p.s !== null);
  if (!pts.length) {
    box.replaceChildren(el("p", { class: "empty", text: "Shown after the first full check." }));
    return;
  }
  const ns = "http://www.w3.org/2000/svg";
  const W = 800, H = 160, padL = 44, padB = 20, padT = 8;
  const max = Math.max(60, ...pts.map((p) => p.s));
  const svg = document.createElementNS(ns, "svg");
  svg.setAttribute("viewBox", `0 0 ${W} ${H}`);
  svg.setAttribute("preserveAspectRatio", "none");
  const bw = (W - padL) / Math.max(pts.length, 12);
  pts.forEach((p, i) => {
    const h = ((H - padB - padT) * p.s) / max;
    const r = document.createElementNS(ns, "rect");
    r.setAttribute("x", padL + i * bw + 1);
    r.setAttribute("y", H - padB - h);
    r.setAttribute("width", Math.max(1, bw - 2));
    r.setAttribute("height", h);
    r.setAttribute("class", "bar" + (p.bad ? " degraded" : ""));
    const title = document.createElementNS(ns, "title");
    title.textContent = `${fmtTime(p.t)}: ${Math.round(p.s / 60)} min${p.bad ? " (degraded)" : ""}`;
    r.append(title);
    svg.append(r);
  });
  for (const frac of [0, 0.5, 1]) {
    const t = document.createElementNS(ns, "text");
    t.setAttribute("x", 0);
    t.setAttribute("y", H - padB - (H - padB - padT) * frac + 4);
    t.setAttribute("class", "axis");
    t.textContent = `${Math.round((max * frac) / 60)}m`;
    svg.append(t);
  }
  box.replaceChildren(svg);
}

// ---------- reports ----------

function renderReportJob(job) {
  const btn = $("report-run");
  const label = $("report-job");
  if (!job) return;
  btn.disabled = job.running;
  if (job.running) {
    label.textContent = `Report running since ${fmtTime(job.started_at)}…`;
  } else if (job.finished_at) {
    label.textContent = `Last run finished ${ago(job.finished_at)}${job.error ? " with an error: " + job.error : ""}${job.result ? " · " + job.result : ""}`;
  } else {
    label.textContent = "";
  }
}

async function loadReports() {
  try {
    const data = await getJSON("api/reports");
    renderReportJob(data.job);
    setRows($("reports").tBodies[0], data.reports.map((r) => el("tr", {},
      el("td", { text: r.stem }),
      el("td", { title: fmtTime(r.modified), text: `${fmtTime(r.modified)} (${ago(r.modified)})` }),
      el("td", { class: "num", text: fmtBytes(r.size) }),
      el("td", {}, el("div", { class: "links" },
        r.json ? el("a", { href: "#reports", onclick: (ev) => { ev.preventDefault(); openReport(r.stem); }, text: "View" }) : null,
        r.markdown ? el("a", { href: `api/reports/${encodeURIComponent(r.stem)}.md?download`, text: "Markdown" }) : null,
        r.json ? el("a", { href: `api/reports/${encodeURIComponent(r.stem)}.json?download`, text: "JSON" }) : null)),
    )), 4, "No reports yet. Run one to compare every season with SeaDex.");
    if (data.job && data.job.running) setTimeout(() => { if (state.tab === "reports") loadReports(); }, 15000);
  } catch (err) {
    setRows($("reports").tBodies[0], [], 4, "Could not read the report folder.");
    console.error(err);
  }
}

async function runReport() {
  const btn = $("report-run");
  btn.disabled = true;
  try {
    const res = await fetch("api/reports/run", { method: "POST", headers: { "X-Seadex-Scout": "1" } });
    if (!res.ok && res.status !== 409) throw new Error(await res.text());
  } catch (err) {
    $("report-job").textContent = "Could not start the report: " + err.message;
  }
  loadReports();
}

const VERDICTS = {
  have_best: ["At SeaDex best", "good"],
  have_alt: ["At a SeaDex alt", "good"],
  have_older_revision: ["Older version", "warn"],
  have_unlisted: ["Neither best nor alt", "warn"],
  no_file: ["Season not found", ""],
  unverified: ["Untagged release", ""],
  unattributed: ["Episode not known", ""],
  withheld: ["Withheld", ""],
  not_on_seadex: ["Not on SeaDex", ""],
};

async function openReport(stem) {
  const view = $("report-view");
  view.hidden = false;
  $("report-title").textContent = stem;
  $("report-rows").tBodies[0].replaceChildren(el("tr", {}, el("td", { colspan: 6, class: "empty", text: "Loading…" })));
  try {
    state.report = await getJSON(`api/reports/${encodeURIComponent(stem)}.json`);
    state.reportVerdict = "";
    const sel = $("report-verdict");
    sel.replaceChildren(el("option", { value: "", text: "Every verdict" }),
      ...Object.keys(state.report.totals || {}).map((v) => el("option", { value: v, text: (VERDICTS[v] || [v])[0] })));
    renderReport();
    view.scrollIntoView({ behavior: "smooth", block: "start" });
  } catch (err) {
    $("report-rows").tBodies[0].replaceChildren(el("tr", {}, el("td", { colspan: 6, class: "empty", text: "Could not load the report." })));
    console.error(err);
  }
}

function renderReport() {
  const rep = state.report;
  if (!rep) return;
  const verdict = $("report-verdict").value;
  const q = $("report-search").value.trim().toLowerCase();
  $("report-totals").replaceChildren(...Object.entries(rep.totals || {}).map(([v, n]) =>
    el("button", { type: "button", class: verdict === v ? "active" : "", onclick: () => { $("report-verdict").value = verdict === v ? "" : v; renderReport(); } },
      `${(VERDICTS[v] || [v])[0]}: ${n}`)));

  const rows = (rep.rows || []).filter((r) => (!verdict || r.verdict === verdict) &&
    (!q || [r.title, ...(r.current_groups || []), ...(r.releases || []).map((x) => x.group)]
      .some((s) => typeof s === "string" && s.toLowerCase().includes(q))));
  const LIMIT = 1000;
  setRows($("report-rows").tBodies[0], rows.slice(0, LIMIT).map((r) => {
    const [label, cls] = VERDICTS[r.verdict] || [r.verdict, ""];
    const rels = (r.releases || []).map((x) => {
      const text = `${x.group || "?"}${x.best ? " (best)" : ""} · ${x.tracker}`;
      return x.url ? link(x.url, text) : el("span", { class: "tag", text });
    });
    return el("tr", {},
      el("td", {}, link(r.arr_url, r.title || "?"), r.seadex_url ? el("span", { class: "sub" }, link(r.seadex_url, "SeaDex")) : null),
      el("td", { text: arrName(r.arr) }),
      el("td", { text: r.special ? "Special" : r.arr === "radarr" ? "Film" : r.season !== undefined ? `S${r.season}` : "–" }),
      el("td", {}, el("span", { class: "tag " + cls, text: label })),
      el("td", { text: (r.current_groups || []).join(", ") || "–" }),
      el("td", {}, el("div", { class: "links" }, rels)));
  }), 6, "No rows match.");
  $("report-more").textContent = `Generated ${fmtTime(rep.generated_at)} · ${rows.length} of ${(rep.rows || []).length} rows${rows.length > LIMIT ? `, first ${LIMIT} shown; narrow the filter to see the rest` : ""}`;
}

// ---------- log ----------

async function loadLogs() {
  try {
    const lines = await getJSON("api/logs");
    const order = { INFO: 0, WARN: 1, ERROR: 2 };
    const min = order[$("log-level").value] ?? 0;
    const q = $("log-search").value.trim().toLowerCase();
    const shown = lines.filter((l) => (order[l.level] ?? 0) >= min).reverse()
      .map((l) => ({ l, attrs: Object.entries(l.attrs || {}).map(([k, v]) => `${k}=${typeof v === "string" ? JSON.stringify(v) : JSON.stringify(v)}`).join(" ") }))
      .filter(({ l, attrs }) => !q || l.msg.toLowerCase().includes(q) || attrs.toLowerCase().includes(q));
    $("log").replaceChildren(...(shown.length ? shown.map(({ l, attrs }) => el("div", { class: "log-line" },
      el("span", { class: "muted", text: new Date(l.time).toLocaleTimeString() + " " }),
      el("span", { class: "lvl " + l.level, text: l.level }),
      el("span", { text: l.msg + " " }),
      el("span", { class: "attrs", text: attrs }))) : [el("p", { class: "empty", text: "No lines." })]));
  } catch (err) {
    $("log").replaceChildren(el("p", { class: "empty", text: "Could not load the log." }));
    console.error(err);
  }
}

// ---------- wiring ----------

function init() {
  initTheme();
  initTabs();
  for (const id of ["upgrades-search", "upgrades-arr", "upgrades-optional"]) {
    $(id).addEventListener("input", () => state.snap && renderOverview(state.snap));
  }
  for (const th of document.querySelectorAll("#upgrades th[data-sort]")) {
    th.addEventListener("click", () => {
      const key = th.dataset.sort;
      const s = state.upgradesSort;
      state.upgradesSort = s.key === key ? { key, asc: !s.asc } : { key, asc: key === "title" || key === "current" || key === "best" || key === "why" };
      if (state.snap) renderOverview(state.snap);
    });
  }
  $("report-run").addEventListener("click", runReport);
  $("report-search").addEventListener("input", renderReport);
  $("report-verdict").addEventListener("input", renderReport);
  $("log-level").addEventListener("input", loadLogs);
  $("log-search").addEventListener("input", loadLogs);

  refresh();
  setInterval(() => {
    refresh();
    if (state.tab === "log") loadLogs();
  }, REFRESH_MS);
  document.addEventListener("visibilitychange", () => { if (!document.hidden) refresh(); });
}

init();
