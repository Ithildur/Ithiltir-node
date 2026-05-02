(() => {
  "use strict";

  const cfg = {
    endpoint: "/metrics",
    staticEndpoint: "/static",
    mode: "node-report",
    pollMs: 5000,
    ...(window.ITHILTIR_LOCAL || {})
  };

  const binds = new Map();
  document.querySelectorAll("[data-bind]").forEach((el) => {
    const group = binds.get(el.dataset.bind) || [];
    group.push(el);
    binds.set(el.dataset.bind, group);
  });
  const lists = new Map([...document.querySelectorAll("[data-list]")].map((el) => [el.dataset.list, el]));
  const sections = new Map([...document.querySelectorAll("[data-section]")].map((el) => [el.dataset.section, el]));
  let staticSnap = null;

  function setText(name, value) {
    const group = binds.get(name);
    if (group) group.forEach((el) => { el.textContent = value; });
  }

  function num(value, fallback = 0) {
    const n = Number(value);
    return Number.isFinite(n) ? n : fallback;
  }

  function ratio(value) {
    return Math.max(0, Math.min(1, num(value)));
  }

  function percent(value, digits = 0) {
    return `${(ratio(value) * 100).toFixed(digits)}%`;
  }

  function bytes(value) {
    const n = num(value);
    const units = ["B", "KB", "MB", "GB", "TB", "PB"];
    let v = n;
    let i = 0;
    while (v >= 1024 && i < units.length - 1) {
      v /= 1024;
      i += 1;
    }
    return `${v.toFixed(i === 0 ? 0 : v >= 10 ? 1 : 2)} ${units[i]}`;
  }

  function rate(value) {
    return `${bytes(value)}/s`;
  }

  function barText(value) {
    const filled = Math.round(ratio(value) * 10);
    return `[${"#".repeat(filled)}${".".repeat(10 - filled)}]`;
  }

  function cpuText(info) {
    if (!info) return "--";
    return info.model_name || info.vendor_id || "--";
  }

  function coreText(info) {
    if (!info) return "--";
    const physical = num(info.cores_physical);
    const logical = num(info.cores_logical);
    if (physical > 0 && logical > 0) return `${physical} Cores ${logical} Threads`;
    if (logical > 0) return `${logical} Threads`;
    if (physical > 0) return `${physical} Cores`;
    return "--";
  }

  function swapTotalText(stat, mem) {
    const total = num(stat?.swap_total) || num(mem?.swap_used) + num(mem?.swap_free);
    return total > 0 ? bytes(total) : "--";
  }

  function platformText(system) {
    if (!system) return "--";
    const parts = [system.os || system.platform, system.platform && system.platform !== system.os ? system.platform : "", system.arch].filter(Boolean);
    return parts.length ? parts.join(" / ") : "--";
  }

  function kernelText(system) {
    if (!system) return "--";
    return system.kernel_version || "--";
  }

  function count(value) {
    return Number.isFinite(Number(value)) ? Number(value).toLocaleString("en-US") : "--";
  }

  function sum(list, pick) {
    return Array.isArray(list) ? list.reduce((total, item) => total + num(pick(item)), 0) : 0;
  }

  function avg(list, pick) {
    return Array.isArray(list) && list.length ? sum(list, pick) / list.length : 0;
  }

  function bestDiskRatio(disk) {
    const items = [
      ...((disk && Array.isArray(disk.filesystems)) ? disk.filesystems : []),
      ...((disk && Array.isArray(disk.logical)) ? disk.logical : [])
    ];
    return items.length ? Math.max(...items.map((item) => ratio(item.used_ratio))) : 0;
  }

  function keyOf(item) {
    return item?.mountpoint || item?.path || item?.name || item?.device || item?.device_path || item?.ref || "";
  }

  function staticTotalLookup(stat, kind) {
    const rows = stat?.disk?.[kind];
    const out = new Map();
    if (!Array.isArray(rows)) return out;
    rows.forEach((item) => {
      const total = num(item.total);
      if (total <= 0) return;
      [item.mountpoint, item.path, item.name, item.device, item.device_path, item.ref].filter(Boolean).forEach((key) => {
        out.set(key, total);
      });
    });
    return out;
  }

  function totalSpace(item, lookup) {
    const direct = num(item?.total);
    if (direct > 0) return direct;
    const keyed = lookup.get(keyOf(item)) || lookup.get(item?.device) || lookup.get(item?.path) || lookup.get(item?.name);
    if (keyed > 0) return keyed;
    return num(item?.used) + num(item?.free);
  }

  function waitText(disks) {
    const wait = avg(disks, (item) => item.wait_ms);
    return wait > 0 ? `${wait.toFixed(wait >= 10 ? 0 : 2)} ms` : "--";
  }

  function ioRows(baseIO, physical) {
    const rows = Array.isArray(baseIO) && baseIO.length ? baseIO : (Array.isArray(physical) ? physical : []);
    return rows.slice(0, 8).map((item) => ({
      name: item.name || item.device_path || item.ref || "--",
      read: rate(item.read_rate_bytes_per_sec),
      write: rate(item.write_rate_bytes_per_sec),
      iops: num(item.iops).toFixed(1),
      util: percent(item.util_ratio)
    }));
  }

  function toggleSection(name, visible) {
    const el = sections.get(name);
    if (el) el.hidden = !visible;
  }

  function toView(report, stat) {
    if (cfg.mode === "local-view") return report;

    const metrics = report.metrics || {};
    const staticCPU = stat?.cpu?.info;
    const staticMemory = stat?.memory;
    const staticSystem = stat?.system;
    const disk = metrics.disk || {};
    const net = Array.isArray(metrics.network) ? metrics.network : [];
    const fsRows = Array.isArray(disk.filesystems) ? disk.filesystems : [];
    const logicalRows = Array.isArray(disk.logical) ? disk.logical : [];
    const diskRows = fsRows.length ? fsRows : logicalRows;
    const baseIO = Array.isArray(disk.base_io) ? disk.base_io : [];
    const physicalIO = Array.isArray(disk.physical) ? disk.physical : [];
    const totalLookup = staticTotalLookup(stat, fsRows.length ? "filesystems" : "logical");
    const cpu = ratio(metrics.cpu?.usage_ratio);
    const mem = ratio(metrics.memory?.used_ratio);
    const diskRatio = bestDiskRatio(disk);

    return {
      hostname: report.hostname || "--",
      cpuModel: cpuText(staticCPU),
      coreCount: coreText(staticCPU),
      memoryTotal: staticMemory?.total ? bytes(staticMemory.total) : bytes(num(metrics.memory?.used) + num(metrics.memory?.available)),
      swapTotal: swapTotalText(staticMemory, metrics.memory),
      platform: platformText(staticSystem),
      kernel: kernelText(staticSystem),
      uptime: metrics.system?.uptime || "--",
      processes: String(metrics.processes?.process_count ?? "--"),
      tcp: count(metrics.connections?.tcp_count),
      udp: count(metrics.connections?.udp_count),
      cpu,
      memory: mem,
      disk: diskRatio,
      load: `${num(metrics.cpu?.load1).toFixed(2)} / ${num(metrics.cpu?.load5).toFixed(2)} / ${num(metrics.cpu?.load15).toFixed(2)}`,
      memoryText: `${bytes(metrics.memory?.used)} used / ${bytes(num(metrics.memory?.used) + num(metrics.memory?.available))}`,
      buffers: bytes(metrics.memory?.buffers),
      cached: bytes(metrics.memory?.cached),
      swapText: `${bytes(metrics.memory?.swap_used)} used / ${bytes(num(metrics.memory?.swap_used) + num(metrics.memory?.swap_free))}`,
      wait: waitText(baseIO.length ? baseIO : physicalIO),
      disks: diskRows.slice(0, 8).map((item) => ({
        name: item.mountpoint || item.path || item.name || item.device || "--",
        mid: percent(item.used_ratio),
        value: `${bytes(item.used)} / ${bytes(totalSpace(item, totalLookup))}`
      })),
      io: ioRows(baseIO, physicalIO),
      nets: net.slice(0, 8).map((item) => ({
        name: item.name || "--",
        rx: rate(item.recv_rate_bytes_per_sec),
        tx: rate(item.sent_rate_bytes_per_sec),
        rxTotal: bytes(item.bytes_recv),
        txTotal: bytes(item.bytes_sent),
        errors: count(num(item.err_in) + num(item.err_out) + num(item.drop_in) + num(item.drop_out))
      })),
      raid: raidRows(metrics.raid)
    };
  }

  function raidRows(raid) {
    if (!raid || !raid.supported) {
      return [];
    }
    if (!raid.available) {
      return [];
    }
    const arrays = Array.isArray(raid.arrays) ? raid.arrays : [];
    if (!arrays.length) {
      return [];
    }
    return arrays.slice(0, 8).map((item) => ({
      name: item.name || "--",
      mid: item.status || item.sync_status || "--",
      value: `${item.active ?? 0}/${item.working ?? 0}`
    }));
  }

  function cell(label, value) {
    const el = document.createElement("div");
    const key = document.createElement("span");
    const val = document.createElement("b");
    key.textContent = label;
    val.textContent = value ?? "--";
    el.append(key, val);
    return el;
  }

  function netRow(item) {
    const card = document.createElement("section");
    const name = document.createElement("div");
    const el = document.createElement("section");
    card.className = "net-card";
    name.className = "net-name";
    name.textContent = item.name || "--";
    el.className = "kv-output compact net-row";
    [
      ["rx/s", item.rx],
      ["tx/s", item.tx],
      ["rx_total", item.rxTotal],
      ["tx_total", item.txTotal],
      ["err+drop", item.errors]
    ].forEach(([label, value]) => {
      el.append(cell(label, value));
    });
    card.append(name, el);
    return card;
  }

  function ioRow(item) {
    const el = document.createElement("div");
    el.className = "row io-row";
    [item.name, item.read, item.write, item.iops, item.util].forEach((text) => {
      const span = document.createElement("span");
      span.textContent = text;
      el.append(span);
    });
    return el;
  }

  function row(item) {
    const el = document.createElement("div");
    el.className = "row";
    [item.name, item.mid, item.value].forEach((text) => {
      const span = document.createElement("span");
      span.textContent = text;
      el.append(span);
    });
    return el;
  }

  function renderList(name, items) {
    const host = lists.get(name);
    if (!host) return;
    if (!items || !items.length) {
      const empty = document.createElement("div");
      empty.className = "empty";
      empty.textContent = "无数据";
      host.replaceChildren(empty);
      return;
    }
    const renderItem = name === "nets" ? netRow : name === "io" ? ioRow : row;
    host.replaceChildren(...items.map(renderItem));
  }

  function render(view) {
    [
      "hostname", "cpuModel", "coreCount", "memoryTotal", "swapTotal", "platform",
      "kernel", "uptime", "processes", "tcp", "udp", "load", "memoryText", "buffers",
      "cached", "swapText", "wait"
    ].forEach((name) => setText(name, view[name] ?? "--"));

    setText("cpu", percent(view.cpu));
    setText("memory", percent(view.memory));
    setText("disk", percent(view.disk));
    setText("cpuBar", barText(view.cpu));
    setText("memoryBar", barText(view.memory));
    setText("diskBar", barText(view.disk));

    renderList("disks", view.disks);
    renderList("io", view.io);
    renderList("nets", view.nets);
    toggleSection("raid", Array.isArray(view.raid) && view.raid.length > 0);
    renderList("raid", view.raid);
  }

  function offline() {
    render({
      hostname: "--",
      cpuModel: "--",
      coreCount: "--",
      memoryTotal: "--",
      swapTotal: "--",
      platform: "--",
      kernel: "--",
      uptime: "--",
      processes: "--",
      tcp: "--",
      udp: "--",
      cpu: 0,
      memory: 0,
      disk: 0,
      load: "--",
      memoryText: "--",
      buffers: "--",
      cached: "--",
      swapText: "--",
      wait: "--",
      disks: [],
      io: [],
      nets: [],
      raid: []
    });
  }

  async function refreshStatic() {
    if (!cfg.staticEndpoint || cfg.mode === "local-view") return;
    try {
      const res = await fetch(cfg.staticEndpoint, { headers: { Accept: "application/json" }, cache: "no-store" });
      if (res.ok) staticSnap = await res.json();
    } catch {}
  }

  async function poll() {
    try {
      await refreshStatic();
      const res = await fetch(cfg.endpoint, { headers: { Accept: "application/json" }, cache: "no-store" });
      if (!res.ok) throw Object.assign(new Error(`HTTP ${res.status}`), { status: res.status });
      const payload = await res.json();
      render(toView(payload, staticSnap));
    } catch {
      offline();
    }
  }

  offline();
  poll();
  setInterval(poll, Math.max(1000, num(cfg.pollMs, 5000)));
})();
