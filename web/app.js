const state = {
  user: null,
  isRegisterMode: false,
  selectedMonth: "all",
  selectedCategory: "all",
  selectedDevice: "all",
  selectedNode: "all",
  searchQuery: "",
  files: [],
  summary: { total_count: 0, total_size: 0, by_time: [], by_category: [], by_device: [], by_node: [] },
  serverInfo: { entry_url: location.origin, entry_addrs: [], local_url: location.origin, invite_required: true, pool: null },
  pool: null,
  clusterSecret: "",
  exposeIP: "",
  exposeIPs: [],
  disabledIPs: [],
  port: "8080",
  availableAddrs: [],
  entryAddrs: [],
  secretVisible: false,
  settingsTab: "general",
  pendingUsersCount: 0,
  theme: "dark",
  appVersion: "V0.12.0",
  localImportMode: "import",
  mounts: [],
  clusterCapabilities: [],
  marketplacePlugins: [],
  pluginJobs: [],
  activePluginRunFileId: "",
  transfers: [],
  transferTab: "upload",
  transferOpen: false,
  updateStatus: null,
  discoveredNodes: [],
  adoptAuthMode: "admin",
  adoptTargetURL: "",
  adoptTargetName: "",
  selectableUsers: [],
  uploadAllowedUsers: [],
  accessModalMode: "upload",
  accessModalFileId: null,
  accessModalIsCustom: false,
  accessDraftSet: new Set(),
  viewMode: "list",
  workspacePage: "files",
  lastRenderedFilesSig: "",
  versionModalFileId: null,
  versionModalData: null,
  editorFileId: null,
  editorFileRec: null,
  editorSplitMd: false,
  healthTimer: null,
  chartMetric: "count",
  autoRefresh: true,
  eventSource: null,
  sseReconnectTimer: null,
  lastEventVersion: 0,
  autoRefreshTimer: null,
  isUploadingSelf: false,
  uploadPolicy: { allowed_exts: [], blocked_exts: [] },
  device: { fp: "", name: "", detail: "" },
  pollTimer: null,
};

const categoryColors = {
  video: "#8b5cf6",
  audio: "#ec4899",
  document: "#3b82f6",
  markdown: "#10b981",
  image: "#06b6d4",
  archive: "#f59e0b",
  other: "#64748b",
};

const categoryNames = {
  all: "全部类型",
  video: "视频",
  audio: "音乐 / 音频",
  document: "文档 (Office / PDF)",
  markdown: "Markdown / 文本",
  image: "图片",
  archive: "压缩包",
  other: "其他",
};

