const state = {
  drives: [],
  stats: null,
  selectedDrive: null,
  currentPath: "",
  snapshots: [],
  searchTimer: null,
};

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => [...document.querySelectorAll(selector)];

async function api(path, options = {}) {
  const response = await fetch(path, options);
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`;
    try {
      const payload = await response.json();
      message = payload.error || message;
    } catch {}
    throw new Error(message);
  }
  return response.json();
}

function formatBytes(value = 0) {
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let amount = Number(value);
  let unit = 0;
  while (amount >= 1000 && unit < units.length - 1) { amount /= 1000; unit += 1; }
  return unit === 0 ? `${amount} ${units[unit]}` : `${amount.toFixed(amount >= 10 ? 1 : 2)} ${units[unit]}`;
}

function formatCount(value = 0) {
  return new Intl.NumberFormat(undefined, { notation: value >= 10000 ? "compact" : "standard", maximumFractionDigits: 1 }).format(value);
}

function formatDate(value) {
  if (!value || String(value).startsWith("0001-")) return "—";
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date(value));
}

function escapeHTML(value = "") {
  const node = document.createElement("span");
  node.textContent = value;
  return node.innerHTML;
}

function fileIcon(kind, extension = "") {
  if (kind === "directory") return "▰";
  const known = { jpg: "◉", jpeg: "◉", png: "◉", gif: "◉", mp4: "▶", mov: "▶", mkv: "▶", pdf: "P", zip: "Z", txt: "T", md: "M" };
  return known[extension] || "·";
}

function showToast(message) {
  const toast = $("#toast");
  toast.textContent = message;
  toast.classList.add("visible");
  clearTimeout(showToast.timer);
  showToast.timer = setTimeout(() => toast.classList.remove("visible"), 3200);
}

function selectText(node) {
  const selection = window.getSelection();
  if (!selection || !node) return;
  const range = document.createRange();
  range.selectNodeContents(node);
  selection.removeAllRanges();
  selection.addRange(range);
}

async function copyPath(path, pathNode) {
  if (!navigator.clipboard?.writeText) {
    selectText(pathNode);
    showToast("Clipboard access is unavailable. Select the path and copy it manually.");
    return;
  }
  try {
    await navigator.clipboard.writeText(path);
    showToast("Path copied.");
  } catch {
    selectText(pathNode);
    showToast("Could not copy the path. It is selected for manual copying.");
  }
}

function showView(name) {
  $$(".view").forEach((view) => view.classList.toggle("active", view.id === `view-${name}`));
  $$(".nav-item").forEach((button) => button.classList.toggle("active", button.dataset.view === name));
  if (name === "search") setTimeout(() => $("#search-stage-input").focus(), 60);
  if (name === "duplicates") loadDuplicates();
  history.replaceState(null, "", `#${name}`);
}

async function loadLibrary() {
  try {
    const [drives, stats, extensions] = await Promise.all([
      api("/api/drives"), api("/api/stats"), api("/api/extensions"),
    ]);
    state.drives = drives;
    state.stats = stats;
    renderStats(stats);
    renderDrives(drives);
    renderDriveNav(drives);
    renderExtensions(extensions);
  } catch (error) {
    showToast(`Could not load catalog: ${error.message}`);
  }
}

function renderStats(stats) {
  $("#metric-drives").textContent = formatCount(stats.drive_count);
  $("#metric-files").textContent = formatCount(stats.file_count);
  $("#metric-bytes").textContent = formatBytes(stats.total_bytes);
  $("#metric-snapshots").textContent = formatCount(stats.snapshot_count);
  $("#drive-count-badge").textContent = stats.drive_count;
}

function renderDrives(drives) {
  const grid = $("#drive-grid");
  if (!drives.length) {
    grid.innerHTML = `<div class="empty-card"><h3>Your shelf is empty.</h3><p>Connect one external drive, scan it once, then unplug it. ColdShelf keeps the searchable map—not your files.</p><button class="button primary" data-open-scan>Scan your first drive</button></div>`;
    grid.querySelector("[data-open-scan]").addEventListener("click", () => openScanDialog());
    return;
  }
  const colors = ["#759db0", "#d36a47", "#9dbf35", "#8d7fb9"];
  grid.innerHTML = drives.map((drive, index) => `
    <article class="drive-card" data-drive-id="${escapeHTML(drive.id)}" tabindex="0" role="button" style="--card-color:${colors[index % colors.length]}">
      <div class="drive-card-top"><span class="status-chip">${drive.latest_snapshot_id ? "Catalog ready" : "Awaiting scan"}</span><span class="drive-chip">${escapeHTML(drive.id.replace("drv_", "#"))}</span></div>
      <h3>${escapeHTML(drive.name)}</h3>
      <div class="location">${escapeHTML(drive.location || "Location not recorded")}</div>
      <div class="drive-card-stats"><div><span>Files</span><strong>${formatCount(drive.file_count)}</strong></div><div><span>Size</span><strong>${formatBytes(drive.total_bytes)}</strong></div><div><span>Scanned</span><strong>${formatDate(drive.last_scanned_at)}</strong></div></div>
    </article>`).join("");
  grid.querySelectorAll(".drive-card").forEach((card) => {
    const open = () => openDrive(card.dataset.driveId);
    card.addEventListener("click", open);
    card.addEventListener("keydown", (event) => { if (event.key === "Enter" || event.key === " ") open(); });
  });
}

