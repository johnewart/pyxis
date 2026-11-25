"use strict";

function fmtBytes(n) {
  if (n === null || n === undefined) return "-";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = Number(n), i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return (i === 0 ? v : v.toFixed(1)) + " " + units[i];
}

function fmtNum(n) {
  if (n === null || n === undefined) return "-";
  return Number(n).toLocaleString();
}

function fmtDuration(sec) {
  sec = Math.floor(sec);
  const d = Math.floor(sec / 86400); sec -= d * 86400;
  const h = Math.floor(sec / 3600); sec -= h * 3600;
  const m = Math.floor(sec / 60); sec -= m * 60;
  const parts = [];
  if (d) parts.push(d + "d");
  if (h) parts.push(h + "h");
  if (m) parts.push(m + "m");
  parts.push(sec + "s");
  return parts.join(" ");
}

function el(tag, attrs, children) {
  const e = document.createElement(tag);
  if (attrs) for (const k in attrs) {
    if (k === "class") e.className = attrs[k];
    else e.setAttribute(k, attrs[k]);
  }
  (children || []).forEach((c) => e.append(c));
  return e;
}

function kvCard(title, rows) {
  const body = el("table", null, rows.map(([k, v]) =>
    el("tr", null, [el("th", null, [k]), el("td", { class: "num" }, [String(v)])])
  ));
  return el("div", { class: "card" }, [el("h2", null, [title]), body]);
}

function render(s) {
  const content = document.getElementById("content");
  content.innerHTML = "";
  const grid = el("div", { class: "grid" });

  grid.append(kvCard("process", [
    ["uptime", fmtDuration(s.uptime_seconds)],
    ["go", s.go.version],
    ["goroutines", fmtNum(s.go.goroutines)],
    ["num cpu", s.go.num_cpu],
    ["num gc", fmtNum(s.go.num_gc)],
    ["loaded runtimes", fmtNum(s.runtime.loaded_runtimes)],
  ]));

  grid.append(kvCard("memory", [
    ["alloc", fmtBytes(s.go.alloc_bytes)],
    ["sys", fmtBytes(s.go.sys_bytes)],
    ["heap alloc", fmtBytes(s.go.heap_alloc_bytes)],
    ["heap inuse", fmtBytes(s.go.heap_inuse_bytes)],
    ["heap sys", fmtBytes(s.go.heap_sys_bytes)],
    ["stack inuse", fmtBytes(s.go.stack_inuse_bytes)],
  ]));

  (s.databases || []).forEach((db) => {
    const rows = [
      ["file", fmtBytes(db.file_bytes)],
      ["wal", fmtBytes(db.wal_bytes)],
      ["pages", fmtNum(db.page_count) + " × " + fmtBytes(db.page_size) + " = " + fmtBytes(db.page_bytes)],
    ];
    if (db.error) rows.push(["error", db.error]);
    const card = kvCard("db · " + db.label, rows);
    if (db.tables && db.tables.length) {
      const t = el("table", null, [
        el("tr", null, [el("th", null, ["table"]), el("th", { class: "num" }, ["rows"])]),
        ...db.tables.map((tb) =>
          el("tr", null, [el("td", null, [tb.name]), el("td", { class: "num" }, [fmtNum(tb.rows)])])
        ),
      ]);
      card.append(el("div", { class: "muted", style: "margin-top:.6rem" }, ["tables"]), t);
    }
    grid.append(card);
  });

  const g = s.graph || {};
  const gCard = kvCard("graph store", [
    ["base dir", g.base_dir || "-"],
    ["open handles", fmtNum(g.open_handles) + " / " + fmtNum(g.max_open_handles)],
    ["partitions", fmtNum((g.partitions || []).length)],
    ["total size", fmtBytes(g.total_size_bytes)],
  ]);
  grid.append(gCard);

  const p = s.paths || {};
  grid.append(kvCard("paths", [
    ["providers", p.providers_file || "-"],
    ["graph data", p.graph_data_dir || "-"],
    ["embedding cache", p.embedding_cache_dir || "-"],
    ["embedding cache size", fmtBytes(p.embedding_cache_dir_bytes)],
  ]));

  content.append(grid);
}

let timer = null;
async function refresh() {
  try {
    const res = await fetch("/debug/statusz?format=json", { cache: "no-store" });
    render(await res.json());
  } catch (e) {
    document.getElementById("content").textContent = "error: " + e;
  }
}

document.getElementById("refresh").addEventListener("click", refresh);
document.getElementById("autorefresh").addEventListener("change", (e) => {
  if (timer) { clearInterval(timer); timer = null; }
  if (e.target.checked) timer = setInterval(refresh, 5000);
});
refresh();