const categoryIcons = {
  all: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></svg>`,
  video: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2"><polygon points="5 3 19 12 5 21 5 3"/></svg>`,
  audio: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/></svg>`,
  document: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>`,
  markdown: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>`,
  image: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/></svg>`,
  archive: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 8v13H3V8"/><path d="M1 3h22v5H1z"/><path d="M10 12h4"/></svg>`,
  other: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="9"/></svg>`,
};

const WIN_FOLDER_ICON_SVG = `<svg class="win-folder-svg" viewBox="0 0 48 48" aria-hidden="true">
  <path d="M5 12c0-2.2 1.8-4 4-4h10.2c1.2 0 2.3.5 3.1 1.4l2.3 2.6H41c2.2 0 4 1.8 4 4v21c0 2.2-1.8 4-4 4H9c-2.2 0-4-1.8-4-4V12z" fill="#E39D00"/>
  <path d="M4 17c0-1.7 1.3-3 3-3h34c1.7 0 3 1.3 3 3v20c0 2.2-1.8 4-4 4H8c-2.2 0-4-1.8-4-4V17z" fill="#FFD34E"/>
  <path d="M4 17c0-1.7 1.3-3 3-3h34c1.7 0 3 1.3 3 3v2H4v-2z" fill="#FFE68A" opacity="0.75"/>
</svg>`;

function getWinFileIconSvg(rec, size = 48) {
  const ext = (rec?.ext || "").toLowerCase().replace(/^\./, "");
  const name = (rec?.name || "").toLowerCase();
  const cat = rec?.category || "other";
  const szAttr = `width="${size}" height="${size}" viewBox="0 0 48 48" aria-hidden="true"`;

  // Archive -> classic Windows zippered folder
  if (["zip", "tar", "gz", "tgz", "rar", "7z"].includes(ext) || name.endsWith(".tar.gz") || cat === "archive") {
    return `<svg ${szAttr}>
      <path d="M5 12c0-2.2 1.8-4 4-4h10.2c1.2 0 2.3.5 3.1 1.4l2.3 2.6H41c2.2 0 4 1.8 4 4v21c0 2.2-1.8 4-4 4H9c-2.2 0-4-1.8-4-4V12z" fill="#D99100"/>
      <path d="M4 17c0-1.7 1.3-3 3-3h34c1.7 0 3 1.3 3 3v20c0 2.2-1.8 4-4 4H8c-2.2 0-4-1.8-4-4V17z" fill="#FFD24C"/>
      <rect x="22" y="14" width="4" height="27" fill="#526074" opacity="0.88"/>
      <rect x="21" y="17" width="3" height="2" fill="#E2E8F0"/>
      <rect x="24" y="20" width="3" height="2" fill="#E2E8F0"/>
      <rect x="21" y="23" width="3" height="2" fill="#E2E8F0"/>
      <rect x="24" y="26" width="3" height="2" fill="#E2E8F0"/>
      <rect x="20.5" y="29" width="7" height="7" rx="1.5" fill="#94A3B8"/>
      <rect x="22" y="31" width="4" height="3" rx="0.6" fill="#E2E8F0"/>
    </svg>`;
  }

  // Word (.doc / .docx)
  if (["doc", "docx"].includes(ext)) {
    return `<svg ${szAttr}>
      <path d="M12 5h17l10 10v26c0 1.7-1.3 3-3 3H12c-1.7 0-3-1.3-3-3V8c0-1.7 1.3-3 3-3z" fill="#F8FAFC" stroke="#CBD5E1" stroke-width="1.2"/>
      <path d="M29 5v7c0 1.7 1.3 3 3 3h7L29 5z" fill="#CBD5E1"/>
      <rect x="20" y="19" width="15" height="2.4" rx="1.2" fill="#94A3B8"/>
      <rect x="20" y="25" width="15" height="2.4" rx="1.2" fill="#94A3B8"/>
      <rect x="20" y="31" width="12" height="2.4" rx="1.2" fill="#94A3B8"/>
      <rect x="4" y="16" width="18" height="18" rx="3.5" fill="#185ABD"/>
      <path d="M7.5 20.5h2.2l1.8 6.4 1.6-5.2h1.9l1.6 5.2 1.8-6.4h2.1l-2.8 9.2h-2l-1.7-5.5-1.7 5.5h-2l-2.8-9.2z" fill="#FFFFFF"/>
    </svg>`;
  }

  // Excel (.xls / .xlsx / .csv)
  if (["xls", "xlsx", "csv"].includes(ext)) {
    return `<svg ${szAttr}>
      <path d="M12 5h17l10 10v26c0 1.7-1.3 3-3 3H12c-1.7 0-3-1.3-3-3V8c0-1.7 1.3-3 3-3z" fill="#F8FAFC" stroke="#CBD5E1" stroke-width="1.2"/>
      <path d="M29 5v7c0 1.7 1.3 3 3 3h7L29 5z" fill="#CBD5E1"/>
      <rect x="20" y="19" width="15" height="2.4" rx="1.2" fill="#A7F3D0"/>
      <rect x="20" y="25" width="15" height="2.4" rx="1.2" fill="#6EE7B7"/>
      <rect x="20" y="31" width="15" height="2.4" rx="1.2" fill="#A7F3D0"/>
      <rect x="4" y="16" width="18" height="18" rx="3.5" fill="#107C41"/>
      <path d="M8.5 20l4.5 5.2L17.5 20h2.4l-5.6 6.5 5.8 6.5h-2.5l-4.6-5.2-4.6 5.2H6l5.8-6.5L6.2 20h2.3z" fill="#FFFFFF" transform="translate(1 0) scale(0.92)"/>
    </svg>`;
  }

  // PowerPoint (.ppt / .pptx)
  if (["ppt", "pptx"].includes(ext)) {
    return `<svg ${szAttr}>
      <path d="M12 5h17l10 10v26c0 1.7-1.3 3-3 3H12c-1.7 0-3-1.3-3-3V8c0-1.7 1.3-3 3-3z" fill="#F8FAFC" stroke="#CBD5E1" stroke-width="1.2"/>
      <path d="M29 5v7c0 1.7 1.3 3 3 3h7L29 5z" fill="#CBD5E1"/>
      <circle cx="29" cy="27" r="6" fill="#FDBA74"/>
      <path d="M29 21a6 6 0 0 1 6 6h-6v-6z" fill="#EA580C"/>
      <rect x="4" y="16" width="18" height="18" rx="3.5" fill="#C43E1C"/>
      <path d="M9.5 20.5h4.2c2.1 0 3.3 1.1 3.3 2.9 0 1.9-1.2 3-3.4 3h-2v3.3H9.5v-9.2zm2.1 1.7v2.6h1.8c1 0 1.5-.5 1.5-1.3s-.5-1.3-1.5-1.3h-1.8z" fill="#FFFFFF"/>
    </svg>`;
  }

  // PDF
  if (ext === "pdf") {
    return `<svg ${szAttr}>
      <path d="M10 5h19l10 10v26c0 1.7-1.3 3-3 3H10c-1.7 0-3-1.3-3-3V8c0-1.7 1.3-3 3-3z" fill="#F8FAFC" stroke="#CBD5E1" stroke-width="1.2"/>
      <path d="M29 5v7c0 1.7 1.3 3 3 3h7L29 5z" fill="#CBD5E1"/>
      <rect x="6" y="21" width="32" height="13" rx="3" fill="#DC2626"/>
      <text x="22" y="30.5" text-anchor="middle" font-size="8.5" font-weight="700" font-family="Segoe UI, Arial, sans-serif" fill="#FFFFFF">PDF</text>
    </svg>`;
  }

  // Video
  if (cat === "video") {
    return `<svg ${szAttr}>
      <rect x="5" y="9" width="38" height="30" rx="5" fill="#3B82F6"/>
      <rect x="8" y="12" width="4" height="4" rx="1" fill="#DBEAFE"/>
      <rect x="8" y="22" width="4" height="4" rx="1" fill="#DBEAFE"/>
      <rect x="8" y="32" width="4" height="4" rx="1" fill="#DBEAFE"/>
      <rect x="36" y="12" width="4" height="4" rx="1" fill="#DBEAFE"/>
      <rect x="36" y="22" width="4" height="4" rx="1" fill="#DBEAFE"/>
      <rect x="36" y="32" width="4" height="4" rx="1" fill="#DBEAFE"/>
      <polygon points="20,17 31,24 20,31" fill="#FFFFFF"/>
    </svg>`;
  }

  // Audio
  if (cat === "audio") {
    return `<svg ${szAttr}>
      <path d="M10 5h19l10 10v26c0 1.7-1.3 3-3 3H10c-1.7 0-3-1.3-3-3V8c0-1.7 1.3-3 3-3z" fill="#F8FAFC" stroke="#CBD5E1" stroke-width="1.2"/>
      <path d="M29 5v7c0 1.7 1.3 3 3 3h7L29 5z" fill="#CBD5E1"/>
      <path d="M20 31.5V19l11-2.2v11.5" fill="none" stroke="#EC4899" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/>
      <circle cx="17" cy="32" r="3.4" fill="#EC4899"/>
      <circle cx="28" cy="29.5" r="3.4" fill="#8B5CF6"/>
    </svg>`;
  }

  // Image (offline / fallback)
  if (cat === "image") {
    return `<svg ${szAttr}>
      <rect x="5" y="8" width="38" height="32" rx="4.5" fill="#E0F2FE" stroke="#7DD3FC" stroke-width="1.5"/>
      <circle cx="15" cy="17" r="3.5" fill="#F59E0B"/>
      <path d="M8 35l9-10 7 7 5-5 11 8v3H8v-3z" fill="#0284C7"/>
      <path d="M18 35l8-9 12 9H18z" fill="#0EA5E9" opacity="0.85"/>
    </svg>`;
  }

  // Markdown / Code / Text
  if (cat === "markdown") {
    return `<svg ${szAttr}>
      <path d="M10 5h19l10 10v26c0 1.7-1.3 3-3 3H10c-1.7 0-3-1.3-3-3V8c0-1.7 1.3-3 3-3z" fill="#F8FAFC" stroke="#CBD5E1" stroke-width="1.2"/>
      <path d="M29 5v7c0 1.7 1.3 3 3 3h7L29 5z" fill="#CBD5E1"/>
      <polyline points="19,22 14,27 19,32" fill="none" stroke="#0EA5E9" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/>
      <polyline points="29,22 34,27 29,32" fill="none" stroke="#10B981" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/>
      <line x1="25.5" y1="20.5" x2="22.5" y2="33.5" stroke="#64748B" stroke-width="2" stroke-linecap="round"/>
    </svg>`;
  }

  // Generic Windows document icon
  return `<svg ${szAttr}>
    <path d="M10 5h19l10 10v26c0 1.7-1.3 3-3 3H10c-1.7 0-3-1.3-3-3V8c0-1.7 1.3-3 3-3z" fill="#F8FAFC" stroke="#CBD5E1" stroke-width="1.2"/>
    <path d="M29 5v7c0 1.7 1.3 3 3 3h7L29 5z" fill="#CBD5E1"/>
    <rect x="13" y="21" width="20" height="2.4" rx="1.2" fill="#94A3B8"/>
    <rect x="13" y="27" width="20" height="2.4" rx="1.2" fill="#94A3B8"/>
    <rect x="13" y="33" width="13" height="2.4" rx="1.2" fill="#94A3B8"/>
  </svg>`;
}

const $ = (id) => document.getElementById(id);

function showToast(msg, type = "info") {
  const wrap = $("toast-container");
  const el = document.createElement("div");
  el.className = `toast ${type}`;
  el.textContent = msg;
  wrap.appendChild(el);
  setTimeout(() => el.remove(), 2600);
}

const ICON_LOCK_SVG = `<svg viewBox="0 0 24 24" width="12.5" height="12.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="vertical-align:-2px;margin-right:3px;"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>`;
const ICON_CHEVRON_DOWN_SVG = `<svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" style="vertical-align:-1px;margin-left:3px;"><polyline points="6 9 12 15 18 9"/></svg>`;
const ICON_MOON_SVG = `<svg viewBox="0 0 24 24" width="13.5" height="13.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>`;
const ICON_SUN_SVG = `<svg viewBox="0 0 24 24" width="13.5" height="13.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/></svg>`;
const ICON_VERSION_BRANCH_SVG = `<svg viewBox="0 0 24 24" width="11.5" height="11.5" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><line x1="6" y1="3" x2="6" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/></svg>`;
const ICON_CLOCK_SVG = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>`;
const ICON_DEVICE_SVG = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="3" width="20" height="14" rx="2" ry="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/></svg>`;
const ICON_SERVER_SVG = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="2" width="20" height="8" rx="2" ry="2"/><rect x="2" y="14" width="20" height="8" rx="2" ry="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/></svg>`;
const ICON_CPU_SVG = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="16" height="16" rx="2"/><rect x="9" y="9" width="6" height="6"/><line x1="9" y1="1" x2="9" y2="4"/><line x1="15" y1="1" x2="15" y2="4"/><line x1="9" y1="20" x2="9" y2="23"/><line x1="15" y1="20" x2="15" y2="23"/><line x1="20" y1="9" x2="23" y2="9"/><line x1="20" y1="14" x2="23" y2="14"/><line x1="1" y1="9" x2="4" y2="9"/><line x1="1" y1="14" x2="4" y2="14"/></svg>`;
const ICON_MEM_SVG = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 19v-3"/><path d="M10 19v-3"/><path d="M14 19v-3"/><path d="M18 19v-3"/><path d="M8 11V9"/><path d="M16 11V9"/><path d="M12 11V9"/><path d="M2 15h20"/><path d="M2 7a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2Z"/></svg>`;
const ICON_DISK_SVG = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="22" y1="12" x2="2" y2="12"/><path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"/><line x1="6" y1="16" x2="6.01" y2="16"/><line x1="10" y1="16" x2="10.01" y2="16"/></svg>`;
const ICON_NET_SVG = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>`;

// ==================== Theme System ====================
function getSavedTheme() {
  try {
    return localStorage.getItem("lan_filehub_theme") === "light" ? "light" : "dark";
  } catch {
    return "dark";
  }
}

function applyTheme(theme) {
  const clean = theme === "light" ? "light" : "dark";
  state.theme = clean;
  document.documentElement.dataset.theme = clean;
  try {
    localStorage.setItem("lan_filehub_theme", clean);
  } catch {}

  const iconSvg = clean === "light" ? ICON_MOON_SVG : ICON_SUN_SVG;
  if ($("btn-auth-theme")) {
    $("btn-auth-theme").innerHTML = `${iconSvg}<span>${clean === "light" ? "深色主题" : "浅色主题"}</span>`;
  }
  if ($("btn-top-theme")) {
    $("btn-top-theme").innerHTML = `${iconSvg}<span>${clean === "light" ? "深色" : "浅色"}</span>`;
  }
  if ($("theme-card-dark")) {
    $("theme-card-dark").classList.toggle("active", clean === "dark");
  }
  if ($("theme-card-light")) {
    $("theme-card-light").classList.toggle("active", clean === "light");
  }
}

function formatSize(bytes) {
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let idx = 0;
  let val = bytes;
  while (val >= 1024 && idx < units.length - 1) {
    val /= 1024;
    idx++;
  }
  return `${val.toFixed(idx === 0 ? 0 : 2)} ${units[idx]}`;
}

function formatQuotaDisplay(bytes) {
  if (!bytes) return "0 MB";
  const TB = 1024 * 1024 * 1024 * 1024;
  const GB = 1024 * 1024 * 1024;
  const MB = 1024 * 1024;
  const fmt = (v) => Number(v.toFixed(2)).toString();
  if (bytes >= TB) return `${fmt(bytes / TB)} TB`;
  if (bytes >= GB) return `${fmt(bytes / GB)} GB`;
  return `${fmt(bytes / MB)} MB`;
}

function splitQuotaUnit(bytes) {
  const TB = 1024 * 1024 * 1024 * 1024;
  const GB = 1024 * 1024 * 1024;
  const MB = 1024 * 1024;
  if (!bytes || bytes < GB) {
    return { value: Number(((bytes || 500 * MB) / MB).toFixed(2)).toString(), unit: "MB" };
  }
  if (bytes >= TB && bytes % GB === 0 && (bytes / TB) >= 1 && Number.isInteger(bytes / TB)) {
    return { value: String(bytes / TB), unit: "TB" };
  }
  if (bytes >= TB && bytes / TB >= 1 && (bytes % (TB / 10) === 0)) {
    return { value: Number((bytes / TB).toFixed(2)).toString(), unit: "TB" };
  }
  if (bytes >= GB && bytes % GB !== 0 && bytes < 2 * GB) {
    return { value: Number((bytes / MB).toFixed(2)).toString(), unit: "MB" };
  }
  if (bytes >= TB) {
    return { value: Number((bytes / TB).toFixed(2)).toString(), unit: "TB" };
  }
  return { value: Number((bytes / GB).toFixed(2)).toString(), unit: "GB" };
}

function formatTime(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString("zh-CN", { hour12: false });
}

function formatRelativeTime(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "-";
  const sec = Math.max(0, Math.floor((Date.now() - d.getTime()) / 1000));
  if (sec < 8) return "刚刚";
  if (sec < 60) return `${sec} 秒前`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} 分钟前`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} 小时前`;
  return `${Math.floor(hr / 24)} 天前`;
}

function escapeHtml(str) {
  return String(str)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

// Pure-JS SHA-256 (works over plain HTTP LAN origins where crypto.subtle is unavailable)
function sha256Hex(str) {
  const bytes = new TextEncoder().encode(str);
  const K = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
  ];
  const H = [
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
    0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
  ];

  const bitLen = bytes.length * 8;
  const paddedLen = Math.ceil((bytes.length + 9) / 64) * 64;
  const padded = new Uint8Array(paddedLen);
  padded.set(bytes);
  padded[bytes.length] = 0x80;
  const view = new DataView(padded.buffer);
  view.setUint32(paddedLen - 8, Math.floor(bitLen / 0x100000000), false);
  view.setUint32(paddedLen - 4, bitLen >>> 0, false);

  const w = new Uint32Array(64);
  const rotr = (n, x) => (x >>> n) | (x << (32 - n));

  for (let offset = 0; offset < paddedLen; offset += 64) {
    for (let i = 0; i < 16; i++) {
      w[i] = view.getUint32(offset + i * 4, false);
    }
    for (let i = 16; i < 64; i++) {
      const s0 = rotr(7, w[i - 15]) ^ rotr(18, w[i - 15]) ^ (w[i - 15] >>> 3);
      const s1 = rotr(17, w[i - 2]) ^ rotr(19, w[i - 2]) ^ (w[i - 2] >>> 10);
      w[i] = (w[i - 16] + s0 + w[i - 7] + s1) >>> 0;
    }

    let [a, b, c, d, e, f, g, h] = H;
    for (let i = 0; i < 64; i++) {
      const S1 = rotr(6, e) ^ rotr(11, e) ^ rotr(25, e);
      const ch = (e & f) ^ (~e & g);
      const temp1 = (h + S1 + ch + K[i] + w[i]) >>> 0;
      const S0 = rotr(2, a) ^ rotr(13, a) ^ rotr(22, a);
      const maj = (a & b) ^ (a & c) ^ (b & c);
      const temp2 = (S0 + maj) >>> 0;

      h = g;
      g = f;
      f = e;
      e = (d + temp1) >>> 0;
      d = c;
      c = b;
      b = a;
      a = (temp1 + temp2) >>> 0;
    }

    H[0] = (H[0] + a) >>> 0;
    H[1] = (H[1] + b) >>> 0;
    H[2] = (H[2] + c) >>> 0;
    H[3] = (H[3] + d) >>> 0;
    H[4] = (H[4] + e) >>> 0;
    H[5] = (H[5] + f) >>> 0;
    H[6] = (H[6] + g) >>> 0;
    H[7] = (H[7] + h) >>> 0;
  }

  return H.map((x) => x.toString(16).padStart(8, "0")).join("");
}

function parseDeviceLabel() {
  const ua = navigator.userAgent || "";
  const low = ua.toLowerCase();
  let os = "未知系统";
  if (low.includes("iphone")) os = "iPhone";
  else if (low.includes("ipad")) os = "iPad";
  else if (low.includes("android")) os = "Android";
  else if (low.includes("windows")) os = "Windows";
  else if (low.includes("mac os x") || low.includes("macintosh")) os = "macOS";
  else if (low.includes("linux")) os = "Linux";

  let browser = "Browser";
  if (low.includes("edg/")) browser = "Edge";
  else if (low.includes("micromessenger")) browser = "微信";
  else if (low.includes("opr/") || low.includes("opera")) browser = "Opera";
  else if (low.includes("firefox/")) browser = "Firefox";
  else if (low.includes("chrome/") || low.includes("crios/")) browser = "Chrome";
  else if (low.includes("safari/")) browser = "Safari";

  return `${os} · ${browser}`;
}

function getPersistentDeviceSalt() {
  const key = "lan_filehub_device_uid";
  try {
    let val = localStorage.getItem(key);
    if (!val) {
      val = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
      localStorage.setItem(key, val);
    }
    return val;
  } catch {
    return "no-storage";
  }
}

function collectDeviceFingerprint() {
  let canvasSig = "";
  try {
    const canvas = document.createElement("canvas");
    canvas.width = 180;
    canvas.height = 36;
    const ctx = canvas.getContext("2d");
    if (ctx) {
      ctx.textBaseline = "top";
      ctx.font = "14px Arial";
      ctx.fillStyle = "#3b82f6";
      ctx.fillRect(4, 4, 40, 20);
      ctx.fillStyle = "#f1f5f9";
      ctx.fillText("FileHub-FP-2026", 8, 8);
      canvasSig = canvas.toDataURL().slice(-64);
    }
  } catch {}

  let webglRenderer = "";
  try {
    const glCanvas = document.createElement("canvas");
    const gl = glCanvas.getContext("webgl") || glCanvas.getContext("experimental-webgl");
    if (gl) {
      const dbg = gl.getExtension("WEBGL_debug_renderer_info");
      if (dbg) {
        webglRenderer = String(gl.getParameter(dbg.UNMASKED_RENDERER_WEBGL) || "");
      }
    }
  } catch {}

  let tz = "";
  try {
    tz = Intl.DateTimeFormat().resolvedOptions().timeZone || "";
  } catch {}

  const sw = window.screen ? window.screen.width : 0;
  const sh = window.screen ? window.screen.height : 0;
  const dpr = window.devicePixelRatio || 1;
  const cores = navigator.hardwareConcurrency || 0;
  const mem = navigator.deviceMemory || 0;
  const touch = navigator.maxTouchPoints || 0;
  const salt = getPersistentDeviceSalt();

  const rawSignals = [
    navigator.userAgent || "",
    navigator.platform || "",
    navigator.language || "",
    `${sw}x${sh}@${dpr}`,
    `c=${cores}`,
    `m=${mem}`,
    `t=${touch}`,
    tz,
    webglRenderer,
    canvasSig,
    salt,
  ].join("|");

  const fp = "FP-" + sha256Hex(rawSignals).slice(0, 10).toUpperCase();
  const name = parseDeviceLabel();
  const detailParts = [];
  if (sw && sh) detailParts.push(`${sw}x${sh}`);
  if (cores) detailParts.push(`${cores}核`);
  if (tz) detailParts.push(tz);

  state.device = {
    fp,
    name,
    detail: detailParts.join(" · "),
  };

  if ($("auth-device-badge")) {
    $("auth-device-badge").textContent = `当前设备：${name} (${fp})`;
  }
  if ($("current-device-badge")) {
    $("current-device-badge").textContent = `${name} · ${fp}`;
    $("current-device-badge").title = `设备指纹 ${fp}\n${state.device.detail}`;
  }
  if ($("upload-device-hint")) {
    $("upload-device-hint").textContent = `支持视频 / 音乐 / Office / PDF / Markdown / 压缩包 · 上传将自动标记 ${name} (${fp}) 并按配额调度`;
  }
  if ($("settings-device-name")) {
    $("settings-device-name").textContent = name;
    $("settings-device-fp").textContent = fp;
    $("settings-device-detail").textContent = state.device.detail || "标准浏览器环境";
  }
}

async function copyText(text) {
  if (!text) return;
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      showToast(`已复制：${text}`, "success");
      return;
    }
  } catch {}

  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    const ok = document.execCommand("copy");
    ta.remove();
    if (ok) {
      showToast(`已复制：${text}`, "success");
      return;
    }
  } catch {}
  showToast("复制失败，请手动选中复制", "error");
}

function safeHeaderSet(headers, key, rawVal) {
  const val = String(rawVal || "").trim();
  if (!val) return;
  try {
    headers.set(key, encodeURIComponent(val));
  } catch {}
}

async function api(url, options = {}) {
  const headers = new Headers(options.headers || {});
  if (state.device) {
    if (state.device.fp && !headers.has("X-Device-FP")) safeHeaderSet(headers, "X-Device-FP", state.device.fp);
    if (state.device.name && !headers.has("X-Device-Name")) safeHeaderSet(headers, "X-Device-Name", state.device.name);
    if (state.device.detail && !headers.has("X-Device-Detail")) safeHeaderSet(headers, "X-Device-Detail", state.device.detail);
  }
  const res = await fetch(url, { ...options, headers });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(data.error || `请求失败 (${res.status})`);
  }
  return data;
}

function normalizeExtList(raw) {
  const parts = Array.isArray(raw)
    ? raw
    : String(raw || "").split(/[\s,;，；、\r\n]+/);
  const seen = new Set();
  const out = [];
  for (const item of parts) {
    const clean = String(item || "")
      .trim()
      .toLowerCase()
      .replace(/^\.+/, "");
    if (!clean || seen.has(clean)) continue;
    seen.add(clean);
    out.push(clean);
  }
  return out;
}

function updateUploadPolicy(policy) {
  const allowed = normalizeExtList(policy?.allowed_exts || []);
  const blocked = normalizeExtList(policy?.blocked_exts || []);
  state.uploadPolicy = { allowed_exts: allowed, blocked_exts: blocked };

  const parts = [];
  if (allowed.length > 0) parts.push(`仅允许: ${allowed.join(", ")}`);
  if (blocked.length > 0) parts.push(`禁止: ${blocked.join(", ")}`);
  const summaryText = parts.length > 0 ? parts.join(" · ") : "允许所有文件类型";

  if ($("upload-policy-hint")) {
    if (parts.length > 0) {
      $("upload-policy-hint").textContent = `上传规则：${summaryText}`;
      $("upload-policy-hint").classList.remove("hidden");
    } else {
      $("upload-policy-hint").classList.add("hidden");
      $("upload-policy-hint").textContent = "";
    }
  }
  if ($("upload-policy-status")) {
    $("upload-policy-status").textContent = `当前规则：${summaryText}`;
  }
  if ($("setting-allowed-exts") && document.activeElement !== $("setting-allowed-exts")) {
    $("setting-allowed-exts").value = allowed.join(", ");
  }
  if ($("setting-blocked-exts") && document.activeElement !== $("setting-blocked-exts")) {
    $("setting-blocked-exts").value = blocked.join(", ");
  }
}

function isExtAllowedByPolicy(filename) {
  const allowed = state.uploadPolicy?.allowed_exts || [];
  const blocked = state.uploadPolicy?.blocked_exts || [];
  const base = String(filename || "").trim().toLowerCase().split(/[\\/]/).pop() || "";
  const dot = base.lastIndexOf(".");
  const ext = dot >= 0 && dot < base.length - 1 ? base.slice(dot + 1) : "";
  const candidates = [];
  if (base.endsWith(".tar.gz")) candidates.push("tar.gz");
  if (ext && !candidates.includes(ext)) candidates.push(ext);

  for (const c of candidates) {
    if (blocked.includes(c)) {
      return { ok: false, reason: `禁止上传 .${c} 文件` };
    }
  }
  if (allowed.length > 0) {
    const matched = candidates.some((c) => allowed.includes(c));
    if (!matched) {
      return { ok: false, reason: `不在允许上传的类型（${allowed.join(", ")}）内` };
    }
  }
  return { ok: true, reason: "" };
}

function syncVersionBadges() {
  const ver = state.appVersion || "V0.12.0";
  if ($("auth-version-badge")) $("auth-version-badge").textContent = ver;
  if ($("sidebar-version-badge")) $("sidebar-version-badge").textContent = ver;
}

function applyUpdateStatus(st) {
  if (!st || typeof st !== "object") return;
  state.updateStatus = st;
  const isAdmin = state.user && state.user.role === "admin";
  const hasUpd = Boolean(isAdmin && st.has_update);

  if ($("btn-update-notice")) {
    $("btn-update-notice").classList.toggle("hidden", !hasUpd);
    if (hasUpd) {
      $("btn-update-notice").textContent = `发现新版本 ${st.latest_version}`;
    }
  }
  if ($("update-tab-dot")) {
    $("update-tab-dot").classList.toggle("hidden", !hasUpd);
  }
  renderUpdateUI();
}

async function loadServerInfo() {
  try {
    const info = await api("/api/info");
    state.serverInfo = info;
    if (Array.isArray(info.entry_addrs)) {
      state.entryAddrs = info.entry_addrs;
    }
    if (info.version) {
      state.appVersion = info.version;
      syncVersionBadges();
    }
    if (info.pool) {
      state.pool = info.pool;
    }
    if (info.upload_policy) {
      updateUploadPolicy(info.upload_policy);
    }
    renderAccessAddresses();
    syncRegisterUI();
  } catch {}
}

function syncRegisterUI() {
  const inviteRequired = state.serverInfo.invite_required !== false;
  if ($("auth-card-sub")) {
    $("auth-card-sub").textContent = inviteRequired
      ? "请登录或使用管理员发放的邀请码注册账号"
      : "请登录或提交注册申请（需管理员同意后登录）";
  }
  if (!state.isRegisterMode) {
    $("invite-field-wrap").classList.add("hidden");
    $("auth-invite-code").required = false;
    $("register-approval-hint").classList.add("hidden");
    $("auth-submit").textContent = "登录进入";
    return;
  }
  if (inviteRequired) {
    $("invite-field-wrap").classList.remove("hidden");
    $("auth-invite-code").required = true;
    $("register-approval-hint").classList.add("hidden");
    $("auth-submit").textContent = "使用邀请码注册并登录";
  } else {
    $("invite-field-wrap").classList.add("hidden");
    $("auth-invite-code").required = false;
    $("register-approval-hint").classList.remove("hidden");
    $("auth-submit").textContent = "提交注册申请";
  }
}

function getActiveEntryAddrs() {
  const fromState = Array.isArray(state.entryAddrs) && state.entryAddrs.length > 0
    ? state.entryAddrs
    : Array.isArray(state.serverInfo?.entry_addrs) && state.serverInfo.entry_addrs.length > 0
    ? state.serverInfo.entry_addrs
    : [];
  if (fromState.length > 0) return fromState;
  const fallbackURL = state.serverInfo?.entry_url || state.serverInfo?.local_url || location.origin;
  return [{ url: fallbackURL, ip: "", iface: "默认入口", tag: "主选入口", primary: true, enabled: true }];
}

function renderAccessAddresses() {
  const entryUrl = state.serverInfo.entry_url || state.serverInfo.local_url || location.origin;
  const pool = state.pool || state.serverInfo.pool;
  const addrs = getActiveEntryAddrs();

  if ($("auth-pool-pill") && pool) {
    $("auth-pool-pill").textContent = `分布式存储池：${pool.online_nodes} 节点在线 · 总预留 ${formatQuotaDisplay(pool.total_quota_bytes)} · 已启用 ${addrs.length} 个网卡入口`;
  }

  const authList = $("auth-access-list");
  if (authList) {
    authList.innerHTML = addrs
      .map((a, idx) => {
        const isPrimary = Boolean(a.primary || idx === 0);
        const tagLabel = isPrimary
          ? `主选入口${a.iface && a.iface !== "默认入口" ? " · " + a.iface : ""}`
          : `${a.iface || "网卡"}${a.tag ? " (" + a.tag + ")" : ""}`;
        return `
          <div class="access-row ${isPrimary ? "primary" : ""}">
            <div class="access-row-info">
              <span class="access-tag">${escapeHtml(tagLabel)}</span>
              <span class="access-url-mono">${escapeHtml(a.url)}</span>
            </div>
            <button class="btn ${isPrimary ? "btn-primary" : "btn-ghost"} btn-xs" type="button" data-copy-url="${escapeHtml(a.url)}">复制地址</button>
          </div>
        `;
      })
      .join("");
  }

  const topSelect = $("top-entry-select");
  const topUrlEl = $("top-entry-url");
  const copyBtn = $("btn-copy-entry-url");
  if (topSelect && topUrlEl && copyBtn) {
    if (addrs.length > 1) {
      const prevVal = topSelect.value;
      topSelect.innerHTML = addrs
        .map((a, idx) => {
          const isPrimary = Boolean(a.primary || idx === 0);
          const lbl = `${a.url} (${a.iface || a.tag || (isPrimary ? "主选" : "网卡")}${isPrimary ? " · 主选" : ""})`;
          return `<option value="${escapeHtml(a.url)}">${escapeHtml(lbl)}</option>`;
        })
        .join("");
      if (prevVal && addrs.some((a) => a.url === prevVal)) {
        topSelect.value = prevVal;
      } else {
        topSelect.value = addrs[0].url;
      }
      topSelect.classList.remove("hidden");
      topUrlEl.classList.add("hidden");
      topSelect.onchange = () => {
        topUrlEl.textContent = topSelect.value;
      };
      copyBtn.onclick = () => copyText(topSelect.value || entryUrl);
    } else {
      const singleUrl = addrs[0]?.url || entryUrl;
      topSelect.classList.add("hidden");
      topUrlEl.classList.remove("hidden");
      topUrlEl.textContent = singleUrl;
      copyBtn.onclick = () => copyText(singleUrl);
    }
  } else if (topUrlEl && copyBtn) {
    topUrlEl.textContent = entryUrl;
    copyBtn.onclick = () => copyText(entryUrl);
  }
}

function updatePendingBadges(count) {
  state.pendingUsersCount = count || 0;
  const show = state.user && state.user.role === "admin" && state.pendingUsersCount > 0;
  for (const id of ["pending-badge", "sidebar-pending-badge", "users-pending-pill"]) {
    const el = $(id);
    if (!el) continue;
    el.textContent = String(state.pendingUsersCount);
    el.classList.toggle("hidden", !show);
  }
}

function getSavedViewMode() {
  try {
    const v = localStorage.getItem("lan_filehub_view_mode");
    return v === "grid" || v === "chart" ? v : "list";
  } catch {
    return "list";
  }
}

function getSavedAutoRefresh() {
  try {
    return localStorage.getItem("lan_filehub_auto_refresh") !== "false";
  } catch {
    return true;
  }
}

function updateLiveSyncUI(status) {
  const pill = $("btn-auto-refresh");
  const txt = $("live-sync-text");
  if (!pill || !txt) return;
  pill.classList.remove("connected", "reconnecting", "paused");
  if (status === "connected") {
    pill.classList.add("connected");
    txt.textContent = "实时同步";
  } else if (status === "reconnecting") {
    pill.classList.add("reconnecting");
    txt.textContent = "重连中...";
  } else {
    pill.classList.add("paused");
    txt.textContent = "自动刷新已暂停";
  }
}

function scheduleAutoRefresh() {
  if (state.autoRefreshTimer) {
    clearTimeout(state.autoRefreshTimer);
  }
  state.autoRefreshTimer = setTimeout(() => {
    state.autoRefreshTimer = null;
    if (state.user && !$("app-view").classList.contains("hidden")) {
      refreshAll();
    }
  }, 250);
}

function stopLiveSync() {
  if (state.sseReconnectTimer) {
    clearTimeout(state.sseReconnectTimer);
    state.sseReconnectTimer = null;
  }
  if (state.autoRefreshTimer) {
    clearTimeout(state.autoRefreshTimer);
    state.autoRefreshTimer = null;
  }
  if (state.eventSource) {
    try {
      state.eventSource.close();
    } catch {}
    state.eventSource = null;
  }
  if (!state.autoRefresh) {
    updateLiveSyncUI("paused");
  }
}

function startLiveSync() {
  stopLiveSync();
  if (!state.autoRefresh) {
    updateLiveSyncUI("paused");
    return;
  }
  if (!state.user || !$("app-view") || $("app-view").classList.contains("hidden")) {
    return;
  }
  if (typeof EventSource !== "function") {
    updateLiveSyncUI("paused");
    return;
  }

  updateLiveSyncUI("reconnecting");
  const es = new EventSource("/api/events");
  state.eventSource = es;

  es.addEventListener("ready", (e) => {
    if (state.eventSource !== es) return;
    updateLiveSyncUI("connected");
    try {
      const payload = JSON.parse(e.data || "{}");
      const ver = Number(payload.version || 0);
      if (payload.app_version && state.appVersion && payload.app_version !== state.appVersion) {
        showToast(`已平滑升级至 ${payload.app_version}，正在加载新版界面...`, "success");
        setTimeout(() => location.reload(), 800);
        return;
      }
      if (state.lastEventVersion > 0 && ver > state.lastEventVersion) {
        scheduleAutoRefresh();
      }
      if (ver > state.lastEventVersion) {
        state.lastEventVersion = ver;
      }
    } catch {}
  });

  es.addEventListener("change", (e) => {
    if (state.eventSource !== es) return;
    let ev = {};
    try {
      ev = JSON.parse(e.data || "{}");
    } catch {
      return;
    }
    const ver = Number(ev.version || 0);
    if (ver > state.lastEventVersion) {
      state.lastEventVersion = ver;
    }

    if (ev.kind === "update_available") {
      if (ev.update_status) applyUpdateStatus(ev.update_status);
      if (state.user && state.user.role === "admin") {
        showToast(`发现新版本 ${ev.node_name || ev.update_status?.latest_version || ""}，可在「设置 -> 系统升级」不停机更新`, "info");
      }
      return;
    }
    if (ev.kind === "update_progress") {
      if (ev.update_status) applyUpdateStatus(ev.update_status);
      return;
    }
    if (ev.kind === "upgrading") {
      showToast(`系统正在平滑切换至新版本 ${ev.node_name || ""}（不停机更新中）...`, "info");
      return;
    }

    if (!state.autoRefresh) return;

    if (ev.kind === "policy") {
      api("/api/me")
        .then((d) => {
          if (d.upload_policy) updateUploadPolicy(d.upload_policy);
        })
        .catch(() => {});
      if (!state.isUploadingSelf) {
        showToast("管理员已更新上传类型规则", "info");
      }
      return;
    }

    if (ev.kind === "role_change") {
      if (state.user && ev.uploader === state.user.username) {
        api("/api/me")
          .then((d) => {
            if (d.user) {
              onLoggedIn(d.user, d.pending_users || 0);
              if (d.update_status) applyUpdateStatus(d.update_status);
              showToast(
                d.user.role === "admin" ? "您已被授予管理员权限" : "您的管理员权限已被收回",
                "info"
              );
            }
          })
          .catch(() => {});
      }
      return;
    }

    if (ev.kind === "access") {
      loadFiles();
      loadSummary();
      return;
    }

    if (ev.kind === "entry_switched") {
      const nextURL = String(ev.entry_url || "").trim();
      if (nextURL && !nextURL.startsWith(location.origin)) {
        showToast(`主节点已切换至 ${ev.node_name || nextURL || "新主入口"}，正在跳转...`, "info");
        setTimeout(() => { location.href = nextURL; }, 1200);
      } else {
        scheduleAutoRefresh();
      }
      return;
    }

    if (ev.kind === "cluster_sync") {
      scheduleAutoRefresh();
      const modal = $("settings-modal");
      if (modal && !modal.classList.contains("hidden")) {
        if (state.settingsTab === "cluster") {
          loadPassiveDiscovered();
          loadAdminMounts();
        } else if (state.settingsTab === "plugins") {
          loadAdminPlugins();
          loadPluginJobs();
        }
      }
      return;
    }

    scheduleAutoRefresh();
    if (!state.isUploadingSelf) {
      const who = ev.uploader || "其他设备";
      const count = Number(ev.count || 0);
      const names = Array.isArray(ev.file_names) ? ev.file_names : [];
      if (ev.kind === "upload" || ev.kind === "extract") {
        const verb = ev.kind === "extract" ? "解压入库了" : "上传了";
        const detail = count === 1 && names.length === 1 ? names[0] : `${count} 个新文件`;
        showToast(`已自动更新：${who} ${verb} ${detail}`, "info");
      } else if (ev.kind === "delete") {
        const target = names[0] ? ` ${names[0]}` : "";
        showToast(`已自动同步：${who} 删除了${target || "文件"}`, "info");
      }
    }
  });

  es.onerror = () => {
    if (state.eventSource !== es) return;
    if (!state.autoRefresh || !state.user || $("app-view").classList.contains("hidden")) {
      stopLiveSync();
      return;
    }
    updateLiveSyncUI("reconnecting");
    if (es.readyState === EventSource.CLOSED) {
      stopLiveSync();
      updateLiveSyncUI("reconnecting");
      state.sseReconnectTimer = setTimeout(() => {
        state.sseReconnectTimer = null;
        if (state.autoRefresh && state.user && !state.eventSource) {
          startLiveSync();
        }
      }, 3000);
    }
  };
}

async function init() {
  state.viewMode = getSavedViewMode();
  state.autoRefresh = getSavedAutoRefresh();
  applyTheme(getSavedTheme());
  collectDeviceFingerprint();
  bindEvents();
  await loadServerInfo();
  try {
    const data = await api("/api/me");
    state.serverInfo = {
      entry_url: data.entry_url || state.serverInfo.entry_url,
      entry_addrs: Array.isArray(data.entry_addrs) ? data.entry_addrs : state.serverInfo.entry_addrs,
      local_url: data.local_url || state.serverInfo.local_url,
      invite_required: typeof data.invite_required === "boolean" ? data.invite_required : state.serverInfo.invite_required,
      pool: data.pool || state.serverInfo.pool,
    };
    if (Array.isArray(data.entry_addrs)) {
      state.entryAddrs = data.entry_addrs;
    }
    if (data.version) {
      state.appVersion = data.version;
      syncVersionBadges();
    }
    if (data.pool) state.pool = data.pool;
    if (data.events_version) state.lastEventVersion = Number(data.events_version);
    if (data.upload_policy) updateUploadPolicy(data.upload_policy);
    renderAccessAddresses();
    onLoggedIn(data.user, data.pending_users || 0);
    if (data.update_status) applyUpdateStatus(data.update_status);
  } catch {
    showAuth();
  }
}

function showAuth() {
  stopLiveSync();
  state.updateStatus = null;
  if ($("btn-update-notice")) $("btn-update-notice").classList.add("hidden");
  if (state.pollTimer) {
    clearInterval(state.pollTimer);
    state.pollTimer = null;
  }
  $("auth-view").classList.remove("hidden");
  $("app-view").classList.add("hidden");
  loadServerInfo();
}

function onLoggedIn(user, pendingCount = 0) {
  state.user = user;
  $("auth-view").classList.add("hidden");
  $("app-view").classList.remove("hidden");
  $("user-badge").textContent = `${user.username} (${user.role === "admin" ? "管理员" : "用户"})`;

  const isAdmin = user.role === "admin";
  document.querySelectorAll(".admin-only-tab, .admin-only-control").forEach((el) => {
    el.classList.toggle("hidden", !isAdmin);
  });
  if (!isAdmin) {
    if ($("upload-target-node")) $("upload-target-node").value = "auto";
    if ($("btn-update-notice")) $("btn-update-notice").classList.add("hidden");
    switchSettingsTab("general");
  }
  updatePendingBadges(pendingCount);

  state.uploadAllowedUsers = [];
  updateUploadAccessButton();
  loadSelectableUsers();

  renderAccessAddresses();
  applyViewMode(state.viewMode);
  refreshAll();
  startLiveSync();

  if (state.pollTimer) clearInterval(state.pollTimer);
  state.pollTimer = setInterval(() => {
    if (state.user && !$("app-view").classList.contains("hidden")) {
      loadCluster();
      if (state.user.role === "admin") {
        api("/api/admin/settings")
          .then((d) => updatePendingBadges(d.pending_count || 0))
          .catch(() => {});
      }
    }
  }, 8000);
}

async function refreshAll() {
  await Promise.all([loadCluster(), loadSummary(), loadFiles()]);
}

async function loadCluster() {
  try {
    const prevCapSig = JSON.stringify((state.clusterCapabilities || []).map((c) => `${c.capability}:${(c.providers || []).length}`));
    const [data, capData] = await Promise.all([
      api("/api/cluster"),
      api("/api/plugins/capabilities").catch(() => null),
    ]);
    if (capData && Array.isArray(capData.capabilities)) {
      state.clusterCapabilities = capData.capabilities;
      const nextCapSig = JSON.stringify(state.clusterCapabilities.map((c) => `${c.capability}:${(c.providers || []).length}`));
      if (nextCapSig !== prevCapSig && state.files && state.files.length > 0 && state.workspacePage === "files") {
        renderCurrentFilesView(true);
      }
    }
    state.pool = data.pool;
    state.clusterSecret = data.cluster_secret || "";
    if (data.port) state.port = data.port;
    if (typeof data.expose_ip === "string") state.exposeIP = data.expose_ip;
    if (Array.isArray(data.expose_ips)) state.exposeIPs = data.expose_ips;
    if (Array.isArray(data.disabled_ips)) state.disabledIPs = data.disabled_ips;
    if (Array.isArray(data.available_addrs)) state.availableAddrs = data.available_addrs;
    if (Array.isArray(data.entry_addrs)) {
      state.entryAddrs = data.entry_addrs;
      state.serverInfo.entry_addrs = data.entry_addrs;
    }
    if (data.events_version) {
      const remoteVer = Number(data.events_version);
      if (state.autoRefresh && state.lastEventVersion > 0 && remoteVer > state.lastEventVersion) {
        state.lastEventVersion = remoteVer;
        scheduleAutoRefresh();
      } else if (remoteVer > state.lastEventVersion) {
        state.lastEventVersion = remoteVer;
      }
    }
    if (data.pool && data.pool.entry_url) {
      state.serverInfo.entry_url = data.pool.entry_url;
      renderAccessAddresses();
    }
    renderPoolStatus();
    renderNodeSummary();
    renderUploadTargetSelect();
    if (state.viewMode === "chart" && $("files-charts") && !$("files-charts").classList.contains("hidden")) {
      renderFilesCharts();
    }
    if (state.workspacePage === "analytics") {
      renderAnalyticsPage();
    } else if (state.workspacePage === "health") {
      renderNodeHealthPage();
    }
    const modal = $("settings-modal");
    if (!modal.classList.contains("hidden") && state.settingsTab === "cluster" && !modal.contains(document.activeElement)) {
      renderAdminCluster();
    }
  } catch {}
}

function renderPoolStatus() {
  const pool = state.pool;
  if (!pool) return;
  $("pool-nodes-badge").textContent = `${pool.online_nodes} / ${pool.total_nodes} 节点在线`;
  const pct = pool.total_quota_bytes > 0
    ? Math.min(100, Math.round((pool.total_used_bytes / pool.total_quota_bytes) * 100))
    : 0;
  $("pool-progress-fill").style.width = `${pct}%`;
  $("pool-capacity-text").textContent = `已用 ${formatSize(pool.total_used_bytes)} / 总预留 ${formatQuotaDisplay(pool.total_quota_bytes)}（剩 ${formatSize(pool.total_free_bytes)}）`;

  const pill = $("topbar-cluster-pill");
  const dot = $("topbar-cluster-dot");
  const text = $("topbar-cluster-text");
  if (pill && dot && text) {
    const hasOffline = pool.total_nodes > pool.online_nodes;
    pill.classList.toggle("has-offline", hasOffline);
    dot.className = `node-dot ${hasOffline ? "offline" : "online"}`;
    text.textContent = hasOffline
      ? `${pool.online_nodes}/${pool.total_nodes} 在线（${pool.total_nodes - pool.online_nodes} 离线）`
      : `${pool.online_nodes}/${pool.total_nodes} 节点在线`;
  }
}

function renderNodeSummary() {
  const wrap = $("node-summary-list");
  if (!wrap || !state.pool) return;
  const nodes = state.pool.nodes || [];
  wrap.innerHTML = "";

  const allBtn = document.createElement("button");
  allBtn.type = "button";
  allBtn.className = `nav-item ${state.selectedNode === "all" ? "active" : ""}`;
  allBtn.innerHTML = `
    <div class="nav-item-left">
      <span class="nav-label">全部节点</span>
    </div>
    <span class="nav-count">${state.pool.online_nodes}/${nodes.length} 在线</span>
  `;
  allBtn.onclick = () => {
    state.selectedNode = "all";
    closeMobileSidebar();
    refreshAll();
  };
  wrap.appendChild(allBtn);

  for (const n of nodes) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = `nav-item ${state.selectedNode === n.node_id ? "active" : ""} ${n.online ? "" : "is-offline"}`;
    const roleTag = n.role === "entry" ? "主入口" : "扩容节点";
    const statusSub = n.online
      ? `${escapeHtml(n.os)} · ${roleTag} · 在线`
      : `${escapeHtml(n.os)} · 离线 (${formatRelativeTime(n.last_seen)}) · 文件已隐藏`;
    btn.innerHTML = `
      <div class="nav-item-left" style="flex-direction:column;align-items:flex-start;gap:1px;">
        <div style="display:flex;align-items:center;gap:6px;max-width:100%;">
          <span class="node-dot ${n.online ? "online" : "offline"}"></span>
          <span class="nav-label">${escapeHtml(n.node_name)}</span>
        </div>
        <span class="nav-sub">${statusSub}</span>
      </div>
      <div class="nav-right">
        <span class="nav-count">${n.online ? n.file_count : "离线"}</span>
        <span class="nav-size">${n.online ? `${formatSize(n.used_bytes)} / ${formatQuotaDisplay(n.quota_bytes)}` : "文件已隐藏"}</span>
      </div>
    `;
    btn.onclick = () => {
      state.selectedNode = state.selectedNode === n.node_id ? "all" : n.node_id;
      closeMobileSidebar();
      refreshAll();
    };
    wrap.appendChild(btn);
  }
}

function renderUploadTargetSelect() {
  const sel = $("upload-target-node");
  if (!sel) return;
  if (!state.user || state.user.role !== "admin") {
    sel.innerHTML = `<option value="auto">自动分配（剩余空间最大）</option>`;
    sel.value = "auto";
    sel.classList.add("hidden");
    return;
  }
  sel.classList.remove("hidden");
  if (!state.pool) return;
  const prev = sel.value || "auto";
  const onlineNodes = (state.pool.nodes || []).filter((n) => n.online);
  sel.innerHTML = `<option value="auto">自动分配（剩余空间最大）</option>` +
    onlineNodes
      .map(
        (n) =>
          `<option value="${escapeHtml(n.node_id)}">${escapeHtml(n.node_name)}${n.is_self ? " (本机)" : ""} · 剩 ${escapeHtml(formatSize(n.free_bytes))}</option>`
      )
      .join("");
  if ([...sel.options].some((o) => o.value === prev)) {
    sel.value = prev;
  } else {
    sel.value = "auto";
  }
}

async function loadSummary() {
  const params = new URLSearchParams();
  if (state.selectedCategory !== "all") params.set("category", state.selectedCategory);
  if (state.selectedMonth !== "all") params.set("month", state.selectedMonth);
  if (state.selectedNode !== "all") params.set("node", state.selectedNode);

  const [globalSum, filteredSum] = await Promise.all([
    api("/api/summary"),
    api(`/api/summary?${params.toString()}`),
  ]);

  state.summary = filteredSum;
  $("stat-total-count").textContent = filteredSum.total_count;
  $("stat-total-size").textContent = formatSize(filteredSum.total_size);

  const devLabel =
    state.selectedDevice === "all"
      ? "全部设备"
      : (globalSum.by_device || []).find((d) => d.key === state.selectedDevice)?.label || state.selectedDevice;

  const nodeLabel =
    state.selectedNode === "all"
      ? "全部节点"
      : (state.pool?.nodes || []).find((n) => n.node_id === state.selectedNode)?.node_name || state.selectedNode;

  $("active-filters").textContent = `${
    state.selectedMonth === "all" ? "全部时间" : state.selectedMonth
  } · ${categoryNames[state.selectedCategory] || state.selectedCategory} · ${devLabel} · ${nodeLabel}`;

  renderTimeSummary(globalSum.by_time || []);
  renderCategorySummary(
    state.selectedMonth === "all" && state.selectedNode === "all"
      ? globalSum.by_category || []
      : filteredSum.by_category || []
  );
  renderDeviceSummary(globalSum.by_device || []);
}

function renderTimeSummary(buckets) {
  const wrap = $("time-summary-list");
  wrap.innerHTML = "";

  const makeItem = (key, label, countText, sizeText) => {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = `nav-item ${state.selectedMonth === key ? "active" : ""}`;
    btn.innerHTML = `
      <div class="nav-item-left">
        <span class="nav-icon">${ICON_CLOCK_SVG}</span>
        <span class="nav-label">${escapeHtml(label)}</span>
      </div>
      <div class="nav-right">
        <span class="nav-count">${escapeHtml(countText)}</span>
        ${sizeText ? `<span class="nav-size">${escapeHtml(sizeText)}</span>` : ""}
      </div>
    `;
    btn.onclick = () => {
      state.selectedMonth = key === "all" ? "all" : state.selectedMonth === key ? "all" : key;
      closeMobileSidebar();
      if (state.workspacePage === "health") switchWorkspacePage("files");
      refreshAll();
    };
    return btn;
  };

  wrap.appendChild(makeItem("all", "全部时间", "全部", ""));
  for (const b of buckets) {
    wrap.appendChild(makeItem(b.key, b.label, `${b.count}`, formatSize(b.size)));
  }
}

function syncMobileQuickCats() {
  const wrap = $("m-quick-cats");
  if (!wrap) return;
  wrap.querySelectorAll("[data-m-cat]").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.mCat === state.selectedCategory);
  });
}

function renderCategorySummary(buckets) {
  syncMobileQuickCats();
  const wrap = $("category-summary-list");
  if (!wrap) return;
  wrap.innerHTML = "";

  const makeItem = (key, label, countText, sizeText) => {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = `nav-item ${state.selectedCategory === key ? "active" : ""}`;
    const iconSvg = categoryIcons[key] || categoryIcons.other;
    btn.innerHTML = `
      <div class="nav-item-left">
        <span class="nav-icon">${iconSvg}</span>
        <span class="nav-label">${escapeHtml(label)}</span>
      </div>
      <div class="nav-right">
        <span class="nav-count">${escapeHtml(countText)}</span>
        ${sizeText ? `<span class="nav-size">${escapeHtml(sizeText)}</span>` : ""}
      </div>
    `;
    btn.onclick = () => {
      state.selectedCategory = key === "all" ? "all" : state.selectedCategory === key ? "all" : key;
      closeMobileSidebar();
      if (state.workspacePage === "health") switchWorkspacePage("files");
      refreshAll();
    };
    return btn;
  };

  wrap.appendChild(makeItem("all", "全部类型", "全部", ""));
  for (const b of buckets) {
    wrap.appendChild(makeItem(b.key, b.label, `${b.count}`, formatSize(b.size)));
  }
}

function renderDeviceSummary(buckets) {
  const wrap = $("device-summary-list");
  wrap.innerHTML = "";

  const allBtn = document.createElement("button");
  allBtn.type = "button";
  allBtn.className = `nav-item ${state.selectedDevice === "all" ? "active" : ""}`;
  allBtn.innerHTML = `
    <div class="nav-item-left">
      <span class="nav-icon">${ICON_DEVICE_SVG}</span>
      <span class="nav-label">全部设备</span>
    </div>
    <span class="nav-count">${buckets.length} 台</span>
  `;
  allBtn.onclick = () => {
    state.selectedDevice = "all";
    closeMobileSidebar();
    if (state.workspacePage === "health") switchWorkspacePage("files");
    refreshAll();
  };
  wrap.appendChild(allBtn);

  for (const b of buckets) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = `nav-item ${state.selectedDevice === b.key ? "active" : ""}`;
    btn.innerHTML = `
      <div class="nav-item-left" style="flex-direction:column;align-items:flex-start;gap:1px;">
        <div style="display:flex;align-items:center;gap:6px;max-width:100%;">
          <span class="nav-icon">${ICON_DEVICE_SVG}</span>
          <span class="nav-label">${escapeHtml(b.label)}</span>
        </div>
        <span class="nav-sub">${escapeHtml(b.key)}</span>
      </div>
      <div class="nav-right">
        <span class="nav-count">${b.count}</span>
        <span class="nav-size">${formatSize(b.size)}</span>
      </div>
    `;
    btn.onclick = () => {
      state.selectedDevice = state.selectedDevice === b.key ? "all" : b.key;
      closeMobileSidebar();
      if (state.workspacePage === "health") switchWorkspacePage("files");
      refreshAll();
    };
    wrap.appendChild(btn);
  }
}

function computeFilesRenderSig() {
  const role = state.user?.role || "";
  const uname = state.user?.username || "";
  const parts = [`${state.viewMode}|${role}|${uname}|${state.selectedNode}|${state.files.length}`];
  for (let i = 0; i < state.files.length; i++) {
    const r = state.files[i];
    const acl = Array.isArray(r.allowed_users) ? r.allowed_users.join(",") : "";
    parts.push(`${r.id}:${r.version || 1}:${r.version_count || 1}:${r.update_method || ""}:${r.size}:${r.uploaded_at}:${r.node_online !== false ? 1 : 0}:${acl}:${r.storage_dir || ""}`);
  }
  return parts.join(";");
}

async function loadFiles() {
  const params = new URLSearchParams();
  if (state.selectedCategory !== "all") params.set("category", state.selectedCategory);
  if (state.selectedMonth !== "all") params.set("month", state.selectedMonth);
  if (state.selectedDevice !== "all") params.set("device", state.selectedDevice);
  if (state.selectedNode !== "all") params.set("node", state.selectedNode);
  if (state.searchQuery.trim()) params.set("q", state.searchQuery.trim());

  const data = await api(`/api/files?${params.toString()}`);
  state.files = data.files || [];
  renderCurrentFilesView(false);
  if (state.workspacePage === "analytics") {
    renderAnalyticsPage();
  }
}

function applyViewMode(mode) {
  const valid = mode === "grid" || mode === "chart" ? mode : "list";
  state.viewMode = valid;
  try {
    localStorage.setItem("lan_filehub_view_mode", valid);
  } catch {}
  document.querySelectorAll("#view-switcher [data-view-mode]").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.viewMode === valid);
  });
  if ($("files-main-table")) $("files-main-table").classList.toggle("hidden", valid !== "list");
  if ($("files-grid")) $("files-grid").classList.toggle("hidden", valid !== "grid");
  if ($("files-charts")) $("files-charts").classList.toggle("hidden", valid !== "chart");
  renderCurrentFilesView(true);
}

function renderCurrentFilesView(force = false) {
  if (state.viewMode === "chart") {
    state.lastRenderedFilesSig = "";
    renderFilesCharts();
    return;
  }
  const sig = computeFilesRenderSig();
  if (!force && sig === state.lastRenderedFilesSig) {
    return;
  }
  state.lastRenderedFilesSig = sig;
  const scrollEl = $("files-scroll-container");
  const prevTop = scrollEl ? scrollEl.scrollTop : 0;
  if (state.viewMode === "grid") {
    renderFilesGrid();
  } else {
    renderFilesTable();
  }
  if (scrollEl && prevTop > 0) {
    scrollEl.scrollTop = prevTop;
  }
}

function isArchiveRec(rec) {
  const ext = (rec?.ext || "").toLowerCase().replace(/^\./, "");
  const name = (rec?.name || "").toLowerCase();
  return ["zip", "tar", "tgz", "gz"].includes(ext) || name.endsWith(".tar.gz");
}

const EDITABLE_TEXT_EXTS = [
  "md", "markdown", "txt", "json", "yaml", "yml", "log", "csv",
  "go", "js", "ts", "jsx", "tsx", "py", "html", "css", "xml",
  "sh", "bat", "ps1", "ini", "conf", "toml", "env", "sql",
  "c", "cpp", "h", "java", "rs", "php", "rb",
];

function isEditableTextRec(rec) {
  if (!rec || isArchiveRec(rec)) return false;
  const ext = (rec.ext || "").toLowerCase().replace(/^\./, "");
  return EDITABLE_TEXT_EXTS.includes(ext);
}

function canPreview(rec) {
  if (isArchiveRec(rec) || isEditableTextRec(rec)) return true;
  const ext = (rec?.ext || "").toLowerCase().replace(/^\./, "");
  return [
    "mp4", "webm", "mov", "m4v", "ogv",
    "mp3", "wav", "flac", "aac", "ogg", "m4a",
    "pdf",
    "docx", "xlsx", "pptx", "doc", "xls", "ppt",
    "jpg", "jpeg", "png", "gif", "webp", "svg", "bmp",
  ].includes(ext);
}

function normalizeUpdateMethod(method, ver = 1) {
  const m = String(method || "").trim();
  if (m) return m;
  return Number(ver || 1) <= 1 ? "initial_upload" : "upload_overwrite";
}

function formatUpdateMethodLabel(method, ver = 1) {
  const m = normalizeUpdateMethod(method, ver);
  switch (m) {
    case "online_edit":
      return "在线编辑更新";
    case "upload_overwrite":
      return "上传覆盖更新";
    case "version_upload":
      return "手动上传新版本";
    case "version_restore":
      return "历史版本回滚";
    case "local_import":
      return "本机零拷贝导入";
    case "mount_sync":
      return "目录挂载同步";
    case "initial_upload":
    default:
      return "初始上传";
  }
}

function formatUpdateMethodBadge(method, ver = 1) {
  const m = normalizeUpdateMethod(method, ver);
  const label = formatUpdateMethodLabel(m, ver);
  const cls = `method-${m.replace(/_/g, "-")}`;
  return `<span class="update-method-pill ${cls}">${escapeHtml(label)}</span>`;
}

function formatStorageModeBadge(rec) {
  const mode = String(rec?.storage_mode || "").trim();
  const badges = [];
  if (mode === "hardlink") {
    badges.push(`<span class="zc-badge zc-hardlink" title="同盘 OS 硬链接（零拷贝、原文件重命名/删除后仍可读）">硬链接</span>`);
  } else if (mode === "ref") {
    badges.push(`<span class="zc-badge zc-ref" title="跨盘零拷贝引用（直接读取原路径）">外链引用</span>`);
  } else if (mode === "mount") {
    badges.push(`<span class="zc-badge zc-mount" title="本机目录零拷贝挂载（随原目录自动同步）">目录挂载</span>`);
  }
  if (rec?.source_missing) {
    badges.push(`<span class="zc-badge zc-missing" title="原始路径上的源文件已移动或删除，外部引用失效">源文件已失效</span>`);
  }
  if (rec?.integrity_status === "corrupted") {
    badges.push(`<span class="zc-badge zc-corrupt" title="底层文件 SHA-256 校验与索引记录不一致，可能发生磁盘静默损坏">校验异常</span>`);
  }
  return badges.join("");
}

function matchExtForPlugin(supportedExts, fileName) {
  if (!Array.isArray(supportedExts) || supportedExts.length === 0) return false;
  if (supportedExts.some((x) => x === "*" || x === ".*")) return true;
  const name = String(fileName || "");
  const dot = name.lastIndexOf(".");
  if (dot < 0) return false;
  const ext = name.slice(dot).toLowerCase();
  const bare = ext.replace(/^\./, "");
  return supportedExts.some((x) => {
    const norm = String(x || "").trim().toLowerCase();
    return norm === ext || norm === bare || `.${norm}` === ext;
  });
}

function getMatchingCapabilitiesForFile(fileName) {
  return (state.clusterCapabilities || []).filter(
    (c) => Array.isArray(c.providers) && c.providers.length > 0 && matchExtForPlugin(c.supported_exts, fileName)
  );
}

function buildRecordMeta(rec) {
  const ext = (rec?.ext || "").toLowerCase().replace(/^\./, "");
  const online = rec.node_online !== false;
  const isArch = isArchiveRec(rec);
  const isImg = ["jpg", "jpeg", "png", "gif", "webp", "svg", "bmp"].includes(ext);
  const isMountRec = rec.storage_mode === "mount";
  const canPrev = canPreview(rec);
  const canEdit = isEditableTextRec(rec) && !isMountRec;
  const previewLabel = isArch ? "在线解压查看" : "在线打开";
  const previewBtn = canPrev
    ? `<button class="btn btn-primary btn-xs" data-action="preview" data-id="${rec.id}" ${online && !rec.source_missing ? "" : "disabled title='节点离线或源文件缺失'"}>${previewLabel}</button>`
    : "";
  const editBtn = canEdit
    ? `<button class="btn btn-ghost btn-xs" type="button" data-action="edit" data-id="${rec.id}" ${online && !rec.source_missing ? "" : "disabled title='节点离线或源文件缺失'"} title="在线文本编辑并保存为新版本">编辑</button>`
    : "";
  const pluginMatches = online && !rec.source_missing ? getMatchingCapabilitiesForFile(rec.name) : [];
  const pluginBtn = pluginMatches.length > 0
    ? `<button class="btn btn-ghost btn-xs" type="button" data-action="plugin-run" data-id="${escapeHtml(rec.id)}" title="调用集群算力插件处理此文件（可用能力：${escapeHtml(pluginMatches.map((c) => c.name || c.capability).join("、"))}）">算力</button>`
    : "";
  const verNum = Number(rec.version || 1);
  const verCnt = Number(rec.version_count || (Array.isArray(rec.versions) ? rec.versions.length + 1 : 1));
  const methodLabel = formatUpdateMethodLabel(rec.update_method, verNum);
  const showMethodInChip = verCnt > 1 || rec.update_method === "online_edit" || rec.update_method === "version_restore";
  const verBadgeHtml = `<button type="button" class="ver-chip ${verCnt > 1 ? "multi" : ""}" data-action="versions" data-id="${rec.id}" title="当前 v${verNum} · 更新方式：${escapeHtml(methodLabel)} · 更新用户：${escapeHtml(rec.uploader || "-")} · 点击查看版本历史">${ICON_VERSION_BRANCH_SVG}<span>v${verNum}${verCnt > 1 ? ` · ${verCnt}版` : ""}${showMethodInChip ? ` · ${escapeHtml(methodLabel)}` : ""}</span></button>`;
  const storageBadgeHtml = formatStorageModeBadge(rec);
  const verBtn = `<button class="btn btn-ghost btn-xs" type="button" data-action="versions" data-id="${rec.id}" title="文件版本历史控制">版本${verCnt > 1 ? `(${verCnt})` : ""}</button>`;
  const propsBtn = `<button class="btn btn-ghost btn-xs" type="button" data-action="props" data-id="${rec.id}">属性</button>`;
  const dlHtml = !online
    ? `<button class="btn btn-ghost btn-xs" type="button" disabled title="节点离线">离线</button>`
    : rec.source_missing
    ? `<button class="btn btn-ghost btn-xs" type="button" disabled title="源文件已移动或删除">失效</button>`
    : `<a class="btn btn-ghost btn-xs" href="/api/files/${rec.id}/download">下载</a><button class="btn btn-ghost btn-xs" type="button" data-action="queued-download" data-id="${rec.id}" title="加入下载队列（支持暂停/断点续传）">队列</button>`;
  const canDel = state.user && (state.user.role === "admin" || state.user.username === rec.uploader);
  const accessBtn = canDel && !isMountRec
    ? `<button class="btn btn-ghost btn-xs" type="button" data-action="access" data-id="${rec.id}" title="指定哪些用户可见与下载">${Array.isArray(rec.allowed_users) && rec.allowed_users.length > 0 ? "权限*" : "权限"}</button>`
    : "";
  const delBtn = canDel && !isMountRec
    ? `<button class="btn btn-danger btn-xs" data-action="delete" data-id="${rec.id}" ${online ? "" : "disabled title='节点离线'"}>删除</button>`
    : "";

  const folderHtml = rec.folder_path
    ? `<button type="button" class="folder-chip" data-filter-folder="${escapeHtml(rec.folder_path)}" title="点击按此文件夹搜索">${WIN_FOLDER_ICON_SVG}<span>${escapeHtml(rec.folder_path)}</span></button>`
    : "";

  const restrictedCount = Array.isArray(rec.allowed_users) ? rec.allowed_users.length : 0;
  const accessBadgeHtml = restrictedCount > 0
    ? `<span class="access-badge" title="仅指定用户可下载：${escapeHtml(rec.allowed_users.join(", "))}">${ICON_LOCK_SVG}<span>指定 ${restrictedCount} 人可下载</span></span>`
    : "";

  const adminPathHtml = state.user && state.user.role === "admin" && rec.storage_dir
    ? `<div class="admin-path-sub" title="文件当前存储位置：${escapeHtml(rec.storage_dir)}">目录：${escapeHtml(rec.storage_dir)}</div>`
    : "";

  const nodeCellHtml = rec.node_id
    ? `<button type="button" class="node-pill ${online ? "" : "offline"}" data-filter-node="${escapeHtml(rec.node_id)}" title="点击按此存储节点筛选">
        <span class="node-dot ${online ? "online" : "offline"}"></span>
        <span>${escapeHtml(rec.node_name || rec.node_id)}</span>
        <span class="node-os-tag">${escapeHtml(rec.node_os || "")}</span>
      </button>${adminPathHtml}`
    : `<span style="color:var(--muted)">本机</span>${adminPathHtml}`;

  const devCellHtml = rec.device_fp
    ? `<div class="dev-cell" title="${escapeHtml(rec.device_detail || "")}">
        <div class="dev-cell-top">
          <span class="dev-name">${escapeHtml(rec.device_name || "未知设备")}</span>
          <button type="button" class="fp-chip" data-filter-device="${escapeHtml(rec.device_fp)}" title="点击按此设备筛选">${escapeHtml(rec.device_fp)}</button>
        </div>
        <div class="dev-sub">${escapeHtml(rec.device_ip || "")}${rec.device_detail ? " · " + escapeHtml(rec.device_detail) : ""}</div>
      </div>`
    : `<span style="color:var(--muted)">未记录</span>`;

  return {
    ext,
    online,
    isImg,
    canPrev,
    canEdit,
    previewBtn,
    editBtn,
    pluginBtn,
    verBadgeHtml,
    storageBadgeHtml,
    verBtn,
    propsBtn,
    accessBtn,
    dlHtml,
    delBtn,
    folderHtml,
    accessBadgeHtml,
    nodeCellHtml,
    devCellHtml,
  };
}

function updateEmptyFilesState(emptyEl) {
  if (!emptyEl) return;
  const selectedOffline =
    state.selectedNode &&
    state.selectedNode !== "all" &&
    state.pool &&
    Array.isArray(state.pool.nodes) &&
    state.pool.nodes.some((n) => n.node_id === state.selectedNode && !n.online);
  emptyEl.textContent = selectedOffline
    ? "该节点当前离线，其上的文件已自动隐藏；待节点上线恢复心跳后会自动重新显示。"
    : "暂无符合条件的文件，试着拖拽文件到上方上传条吧。";
  emptyEl.classList.remove("hidden");
}

function renderFilesTable() {
  const tbody = $("files-tbody");
  const empty = $("empty-state");
  if (!tbody) return;

  if (state.files.length === 0) {
    tbody.replaceChildren();
    updateEmptyFilesState(empty);
    return;
  }
  empty.classList.add("hidden");

  const frag = document.createDocumentFragment();
  for (const rec of state.files) {
    const meta = buildRecordMeta(rec);
    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td class="file-name-cell" data-label="文件名" title="${escapeHtml(rec.name)}">
        <div class="file-name-wrap">
          <div class="file-main-row">
            <span class="win-file-icon-sm">${getWinFileIconSvg(rec, 22)}</span>
            <span class="file-main-name">${escapeHtml(rec.name)}</span>
            ${meta.verBadgeHtml}
            ${meta.storageBadgeHtml}
          </div>
          ${meta.folderHtml}
          ${meta.accessBadgeHtml}
        </div>
      </td>
      <td data-label="分类"><span class="cat-tag">${escapeHtml(categoryNames[rec.category] || rec.category)}</span></td>
      <td data-label="大小">${formatSize(rec.size)}</td>
      <td data-label="更新时间">${formatTime(rec.uploaded_at)}</td>
      <td data-label="上传者">${escapeHtml(rec.uploader)}</td>
      <td data-label="存储节点">${meta.nodeCellHtml}</td>
      <td class="col-device" data-label="上传设备">${meta.devCellHtml}</td>
      <td class="col-actions" data-label="操作">
        <div class="row-actions">
          ${meta.previewBtn}
          ${meta.editBtn}
          ${meta.pluginBtn}
          ${meta.verBtn}
          ${meta.propsBtn}
          ${meta.accessBtn}
          ${meta.dlHtml}
          ${meta.delBtn}
        </div>
      </td>
    `;
    frag.appendChild(tr);
  }
  tbody.replaceChildren(frag);
}