function renderDriveNav(drives) {
  const nav = $("#drive-nav");
  nav.innerHTML = drives.map((drive) => `<button data-drive-id="${escapeHTML(drive.id)}"><span class="drive-dot"></span><span>${escapeHTML(drive.name)}</span></button>`).join("");
  nav.querySelectorAll("button").forEach((button) => button.addEventListener("click", () => openDrive(button.dataset.driveId)));
}

function renderExtensions(items) {
  const chart = $("#extension-chart");
  if (!items.length) {
    chart.innerHTML = `<div class="empty-card"><p>File-type distribution appears after the first scan.</p></div>`;
    return;
  }
  const max = Math.max(...items.map((item) => item.bytes), 1);
  const colors = ["#172019", "#d36a47", "#759db0", "#9dbf35", "#8d7fb9"];
  chart.innerHTML = items.map((item, index) => `<div class="extension-bar" title="${escapeHTML(item.extension)} · ${formatBytes(item.bytes)} · ${formatCount(item.count)} files"><span style="height:${Math.max(4, item.bytes / max * 135)}px;--bar-color:${colors[index % colors.length]}"></span><small>${escapeHTML(item.extension)}</small></div>`).join("");
}

async function runSearch(query) {
  const body = $("#search-results");
  query = query.trim();
  if (!query) {
    $("#search-summary").textContent = "Start typing to search all latest snapshots.";
    body.innerHTML = "";
    return;
  }
  try {
    const hits = await api(`/api/search?q=${encodeURIComponent(query)}&limit=200`);
    $("#search-summary").textContent = `${formatCount(hits.length)} result${hits.length === 1 ? "" : "s"} for “${query}”`;
    body.innerHTML = hits.length ? hits.map((hit, index) => `<tr>
      <td><span class="file-name"><span class="file-icon">${fileIcon(hit.kind, hit.extension)}</span>${escapeHTML(hit.name)}</span></td>
      <td><button class="text-button" data-result-drive="${escapeHTML(hit.drive_id)}">${escapeHTML(hit.drive_name)}</button></td>
      <td class="path-cell"><span class="path-content"><span class="path-text" title="${escapeHTML(hit.path)}">${escapeHTML(hit.path)}</span><button type="button" class="copy-path-button" data-copy-path-index="${index}">Copy</button></span></td>
      <td>${hit.kind === "file" ? formatBytes(hit.size) : "—"}</td><td>${formatDate(hit.modified_at)}</td>
    </tr>`).join("") : `<tr class="empty-row"><td colspan="5">Nothing matched. Try fewer or shorter words.</td></tr>`;
    body.querySelectorAll("[data-result-drive]").forEach((button) => button.addEventListener("click", () => openDrive(button.dataset.resultDrive)));
    body.querySelectorAll("[data-copy-path-index]").forEach((button) => {
      const index = Number(button.dataset.copyPathIndex);
      const hit = hits[index];
      const pathNode = button.closest(".path-content").querySelector(".path-text");
      button.setAttribute("aria-label", `Copy path: ${hit.path}`);
      button.addEventListener("click", () => copyPath(hit.path, pathNode));
    });
  } catch (error) {
    showToast(`Search failed: ${error.message}`);
  }
}

async function openDrive(id, path = "") {
  const drive = state.drives.find((item) => item.id === id);
  if (!drive) return;
  state.selectedDrive = drive;
  state.currentPath = path;
  $("#drive-title").textContent = drive.name;
  $("#drive-id").textContent = "Offline drive · " + drive.id;
  $("#drive-meta").textContent = `${formatCount(drive.file_count)} files · ${formatBytes(drive.total_bytes)} · ${drive.tags.length ? drive.tags.join(" · ") : "untagged"}`;
  $("#drive-location").textContent = drive.location || "Not recorded";
  $("#drive-last-scan").textContent = formatDate(drive.last_scanned_at);
  $("#drive-catalog-id").textContent = drive.id;
  $("#drive-label").href = `/api/drives/${encodeURIComponent(id)}/label.svg`;
  $("#history-panel").classList.add("hidden");
  showView("drive");
  await Promise.all([loadEntries(path), loadSnapshots()]);
}