function renderFilesGrid() {
  const grid = $("files-grid");
  const empty = $("empty-state");
  if (!grid) return;

  if (state.files.length === 0) {
    grid.replaceChildren();
    updateEmptyFilesState(empty);
    return;
  }
  empty.classList.add("hidden");

  const frag = document.createDocumentFragment();
  for (const rec of state.files) {
    const meta = buildRecordMeta(rec);
    const card = document.createElement("div");
    card.className = "file-card";
    const catKey = rec.category || "other";
    const thumbClickable = meta.canPrev && meta.online;
    const thumbAttrs = thumbClickable
      ? `data-action="preview" data-id="${rec.id}" title="点击在线预览"`
      : "";
    const iconSvg = getWinFileIconSvg(rec, 68);
    const thumbInner = meta.isImg && meta.online
      ? `<img loading="lazy" src="/api/files/${rec.id}/raw" alt="${escapeHtml(rec.name)}" onerror="this.style.display='none';this.nextElementSibling.classList.remove('hidden');" />
         <div class="thumb-icon-wrap hidden">${iconSvg}</div>`
      : `<div class="thumb-icon-wrap">${iconSvg}</div>`;

    const fpBadge = rec.device_fp
      ? `<button type="button" class="fp-chip" data-filter-device="${escapeHtml(rec.device_fp)}" title="${escapeHtml((rec.device_name || "设备") + (rec.device_detail ? " · " + rec.device_detail : ""))}">${escapeHtml(rec.device_fp)}</button>`
      : "";

    card.innerHTML = `
      <div class="file-card-thumb cat-${escapeHtml(catKey)} ${thumbClickable ? "clickable" : ""}" ${thumbAttrs}>
        ${thumbInner}
        <span class="thumb-ext-badge">${escapeHtml((meta.ext || catKey || "FILE").toUpperCase())}</span>
        ${meta.online ? "" : `<span class="thumb-offline">节点离线</span>`}
      </div>
      <div class="file-card-body">
        <div style="display:flex;align-items:center;justify-content:space-between;gap:6px;">
          <div class="file-card-title" title="${escapeHtml(rec.name)}">${escapeHtml(rec.name)}</div>
          ${meta.verBadgeHtml}
        </div>
        ${meta.folderHtml}
        ${meta.accessBadgeHtml}
        ${meta.storageBadgeHtml ? `<div class="file-card-meta">${meta.storageBadgeHtml}</div>` : ""}
        <div class="file-card-meta">
          <span>${formatSize(rec.size)}</span>
          <span>·</span>
          <span>${escapeHtml(rec.uploader)}</span>
          <span>·</span>
          <span>${formatTime(rec.uploaded_at)}</span>
        </div>
        <div class="file-card-meta">
          ${meta.nodeCellHtml}
          ${fpBadge}
        </div>
      </div>
      <div class="file-card-actions">
        ${meta.previewBtn}
        ${meta.editBtn}
        ${meta.pluginBtn}
        ${meta.verBtn}
        ${meta.propsBtn}
        ${meta.accessBtn}
        ${meta.dlHtml}
        ${meta.delBtn}
      </div>
    `;
    frag.appendChild(card);
  }
  grid.replaceChildren(frag);
}

function renderFilesCharts() {
  const wrap = $("files-charts");
  const empty = $("empty-state");
  if (!wrap) return;
  if (empty) empty.classList.add("hidden");

  const files = state.files || [];
  let totalBytes = 0;
  const catMap = new Map();
  const monthMap = new Map();
  const devMap = new Map();
  const nodeFileCount = new Map();

  for (const rec of files) {
    const sz = Number(rec.size || 0);
    totalBytes += sz;

    const catKey = rec.category || "other";
    const curCat = catMap.get(catKey) || {
      key: catKey,
      label: categoryNames[catKey] || catKey,
      count: 0,
      size: 0,
      color: categoryColors[catKey] || categoryColors.other,
    };
    curCat.count += 1;
    curCat.size += sz;
    catMap.set(catKey, curCat);

    const monthKey = rec.month_key || String(rec.uploaded_at || "").slice(0, 7) || "未知";
    const curMonth = monthMap.get(monthKey) || { key: monthKey, count: 0, size: 0 };
    curMonth.count += 1;
    curMonth.size += sz;
    monthMap.set(monthKey, curMonth);

    const devKey = rec.device_fp || "__unknown__";
    const curDev = devMap.get(devKey) || {
      key: devKey,
      label: rec.device_fp ? `${rec.device_name || "未知设备"} (${rec.device_fp})` : "未记录指纹",
      count: 0,
      size: 0,
    };
    curDev.count += 1;
    curDev.size += sz;
    devMap.set(devKey, curDev);

    if (rec.node_id) {
      nodeFileCount.set(rec.node_id, (nodeFileCount.get(rec.node_id) || 0) + 1);
    }
  }

  const metric = state.chartMetric === "size" ? "size" : "count";
  const catList = [...catMap.values()].sort((a, b) => (metric === "size" ? b.size - a.size : b.count - a.count));
  const totalMetric = catList.reduce((acc, item) => acc + (metric === "size" ? item.size : item.count), 0);

  const radius = 62;
  const circ = 2 * Math.PI * radius;
  let offset = 0;
  const circlesHtml = totalMetric > 0
    ? catList
        .map((item) => {
          const val = metric === "size" ? item.size : item.count;
          if (val <= 0) return "";
          const seg = Math.min(circ, (val / totalMetric) * circ);
          const dashArray = `${seg.toFixed(2)} ${Math.max(0, circ - seg).toFixed(2)}`;
          const circle = `<circle cx="82" cy="82" r="${radius}" fill="none" stroke="${item.color}" stroke-width="20" stroke-dasharray="${dashArray}" stroke-dashoffset="${(-offset).toFixed(2)}" />`;
          offset += seg;
          return circle;
        })
        .join("")
    : "";

  const legendHtml = catList.length > 0
    ? catList
        .map((item) => {
          const val = metric === "size" ? item.size : item.count;
          const pct = totalMetric > 0 ? Math.round((val / totalMetric) * 100) : 0;
          const active = state.selectedCategory === item.key ? "active" : "";
          const metricText = metric === "size" ? formatSize(item.size) : `${item.count} 个`;
          return `
            <button type="button" class="legend-row ${active}" data-chart-category="${escapeHtml(item.key)}">
              <span class="legend-left">
                <span class="legend-dot" style="background:${item.color}"></span>
                <span>${escapeHtml(item.label)}</span>
              </span>
              <span class="legend-right">${metricText} (${pct}%)</span>
            </button>
          `;
        })
        .join("")
    : `<div class="chart-empty">暂无分类统计数据</div>`;

  const monthsAll = [...monthMap.values()].sort((a, b) => a.key.localeCompare(b.key));
  const monthsRecent = monthsAll.slice(-12);
  const maxMonthCount = monthsRecent.reduce((m, b) => Math.max(m, b.count), 1);
  const monthBarsHtml = monthsRecent.length > 0
    ? `<div class="vbar-chart">
        ${monthsRecent
          .map((m) => {
            const pct = Math.max(8, Math.round((m.count / maxMonthCount) * 100));
            const active = state.selectedMonth === m.key ? "active" : "";
            return `
              <button type="button" class="vbar-col ${active}" data-chart-month="${escapeHtml(m.key)}" title="${escapeHtml(m.key)} · ${m.count} 个 · ${formatSize(m.size)}">
                <span class="vbar-val">${m.count}</span>
                <div class="vbar-track">
                  <div class="vbar-fill" style="height:${pct}%"></div>
                </div>
                <span class="vbar-lbl">${escapeHtml(m.key)}</span>
              </button>
            `;
          })
          .join("")}
      </div>`
    : `<div class="chart-empty">当前筛选条件下暂无月份记录</div>`;

  const nodes = state.pool?.nodes || [];
  const nodesHtml = nodes.length > 0
    ? `<div class="hbar-list">
        ${nodes
          .map((n) => {
            const pct = n.quota_bytes > 0 ? Math.min(100, Math.round((n.used_bytes / n.quota_bytes) * 100)) : 0;
            const active = state.selectedNode === n.node_id ? "active" : "";
            const filteredCount = nodeFileCount.get(n.node_id) || 0;
            return `
              <button type="button" class="hbar-row ${active}" data-chart-node="${escapeHtml(n.node_id)}">
                <div class="hbar-head">
                  <span><span class="node-dot ${n.online ? "online" : "offline"}" style="display:inline-block;margin-right:6px;"></span><strong>${escapeHtml(n.node_name)}</strong> (${filteredCount} 文件)</span>
                  <span style="color:var(--muted)">${formatSize(n.used_bytes)} / ${formatQuotaDisplay(n.quota_bytes)} (${pct}%)</span>
                </div>
                <div class="hbar-track"><div class="hbar-fill" style="width:${pct}%"></div></div>
              </button>
            `;
          })
          .join("")}
      </div>`
    : `<div class="chart-empty">暂无集群节点信息</div>`;

  const devTop = [...devMap.values()].sort((a, b) => b.count - a.count).slice(0, 8);
  const maxDevCount = devTop.length > 0 ? Math.max(1, devTop[0].count) : 1;
  const devsHtml = devTop.length > 0
    ? `<div class="hbar-list">
        ${devTop
          .map((d) => {
            const pct = Math.max(6, Math.round((d.count / maxDevCount) * 100));
            const clickable = d.key !== "__unknown__";
            const active = clickable && state.selectedDevice === d.key ? "active" : "";
            const attrs = clickable ? `data-chart-device="${escapeHtml(d.key)}"` : "";
            return `
              <button type="button" class="hbar-row ${active}" ${attrs}>
                <div class="hbar-head">
                  <span>${escapeHtml(d.label)}</span>
                  <span style="color:var(--muted)">${d.count} 个 · ${formatSize(d.size)}</span>
                </div>
                <div class="hbar-track"><div class="hbar-fill" style="width:${pct}%"></div></div>
              </button>
            `;
          })
          .join("")}
      </div>`
    : `<div class="chart-empty">当前筛选条件下暂无设备上传记录</div>`;

  const onlineNodesCount = state.pool?.online_nodes || nodes.filter((n) => n.online).length || 1;
  wrap.innerHTML = `
    <div class="chart-kpi-grid">
      <div class="chart-kpi-card">
        <div class="chart-kpi-val">${files.length} 个</div>
        <div class="chart-kpi-lbl">当前视图文件总数</div>
      </div>
      <div class="chart-kpi-card">
        <div class="chart-kpi-val">${formatSize(totalBytes)}</div>
        <div class="chart-kpi-lbl">当前视图占用空间</div>
      </div>
      <div class="chart-kpi-card">
        <div class="chart-kpi-val">${catMap.size} 类</div>
        <div class="chart-kpi-lbl">涉及文件分类数</div>
      </div>
      <div class="chart-kpi-card">
        <div class="chart-kpi-val">${devMap.size} 设备 / ${onlineNodesCount} 节点</div>
        <div class="chart-kpi-lbl">活跃设备与在线存储节点</div>
      </div>
    </div>

    <div class="charts-grid">
      <div class="chart-card">
        <div class="chart-card-head">
          <div>
            <div class="chart-card-title">文件类型分布</div>
            <div class="chart-card-sub">点击右侧分类可联动筛选文件</div>
          </div>
          <div class="chart-metric-switch">
            <button type="button" class="btn btn-xs ${metric === "count" ? "btn-primary" : "btn-ghost"}" data-chart-metric="count">按文件数</button>
            <button type="button" class="btn btn-xs ${metric === "size" ? "btn-primary" : "btn-ghost"}" data-chart-metric="size">按空间大小</button>
          </div>
        </div>
        <div class="donut-wrap">
          <div class="donut-svg-wrap">
            <svg viewBox="0 0 164 164">
              <circle cx="82" cy="82" r="${radius}" fill="none" stroke="var(--surface-3)" stroke-width="20" />
              ${circlesHtml}
            </svg>
            <div class="donut-center">
              <div class="donut-center-val">${metric === "size" ? formatSize(totalBytes) : `${files.length} 个`}</div>
              <div class="donut-center-lbl">${metric === "size" ? "总大小" : "总文件"}</div>
            </div>
          </div>
          <div class="donut-legend">${legendHtml}</div>
        </div>
      </div>

      <div class="chart-card">
        <div class="chart-card-head">
          <div>
            <div class="chart-card-title">按月份上传趋势</div>
            <div class="chart-card-sub">最近 ${monthsRecent.length} 个月，点击柱条筛选月份</div>
          </div>
        </div>
        ${monthBarsHtml}
      </div>

      <div class="chart-card">
        <div class="chart-card-head">
          <div>
            <div class="chart-card-title">集群存储节点容量分布</div>
            <div class="chart-card-sub">点击节点查看该节点存储的文件</div>
          </div>
        </div>
        ${nodesHtml}
      </div>

      <div class="chart-card">
        <div class="chart-card-head">
          <div>
            <div class="chart-card-title">上传设备指纹活跃榜</div>
            <div class="chart-card-sub">自动识别上传终端，点击按设备筛选</div>
          </div>
        </div>
        ${devsHtml}
      </div>
    </div>
  `;
}

async function readAllDirectoryEntries(dirReader) {
  const entries = [];
  while (true) {
    const batch = await new Promise((resolve, reject) => {
      dirReader.readEntries(resolve, reject);
    });
    if (!batch || batch.length === 0) break;
    entries.push(...batch);
  }
  return entries;
}

async function walkFileSystemEntry(entry, prefix = "") {
  if (!entry) return [];
  if (entry.isFile) {
    const file = await new Promise((resolve, reject) => entry.file(resolve, reject));
    const rel = prefix ? `${prefix}/${file.name}` : file.name;
    return [{ file, relativePath: rel }];
  }
  if (entry.isDirectory) {
    const dirPrefix = prefix ? `${prefix}/${entry.name}` : entry.name;
    const reader = entry.createReader();
    const children = await readAllDirectoryEntries(reader);
    const out = [];
    for (const child of children) {
      const sub = await walkFileSystemEntry(child, dirPrefix);
      out.push(...sub);
    }
    return out;
  }
  return [];
}

async function collectEntriesFromDataTransfer(dataTransfer) {
  if (!dataTransfer) return [];
  // Must synchronously snapshot webkitGetAsEntry & files before any await!
  const rawItems = Array.from(dataTransfer.items || []);
  const fsEntries = [];
  for (const item of rawItems) {
    if (item.kind === "file" && typeof item.webkitGetAsEntry === "function") {
      const entry = item.webkitGetAsEntry();
      if (entry) fsEntries.push(entry);
    }
  }
  const fallbackFiles = Array.from(dataTransfer.files || []);

  if (fsEntries.length > 0) {
    const collected = [];
    for (const entry of fsEntries) {
      try {
        const sub = await walkFileSystemEntry(entry, "");
        collected.push(...sub);
      } catch {}
    }
    if (collected.length > 0) return collected;
  }

  return fallbackFiles.map((file) => ({
    file,
    relativePath: file.webkitRelativePath || file.name,
  }));
}

function sendUploadBatchXHR(items, targetNode, onProgress) {
  return new Promise((resolve, reject) => {
    const formData = new FormData();
    for (const item of items) {
      formData.append("files", item.file);
      formData.append("relative_paths", item.relativePath || item.file.name);
    }
    formData.append("target_node", targetNode);
    if (Array.isArray(state.uploadAllowedUsers) && state.uploadAllowedUsers.length > 0) {
      formData.append("allowed_users", JSON.stringify(state.uploadAllowedUsers));
    }
    formData.append("device_fp", state.device.fp || "");
    formData.append("device_name", state.device.name || "");
    formData.append("device_detail", state.device.detail || "");

    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/upload", true);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && typeof onProgress === "function") {
        onProgress(e.loaded);
      }
    };
    xhr.onload = () => {
      let data = {};
      try {
        data = JSON.parse(xhr.responseText || "{}");
      } catch {}
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(data.uploaded || []);
      } else {
        reject(new Error(data.error || `上传失败 (${xhr.status})`));
      }
    };
    xhr.onerror = () => reject(new Error("网络错误，上传失败"));
    xhr.send(formData);
  });
}

const TRANSFER_CHUNK_BYTES = 4 * 1024 * 1024;
const MAX_CONCURRENT_UPLOADS = 3;
const MAX_CONCURRENT_DOWNLOADS = 2;
const CHUNK_UPLOAD_WORKERS = 3;
let transferRenderTimer = null;

function formatETA(sec) {
  if (sec == null || !isFinite(sec) || sec < 0) return "-";
  const s = Math.round(sec);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m${s % 60}s`;
  return `${Math.floor(m / 60)}h${m % 60}m`;
}

function hasActiveTransfers() {
  return state.transfers.some((t) => t.status === "running" || t.status === "queued");
}

function setTransferTab(tab) {
  state.transferTab = ["upload", "download", "done"].includes(tab) ? tab : "upload";
  document.querySelectorAll("[data-transfer-tab]").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.transferTab === state.transferTab);
  });
  renderTransferDrawer();
}

function setTransferDrawerOpen(open) {
  state.transferOpen = Boolean(open);
  if ($("transfer-drawer")) {
    $("transfer-drawer").classList.toggle("hidden", !state.transferOpen);
  }
  renderTransferDrawer();
}

function scheduleTransferRender() {
  if (transferRenderTimer) return;
  transferRenderTimer = setTimeout(() => {
    transferRenderTimer = null;
    renderTransferDrawer();
  }, 120);
}

function renderTransferDrawer() {
  const fab = $("transfer-fab");
  const badge = $("transfer-fab-badge");
  const listEl = $("transfer-queue-list");
  if (!fab || !listEl) return;

  const uploads = state.transfers.filter((t) => t.direction === "upload" && t.status !== "done");
  const downloads = state.transfers.filter((t) => t.direction === "download" && t.status !== "done");
  const doneList = state.transfers.filter((t) => t.status === "done");
  const activeCnt = state.transfers.filter((t) => t.status === "running" || t.status === "queued").length;

  fab.classList.toggle("hidden", state.transfers.length === 0);
  if (badge) {
    badge.textContent = String(activeCnt);
    badge.classList.toggle("hidden", activeCnt === 0);
  }
  if ($("transfer-count-upload")) $("transfer-count-upload").textContent = String(uploads.length);
  if ($("transfer-count-download")) $("transfer-count-download").textContent = String(downloads.length);
  if ($("transfer-count-done")) $("transfer-count-done").textContent = String(doneList.length);

  // Sync compact top upload bar cleanly while uploads are active.
  const wrap = $("upload-progress-wrap");
  const fill = $("upload-progress-fill");
  const text = $("upload-progress-text");
  if (wrap && fill && text) {
    const activeUps = state.transfers.filter((t) => t.direction === "upload" && (t.status === "running" || t.status === "queued"));
    if (activeUps.length > 0) {
      const tot = activeUps.reduce((s, t) => s + Math.max(1, t.size), 0);
      const got = activeUps.reduce((s, t) => s + Math.min(t.loadedBytes || 0, Math.max(1, t.size)), 0);
      const pct = Math.min(99, Math.round((got * 100) / Math.max(1, tot)));
      wrap.classList.remove("hidden");
      fill.style.width = `${pct}%`;
      text.textContent = `${pct}% (${activeUps.length}个)`;
    } else if (!hasActiveTransfers()) {
      wrap.classList.add("hidden");
    }
  }

  const visible =
    state.transferTab === "download"
      ? downloads
      : state.transferTab === "done"
      ? doneList
      : uploads;

  if (visible.length === 0) {
    listEl.innerHTML = `<div style="color:var(--muted);font-size:12px;text-align:center;padding:24px 10px;">当前列表暂无传输任务</div>`;
    return;
  }

  listEl.innerHTML = visible
    .map((t) => {
      const total = Math.max(1, Number(t.size || 0));
      const got = t.status === "done" ? total : Math.min(total, Number(t.loadedBytes || 0));
      const pct = t.status === "done" ? 100 : Math.min(99, Math.round((got * 100) / total));
      const statusText =
        t.status === "running"
          ? `${formatRate(t.speedBps || 0)} · 剩余 ${formatETA(t.etaSec)}`
          : t.status === "queued"
          ? "排队等待中"
          : t.status === "paused"
          ? "已暂停（支持断点续传）"
          : t.status === "error"
          ? `失败：${t.error || "网络错误"}`
          : t.note || "已完成";

      const actionBtns = [];
      if (t.status === "running" || t.status === "queued") {
        actionBtns.push(`<button type="button" class="btn btn-ghost btn-xs" data-transfer-act="pause" data-tid="${escapeHtml(t.id)}">暂停</button>`);
      } else if (t.status === "paused" || t.status === "error") {
        actionBtns.push(`<button type="button" class="btn btn-primary btn-xs" data-transfer-act="resume" data-tid="${escapeHtml(t.id)}">${t.status === "error" ? "重试" : "继续"}</button>`);
      }
      if (t.status !== "done") {
        actionBtns.push(`<button type="button" class="btn btn-danger btn-xs" data-transfer-act="cancel" data-tid="${escapeHtml(t.id)}">取消</button>`);
      } else {
        actionBtns.push(`<button type="button" class="btn btn-ghost btn-xs" data-transfer-act="remove" data-tid="${escapeHtml(t.id)}">清除</button>`);
      }

      return `
        <div class="transfer-item">
          <div class="transfer-item-head">
            <span class="transfer-item-name" title="${escapeHtml(t.name)}">${escapeHtml(t.name)}</span>
            <div class="row-actions">${actionBtns.join("")}</div>
          </div>
          <div class="transfer-bar">
            <div class="transfer-bar-fill ${t.status === "done" ? "done" : t.status === "error" ? "error" : ""}" style="width:${pct}%"></div>
          </div>
          <div class="transfer-item-meta">
            <span>${formatSize(got)} / ${formatSize(t.size)} (${pct}%)</span>
            <span>${escapeHtml(statusText)}</span>
          </div>
        </div>
      `;
    })
    .join("");
}

async function computeBlobSHA256(blob) {
  if (!window.crypto || !window.crypto.subtle || typeof blob.arrayBuffer !== "function") {
    return "";
  }
  try {
    const buf = await blob.arrayBuffer();
    const digest = await window.crypto.subtle.digest("SHA-256", buf);
    return Array.from(new Uint8Array(digest))
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
  } catch {
    return "";
  }
}

function sendSingleUploadTaskXHR(task) {
  return new Promise((resolve, reject) => {
    const formData = new FormData();
    formData.append("files", task.file);
    formData.append("relative_paths", task.relativePath || task.file.name);
    formData.append("target_node", task.targetNode || "auto");
    if (Array.isArray(task.allowedUsers) && task.allowedUsers.length > 0) {
      formData.append("allowed_users", JSON.stringify(task.allowedUsers));
    }
    formData.append("device_fp", state.device.fp || "");
    formData.append("device_name", state.device.name || "");
    formData.append("device_detail", state.device.detail || "");

    const xhr = new XMLHttpRequest();
    task.abortXhr = xhr;
    let lastAt = Date.now();
    let lastLoaded = 0;

    xhr.open("POST", "/api/upload", true);
    xhr.upload.onprogress = (e) => {
      if (!e.lengthComputable) return;
      const now = Date.now();
      const dt = Math.max(0.05, (now - lastAt) / 1000);
      const delta = Math.max(0, e.loaded - lastLoaded);
      if (dt >= 0.25) {
        task.speedBps = delta / dt;
        task.etaSec = task.speedBps > 0 ? Math.max(0, (task.size - e.loaded) / task.speedBps) : null;
        lastAt = now;
        lastLoaded = e.loaded;
      }
      task.loadedBytes = Math.min(task.size, e.loaded);
      scheduleTransferRender();
    };
    xhr.onload = () => {
      task.abortXhr = null;
      let data = {};
      try {
        data = JSON.parse(xhr.responseText || "{}");
      } catch {}
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(data.uploaded?.[0] || null);
      } else {
        reject(new Error(data.error || `上传失败 (${xhr.status})`));
      }
    };
    xhr.onabort = () => {
      task.abortXhr = null;
      const err = new Error("ABORTED");
      err.name = "AbortError";
      reject(err);
    };
    xhr.onerror = () => {
      task.abortXhr = null;
      reject(new Error("网络错误，上传失败"));
    };
    xhr.send(formData);
  });
}

async function runChunkedUploadTask(task) {
  task.abortCtrl = new AbortController();
  const sig = task.abortCtrl.signal;
  const folderPath = task.folderPath || "";
  const fileSHA = task.size <= 128 * 1024 * 1024 ? await computeBlobSHA256(task.file) : "";
  if (sig.aborted) throw new DOMException("Aborted", "AbortError");

  const initData = await api("/api/uploads/init", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    signal: sig,
    body: JSON.stringify({
      name: task.name,
      folder_path: folderPath,
      file_size: task.size,
      chunk_size: TRANSFER_CHUNK_BYTES,
      file_sha256: fileSHA,
      target_node: task.targetNode || "auto",
      allowed_users: task.allowedUsers || [],
    }),
  });

  if (initData.instant) {
    task.loadedBytes = task.size;
    task.speedBps = 0;
    task.note = "SHA-256 秒传成功";
    return initData.file || null;
  }

  task.uploadId = initData.upload_id;
  task.totalChunks = Number(initData.total_chunks || 1);
  const doneSet = new Set(Array.isArray(initData.uploaded_chunks) ? initData.uploaded_chunks : []);
  task.uploadedSet = doneSet;

  const chunkSize = Number(initData.chunk_size || TRANSFER_CHUNK_BYTES);
  const sizeForChunk = (idx) => {
    if (task.size === 0) return 0;
    if (idx < task.totalChunks - 1) return chunkSize;
    return Math.max(0, task.size - idx * chunkSize);
  };
  let uploadedBytes = 0;
  for (const idx of doneSet) {
    uploadedBytes += sizeForChunk(idx);
  }
  task.loadedBytes = uploadedBytes;
  scheduleTransferRender();

  const pending = [];
  for (let i = 0; i < task.totalChunks; i++) {
    if (!doneSet.has(i)) pending.push(i);
  }

  let cursor = 0;
  let sampleAt = Date.now();
  let sampleBytes = 0;

  const worker = async () => {
    while (cursor < pending.length) {
      if (sig.aborted) throw new DOMException("Aborted", "AbortError");
      const idx = pending[cursor++];
      const start = idx * chunkSize;
      const end = Math.min(task.size, start + chunkSize);
      const blob = task.file.slice(start, end);
      const chunkSHA = await computeBlobSHA256(blob);
      if (sig.aborted) throw new DOMException("Aborted", "AbortError");

      await api(`/api/uploads/${encodeURIComponent(task.uploadId)}/chunk/${idx}`, {
        method: "PUT",
        headers: chunkSHA ? { "X-Chunk-SHA256": chunkSHA } : {},
        body: blob,
        signal: sig,
      });
      doneSet.add(idx);
      const cLen = blob.size;
      task.loadedBytes = Math.min(task.size, task.loadedBytes + cLen);
      sampleBytes += cLen;
      const now = Date.now();
      const dt = (now - sampleAt) / 1000;
      if (dt >= 0.25) {
        task.speedBps = sampleBytes / dt;
        task.etaSec = task.speedBps > 0 ? Math.max(0, (task.size - task.loadedBytes) / task.speedBps) : null;
        sampleAt = now;
        sampleBytes = 0;
      }
      scheduleTransferRender();
    }
  };

  const workers = Array.from({ length: Math.min(CHUNK_UPLOAD_WORKERS, Math.max(1, pending.length)) }, () => worker());
  await Promise.all(workers);
  if (sig.aborted) throw new DOMException("Aborted", "AbortError");

  task.note = "正在合并校验分片...";
  scheduleTransferRender();
  const completeData = await api(`/api/uploads/${encodeURIComponent(task.uploadId)}/complete`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    signal: sig,
    body: JSON.stringify({ target_node: task.targetNode || "auto" }),
  });
  task.loadedBytes = task.size;
  task.speedBps = 0;
  task.note = "分片合并校验通过";
  return completeData.file || null;
}

async function runUploadTask(task) {
  task.status = "running";
  task.error = "";
  task.speedBps = 0;
  state.isUploadingSelf = true;
  renderTransferDrawer();

  try {
    const rec = task.size >= TRANSFER_CHUNK_BYTES
      ? await runChunkedUploadTask(task)
      : await sendSingleUploadTaskXHR(task);
    task.status = "done";
    task.loadedBytes = task.size;
    task.speedBps = 0;
    task.etaSec = 0;
    if (!task.note) {
      task.note = rec?.node_name ? `已存至 ${rec.node_name}` : "上传完成";
    }
    renderTransferDrawer();
    await refreshAll();
  } catch (err) {
    if (err && err.name === "AbortError") {
      if (task.status !== "cancelled") {
        task.status = "paused";
      }
    } else {
      task.status = "error";
      task.error = err?.message || "上传失败";
      showToast(`${task.name}：${task.error}`, "error");
    }
    task.speedBps = 0;
    renderTransferDrawer();
  } finally {
    task.abortCtrl = null;
    task.abortXhr = null;
    setTimeout(() => {
      if (!hasActiveTransfers()) {
        state.isUploadingSelf = false;
      }
    }, 600);
    pumpTransferQueue();
  }
}

function triggerBlobDownload(blob, filename) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename || "download";
  a.style.display = "none";
  document.body.appendChild(a);
  a.click();
  setTimeout(() => {
    URL.revokeObjectURL(url);
    a.remove();
  }, 8000);
}

async function runDownloadTask(task) {
  task.status = "running";
  task.error = "";
  task.abortCtrl = new AbortController();
  const sig = task.abortCtrl.signal;
  if (!Array.isArray(task.parts)) task.parts = [];
  renderTransferDrawer();

  let sampleAt = Date.now();
  let sampleBytes = 0;

  try {
    if (task.size <= 0) {
      const res = await fetch(`/api/files/${encodeURIComponent(task.fileId)}/download`, { signal: sig });
      if (!res.ok) throw new Error(`下载失败 (${res.status})`);
      const blob = await res.blob();
      task.status = "done";
      task.size = blob.size;
      task.loadedBytes = blob.size;
      task.note = "下载完成";
      triggerBlobDownload(blob, task.name);
      renderTransferDrawer();
      return;
    }

    while (task.loadedBytes < task.size) {
      if (sig.aborted) throw new DOMException("Aborted", "AbortError");
      const start = task.loadedBytes;
      const end = Math.min(task.size - 1, start + TRANSFER_CHUNK_BYTES - 1);
      const res = await fetch(`/api/files/${encodeURIComponent(task.fileId)}/download`, {
        headers: { Range: `bytes=${start}-${end}` },
        signal: sig,
      });
      if (!res.ok) {
        const msg = await res.text().catch(() => "");
        throw new Error(msg || `下载失败 (${res.status})`);
      }
      const buf = await res.arrayBuffer();
      if (sig.aborted) throw new DOMException("Aborted", "AbortError");

      if (res.status === 200) {
        // Server sent full body without partial slicing; save directly cleanly.
        const blob = new Blob([buf]);
        task.parts = [];
        task.loadedBytes = blob.size;
        task.status = "done";
        task.speedBps = 0;
        task.note = "下载完成";
        triggerBlobDownload(blob, task.name);
        renderTransferDrawer();
        return;
      }

      task.parts.push(buf);
      task.loadedBytes += buf.byteLength;
      sampleBytes += buf.byteLength;
      const now = Date.now();
      const dt = (now - sampleAt) / 1000;
      if (dt >= 0.25) {
        task.speedBps = sampleBytes / dt;
        task.etaSec = task.speedBps > 0 ? Math.max(0, (task.size - task.loadedBytes) / task.speedBps) : null;
        sampleAt = now;
        sampleBytes = 0;
      }
      scheduleTransferRender();
      if (buf.byteLength === 0) break;
    }

    const finalBlob = new Blob(task.parts);
    task.parts = [];
    task.status = "done";
    task.speedBps = 0;
    task.etaSec = 0;
    task.note = "下载完成";
    triggerBlobDownload(finalBlob, task.name);
    showToast(`已下载完成：${task.name}`, "success");
    renderTransferDrawer();
  } catch (err) {
    if (err && err.name === "AbortError") {
      if (task.status !== "cancelled") {
        task.status = "paused";
      } else {
        task.parts = [];
      }
    } else {
      task.status = "error";
      task.error = err?.message || "下载失败";
      showToast(`${task.name}：${task.error}`, "error");
    }
    task.speedBps = 0;
    renderTransferDrawer();
  } finally {
    task.abortCtrl = null;
    pumpTransferQueue();
  }
}

function pumpTransferQueue() {
  let activeUp = state.transfers.filter((t) => t.direction === "upload" && t.status === "running").length;
  for (const t of state.transfers) {
    if (activeUp >= MAX_CONCURRENT_UPLOADS) break;
    if (t.direction === "upload" && t.status === "queued") {
      activeUp++;
      runUploadTask(t);
    }
  }

  let activeDl = state.transfers.filter((t) => t.direction === "download" && t.status === "running").length;
  for (const t of state.transfers) {
    if (activeDl >= MAX_CONCURRENT_DOWNLOADS) break;
    if (t.direction === "download" && t.status === "queued") {
      activeDl++;
      runDownloadTask(t);
    }
  }
}

function pauseTransferTask(tid) {
  const t = state.transfers.find((x) => x.id === tid);
  if (!t) return;
  if (t.status === "queued") {
    t.status = "paused";
    t.speedBps = 0;
    renderTransferDrawer();
    return;
  }
  if (t.status === "running") {
    t.status = "paused";
    if (t.abortCtrl) t.abortCtrl.abort();
    if (t.abortXhr) t.abortXhr.abort();
    renderTransferDrawer();
  }
}

function resumeTransferTask(tid) {
  const t = state.transfers.find((x) => x.id === tid);
  if (!t) return;
  if (t.status === "paused" || t.status === "error") {
    t.status = "queued";
    t.error = "";
    renderTransferDrawer();
    pumpTransferQueue();
  }
}

function cancelTransferTask(tid) {
  const idx = state.transfers.findIndex((x) => x.id === tid);
  if (idx < 0) return;
  const t = state.transfers[idx];
  t.status = "cancelled";
  if (t.abortCtrl) t.abortCtrl.abort();
  if (t.abortXhr) t.abortXhr.abort();
  t.parts = [];
  if (t.uploadId) {
    api(`/api/uploads/${encodeURIComponent(t.uploadId)}`, { method: "DELETE" }).catch(() => {});
  }
  state.transfers.splice(idx, 1);
  renderTransferDrawer();
  pumpTransferQueue();
}

function enqueueRangeDownload(rec) {
  if (!rec) return;
  const existing = state.transfers.find(
    (t) => t.direction === "download" && t.fileId === rec.id && (t.status === "running" || t.status === "queued" || t.status === "paused")
  );
  if (existing) {
    setTransferTab("download");
    setTransferDrawerOpen(true);
    if (existing.status === "paused") resumeTransferTask(existing.id);
    return;
  }
  state.transfers.unshift({
    id: "dl-" + Date.now() + "-" + Math.random().toString(16).slice(2, 8),
    direction: "download",
    fileId: rec.id,
    name: rec.name,
    size: Number(rec.size || 0),
    loadedBytes: 0,
    parts: [],
    status: "queued",
    speedBps: 0,
    etaSec: null,
    error: "",
    note: "",
  });
  setTransferTab("download");
  setTransferDrawerOpen(true);
  pumpTransferQueue();
}

async function uploadEntries(rawItems) {
  if (!rawItems || rawItems.length === 0) return;

  const allowedItems = [];
  const rejectedNames = [];
  let firstReason = "";
  for (const it of rawItems) {
    if (!it || !it.file) continue;
    const chk = isExtAllowedByPolicy(it.file.name);
    if (chk.ok) {
      allowedItems.push(it);
    } else {
      rejectedNames.push(it.relativePath || it.file.name);
      if (!firstReason) firstReason = chk.reason;
    }
  }

  if (allowedItems.length === 0) {
    showToast(`未上传任何文件：${firstReason || "均被上传类型规则拦截"}`, "error");
    return;
  }
  if (rejectedNames.length > 0) {
    showToast(`已自动跳过 ${rejectedNames.length} 个受限文件（${firstReason}）`, "error");
  }

  const targetNode = (state.user && state.user.role === "admin" && $("upload-target-node"))
    ? $("upload-target-node").value
    : "auto";
  const allowedUsers = Array.isArray(state.uploadAllowedUsers) ? [...state.uploadAllowedUsers] : [];

  for (const it of allowedItems) {
    const rel = String(it.relativePath || it.file.name || "").replace(/\\/g, "/");
    let folderPath = "";
    const slashIdx = rel.lastIndexOf("/");
    if (slashIdx > 0) {
      folderPath = rel.slice(0, slashIdx);
    }
    state.transfers.unshift({
      id: "up-" + Date.now() + "-" + Math.random().toString(16).slice(2, 8),
      direction: "upload",
      name: it.file.name,
      relativePath: rel,
      folderPath,
      file: it.file,
      size: Number(it.file.size || 0),
      targetNode,
      allowedUsers,
      loadedBytes: 0,
      status: "queued",
      speedBps: 0,
      etaSec: null,
      error: "",
      note: "",
      uploadId: null,
      totalChunks: 1,
    });
  }

  setTransferTab("upload");
  setTransferDrawerOpen(true);
  pumpTransferQueue();
}

function uploadFiles(fileList) {
  if (!fileList || fileList.length === 0) return;
  const items = Array.from(fileList).map((file) => ({
    file,
    relativePath: file.webkitRelativePath || file.name,
  }));
  uploadEntries(items);
}

async function openPreview(id) {
  const rec = state.files.find((f) => f.id === id);
  if (!rec) return;
  const ext = (rec.ext || "").toLowerCase().replace(/^\./, "");

  $("preview-title").textContent = rec.name;
  $("preview-download-link").href = `/api/files/${rec.id}/download`;
  const editHeaderBtn = $("preview-edit-btn");
  if (editHeaderBtn) {
    const canEdit = isEditableTextRec(rec) && rec.node_online !== false;
    editHeaderBtn.classList.toggle("hidden", !canEdit);
    editHeaderBtn.onclick = canEdit
      ? () => {
          closePreview();
          openTextEditor(rec.id);
        }
      : null;
  }
  const body = $("preview-body");
  body.innerHTML = `<div class="empty-state">正在加载预览...</div>`;
  $("preview-modal").classList.remove("hidden");

  const rawUrl = `/api/files/${rec.id}/raw`;

  if (["mp4", "webm", "mov", "m4v", "ogv"].includes(ext)) {
    body.innerHTML = `<video class="preview-video" controls autoplay src="${rawUrl}"></video>`;
    return;
  }
  if (["mp3", "wav", "flac", "aac", "ogg", "m4a"].includes(ext)) {
    body.innerHTML = `
      <div class="preview-audio-box">
        <div><strong>${escapeHtml(rec.name)}</strong></div>
        <div style="color:var(--muted);font-size:12px;margin-top:4px;">${formatSize(rec.size)}</div>
        <audio controls autoplay src="${rawUrl}"></audio>
      </div>`;
    return;
  }
  if (ext === "pdf") {
    body.innerHTML = `<iframe class="preview-pdf" src="${rawUrl}"></iframe>`;
    return;
  }
  if (["jpg", "jpeg", "png", "gif", "webp", "svg", "bmp"].includes(ext)) {
    body.innerHTML = `<img class="preview-image" src="${rawUrl}" alt="${escapeHtml(rec.name)}" />`;
    return;
  }

  try {
    const data = await api(`/api/files/${rec.id}/preview`);
    if (data.type === "archive") {
      renderArchivePreview(body, rec, data);
    } else if (data.is_md) {
      body.innerHTML = `<div class="preview-doc">${renderMarkdown(data.content || "")}</div>`;
    } else if (data.type === "docx") {
      body.innerHTML = `<div class="preview-doc">${renderDocx(data.blocks || [])}</div>`;
    } else if (data.type === "xlsx") {
      renderXlsx(body, data.sheets || []);
    } else if (data.type === "pptx") {
      renderPptx(body, data.slides || []);
    } else {
      body.innerHTML = `<div class="preview-doc"><pre>${escapeHtml(data.content || "")}</pre></div>`;
    }
  } catch (err) {
    body.innerHTML = `<div class="error-box">${escapeHtml(err.message)}</div>`;
  }
}

function renderArchivePreview(container, rec, data, searchText = "") {
  const entries = data.entries || [];
  let filter = searchText;

  container.innerHTML = `
    <div class="archive-toolbar">
      <div class="archive-meta">
        <span class="cat-tag">${escapeHtml((data.format || "archive").toUpperCase())}</span>
        <span>共 ${data.total_files || 0} 个文件${data.total_dirs ? ` / ${data.total_dirs} 个目录` : ""} · 解压总计 ${formatSize(data.total_size || 0)}</span>
        ${data.truncated ? `<span class="status-pill status-warn">条目过多，仅显示前 ${entries.length} 项</span>` : ""}
      </div>
      <div class="archive-tools">
        <input id="archive-search-input" class="archive-search" type="search" value="${escapeHtml(filter)}" placeholder="包内搜索文件名/路径..." />
        <button id="btn-extract-all" class="btn btn-primary btn-sm" type="button">全部解压到文件库</button>
      </div>
    </div>
    <div class="archive-list" id="archive-list-body"></div>
  `;

  const listEl = $("archive-list-body");
  const drawRows = () => {
    const q = filter.trim().toLowerCase();
    const filtered = q
      ? entries.filter((e) => (e.path || "").toLowerCase().includes(q) || (e.name || "").toLowerCase().includes(q))
      : entries;

    listEl.innerHTML = filtered.length === 0
      ? `<div class="empty-state">没有匹配的压缩包条目</div>`
      : filtered
          .map((e) => {
            const dlUrl = `/api/files/${rec.id}/archive-entry?path=${encodeURIComponent(e.path)}&mode=download`;
            if (e.is_dir) {
              return `
                <div class="archive-row is-dir">
                  <div class="archive-row-main">
                    <span class="archive-badge">目录</span>
                    <span class="archive-path" title="${escapeHtml(e.path)}">${WIN_FOLDER_ICON_SVG} ${escapeHtml(e.path)}</span>
                  </div>
                  <div class="archive-row-right">
                    <span class="archive-size">-</span>
                  </div>
                </div>
              `;
            }
            const entryIdx = entries.indexOf(e);
            const previewBtn = e.can_preview
              ? `<button class="btn btn-primary btn-xs" type="button" data-entry-preview="${entryIdx}">在线预览</button>`
              : "";
            return `
              <div class="archive-row">
                <div class="archive-row-main">
                  <span class="archive-badge">${escapeHtml((e.ext || "FILE").toUpperCase())}</span>
                  <span class="archive-path" title="${escapeHtml(e.path)}">${escapeHtml(e.path)}</span>
                </div>
                <div class="archive-row-right">
                  <span class="archive-size">${formatSize(e.size)}</span>
                  ${previewBtn}
                  <a class="btn btn-ghost btn-xs" href="${dlUrl}">下载</a>
                </div>
              </div>
            `;
          })
          .join("");

    listEl.querySelectorAll("[data-entry-preview]").forEach((btn) => {
      btn.onclick = () => {
        const entry = entries[Number(btn.dataset.entryPreview)];
        if (entry) openArchiveEntryPreview(container, rec, data, entry, filter);
      };
    });
  };

  drawRows();

  const searchEl = $("archive-search-input");
  if (searchEl) {
    searchEl.oninput = (ev) => {
      filter = ev.target.value;
      drawRows();
    };
  }

  const extractBtn = $("btn-extract-all");
  if (extractBtn) {
    extractBtn.onclick = async () => {
      extractBtn.disabled = true;
      extractBtn.textContent = "正在解压入库...";
      state.isUploadingSelf = true;
      try {
        const targetNode = (state.user && state.user.role === "admin" && $("upload-target-node"))
    ? $("upload-target-node").value
    : "auto";
        const qs = new URLSearchParams({
          target_node: targetNode,
          device_fp: state.device.fp || "",
          device_name: state.device.name || "",
          device_detail: state.device.detail || "",
        });
        const res = await api(`/api/files/${rec.id}/extract?${qs.toString()}`, { method: "POST" });
        const count = (res.extracted || []).length;
        const skipped = res.skipped || [];
        showToast(`成功解压 ${count} 个文件到文件库`, "success");
        if (skipped.length > 0) {
          showToast(`跳过 ${skipped.length} 个受限或超额条目`, "error");
        }
        refreshAll();
      } catch (err) {
        showToast(err.message, "error");
      } finally {
        extractBtn.disabled = false;
        extractBtn.textContent = "全部解压到文件库";
        setTimeout(() => {
          state.isUploadingSelf = false;
        }, 600);
      }
    };
  }
}

async function openArchiveEntryPreview(container, rec, archiveData, entry, prevSearch = "") {
  const ext = (entry.ext || "").toLowerCase().replace(/^\./, "");
  const rawUrl = `/api/files/${rec.id}/archive-entry?path=${encodeURIComponent(entry.path)}&mode=raw`;
  const dlUrl = `/api/files/${rec.id}/archive-entry?path=${encodeURIComponent(entry.path)}&mode=download`;

  container.innerHTML = `
    <div class="archive-entry-sub">
      <button id="btn-archive-back" class="btn btn-ghost btn-xs" type="button">← 返回压缩包目录</button>
      <span class="archive-entry-path" title="${escapeHtml(entry.path)}">${escapeHtml(entry.path)} · ${formatSize(entry.size)}</span>
      <a class="btn btn-primary btn-xs" href="${dlUrl}">下载此文件</a>
    </div>
    <div id="archive-entry-content"><div class="empty-state">正在解压预览...</div></div>
  `;

  $("btn-archive-back").onclick = () => renderArchivePreview(container, rec, archiveData, prevSearch);
  const contentEl = $("archive-entry-content");

  if (["mp4", "webm", "mov", "m4v", "ogv"].includes(ext)) {
    contentEl.innerHTML = `<video class="preview-video" controls autoplay src="${rawUrl}"></video>`;
    return;
  }
  if (["mp3", "wav", "flac", "aac", "ogg", "m4a"].includes(ext)) {
    contentEl.innerHTML = `
      <div class="preview-audio-box">
        <div><strong>${escapeHtml(entry.name)}</strong></div>
        <div style="color:var(--muted);font-size:12px;margin-top:4px;">${formatSize(entry.size)}</div>
        <audio controls autoplay src="${rawUrl}"></audio>
      </div>`;
    return;
  }
  if (ext === "pdf") {
    contentEl.innerHTML = `<iframe class="preview-pdf" src="${rawUrl}"></iframe>`;
    return;
  }
  if (["jpg", "jpeg", "png", "gif", "webp", "svg", "bmp"].includes(ext)) {
    contentEl.innerHTML = `<img class="preview-image" src="${rawUrl}" alt="${escapeHtml(entry.name)}" />`;
    return;
  }

  try {
    const data = await api(`/api/files/${rec.id}/archive-entry?path=${encodeURIComponent(entry.path)}&mode=preview`);
    if (data.is_md) {
      contentEl.innerHTML = `<div class="preview-doc">${renderMarkdown(data.content || "")}</div>`;
    } else if (data.type === "docx") {
      contentEl.innerHTML = `<div class="preview-doc">${renderDocx(data.blocks || [])}</div>`;
    } else if (data.type === "xlsx") {
      renderXlsx(contentEl, data.sheets || []);
    } else if (data.type === "pptx") {
      renderPptx(contentEl, data.slides || []);
    } else {
      contentEl.innerHTML = `<div class="preview-doc"><pre>${escapeHtml(data.content || "")}</pre></div>`;
    }
  } catch (err) {
    contentEl.innerHTML = `<div class="error-box">${escapeHtml(err.message)}</div>`;
  }
}

function renderDocx(blocks) {
  if (!blocks.length) return `<p style="color:var(--muted)">文档内容为空</p>`;
  return blocks
    .map((b) => {
      if (b.kind === "heading") {
        const lvl = Math.min(Math.max(b.level || 2, 1), 4);
        return `<h${lvl}>${escapeHtml(b.text || "")}</h${lvl}>`;
      }
      if (b.kind === "table") {
        const rows = (b.rows || [])
          .map(
            (r, idx) =>
              `<tr>${r
                .map((c) => `<${idx === 0 ? "th" : "td"}>${escapeHtml(c)}</${idx === 0 ? "th" : "td"}>`)
                .join("")}</tr>`
          )
          .join("");
        return `<div class="preview-table-wrap"><table class="preview-table">${rows}</table></div>`;
      }
      return `<p>${escapeHtml(b.text || "")}</p>`;
    })
    .join("");
}

function renderXlsx(container, sheets) {
  if (!sheets.length) {
    container.innerHTML = `<div class="empty-state">表格为空</div>`;
    return;
  }
  let active = 0;
  const draw = () => {
    const tabs = sheets
      .map(
        (s, idx) =>
          `<button class="btn btn-xs ${idx === active ? "btn-primary" : "btn-ghost"}" data-sheet="${idx}">${escapeHtml(s.name)}</button>`
      )
      .join("");
    const rows = (sheets[active].rows || [])
      .map(
        (r, rIdx) =>
          `<tr>${r
            .map((c) => `<${rIdx === 0 ? "th" : "td"}>${escapeHtml(c)}</${rIdx === 0 ? "th" : "td"}>`)
            .join("")}</tr>`
      )
      .join("");
    container.innerHTML = `
      <div class="sheet-tabs">${tabs}</div>
      <div class="preview-table-wrap"><table class="preview-table">${rows}</table></div>
    `;
    container.querySelectorAll("[data-sheet]").forEach((btn) => {
      btn.onclick = () => {
        active = Number(btn.dataset.sheet);
        draw();
      };
    });
  };
  draw();
}

function renderPptx(container, slides) {
  if (!slides.length) {
    container.innerHTML = `<div class="empty-state">未解析到幻灯片内容</div>`;
    return;
  }
  let idx = 0;
  const draw = () => {
    const s = slides[idx];
    const items = (s.lines || []).map((l) => `<li>${escapeHtml(l)}</li>`).join("");
    container.innerHTML = `
      <div class="pptx-nav">
        <button class="btn btn-ghost btn-sm" id="ppt-prev" ${idx === 0 ? "disabled" : ""}>上一页</button>
        <span>第 ${idx + 1} / ${slides.length} 页</span>
        <button class="btn btn-ghost btn-sm" id="ppt-next" ${idx === slides.length - 1 ? "disabled" : ""}>下一页</button>
      </div>
      <div class="pptx-slide">
        <h2>${escapeHtml(s.title || "")}</h2>
        ${items ? `<ul>${items}</ul>` : `<p style="color:var(--muted)">（本页无额外正文）</p>`}
      </div>
    `;
    $("ppt-prev").onclick = () => {
      if (idx > 0) {
        idx--;
        draw();
      }
    };
    $("ppt-next").onclick = () => {
      if (idx < slides.length - 1) {
        idx++;
        draw();
      }
    };
  };
  draw();
}

function renderMarkdown(md) {
  let html = escapeHtml(md);
  html = html.replace(/```([\s\S]*?)```/g, (_, code) => `<pre><code>${code.trim()}</code></pre>`);
  html = html.replace(/`([^`\n]+)`/g, "<code>$1</code>");
  html = html.replace(/^#### (.+)$/gm, "<h4>$1</h4>");
  html = html.replace(/^### (.+)$/gm, "<h3>$1</h3>");
  html = html.replace(/^## (.+)$/gm, "<h2>$1</h2>");
  html = html.replace(/^# (.+)$/gm, "<h1>$1</h1>");
  html = html.replace(/^&gt; (.+)$/gm, "<blockquote>$1</blockquote>");
  html = html.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  html = html.replace(/\*([^*]+)\*/g, "<em>$1</em>");
  html = html.replace(/^[-*] (.+)$/gm, "<li>$1</li>");
  html = html.replace(/\n\n+/g, "</p><p>");
  return `<p>${html}</p>`;
}

function closePreview() {
  $("preview-modal").classList.add("hidden");
  $("preview-body").innerHTML = "";
}

// ==================== Online Text / JSON / Markdown / Code Editor ====================

function setEditorStatusPill(text, kind = "ok") {
  const pill = $("editor-status-pill");
  if (!pill) return;
  if (!text) {
    pill.textContent = "";
    pill.classList.add("hidden");
    return;
  }
  pill.textContent = text;
  pill.className = `editor-status-pill ${kind === "err" ? "err" : "ok"}`;
}

function updateEditorGutterAndStats() {
  const ta = $("editor-textarea");
  const gutter = $("editor-line-gutter");
  const stats = $("editor-stats-text");
  if (!ta) return;
  const val = ta.value || "";
  const lines = val.split("\n");
  const lineCount = Math.max(1, lines.length);
  if (gutter) {
    const nums = [];
    for (let i = 1; i <= lineCount; i++) {
      nums.push(i);
    }
    gutter.textContent = nums.join("\n");
    gutter.scrollTop = ta.scrollTop;
  }
  if (stats) {
    const byteLen = new TextEncoder().encode(val).length;
    stats.textContent = `${lineCount} 行 · ${val.length} 字符 · ${formatSize(byteLen)}`;
  }
  if (state.editorSplitMd && $("editor-md-preview") && !$("editor-md-preview").classList.contains("hidden")) {
    $("editor-md-preview").innerHTML = renderMarkdown(val);
  }
}

async function openTextEditor(id) {
  const rec = state.files.find((f) => f.id === id);
  if (!rec) return;
  if (!isEditableTextRec(rec)) {
    showToast("该文件类型暂不支持在线文本编辑", "error");
    return;
  }
  if (rec.node_online === false) {
    showToast("文件所在存储节点当前离线，无法在线编辑", "error");
    return;
  }
  const ext = (rec.ext || "").toLowerCase().replace(/^\./, "");
  const isJson = ext === "json";
  const isMd = ext === "md" || ext === "markdown";

  state.editorFileId = rec.id;
  state.editorFileRec = rec;
  state.editorSplitMd = isMd;

  if ($("editor-title")) {
    $("editor-title").textContent = `在线编辑 — ${rec.name} (当前 v${Number(rec.version || 1)})`;
  }
  if ($("editor-lang-badge")) {
    $("editor-lang-badge").textContent = (ext || "TXT").toUpperCase();
  }
  if ($("editor-btn-json-format")) $("editor-btn-json-format").classList.toggle("hidden", !isJson);
  if ($("editor-btn-json-minify")) $("editor-btn-json-minify").classList.toggle("hidden", !isJson);
  if ($("editor-btn-json-validate")) $("editor-btn-json-validate").classList.toggle("hidden", !isJson);
  if ($("editor-btn-md-preview")) {
    $("editor-btn-md-preview").classList.toggle("hidden", !isMd);
    $("editor-btn-md-preview").textContent = state.editorSplitMd ? "隐藏 Markdown 预览" : "分栏预览 Markdown";
  }
  if ($("editor-workspace")) {
    $("editor-workspace").classList.toggle("split", state.editorSplitMd);
  }
  if ($("editor-md-preview")) {
    $("editor-md-preview").classList.toggle("hidden", !state.editorSplitMd);
  }
  if ($("editor-note-input")) {
    $("editor-note-input").value = "";
  }
  setEditorStatusPill("");

  const ta = $("editor-textarea");
  if (ta) {
    ta.value = "正在加载文件内容...";
    ta.disabled = true;
  }
  $("editor-modal").classList.remove("hidden");

  try {
    const data = await api(`/api/files/${encodeURIComponent(rec.id)}/preview`);
    if (state.editorFileId !== rec.id) return;
    if (ta) {
      ta.value = typeof data.content === "string" ? data.content : "";
      ta.disabled = false;
      ta.focus();
    }
    updateEditorGutterAndStats();
    if (data.truncated) {
      setEditorStatusPill("提示：原文件较大，已截取可编辑前段内容", "err");
    } else if (isJson) {
      try {
        JSON.parse(ta.value || "{}");
        setEditorStatusPill("JSON 语法有效", "ok");
      } catch (e) {
        setEditorStatusPill(`JSON 语法提示：${e.message}`, "err");
      }
    }
  } catch (err) {
    if (ta) {
      ta.value = "";
      ta.disabled = false;
    }
    setEditorStatusPill(err.message || "读取文件失败", "err");
    showToast(err.message || "读取文件失败", "error");
  }
}

function closeTextEditor() {
  state.editorFileId = null;
  state.editorFileRec = null;
  if ($("editor-modal")) $("editor-modal").classList.add("hidden");
}

function formatEditorJson(minify = false) {
  const ta = $("editor-textarea");
  if (!ta) return;
  try {
    const parsed = JSON.parse(ta.value);
    ta.value = minify ? JSON.stringify(parsed) : JSON.stringify(parsed, null, 2);
    updateEditorGutterAndStats();
    setEditorStatusPill(minify ? "已压缩为单行 JSON" : "已格式化为标准 2 空格缩进 JSON", "ok");
  } catch (err) {
    setEditorStatusPill(`JSON 语法错误：${err.message}`, "err");
    showToast(`JSON 语法错误：${err.message}`, "error");
  }
}

function validateEditorJson() {
  const ta = $("editor-textarea");
  if (!ta) return true;
  try {
    JSON.parse(ta.value);
    setEditorStatusPill("JSON 语法校验通过", "ok");
    showToast("JSON 语法校验通过", "success");
    return true;
  } catch (err) {
    setEditorStatusPill(`JSON 语法错误：${err.message}`, "err");
    showToast(`JSON 语法错误：${err.message}`, "error");
    return false;
  }
}

async function saveTextEditor() {
  const fileId = state.editorFileId;
  const rec = state.editorFileRec;
  const ta = $("editor-textarea");
  const btn = $("editor-save-btn");
  if (!fileId || !ta || ta.disabled) return;

  const ext = (rec?.ext || "").toLowerCase().replace(/^\./, "");
  if (ext === "json") {
    try {
      JSON.parse(ta.value);
    } catch (err) {
      if (!confirm(`检测到当前 JSON 存在语法错误（${err.message}），仍然强制保存吗？`)) {
        return;
      }
    }
  }

  const content = ta.value;
  const note = ($("editor-note-input")?.value || "").trim() || "在线编辑更新";
  if (btn) {
    btn.disabled = true;
    btn.textContent = "正在保存新版本...";
  }
  state.isUploadingSelf = true;
  try {
    const res = await api(`/api/files/${encodeURIComponent(fileId)}/edit`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ content, note }),
    });
    const newVer = res.file?.version || (Number(rec?.version || 1) + 1);
    showToast(`已保存为新版本 v${newVer}（在线编辑更新）`, "success");
    closeTextEditor();
    await refreshAll();
    if ($("versions-modal") && !$("versions-modal").classList.contains("hidden") && state.versionModalFileId === fileId) {
      await openFileVersionsModal(fileId);
    }
  } catch (err) {
    setEditorStatusPill(err.message || "保存失败", "err");
    showToast(err.message || "保存失败", "error");
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.textContent = "保存为新版本 (Ctrl+S)";
    }
    setTimeout(() => {
      state.isUploadingSelf = false;
    }, 600);
  }
}

function updateCreateTxtStats() {
  const ta = $("create-txt-content");
  const stats = $("create-txt-stats");
  if (!ta || !stats) return;
  const val = ta.value || "";
  const lines = val === "" ? 0 : val.split("\n").length;
  stats.textContent = `${lines} 行 · ${val.length} 字符`;
}

function openCreateTxtModal() {
  const modal = $("create-txt-modal");
  if (!modal) return;
  if ($("create-txt-name")) $("create-txt-name").value = "";
  if ($("create-txt-folder")) $("create-txt-folder").value = "";
  if ($("create-txt-content")) $("create-txt-content").value = "";
  updateCreateTxtStats();
  modal.classList.remove("hidden");
  setTimeout(() => {
    $("create-txt-name")?.focus();
  }, 20);
}

function closeCreateTxtModal() {
  if ($("create-txt-modal")) $("create-txt-modal").classList.add("hidden");
}

async function saveCreateTxtFile() {
  const nameEl = $("create-txt-name");
  const folderEl = $("create-txt-folder");
  const contentEl = $("create-txt-content");
  const btn = $("create-txt-save-btn");
  const rawName = (nameEl?.value || "").trim();
  if (!rawName) {
    showToast("请输入 .txt 文件名", "error");
    nameEl?.focus();
    return;
  }

  const folderPath = (folderEl?.value || "").trim();
  const content = contentEl?.value || "";
  const targetNode = (state.user && state.user.role === "admin" && $("upload-target-node"))
    ? $("upload-target-node").value
    : "auto";
  const origText = btn ? (btn.dataset.origText || btn.textContent) : "";
  if (btn) {
    btn.dataset.origText = origText;
    btn.disabled = true;
    btn.textContent = "正在创建并上传...";
  }
  state.isUploadingSelf = true;
  try {
    const res = await api("/api/files/create-txt", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: rawName,
        folder_path: folderPath,
        content,
        allowed_users: Array.isArray(state.uploadAllowedUsers) ? state.uploadAllowedUsers : [],
        target_node: targetNode,
      }),
    });
    const createdName = res.file?.name || (rawName.toLowerCase().endsWith(".txt") ? rawName : `${rawName}.txt`);
    showToast(`已创建并保存文本文件：${createdName}`, "success");
    closeCreateTxtModal();
    await refreshAll();
  } catch (err) {
    showToast(err.message || "创建文本文件失败", "error");
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.textContent = origText || "创建并上传 .txt (Ctrl+S)";
    }
    setTimeout(() => {
      state.isUploadingSelf = false;
    }, 600);
  }
}

function updateUploadAccessButton() {
  const btn = $("btn-upload-access");
  if (!btn) return;
  const cnt = Array.isArray(state.uploadAllowedUsers) ? state.uploadAllowedUsers.length : 0;
  if (cnt === 0) {
    btn.innerHTML = `<span>可见：全部用户</span>${ICON_CHEVRON_DOWN_SVG}`;
    btn.classList.remove("restricted");
  } else {
    btn.innerHTML = `${ICON_LOCK_SVG}<span>仅指定 ${cnt} 人</span>${ICON_CHEVRON_DOWN_SVG}`;
    btn.classList.add("restricted");
  }
}

async function loadSelectableUsers() {
  try {
    const data = await api("/api/users/selectable");
    if (Array.isArray(data.users)) {
      state.selectableUsers = data.users;
    }
  } catch {}
}

function updateAccessModeUI() {
  const isCustom = state.accessModalIsCustom;
  if ($("access-mode-all")) $("access-mode-all").classList.toggle("active", !isCustom);
  if ($("access-mode-custom")) $("access-mode-custom").classList.toggle("active", isCustom);
  if ($("access-custom-panel")) $("access-custom-panel").classList.toggle("hidden", !isCustom);
  const hint = $("access-summary-hint");
  if (hint) {
    if (!isCustom) {
      hint.textContent = "当前设置：全部系统用户可见并可下载";
    } else {
      const cnt = state.accessDraftSet.size;
      hint.textContent = cnt > 0
        ? `已指定 ${cnt} 个用户可下载（上传者与管理员默认可见，其余用户完全隐藏）`
        : "请至少勾选 1 个允许下载的系统用户";
    }
  }
}

function renderAccessModalUsers() {
  const container = $("access-users-list");
  if (!container) return;
  const q = ($("access-user-search")?.value || "").trim().toLowerCase();
  const list = (state.selectableUsers || []).filter((u) => {
    if (!q) return true;
    return u.username.toLowerCase().includes(q);
  });
  if (list.length === 0) {
    container.innerHTML = `<div style="color:var(--muted);font-size:12.5px;padding:8px;">未找到匹配的正常用户</div>`;
    updateAccessModeUI();
    return;
  }
  container.innerHTML = "";
  for (const u of list) {
    const checked = state.accessDraftSet.has(u.username);
    const isSelf = state.user && u.username === state.user.username;
    const item = document.createElement("label");
    item.className = "access-user-item";
    item.innerHTML = `
      <input type="checkbox" value="${escapeHtml(u.username)}" ${checked ? "checked" : ""} />
      <span class="access-user-name">${escapeHtml(u.username)}</span>
      ${isSelf ? '<span class="cat-tag">自己</span>' : u.role === "admin" ? '<span class="status-pill status-ok">管理员</span>' : ""}
    `;
    const cb = item.querySelector("input");
    cb.onchange = () => {
      if (cb.checked) state.accessDraftSet.add(u.username);
      else state.accessDraftSet.delete(u.username);
      updateAccessModeUI();
    };
    container.appendChild(item);
  }
  updateAccessModeUI();
}

async function openUploadAccessPicker() {
  state.accessModalMode = "upload";
  state.accessModalFileId = null;
  const existing = Array.isArray(state.uploadAllowedUsers) ? state.uploadAllowedUsers : [];
  state.accessModalIsCustom = existing.length > 0;
  state.accessDraftSet = new Set(existing);
  if ($("access-modal-title")) $("access-modal-title").textContent = "设置上传文件可见与下载权限";
  if ($("access-user-search")) $("access-user-search").value = "";
  $("access-modal").classList.remove("hidden");
  await loadSelectableUsers();
  renderAccessModalUsers();
}

async function openFileAccessPicker(rec) {
  if (!rec) return;
  state.accessModalMode = "file";
  state.accessModalFileId = rec.id;
  const existing = Array.isArray(rec.allowed_users) ? rec.allowed_users : [];
  state.accessModalIsCustom = existing.length > 0;
  state.accessDraftSet = new Set(existing);
  if ($("access-modal-title")) $("access-modal-title").textContent = `权限设置 — ${rec.name}`;
  if ($("access-user-search")) $("access-user-search").value = "";
  $("access-modal").classList.remove("hidden");
  await loadSelectableUsers();
  renderAccessModalUsers();
}

function closeAccessPicker() {
  $("access-modal").classList.add("hidden");
}

async function saveAccessPicker() {
  let selected = [];
  if (state.accessModalIsCustom) {
    selected = [...state.accessDraftSet].sort();
    if (selected.length === 0) {
      showToast("请选择至少一名可下载用户，或切换为「全部系统用户可见」", "error");
      return;
    }
  }

  if (state.accessModalMode === "upload") {
    state.uploadAllowedUsers = selected;
    updateUploadAccessButton();
    closeAccessPicker();
    showToast(
      selected.length === 0
        ? "已设为所有系统用户可见"
        : `已限定仅 ${selected.length} 名指定用户可下载`,
      "success"
    );
    return;
  }

  const fileID = state.accessModalFileId;
  if (!fileID) return;
  try {
    const data = await api(`/api/files/${fileID}/access`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ allowed_users: selected }),
    });
    closeAccessPicker();
    showToast(
      selected.length === 0
        ? "已变更为全部系统用户可见"
        : `权限已更新：仅 ${selected.length} 名指定用户可下载`,
      "success"
    );
    await refreshAll();
    if ($("props-modal") && !$("props-modal").classList.contains("hidden") && data.file && data.file.id === fileID) {
      openFileProperties(fileID);
    }
  } catch (err) {
    showToast(err.message, "error");
  }
}

function closeFileProperties() {
  state.propsReqSeq = (state.propsReqSeq || 0) + 1;
  $("props-modal").classList.add("hidden");
  $("props-body").innerHTML = "";
}

// ==================== Workspace Pages: Analytics, Node Health & File Version Control ====================

function formatRate(bps) {
  const n = Number(bps || 0);
  if (!n || n <= 0) return "0 B/s";
  return `${formatSize(n)}/s`;
}

function gaugeBarClass(pct) {
  const v = Number(pct || 0);
  if (v >= 88) return "crit";
  if (v >= 72) return "warn";
  return "ok";
}

function compareVersionsClient(a, b) {
  const parse = (raw) =>
    String(raw || "")
      .trim()
      .replace(/^[vV]+/, "")
      .split(/[.-]/)
      .map((s) => parseInt(s, 10) || 0);
  const pa = parse(a);
  const pb = parse(b);
  const len = Math.max(pa.length, pb.length);
  for (let i = 0; i < len; i++) {
    const va = pa[i] || 0;
    const vb = pb[i] || 0;
    if (va < vb) return -1;
    if (va > vb) return 1;
  }
  return 0;
}

async function syncClusterNodeVersions(nodeID, nodeName) {
  const isSingle = Boolean(nodeID && nodeID !== "all");
  const entryVer = state.pool?.entry_version || state.appVersion || "V0.12.0";
  const label = isSingle ? `分节点「${nodeName || nodeID}」` : "所有在线分节点";
  try {
    const data = await api("/api/admin/cluster/upgrade-nodes", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ node_id: isSingle ? nodeID : "all" }),
    });
    if (data.pool) state.pool = data.pool;
    renderAdminCluster();
    renderPoolStatus();
    renderNodeSummary();
    if (state.workspacePage === "health") renderNodeHealthPage();
    if (isSingle) {
      const first = Array.isArray(data.results) && data.results[0];
      showToast(first?.message || `已向 ${label} 推送版本 ${entryVer}`, "success");
    } else if (data.total_targets === 0) {
      showToast("当前没有需要同步的在线分节点", "info");
    } else if (data.first_error && data.synced === 0) {
      showToast(data.first_error, "error");
    } else {
      showToast(
        `已向 ${data.synced}/${data.total_targets} 个在线分节点同步版本 ${entryVer}${data.first_error ? "（部分节点提示：" + data.first_error + "）" : ""}`,
        data.first_error ? "info" : "success"
      );
    }
    setTimeout(() => loadCluster(), 1600);
  } catch (err) {
    showToast(err.message, "error");
  }
}

function switchWorkspacePage(page) {
  const valid = page === "analytics" || page === "health" ? page : "files";
  state.workspacePage = valid;
  document.querySelectorAll("#workspace-nav [data-workspace-page]").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.workspacePage === valid);
  });
  if ($("page-files")) $("page-files").classList.toggle("hidden", valid !== "files");
  if ($("page-analytics")) $("page-analytics").classList.toggle("hidden", valid !== "analytics");
  if ($("page-health")) $("page-health").classList.toggle("hidden", valid !== "health");

  const isAdmin = Boolean(state.user && state.user.role === "admin");
  if ($("btn-health-open-cluster-settings")) {
    $("btn-health-open-cluster-settings").classList.toggle("hidden", !isAdmin);
  }
  if ($("btn-health-sync-versions")) {
    $("btn-health-sync-versions").classList.toggle("hidden", !isAdmin);
  }

  if (state.healthTimer) {
    clearInterval(state.healthTimer);
    state.healthTimer = null;
  }

  if (valid === "analytics") {
    renderAnalyticsPage();
  } else if (valid === "health") {
    loadCluster();
    renderNodeHealthPage();
    state.healthTimer = setInterval(() => {
      if (state.workspacePage === "health" && state.user && !$("app-view").classList.contains("hidden")) {
        loadCluster();
      }
    }, 3000);
  } else {
    renderCurrentFilesView(false);
  }
}