async function loadEntries(path = "") {
  const drive = state.selectedDrive;
  if (!drive) return;
  state.currentPath = path;
  renderBreadcrumbs(path);
  const body = $("#entry-results");
  try {
    const entries = await api(`/api/drives/${encodeURIComponent(drive.id)}/entries?path=${encodeURIComponent(path)}&limit=1000`);
    body.innerHTML = entries.length ? entries.map((entry) => `<tr class="${entry.kind === "directory" ? "clickable" : ""}" ${entry.kind === "directory" ? `data-entry-path="${escapeHTML(entry.path)}"` : ""}>
      <td><span class="file-name"><span class="file-icon">${fileIcon(entry.kind, entry.extension)}</span>${escapeHTML(entry.name)}</span></td>
      <td>${entry.kind}</td><td>${entry.kind === "file" ? formatBytes(entry.size) : "—"}</td><td>${formatDate(entry.modified_at)}</td><td class="hash-cell" title="${escapeHTML(entry.hash)}">${escapeHTML(entry.hash || "—")}</td>
    </tr>`).join("") : `<tr class="empty-row"><td colspan="5">This folder is empty in the selected snapshot.</td></tr>`;
    body.querySelectorAll("[data-entry-path]").forEach((row) => row.addEventListener("click", () => loadEntries(row.dataset.entryPath)));
  } catch (error) {
    body.innerHTML = `<tr class="empty-row"><td colspan="5">${escapeHTML(error.message)}</td></tr>`;
  }
}

function renderBreadcrumbs(current) {
  const parts = current ? current.split("/") : [];
  const crumbs = [{ label: state.selectedDrive.name, path: "" }];
  parts.forEach((part, index) => crumbs.push({ label: part, path: parts.slice(0, index + 1).join("/") }));
  $("#breadcrumbs").innerHTML = crumbs.map((crumb, index) => `${index ? "<span>/</span>" : ""}<button data-crumb-path="${escapeHTML(crumb.path)}">${escapeHTML(crumb.label)}</button>`).join("");
  $$("#breadcrumbs button").forEach((button) => button.addEventListener("click", () => loadEntries(button.dataset.crumbPath)));
}

async function loadSnapshots() {
  if (!state.selectedDrive) return;
  try { state.snapshots = await api(`/api/drives/${encodeURIComponent(state.selectedDrive.id)}/snapshots`); }
  catch { state.snapshots = []; }
}

async function toggleHistory() {
  const panel = $("#history-panel");
  panel.classList.toggle("hidden");
  if (panel.classList.contains("hidden")) return;
  const complete = state.snapshots.filter((snapshot) => snapshot.status === "complete");
  panel.innerHTML = `<div class="panel-heading"><div><p class="eyebrow">Change memory</p><h2>Snapshot history</h2></div>${complete.length >= 2 ? '<button id="compare-latest" class="button secondary">Compare latest two</button>' : ""}</div>
    <div class="snapshot-list">${state.snapshots.map((snapshot) => `<div class="snapshot-row"><code>#${snapshot.id}</code><div><strong>${formatDate(snapshot.completed_at || snapshot.started_at)}</strong><div class="subtle">${formatCount(snapshot.file_count)} files · ${formatBytes(snapshot.total_bytes)} · ${snapshot.error_count} read errors</div></div><span class="status-chip">${snapshot.status}</span></div>`).join("") || '<p class="subtle">No snapshots yet.</p>'}</div><div id="diff-results" class="diff-list"></div>`;
  if (complete.length >= 2) $("#compare-latest").addEventListener("click", () => compareSnapshots(complete[1].id, complete[0].id));
}

async function compareSnapshots(from, to) {
  const target = $("#diff-results");
  target.innerHTML = '<p class="subtle">Comparing snapshots…</p>';
  try {
    const items = await api(`/api/drives/${encodeURIComponent(state.selectedDrive.id)}/diff?from=${from}&to=${to}&limit=1000`);
    target.innerHTML = items.length ? items.map((item) => `<div class="diff-item"><strong>${item.change}</strong><span>${escapeHTML(item.path)}</span></div>`).join("") : '<p class="subtle">No file-level changes between these snapshots.</p>';
  } catch (error) { target.innerHTML = `<p class="subtle">${escapeHTML(error.message)}</p>`; }
}