function renderAnalyticsPage() {
  const wrap = $("analytics-page-body");
  if (!wrap) return;
  if ($("analytics-filter-subtitle") && $("active-filters")) {
    $("analytics-filter-subtitle").textContent = `当前统计维度：${$("active-filters").textContent} · 点击分类/月份/节点/设备可联动筛选`;
  }

  const files = state.files || [];
  const nodes = state.pool?.nodes || [];
  let totalBytes = 0;
  let versionedFilesCount = 0;
  let totalHistoricalVersions = 0;
  let historicalVersionBytes = 0;

  const catMap = new Map();
  const monthMap = new Map();
  const devMap = new Map();
  const nodeFileCount = new Map();

  for (const rec of files) {
    const sz = Number(rec.size || 0);
    totalBytes += sz;

    const vCnt = Number(rec.version_count || (Array.isArray(rec.versions) ? rec.versions.length + 1 : 1));
    if (vCnt > 1) {
      versionedFilesCount += 1;
      totalHistoricalVersions += vCnt - 1;
      for (const hv of rec.versions || []) {
        historicalVersionBytes += Number(hv.size || 0);
      }
    }

    const catKey = rec.category || "other";
    const curCat = catMap.get(catKey) || {
      key: catKey,
      label: categoryNames[catKey] || catKey,
      count: 0,
      size: 0,
      color: categoryColors[catKey] || categoryColors.other,
    };
    curCat.count += 1;
    curCat.size += sz;
    catMap.set(catKey, curCat);

    const monthKey = rec.month_key || String(rec.uploaded_at || "").slice(0, 7) || "未知";
    const curMonth = monthMap.get(monthKey) || { key: monthKey, count: 0, size: 0 };
    curMonth.count += 1;
    curMonth.size += sz;
    monthMap.set(monthKey, curMonth);

    const devKey = rec.device_fp || "__unknown__";
    const curDev = devMap.get(devKey) || {
      key: devKey,
      label: rec.device_fp ? `${rec.device_name || "未知设备"} (${rec.device_fp})` : "未记录指纹",
      count: 0,
      size: 0,
    };
    curDev.count += 1;
    curDev.size += sz;
    devMap.set(devKey, curDev);

    if (rec.node_id) {
      nodeFileCount.set(rec.node_id, (nodeFileCount.get(rec.node_id) || 0) + 1);
    }
  }

  let totalDiskBytes = 0;
  let totalDiskFreeBytes = 0;
  let totalUpBps = 0;
  let totalDownBps = 0;
  let totalIOErrors = 0;
  for (const n of nodes) {
    if (!n.online) continue;
    totalDiskBytes += Number(n.disk_total_bytes || 0);
    totalDiskFreeBytes += Number(n.disk_free_bytes || 0);
    totalUpBps += Number(n.upload_bps || 0);
    totalDownBps += Number(n.download_bps || 0);
    totalIOErrors += Number(n.io_total_errors || (Number(n.io_read_errors || 0) + Number(n.io_write_errors || 0)));
  }

  const metric = state.chartMetric === "size" ? "size" : "count";
  const catList = [...catMap.values()].sort((a, b) => (metric === "size" ? b.size - a.size : b.count - a.count));
  const totalMetric = catList.reduce((acc, item) => acc + (metric === "size" ? item.size : item.count), 0);

  const radius = 62;
  const circ = 2 * Math.PI * radius;
  let offset = 0;
  const circlesHtml = totalMetric > 0
    ? catList
        .map((item) => {
          const val = metric === "size" ? item.size : item.count;
          if (val <= 0) return "";
          const seg = Math.min(circ, (val / totalMetric) * circ);
          const dashArray = `${seg.toFixed(2)} ${Math.max(0, circ - seg).toFixed(2)}`;
          const circle = `<circle cx="82" cy="82" r="${radius}" fill="none" stroke="${item.color}" stroke-width="20" stroke-dasharray="${dashArray}" stroke-dashoffset="${(-offset).toFixed(2)}" />`;
          offset += seg;
          return circle;
        })
        .join("")
    : "";

  const legendHtml = catList.length > 0
    ? catList
        .map((item) => {
          const val = metric === "size" ? item.size : item.count;
          const pct = totalMetric > 0 ? Math.round((val / totalMetric) * 100) : 0;
          const active = state.selectedCategory === item.key ? "active" : "";
          const metricText = metric === "size" ? formatSize(item.size) : `${item.count} 个 · ${formatSize(item.size)}`;
          return `
            <button type="button" class="legend-row ${active}" data-chart-category="${escapeHtml(item.key)}">
              <span class="legend-left">
                <span class="legend-dot" style="background:${item.color}"></span>
                <span>${escapeHtml(item.label)}</span>
              </span>
              <span class="legend-right">${metricText} (${pct}%)</span>
            </button>
          `;
        })
        .join("")
    : `<div class="chart-empty">暂无分类统计数据</div>`;

  const monthsAll = [...monthMap.values()].sort((a, b) => a.key.localeCompare(b.key));
  const monthsRecent = monthsAll.slice(-12);
  const maxMonthCount = monthsRecent.reduce((m, b) => Math.max(m, b.count), 1);
  const monthBarsHtml = monthsRecent.length > 0
    ? `<div class="vbar-chart">
        ${monthsRecent
          .map((m) => {
            const pct = Math.max(8, Math.round((m.count / maxMonthCount) * 100));
            const active = state.selectedMonth === m.key ? "active" : "";
            return `
              <button type="button" class="vbar-col ${active}" data-chart-month="${escapeHtml(m.key)}" title="${escapeHtml(m.key)} · ${m.count} 个 · ${formatSize(m.size)}">
                <span class="vbar-val">${m.count}</span>
                <div class="vbar-track">
                  <div class="vbar-fill" style="height:${pct}%"></div>
                </div>
                <span class="vbar-lbl">${escapeHtml(m.key)}</span>
              </button>
            `;
          })
          .join("")}
      </div>`
    : `<div class="chart-empty">当前筛选条件下暂无月份记录</div>`;

  const nodesHtml = nodes.length > 0
    ? `<div class="hbar-list">
        ${nodes
          .map((n) => {
            const pct = n.quota_bytes > 0 ? Math.min(100, Math.round((n.used_bytes / n.quota_bytes) * 100)) : 0;
            const active = state.selectedNode === n.node_id ? "active" : "";
            const filteredCount = nodeFileCount.get(n.node_id) || 0;
            const diskSub = n.disk_total_bytes > 0
              ? ` · 物理磁盘已用 ${formatSize(n.disk_used_bytes)} / 总 ${formatSize(n.disk_total_bytes)}`
              : "";
            return `
              <button type="button" class="hbar-row ${active}" data-chart-node="${escapeHtml(n.node_id)}">
                <div class="hbar-head">
                  <span><span class="node-dot ${n.online ? "online" : "offline"}" style="display:inline-block;margin-right:6px;"></span><strong>${escapeHtml(n.node_name)}</strong> (${filteredCount} 文件 · ${escapeHtml(n.version || "-")})</span>
                  <span style="color:var(--muted)">预留已用 ${formatSize(n.used_bytes)} / ${formatQuotaDisplay(n.quota_bytes)} (${pct}%)${diskSub}</span>
                </div>
                <div class="hbar-track"><div class="hbar-fill" style="width:${pct}%"></div></div>
              </button>
            `;
          })
          .join("")}
      </div>`
    : `<div class="chart-empty">暂无集群节点信息</div>`;

  const versionedTop = [...files]
    .sort((a, b) => {
      const va = Number(a.version_count || 1);
      const vb = Number(b.version_count || 1);
      if (va !== vb) return vb - va;
      return Number(b.size || 0) - Number(a.size || 0);
    })
    .slice(0, 8);
  const versionedHtml = versionedTop.length > 0
    ? `<div class="hbar-list">
        ${versionedTop
          .map((f) => {
            const vNum = Number(f.version || 1);
            const vCnt = Number(f.version_count || 1);
            return `
              <button type="button" class="hbar-row" data-open-version-id="${escapeHtml(f.id)}" title="点击打开版本控制详情">
                <div class="hbar-head">
                  <span><strong>${escapeHtml(f.name)}</strong> ${f.folder_path ? `<span style="color:var(--muted)">(${escapeHtml(f.folder_path)})</span>` : ""}</span>
                  <span style="color:var(--link-text)">当前 v${vNum} · 共 ${vCnt} 个版本 · ${formatSize(f.size)}</span>
                </div>
              </button>
            `;
          })
          .join("")}
      </div>`
    : `<div class="chart-empty">暂无文件版本记录</div>`;

  const quotaTotal = state.pool?.total_quota_bytes || 0;
  const quotaUsed = state.pool?.total_used_bytes || totalBytes;
  const quotaPct = quotaTotal > 0 ? Math.round((quotaUsed / quotaTotal) * 100) : 0;

  wrap.innerHTML = `
    <div class="health-kpi-strip">
      <div class="health-kpi-card">
        <div class="health-kpi-head"><span>当前文件总数</span>${ICON_SERVER_SVG}</div>
        <div class="health-kpi-val">${files.length} 个</div>
        <div class="health-kpi-sub">当前活跃版本总占用 ${formatSize(totalBytes)}</div>
      </div>
      <div class="health-kpi-card">
        <div class="health-kpi-head"><span>多版本受控文件</span>${ICON_VERSION_BRANCH_SVG}</div>
        <div class="health-kpi-val">${versionedFilesCount} 个文件</div>
        <div class="health-kpi-sub">累计归档 ${totalHistoricalVersions} 个历史版本 (${formatSize(historicalVersionBytes)})</div>
      </div>
      <div class="health-kpi-card">
        <div class="health-kpi-head"><span>集群预留配额池</span>${ICON_DISK_SVG}</div>
        <div class="health-kpi-val">${formatSize(quotaUsed)} / ${formatQuotaDisplay(quotaTotal)}</div>
        <div class="health-kpi-sub">配额使用率 ${quotaPct}% · 剩余 ${formatSize(state.pool?.total_free_bytes || 0)}</div>
      </div>
      <div class="health-kpi-card">
        <div class="health-kpi-head"><span>物理磁盘总可用 / 总容量</span>${ICON_DISK_SVG}</div>
        <div class="health-kpi-val">${totalDiskBytes > 0 ? `${formatSize(totalDiskFreeBytes)} / ${formatSize(totalDiskBytes)}` : "-"}</div>
        <div class="health-kpi-sub">已用物理空间 ${totalDiskBytes > 0 ? formatSize(Math.max(0, totalDiskBytes - totalDiskFreeBytes)) : "-"}</div>
      </div>
      <div class="health-kpi-card">
        <div class="health-kpi-head"><span>实时吞吐速率</span>${ICON_NET_SVG}</div>
        <div class="health-kpi-val">↑ ${formatRate(totalUpBps)}</div>
        <div class="health-kpi-sub">↓ 下载速率 ${formatRate(totalDownBps)}</div>
      </div>
      <div class="health-kpi-card">
        <div class="health-kpi-head"><span>存储读写错误计数</span>${ICON_CPU_SVG}</div>
        <div class="health-kpi-val" style="color:${totalIOErrors > 0 ? "var(--danger-text)" : "var(--success-text)"}">${totalIOErrors} 次</div>
        <div class="health-kpi-sub">${totalIOErrors === 0 ? "所有在线节点读写正常" : "检测到磁盘/IO异常，请检查节点健康页"}</div>
      </div>
    </div>

    <div class="charts-grid">
      <div class="chart-card">
        <div class="chart-card-head">
          <div>
            <div class="chart-card-title">文件类型分布</div>
            <div class="chart-card-sub">点击分类可联动筛选文件</div>
          </div>
          <div class="chart-metric-switch">
            <button type="button" class="btn btn-xs ${metric === "count" ? "btn-primary" : "btn-ghost"}" data-chart-metric="count">按文件数</button>
            <button type="button" class="btn btn-xs ${metric === "size" ? "btn-primary" : "btn-ghost"}" data-chart-metric="size">按空间大小</button>
          </div>
        </div>
        <div class="donut-wrap">
          <div class="donut-svg-wrap">
            <svg viewBox="0 0 164 164">
              <circle cx="82" cy="82" r="${radius}" fill="none" stroke="var(--surface-3)" stroke-width="20" />
              ${circlesHtml}
            </svg>
            <div class="donut-center">
              <div class="donut-center-val">${metric === "size" ? formatSize(totalBytes) : `${files.length} 个`}</div>
              <div class="donut-center-lbl">${metric === "size" ? "总大小" : "总文件"}</div>
            </div>
          </div>
          <div class="donut-legend">${legendHtml}</div>
        </div>
      </div>

      <div class="chart-card">
        <div class="chart-card-head">
          <div>
            <div class="chart-card-title">按月份上传趋势</div>
            <div class="chart-card-sub">最近 ${monthsRecent.length} 个月，点击柱条按月份筛选</div>
          </div>
        </div>
        ${monthBarsHtml}
      </div>

      <div class="chart-card">
        <div class="chart-card-head">
          <div>
            <div class="chart-card-title">集群存储节点容量与物理磁盘对比</div>
            <div class="chart-card-sub">点击节点筛选该节点上的文件</div>
          </div>
        </div>
        ${nodesHtml}
      </div>

      <div class="chart-card">
        <div class="chart-card-head">
          <div>
            <div class="chart-card-title">文件版本控制与容量排行</div>
            <div class="chart-card-sub">点击任意文件直接打开「版本历史控制」管理弹窗</div>
          </div>
        </div>
        ${versionedHtml}
      </div>
    </div>
  `;
}

function renderNodeHealthPage() {
  const wrap = $("health-page-body");
  if (!wrap) return;
  const pool = state.pool;
  const nodes = pool?.nodes || [];
  if (nodes.length === 0) {
    wrap.innerHTML = `<div class="empty-state">正在加载集群节点遥测数据...</div>`;
    return;
  }

  let onlineCount = 0;
  let cpuSum = 0;
  let cpuCount = 0;
  let memUsedSum = 0;
  let memTotalSum = 0;
  let diskTotalSum = 0;
  let diskUsedSum = 0;
  let diskFreeSum = 0;
  let latSum = 0;
  let latCount = 0;
  let totalErrors = 0;

  for (const n of nodes) {
    if (n.online) {
      onlineCount++;
      if (n.cpu_percent > 0 || n.is_self || n.mem_total_bytes > 0) {
        cpuSum += Number(n.cpu_percent || 0);
        cpuCount++;
      }
      memUsedSum += Number(n.mem_used_bytes || 0);
      memTotalSum += Number(n.mem_total_bytes || 0);
      diskTotalSum += Number(n.disk_total_bytes || 0);
      diskUsedSum += Number(n.disk_used_bytes || 0);
      diskFreeSum += Number(n.disk_free_bytes || 0);
      if (n.net_latency_ms > 0) {
        latSum += Number(n.net_latency_ms);
        latCount++;
      }
    }
    totalErrors += Number(n.io_total_errors || (Number(n.io_read_errors || 0) + Number(n.io_write_errors || 0)));
  }

  const avgCPU = cpuCount > 0 ? (cpuSum / cpuCount).toFixed(1) : "0.0";
  const avgMemPct = memTotalSum > 0 ? ((memUsedSum * 100) / memTotalSum).toFixed(1) : "0.0";
  const avgLat = latCount > 0 ? (latSum / latCount).toFixed(1) : "0.3";

  const cardsHtml = nodes
    .map((n) => {
      const cpuPct = Number(n.cpu_percent || 0);
      const memPct = Number(n.mem_percent || 0);
      const hasHostMem = Number(n.mem_total_bytes || 0) > 0;
      const diskTotal = Number(n.disk_total_bytes || 0);
      const diskUsed = Number(n.disk_used_bytes || 0);
      const diskFree = Number(n.disk_free_bytes || 0);
      const diskPct = diskTotal > 0 ? Math.min(100, Math.round((diskUsed * 1000) / diskTotal) / 10) : 0;
      const quotaTotal = Number(n.quota_bytes || 0);
      const quotaUsed = Number(n.used_bytes || 0);
      const quotaFree = Number(n.free_bytes || 0);
      const quotaPct = quotaTotal > 0 ? Math.min(100, Math.round((quotaUsed * 1000) / quotaTotal) / 10) : 0;

      const netLat = Number(n.net_latency_ms || 0);
      const hbLat = Number(n.heartbeat_latency_ms || netLat || 0);
      const upBps = Number(n.upload_bps || 0);
      const downBps = Number(n.download_bps || 0);
      const totalUp = Number(n.total_upload_bytes || 0);
      const totalDown = Number(n.total_download_bytes || 0);
      const rErr = Number(n.io_read_errors || 0);
      const wErr = Number(n.io_write_errors || 0);
      const totErr = Number(n.io_total_errors || rErr + wErr);
      const syncTime = n.last_sync_at || n.last_seen;
      const entryVer = pool?.entry_version || state.appVersion || "V0.12.0";
      const nodeVer = n.version || (n.is_self ? entryVer : "V0.11.21.4");
      const isBehind = !n.is_self && compareVersionsClient(nodeVer, entryVer) < 0;
      const isAdmin = Boolean(state.user && state.user.role === "admin");
      const verStatusHtml = n.is_self
        ? `<span class="status-pill status-ok">主本机基准版本</span>`
        : isBehind
        ? `<span class="status-pill status-warn">待同步 → ${escapeHtml(entryVer)}</span>`
        : `<span class="status-pill status-ok">与主版本同步</span>`;
      const syncActionBtn = !n.is_self && n.online && isAdmin
        ? `<button type="button" class="btn ${isBehind ? "btn-primary" : "btn-ghost"} btn-xs" data-sync-node-version="${escapeHtml(n.node_id)}" data-node-name="${escapeHtml(n.node_name)}">${isBehind ? `同步至 ${escapeHtml(entryVer)}` : "重新同步版本"}</button>`
        : "";
      const roleBadge = n.role === "entry"
        ? `<span class="status-pill status-ok">主节点 (Entry)</span>`
        : `<span class="cat-tag">从节点 (Worker)</span>`;
      const onlineBadge = n.online
        ? `<span class="status-pill status-ok">在线健康</span>`
        : `<span class="status-pill status-off">离线 (${escapeHtml(formatRelativeTime(n.last_seen))})</span>`;
      const enabledPlugins = Array.isArray(n.plugins) ? n.plugins.filter((p) => p.enabled) : [];
      const pluginPillsHtml = enabledPlugins.length > 0
        ? `<div class="plugin-card-tags" style="margin-top:6px;">${enabledPlugins.map((p) => `<span class="plugin-cap-pill" title="${escapeHtml((p.capabilities || []).join(", "))}">${escapeHtml(p.name || p.plugin_id)}${Number(p.busy_jobs || 0) > 0 ? ` (${Number(p.busy_jobs)}/${Number(p.max_concurrency || 1)})` : ""}</span>`).join("")}</div>`
        : "";

      return `
        <div class="health-node-card ${n.online ? "" : "offline"}">
          <div class="health-node-top">
            <div>
              <div class="health-node-title">
                <span class="node-dot ${n.online ? "online" : "offline"}"></span>
                <span>${escapeHtml(n.node_name)}</span>
                ${n.is_self ? `<span class="cat-tag">本机</span>` : ""}
                ${roleBadge}
                ${onlineBadge}
                <span class="clip-status-badge ${n.clipboard_enabled ? "on" : "off"}">超级剪贴板：${n.clipboard_enabled ? "已开启" : "已关闭"}</span>
              </div>
              <div class="health-node-url">${escapeHtml(n.http_url || "本机直连")} · ${escapeHtml(n.os || "-")}/${escapeHtml(n.arch || "-")} · 文件数 ${Number(n.file_count || 0)} 个</div>
              ${pluginPillsHtml}
            </div>
            <div class="row-actions" style="align-items:center;">
              <span class="ver-badge">${escapeHtml(nodeVer)}</span>
              ${verStatusHtml}
              ${syncActionBtn}
            </div>
          </div>

          <div class="health-gauge-grid">
            <div class="health-gauge-box">
              <div class="health-gauge-head">
                <span class="health-gauge-lbl">${ICON_CPU_SVG}<span>CPU 使用率</span></span>
                <span class="health-gauge-pct">${hasHostMem || n.is_self ? `${cpuPct.toFixed(1)}%` : "远端旧版未上报"}</span>
              </div>
              <div class="health-bar-track">
                <div class="health-bar-fill ${gaugeBarClass(cpuPct)}" style="width:${Math.min(100, Math.max(2, cpuPct))}%"></div>
              </div>
              <div class="health-gauge-sub">
                <span>逻辑核心：${n.cpu_cores ? `${n.cpu_cores} 核` : "-"}</span>
                <span>状态：${cpuPct >= 85 ? "高负载" : "正常"}</span>
              </div>
            </div>

            <div class="health-gauge-box">
              <div class="health-gauge-head">
                <span class="health-gauge-lbl">${ICON_MEM_SVG}<span>内存使用率</span></span>
                <span class="health-gauge-pct">${hasHostMem ? `${memPct.toFixed(1)}%` : "远端旧版未上报"}</span>
              </div>
              <div class="health-bar-track">
                <div class="health-bar-fill ${gaugeBarClass(memPct)}" style="width:${Math.min(100, Math.max(2, memPct))}%"></div>
              </div>
              <div class="health-gauge-sub">
                <span>已用：${hasHostMem ? formatSize(n.mem_used_bytes) : "-"}</span>
                <span>总内存：${hasHostMem ? formatSize(n.mem_total_bytes) : "-"}</span>
              </div>
            </div>

            <div class="health-gauge-box">
              <div class="health-gauge-head">
                <span class="health-gauge-lbl">${ICON_DISK_SVG}<span>物理磁盘容量</span></span>
                <span class="health-gauge-pct">${diskTotal > 0 ? `${diskPct.toFixed(1)}%` : "-"}</span>
              </div>
              <div class="health-bar-track">
                <div class="health-bar-fill ${gaugeBarClass(diskPct)}" style="width:${Math.min(100, Math.max(2, diskPct))}%"></div>
              </div>
              <div class="health-gauge-sub">
                <span>总容量：${diskTotal > 0 ? formatSize(diskTotal) : "-"}</span>
                <span>已用：${diskTotal > 0 ? formatSize(diskUsed) : "-"}</span>
                <span>剩余：${diskTotal > 0 ? formatSize(diskFree) : "-"}</span>
              </div>
            </div>

            <div class="health-gauge-box">
              <div class="health-gauge-head">
                <span class="health-gauge-lbl">${ICON_SERVER_SVG}<span>LFH 预留空间池</span></span>
                <span class="health-gauge-pct">${quotaPct.toFixed(1)}%</span>
              </div>
              <div class="health-bar-track">
                <div class="health-bar-fill ${gaugeBarClass(quotaPct)}" style="width:${Math.min(100, Math.max(2, quotaPct))}%"></div>
              </div>
              <div class="health-gauge-sub">
                <span>总预留：${formatQuotaDisplay(quotaTotal)}</span>
                <span>已用：${formatSize(quotaUsed)}</span>
                <span>剩余：${formatSize(quotaFree)}</span>
              </div>
            </div>
          </div>

          <div class="health-meta-grid">
            <div class="health-meta-item">
              <div class="health-meta-k">${ICON_NET_SVG}<span>网络延迟</span></div>
              <div class="health-meta-v">${n.online ? `${(netLat > 0 ? netLat : 0.3).toFixed(1)} ms` : "离线"}</div>
              <div class="health-meta-sub">${netLat < 15 ? "局域网极速连接" : netLat < 80 ? "连接良好" : "延迟偏高"}</div>
            </div>

            <div class="health-meta-item">
              <div class="health-meta-k">${ICON_NET_SVG}<span>上传 / 下载速度</span></div>
              <div class="health-meta-v">↑ ${formatRate(upBps)} · ↓ ${formatRate(downBps)}</div>
              <div class="health-meta-sub">累计 ↑ ${formatSize(totalUp)} / ↓ ${formatSize(totalDown)}</div>
            </div>

            <div class="health-meta-item">
              <div class="health-meta-k">${ICON_CLOCK_SVG}<span>心跳延迟</span></div>
              <div class="health-meta-v">${n.online ? `${(hbLat > 0 ? hbLat : 0.3).toFixed(1)} ms` : "-"}</div>
              <div class="health-meta-sub">3秒周期双向心跳校验</div>
            </div>

            <div class="health-meta-item">
              <div class="health-meta-k">${ICON_SERVER_SVG}<span>节点版本</span></div>
              <div class="health-meta-v">${escapeHtml(nodeVer)}</div>
              <div class="health-meta-sub">${escapeHtml(n.os || "-")}/${escapeHtml(n.arch || "-")}</div>
            </div>

            <div class="health-meta-item">
              <div class="health-meta-k">${ICON_CLOCK_SVG}<span>最近一次成功同步</span></div>
              <div class="health-meta-v">${escapeHtml(formatRelativeTime(syncTime))}</div>
              <div class="health-meta-sub">${escapeHtml(formatTime(syncTime))}</div>
            </div>

            <div class="health-meta-item">
              <div class="health-meta-k">${ICON_DISK_SVG}<span>存储读写错误数量</span></div>
              <div class="health-meta-v" style="color:${totErr > 0 ? "var(--danger-text)" : "var(--success-text)"}">${totErr} 次错误</div>
              <div class="health-meta-sub">读错误 ${rErr} 次 · 写错误 ${wErr} 次</div>
            </div>
            ${Number(n.zero_copy_count || 0) > 0 || Number(n.mount_count || 0) > 0 ? `
            <div class="health-meta-item">
              <div class="health-meta-k">${ICON_SERVER_SVG}<span>零拷贝节省</span></div>
              <div class="health-meta-v">${formatSize(n.zero_copy_bytes || 0)}</div>
              <div class="health-meta-sub">零拷贝索引 ${Number(n.zero_copy_count || 0)} 个文件 · 挂载目录 ${Number(n.mount_count || 0)} 个</div>
            </div>` : ""}
          </div>
        </div>
      `;
    })
    .join("");

  wrap.innerHTML = `
    <div class="health-kpi-strip">
      <div class="health-kpi-card">
        <div class="health-kpi-head"><span>在线健康节点</span>${ICON_SERVER_SVG}</div>
        <div class="health-kpi-val">${onlineCount} / ${nodes.length}</div>
        <div class="health-kpi-sub">全部在线节点文件实时汇总可见</div>
      </div>
      <div class="health-kpi-card">
        <div class="health-kpi-head"><span>平均 CPU 使用率</span>${ICON_CPU_SVG}</div>
        <div class="health-kpi-val">${avgCPU}%</div>
        <div class="health-kpi-sub">节点实时内核时间片采样</div>
      </div>
      <div class="health-kpi-card">
        <div class="health-kpi-head"><span>集群物理内存使用</span>${ICON_MEM_SVG}</div>
        <div class="health-kpi-val">${avgMemPct}%</div>
        <div class="health-kpi-sub">${memTotalSum > 0 ? `已用 ${formatSize(memUsedSum)} / 总 ${formatSize(memTotalSum)}` : "-"}</div>
      </div>
      <div class="health-kpi-card">
        <div class="health-kpi-head"><span>物理磁盘总容量 / 剩余</span>${ICON_DISK_SVG}</div>
        <div class="health-kpi-val">${diskTotalSum > 0 ? formatSize(diskTotalSum) : "-"}</div>
        <div class="health-kpi-sub">${diskTotalSum > 0 ? `已用 ${formatSize(diskUsedSum)} · 剩余 ${formatSize(diskFreeSum)}` : "-"}</div>
      </div>
      <div class="health-kpi-card">
        <div class="health-kpi-head"><span>平均网络 / 心跳延迟</span>${ICON_NET_SVG}</div>
        <div class="health-kpi-val">${avgLat} ms</div>
        <div class="health-kpi-sub">局域网直连免代理通道</div>
      </div>
      <div class="health-kpi-card">
        <div class="health-kpi-head"><span>集群存储读写错误总数</span>${ICON_DISK_SVG}</div>
        <div class="health-kpi-val" style="color:${totalErrors > 0 ? "var(--danger-text)" : "var(--success-text)"}">${totalErrors} 次</div>
        <div class="health-kpi-sub">${totalErrors === 0 ? "零读写错误 · 存储健康" : "存在读写异常，请检查磁盘"}</div>
      </div>
    </div>

    <div class="health-nodes-grid">${cardsHtml}</div>
  `;
}

async function openFileVersionsModal(fileId) {
  if (!fileId) return;
  state.versionModalFileId = fileId;
  $("versions-modal").classList.remove("hidden");
  $("versions-list-wrap").innerHTML = `<div class="empty-state">正在加载文件版本历史...</div>`;
  if ($("version-file-input")) $("version-file-input").value = "";
  if ($("version-note-input")) $("version-note-input").value = "";
  try {
    const data = await api(`/api/files/${encodeURIComponent(fileId)}/versions`);
    state.versionModalData = data;
    renderFileVersionsModal();
  } catch (err) {
    $("versions-list-wrap").innerHTML = `<div class="error-box">${escapeHtml(err.message)}</div>`;
  }
}

function closeFileVersionsModal() {
  state.versionModalFileId = null;
  state.versionModalData = null;
  if ($("versions-modal")) $("versions-modal").classList.add("hidden");
}

function renderFileVersionsModal() {
  const data = state.versionModalData;
  if (!data || !data.file) return;
  const f = data.file;
  const versions = Array.isArray(data.versions) ? data.versions : [];
  const curVer = Number(data.current_version || f.version || 1);
  const canManage = Boolean(state.user && (state.user.role === "admin" || state.user.username === f.uploader));
  const canEdit = isEditableTextRec(f) && f.node_online !== false;

  if ($("versions-title")) {
    $("versions-title").textContent = `文件版本控制 — ${f.name}`;
  }
  if ($("versions-file-summary")) {
    $("versions-file-summary").innerHTML = `
      <div>
        <strong>${escapeHtml(f.name)}</strong>
        ${f.folder_path ? `<span style="color:var(--muted);margin-left:6px;">目录：${escapeHtml(f.folder_path)}</span>` : ""}
        <div style="font-size:12px;color:var(--muted);margin-top:3px;">
          所在节点：${escapeHtml(f.node_name || f.node_id || "本机")} · 当前生效版本：<strong>v${curVer}</strong>（${escapeHtml(formatUpdateMethodLabel(f.update_method, curVer))}） · 历史版本总数：<strong>${versions.length}</strong> 个
        </div>
      </div>
      <div class="row-actions">
        ${canEdit ? `<button type="button" class="btn btn-ghost btn-xs" id="versions-open-editor-btn">在线编辑此文件</button>` : ""}
        <a class="btn btn-primary btn-xs" href="/api/files/${encodeURIComponent(f.id)}/download">下载当前生效版本 (v${curVer})</a>
      </div>
    `;
    const openEditorBtn = $("versions-open-editor-btn");
    if (openEditorBtn) {
      openEditorBtn.onclick = () => {
        closeFileVersionsModal();
        openTextEditor(f.id);
      };
    }
  }

  const rowsHtml = versions
    .map((v) => {
      const isCurrent = Number(v.version) === curVer;
      const badge = isCurrent
        ? `<span class="status-pill status-ok">v${v.version} · 当前生效</span>`
        : `<span class="cat-tag">v${v.version} · 历史版本</span>`;
      const methodBadge = formatUpdateMethodBadge(v.update_method, v.version);
      const devPrimary = v.device_name || v.device_fp || "未记录设备";
      const devSecondaryParts = [];
      if (v.device_fp && v.device_name) devSecondaryParts.push(v.device_fp);
      if (v.device_ip) devSecondaryParts.push(v.device_ip);
      const devHtml = `
        <div>
          <div style="font-weight:500;">${escapeHtml(devPrimary)}</div>
          ${devSecondaryParts.length > 0 ? `<div style="font-size:11px;color:var(--muted);font-family:ui-monospace,Consolas,monospace;">${escapeHtml(devSecondaryParts.join(" · "))}</div>` : ""}
        </div>
      `;
      const timeHtml = `
        <div>
          <div>${escapeHtml(formatTime(v.uploaded_at))}</div>
          <div style="font-size:11px;color:var(--muted);">${escapeHtml(formatRelativeTime(v.uploaded_at))}</div>
        </div>
      `;
      const restoreBtn = !isCurrent && canManage
        ? `<button type="button" class="btn btn-primary btn-xs" data-restore-version="${v.version}">设为当前版本</button>`
        : "";
      const delBtn = versions.length > 1 && canManage
        ? `<button type="button" class="btn btn-danger btn-xs" data-delete-version="${v.version}">删除此版本</button>`
        : "";
      return `
        <tr>
          <td>${badge}</td>
          <td>${methodBadge}</td>
          <td><strong>${escapeHtml(v.uploader || "-")}</strong></td>
          <td>${devHtml}</td>
          <td>${timeHtml}</td>
          <td>${escapeHtml(v.note || formatUpdateMethodLabel(v.update_method, v.version))}</td>
          <td>${formatSize(v.size)}</td>
          <td>
            <div class="row-actions">
              <a class="btn btn-ghost btn-xs" href="/api/files/${encodeURIComponent(f.id)}/versions/${v.version}/download">下载 v${v.version}</a>
              ${restoreBtn}
              ${delBtn}
            </div>
          </td>
        </tr>
      `;
    })
    .join("");

  $("versions-list-wrap").innerHTML = `
    <table class="files-table">
      <thead>
        <tr>
          <th>版本号</th>
          <th>更新方式</th>
          <th>更新用户</th>
          <th>更新设备</th>
          <th>更新时间</th>
          <th>版本备注说明</th>
          <th>大小</th>
          <th>操作</th>
        </tr>
      </thead>
      <tbody>${rowsHtml}</tbody>
    </table>
  `;
}

function buildPropsBodyHtml(fileRec, meta, metaError) {
  const ext = (fileRec.ext || "").toLowerCase().replace(/^\./, "");
  const online = fileRec.node_online !== false;
  const isImg = ["jpg", "jpeg", "png", "gif", "webp", "svg", "bmp"].includes(ext);
  const iconHtml = isImg && online
    ? `<img src="/api/files/${fileRec.id}/raw" alt="${escapeHtml(fileRec.name)}" />`
    : getWinFileIconSvg(fileRec, 44);
  const catLabel = categoryNames[fileRec.category] || fileRec.category || "其他";
  const exactSize = typeof fileRec.size === "number"
    ? `${formatSize(fileRec.size)} (${fileRec.size.toLocaleString()} 字节)`
    : "-";
  const nodeLabel = `${fileRec.node_name || fileRec.node_id || "本机"}${fileRec.node_os ? " (" + fileRec.node_os + ")" : ""} · ${online ? "在线" : "离线"}`;

  const isAdmin = Boolean(state.user && state.user.role === "admin");
  const canManage = Boolean(state.user && (isAdmin || state.user.username === fileRec.uploader));
  const allowedList = Array.isArray(fileRec.allowed_users) ? fileRec.allowed_users : [];
  const accessText = allowedList.length === 0
    ? "全部系统用户可见与下载"
    : `${ICON_LOCK_SVG}<span>仅指定用户可下载：${escapeHtml(allowedList.join("、"))}（其余用户隐藏）</span>`;
  const accessEditBtn = canManage
    ? ` <button type="button" class="btn btn-ghost btn-xs" id="props-edit-access">修改权限</button>`
    : "";

  const physicalPath = (meta && meta.disk_path) || fileRec.storage_dir || "";
  const sourcePathVal = (meta && meta.source_path) || fileRec.source_path || "";
  const rawMode = (meta && meta.storage_mode) || fileRec.storage_mode || "copy";
  const storageModeText =
    rawMode === "hardlink"
      ? "同盘硬链接（零拷贝·防误删）"
      : rawMode === "ref"
      ? "跨盘零拷贝引用"
      : rawMode === "mount"
      ? "本机目录零拷贝挂载"
      : "普通副本";
  const isMissingSrc = Boolean((meta && meta.source_missing) || fileRec.source_missing);
  const storageModeHtml = `${escapeHtml(storageModeText)}${isMissingSrc ? ` <span class="zc-badge zc-missing">源文件已失效</span>` : ""}`;
  const integrityStatus = (meta && meta.integrity_status) || fileRec.integrity_status || "";
  const integrityHtml =
    integrityStatus === "corrupted"
      ? `<span class="zc-badge zc-corrupt">校验异常（可能已损坏）</span>`
      : integrityStatus === "missing"
      ? `<span class="zc-badge zc-missing">底层文件缺失</span>`
      : integrityStatus === "ok"
      ? `<span class="status-pill status-ok">校验正常</span>${fileRec.last_verified_at ? ` <span style="color:var(--muted);font-size:11.5px;">(${escapeHtml(formatTime(fileRec.last_verified_at))})</span>` : ""}`
      : `<span style="color:var(--muted)">待巡检</span>`;
  const adminPathRow = isAdmin
    ? `<div class="props-row"><div class="props-key">物理存储位置</div><div class="props-val props-mono">${physicalPath ? `<span>${escapeHtml(physicalPath)}</span> <button type="button" class="btn btn-ghost btn-xs" id="props-copy-path">复制路径</button>` : `<span style="color:var(--muted)">获取中...</span>`}</div></div>
       ${sourcePathVal ? `<div class="props-row"><div class="props-key">源文件路径</div><div class="props-val props-mono">${escapeHtml(sourcePathVal)}</div></div>` : ""}`
    : "";

  let mimeText = "识别中...";
  let shaHtml = `<span style="color:var(--muted)">计算中...</span>`;
  if (metaError) {
    mimeText = "-";
    shaHtml = `<span style="color:var(--danger)">获取失败：${escapeHtml(metaError)}</span>`;
  } else if (meta) {
    mimeText = meta.content_type || "-";
    if (meta.sha256_skipped) {
      shaHtml = `<span style="color:var(--muted)">文件超过 256 MB，已跳过实时校验</span>`;
    } else if (meta.sha256) {
      shaHtml = `<span>${escapeHtml(meta.sha256)}</span> <button type="button" class="btn btn-ghost btn-xs" id="props-copy-sha">复制</button>`;
    } else {
      shaHtml = `<span style="color:var(--muted)">未计算${online ? "" : "（节点离线）"}</span>`;
    }
  }

  const archiveGroup = meta && meta.archive_info
    ? `
      <div class="props-group">
        <div class="props-group-title">压缩包统计</div>
        <div class="props-row"><div class="props-key">归档格式</div><div class="props-val">${escapeHtml((meta.archive_info.format || ext || "archive").toUpperCase())}</div></div>
        <div class="props-row"><div class="props-key">包含条目</div><div class="props-val">${Number(meta.archive_info.total_files || 0)} 个文件，${Number(meta.archive_info.total_dirs || 0)} 个目录</div></div>
        <div class="props-row"><div class="props-key">原始总大小</div><div class="props-val">${formatSize(meta.archive_info.total_size || 0)} (${Number(meta.archive_info.total_size || 0).toLocaleString()} 字节)</div></div>
      </div>
    `
    : "";

  return `
    <div class="props-hero">
      <div class="props-hero-icon">${iconHtml}</div>
      <div class="props-hero-meta">
        <div class="props-hero-name">${escapeHtml(fileRec.name)}</div>
        <div class="props-hero-sub">${escapeHtml(catLabel)}${ext ? " · ." + escapeHtml(ext) : ""} · ${formatSize(fileRec.size || 0)}</div>
      </div>
    </div>
    <div class="props-group">
      <div class="props-group-title">常规信息</div>
      <div class="props-row"><div class="props-key">文件名称</div><div class="props-val">${escapeHtml(fileRec.name)}</div></div>
      <div class="props-row"><div class="props-key">所属目录</div><div class="props-val">${escapeHtml(fileRec.folder_path || "根目录")}</div></div>
      <div class="props-row"><div class="props-key">文件类型</div><div class="props-val">${escapeHtml(catLabel)}${ext ? ` (.${escapeHtml(ext)})` : ""}</div></div>
      <div class="props-row"><div class="props-key">MIME 类型</div><div class="props-val props-mono">${escapeHtml(mimeText)}</div></div>
      <div class="props-row"><div class="props-key">精确大小</div><div class="props-val">${escapeHtml(exactSize)}</div></div>
      <div class="props-row"><div class="props-key">可见范围</div><div class="props-val"><span>${escapeHtml(accessText)}</span>${accessEditBtn}</div></div>
      <div class="props-row"><div class="props-key">文件编号</div><div class="props-val props-mono">${escapeHtml(fileRec.id)}</div></div>
    </div>
    ${archiveGroup}
    <div class="props-group">
      <div class="props-group-title">版本控制与更新溯源</div>
      <div class="props-row"><div class="props-key">当前版本</div><div class="props-val"><strong>v${Number(fileRec.version || 1)}</strong>（共 ${Number(fileRec.version_count || 1)} 个版本）</div></div>
      <div class="props-row"><div class="props-key">更新方式</div><div class="props-val">${formatUpdateMethodBadge(fileRec.update_method, fileRec.version)}</div></div>
      <div class="props-row"><div class="props-key">更新用户</div><div class="props-val">${escapeHtml(fileRec.uploader || "-")}</div></div>
      <div class="props-row"><div class="props-key">更新时间</div><div class="props-val">${escapeHtml(formatTime(fileRec.uploaded_at))} (${escapeHtml(formatRelativeTime(fileRec.uploaded_at))})</div></div>
      <div class="props-row"><div class="props-key">版本备注</div><div class="props-val">${escapeHtml(fileRec.version_note || formatUpdateMethodLabel(fileRec.update_method, fileRec.version))}</div></div>
      <div class="props-row"><div class="props-key">SHA-256</div><div class="props-val props-mono">${shaHtml}</div></div>
      <div class="props-row"><div class="props-key">完整性校验</div><div class="props-val">${integrityHtml}</div></div>
    </div>
    <div class="props-group">
      <div class="props-group-title">存储节点与设备指纹</div>
      <div class="props-row"><div class="props-key">所在节点</div><div class="props-val">${escapeHtml(nodeLabel)}</div></div>
      <div class="props-row"><div class="props-key">存储方式</div><div class="props-val">${storageModeHtml}</div></div>
      ${adminPathRow}
      <div class="props-row"><div class="props-key">设备名称</div><div class="props-val">${escapeHtml(fileRec.device_name || "未知设备")}</div></div>
      <div class="props-row"><div class="props-key">设备指纹</div><div class="props-val props-mono">${escapeHtml(fileRec.device_fp || "未记录")}</div></div>
      <div class="props-row"><div class="props-key">来源 IP</div><div class="props-val props-mono">${escapeHtml(fileRec.device_ip || "未记录")}</div></div>
      <div class="props-row"><div class="props-key">终端环境</div><div class="props-val">${escapeHtml(fileRec.device_detail || "无")}</div></div>
    </div>
  `;
}