async function loadDuplicates() {
  const target = $("#duplicate-list");
  target.innerHTML = '<div class="empty-card"><p>Checking full SHA-256 fingerprints…</p></div>';
  try {
    const groups = await api("/api/duplicates?limit=100");
    target.innerHTML = groups.length ? groups.map((group) => `<article class="duplicate-group"><div class="duplicate-group-head"><strong>${escapeHTML(group.hash.slice(0, 27))}…</strong><span>${formatBytes(group.size)} each · ${group.files.length} copies</span></div>${group.files.map((file) => `<div class="duplicate-file"><strong>${escapeHTML(file.drive_name)}</strong><span title="${escapeHTML(file.path)}">${escapeHTML(file.path)}</span></div>`).join("")}</article>`).join("") : '<div class="empty-card"><h3>No exact duplicates yet.</h3><p>Choose “Full SHA-256” during a scan to prove which files are byte-for-byte identical.</p></div>';
  } catch (error) { target.innerHTML = `<div class="empty-card"><p>${escapeHTML(error.message)}</p></div>`; }
}

function openScanDialog(drive = null) {
  const dialog = $("#scan-dialog");
  $("#scan-form").reset();
  $("#scan-drive-id").value = drive?.id || "";
  $("#scan-name").value = drive?.name || "";
  $("#scan-location").value = drive?.location || "";
  $("#scan-path").value = drive?.source_path || "";
  $("#scan-name").disabled = Boolean(drive);
  $("#scan-progress").classList.add("hidden");
  $("#start-scan").disabled = false;
  dialog.showModal();
}

async function submitScan(event) {
  event.preventDefault();
  const payload = {
    drive_id: $("#scan-drive-id").value,
    path: $("#scan-path").value,
    name: $("#scan-name").value,
    location: $("#scan-location").value,
    tags: $("#scan-tags").value.split(",").map((tag) => tag.trim()).filter(Boolean),
    hash_mode: $("#scan-hash").value,
  };
  $("#start-scan").disabled = true;
  $("#scan-progress").classList.remove("hidden");
  try {
    const job = await api("/api/scans", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) });
    pollJob(job.id);
  } catch (error) {
    $("#start-scan").disabled = false;
    $("#scan-progress").classList.add("hidden");
    showToast(`Scan could not start: ${error.message}`);
  }
}

async function pollJob(id) {
  try {
    const job = await api(`/api/jobs/${encodeURIComponent(id)}`);
    $("#scan-progress-label").textContent = job.progress.current_path || "Reading the drive…";
    $("#scan-progress-count").textContent = `${formatCount(job.progress.files)} files · ${formatBytes(job.progress.bytes)}`;
    if (job.status === "complete") {
      $("#scan-dialog").close();
      showToast(`Scan complete: ${formatCount(job.progress.files)} files cataloged.`);
      await loadLibrary();
      const drive = state.drives.find((item) => item.id === job.drive_id);
      if (drive) openDrive(drive.id);
      return;
    }
    if (job.status === "failed") {
      $("#start-scan").disabled = false;
      showToast(`Scan failed: ${job.error}`);
      return;
    }
    setTimeout(() => pollJob(id), 500);
  } catch (error) { showToast(`Lost scan status: ${error.message}`); }
}

function bindEvents() {
  $$("[data-view]").forEach((button) => button.addEventListener("click", () => showView(button.dataset.view)));
  $("#scan-button").addEventListener("click", () => openScanDialog());
  $("#refresh-library").addEventListener("click", loadLibrary);
  $("#field-note-action").addEventListener("click", () => state.drives.length ? openDrive(state.drives[0].id) : openScanDialog());
  $("#export-json").addEventListener("click", () => { location.href = "/api/export?format=json"; });
  $("#export-csv").addEventListener("click", () => { location.href = "/api/export?format=csv"; });
  $("#show-history").addEventListener("click", toggleHistory);
  $("#rescan-drive").addEventListener("click", () => openScanDialog(state.selectedDrive));
  $("#close-scan").addEventListener("click", () => $("#scan-dialog").close());
  $("#cancel-scan").addEventListener("click", () => $("#scan-dialog").close());
  $("#scan-form").addEventListener("submit", submitScan);

  const queueSearch = (value) => {
    clearTimeout(state.searchTimer);
    state.searchTimer = setTimeout(() => runSearch(value), 140);
  };
  $("#search-stage-input").addEventListener("input", (event) => queueSearch(event.target.value));
  $("#global-search").addEventListener("input", (event) => {
    $("#search-stage-input").value = event.target.value;
    showView("search");
    queueSearch(event.target.value);
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "/" && !["INPUT", "TEXTAREA", "SELECT"].includes(document.activeElement.tagName)) {
      event.preventDefault();
      showView("search");
    }
  });
}

bindEvents();
loadLibrary();
const initialView = location.hash.slice(1);
if (["dashboard", "search", "duplicates"].includes(initialView)) showView(initialView);