async function openFileProperties(id) {
  const initialRec = state.files.find((f) => f.id === id);
  const reqSeq = (state.propsReqSeq = (state.propsReqSeq || 0) + 1);
  const modal = $("props-modal");
  const body = $("props-body");
  const prevBtn = $("props-preview-btn");
  const dlLink = $("props-download-link");

  const syncHeader = (rec) => {
    const online = rec.node_online !== false;
    $("props-title").textContent = `属性 — ${rec.name}`;
    if (online) {
      dlLink.href = `/api/files/${rec.id}/download`;
      dlLink.classList.remove("hidden");
    } else {
      dlLink.href = "#";
      dlLink.classList.add("hidden");
    }
    if (online && canPreview(rec)) {
      prevBtn.textContent = isArchiveRec(rec) ? "在线解压查看" : "在线打开";
      prevBtn.classList.remove("hidden");
      prevBtn.onclick = () => {
        closeFileProperties();
        openPreview(rec.id);
      };
    } else {
      prevBtn.classList.add("hidden");
      prevBtn.onclick = null;
    }
  };

  modal.classList.remove("hidden");
  if (initialRec) {
    syncHeader(initialRec);
    body.innerHTML = buildPropsBodyHtml(initialRec, null, null);
    const copyPathEarly = $("props-copy-path");
    if (copyPathEarly && initialRec.storage_dir) {
      copyPathEarly.onclick = () => copyText(initialRec.storage_dir);
    }
    const editAccessEarly = $("props-edit-access");
    if (editAccessEarly) {
      editAccessEarly.onclick = () => openFileAccessPicker(initialRec);
    }
  } else {
    $("props-title").textContent = "属性 — 加载中...";
    prevBtn.classList.add("hidden");
    dlLink.classList.add("hidden");
    body.innerHTML = `<div style="padding:20px;color:var(--muted)">正在加载文件属性...</div>`;
  }

  try {
    const meta = await api(`/api/files/${id}/meta`);
    if (state.propsReqSeq !== reqSeq || modal.classList.contains("hidden")) return;
    const rec = meta.file || initialRec;
    if (!rec) {
      showToast("未找到文件详情", "error");
      closeFileProperties();
      return;
    }
    syncHeader(rec);
    body.innerHTML = buildPropsBodyHtml(rec, meta, null);
    const copyShaBtn = $("props-copy-sha");
    if (copyShaBtn && meta.sha256) {
      copyShaBtn.onclick = () => copyText(meta.sha256);
    }
    const copyPathBtn = $("props-copy-path");
    const diskPath = meta.disk_path || rec.storage_dir || "";
    if (copyPathBtn && diskPath) {
      copyPathBtn.onclick = () => copyText(diskPath);
    }
    const editAccessBtn = $("props-edit-access");
    if (editAccessBtn) {
      editAccessBtn.onclick = () => openFileAccessPicker(rec);
    }
  } catch (err) {
    if (state.propsReqSeq !== reqSeq || modal.classList.contains("hidden")) return;
    if (initialRec) {
      body.innerHTML = buildPropsBodyHtml(initialRec, null, err.message || "请求失败");
    } else {
      body.innerHTML = `<div style="padding:20px;color:var(--danger)">加载失败：${escapeHtml(err.message || "未知错误")}</div>`;
    }
  }
}

// ==================== Settings Center Modal ====================
function switchSettingsTab(tab) {
  if (!state.user || (state.user.role !== "admin" && tab !== "general")) {
    tab = "general";
  }
  state.settingsTab = tab;
  document.querySelectorAll("[data-settings-tab]").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.settingsTab === tab);
  });
  for (const name of ["general", "security", "users", "cluster", "plugins", "update"]) {
    const panel = $(`panel-${name}`);
    if (panel) panel.classList.toggle("hidden", name !== tab);
  }
  if (tab === "cluster" && state.user && state.user.role === "admin") {
    loadPassiveDiscovered();
    loadAdminMounts();
  } else if (tab === "security" && state.user && state.user.role === "admin") {
    loadIntegrityStatus();
    loadAdminAudit();
  } else if (tab === "plugins" && state.user && state.user.role === "admin") {
    loadAdminPlugins();
    loadPluginJobs();
  }
}

function formatScrubSummaryHtml(res, corruptedCount = 0) {
  if (!res) {
    return `暂无完整巡检记录（当前异常文件数：${Number(corruptedCount || 0)}）。点击右上角「立即全盘完整性巡检」可校验全部底层文件 SHA-256。`;
  }
  const warn = Number(res.corrupted || 0) + Number(res.missing || 0) + Number(corruptedCount || 0);
  const pill = warn > 0
    ? `<span class="zc-badge zc-corrupt">发现 ${warn} 个异常</span>`
    : `<span class="status-pill status-ok">全部校验正常</span>`;
  return `
    <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;margin-bottom:4px;">
      ${pill}
      <span>最近巡检：${escapeHtml(formatTime(res.finished_at))}（耗时 ${Number(res.duration_ms || 0)} ms）</span>
    </div>
    <div style="color:var(--muted);font-size:12px;">
      已检查 ${Number(res.checked || 0)} 个文件 · 正常 ${Number(res.ok || 0)} · 补全哈希 ${Number(res.backfilled || 0)} · 损坏 ${Number(res.corrupted || 0)} · 缺失 ${Number(res.missing || 0)}
    </div>
  `;
}

async function loadIntegrityStatus() {
  const box = $("integrity-scrub-summary");
  if (!box || !state.user || state.user.role !== "admin") return;
  try {
    const data = await api("/api/admin/integrity/status");
    box.innerHTML = formatScrubSummaryHtml(data.last_result || null, data.corrupted_count || 0);
  } catch (err) {
    box.textContent = err.message || "加载完整性状态失败";
  }
}

async function runIntegrityScrub() {
  const btn = $("btn-run-scrub");
  const box = $("integrity-scrub-summary");
  const orig = btn ? btn.textContent : "";
  if (btn) {
    btn.disabled = true;
    btn.textContent = "全盘校验中...";
  }
  try {
    const data = await api("/api/admin/integrity/scrub", { method: "POST" });
    if (box) {
      box.innerHTML = formatScrubSummaryHtml(data.result || null, data.corrupted_count || 0);
    }
    const r = data.result || {};
    const bad = Number(r.corrupted || 0) + Number(r.missing || 0);
    showToast(bad > 0 ? `完整性巡检发现 ${bad} 个异常文件` : `完整性巡检完成：${Number(r.checked || 0)} 个文件全部正常`, bad > 0 ? "error" : "success");
    loadAdminAudit();
    loadFiles();
  } catch (err) {
    showToast(err.message || "执行完整性巡检失败", "error");
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.textContent = orig;
    }
  }
}

async function loadAdminAudit() {
  const tbody = $("audit-log-tbody");
  if (!tbody || !state.user || state.user.role !== "admin") return;
  try {
    const data = await api("/api/admin/audit?limit=100");
    renderAdminAudit(Array.isArray(data.entries) ? data.entries : []);
  } catch (err) {
    tbody.innerHTML = `<tr><td colspan="7" style="color:var(--danger);text-align:center;">${escapeHtml(err.message || "加载失败")}</td></tr>`;
  }
}

function renderAdminAudit(entries) {
  const tbody = $("audit-log-tbody");
  if (!tbody) return;
  if (!entries || entries.length === 0) {
    tbody.innerHTML = `<tr><td colspan="7" style="color:var(--muted);text-align:center;">暂无审计日志记录</td></tr>`;
    return;
  }
  const statusPill = (st) => {
    if (st === "denied" || st === "error") {
      return `<span class="status-pill status-off">${escapeHtml(st)}</span>`;
    }
    return `<span class="status-pill status-ok">${escapeHtml(st || "ok")}</span>`;
  };
  tbody.innerHTML = entries
    .map(
      (e) => `
      <tr>
        <td data-label="时间">${escapeHtml(formatTime(e.timestamp))}</td>
        <td data-label="操作人">${escapeHtml(e.actor || "-")}</td>
        <td data-label="动作"><code>${escapeHtml(e.action || "-")}</code></td>
        <td data-label="目标">${escapeHtml(e.target || "-")}</td>
        <td data-label="IP">${escapeHtml(e.ip || "-")}</td>
        <td data-label="状态">${statusPill(e.status)}</td>
        <td data-label="详情">${escapeHtml(e.detail || "-")}</td>
      </tr>`
    )
    .join("");
}

function setLocalImportMode(mode) {
  const m = mode === "mount" ? "mount" : "import";
  state.localImportMode = m;
  document.querySelectorAll("[data-local-import-mode]").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.localImportMode === m);
  });
  const folderLabel = $("local-import-folder-label");
  const folderInput = $("local-import-folder");
  const pathInput = $("local-import-path");
  const hintBox = $("local-import-hint");
  const submitBtn = $("local-import-submit-btn");
  if (m === "mount") {
    if (folderLabel) folderLabel.textContent = "挂载共享别名（选填）";
    if (folderInput) folderInput.placeholder = "例如：影音库（留空自动使用目录名）";
    if (pathInput) pathInput.placeholder = "例如：E:\\Movies 或 /mnt/media";
    if (hintBox) {
      hintBox.textContent = "挂载目录不复制文件、不占用 LFH 上传配额；后台每 15 秒自动同步目录文件增删改，移除挂载时严格保留磁盘原文件。";
    }
    if (submitBtn) submitBtn.textContent = "确认挂载目录";
  } else {
    if (folderLabel) folderLabel.textContent = "目标虚拟目录（选填）";
    if (folderInput) folderInput.placeholder = "留空自动使用目录名或保存于根目录";
    if (pathInput) pathInput.placeholder = "例如：E:\\Movies\\demo.mp4 或 /mnt/data";
    if (hintBox) {
      hintBox.textContent = "同磁盘分区将自动创建 OS 硬链接（0 额外磁盘占用，且原文件重命名/移动/删除后 LFH 仍完整可读）；跨磁盘分区将转为外链引用并自动巡检源路径状态。";
    }
    if (submitBtn) submitBtn.textContent = "立即零拷贝导入";
  }
}

function openLocalImportModal(defaultMode = "import") {
  const modal = $("local-import-modal");
  if (!modal) return;
  const sel = $("local-import-node");
  if (sel) {
    const nodes = (state.pool && Array.isArray(state.pool.nodes) ? state.pool.nodes : []).filter((n) => n.online);
    if (nodes.length > 0) {
      sel.innerHTML = nodes
        .map((n) => {
          const roleTag = n.role === "entry" ? "主节点" : "从节点";
          return `<option value="${escapeHtml(n.node_id)}">${escapeHtml(n.node_name)} (${roleTag} · ${escapeHtml(n.os || "")})</option>`;
        })
        .join("");
      const selfNode = nodes.find((n) => n.is_self) || nodes[0];
      if (selfNode) sel.value = selfNode.node_id;
    } else {
      sel.innerHTML = `<option value="">当前主节点</option>`;
    }
  }
  if ($("local-import-path")) $("local-import-path").value = "";
  if ($("local-import-folder")) $("local-import-folder").value = "";
  setLocalImportMode(defaultMode);
  modal.classList.remove("hidden");
  setTimeout(() => {
    $("local-import-path")?.focus();
  }, 20);
}

function closeLocalImportModal() {
  if ($("local-import-modal")) $("local-import-modal").classList.add("hidden");
}

async function submitLocalImport() {
  const mode = state.localImportMode === "mount" ? "mount" : "import";
  const nodeID = ($("local-import-node")?.value || "").trim();
  const pathVal = ($("local-import-path")?.value || "").trim();
  const folderVal = ($("local-import-folder")?.value || "").trim();
  if (!pathVal) {
    showToast(mode === "mount" ? "请输入要挂载的本机目录绝对路径" : "请输入要零拷贝导入的文件或目录绝对路径", "error");
    $("local-import-path")?.focus();
    return;
  }

  const btn = $("local-import-submit-btn");
  const origText = btn ? btn.textContent : "";
  if (btn) {
    btn.disabled = true;
    btn.textContent = mode === "mount" ? "正在扫描并挂载..." : "正在零拷贝导入...";
  }
  state.isUploadingSelf = true;
  try {
    if (mode === "mount") {
      const data = await api("/api/admin/mounts", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          node_id: nodeID,
          dir_path: pathVal,
          alias: folderVal,
          allowed_users: Array.isArray(state.uploadAllowedUsers) ? state.uploadAllowedUsers : [],
        }),
      });
      if (Array.isArray(data.mounts)) {
        state.mounts = data.mounts;
        renderAdminMounts();
      }
      if (data.pool) state.pool = data.pool;
      const added = Number(data.added || 0);
      showToast(`已挂载目录「${data.mount?.alias || pathVal}」（新增 ${added} 个文件索引，0 磁盘拷贝）`, "success");
      closeLocalImportModal();
      await refreshAll();
    } else {
      const data = await api("/api/admin/files/import-local", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          node_id: nodeID,
          source_path: pathVal,
          folder_path: folderVal,
          allowed_users: Array.isArray(state.uploadAllowedUsers) ? state.uploadAllowedUsers : [],
        }),
      });
      if (data.pool) state.pool = data.pool;
      const sm = data.summary || {};
      showToast(
        `零拷贝导入完成：共 ${Number(sm.total_imported || 0)} 个文件（硬链接 ${Number(sm.hardlink_count || 0)}，外链引用 ${Number(sm.ref_count || 0)}）`,
        "success"
      );
      closeLocalImportModal();
      await refreshAll();
    }
  } catch (err) {
    showToast(err.message || "操作失败", "error");
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.textContent = origText;
    }
    setTimeout(() => {
      state.isUploadingSelf = false;
    }, 600);
  }
}

async function loadAdminMounts() {
  const wrap = $("cluster-mounts-list");
  if (!wrap || !state.user || state.user.role !== "admin") return;
  try {
    const data = await api("/api/admin/mounts");
    state.mounts = Array.isArray(data.mounts) ? data.mounts : [];
    renderAdminMounts();
  } catch (err) {
    wrap.innerHTML = `<div style="color:var(--danger);font-size:12.5px;">加载挂载列表失败：${escapeHtml(err.message || "未知错误")}</div>`;
  }
}

function renderAdminMounts() {
  const wrap = $("cluster-mounts-list");
  if (!wrap) return;
  const list = Array.isArray(state.mounts) ? state.mounts : [];
  if (list.length === 0) {
    wrap.innerHTML = `<div style="color:var(--muted);font-size:12.5px;padding:6px 0;">暂未配置本机挂载目录。点击「新增挂载目录」可将主机已有文件夹直接共享至文件中心（不复制文件、不占用配额）。</div>`;
    return;
  }
  wrap.innerHTML = list
    .map((m) => {
      const ok = m.status !== "missing";
      const pill = ok
        ? `<span class="status-pill status-ok">正常同步</span>`
        : `<span class="status-pill status-off">目录不可访问</span>`;
      const syncTxt = m.last_sync_at ? formatRelativeTime(m.last_sync_at) : "未扫描";
      return `
        <div class="mount-card">
          <div class="mount-card-head">
            <div class="mount-card-title">
              <span class="zc-badge zc-mount">挂载</span>
              <span>${escapeHtml(m.alias || "未命名目录")}</span>
              <span class="cat-tag">${escapeHtml(m.node_name || m.node_id || "本机")}${m.is_self ? "（本机）" : ""}</span>
              ${pill}
            </div>
            <div class="row-actions">
              <button type="button" class="btn btn-ghost btn-xs" data-sync-mount="${escapeHtml(m.id)}" data-mount-node="${escapeHtml(m.node_id)}">立即扫描</button>
              <button type="button" class="btn btn-danger btn-xs" data-del-mount="${escapeHtml(m.id)}" data-mount-node="${escapeHtml(m.node_id)}" data-mount-alias="${escapeHtml(m.alias || m.dir_path)}">移除挂载</button>
            </div>
          </div>
          <div class="mount-card-meta">
            <span>路径：<code>${escapeHtml(m.dir_path)}</code></span>
            <span>文件：${Number(m.file_count || 0)} 个 (${formatSize(m.total_bytes || 0)})</span>
            <span>上次同步：${escapeHtml(syncTxt)}</span>
          </div>
          ${m.last_error ? `<div style="color:var(--danger);font-size:11.5px;">异常信息：${escapeHtml(m.last_error)}</div>` : ""}
        </div>
      `;
    })
    .join("");
}

async function openSettingsModal(defaultTab = "general") {
  $("pwd-msg").classList.add("hidden");
  $("pwd-form").reset();
  switchSettingsTab(defaultTab);
  $("settings-modal").classList.remove("hidden");
  if (state.user && state.user.role === "admin") {
    await refreshAdminData();
  }
}

async function refreshAdminData() {
  try {
    const [settingsData, invData, usersData, clusterData, updateData] = await Promise.all([
      api("/api/admin/settings"),
      api("/api/admin/invites"),
      api("/api/admin/users"),
      api("/api/cluster"),
      api("/api/admin/update").catch(() => null),
    ]);
    const inviteReq = Boolean(settingsData.settings?.invite_required);
    state.serverInfo.invite_required = inviteReq;
    $("toggle-invite-required").checked = inviteReq;
    updateUploadPolicy(settingsData.settings || {});
    updatePendingBadges(usersData.pending_count || settingsData.pending_count || 0);

    state.pool = clusterData.pool;
    state.clusterSecret = clusterData.cluster_secret || "";
    if (clusterData.port) state.port = clusterData.port;
    if (typeof clusterData.expose_ip === "string") state.exposeIP = clusterData.expose_ip;
    if (Array.isArray(clusterData.expose_ips)) state.exposeIPs = clusterData.expose_ips;
    if (Array.isArray(clusterData.disabled_ips)) state.disabledIPs = clusterData.disabled_ips;
    if (Array.isArray(clusterData.available_addrs)) state.availableAddrs = clusterData.available_addrs;
    if (Array.isArray(clusterData.entry_addrs)) {
      state.entryAddrs = clusterData.entry_addrs;
      state.serverInfo.entry_addrs = clusterData.entry_addrs;
    }

    renderAdminCluster();
    loadPassiveDiscovered();
    loadAdminMounts();
    loadIntegrityStatus();
    loadAdminAudit();
    loadAdminPlugins();
    loadPluginJobs();
    renderInvitesTable(invData.invites || []);
    renderUsersTable(usersData.users || []);
    if (updateData && updateData.status) {
      applyUpdateStatus(updateData.status);
    }
  } catch (err) {
    showToast(err.message, "error");
  }
}

// ==================== Stage 4: 分布式算力插件市场与任务调度 ====================
async function loadAdminPlugins() {
  if (!state.user || state.user.role !== "admin") return;
  try {
    const data = await api("/api/admin/plugins/marketplace");
    if (Array.isArray(data.marketplace)) state.marketplacePlugins = data.marketplace;
    if (Array.isArray(data.capabilities)) state.clusterCapabilities = data.capabilities;
    if (Array.isArray(data.nodes) && state.pool) {
      state.pool.nodes = data.nodes;
    }
    renderAdminPlugins();
  } catch (err) {
    if ($("plugins-marketplace-list")) {
      $("plugins-marketplace-list").innerHTML = `<div class="muted">加载插件市场失败：${escapeHtml(err.message)}</div>`;
    }
  }
}

async function loadPluginJobs() {
  try {
    const data = await api("/api/plugins/jobs");
    state.pluginJobs = Array.isArray(data.jobs) ? data.jobs : [];
    renderPluginJobs();
  } catch {
    state.pluginJobs = [];
    renderPluginJobs();
  }
}

function getSelectedPluginTargetNodeId(nodes) {
  const sel = $("plugin-install-node");
  const cur = sel ? sel.value : "";
  if (cur && nodes.some((n) => n.node_id === cur)) return cur;
  const selfNode = nodes.find((n) => n.is_self && n.online);
  if (selfNode) return selfNode.node_id;
  const onlineNode = nodes.find((n) => n.online);
  return onlineNode ? onlineNode.node_id : (nodes[0]?.node_id || "");
}

function syncPluginMutationState(data) {
  if (data && data.pool) {
    state.pool = data.pool;
    renderPoolStatus();
    renderNodeSummary();
    if (state.workspacePage === "health") renderNodeHealthPage();
  }
  if (data && Array.isArray(data.capabilities)) {
    state.clusterCapabilities = data.capabilities;
    if (state.workspacePage === "files" && state.files.length > 0) {
      renderCurrentFilesView(true);
    }
  }
  renderAdminPlugins();
}

function renderAdminPlugins() {
  const sel = $("plugin-install-node");
  const marketEl = $("plugins-marketplace-list");
  const capEl = $("plugins-capabilities-list");
  const nodes = Array.isArray(state.pool?.nodes) ? state.pool.nodes : [];
  const targetNodeId = getSelectedPluginTargetNodeId(nodes);

  if (sel) {
    sel.innerHTML = nodes.length === 0
      ? `<option value="">暂无可用节点</option>`
      : nodes.map((n) => {
          const roleTag = n.role === "entry" ? "主" : "从";
          const statusTag = n.online ? "在线" : "离线";
          return `<option value="${escapeHtml(n.node_id)}" ${n.node_id === targetNodeId ? "selected" : ""} ${n.online ? "" : "disabled"}>${escapeHtml(n.node_name)} (${roleTag} · ${statusTag})</option>`;
        }).join("");
  }

  const targetNode = nodes.find((n) => n.node_id === targetNodeId);
  const targetOnline = Boolean(targetNode && targetNode.online);

  if (marketEl) {
    const items = state.marketplacePlugins || [];
    if (items.length === 0) {
      marketEl.innerHTML = `<div class="muted">暂无可用插件条目</div>`;
    } else {
      marketEl.innerHTML = items.map((item) => {
        const pid = escapeHtml(item.id);
        const nid = escapeHtml(targetNodeId);
        const inst = (targetNode?.plugins || []).find((p) => p.plugin_id === item.id);
        const installedNodes = nodes
          .map((n) => {
            const p = (n.plugins || []).find((x) => x.plugin_id === item.id);
            return p ? `${n.node_name} (${p.enabled ? "启用" : "停用"})` : null;
          })
          .filter(Boolean);

        const capBadges = (item.capabilities || [])
          .map((c) => `<span class="plugin-cap-pill">${escapeHtml(c)}</span>`)
          .join("");
        const extText = Array.isArray(item.supported_exts) && item.supported_exts.length > 0
          ? item.supported_exts.join(" ")
          : "*";

        let actionBtns = "";
        if (!targetNodeId || !targetOnline) {
          actionBtns = `<button type="button" class="btn btn-ghost btn-xs" disabled>目标节点离线</button>`;
        } else if (!inst) {
          actionBtns = `<button type="button" class="btn btn-primary btn-xs" data-plugin-act="install" data-node="${nid}" data-plugin="${pid}">安装到 ${escapeHtml(targetNode.node_name)}</button>`;
        } else {
          const nextEnabled = !inst.enabled;
          actionBtns = `
            <span class="status-pill ${inst.enabled ? "status-ok" : "status-off"}">${inst.enabled ? "当前节点已启用" : "当前节点已停用"}</span>
            <button type="button" class="btn ${nextEnabled ? "btn-primary" : "btn-ghost"} btn-xs" data-plugin-act="toggle" data-node="${nid}" data-plugin="${pid}" data-enabled="${nextEnabled}">${nextEnabled ? "启用" : "停用"}</button>
            <button type="button" class="btn btn-danger btn-xs" data-plugin-act="uninstall" data-node="${nid}" data-plugin="${pid}">卸载</button>
          `;
        }

        return `
          <div class="plugin-card">
            <div class="plugin-card-head">
              <div>
                <div class="plugin-card-title">${escapeHtml(item.name)} <span class="muted">(${pid})</span></div>
                <div class="muted" style="font-size:11px;">${escapeHtml(item.category || "compute")} · v${escapeHtml(item.version || "1.0.0")} · 并发上限 ${Number(item.max_concurrency || 1)}</div>
              </div>
              <span class="cat-tag">${escapeHtml(item.runtime || "builtin")}</span>
            </div>
            <div class="plugin-card-desc">${escapeHtml(item.description || "")}</div>
            <div class="plugin-card-tags">
              ${capBadges}
              <span class="cat-tag" title="支持扩展名">支持：${escapeHtml(extText)}</span>
            </div>
            <div class="muted" style="font-size:11px;">
              依赖：${escapeHtml(item.requirements || "内置零依赖")} · 集群分布：${escapeHtml(installedNodes.length > 0 ? installedNodes.join("，") : "尚未安装")}
            </div>
            <div class="row-actions" style="margin-top:auto;padding-top:4px;">${actionBtns}</div>
          </div>
        `;
      }).join("");
    }
  }

  if (capEl) {
    const caps = state.clusterCapabilities || [];
    const disabledRows = [];
    for (const n of nodes) {
      for (const p of n.plugins || []) {
        if (!p.enabled) {
          disabledRows.push({ node: n, plugin: p });
        }
      }
    }
    if (caps.length === 0 && disabledRows.length === 0) {
      capEl.innerHTML = `<div class="muted">集群尚未安装或启用任何算力插件，可在上方市场选择节点一键安装。</div>`;
    } else {
      const capHtml = caps.map((c) => {
        const providersHtml = (c.providers || []).map((pv) => `
          <span class="plugin-provider-tag">
            <span>${escapeHtml(pv.node_name || pv.node_id)}${pv.is_self ? " (主本机)" : ""}</span>
            <span class="muted">负载 ${Number(pv.busy_jobs || 0)}/${Number(pv.max_concurrency || 1)} · CPU ${Number(pv.cpu_percent || 0).toFixed(0)}%${pv.has_gpu ? " · GPU" : ""}</span>
            <button type="button" class="btn btn-ghost btn-xs" data-plugin-act="toggle" data-node="${escapeHtml(pv.node_id)}" data-plugin="${escapeHtml(c.plugin_id)}" data-enabled="false">停用</button>
            <button type="button" class="btn btn-danger btn-xs" data-plugin-act="uninstall" data-node="${escapeHtml(pv.node_id)}" data-plugin="${escapeHtml(c.plugin_id)}">卸载</button>
          </span>
        `).join("");
        return `
          <div class="plugin-cap-row">
            <div class="plugin-cap-row-head">
              <div>
                <strong>${escapeHtml(c.name || c.capability)}</strong>
                <span class="plugin-cap-pill">${escapeHtml(c.capability)}</span>
                <span class="muted" style="font-size:12px;">插件：${escapeHtml(c.plugin_id)} · 扩展名：${escapeHtml((c.supported_exts || []).join(", "))}</span>
              </div>
              <span class="status-pill status-ok">${(c.providers || []).length} 个算力节点在线</span>
            </div>
            <div class="plugin-provider-list">${providersHtml}</div>
          </div>
        `;
      }).join("");

      const disabledHtml = disabledRows.length > 0
        ? `
          <div class="plugin-cap-row">
            <div class="plugin-cap-row-head">
              <strong>已安装但停用的节点插件</strong>
              <span class="status-pill status-off">${disabledRows.length} 项停用</span>
            </div>
            <div class="plugin-provider-list">
              ${disabledRows.map(({ node, plugin }) => `
                <span class="plugin-provider-tag disabled">
                  <span>${escapeHtml(plugin.name || plugin.plugin_id)} @ ${escapeHtml(node.node_name)}</span>
                  <button type="button" class="btn btn-primary btn-xs" data-plugin-act="toggle" data-node="${escapeHtml(node.node_id)}" data-plugin="${escapeHtml(plugin.plugin_id)}" data-enabled="true">启用</button>
                  <button type="button" class="btn btn-danger btn-xs" data-plugin-act="uninstall" data-node="${escapeHtml(node.node_id)}" data-plugin="${escapeHtml(plugin.plugin_id)}">卸载</button>
                </span>
              `).join("")}
            </div>
          </div>
        `
        : "";

      capEl.innerHTML = capHtml + disabledHtml;
    }
  }
}

function renderPluginJobs() {
  const tbody = $("plugins-jobs-tbody");
  if (!tbody) return;
  const jobs = state.pluginJobs || [];
  if (jobs.length === 0) {
    tbody.innerHTML = `<tr><td colspan="6" class="muted" style="text-align:center;">暂无分布式算力任务记录</td></tr>`;
    return;
  }
  tbody.innerHTML = jobs.slice(0, 50).map((j) => {
    const statusCls = j.status === "succeeded" ? "status-ok" : j.status === "failed" ? "status-off" : "";
    const statusLabel = j.status === "succeeded" ? "成功" : j.status === "failed" ? "失败" : j.status || "运行中";
    const summaryText = escapeHtml(j.summary || j.error || "-");
    const outLink = j.output_file_id
      ? ` <a class="btn btn-ghost btn-xs" href="/api/files/${escapeHtml(j.output_file_id)}/download">下载 ${escapeHtml(j.output_file_name || "产物")}</a>`
      : "";
    return `
      <tr>
        <td>${escapeHtml(j.source_file_name || j.source_file_id || "-")}</td>
        <td><strong>${escapeHtml(j.plugin_id || "-")}</strong> <span class="muted">(${escapeHtml(j.capability || "-")})</span></td>
        <td>${escapeHtml(j.compute_node_name || j.compute_node_id || "-")}</td>
        <td><span class="status-pill ${statusCls}">${escapeHtml(statusLabel)}</span></td>
        <td>${summaryText}${outLink}</td>
        <td>${Number(j.duration_ms || 0)} ms</td>
      </tr>
    `;
  }).join("");
}

async function installPluginOnNode(nodeId, pluginId) {
  try {
    const data = await api("/api/admin/plugins/install", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ node_id: nodeId, plugin_id: pluginId }),
    });
    syncPluginMutationState(data);
    showToast(`已在节点安装并启用插件 ${pluginId}`, "success");
  } catch (err) {
    showToast(err.message, "error");
  }
}

async function togglePluginOnNode(nodeId, pluginId, enabled) {
  try {
    const data = await api("/api/admin/plugins/toggle", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ node_id: nodeId, plugin_id: pluginId, enabled }),
    });
    syncPluginMutationState(data);
    showToast(`已${enabled ? "启用" : "停用"}插件 ${pluginId}`, "success");
  } catch (err) {
    showToast(err.message, "error");
  }
}

async function uninstallPluginOnNode(nodeId, pluginId) {
  if (!confirm(`确定要从该节点卸载插件 ${pluginId} 吗？`)) return;
  try {
    const data = await api(`/api/admin/plugins/${encodeURIComponent(nodeId)}/${encodeURIComponent(pluginId)}`, {
      method: "DELETE",
    });
    syncPluginMutationState(data);
    showToast(`已卸载插件 ${pluginId}`, "success");
  } catch (err) {
    showToast(err.message, "error");
  }
}

function closePluginRunModal() {
  state.activePluginRunFileId = "";
  const modal = $("plugin-run-modal");
  if (modal) modal.classList.add("hidden");
}

function openPluginRunModal(fileId) {
  const rec = state.files.find((f) => f.id === fileId);
  if (!rec) return;
  const modal = $("plugin-run-modal");
  const titleEl = $("plugin-run-file-title");
  const listEl = $("plugin-run-list");
  const resEl = $("plugin-run-result");
  if (!modal || !listEl) return;

  state.activePluginRunFileId = fileId;
  if (titleEl) {
    titleEl.textContent = `${rec.name} (${formatSize(rec.size)}) · 当前存储节点：${rec.node_name || rec.node_id || "本机"}`;
  }
  if (resEl) {
    resEl.className = "plugin-run-result hidden";
    resEl.innerHTML = "";
  }

  const matches = getMatchingCapabilitiesForFile(rec.name);
  if (matches.length === 0) {
    listEl.innerHTML = `<div class="muted">当前集群没有匹配此文件类型的在线算力插件，管理员可在「设置 → 插件市场与算力」中安装启用。</div>`;
  } else {
    listEl.innerHTML = matches.map((cap) => {
      const providersText = (cap.providers || [])
        .map((pv) => `${pv.node_name || pv.node_id}${pv.has_gpu ? " (GPU)" : ""} [${Number(pv.busy_jobs || 0)}/${Number(pv.max_concurrency || 1)}]`)
        .join("、");
      return `
        <div class="plugin-run-item">
          <div class="plugin-run-item-info">
            <div class="plugin-run-item-title">${escapeHtml(cap.name || cap.capability)} <span class="plugin-cap-pill">${escapeHtml(cap.capability)}</span></div>
            <div class="plugin-run-item-meta">插件：${escapeHtml(cap.plugin_id)} · 可用算力节点：${escapeHtml(providersText || "自动分配")}</div>
          </div>
          <button type="button" class="btn btn-primary btn-xs" data-run-cap="${escapeHtml(cap.capability)}" data-run-plugin="${escapeHtml(cap.plugin_id)}">调度执行</button>
        </div>
      `;
    }).join("");
  }
  modal.classList.remove("hidden");
}

async function executePluginJob(fileId, capability, pluginId, triggerBtn) {
  const listEl = $("plugin-run-list");
  const resEl = $("plugin-run-result");
  const buttons = listEl ? Array.from(listEl.querySelectorAll("button")) : [];
  buttons.forEach((b) => { b.disabled = true; });
  if (triggerBtn) triggerBtn.textContent = "算力调度执行中...";
  if (resEl) {
    resEl.className = "plugin-run-result";
    resEl.innerHTML = `正在为文件调度最优算力节点并执行 <strong>${escapeHtml(pluginId || capability)}</strong>...`;
  }
  try {
    const data = await api("/api/plugins/jobs", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ file_id: fileId, capability, plugin_id: pluginId }),
    });
    const job = data.job || {};
    const outFile = data.output_file || null;
    const outId = outFile?.id || job.output_file_id || "";
    const outName = outFile?.name || job.output_file_name || "产物文件";
    if (resEl) {
      resEl.className = "plugin-run-result";
      resEl.innerHTML = `
        <div><strong>算力执行完成</strong>（执行节点：${escapeHtml(job.compute_node_name || job.compute_node_id || "-")} · 耗时 ${Number(job.duration_ms || 0)} ms）</div>
        <div style="margin-top:4px;">${escapeHtml(job.summary || "处理已完成")}</div>
        ${outId ? `<div class="row-actions" style="margin-top:8px;"><a class="btn btn-primary btn-xs" href="/api/files/${escapeHtml(outId)}/download">下载产物 (${escapeHtml(outName)})</a><span class="muted" style="font-size:12px;">已保存至「插件产物/${escapeHtml(job.plugin_id || pluginId)}」目录</span></div>` : ""}
      `;
    }
    showToast(`算力任务完成：已由 ${job.compute_node_name || "集群节点"} 执行`, "success");
    await Promise.all([refreshAll(), loadPluginJobs()]);
  } catch (err) {
    if (resEl) {
      resEl.className = "plugin-run-result error";
      resEl.innerHTML = `<strong>算力执行失败：</strong>${escapeHtml(err.message)}`;
    }
    showToast(err.message, "error");
  } finally {
    buttons.forEach((b) => { b.disabled = false; });
    if (triggerBtn) triggerBtn.textContent = "调度执行";
  }
}

function renderUpdateUI() {
  const st = state.updateStatus;
  if (!st || !$("upd-cur-version")) return;

  $("upd-cur-version").textContent = st.current_version || state.appVersion || "V0.12.0";
  $("upd-latest-version").textContent = st.latest_version || "尚未检查";
  $("upd-platform").textContent = `${st.os || "-"}/${st.arch || "-"}`;
  $("upd-checked-at").textContent = st.checked_at || "未检查";

  const banner = $("upd-status-banner");
  if (banner) {
    banner.className = "info-box";
    if (st.state === "error" || st.last_error) {
      banner.className = "error-box";
      banner.textContent = st.last_error || st.message || "更新出错";
    } else if (st.has_update) {
      banner.className = "success-box";
      const assetInfo = st.asset_name ? ` · 安装包: ${st.asset_name} (${formatSize(st.asset_size || 0)})` : "";
      banner.textContent = (st.message || `发现新版本 ${st.latest_version}`) + assetInfo;
    } else {
      banner.textContent = st.message || "当前已是最新版本";
    }
  }

  const busy = ["downloading", "verifying", "swapping", "restarting"].includes(st.state);
  const progWrap = $("upd-progress-wrap");
  if (progWrap) {
    progWrap.classList.toggle("hidden", !(busy || st.state === "done"));
    $("upd-progress-fill").style.width = `${Math.min(100, Math.max(0, Number(st.progress || 0)))}%`;
    $("upd-progress-text").textContent = `${Number(st.progress || 0)}%`;
  }

  const applyBtn = $("btn-apply-update");
  if (applyBtn) {
    applyBtn.classList.toggle("hidden", !(st.has_update && st.asset_url));
    applyBtn.disabled = busy;
    applyBtn.textContent = busy ? "正在升级切换..." : `立即不停机更新至 ${st.latest_version || ""}`;
  }
  if ($("btn-check-update")) {
    $("btn-check-update").disabled = busy || st.state === "checking";
  }

  const relLink = $("update-release-link");
  if (relLink) {
    relLink.classList.toggle("hidden", !st.release_url);
    if (st.release_url) relLink.href = st.release_url;
  }

  const notesBox = $("upd-notes-box");
  if (notesBox) {
    const notes = (st.release_notes || "").trim();
    notesBox.classList.toggle("hidden", !notes);
    if (notes) notesBox.textContent = notes;
  }

  if ($("update-repo") && document.activeElement !== $("update-repo")) {
    $("update-repo").value = st.repo || "";
  }
  if ($("update-proxy") && document.activeElement !== $("update-proxy")) {
    $("update-proxy").value = st.proxy_url || "";
  }
  if ($("toggle-auto-check") && document.activeElement !== $("toggle-auto-check")) {
    $("toggle-auto-check").checked = Boolean(st.auto_check);
  }
}

async function checkForUpdate() {
  const btn = $("btn-check-update");
  if (btn) {
    btn.disabled = true;
    btn.textContent = "检查中...";
  }
  try {
    const data = await api("/api/admin/update/check", { method: "POST" });
    if (data.status) applyUpdateStatus(data.status);
    showToast(data.status?.message || "检查更新完成", data.status?.has_update ? "success" : "info");
  } catch (err) {
    showToast(err.message, "error");
    api("/api/admin/update").then((d) => { if (d.status) applyUpdateStatus(d.status); }).catch(() => {});
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.textContent = "立即检查更新";
    }
  }
}

async function applyUpdate() {
  const ver = state.updateStatus?.latest_version || "";
  if (!confirm(`确定要不停机在线升级至 ${ver} 吗？\n系统将下载新版本、校验 SHA256 与可启动性后平滑接管端口，正在传输的文件不会中断。`)) {
    return;
  }
  try {
    const data = await api("/api/admin/update/apply", { method: "POST" });
    if (data.status) applyUpdateStatus(data.status);
    showToast("已启动后台下载与不停机升级", "info");
  } catch (err) {
    showToast(err.message, "error");
  }
}

async function saveUpdateConfig() {
  try {
    const data = await api("/api/admin/update/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        repo: $("update-repo")?.value.trim() || "",
        proxy_url: $("update-proxy")?.value.trim() || "",
        auto_check: Boolean($("toggle-auto-check")?.checked),
      }),
    });
    if (data.status) applyUpdateStatus(data.status);
    showToast("升级仓库配置已保存", "success");
  } catch (err) {
    showToast(err.message, "error");
  }
}

function renderAdminEndpointControls() {
  const sel = $("admin-expose-ip-select");
  const customWrap = $("admin-expose-ip-custom-wrap");
  const customInput = $("admin-expose-ip-custom");
  const portInput = $("admin-expose-port");
  const nicGrid = $("admin-nic-cards-list");
  if (!sel || !portInput) return;

  const addrs = state.availableAddrs || [];
  const opts = [`<option value="">自动选择已启用网卡中的推荐局域网 IP</option>`];
  const seenIPs = new Set();
  for (const a of addrs) {
    seenIPs.add(a.ip);
    const statusTag = a.enabled !== false ? "已启用" : "已关闭";
    opts.push(
      `<option value="${escapeHtml(a.ip)}">${escapeHtml(a.ip)} · ${escapeHtml(a.iface)} (${escapeHtml(a.tag)} · ${statusTag})</option>`
    );
  }
  if (!seenIPs.has("127.0.0.1")) {
    opts.push(`<option value="127.0.0.1">127.0.0.1 · 仅本机回环</option>`);
    seenIPs.add("127.0.0.1");
  }
  opts.push(`<option value="__custom__">手动输入自定义 IP / 域名</option>`);
  sel.innerHTML = opts.join("");

  const curIP = state.exposeIP || "";
  if (curIP === "") {
    sel.value = "";
    customWrap.classList.add("hidden");
  } else if (seenIPs.has(curIP)) {
    sel.value = curIP;
    customWrap.classList.add("hidden");
  } else {
    sel.value = "__custom__";
    customWrap.classList.remove("hidden");
    customInput.value = curIP;
  }
  portInput.value = state.port || "8080";

  if (nicGrid) {
    if (addrs.length === 0) {
      nicGrid.innerHTML = `<div style="color:var(--muted);font-size:12.5px;">未检测到本机活动网卡</div>`;
      return;
    }
    nicGrid.innerHTML = addrs
      .map((a) => {
        const enabled = a.enabled !== false;
        const isPrimary = Boolean(a.primary);
        const isVirtual = a.tag === "虚拟网卡";
        return `
          <div class="nic-card ${enabled ? "enabled" : "disabled"} ${isPrimary ? "primary" : ""}" data-nic-card-ip="${escapeHtml(a.ip)}">
            <div class="nic-card-top">
              <div class="nic-card-info">
                <div class="nic-card-name">
                  <strong>${escapeHtml(a.iface || "网卡")}</strong>
                  <span class="${isVirtual ? "cat-tag" : "status-pill status-ok"}">${escapeHtml(a.tag || "网络")}</span>
                  ${isPrimary ? `<span class="status-pill status-ok">主选入口</span>` : ""}
                </div>
                <div class="nic-card-url">${escapeHtml(a.url)}</div>
              </div>
              <label class="toggle-switch" title="${enabled ? "点击关闭该网卡入口" : "点击开启该网卡入口"}">
                <input type="checkbox" data-nic-toggle-ip="${escapeHtml(a.ip)}" ${enabled ? "checked" : ""} />
                <span class="toggle-slider"></span>
              </label>
            </div>
            <div class="nic-card-actions">
              <span class="nic-card-state">${enabled ? "对外展示并允许访问" : "已关闭（不对外展示）"}</span>
              <div class="row-actions">
                ${!isPrimary ? `<button type="button" class="btn btn-ghost btn-xs" data-nic-set-primary="${escapeHtml(a.ip)}">设为主选</button>` : ""}
                <button type="button" class="btn btn-ghost btn-xs" data-copy-url="${escapeHtml(a.url)}">复制</button>
              </div>
            </div>
          </div>
        `;
      })
      .join("");
  }
}

async function saveAdminEndpointSettings(customOverride = null) {
  const selVal = $("admin-expose-ip-select")?.value || "";
  let expose_ip = selVal === "__custom__" ? ($("admin-expose-ip-custom")?.value || "").trim() : selVal;
  if (customOverride && typeof customOverride.expose_ip === "string") {
    expose_ip = customOverride.expose_ip;
  }
  const port = String($("admin-expose-port")?.value || state.port || "8080").trim();

  let expose_ips = [];
  let disabled_ips = [];
  if (customOverride && Array.isArray(customOverride.expose_ips) && Array.isArray(customOverride.disabled_ips)) {
    expose_ips = customOverride.expose_ips;
    disabled_ips = customOverride.disabled_ips;
  } else {
    const checkboxes = document.querySelectorAll("[data-nic-toggle-ip]");
    checkboxes.forEach((cb) => {
      const ip = cb.dataset.nicToggleIp;
      if (!ip) return;
      if (cb.checked) expose_ips.push(ip);
      else disabled_ips.push(ip);
    });
  }
  if (expose_ip && !expose_ips.includes(expose_ip)) {
    expose_ips.push(expose_ip);
    disabled_ips = disabled_ips.filter((x) => x !== expose_ip);
  }
  if (expose_ips.length === 0 && (state.availableAddrs || []).length > 0) {
    showToast("请至少保留开启 1 个网卡入口地址", "error");
    renderAdminEndpointControls();
    return;
  }

  try {
    const data = await api("/api/admin/cluster/endpoint", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ expose_ip, expose_ips, disabled_ips, port }),
    });
    state.exposeIP = data.expose_ip || "";
    if (Array.isArray(data.expose_ips)) state.exposeIPs = data.expose_ips;
    if (Array.isArray(data.disabled_ips)) state.disabledIPs = data.disabled_ips;
    if (Array.isArray(data.available_addrs)) state.availableAddrs = data.available_addrs;
    if (Array.isArray(data.entry_addrs)) {
      state.entryAddrs = data.entry_addrs;
      state.serverInfo.entry_addrs = data.entry_addrs;
    }
    state.port = data.port || state.port;
    state.pool = data.pool || state.pool;
    state.serverInfo.entry_url = data.entry_url;
    renderAccessAddresses();
    renderAdminCluster();
    if (data.port_changed) {
      showToast(`监听端口已切换至 ${data.port}，正在自动跳转...`, "success");
      setTimeout(() => {
        window.location.href = `${location.protocol}//${location.hostname}:${data.port}/`;
      }, 900);
    } else {
      const enabledCount = (state.entryAddrs || []).length || 1;
      showToast(`网卡入口设置已生效（共开启 ${enabledCount} 个入口，主选：${data.entry_url}）`, "success");
    }
  } catch (err) {
    showToast(err.message, "error");
  }
}

function renderAdminCluster() {
  if (!state.pool) return;
  $("admin-entry-url").textContent = state.pool.entry_url || state.serverInfo.entry_url || location.origin;
  $("admin-cluster-secret").textContent = state.secretVisible ? state.clusterSecret : "••••••••••••••••";
  $("btn-toggle-secret").textContent = state.secretVisible ? "隐藏" : "显示";

  renderAdminEndpointControls();

  const dissolveBtn = $("btn-dissolve-cluster");
  if (dissolveBtn) {
    const total = Number(state.pool.total_nodes || (state.pool.nodes ? state.pool.nodes.length : 1));
    dissolveBtn.classList.toggle("hidden", total <= 1);
  }
  if ($("toggle-auto-sync-workers")) {
    $("toggle-auto-sync-workers").checked = state.pool.auto_sync_workers !== false;
  }

  const entryVer = state.pool.entry_version || state.appVersion || "V0.12.0";
  const tbody = $("cluster-nodes-tbody");
  tbody.innerHTML = "";
  for (const n of state.pool.nodes || []) {
    const q = splitQuotaUnit(n.quota_bytes);
    const nid = escapeHtml(n.node_id);
    const nnameAttr = escapeHtml(n.node_name || n.node_id || "");
    const nodeVer = n.version || (n.is_self ? entryVer : "V0.11.21.4");
    const isBehind = !n.is_self && compareVersionsClient(nodeVer, entryVer) < 0;
    const statusPill = n.online
      ? `<span class="status-pill status-ok">在线</span>`
      : `<span class="status-pill status-off">离线 (${escapeHtml(formatRelativeTime(n.last_seen))})</span>`;
    const fileInfo = n.online
      ? `${Number(n.file_count || 0)} 个文件 · ${formatSize(n.used_bytes)}`
      : `<span style="color:var(--muted)">文件已隐藏</span>`;
    const syncVerBtn = !n.is_self && n.online
      ? `<button class="btn ${isBehind ? "btn-primary" : "btn-ghost"} btn-xs" type="button" data-sync-node-version="${nid}" data-node-name="${nnameAttr}" title="将主节点当前版本 (${escapeHtml(entryVer)}) 同步推送至该分节点">${isBehind ? "同步版本" : "重推版本"}</button>`
      : "";
    const promoteBtn = n.online && n.role !== "entry"
      ? `<button class="btn btn-ghost btn-xs" type="button" data-promote-node="${nid}" data-node-name="${nnameAttr}" title="将该在线节点设为集群主节点">设为主节点</button>`
      : "";
    const removeBtn = !n.is_self
      ? (n.online
        ? `<button class="btn btn-danger btn-xs" type="button" data-remove-node="${nid}" data-node-online="1" data-node-name="${nnameAttr}" title="通知该节点退出当前集群并恢复为独立节点">移出集群</button>`
        : `<button class="btn btn-danger btn-xs" type="button" data-remove-node="${nid}" data-node-online="0" data-node-name="${nnameAttr}" title="从节点列表删除已停止的离线节点">删除节点</button>`)
      : "";
    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td>
        <div><strong>${escapeHtml(n.node_name)}</strong>${n.is_self ? ' <span class="cat-tag">本机</span>' : ""}</div>
        ${n.http_url ? `<div class="node-url-sub">${escapeHtml(n.http_url)}</div>` : ""}
      </td>
      <td>
        <div>${n.role === "entry" ? '<span class="status-pill status-ok">主入口</span>' : '<span class="cat-tag">扩容节点</span>'}</div>
        <div style="margin-top:4px;"><span class="ver-badge">${escapeHtml(nodeVer)}</span>${isBehind ? ` <span class="status-pill status-warn" style="font-size:10.5px;">待同步</span>` : ""}</div>
      </td>
      <td>${escapeHtml(n.os)}/${escapeHtml(n.arch)}</td>
      <td>${statusPill}</td>
      <td>${fileInfo}</td>
      <td>
        <div class="quota-editor">
          <input class="quota-input" type="number" step="any" min="1" value="${escapeHtml(q.value)}" data-quota-input="${nid}" />
          <select class="quota-unit-select" data-quota-unit="${nid}">
            <option value="MB" ${q.unit === "MB" ? "selected" : ""}>MB</option>
            <option value="GB" ${q.unit === "GB" ? "selected" : ""}>GB</option>
            <option value="TB" ${q.unit === "TB" ? "selected" : ""}>TB</option>
          </select>
        </div>
      </td>
      <td>
        <div class="row-actions">
          ${syncVerBtn}
          ${promoteBtn}
          <button class="btn btn-primary btn-xs" type="button" data-save-quota="${nid}" ${n.online ? "" : "disabled"}>保存配额</button>
          ${removeBtn}
        </div>
      </td>
    `;
    tbody.appendChild(tr);

    const drives = Array.isArray(n.drives) ? n.drives : [];
    const driveOpts = [`<option value="">选择磁盘 / 挂载点...</option>`];
    for (const d of drives) {
      driveOpts.push(
        `<option value="${escapeHtml(d.default_dir)}">${escapeHtml(d.label)}（可用 ${formatSize(d.free_bytes)} / ${formatSize(d.total_bytes)}）</option>`
      );
    }

    const storageTr = document.createElement("tr");
    storageTr.className = "cluster-storage-row";
    storageTr.innerHTML = `
      <td colspan="7">
        <div class="storage-dir-editor">
          <span class="storage-dir-label">存储盘与路径：</span>
          <select class="storage-drive-select" data-drive-select="${nid}" ${n.online ? "" : "disabled"}>
            ${driveOpts.join("")}
          </select>
          <input class="storage-dir-input" type="text" data-storage-input="${nid}" value="${escapeHtml(n.storage_dir || "")}" placeholder="完整存储路径，如 D:\\LFH-Storage 或 /mnt/data/lfh" ${n.online ? "" : "disabled"} />
          <label class="storage-migrate-check" title="勾选后自动把该节点已有上传文件搬移到新盘/新目录">
            <input type="checkbox" data-storage-migrate="${nid}" ${n.online ? "" : "disabled"} />
            <span>迁移已有文件</span>
          </label>
          <button class="btn btn-ghost btn-xs" type="button" data-save-storage="${nid}" ${n.online ? "" : "disabled"}>切换存储位置</button>
        </div>
        <div class="cluster-clipboard-bar">
          <label class="cluster-clipboard-toggle">
            <input type="checkbox" data-clipboard-toggle="${nid}" ${n.clipboard_enabled ? "checked" : ""} ${n.online ? "" : "disabled"} />
            <span>开启超级剪贴板</span>
            <span class="clip-status-badge ${n.clipboard_enabled ? "on" : "off"}">${n.clipboard_enabled ? "已开启" : "已关闭"}</span>
          </label>
          <span class="cluster-clipboard-hint">${n.online ? "开启后自动命名并上传该节点本机复制的文本/图片至「超级剪贴板/YYYY-MM」" : "节点离线，无法远程切换超级剪贴板"}</span>
        </div>
      </td>
    `;
    tbody.appendChild(storageTr);
  }
}

async function loadPassiveDiscovered() {
  try {
    const data = await api("/api/admin/cluster/discovered");
    if (Array.isArray(data.nodes)) {
      state.discoveredNodes = data.nodes;
      renderDiscoveredNodes();
    }
  } catch {}
}

function renderDiscoveredNodes() {
  const wrap = $("discovered-nodes-list");
  if (!wrap) return;
  const list = state.discoveredNodes || [];
  if (list.length === 0) {
    wrap.innerHTML = `<div style="color:var(--muted);font-size:12.5px;padding:6px 0;">暂未检测到局域网中的其他独立 LFH 节点。可点击上方「主动扫描局域网」或直接输入对方 IP:端口 进行探测。</div>`;
    return;
  }

  const srcLabel = { udp: "UDP 广播发现", lan_scan: "局域网扫描", manual: "手动探测" };
  wrap.innerHTML = "";
  for (const d of list) {
    const card = document.createElement("div");
    card.className = "discovered-card";
    const statusBadge = d.already_in_pool
      ? `<span class="status-pill status-ok">已在当前集群</span>`
      : `<span class="status-pill status-warn">未加入本集群</span>`;
    const roleText = d.role === "worker" ? "Worker" : "Entry";
    const srcText = srcLabel[d.source] || "探测";
    const adoptBtn = d.already_in_pool
      ? ""
      : `<button class="btn btn-primary btn-xs" type="button" data-adopt-url="${escapeHtml(d.url)}" data-adopt-name="${escapeHtml(d.node_name)}">纳管加入本集群</button>`;
    const joinBtn = d.role === "entry" && !d.already_in_pool
      ? `<button class="btn btn-ghost btn-xs" type="button" data-prefill-join="${escapeHtml(d.url)}">将本机加入它</button>`
      : "";

    card.innerHTML = `
      <div class="discovered-card-top">
        <div>
          <div class="discovered-name">${escapeHtml(d.node_name || d.node_id)}</div>
          <div class="discovered-url">${escapeHtml(d.url)}</div>
        </div>
        ${statusBadge}
      </div>
      <div class="discovered-meta">
        <span>${escapeHtml(roleText)}</span>
        <span>·</span>
        <span>${escapeHtml(d.os || "-")}/${escapeHtml(d.arch || "-")}</span>
        ${d.version ? `<span>·</span><span>${escapeHtml(d.version)}</span>` : ""}
        <span>·</span>
        <span>${Number(d.file_count || 0)} 文件</span>
        <span>·</span>
        <span>${escapeHtml(srcText)}</span>
      </div>
      <div class="discovered-actions">
        ${adoptBtn}
        ${joinBtn}
        <button class="btn btn-ghost btn-xs" type="button" data-copy-url="${escapeHtml(d.url)}">复制地址</button>
      </div>
    `;
    wrap.appendChild(card);
  }
}

function setAdoptAuthMode(mode) {
  state.adoptAuthMode = mode === "secret" ? "secret" : "admin";
  const isAdmin = state.adoptAuthMode === "admin";
  if ($("adopt-auth-tab-admin")) $("adopt-auth-tab-admin").classList.toggle("active", isAdmin);
  if ($("adopt-auth-tab-secret")) $("adopt-auth-tab-secret").classList.toggle("active", !isAdmin);
  if ($("adopt-admin-panel")) $("adopt-admin-panel").classList.toggle("hidden", !isAdmin);
  if ($("adopt-secret-panel")) $("adopt-secret-panel").classList.toggle("hidden", isAdmin);
}

function openAdoptModal(url, name) {
  state.adoptTargetURL = url;
  state.adoptTargetName = name || url;
  if ($("adopt-target-info")) {
    $("adopt-target-info").textContent = `准备将节点 ${state.adoptTargetName} (${url}) 切换为扩容节点并接入当前主入口 ${state.pool?.entry_url || location.origin}`;
  }
  if ($("adopt-form")) $("adopt-form").reset();
  if ($("adopt-error")) $("adopt-error").classList.add("hidden");
  setAdoptAuthMode("admin");
  $("adopt-modal").classList.remove("hidden");
}

function closeAdoptModal() {
  $("adopt-modal").classList.add("hidden");
}

function renderInvitesTable(invites) {
  const tbody = $("invites-tbody");
  const empty = $("invites-empty");
  tbody.innerHTML = "";
  if (invites.length === 0) {
    empty.classList.remove("hidden");
    return;
  }
  empty.classList.add("hidden");

  const now = Date.now();
  for (const inv of invites) {
    const expired = inv.expires_at ? new Date(inv.expires_at).getTime() < now : false;
    const exhausted = inv.max_uses > 0 && inv.used_count >= inv.max_uses;
    let statusBadge = `<span class="status-pill status-ok">有效</span>`;
    if (expired) statusBadge = `<span class="status-pill status-off">已过期</span>`;
    else if (exhausted) statusBadge = `<span class="status-pill status-warn">已用完</span>`;

    const maxText = inv.max_uses === 0 ? "不限" : String(inv.max_uses);
    const expText = inv.expires_at ? formatTime(inv.expires_at) : "永不过期";

    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td>
        <code>${escapeHtml(inv.code)}</code>
        <button class="btn btn-ghost btn-xs" type="button" data-copy-url="${escapeHtml(inv.code)}">复制</button>
      </td>
      <td>${escapeHtml(inv.note || "-")}</td>
      <td>${inv.used_count} / ${maxText}</td>
      <td>${statusBadge}</td>
      <td>${escapeHtml(expText)}</td>
      <td><button class="btn btn-danger btn-xs" type="button" data-del-invite="${escapeHtml(inv.code)}">删除</button></td>
    `;
    tbody.appendChild(tr);
  }
}

function renderUsersTable(users) {
  const tbody = $("users-tbody");
  tbody.innerHTML = "";
  for (const u of users) {
    const st = u.status || "active";
    let statusPill = `<span class="status-pill status-ok">正常</span>`;
    if (st === "pending") statusPill = `<span class="status-pill status-warn">待审核</span>`;
    else if (st === "banned") statusPill = `<span class="status-pill status-off">已封禁</span>`;

    const isSelf = state.user && u.username === state.user.username;
    const eu = escapeHtml(u.username);
    let actionsHtml = `<span style="color:var(--muted);font-size:12px;">当前登录账号（受保护）</span>`;
    if (!isSelf) {
      if (u.role === "admin") {
        actionsHtml = `
          <div class="row-actions">
            <button class="btn btn-warn btn-xs" type="button" data-user-action="demote" data-username="${eu}">取消管理员</button>
          </div>
        `;
      } else if (st === "pending") {
        actionsHtml = `
          <div class="row-actions">
            <button class="btn btn-primary btn-xs" type="button" data-user-action="approve" data-username="${eu}">同意通过</button>
            <button class="btn btn-danger btn-xs" type="button" data-user-action="delete" data-username="${eu}">拒绝删除</button>
          </div>
        `;
      } else if (st === "banned") {
        actionsHtml = `
          <div class="row-actions">
            <button class="btn btn-primary btn-xs" type="button" data-user-action="unban" data-username="${eu}">解封</button>
            <button class="btn btn-danger btn-xs" type="button" data-user-action="delete" data-username="${eu}">删除</button>
          </div>
        `;
      } else {
        actionsHtml = `
          <div class="row-actions">
            <button class="btn btn-primary btn-xs" type="button" data-user-action="promote" data-username="${eu}">设为管理员</button>
            <button class="btn btn-warn btn-xs" type="button" data-user-action="ban" data-username="${eu}">封禁</button>
            <button class="btn btn-danger btn-xs" type="button" data-user-action="delete" data-username="${eu}">删除</button>
          </div>
        `;
      }
    }

    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td><strong>${escapeHtml(u.username)}</strong></td>
      <td>${u.role === "admin" ? '<span class="status-pill status-ok">管理员</span>' : '<span class="cat-tag">普通用户</span>'}</td>
      <td>${statusPill}</td>
      <td>${formatTime(u.created_at)}</td>
      <td>${actionsHtml}</td>
    `;
    tbody.appendChild(tr);
  }
}

function openMobileSidebar() {
  $("app-sidebar").classList.add("open");
  $("sidebar-backdrop").classList.remove("hidden");
}

function closeMobileSidebar() {
  $("app-sidebar").classList.remove("open");
  $("sidebar-backdrop").classList.add("hidden");
}

function bindEvents() {
  document.addEventListener("click", (e) => {
    const copyBtn = e.target.closest("[data-copy-url]");
    if (copyBtn) {
      copyText(copyBtn.dataset.copyUrl);
    }
  });

  // Theme toggles
  const toggleTheme = () => applyTheme(state.theme === "dark" ? "light" : "dark");
  $("btn-auth-theme").onclick = toggleTheme;
  $("btn-top-theme").onclick = toggleTheme;
  document.querySelectorAll("[data-set-theme]").forEach((card) => {
    card.onclick = () => {
      applyTheme(card.dataset.setTheme);
      showToast(`已切换为${state.theme === "light" ? "浅色" : "深色"}主题`, "success");
    };
  });

  $("tab-login").onclick = () => {
    state.isRegisterMode = false;
    $("tab-login").classList.add("active");
    $("tab-register").classList.remove("active");
    $("auth-error").classList.add("hidden");
    $("auth-success").classList.add("hidden");
    syncRegisterUI();
  };

  $("tab-register").onclick = () => {
    state.isRegisterMode = true;
    $("tab-register").classList.add("active");
    $("tab-login").classList.remove("active");
    $("auth-error").classList.add("hidden");
    $("auth-success").classList.add("hidden");
    syncRegisterUI();
  };

  $("auth-form").onsubmit = async (e) => {
    e.preventDefault();
    $("auth-error").classList.add("hidden");
    $("auth-success").classList.add("hidden");
    const username = $("auth-username").value.trim();
    const password = $("auth-password").value;
    const invite_code = $("auth-invite-code").value.trim();
    try {
      const endpoint = state.isRegisterMode ? "/api/register" : "/api/login";
      const payload = state.isRegisterMode
        ? { username, password, invite_code }
        : { username, password };
      const res = await api(endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      if (res.pending) {
        $("auth-password").value = "";
        state.isRegisterMode = false;
        $("tab-login").classList.add("active");
        $("tab-register").classList.remove("active");
        syncRegisterUI();
        $("auth-success").textContent = res.message || "注册申请已提交，请等待管理员同意后登录";
        $("auth-success").classList.remove("hidden");
        showToast("注册申请已提交，等待管理员同意", "success");
        return;
      }
      const me = await api("/api/me");
      state.serverInfo = {
        entry_url: me.entry_url || state.serverInfo.entry_url,
        entry_addrs: Array.isArray(me.entry_addrs) ? me.entry_addrs : state.serverInfo.entry_addrs,
        local_url: me.local_url || state.serverInfo.local_url,
        invite_required: typeof me.invite_required === "boolean" ? me.invite_required : state.serverInfo.invite_required,
        pool: me.pool || state.serverInfo.pool,
      };
      if (Array.isArray(me.entry_addrs)) {
        state.entryAddrs = me.entry_addrs;
      }
      if (me.version) {
        state.appVersion = me.version;
        syncVersionBadges();
      }
      if (me.pool) state.pool = me.pool;
      if (me.events_version) state.lastEventVersion = Number(me.events_version);
      if (me.upload_policy) updateUploadPolicy(me.upload_policy);
      onLoggedIn(me.user, me.pending_users || 0);
      if (me.update_status) applyUpdateStatus(me.update_status);
    } catch (err) {
      $("auth-error").textContent = err.message;
      $("auth-error").classList.remove("hidden");
    }
  };

  $("btn-logout").onclick = async () => {
    await api("/api/logout", { method: "POST" }).catch(() => {});
    state.user = null;
    showAuth();
  };

  $("btn-pick-files").onclick = () => $("file-input").click();
  $("file-input").onchange = (e) => {
    uploadFiles(e.target.files);
    e.target.value = "";
  };

  if ($("btn-pick-folder") && $("folder-input")) {
    $("btn-pick-folder").onclick = () => $("folder-input").click();
    $("folder-input").onchange = (e) => {
      uploadFiles(e.target.files);
      e.target.value = "";
    };
  }
  if ($("btn-create-txt")) $("btn-create-txt").onclick = openCreateTxtModal;
  if ($("btn-import-local")) $("btn-import-local").onclick = () => openLocalImportModal("import");

  // Highlight dropzone bar visually (actual drop handled once at window level to support full-screen auto-upload)
  const dz = $("dropzone");
  dz.addEventListener("dragenter", () => dz.classList.add("dragover"));
  dz.addEventListener("dragleave", () => dz.classList.remove("dragover"));
  dz.addEventListener("drop", () => dz.classList.remove("dragover"));

  // Global full-screen drag & drop auto-upload (files + folders + archives)
  const overlay = $("global-drop-overlay");
  let globalDragDepth = 0;
  const hasDragFiles = (e) => Boolean(e.dataTransfer && Array.from(e.dataTransfer.types || []).includes("Files"));
  const canAcceptGlobalDrop = () => Boolean(state.user && $("app-view") && !$("app-view").classList.contains("hidden"));
  const resetGlobalOverlay = () => {
    globalDragDepth = 0;
    if (overlay) overlay.classList.add("hidden");
    dz.classList.remove("dragover");
  };

  window.addEventListener("dragenter", (e) => {
    if (!hasDragFiles(e)) return;
    e.preventDefault();
    if (!canAcceptGlobalDrop()) return;
    globalDragDepth += 1;
    if (overlay) overlay.classList.remove("hidden");
  });

  window.addEventListener("dragover", (e) => {
    if (!hasDragFiles(e)) return;
    e.preventDefault();
    if (e.dataTransfer) {
      e.dataTransfer.dropEffect = canAcceptGlobalDrop() ? "copy" : "none";
    }
  });

  window.addEventListener("dragleave", (e) => {
    if (!hasDragFiles(e)) return;
    globalDragDepth = Math.max(0, globalDragDepth - 1);
    if (globalDragDepth === 0) {
      resetGlobalOverlay();
    }
  });

  window.addEventListener("dragend", resetGlobalOverlay);
  window.addEventListener("blur", resetGlobalOverlay);

  window.addEventListener("drop", async (e) => {
    if (!hasDragFiles(e)) {
      resetGlobalOverlay();
      return;
    }
    e.preventDefault();
    resetGlobalOverlay();
    if (!canAcceptGlobalDrop()) return;
    const entries = await collectEntriesFromDataTransfer(e.dataTransfer);
    if (entries.length > 0) {
      uploadEntries(entries);
    }
  });

  $("btn-reset-filters").onclick = () => {
    state.selectedMonth = "all";
    state.selectedCategory = "all";
    state.selectedDevice = "all";
    state.selectedNode = "all";
    state.searchQuery = "";
    $("search-input").value = "";
    refreshAll();
  };

  $("search-input").oninput = (e) => {
    state.searchQuery = e.target.value;
    loadFiles();
  };
  $("btn-refresh").onclick = () => refreshAll();

  if ($("view-switcher")) {
    $("view-switcher").onclick = (e) => {
      const btn = e.target.closest("[data-view-mode]");
      if (!btn) return;
      applyViewMode(btn.dataset.viewMode);
    };
  }

  if ($("m-quick-cats")) {
    $("m-quick-cats").onclick = (e) => {
      const pill = e.target.closest("[data-m-cat]");
      if (!pill) return;
      const cat = pill.dataset.mCat || "all";
      state.selectedCategory = cat === "all" ? "all" : state.selectedCategory === cat ? "all" : cat;
      syncMobileQuickCats();
      if (state.workspacePage !== "files") switchWorkspacePage("files");
      refreshAll();
    };
  }

  if ($("btn-auto-refresh")) {
    $("btn-auto-refresh").onclick = () => {
      state.autoRefresh = !state.autoRefresh;
      try {
        localStorage.setItem("lan_filehub_auto_refresh", state.autoRefresh ? "true" : "false");
      } catch {}
      if (state.autoRefresh) {
        startLiveSync();
        refreshAll();
        showToast("已开启实时自动刷新", "success");
      } else {
        stopLiveSync();
        showToast("已暂停实时自动刷新", "info");
      }
    };
  }

  document.addEventListener("visibilitychange", () => {
    if (
      document.visibilityState === "visible" &&
      state.user &&
      $("app-view") &&
      !$("app-view").classList.contains("hidden") &&
      state.autoRefresh
    ) {
      loadCluster();
      if (!state.eventSource || state.eventSource.readyState === EventSource.CLOSED) {
        startLiveSync();
      }
    }
  });

  const handleFilesInteraction = async (e) => {
    const folderChip = e.target.closest("[data-filter-folder]");
    if (folderChip) {
      const fp = folderChip.dataset.filterFolder;
      state.searchQuery = fp;
      $("search-input").value = fp;
      loadFiles();
      return;
    }

    const devChip = e.target.closest("[data-filter-device]");
    if (devChip) {
      const fp = devChip.dataset.filterDevice;
      state.selectedDevice = state.selectedDevice === fp ? "all" : fp;
      refreshAll();
      return;
    }

    const nodeChip = e.target.closest("[data-filter-node]");
    if (nodeChip) {
      const nid = nodeChip.dataset.filterNode;
      state.selectedNode = state.selectedNode === nid ? "all" : nid;
      refreshAll();
      return;
    }

    const btn = e.target.closest("[data-action]");
    if (!btn || btn.disabled) return;
    const { action, id } = btn.dataset;
    if (action === "preview") {
      openPreview(id);
    } else if (action === "queued-download") {
      const rec = state.files.find((f) => f.id === id);
      if (rec) enqueueRangeDownload(rec);
    } else if (action === "edit") {
      openTextEditor(id);
    } else if (action === "plugin-run") {
      openPluginRunModal(id);
    } else if (action === "versions") {
      openFileVersionsModal(id);
    } else if (action === "props") {
      openFileProperties(id);
    } else if (action === "access") {
      const rec = state.files.find((f) => f.id === id);
      if (rec) openFileAccessPicker(rec);
    } else if (action === "delete") {
      if (!confirm("确定要删除这个文件吗？")) return;
      state.isUploadingSelf = true;
      try {
        await api(`/api/files/${id}`, { method: "DELETE" });
        showToast("已删除文件", "success");
        refreshAll();
      } catch (err) {
        showToast(err.message, "error");
      } finally {
        setTimeout(() => {
          state.isUploadingSelf = false;
        }, 600);
      }
    }
  };

  $("files-tbody").onclick = handleFilesInteraction;
  if ($("files-grid")) {
    $("files-grid").onclick = handleFilesInteraction;
  }

  if ($("files-charts")) {
    $("files-charts").onclick = (e) => {
      const metricBtn = e.target.closest("[data-chart-metric]");
      if (metricBtn) {
        state.chartMetric = metricBtn.dataset.chartMetric === "size" ? "size" : "count";
        renderFilesCharts();
        return;
      }

      const catBtn = e.target.closest("[data-chart-category]");
      if (catBtn) {
        const key = catBtn.dataset.chartCategory;
        state.selectedCategory = state.selectedCategory === key ? "all" : key;
        refreshAll();
        return;
      }

      const monthBtn = e.target.closest("[data-chart-month]");
      if (monthBtn) {
        const key = monthBtn.dataset.chartMonth;
        state.selectedMonth = state.selectedMonth === key ? "all" : key;
        refreshAll();
        return;
      }

      const nodeBtn = e.target.closest("[data-chart-node]");
      if (nodeBtn) {
        const nid = nodeBtn.dataset.chartNode;
        state.selectedNode = state.selectedNode === nid ? "all" : nid;
        refreshAll();
        return;
      }

      const devBtn = e.target.closest("[data-chart-device]");
      if (devBtn) {
        const fp = devBtn.dataset.chartDevice;
        state.selectedDevice = state.selectedDevice === fp ? "all" : fp;
        refreshAll();
      }
    };
  }

  $("preview-close").onclick = closePreview;
  $("preview-backdrop").onclick = closePreview;
  if ($("editor-close")) $("editor-close").onclick = closeTextEditor;
  if ($("editor-backdrop")) $("editor-backdrop").onclick = closeTextEditor;
  if ($("editor-save-btn")) $("editor-save-btn").onclick = saveTextEditor;
  if ($("create-txt-close")) $("create-txt-close").onclick = closeCreateTxtModal;
  if ($("create-txt-backdrop")) $("create-txt-backdrop").onclick = closeCreateTxtModal;
  if ($("create-txt-save-btn")) $("create-txt-save-btn").onclick = saveCreateTxtFile;
  if ($("create-txt-content")) {
    const cta = $("create-txt-content");
    cta.oninput = updateCreateTxtStats;
    cta.onkeydown = (e) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
        e.preventDefault();
        saveCreateTxtFile();
      }
    };
  }
  if ($("local-import-close")) $("local-import-close").onclick = closeLocalImportModal;
  if ($("local-import-backdrop")) $("local-import-backdrop").onclick = closeLocalImportModal;
  if ($("local-import-submit-btn")) $("local-import-submit-btn").onclick = submitLocalImport;
  document.querySelectorAll("[data-local-import-mode]").forEach((btn) => {
    btn.onclick = () => setLocalImportMode(btn.dataset.localImportMode);
  });
  if ($("local-import-path")) {
    $("local-import-path").onkeydown = (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        submitLocalImport();
      }
    };
  }
  if ($("editor-btn-json-format")) $("editor-btn-json-format").onclick = () => formatEditorJson(false);
  if ($("editor-btn-json-minify")) $("editor-btn-json-minify").onclick = () => formatEditorJson(true);
  if ($("editor-btn-json-validate")) $("editor-btn-json-validate").onclick = validateEditorJson;
  if ($("editor-btn-md-preview")) {
    $("editor-btn-md-preview").onclick = () => {
      state.editorSplitMd = !state.editorSplitMd;
      $("editor-btn-md-preview").textContent = state.editorSplitMd ? "隐藏 Markdown 预览" : "分栏预览 Markdown";
      if ($("editor-workspace")) $("editor-workspace").classList.toggle("split", state.editorSplitMd);
      if ($("editor-md-preview")) $("editor-md-preview").classList.toggle("hidden", !state.editorSplitMd);
      updateEditorGutterAndStats();
    };
  }
  if ($("editor-textarea")) {
    const ta = $("editor-textarea");
    ta.oninput = () => {
      setEditorStatusPill("");
      updateEditorGutterAndStats();
    };
    ta.onscroll = () => {
      const gutter = $("editor-line-gutter");
      if (gutter) gutter.scrollTop = ta.scrollTop;
    };
    ta.onkeydown = (e) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
        e.preventDefault();
        saveTextEditor();
        return;
      }
      if (e.key === "Tab") {
        e.preventDefault();
        const start = ta.selectionStart;
        const end = ta.selectionEnd;
        const val = ta.value;
        ta.value = val.slice(0, start) + "  " + val.slice(end);
        ta.selectionStart = ta.selectionEnd = start + 2;
        updateEditorGutterAndStats();
      }
    };
  }
  $("props-close").onclick = closeFileProperties;
  $("props-backdrop").onclick = closeFileProperties;
  if ($("btn-upload-access")) {
    $("btn-upload-access").onclick = openUploadAccessPicker;
  }
  if ($("access-close")) $("access-close").onclick = closeAccessPicker;
  if ($("access-backdrop")) $("access-backdrop").onclick = closeAccessPicker;
  if ($("access-mode-all")) {
    $("access-mode-all").onclick = () => {
      state.accessModalIsCustom = false;
      updateAccessModeUI();
    };
  }
  if ($("access-mode-custom")) {
    $("access-mode-custom").onclick = () => {
      state.accessModalIsCustom = true;
      renderAccessModalUsers();
    };
  }
  if ($("access-user-search")) {
    $("access-user-search").oninput = renderAccessModalUsers;
  }
  if ($("access-btn-self-only")) {
    $("access-btn-self-only").onclick = () => {
      state.accessModalIsCustom = true;
      state.accessDraftSet = new Set(state.user ? [state.user.username] : []);
      renderAccessModalUsers();
    };
  }
  if ($("access-btn-clear")) {
    $("access-btn-clear").onclick = () => {
      state.accessDraftSet = new Set();
      renderAccessModalUsers();
    };
  }
  if ($("access-save-btn")) {
    $("access-save-btn").onclick = saveAccessPicker;
  }

  $("btn-toggle-sidebar").onclick = openMobileSidebar;
  $("btn-close-sidebar").onclick = closeMobileSidebar;
  $("sidebar-backdrop").onclick = closeMobileSidebar;

  // Unified Settings Modal
  $("btn-open-settings").onclick = () => {
    const defaultTab = state.user && state.user.role === "admin" && state.pendingUsersCount > 0 ? "users" : "general";
    openSettingsModal(defaultTab);
  };
  $("btn-sidebar-settings").onclick = () => {
    closeMobileSidebar();
    const defaultTab = state.user && state.user.role === "admin" && state.pendingUsersCount > 0 ? "users" : "general";
    openSettingsModal(defaultTab);
  };
  $("settings-close").onclick = () => $("settings-modal").classList.add("hidden");
  $("settings-backdrop").onclick = () => $("settings-modal").classList.add("hidden");

  $("settings-tabs").onclick = (e) => {
    const btn = e.target.closest("[data-settings-tab]");
    if (!btn) return;
    switchSettingsTab(btn.dataset.settingsTab);
  };

  // Invite Required Switch
  $("toggle-invite-required").onchange = async (e) => {
    const nextVal = Boolean(e.target.checked);
    try {
      const data = await api("/api/admin/settings", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ invite_required: nextVal }),
      });
      state.serverInfo.invite_required = Boolean(data.settings?.invite_required);
      syncRegisterUI();
      showToast(
        nextVal ? "已开启邀请码注册" : "已关闭邀请码（新注册将等待管理员同意）",
        "success"
      );
    } catch (err) {
      e.target.checked = !nextVal;
      showToast(err.message, "error");
    }
  };

  // Upload Policy Presets & Save
  const policyPresets = {
    clear: { allowed: "", blocked: "" },
    "safe-block": { allowed: "", blocked: "exe, msi, bat, cmd, com, scr, pif, vbs, ps1, dll, sh" },
    "office-media": {
      allowed: "pdf, doc, docx, xls, xlsx, ppt, pptx, md, txt, csv, jpg, jpeg, png, gif, webp, mp4, webm, mov, mp3, wav, flac, m4a, zip, tar, gz, tar.gz, tgz",
      blocked: "",
    },
    "docs-only": { allowed: "pdf, docx, xlsx, pptx, md, txt, jpg, jpeg, png, gif, webp, svg", blocked: "" },
  };
  document.querySelectorAll(".preset-chip[data-policy-preset]").forEach((btn) => {
    btn.onclick = () => {
      const p = policyPresets[btn.dataset.policyPreset];
      if (!p) return;
      if ($("setting-allowed-exts")) $("setting-allowed-exts").value = p.allowed;
      if ($("setting-blocked-exts")) $("setting-blocked-exts").value = p.blocked;
    };
  });

  if ($("btn-save-upload-policy")) {
    $("btn-save-upload-policy").onclick = async () => {
      const allowed_exts = normalizeExtList($("setting-allowed-exts")?.value || "");
      const blocked_exts = normalizeExtList($("setting-blocked-exts")?.value || "");
      try {
        const data = await api("/api/admin/settings", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ allowed_exts, blocked_exts }),
        });
        updateUploadPolicy(data.settings || { allowed_exts, blocked_exts });
        showToast("上传文件类型规则已保存并立即生效", "success");
      } catch (err) {
        showToast(err.message, "error");
      }
    };
  }

  // User Governance Actions
  $("btn-refresh-users").onclick = () => refreshAdminData();
  $("users-tbody").onclick = async (e) => {
    const btn = e.target.closest("[data-user-action]");
    if (!btn) return;
    const action = btn.dataset.userAction;
    const username = btn.dataset.username;
    const enc = encodeURIComponent(username);
    try {
      if (action === "approve") {
        await api(`/api/admin/users/${enc}/approve`, { method: "POST" });
        showToast(`已同意用户 ${username} 注册`, "success");
      } else if (action === "promote") {
        if (!confirm(`确定要授予用户 ${username} 管理员权限吗？`)) return;
        await api(`/api/admin/users/${enc}/role`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ role: "admin" }),
        });
        showToast(`已授予 ${username} 管理员权限`, "success");
      } else if (action === "demote") {
        if (!confirm(`确定要取消用户 ${username} 的管理员权限，降为普通用户吗？`)) return;
        await api(`/api/admin/users/${enc}/role`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ role: "user" }),
        });
        showToast(`已取消 ${username} 的管理员权限`, "success");
      } else if (action === "ban") {
        if (!confirm(`确定要封禁用户 ${username} 吗？封禁后其当前登录会立即失效。`)) return;
        await api(`/api/admin/users/${enc}/ban`, { method: "POST" });
        showToast(`已封禁用户 ${username}`, "success");
      } else if (action === "unban") {
        await api(`/api/admin/users/${enc}/unban`, { method: "POST" });
        showToast(`已解封用户 ${username}`, "success");
      } else if (action === "delete") {
        if (!confirm(`确定要彻底删除用户 ${username} 吗？此操作不可撤销。`)) return;
        await api(`/api/admin/users/${enc}`, { method: "DELETE" });
        showToast(`已删除用户 ${username}`, "success");
      }
      refreshAdminData();
    } catch (err) {
      showToast(err.message, "error");
    }
  };

  // Cluster & Endpoint controls
  if ($("topbar-cluster-pill")) {
    $("topbar-cluster-pill").onclick = () => {
      switchWorkspacePage("health");
    };
  }
  if ($("workspace-nav")) {
    $("workspace-nav").onclick = (e) => {
      const btn = e.target.closest("[data-workspace-page]");
      if (!btn) return;
      switchWorkspacePage(btn.dataset.workspacePage);
    };
  }
  if ($("btn-analytics-refresh")) {
    $("btn-analytics-refresh").onclick = async () => {
      await refreshAll();
      renderAnalyticsPage();
      showToast("数据统计已刷新", "success");
    };
  }
  if ($("btn-health-refresh")) {
    $("btn-health-refresh").onclick = async () => {
      await loadCluster();
      renderNodeHealthPage();
      showToast("已刷新节点健康遥测状态", "success");
    };
  }
  if ($("btn-health-open-cluster-settings")) {
    $("btn-health-open-cluster-settings").onclick = () => openSettingsModal("cluster");
  }
  if ($("btn-health-sync-versions")) {
    $("btn-health-sync-versions").onclick = () => syncClusterNodeVersions("all", "");
  }
  if ($("btn-sync-all-worker-versions")) {
    $("btn-sync-all-worker-versions").onclick = () => syncClusterNodeVersions("all", "");
  }
  if ($("btn-update-sync-workers")) {
    $("btn-update-sync-workers").onclick = () => syncClusterNodeVersions("all", "");
  }
  if ($("toggle-auto-sync-workers")) {
    $("toggle-auto-sync-workers").onchange = async (e) => {
      const enabled = Boolean(e.target.checked);
      try {
        const data = await api("/api/admin/cluster/upgrade-nodes", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ auto_sync_workers: enabled }),
        });
        if (data.pool) state.pool = data.pool;
        showToast(enabled ? "已开启：主版本更新时自动同步更新所有分节点" : "已关闭分节点自动版本同步", "success");
      } catch (err) {
        e.target.checked = !enabled;
        showToast(err.message, "error");
      }
    };
  }
  if ($("health-page-body")) {
    $("health-page-body").onclick = (e) => {
      const syncBtn = e.target.closest("[data-sync-node-version]");
      if (syncBtn) {
        syncClusterNodeVersions(syncBtn.dataset.syncNodeVersion, syncBtn.dataset.nodeName);
      }
    };
  }
  if ($("analytics-page-body")) {
    $("analytics-page-body").onclick = (e) => {
      const verBtn = e.target.closest("[data-open-version-id]");
      if (verBtn) {
        openFileVersionsModal(verBtn.dataset.openVersionId);
        return;
      }
      const metricBtn = e.target.closest("[data-chart-metric]");
      if (metricBtn) {
        state.chartMetric = metricBtn.dataset.chartMetric === "size" ? "size" : "count";
        renderAnalyticsPage();
        return;
      }
      const catBtn = e.target.closest("[data-chart-category]");
      if (catBtn) {
        const key = catBtn.dataset.chartCategory;
        state.selectedCategory = state.selectedCategory === key ? "all" : key;
        refreshAll();
        return;
      }
      const monthBtn = e.target.closest("[data-chart-month]");
      if (monthBtn) {
        const key = monthBtn.dataset.chartMonth;
        state.selectedMonth = state.selectedMonth === key ? "all" : key;
        refreshAll();
        return;
      }
      const nodeBtn = e.target.closest("[data-chart-node]");
      if (nodeBtn) {
        const nid = nodeBtn.dataset.chartNode;
        state.selectedNode = state.selectedNode === nid ? "all" : nid;
        refreshAll();
      }
    };
  }
  if ($("versions-close")) $("versions-close").onclick = closeFileVersionsModal;
  if ($("versions-backdrop")) $("versions-backdrop").onclick = closeFileVersionsModal;
  if ($("version-upload-form")) {
    $("version-upload-form").onsubmit = async (e) => {
      e.preventDefault();
      const fileId = state.versionModalFileId;
      const fileInput = $("version-file-input");
      const noteInput = $("version-note-input");
      const btn = $("btn-submit-version");
      if (!fileId || !fileInput || !fileInput.files || fileInput.files.length === 0) {
        showToast("请选择要上传的新版本文件", "error");
        return;
      }
      const fd = new FormData();
      fd.append("file", fileInput.files[0]);
      fd.append("note", (noteInput?.value || "").trim());
      if (state.device.fp) fd.append("device_fp", state.device.fp);
      if (state.device.name) fd.append("device_name", state.device.name);
      if (state.device.detail) fd.append("device_detail", state.device.detail);

      btn.disabled = true;
      try {
        await api(`/api/files/${encodeURIComponent(fileId)}/versions`, {
          method: "POST",
          body: fd,
        });
        showToast("新版本已上传并设为当前版本", "success");
        await Promise.all([openFileVersionsModal(fileId), refreshAll()]);
      } catch (err) {
        showToast(err.message, "error");
      } finally {
        btn.disabled = false;
      }
    };
  }
  if ($("versions-list-wrap")) {
    $("versions-list-wrap").onclick = async (e) => {
      const fileId = state.versionModalFileId;
      if (!fileId) return;
      const restBtn = e.target.closest("[data-restore-version]");
      if (restBtn) {
        const ver = Number(restBtn.dataset.restoreVersion);
        if (!confirm(`确定将版本 v${ver} 恢复为当前生效版本吗？（当前版本仍会保留在历史记录中）`)) return;
        restBtn.disabled = true;
        try {
          await api(`/api/files/${encodeURIComponent(fileId)}/versions/${ver}/restore`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ note: `从历史版本 v${ver} 恢复` }),
          });
          showToast(`已切换至版本 v${ver}`, "success");
          await Promise.all([openFileVersionsModal(fileId), refreshAll()]);
        } catch (err) {
          restBtn.disabled = false;
          showToast(err.message, "error");
        }
        return;
      }
      const delBtn = e.target.closest("[data-delete-version]");
      if (delBtn) {
        const ver = Number(delBtn.dataset.deleteVersion);
        if (!confirm(`确定删除版本 v${ver} 吗？`)) return;
        delBtn.disabled = true;
        try {
          await api(`/api/files/${encodeURIComponent(fileId)}/versions/${ver}`, {
            method: "DELETE",
          });
          showToast(`已删除版本 v${ver}`, "success");
          await Promise.all([openFileVersionsModal(fileId), refreshAll()]);
        } catch (err) {
          delBtn.disabled = false;
          showToast(err.message, "error");
        }
      }
    };
  }
  $("btn-toggle-secret").onclick = () => {
    state.secretVisible = !state.secretVisible;
    renderAdminCluster();
  };
  $("btn-copy-secret").onclick = () => {
    if (state.clusterSecret) copyText(state.clusterSecret);
  };
  if ($("btn-copy-join-cmd")) {
    $("btn-copy-join-cmd").onclick = () => {
      const entry = state.pool?.entry_url || state.serverInfo.entry_url || location.origin;
      const cmd = `.\\lfh.exe -role worker -secret "${state.clusterSecret}" -entry "${entry}" -quota 20GB -data .\\data`;
      copyText(cmd);
    };
  }
  if ($("btn-refresh-cluster")) {
    $("btn-refresh-cluster").onclick = async () => {
      await Promise.all([loadCluster(), loadPassiveDiscovered(), loadFiles(), loadSummary()]);
      renderAdminCluster();
      showToast("已刷新节点在线状态与文件列表", "success");
    };
  }
  if ($("btn-dissolve-cluster")) {
    $("btn-dissolve-cluster").onclick = async () => {
      if (!confirm("确定要解散当前集群吗？\n\n所有在线从节点将收到通知并恢复为独立主入口，本机集群密钥也将重置。各节点上的文件均会完整保留。")) return;
      const btn = $("btn-dissolve-cluster");
      btn.disabled = true;
      try {
        const data = await api("/api/admin/cluster/leave", { method: "POST" });
        state.pool = data.pool || state.pool;
        if (data.cluster_secret) state.clusterSecret = data.cluster_secret;
        renderAdminCluster();
        renderPoolStatus();
        renderNodeSummary();
        renderUploadTargetSelect();
        loadPassiveDiscovered();
        loadFiles();
        loadSummary();
        showToast(`已解散集群（已通知 ${Number(data.detached || 0)} 个在线从节点退出）`, "success");
      } catch (err) {
        showToast(err.message, "error");
      } finally {
        btn.disabled = false;
      }
    };
  }
  if ($("btn-scan-lan")) {
    $("btn-scan-lan").onclick = async () => {
      const btn = $("btn-scan-lan");
      const rawPorts = ($("lan-scan-ports")?.value || "8080")
        .split(/[\s,，;]+/)
        .map((s) => Number(s.trim()))
        .filter((n) => Number.isInteger(n) && n >= 1 && n <= 65535);
      btn.disabled = true;
      btn.textContent = "扫描中...";
      if ($("discover-status-hint")) {
        $("discover-status-hint").textContent = "正在并发扫描同网段 LFH 实例，请稍候...";
      }
      try {
        const data = await api("/api/admin/cluster/discover", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ mode: "scan", ports: rawPorts }),
        });
        state.discoveredNodes = Array.isArray(data.nodes) ? data.nodes : [];
        renderDiscoveredNodes();
        if ($("discover-status-hint")) {
          $("discover-status-hint").textContent = `扫描完成：共发现 ${state.discoveredNodes.length} 个其他节点`;
        }
        showToast(`扫描完成，发现 ${state.discoveredNodes.length} 个其他节点`, "success");
      } catch (err) {
        showToast(err.message, "error");
      } finally {
        btn.disabled = false;
        btn.textContent = "主动扫描局域网";
      }
    };
  }
  if ($("btn-manual-probe")) {
    $("btn-manual-probe").onclick = async () => {
      const addr = ($("manual-probe-addr")?.value || "").trim();
      if (!addr) {
        showToast("请输入目标节点的 IP:端口，例如 192.168.3.88:8080", "error");
        return;
      }
      const btn = $("btn-manual-probe");
      btn.disabled = true;
      btn.textContent = "探测中...";
      try {
        const data = await api("/api/admin/cluster/discover", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ mode: "manual", address: addr }),
        });
        state.discoveredNodes = Array.isArray(data.nodes) ? data.nodes : [];
        renderDiscoveredNodes();
        showToast(`探测成功：${data.node?.node_name || addr}`, "success");
      } catch (err) {
        showToast(err.message, "error");
      } finally {
        btn.disabled = false;
        btn.textContent = "手动探测";
      }
    };
  }
  if ($("discovered-nodes-list")) {
    $("discovered-nodes-list").onclick = (e) => {
      const adoptBtn = e.target.closest("[data-adopt-url]");
      if (adoptBtn) {
        openAdoptModal(adoptBtn.dataset.adoptUrl, adoptBtn.dataset.adoptName);
        return;
      }
      const joinBtn = e.target.closest("[data-prefill-join]");
      if (joinBtn && $("join-entry-url")) {
        $("join-entry-url").value = joinBtn.dataset.prefillJoin;
        $("join-cluster-secret")?.focus();
        showToast("已填入对方主入口地址，请输入对方集群密钥后确认加入", "info");
      }
    };
  }
  if ($("adopt-close")) $("adopt-close").onclick = closeAdoptModal;
  if ($("adopt-backdrop")) $("adopt-backdrop").onclick = closeAdoptModal;
  if ($("adopt-auth-tab-admin")) $("adopt-auth-tab-admin").onclick = () => setAdoptAuthMode("admin");
  if ($("adopt-auth-tab-secret")) $("adopt-auth-tab-secret").onclick = () => setAdoptAuthMode("secret");
  if ($("adopt-form")) {
    $("adopt-form").onsubmit = async (e) => {
      e.preventDefault();
      if ($("adopt-error")) $("adopt-error").classList.add("hidden");
      const btn = $("adopt-submit-btn");
      btn.disabled = true;
      btn.textContent = "正在验证并接入...";
      const isAdminMode = state.adoptAuthMode === "admin";
      try {
        const data = await api("/api/admin/cluster/adopt-peer", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            address: state.adoptTargetURL,
            target_admin_user: isAdminMode ? ($("adopt-admin-user")?.value || "").trim() : "",
            target_admin_pass: isAdminMode ? ($("adopt-admin-pass")?.value || "") : "",
            target_auth_secret: isAdminMode ? "" : ($("adopt-target-secret")?.value || "").trim(),
            quota_text: ($("adopt-quota-text")?.value || "").trim(),
          }),
        });
        if (data.pool) state.pool = data.pool;
        closeAdoptModal();
        await Promise.all([loadCluster(), loadPassiveDiscovered(), loadFiles(), loadSummary()]);
        renderAdminCluster();
        showToast(`节点 ${data.node?.node_name || state.adoptTargetName} 已成功接入集群`, "success");
      } catch (err) {
        if ($("adopt-error")) {
          $("adopt-error").textContent = err.message;
          $("adopt-error").classList.remove("hidden");
        } else {
          showToast(err.message, "error");
        }
      } finally {
        btn.disabled = false;
        btn.textContent = "验证并纳管加入";
      }
    };
  }
  if ($("join-cluster-form")) {
    $("join-cluster-form").onsubmit = async (e) => {
      e.preventDefault();
      const entry_url = ($("join-entry-url")?.value || "").trim();
      const cluster_secret = ($("join-cluster-secret")?.value || "").trim();
      const quota_text = ($("join-quota-text")?.value || "").trim();
      if (!confirm(`确定要将本机切换为 Worker 节点并加入主入口 ${entry_url} 吗？\n加入后浏览器将自动跳转到主入口管理。`)) {
        return;
      }
      const btn = $("btn-join-cluster");
      btn.disabled = true;
      btn.textContent = "正在验证并加入...";
      try {
        const data = await api("/api/admin/cluster/join", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ entry_url, cluster_secret, quota_text }),
        });
        showToast("已成功加入主集群，正在跳转至唯一入口...", "success");
        setTimeout(() => {
          location.href = data.entry_url || entry_url;
        }, 900);
      } catch (err) {
        showToast(err.message, "error");
      } finally {
        btn.disabled = false;
        btn.textContent = "验证并加入集群";
      }
    };
  }

  $("admin-expose-ip-select").onchange = (e) => {
    const isCustom = e.target.value === "__custom__";
    $("admin-expose-ip-custom-wrap").classList.toggle("hidden", !isCustom);
    if (isCustom) {
      $("admin-expose-ip-custom").focus();
    } else if (e.target.value) {
      const cb = document.querySelector(`[data-nic-toggle-ip="${CSS.escape(e.target.value)}"]`);
      if (cb) cb.checked = true;
    }
  };

  if ($("admin-nic-cards-list")) {
    $("admin-nic-cards-list").onchange = (e) => {
      const cb = e.target.closest("[data-nic-toggle-ip]");
      if (cb) {
        saveAdminEndpointSettings();
      }
    };
    $("admin-nic-cards-list").onclick = (e) => {
      const primBtn = e.target.closest("[data-nic-set-primary]");
      if (primBtn) {
        const ip = primBtn.dataset.nicSetPrimary;
        if (ip && $("admin-expose-ip-select")) {
          $("admin-expose-ip-select").value = ip;
          const cb = document.querySelector(`[data-nic-toggle-ip="${CSS.escape(ip)}"]`);
          if (cb) cb.checked = true;
          saveAdminEndpointSettings({ expose_ip: ip });
        }
      }
    };
  }

  if ($("btn-nic-enable-lan")) {
    $("btn-nic-enable-lan").onclick = () => {
      const addrs = state.availableAddrs || [];
      const expose_ips = [];
      const disabled_ips = [];
      for (const a of addrs) {
        if (a.tag !== "虚拟网卡") expose_ips.push(a.ip);
        else disabled_ips.push(a.ip);
      }
      if (expose_ips.length === 0 && addrs.length > 0) {
        expose_ips.push(addrs[0].ip);
      }
      saveAdminEndpointSettings({ expose_ips, disabled_ips });
    };
  }

  if ($("btn-nic-enable-all")) {
    $("btn-nic-enable-all").onclick = () => {
      const addrs = state.availableAddrs || [];
      const expose_ips = addrs.map((a) => a.ip);
      saveAdminEndpointSettings({ expose_ips, disabled_ips: [] });
    };
  }

  $("btn-save-endpoint").onclick = () => saveAdminEndpointSettings();

  $("cluster-nodes-tbody").onchange = async (e) => {
    const clipToggle = e.target.closest("[data-clipboard-toggle]");
    if (clipToggle) {
      const nodeID = clipToggle.dataset.clipboardToggle;
      const enabled = Boolean(clipToggle.checked);
      clipToggle.disabled = true;
      try {
        const data = await api("/api/admin/cluster/clipboard", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ node_id: nodeID, enabled }),
        });
        state.pool = data.pool || state.pool;
        renderAdminCluster();
        renderPoolStatus();
        renderNodeSummary();
        renderNodeHealthPage();
        showToast(enabled ? "已为该节点开启超级剪贴板" : "已关闭该节点超级剪贴板", "success");
      } catch (err) {
        clipToggle.checked = !enabled;
        clipToggle.disabled = false;
        showToast(err.message || "切换超级剪贴板状态失败", "error");
      }
      return;
    }

    const driveSel = e.target.closest("[data-drive-select]");
    if (!driveSel || !driveSel.value) return;
    const nodeID = driveSel.dataset.driveSelect;
    const input = document.querySelector(`[data-storage-input="${CSS.escape(nodeID)}"]`);
    if (input) {
      input.value = driveSel.value;
    }
  };

  $("cluster-nodes-tbody").onclick = async (e) => {
    const syncVerBtn = e.target.closest("[data-sync-node-version]");
    if (syncVerBtn) {
      syncVerBtn.disabled = true;
      try {
        await syncClusterNodeVersions(syncVerBtn.dataset.syncNodeVersion, syncVerBtn.dataset.nodeName);
      } finally {
        syncVerBtn.disabled = false;
      }
      return;
    }

    const promoteBtn = e.target.closest("[data-promote-node]");
    if (promoteBtn) {
      const nodeID = promoteBtn.dataset.promoteNode;
      const nodeName = promoteBtn.dataset.nodeName || nodeID;
      if (!confirm(`确定将「${nodeName}」设为集群主节点吗？\n\n操作后各节点上的本地文件均会完整保留。`)) return;
      promoteBtn.disabled = true;
      try {
        const data = await api("/api/admin/cluster/promote", {
          method: "POST",
          body: JSON.stringify({ target_node_id: nodeID })
        });
        state.pool = data.pool || state.pool;
        renderAdminCluster();
        renderPoolStatus();
        renderNodeSummary();
        renderUploadTargetSelect();
        const nextURL = String(data.new_entry_url || "").trim();
        if (nextURL && !nextURL.startsWith(location.origin)) {
          showToast(`主节点已切换至「${nodeName}」，正在跳转...`, "success");
          setTimeout(() => { location.href = nextURL; }, 700);
        } else {
          showToast(`已将「${nodeName}」设为主节点`, "success");
        }
      } catch (err) {
        promoteBtn.disabled = false;
        showToast(err.message, "error");
      }
      return;
    }

    const rmBtn = e.target.closest("[data-remove-node]");
    if (rmBtn) {
      const nodeID = rmBtn.dataset.removeNode;
      const nodeName = rmBtn.dataset.nodeName || nodeID;
      const isOnline = rmBtn.dataset.nodeOnline === "1";
      const confirmMsg = isOnline
        ? `确定将在线节点「${nodeName}」移出集群吗？该节点将恢复为独立节点并保留其本地文件。`
        : `确定删除离线节点「${nodeName}」记录吗？`;
      if (!confirm(confirmMsg)) return;
      try {
        const data = await api(`/api/admin/cluster/nodes/${encodeURIComponent(nodeID)}`, { method: "DELETE" });
        state.pool = data.pool || state.pool;
        renderAdminCluster();
        renderPoolStatus();
        renderNodeSummary();
        renderUploadTargetSelect();
        loadPassiveDiscovered();
        loadFiles();
        loadSummary();
        showToast(isOnline ? `已将「${nodeName}」移出集群` : `已删除离线节点「${nodeName}」记录`, "success");
      } catch (err) {
        showToast(err.message, "error");
      }
      return;
    }

    const storageBtn = e.target.closest("[data-save-storage]");
    if (storageBtn && !storageBtn.disabled) {
      const nodeID = storageBtn.dataset.saveStorage;
      const input = document.querySelector(`[data-storage-input="${CSS.escape(nodeID)}"]`);
      const migCb = document.querySelector(`[data-storage-migrate="${CSS.escape(nodeID)}"]`);
      const storageDir = (input ? input.value : "").trim();
      const migrateExisting = Boolean(migCb && migCb.checked);
      if (!storageDir) {
        showToast("请输入或选择有效的完整存储路径", "error");
        return;
      }
      if (migrateExisting && !confirm(`确定要将节点存储路径切换至 ${storageDir} 并迁移该节点已有文件吗？`)) {
        return;
      }
      storageBtn.disabled = true;
      const prevText = storageBtn.textContent;
      storageBtn.textContent = migrateExisting ? "迁移中..." : "应用中...";
      try {
        const data = await api("/api/admin/cluster/storage-dir", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            node_id: nodeID,
            storage_dir: storageDir,
            migrate_existing: migrateExisting,
          }),
        });
        state.pool = data.pool || state.pool;
        renderAdminCluster();
        renderPoolStatus();
        renderNodeSummary();
        loadFiles();
        showToast(
          migrateExisting
            ? `已切换存储位置至 ${data.node?.storage_dir || storageDir}（共迁移 ${data.moved_count || 0} 个文件）`
            : `新上传文件将保存至 ${data.node?.storage_dir || storageDir}`,
          "success"
        );
      } catch (err) {
        showToast(err.message, "error");
      } finally {
        storageBtn.disabled = false;
        storageBtn.textContent = prevText;
      }
      return;
    }

    const saveBtn = e.target.closest("[data-save-quota]");
    if (!saveBtn || saveBtn.disabled) return;
    const nodeID = saveBtn.dataset.saveQuota;
    const input = document.querySelector(`[data-quota-input="${CSS.escape(nodeID)}"]`);
    const unitSel = document.querySelector(`[data-quota-unit="${CSS.escape(nodeID)}"]`);
    const val = Number(input ? input.value : 0);
    const unit = unitSel ? unitSel.value : "GB";
    if (!val || val <= 0) {
      showToast("请输入大于 0 的预留空间数值", "error");
      return;
    }
    const quotaText = `${val}${unit}`;
    try {
      const data = await api("/api/admin/cluster/quota", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ node_id: nodeID, quota_text: quotaText }),
      });
      state.pool = data.pool;
      renderAdminCluster();
      renderPoolStatus();
      renderNodeSummary();
      renderUploadTargetSelect();
      showToast(`已更新节点预留空间为 ${val} ${unit}`, "success");
    } catch (err) {
      showToast(err.message, "error");
    }
  };

  $("invite-create-form").onsubmit = async (e) => {
    e.preventDefault();
    try {
      const data = await api("/api/admin/invites", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          note: $("invite-note").value.trim(),
          code: $("invite-custom-code").value.trim(),
          max_uses: Number($("invite-max-uses").value || 0),
          expire_hours: Number($("invite-expire-hours").value || 0),
        }),
      });
      $("invite-note").value = "";
      $("invite-custom-code").value = "";
      showToast(`邀请码已生成：${data.invite.code}`, "success");
      refreshAdminData();
    } catch (err) {
      showToast(err.message, "error");
    }
  };

  $("invites-tbody").onclick = async (e) => {
    const delBtn = e.target.closest("[data-del-invite]");
    if (!delBtn) return;
    const code = delBtn.dataset.delInvite;
    if (!confirm(`确定删除邀请码 ${code} 吗？`)) return;
    try {
      await api(`/api/admin/invites/${encodeURIComponent(code)}`, { method: "DELETE" });
      showToast("邀请码已删除", "success");
      refreshAdminData();
    } catch (err) {
      showToast(err.message, "error");
    }
  };

  // Update center buttons
  if ($("btn-update-notice")) {
    $("btn-update-notice").onclick = () => openSettingsModal("update");
  }
  if ($("btn-check-update")) {
    $("btn-check-update").onclick = checkForUpdate;
  }
  if ($("btn-apply-update")) {
    $("btn-apply-update").onclick = applyUpdate;
  }
  if ($("btn-save-update-config")) {
    $("btn-save-update-config").onclick = saveUpdateConfig;
  }

  if ($("btn-add-mount")) {
    $("btn-add-mount").onclick = () => openLocalImportModal("mount");
  }
  if ($("btn-refresh-mounts")) {
    $("btn-refresh-mounts").onclick = loadAdminMounts;
  }
  if ($("cluster-mounts-list")) {
    $("cluster-mounts-list").onclick = async (e) => {
      const syncBtn = e.target.closest("[data-sync-mount]");
      if (syncBtn) {
        const mountID = syncBtn.dataset.syncMount;
        const nodeID = syncBtn.dataset.mountNode || "";
        const origText = syncBtn.textContent;
        syncBtn.disabled = true;
        syncBtn.textContent = "扫描中...";
        try {
          const data = await api(`/api/admin/mounts/${encodeURIComponent(nodeID)}/${encodeURIComponent(mountID)}/sync`, {
            method: "POST",
          });
          if (Array.isArray(data.mounts)) {
            state.mounts = data.mounts;
            renderAdminMounts();
          }
          if (data.pool) state.pool = data.pool;
          const r = data.result || {};
          showToast(`扫描完成：新增 ${Number(r.added || 0)}，更新 ${Number(r.updated || 0)}，移除 ${Number(r.removed || 0)}`, "success");
          await refreshAll();
        } catch (err) {
          showToast(err.message || "扫描挂载目录失败", "error");
        } finally {
          syncBtn.disabled = false;
          syncBtn.textContent = origText;
        }
        return;
      }

      const delBtn = e.target.closest("[data-del-mount]");
      if (delBtn) {
        const mountID = delBtn.dataset.delMount;
        const nodeID = delBtn.dataset.mountNode || "";
        const alias = delBtn.dataset.mountAlias || mountID;
        if (!confirm(`确定移除挂载目录「${alias}」吗？\n\n仅取消 LFH 共享索引，磁盘上的原始文件将 100% 完整保留。`)) return;
        delBtn.disabled = true;
        try {
          const data = await api(`/api/admin/mounts/${encodeURIComponent(nodeID)}/${encodeURIComponent(mountID)}`, {
            method: "DELETE",
          });
          if (Array.isArray(data.mounts)) {
            state.mounts = data.mounts;
            renderAdminMounts();
          }
          if (data.pool) state.pool = data.pool;
          showToast(`已移除挂载「${alias}」（清理 ${Number(data.removed || 0)} 条索引，原文件已保留）`, "success");
          await refreshAll();
        } catch (err) {
          delBtn.disabled = false;
          showToast(err.message || "移除挂载失败", "error");
        }
      }
    };
  }

  if ($("btn-run-scrub")) {
    $("btn-run-scrub").onclick = runIntegrityScrub;
  }
  if ($("btn-refresh-audit")) {
    $("btn-refresh-audit").onclick = loadAdminAudit;
  }
  if ($("transfer-fab")) {
    $("transfer-fab").onclick = () => setTransferDrawerOpen(!state.transferOpen);
  }
  if ($("transfer-drawer-close")) {
    $("transfer-drawer-close").onclick = () => setTransferDrawerOpen(false);
  }
  if ($("transfer-clear-done")) {
    $("transfer-clear-done").onclick = () => {
      state.transfers = state.transfers.filter((t) => t.status !== "done");
      renderTransferDrawer();
    };
  }
  document.querySelectorAll("[data-transfer-tab]").forEach((btn) => {
    btn.onclick = () => setTransferTab(btn.dataset.transferTab);
  });
  if ($("transfer-queue-list")) {
    $("transfer-queue-list").onclick = (e) => {
      const btn = e.target.closest("[data-transfer-act]");
      if (!btn) return;
      const act = btn.dataset.transferAct;
      const tid = btn.dataset.tid;
      if (act === "pause") {
        pauseTransferTask(tid);
      } else if (act === "resume") {
        resumeTransferTask(tid);
      } else if (act === "cancel") {
        cancelTransferTask(tid);
      } else if (act === "remove") {
        state.transfers = state.transfers.filter((t) => t.id !== tid);
        renderTransferDrawer();
      }
    };
  }
  window.addEventListener("beforeunload", (e) => {
    if (hasActiveTransfers()) {
      e.preventDefault();
      e.returnValue = "";
    }
  });

  // Stage 4: 分布式算力插件市场与任务调度事件绑定
  if ($("btn-refresh-plugins")) {
    $("btn-refresh-plugins").onclick = async () => {
      await Promise.all([loadAdminPlugins(), loadPluginJobs(), loadCluster()]);
      showToast("已刷新全集群插件算力状态", "success");
    };
  }
  if ($("plugin-install-node")) {
    $("plugin-install-node").onchange = () => renderAdminPlugins();
  }
  const handlePluginActionClick = (e) => {
    const btn = e.target.closest("[data-plugin-act]");
    if (!btn || btn.disabled) return;
    const act = btn.dataset.pluginAct;
    const nodeId = btn.dataset.node || "";
    const pluginId = btn.dataset.plugin || "";
    if (!nodeId || !pluginId) return;
    if (act === "install") {
      installPluginOnNode(nodeId, pluginId);
    } else if (act === "toggle") {
      togglePluginOnNode(nodeId, pluginId, btn.dataset.enabled === "true");
    } else if (act === "uninstall") {
      uninstallPluginOnNode(nodeId, pluginId);
    }
  };
  if ($("plugins-marketplace-list")) {
    $("plugins-marketplace-list").onclick = handlePluginActionClick;
  }
  if ($("plugins-capabilities-list")) {
    $("plugins-capabilities-list").onclick = handlePluginActionClick;
  }
  if ($("plugin-run-close")) {
    $("plugin-run-close").onclick = closePluginRunModal;
  }
  if ($("plugin-run-backdrop")) {
    $("plugin-run-backdrop").onclick = closePluginRunModal;
  }
  if ($("plugin-run-list")) {
    $("plugin-run-list").onclick = (e) => {
      const btn = e.target.closest("[data-run-cap]");
      if (!btn || btn.disabled || !state.activePluginRunFileId) return;
      executePluginJob(
        state.activePluginRunFileId,
        btn.dataset.runCap || "",
        btn.dataset.runPlugin || "",
        btn
      );
    };
  }

  // Password form inside Settings -> General
  $("pwd-form").onsubmit = async (e) => {
    e.preventDefault();
    $("pwd-msg").classList.add("hidden");
    try {
      await api("/api/password", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          old_password: $("old-pwd").value,
          new_password: $("new-pwd").value,
        }),
      });
      $("pwd-form").reset();
      showToast("密码修改成功", "success");
    } catch (err) {
      $("pwd-msg").textContent = err.message;
      $("pwd-msg").classList.remove("hidden");
    }
  };
}

init();
