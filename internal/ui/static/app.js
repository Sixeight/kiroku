// ── State ───────────────────────────────────────────

let nextCursor = "";
let loading = false;
let currentCWD = "";
let currentContentSearch = "";
let isIndexing = false;
let conversationReversed = false;
let prevView = "";

// ── Cost estimation ─────────────────────────────────

const MODEL_PRICING = {
  "claude-opus-4-6": [5, 25],
  "claude-opus-4-5": [5, 25],
  "claude-opus-4-1": [15, 75],
  "claude-opus-4": [15, 75],
  "claude-sonnet-4-6": [3, 15],
  "claude-sonnet-4-5": [3, 15],
  "claude-sonnet-4": [3, 15],
  "claude-haiku-4-5-20251001": [1, 5],
  "claude-haiku-4-5": [1, 5],
  haiku: [1, 5],
};

function calculateCost(model, input, output, cacheRead, cacheWrite) {
  const [inPrice, outPrice] =
    MODEL_PRICING[model] || MODEL_PRICING["claude-sonnet-4-6"];
  return (
    (input * inPrice +
      output * outPrice +
      cacheRead * inPrice * 0.1 +
      cacheWrite * inPrice * 1.25) /
    1_000_000
  );
}

function calculateCostForItem(item) {
  return calculateCost(
    item.model || item.name || item.primary_model || "",
    item.input_tokens || 0,
    item.output_tokens || 0,
    item.cache_read_tokens || 0,
    item.cache_write_tokens || 0,
  );
}

function calculateCostFromModels(models) {
  let total = 0;
  for (const m of models) {
    total += calculateCost(
      m.name || m.model,
      m.input_tokens || 0,
      m.output_tokens || 0,
      m.cache_read_tokens || 0,
      m.cache_write_tokens || 0,
    );
  }
  return total;
}

function formatCost(dollars) {
  if (dollars < 0.01) return "<$0.01";
  return "$" + dollars.toFixed(2);
}

// ── Hook formatting ─────────────────────────────────

function formatHookLabel(command) {
  if (!command || command === "callback") return "";
  // File path: show basename without .sh
  if (command.includes("/"))
    return command.split("/").pop().replace(/\.sh$/, "");
  // Inline script: truncate
  return command.length > 30 ? command.slice(0, 30) + "\u2026" : command;
}

function formatHookPill(event, command, count) {
  const label = formatHookLabel(command);
  const title = command && command !== "callback" ? command : event;
  const tag = label
    ? `<span class="hook-label">${escapeHTML(label)}</span>`
    : `<span class="hook-event">${escapeHTML(event)}</span>`;
  return `<div class="hook-pill" title="${escapeHTML(title)}">${tag}<span class="hook-count">${count}</span></div>`;
}

// ── Router ──────────────────────────────────────────

function navigate() {
  const hash = location.hash;
  const homeView = document.getElementById("home-view");
  const detailView = document.getElementById("detail-view");
  const dailyView = document.getElementById("daily-view");

  homeView.hidden = true;
  detailView.hidden = true;
  if (dailyView) dailyView.hidden = true;

  if (hash.startsWith("#/sessions/")) {
    const sessionId = decodeURIComponent(hash.slice("#/sessions/".length));
    detailView.hidden = false;
    loadSessionDetail(sessionId);
  } else if (hash.startsWith("#/daily/")) {
    prevView = "";
    const date = decodeURIComponent(hash.slice("#/daily/".length));
    if (dailyView) {
      dailyView.hidden = false;
      loadDailyStats(date);
    }
  } else {
    prevView = "";
    homeView.hidden = false;
  }
}

// ── Init ────────────────────────────────────────────

window.addEventListener("DOMContentLoaded", () => {
  initTheme();

  const reindexBtn = document.getElementById("reindex-button");
  if (reindexBtn) {
    reindexBtn.addEventListener("click", triggerReindex);
    document.addEventListener("keydown", (e) => {
      if (e.key === "Shift") reindexBtn.title = "Full Reindex";
    });
    document.addEventListener("keyup", (e) => {
      if (e.key === "Shift") reindexBtn.title = "Reindex";
    });
  }

  initSearchBar();

  document.body.addEventListener("click", (event) => {
    const trigger = event.target.closest(".session-item");
    if (!trigger) return;

    const sessionId = trigger.dataset.sessionId;
    if (sessionId) {
      location.hash = `#/sessions/${encodeURIComponent(sessionId)}`;
    }
  });

  const loadMore = document.getElementById("load-more");
  if (loadMore) {
    loadMore.addEventListener("click", () => fetchSessions(false));
  }

  navigate();
  refreshStats();
  fetchSessions(true);
  window.setInterval(refreshStats, 5000);
});

window.addEventListener("hashchange", (e) => {
  const oldHash = e.oldURL ? new URL(e.oldURL).hash : "";
  if (oldHash.startsWith("#/daily/")) {
    prevView = oldHash;
  }
  navigate();
});

// ── Session controls ────────────────────────────────

let searchDebounce = null;
let composing = false;
let currentDir = "desc";

function initSearchBar() {
  const toggle = document.getElementById("session-search-toggle");
  const bar = document.getElementById("search-bar");
  const input = document.getElementById("search-input");
  const sortBtn = document.getElementById("session-sort-toggle");

  if (toggle && bar && input) {
    toggle.addEventListener("click", () => {
      const open = bar.classList.toggle("is-open");
      toggle.classList.toggle("is-active", open);
      if (open) {
        input.focus();
      } else {
        input.value = "";
        if (currentContentSearch) {
          currentContentSearch = "";
          fetchSessions(true);
        }
      }
    });

    const clearBtn = bar.querySelector(".search-clear");
    input.addEventListener("compositionstart", () => {
      composing = true;
    });
    input.addEventListener("compositionend", () => {
      composing = false;
      triggerSearch(input, clearBtn);
    });
    input.addEventListener("input", () => {
      if (composing) return;
      triggerSearch(input, clearBtn);
    });

    input.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        input.value = "";
        currentContentSearch = "";
        bar.classList.remove("is-open");
        toggle.classList.remove("is-active");
        fetchSessions(true);
      }
    });
  }

  if (sortBtn) {
    sortBtn.addEventListener("click", () => {
      currentDir = currentDir === "desc" ? "asc" : "desc";
      sortBtn.textContent =
        currentDir === "desc" ? "\u2193 Newest" : "\u2191 Oldest";
      fetchSessions(true);
    });
  }
}

function triggerSearch(input, clearBtn) {
  if (clearBtn) clearBtn.hidden = !input.value;
  clearTimeout(searchDebounce);
  searchDebounce = setTimeout(() => {
    currentContentSearch = input.value.trim();
    fetchSessions(true);
  }, 300);
}

// ── Stats refresh ───────────────────────────────────

async function refreshStats() {
  const response = await fetch("/api/summary");
  if (!response.ok) return;

  const summary = await response.json();

  setText("total-sessions", String(summary.totals.total_sessions));
  setText("total-messages", String(summary.totals.total_messages));
  setText(
    "output-tokens",
    formatNumber(summary.analytics?.total_output_tokens || 0),
  );

  isIndexing = !!summary.indexing;
  const reindexBtn = document.getElementById("reindex-button");
  if (reindexBtn) {
    reindexBtn.disabled = isIndexing;
  }

  const statsBar = document.querySelector(".stats-bar");
  if (statsBar) {
    statsBar.classList.toggle("is-indexing", isIndexing);
  }

  const topModels = summary.analytics?.top_models || [];
  setText("total-cost", formatCost(calculateCostFromModels(topModels)));

  renderTimeline(summary.daily_activity || []);
  renderProjectFilter(summary.top_projects || []);
}

function setText(id, value) {
  const el = document.getElementById(id);
  if (el) el.textContent = value;
}

async function triggerReindex(e) {
  const btn = document.getElementById("reindex-button");
  if (btn?.disabled) return;

  const full = e && e.shiftKey;
  const url = full ? "/api/reindex?full=true" : "/api/reindex";

  if (btn) {
    btn.disabled = true;
  }
  const statsBar = document.querySelector(".stats-bar");
  if (statsBar) {
    statsBar.classList.add("is-indexing");
  }

  try {
    const response = await fetch(url, { method: "POST" });
    if (!response.ok) return;
    await refreshStats();
    await fetchSessions(true);
  } finally {
    if (btn) {
      btn.disabled = false;
    }
  }
}

// ── Usage timeline ──────────────────────────────────

function renderTimeline(dailyActivity) {
  const container = document.getElementById("usage-timeline");
  if (!container) return;

  const days = fillDays(dailyActivity, 30);
  if (!days.length) return;

  const maxCount = Math.max(...days.map((d) => d.sessionCount), 1);
  const svgH = 40;
  const barW = 100 / days.length;

  const bars = days
    .map((d, i) => {
      const h = Math.max(
        (d.sessionCount / maxCount) * svgH,
        d.sessionCount > 0 ? 2 : 0,
      );
      return (
        `<rect x="${i * barW}" y="0" width="${barW - 0.4}" height="${svgH}" fill="transparent" data-idx="${i}"/>` +
        `<rect x="${i * barW}" y="${svgH - h}" width="${barW - 0.4}" height="${h}"
                fill="var(--accent)" opacity="0.6" rx="1" pointer-events="none"/>`
      );
    })
    .join("");

  const startDate = formatShortDate(days[0].date);
  const endDate = formatShortDate(days[days.length - 1].date);

  container.innerHTML = `<svg viewBox="0 0 100 ${svgH}" preserveAspectRatio="none"
    style="width:100%;height:48px;display:block">${bars}</svg>
    <div class="timeline-tooltip" hidden></div>
    <div class="timeline-dates">
      <span>${startDate}</span><span>${endDate}</span>
    </div>`;
  container.hidden = false;

  const svg = container.querySelector("svg");
  const tooltip = container.querySelector(".timeline-tooltip");
  svg.addEventListener("mousemove", (e) => {
    const hit = e.target.closest("[data-idx]");
    if (!hit) {
      tooltip.hidden = true;
      return;
    }
    const d = days[hit.dataset.idx];
    const [y, m, day] = d.date.split("-");
    tooltip.innerHTML = `<strong>${parseInt(m)}/${parseInt(day)}</strong> <span>${d.sessionCount} sessions \u00b7 ${formatNumber(d.messageCount)} msgs</span>`;
    const svgRect = svg.getBoundingClientRect();
    const x = e.clientX - svgRect.left;
    tooltip.hidden = false;
    tooltip.style.left = Math.min(Math.max(x, 70), svgRect.width - 70) + "px";
  });
  svg.addEventListener("mouseleave", () => {
    tooltip.hidden = true;
  });
  svg.addEventListener("click", (e) => {
    const hit = e.target.closest("[data-idx]");
    if (!hit) return;
    const d = days[hit.dataset.idx];
    if (d.sessionCount > 0) {
      location.hash = `#/daily/${d.date}`;
    }
  });
}

function formatShortDate(dateStr) {
  const [, m, d] = dateStr.split("-");
  return `${parseInt(m)}/${parseInt(d)}`;
}

function fillDays(dailyActivity, count) {
  const byDate = {};
  for (const d of dailyActivity) {
    byDate[d.date] = d;
  }

  const result = [];
  const now = new Date();
  for (let i = count - 1; i >= 0; i--) {
    const date = new Date(now);
    date.setDate(date.getDate() - i);
    const key = `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
    const entry = byDate[key];
    result.push({
      date: key,
      sessionCount: entry?.sessionCount || 0,
      messageCount: entry?.messageCount || 0,
    });
  }
  return result;
}

// ── Project filter ──────────────────────────────────

let allProjects = [];
let filterOpen = false;
let focusIndex = -1;

function renderProjectFilter(projects) {
  const root = document.getElementById("project-filter");
  if (!root) return;

  allProjects = projects;
  if (!projects.length) {
    root.hidden = true;
    return;
  }

  root.hidden = false;
  if (root.querySelector(".filter-box")) return;

  root.innerHTML = `
    <div class="filter-box">
      <input class="filter-input" type="text" placeholder="Filter by project..." />
      <button class="filter-clear" type="button" hidden>&times;</button>
      <div class="filter-dropdown" hidden></div>
    </div>
  `;

  const input = root.querySelector(".filter-input");
  const dropdown = root.querySelector(".filter-dropdown");
  const clearBtn = root.querySelector(".filter-clear");

  if (currentCWD) {
    input.value = currentCWD.split("/").pop();
    if (clearBtn) clearBtn.hidden = false;
  }

  if (clearBtn) {
    clearBtn.addEventListener("click", () => {
      selectProject("", input, dropdown);
      clearBtn.hidden = true;
    });
  }

  input.addEventListener("focus", () => {
    filterOpen = true;
    focusIndex = -1;
    renderDropdown(dropdown, input.value);
    dropdown.hidden = false;
  });

  input.addEventListener("input", () => {
    focusIndex = -1;
    renderDropdown(dropdown, input.value);
  });

  input.addEventListener("keydown", (e) => {
    const options = dropdown.querySelectorAll(".filter-option");
    if (e.key === "ArrowDown") {
      e.preventDefault();
      focusIndex = Math.min(focusIndex + 1, options.length - 1);
      updateFocus(options);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      focusIndex = Math.max(focusIndex - 1, 0);
      updateFocus(options);
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (focusIndex >= 0 && options[focusIndex]) {
        selectProject(options[focusIndex].dataset.cwd, input, dropdown);
      }
    } else if (e.key === "Escape") {
      input.blur();
    }
  });

  dropdown.addEventListener("click", (e) => {
    const opt = e.target.closest(".filter-option");
    if (opt) selectProject(opt.dataset.cwd, input, dropdown);
  });

  document.addEventListener("click", (e) => {
    if (!root.contains(e.target)) {
      dropdown.hidden = true;
      filterOpen = false;
    }
  });
}

function renderDropdown(dropdown, query) {
  const q = query.toLowerCase();
  const shortName = (cwd) => cwd.split("/").pop();

  const filtered = allProjects.filter(
    (p) =>
      !q ||
      shortName(p.name).toLowerCase().includes(q) ||
      p.name.toLowerCase().includes(q),
  );

  dropdown.innerHTML =
    `<button class="filter-option${currentCWD === "" ? " is-active" : ""}" data-cwd="" type="button">
      <span>All projects</span>
    </button>` +
    filtered
      .map(
        (p) =>
          `<button class="filter-option${currentCWD === p.name ? " is-active" : ""}" data-cwd="${escapeHTML(p.name)}" type="button">
            <span>${escapeHTML(shortName(p.name))}</span>
            <span class="filter-option-count">${p.session_count}</span>
          </button>`,
      )
      .join("");
}

function updateFocus(options) {
  options.forEach((el, i) => {
    el.classList.toggle("is-focused", i === focusIndex);
  });
  if (options[focusIndex]) {
    options[focusIndex].scrollIntoView({ block: "nearest" });
  }
}

function selectProject(cwd, input, dropdown) {
  currentCWD = cwd;
  input.value = cwd ? cwd.split("/").pop() : "";
  dropdown.hidden = true;
  filterOpen = false;
  const clearBtn = input.parentElement.querySelector(".filter-clear");
  if (clearBtn) clearBtn.hidden = !cwd;
  fetchSessions(true);

  if (cwd) {
    fetchProjectSummary(cwd);
  } else {
    const el = document.getElementById("project-summary");
    if (el) el.hidden = true;
  }
}

// ── Project summary ─────────────────────────────────

async function fetchProjectSummary(cwd) {
  const res = await fetch(
    `/api/projects/summary?cwd=${encodeURIComponent(cwd)}`,
  );
  if (!res.ok) return;
  const stats = await res.json();
  renderProjectSummary(stats);
}

function renderProjectSummary(stats) {
  const root = document.getElementById("project-summary");
  if (!root) return;

  const cost = calculateCostFromModels(stats.top_models || []);
  const toolBadges = (stats.top_tools || [])
    .slice(0, 6)
    .map((t) => `<span class="badge">${escapeHTML(t.name)} ${t.count}</span>`)
    .join("");
  const branches = (stats.branches || [])
    .slice(0, 5)
    .map((b) => escapeHTML(b.name))
    .join(", ");

  root.innerHTML = `
    <div class="project-stats">
      <div class="detail-stat"><span>Sessions</span><strong>${formatNumber(stats.total_sessions)}</strong></div>
      <div class="detail-stat"><span>Messages</span><strong>${formatNumber(stats.total_messages)}</strong></div>
      <div class="detail-stat"><span>Tools</span><strong>${formatNumber(stats.total_tool_calls)}</strong></div>
      <div class="detail-stat"><span>Est. Cost</span><strong>${formatCost(cost)}</strong></div>
    </div>
    ${toolBadges ? `<div class="badge-row">${toolBadges}</div>` : ""}
    <div class="project-path">${escapeHTML(stats.cwd || "")}</div>
  `;
  root.hidden = false;
}

// ── Session list ────────────────────────────────────

async function fetchSessions(replace) {
  if (loading) return;
  loading = true;

  const loadMore = document.getElementById("load-more");
  if (loadMore) {
    if (replace) {
      loadMore.hidden = true;
    } else {
      loadMore.textContent = "Loading...";
    }
  }

  const cursor = replace ? "" : nextCursor;
  const params = new URLSearchParams({ limit: "50" });
  if (cursor) params.set("cursor", cursor);
  if (currentCWD) params.set("cwd", currentCWD);
  if (currentContentSearch) params.set("content", currentContentSearch);
  if (currentDir === "asc") params.set("dir", "asc");

  const response = await fetch(`/api/sessions?${params}`);
  loading = false;
  if (!response.ok) {
    nextCursor = "";
    if (loadMore) loadMore.hidden = true;
    return;
  }

  const data = await response.json();
  nextCursor = data.next_cursor || "";
  const items = data.items || [];

  renderSessions(items, replace);

  if (loadMore) {
    loadMore.textContent = "Load more";
    loadMore.hidden = !nextCursor || items.length < 50;
  }
}

function renderSessions(items, replace) {
  const root = document.getElementById("recent-sessions");
  if (!root) return;

  if (replace) {
    if (!items.length) {
      root.innerHTML = isIndexing
        ? '<p class="empty indexing-text">Indexing sessions...</p>'
        : '<p class="empty">No sessions found.</p>';
      return;
    }
    root.innerHTML = items.map(sessionItemHTML).join("");
  } else {
    root.insertAdjacentHTML("beforeend", items.map(sessionItemHTML).join(""));
  }
}

function sessionItemHTML(item) {
  const project = item.cwd ? item.cwd.split("/").pop() : "-";
  const preview = cleanUserText(item.preview || "");
  const ago = item.started_at ? timeAgo(item.started_at) : "";
  const cost = calculateCostForItem(item);

  return `
    <div class="session-row">
      <button class="session-item" data-session-id="${escapeHTML(item.session_id)}" type="button">
        <div class="session-main">
          <div class="session-top">
            <strong>${escapeHTML(project)}</strong>
            ${(item.prs || []).map((pr) => `<a class="badge badge-pr" href="${escapeHTML(pr.pr_url)}" target="_blank" rel="noopener" onclick="event.stopPropagation()">#${pr.pr_number}</a>`).join("")}
            <span class="session-meta-inline">${escapeHTML(item.git_branch || "-")}${ago ? " \u00b7 " + escapeHTML(ago) : ""}</span>
          </div>
          <p class="session-preview">${escapeHTML(preview)}</p>
        </div>
        <div class="session-meta">
          <span>${formatCost(cost)}</span>
          <span>${item.message_count} msgs</span>
        </div>
      </button>
      <button class="session-resume action-icon has-tooltip" type="button" data-tooltip="Copy resume" onclick="copyResumeCommand('${escapeHTML(item.session_id)}', '${escapeHTML(item.cwd || "")}', this)">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
      </button>
    </div>
  `;
}

function timeAgo(isoString) {
  const now = Date.now();
  const then = new Date(isoString).getTime();
  const diff = Math.max(0, now - then);

  const minutes = Math.floor(diff / 60000);
  if (minutes < 1) return "now";
  if (minutes < 60) return `${minutes}m ago`;

  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;

  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;

  const months = Math.floor(days / 30);
  return `${months}mo ago`;
}

// ── Session detail ──────────────────────────────────

let currentDetailSessionId = "";
let conversationOffset = 0;

async function loadSessionDetail(sessionId) {
  if (!sessionId) return;

  currentDetailSessionId = sessionId;
  conversationOffset = 0;
  conversationReversed = false;

  const root = document.getElementById("detail-view");
  if (root) root.innerHTML = '<p class="empty">Loading...</p>';

  const [detailRes, messagesRes] = await Promise.all([
    fetch(`/api/sessions/${encodeURIComponent(sessionId)}`),
    fetch(`/api/sessions/${encodeURIComponent(sessionId)}/messages?limit=100`),
  ]);

  if (!detailRes.ok) return;

  const detail = await detailRes.json();
  let hasMore = false;
  let messages = [];
  if (messagesRes.ok) {
    const data = await messagesRes.json();
    messages = data.messages || [];
    hasMore = data.has_more || false;
  }
  conversationOffset = messages.length;
  renderSessionDetail(detail, messages, hasMore);
}

async function loadMoreMessages() {
  const container = document.querySelector(".conversation");
  const btn = document.getElementById("load-more-messages");
  if (!container || !currentDetailSessionId) return;

  if (btn) btn.textContent = "Loading...";

  const res = await fetch(
    `/api/sessions/${encodeURIComponent(currentDetailSessionId)}/messages?limit=100&offset=${conversationOffset}`,
  );
  if (!res.ok) return;

  const data = await res.json();
  const messages = data.messages || [];
  conversationOffset += messages.length;

  container.insertAdjacentHTML("beforeend", renderConversation(messages));

  if (btn) {
    if (data.has_more) {
      btn.textContent = "Load more";
    } else {
      btn.remove();
    }
  }
}

function renderSessionDetail(detail, messages, hasMore) {
  const root = document.getElementById("detail-view");
  if (!root) return;

  const meta = detail.meta || {};
  const timeline = detail.timeline_counts || {};
  const tools = detail.tools || [];
  const hooks = detail.hooks || [];
  const models = detail.models || [];
  const totalCost = calculateCostFromModels(models);

  const backHref = prevView || "#/";
  const backLabel = prevView ? "\u2190 Daily" : "\u2190 Sessions";
  root.innerHTML = `
    <div class="detail-header">
      <a href="${backHref}" class="back">${backLabel}</a>
      <button class="action-icon resume-icon has-tooltip" type="button" data-tooltip="Copy resume" onclick="copyResumeCommand('${escapeHTML(meta.session_id || "")}', '${escapeHTML(meta.cwd || "")}', this)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
      </button>
    </div>
    <h2 class="detail-title">${escapeHTML(meta.session_id || "-")}</h2>
    <p class="detail-preview">${escapeHTML(cleanUserText(meta.preview || ""))}</p>

    <div class="detail-stats">
      <div class="detail-stat"><span>Duration</span><strong>${formatNumber(meta.duration_seconds || 0)}s</strong></div>
      <div class="detail-stat"><span>Messages</span><strong>${formatNumber(meta.message_count || 0)}</strong></div>
      <div class="detail-stat"><span>Tool Calls</span><strong>${formatNumber(meta.tool_call_count || 0)}</strong></div>
      <div class="detail-stat"><span>Est. Cost</span><strong>${formatCost(totalCost)}</strong></div>
    </div>

    <div class="detail-section">
      <h3>Info</h3>
      <div class="detail-rows">
        <div class="detail-row"><span>Project</span><span>${escapeHTML(meta.cwd || "-")}</span></div>
        <div class="detail-row"><span>Branch</span><span>${escapeHTML(meta.git_branch || "-")}</span></div>
        ${(detail.prs || []).map((pr) => `<div class="detail-row"><span>PR</span><span><a href="${escapeHTML(pr.pr_url)}" target="_blank" rel="noopener">${escapeHTML(pr.pr_repository)}#${pr.pr_number}</a></span></div>`).join("")}
        <div class="detail-row"><span>Started</span><span>${formatLocalTime(meta.started_at)}</span></div>
        <div class="detail-row"><span>Ended</span><span>${formatLocalTime(meta.ended_at)}</span></div>
        <div class="detail-row"><span>Source</span><span>${escapeHTML(meta.source_path || "-")}</span></div>
      </div>
    </div>

    <div class="detail-grid">
      <div class="detail-section">
        <h3>Tools</h3>
        <div class="detail-rows">
          ${renderMetricRows(tools, "tool_name", "count")}
        </div>
      </div>
      <div class="detail-section">
        <h3>Models</h3>
        <div class="detail-rows">
          ${renderModelRows(models)}
        </div>
      </div>
    </div>

    ${hooks.length ? `<div class="detail-section hooks-section"><h3>Hooks</h3><div class="hooks-bar">${hooks.map((h) => formatHookPill(h.hook_event, h.command, h.count)).join("")}</div></div>` : ""}

    ${renderSessionMeta(messages)}

    ${renderCostlyMessages(messages)}

    ${messages.length ? `<div class="detail-section detail-conversation"><h3>Conversation <button class="search-toggle" type="button" onclick="toggleConversationOrder(this)" title="Reverse order">${conversationReversed ? "\u2191" : "\u2193"} ${conversationReversed ? "Oldest" : "Newest"}</button><button class="search-toggle" type="button" onclick="toggleConversationSearch(this)"><svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><circle cx="10" cy="10" r="7"/><line x1="15" y1="15" x2="21" y2="21"/></svg> Search</button></h3><div id="conversation-search" class="conversation-search"><input class="search-input" type="text" placeholder="Filter messages..." oninput="filterConversation(this.value);this.nextElementSibling.hidden=!this.value" onkeydown="if(event.key==='Escape'){this.value='';filterConversation('');this.nextElementSibling.hidden=true;toggleConversationSearch(this.closest('.detail-section').querySelector('.search-toggle'))}" /><button class="search-clear" type="button" hidden onclick="var i=this.previousElementSibling;i.value='';filterConversation('');this.hidden=true">&times;</button></div><div class="conversation">${renderConversation(messages)}</div>${hasMore ? `<button id="load-more-messages" class="load-more" type="button" onclick="loadMoreMessages()">Load more</button>` : ""}</div>` : ""}
  `;
}

function renderMetricRows(items, nameKey, valueKey) {
  if (!items.length) return '<p class="empty">No data.</p>';

  return items
    .map(
      (item) => `
        <div class="detail-row">
          <span>${escapeHTML(item[nameKey])}</span>
          <strong>${formatNumber(item[valueKey] || 0)}</strong>
        </div>
      `,
    )
    .join("");
}

function renderModelRows(items) {
  if (!items.length) return '<p class="empty">No data.</p>';

  return items
    .map((item) => {
      const cost = calculateCostForItem(item);
      return `
        <div class="detail-row">
          <span>${escapeHTML(item.model)}</span>
          <strong>${formatCost(cost)}</strong>
        </div>
      `;
    })
    .join("");
}

// ── Conversation view ───────────────────────────────

function renderSessionMeta(messages) {
  const skills = new Set();

  for (const msg of messages) {
    if (msg.role === "user") {
      for (const b of msg.blocks || []) {
        if (b.type === "text" && b.text) {
          const m = b.text.match(/<command-name>([^<]+)<\/command-name>/);
          if (m) skills.add(m[1]);
        }
      }
    }
    if (msg.role === "system") {
      for (const b of msg.blocks || []) {
        if (b.type === "skill" && b.text) {
          skills.add(b.text);
        }
      }
    }
  }

  if (!skills.size) return "";

  let html =
    '<div class="detail-section"><h3>Skills</h3><div class="badge-row">';
  for (const s of skills) {
    html += `<span class="badge badge-skill">${escapeHTML(s)}</span>`;
  }
  html += "</div></div>";
  return html;
}

function renderCostlyMessages(messages) {
  const costed = messages
    .filter((m) => m.role === "assistant" && m.output_tokens > 0)
    .map((m) => ({
      msg: m,
      cost: calculateCostForItem(m),
    }))
    .sort((a, b) => b.cost - a.cost)
    .slice(0, 3);

  if (!costed.length || costed[0].cost < 0.01) return "";

  const items = costed
    .map((c) => {
      const usage = `<span class="msg-usage">${formatNumber(c.msg.output_tokens)} out · ${formatCost(c.cost)}</span>`;
      const header = `<div class="msg-header"><span class="msg-role msg-role-assistant">Claude</span>${usage}</div>`;

      const content = (c.msg.blocks || [])
        .map((b) => {
          switch (b.type) {
            case "text":
              return b.text
                ? `<div class="msg-text">${renderMarkdown(b.text)}</div>`
                : "";
            case "tool_use":
              return renderToolUse(b.tool, b.input);
            case "thinking":
              return b.text
                ? `<details class="msg-thinking"><summary>Thinking...</summary><p>${escapeHTML(b.text)}</p></details>`
                : "";
            default:
              return "";
          }
        })
        .filter(Boolean)
        .join("");

      if (!content) return "";
      return `<div class="costly-item">${header}${content}</div>`;
    })
    .filter(Boolean)
    .join("");

  return `<div class="detail-section"><h3>Most Expensive</h3><div class="costly-list">${items}</div></div>`;
}

function renderConversation(messages) {
  return messages
    .map((msg) => {
      if (msg.role === "system") {
        const parts = [];
        for (const b of msg.blocks || []) {
          if (b.type === "hook" && b.text) {
            const badges = b.text
              .split("\n")
              .filter(Boolean)
              .map(
                (c) =>
                  `<span class="badge badge-hook">${escapeHTML(c.length > 60 ? c.slice(0, 60) + "..." : c)}</span>`,
              )
              .join("");
            parts.push(
              `<span class="msg-role msg-role-system">Hook</span><div class="badge-row">${badges}</div>`,
            );
          }
          if (b.type === "skill" && b.text) {
            parts.push(
              `<span class="msg-role msg-role-system">Skill</span> <span class="badge badge-skill">${escapeHTML(b.text)}</span>`,
            );
          }
        }
        if (!parts.length) return "";
        return `<div class="msg msg-system">${parts.join("")}</div>`;
      }

      const isUser = msg.role === "user";
      const blocks = (msg.blocks || [])
        .map((b) => {
          switch (b.type) {
            case "text": {
              const raw = b.text || "";
              if (isUser) {
                const text = cleanUserText(raw);
                if (!text) return "";
                const slashMatch = text.match(/^(\/\S+)(.*)/s);
                if (slashMatch) {
                  const cmd = slashMatch[1];
                  const rest = slashMatch[2];
                  return `<p class="msg-text"><span class="msg-slash-cmd">${escapeHTML(cmd)}</span>${rest ? escapeHTML(rest) : ""}</p>`;
                }
                return `<p class="msg-text">${escapeHTML(text)}</p>`;
              }
              return raw
                ? `<div class="msg-text">${renderMarkdown(raw)}</div>`
                : "";
            }
            case "tool_use":
              return renderToolUse(b.tool, b.input);
            case "tool_result": {
              const content = b.content || "";
              if (!content) return "";
              const lines = content.split("\n");
              const short = lines.slice(0, 3).join("\n");
              if (lines.length > 3) {
                return `<details class="msg-result"><summary>${escapeHTML(short)}...</summary><pre>${escapeHTML(content)}</pre></details>`;
              }
              return `<pre class="msg-result-short">${escapeHTML(content)}</pre>`;
            }
            case "thinking":
              return b.text
                ? `<details class="msg-thinking"><summary>Thinking...</summary><p>${escapeHTML(b.text)}</p></details>`
                : "";
            case "image":
              return b.image_data && b.media_type
                ? `<img class="msg-image" src="data:${escapeHTML(b.media_type)};base64,${b.image_data}" alt="image" />`
                : "";
            default:
              return "";
          }
        })
        .filter(Boolean)
        .join("");

      if (!blocks) return "";

      const hasUserText =
        isUser &&
        (msg.blocks || []).some(
          (b) => b.type === "text" && cleanUserText(b.text || ""),
        );
      const isToolOutput = isUser && !hasUserText;

      if (isToolOutput) {
        return `<div class="msg msg-assistant">${blocks}</div>`;
      }

      let header;
      if (isUser) {
        header =
          '<div class="msg-header"><span class="msg-role msg-role-user">You</span></div>';
      } else {
        const cost = msg.output_tokens ? calculateCostForItem(msg) : 0;
        const usage = msg.output_tokens
          ? `<span class="msg-usage">${formatNumber(msg.output_tokens)} out · ${formatCost(cost)}</span>`
          : "";
        header = `<div class="msg-header"><span class="msg-role msg-role-assistant">Claude</span>${usage}</div>`;
      }
      const cls = isUser ? "msg msg-user" : "msg msg-assistant";
      return `<div class="${cls}">${header}${blocks}</div>`;
    })
    .filter(Boolean)
    .join("");
}

// ── Conversation order ──────────────────────────────

function toggleConversationOrder(btn) {
  const container = document.querySelector(".conversation");
  if (!container) return;

  conversationReversed = !conversationReversed;
  const children = Array.from(container.children);
  children.reverse();
  container.innerHTML = "";
  for (const child of children) container.appendChild(child);

  btn.textContent = conversationReversed ? "\u2191 Oldest" : "\u2193 Newest";
}

// ── Conversation search ─────────────────────────────

function toggleConversationSearch(btn) {
  const bar = document.getElementById("conversation-search");
  if (!bar) return;

  const open = bar.classList.toggle("is-open");
  btn.classList.toggle("is-active", open);

  if (open) {
    const input = bar.querySelector("input");
    if (input) input.focus();
  } else {
    const input = bar.querySelector("input");
    if (input) input.value = "";
    filterConversation("");
  }
}

function filterConversation(query) {
  const container = document.querySelector(".conversation");
  if (!container) return;

  const msgs = container.querySelectorAll(".msg");
  const q = query.toLowerCase().trim();

  msgs.forEach((el) => {
    if (!q) {
      el.classList.remove("is-hidden");
      removeHighlights(el);
      return;
    }

    const text = el.textContent.toLowerCase();
    if (text.includes(q)) {
      el.classList.remove("is-hidden");
      highlightText(el, query.trim());
    } else {
      el.classList.add("is-hidden");
      removeHighlights(el);
    }
  });
}

function highlightText(el, query) {
  removeHighlights(el);
  const textNodes = [];
  const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  while (walker.nextNode()) textNodes.push(walker.currentNode);

  const lowerQ = query.toLowerCase();
  for (const node of textNodes) {
    const text = node.textContent;
    const idx = text.toLowerCase().indexOf(lowerQ);
    if (idx === -1) continue;

    const before = text.slice(0, idx);
    const match = text.slice(idx, idx + query.length);
    const after = text.slice(idx + query.length);

    const span = document.createElement("span");
    span.className = "search-highlight";
    span.textContent = match;

    const parent = node.parentNode;
    if (before) parent.insertBefore(document.createTextNode(before), node);
    parent.insertBefore(span, node);
    if (after) parent.insertBefore(document.createTextNode(after), node);
    parent.removeChild(node);
  }
}

function removeHighlights(el) {
  el.querySelectorAll(".search-highlight").forEach((span) => {
    const parent = span.parentNode;
    parent.replaceChild(document.createTextNode(span.textContent), span);
    parent.normalize();
  });
}

// ── Resume ──────────────────────────────────────────

function copyResumeCommand(sessionId, cwd, btn) {
  const parts = [];
  if (cwd) {
    parts.push(`cd '${cwd.replace(/'/g, "'\\''")}'`);
  }
  parts.push(`claude --resume '${sessionId.replace(/'/g, "'\\''")}'`);
  const command = parts.join(" && ");

  navigator.clipboard.writeText(command).then(() => {
    const original = btn.innerHTML;
    btn.innerHTML =
      '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>';
    btn.classList.add("is-copied");
    btn.dataset.tooltip = "Copied!";
    setTimeout(() => {
      btn.innerHTML = original;
      btn.classList.remove("is-copied");
      btn.dataset.tooltip = "Copy resume";
    }, 1500);
  });
}

// ── Utilities ───────────────────────────────────────

function renderToolUse(tool, input) {
  if (!input) {
    return `<div class="msg-tool"><span class="msg-tool-name">${escapeHTML(tool)}</span></div>`;
  }

  switch (tool) {
    case "Edit": {
      const file = input.file_path || "";
      const old_s = String(input.old_string || "");
      const new_s = String(input.new_string || "");
      return `<div class="msg-tool">
        <span class="msg-tool-name">Edit</span> <span class="msg-tool-file">${escapeHTML(file)}</span>
        <div class="msg-diff">
          <pre class="msg-diff-del">${escapeHTML(truncate(old_s, 500))}</pre>
          <pre class="msg-diff-add">${escapeHTML(truncate(new_s, 500))}</pre>
        </div>
      </div>`;
    }
    case "Write": {
      const file = input.file_path || "";
      const content = String(input.content || "");
      return `<div class="msg-tool">
        <span class="msg-tool-name">Write</span> <span class="msg-tool-file">${escapeHTML(file)}</span>
        ${content ? `<details class="msg-tool-detail"><summary>${escapeHTML(content.split("\\n")[0].slice(0, 80))}...</summary><pre>${escapeHTML(truncate(content, 2000))}</pre></details>` : ""}
      </div>`;
    }
    default: {
      const detail = formatToolInput(tool, input);
      return `<div class="msg-tool"><span class="msg-tool-name">${escapeHTML(tool)}</span>${detail ? `<pre class="msg-tool-input">${escapeHTML(detail)}</pre>` : ""}</div>`;
    }
  }
}

function formatToolInput(tool, input) {
  if (!input) return "";
  switch (tool) {
    case "Bash":
      return input.command || "";
    case "Read":
      return input.file_path || "";
    case "Glob":
      return input.pattern || "";
    case "Grep":
      return `${input.pattern || ""}${input.path ? "  " + input.path : ""}`;
    case "Agent":
      return input.prompt ? input.prompt.slice(0, 200) : "";
    case "AskUserQuestion": {
      const qs = input.questions;
      if (!Array.isArray(qs)) return "";
      return qs
        .map((q) => {
          const opts = (q.options || []).map((o) => o.label).join(" / ");
          return `${q.question || ""}${opts ? "\n  → " + opts : ""}`;
        })
        .join("\n");
    }
    default: {
      const keys = Object.keys(input);
      if (!keys.length) return "";
      const parts = keys.slice(0, 3).map((k) => {
        const v = String(input[k]);
        return `${k}: ${v.length > 120 ? v.slice(0, 120) + "..." : v}`;
      });
      return parts.join("\n");
    }
  }
}

function truncate(text, max) {
  return text.length > max ? text.slice(0, max) + "..." : text;
}

function cleanUserText(text) {
  const cmdMatch = text.match(/<command-name>([^<]+)<\/command-name>/);
  if (cmdMatch) {
    const argsMatch = text.match(/<command-args>([^<]*)<\/command-args>/);
    const args = argsMatch ? argsMatch[1].trim() : "";
    return args ? `${cmdMatch[1]} ${args}` : cmdMatch[1];
  }

  if (isSystemText(text)) return "";
  return stripTags(text);
}

function isSystemText(text) {
  const markers = [
    "<system-reminder>",
    "<local-command-caveat>",
    "<bash-input>",
    "<new-diagnostics>",
    "Base directory for this skill:",
    "This skill guides",
    "ARGUMENTS:",
    "Available skills",
    "UserPromptSubmit hook",
    "Plan mode",
    "Caveat: The messages below were generated",
    "DO NOT respond to these messages",
  ];
  return markers.some((m) => text.includes(m));
}

function renderMarkdown(text) {
  if (typeof marked !== "undefined") {
    return marked.parse(text, { breaks: true });
  }
  return `<p>${escapeHTML(text)}</p>`;
}

function initTheme() {
  const saved = localStorage.getItem("kiroku-theme") || "green";
  applyTheme(saved);

  const toggle = document.getElementById("theme-toggle");
  const dropdown = document.getElementById("theme-dropdown");
  if (!toggle || !dropdown) return;

  toggle.addEventListener("click", (e) => {
    e.stopPropagation();
    dropdown.hidden = !dropdown.hidden;
  });

  dropdown.addEventListener("click", (e) => {
    e.stopPropagation();
    const dot = e.target.closest(".theme-dot");
    if (!dot) return;
    applyTheme(dot.dataset.theme);
    dropdown.hidden = true;
  });

  document.addEventListener("click", () => {
    dropdown.hidden = true;
  });
}

const THEME_COLORS = {
  green: "#1f6b52",
  blue: "#2563eb",
  violet: "#7c3aed",
  amber: "#b07818",
  rose: "#c03050",
};

function applyTheme(theme) {
  document.documentElement.setAttribute("data-theme", theme);
  localStorage.setItem("kiroku-theme", theme);

  document.querySelectorAll(".theme-dot").forEach((dot) => {
    dot.classList.toggle("is-active", dot.dataset.theme === theme);
  });

  const color = THEME_COLORS[theme] || THEME_COLORS.green;
  const svg = `<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'><circle cx='16' cy='16' r='11' fill='${color}'/></svg>`;
  const favicon = document.getElementById("favicon");
  if (favicon) {
    favicon.href = "data:image/svg+xml," + encodeURIComponent(svg);
  }
}

function stripTags(text) {
  return text
    .replace(/<[^>]*>/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function formatLocalTime(isoString) {
  if (!isoString || isoString === "-") return "-";
  const d = new Date(isoString);
  if (isNaN(d.getTime())) return escapeHTML(isoString);
  return escapeHTML(
    d.toLocaleString("en-US", {
      year: "numeric",
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    }),
  );
}

function formatNumber(value) {
  return new Intl.NumberFormat("en-US").format(Number(value || 0));
}

// ── Daily stats ─────────────────────────────────────

async function loadDailyStats(date) {
  const root = document.getElementById("daily-view");
  if (!root) return;
  root.innerHTML = '<p class="empty">Loading...</p>';

  const res = await fetch(`/api/stats/daily?date=${encodeURIComponent(date)}`);
  if (!res.ok) {
    root.innerHTML = '<p class="empty">Failed to load daily stats.</p>';
    return;
  }

  const stats = await res.json();
  renderDailyStats(stats);
}

function renderDailyStats(stats) {
  const root = document.getElementById("daily-view");
  if (!root) return;

  const cost = stats.top_models ? calculateCostFromModels(stats.top_models) : 0;

  const isSkill = (t) => t.name.startsWith("/") || t.name.startsWith("auto:/");
  const isAgent = (t) => t.name.startsWith("agent:");
  const allTools = stats.top_tools || [];
  const skills = allTools.filter(isSkill);
  const agents = allTools.filter(isAgent);
  const tools = allTools.filter((t) => !isSkill(t) && !isAgent(t));

  const TOP_N = 8;
  const toolRowFn = (t) =>
    `<div class="detail-row daily-skill-filter" style="cursor:pointer" data-tool="${escapeHTML(t.name)}"><span>${escapeHTML(t.name)}</span><strong>${formatNumber(t.count)}</strong></div>`;
  const visibleTools = tools.slice(0, TOP_N);
  const hiddenTools = tools.slice(TOP_N);
  let toolRows = visibleTools.map(toolRowFn).join("");
  if (hiddenTools.length) {
    toolRows +=
      `<details class="daily-expand"><summary>${hiddenTools.length} more</summary><div class="detail-rows">` +
      hiddenTools.map(toolRowFn).join("") +
      `</div></details>`;
  }

  const modelRows = (stats.top_models || [])
    .map((m) => {
      const mCost = calculateCostForItem(m);
      return `<div class="detail-row"><span>${escapeHTML(m.name)}</span><strong>${formatCost(mCost)}</strong></div>`;
    })
    .join("");

  const skillBadges = skills
    .map((s) => {
      const auto = s.name.startsWith("auto:");
      const label = auto ? s.name.slice("auto:".length) : s.name;
      const cls = auto ? "badge badge-skill-auto" : "badge badge-skill";
      const tag = auto
        ? ' <span style="opacity:0.5;font-size:0.65rem">auto</span>'
        : "";
      return `<span class="${cls} daily-skill-filter" style="cursor:pointer" data-tool="${escapeHTML(s.name)}">${escapeHTML(label)}${tag} <span style="opacity:0.6">${s.count}</span></span>`;
    })
    .join("");

  const agentBadges = agents
    .map((a) => {
      const label = a.name.slice("agent:".length);
      return `<span class="badge badge-agent daily-skill-filter" style="cursor:pointer" data-tool="${escapeHTML(a.name)}">${escapeHTML(label)} <span style="opacity:0.6">${a.count}</span></span>`;
    })
    .join("");

  const sessionRows = (stats.sessions || [])
    .map((s) => sessionItemHTML(s))
    .join("");

  const [y, m, d] = stats.date.split("-");
  const dateLabel = `${parseInt(m)}/${parseInt(d)}/${y}`;

  root.innerHTML = `
    <a class="back" href="#/">&larr; Back</a>
    <div class="detail-header">
      <div>
        <h2 class="detail-title">${escapeHTML(dateLabel)}</h2>
      </div>
      <button class="action-icon has-tooltip" type="button" data-tooltip="Copy prompt" onclick="copyDailyPrompt(this)">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
      </button>
    </div>

    <div class="detail-stats">
      <div class="detail-stat"><span>Sessions</span><strong>${stats.total_sessions}</strong></div>
      <div class="detail-stat"><span>Messages</span><strong>${formatNumber(stats.total_messages)}</strong></div>
      <div class="detail-stat"><span>Tool Calls</span><strong>${formatNumber(stats.total_tool_calls)}</strong></div>
      <div class="detail-stat"><span>Est. Cost</span><strong>${formatCost(cost)}</strong></div>
    </div>

    <div class="detail-grid">
      <div class="detail-section">
        <h3>Tools</h3>
        <div class="detail-rows">${toolRows || '<p class="empty">No tool usage</p>'}</div>
      </div>
      <div class="detail-section">
        <h3>Models</h3>
        <div class="detail-rows">${modelRows || '<p class="empty">No model usage</p>'}</div>
      </div>
    </div>

    ${skillBadges ? `<div class="detail-section" style="margin-top: 16px"><h3>Skills</h3><div class="badge-row">${skillBadges}</div></div>` : ""}
    ${agentBadges ? `<div class="detail-section" style="margin-top: 16px"><h3>Agents</h3><div class="badge-row">${agentBadges}</div></div>` : ""}
    ${(stats.top_hooks || []).length ? `<div class="detail-section hooks-section" style="margin-top: 16px"><h3>Hooks</h3><div class="hooks-bar">${(stats.top_hooks || []).map((h) => formatHookPill(h.event, h.command, h.count)).join("")}</div></div>` : ""}

    <div class="detail-section" style="margin-top: 24px">
      <h3 id="daily-sessions-heading">Sessions
        <button id="daily-sort-toggle" class="search-toggle" type="button">\u2193 Newest</button>
        <button id="daily-search-toggle" class="search-toggle" type="button"><svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><circle cx="10" cy="10" r="7"/><line x1="15" y1="15" x2="21" y2="21"/></svg> Search</button>
      </h3>
      <div id="daily-filter-tags" class="badge-row" style="margin-bottom:8px"></div>
      <div id="daily-search-bar" class="conversation-search">
        <input id="daily-search-input" class="search-input" type="text" placeholder="Search message content..." />
        <button class="search-clear" type="button" hidden>&times;</button>
      </div>
      <nav id="daily-project-filter"></nav>
      <div id="daily-sessions-list" class="session-list">${sessionRows || '<p class="empty">No sessions</p>'}</div>
    </div>
  `;

  root.dataset.stats = JSON.stringify(stats);
  initDailyControls(stats);
}

const dailyFilters = { tool: "", cwd: "", content: "", dir: "desc", date: "" };

function initDailyControls(stats) {
  dailyFilters.date = stats.date;
  dailyFilters.tool = "";
  dailyFilters.cwd = "";
  dailyFilters.content = "";
  dailyFilters.dir = "desc";

  const root = document.getElementById("daily-view");
  if (!root) return;

  // Tool/skill click
  root.addEventListener("click", (e) => {
    const trigger = e.target.closest(".daily-skill-filter");
    if (trigger) {
      const tool = trigger.dataset.tool;
      dailyFilters.tool = dailyFilters.tool === tool ? "" : tool;
      fetchDailySessions();
      return;
    }
  });

  // Sort
  const sortBtn = document.getElementById("daily-sort-toggle");
  if (sortBtn) {
    sortBtn.addEventListener("click", () => {
      dailyFilters.dir = dailyFilters.dir === "desc" ? "asc" : "desc";
      sortBtn.textContent =
        dailyFilters.dir === "desc" ? "\u2193 Newest" : "\u2191 Oldest";
      fetchDailySessions();
    });
  }

  // Search
  const searchToggle = document.getElementById("daily-search-toggle");
  const searchBar = document.getElementById("daily-search-bar");
  const searchInput = document.getElementById("daily-search-input");
  let debounce = null;
  let comp = false;

  if (searchToggle && searchBar && searchInput) {
    const clearBtn = searchBar.querySelector(".search-clear");
    searchToggle.addEventListener("click", () => {
      const open = searchBar.classList.toggle("is-open");
      searchToggle.classList.toggle("is-active", open);
      if (open) {
        searchInput.focus();
      } else {
        searchInput.value = "";
        if (dailyFilters.content) {
          dailyFilters.content = "";
          fetchDailySessions();
        }
      }
    });
    searchInput.addEventListener("compositionstart", () => {
      comp = true;
    });
    searchInput.addEventListener("compositionend", () => {
      comp = false;
      doSearch();
    });
    searchInput.addEventListener("input", () => {
      if (!comp) doSearch();
    });
    searchInput.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        searchInput.value = "";
        dailyFilters.content = "";
        searchBar.classList.remove("is-open");
        searchToggle.classList.remove("is-active");
        fetchDailySessions();
      }
    });
    if (clearBtn) {
      clearBtn.addEventListener("click", () => {
        searchInput.value = "";
        clearBtn.hidden = true;
        dailyFilters.content = "";
        fetchDailySessions();
      });
    }
    function doSearch() {
      if (clearBtn) clearBtn.hidden = !searchInput.value;
      clearTimeout(debounce);
      debounce = setTimeout(() => {
        dailyFilters.content = searchInput.value.trim();
        fetchDailySessions();
      }, 300);
    }
  }

  // Project filter
  const filterRoot = document.getElementById("daily-project-filter");
  if (filterRoot && stats.sessions && stats.sessions.length) {
    const cwds = {};
    for (const s of stats.sessions) {
      if (s.cwd) {
        const name = s.cwd.split("/").pop();
        cwds[s.cwd] = (cwds[s.cwd] || 0) + 1;
      }
    }
    const projects = Object.entries(cwds)
      .map(([cwd, count]) => ({ name: cwd, session_count: count }))
      .sort((a, b) => b.session_count - a.session_count);

    if (projects.length > 1) {
      filterRoot.innerHTML = `
        <div class="filter-box">
          <input class="filter-input" type="text" placeholder="Filter by project..." />
          <button class="filter-clear" type="button" hidden>&times;</button>
          <div class="filter-dropdown" hidden></div>
        </div>`;
      const input = filterRoot.querySelector(".filter-input");
      const dropdown = filterRoot.querySelector(".filter-dropdown");
      const clearBtn = filterRoot.querySelector(".filter-clear");

      input.addEventListener("focus", () => {
        renderDailyProjectDropdown(projects, dropdown, input.value);
        dropdown.hidden = false;
      });
      input.addEventListener("input", () => {
        renderDailyProjectDropdown(projects, dropdown, input.value);
        dropdown.hidden = false;
      });
      if (clearBtn) {
        clearBtn.addEventListener("click", () => {
          input.value = "";
          clearBtn.hidden = true;
          dailyFilters.cwd = "";
          dropdown.hidden = true;
          fetchDailySessions();
        });
      }
      document.addEventListener("click", (e) => {
        if (!filterRoot.contains(e.target)) dropdown.hidden = true;
      });

      dropdown.addEventListener("click", (e) => {
        const opt = e.target.closest(".filter-option");
        if (!opt) return;
        const cwd = opt.dataset.cwd;
        if (dailyFilters.cwd === cwd) {
          dailyFilters.cwd = "";
          input.value = "";
          if (clearBtn) clearBtn.hidden = true;
        } else {
          dailyFilters.cwd = cwd;
          input.value = cwd.split("/").pop();
          if (clearBtn) clearBtn.hidden = false;
        }
        dropdown.hidden = true;
        fetchDailySessions();
      });
    }
  }
}

function renderDailyProjectDropdown(projects, dropdown, query) {
  const q = query.toLowerCase();
  const filtered = q
    ? projects.filter((p) => p.name.toLowerCase().includes(q))
    : projects;
  dropdown.innerHTML = filtered
    .map(
      (p) =>
        `<button class="filter-option${dailyFilters.cwd === p.name ? " is-active" : ""}" data-cwd="${escapeHTML(p.name)}" type="button">
          <span>${escapeHTML(p.name.split("/").pop())}</span>
          <span class="filter-option-count">${p.session_count}</span>
        </button>`,
    )
    .join("");
}

async function fetchDailySessions() {
  const list = document.getElementById("daily-sessions-list");
  const tags = document.getElementById("daily-filter-tags");
  if (!list) return;

  // Render filter tags
  if (tags) {
    let html = "";
    if (dailyFilters.tool) {
      const t = dailyFilters.tool;
      const label = t.startsWith("auto:")
        ? t.slice("auto:".length)
        : t.startsWith("agent:")
          ? t.slice("agent:".length)
          : t;
      const cls = t.startsWith("agent:")
        ? "badge badge-agent"
        : "badge badge-skill";
      html += `<span class="${cls}" style="cursor:pointer" data-clear="tool">${escapeHTML(label)} &times;</span>`;
    }
    if (dailyFilters.cwd) {
      html += `<span class="badge" style="cursor:pointer" data-clear="cwd">${escapeHTML(dailyFilters.cwd.split("/").pop())} &times;</span>`;
    }
    if (dailyFilters.content) {
      html += `<span class="badge" style="cursor:pointer" data-clear="content">&ldquo;${escapeHTML(dailyFilters.content)}&rdquo; &times;</span>`;
    }
    tags.innerHTML = html;
    tags.querySelectorAll("[data-clear]").forEach((el) => {
      el.addEventListener("click", () => {
        dailyFilters[el.dataset.clear] = "";
        if (el.dataset.clear === "content") {
          const si = document.getElementById("daily-search-input");
          if (si) si.value = "";
        }
        if (el.dataset.clear === "cwd") {
          const fi = document.querySelector(
            "#daily-project-filter .filter-input",
          );
          const fc = document.querySelector(
            "#daily-project-filter .filter-clear",
          );
          if (fi) fi.value = "";
          if (fc) fc.hidden = true;
        }
        fetchDailySessions();
      });
    });
  }

  // If no filters active, show original sessions from stats
  if (
    !dailyFilters.tool &&
    !dailyFilters.cwd &&
    !dailyFilters.content &&
    dailyFilters.dir === "desc"
  ) {
    const root = document.getElementById("daily-view");
    const stats = root ? JSON.parse(root.dataset.stats || "{}") : {};
    const rows = (stats.sessions || []).map((s) => sessionItemHTML(s)).join("");
    list.innerHTML = rows || '<p class="empty">No sessions</p>';
    return;
  }

  list.innerHTML = '<p class="empty">Loading...</p>';

  const params = new URLSearchParams({
    from: dailyFilters.date + "T00:00:00Z",
    to: dailyFilters.date + "T23:59:59Z",
    limit: "100",
  });
  if (dailyFilters.tool) params.set("tool", dailyFilters.tool);
  if (dailyFilters.cwd) params.set("cwd", dailyFilters.cwd);
  if (dailyFilters.content) params.set("content", dailyFilters.content);
  if (dailyFilters.dir === "asc") params.set("dir", "asc");

  const res = await fetch(`/api/sessions?${params}`);
  if (!res.ok) {
    list.innerHTML = '<p class="empty">Failed to load</p>';
    return;
  }
  const data = await res.json();
  const rows = (data.items || []).map((s) => sessionItemHTML(s)).join("");
  list.innerHTML = rows || '<p class="empty">No sessions</p>';
}

function generateDailyPrompt(stats) {
  const cost = stats.top_models ? calculateCostFromModels(stats.top_models) : 0;

  const lines = [
    `# ${stats.date} の Claude Code 使用レポート`,
    "",
    `- セッション数: ${stats.total_sessions}`,
    `- メッセージ数: ${stats.total_messages}`,
    `- ツール呼び出し: ${stats.total_tool_calls}`,
    `- 推定コスト: ${formatCost(cost)}`,
    "",
  ];

  const allTools = stats.top_tools || [];
  const isPromptSkill = (t) =>
    t.name.startsWith("/") || t.name.startsWith("auto:/");
  const isPromptAgent = (t) => t.name.startsWith("agent:");
  const promptTools = allTools.filter(
    (t) => !isPromptSkill(t) && !isPromptAgent(t),
  );
  const promptSkills = allTools.filter(isPromptSkill);
  const promptAgents = allTools.filter(isPromptAgent);

  if (promptTools.length) {
    lines.push("## ツール使用状況");
    for (const t of promptTools) {
      lines.push(`- ${t.name}: ${t.count} 回 (${t.session_count} セッション)`);
    }
    lines.push("");
  }

  if (promptSkills.length) {
    lines.push("## スキル使用状況");
    for (const t of promptSkills) {
      const auto = t.name.startsWith("auto:");
      const label = auto ? t.name.slice("auto:".length) : t.name;
      const tag = auto ? " [自動]" : "";
      lines.push(
        `- ${label}${tag}: ${t.count} 回 (${t.session_count} セッション)`,
      );
    }
    lines.push("");
  }

  if (promptAgents.length) {
    lines.push("## エージェント使用状況");
    for (const t of promptAgents) {
      const label = t.name.slice("agent:".length);
      lines.push(`- ${label}: ${t.count} 回 (${t.session_count} セッション)`);
    }
    lines.push("");
  }

  if (stats.top_hooks && stats.top_hooks.length) {
    lines.push("## フック発火状況");
    for (const h of stats.top_hooks) {
      const cmd = h.command && h.command !== "callback" ? h.command : "";
      const label = cmd ? `${h.event} (${cmd.split("/").pop()})` : h.event;
      lines.push(`- ${label}: ${h.count} 回 (${h.session_count} セッション)`);
    }
    lines.push("");
  }

  if (stats.top_models && stats.top_models.length) {
    lines.push("## モデル使用状況");
    for (const m of stats.top_models) {
      const mCost = calculateCostForItem(m);
      lines.push(
        `- ${m.name}: ${formatCost(mCost)} (${m.session_count} セッション, out=${formatNumber(m.output_tokens)} tokens)`,
      );
    }
    lines.push("");
  }

  if (stats.sessions && stats.sessions.length) {
    lines.push("## セッション一覧");
    for (const s of stats.sessions) {
      const project = s.cwd ? s.cwd.split("/").pop() : "-";
      const sCost = calculateCostForItem(s);
      const path = s.source_path || "";
      lines.push(
        `- [${project}] ${formatCost(sCost)}, ${s.message_count} msgs, branch: ${s.git_branch || "-"} — ${path}`,
      );
    }
    lines.push("");
  }

  lines.push(
    "---",
    "上記の統計データだけで振り返りをしてください。個別セッションの中身を読む必要はありません。",
    "観点：",
    "- 生産性: ツール使用パターンと作業効率",
    "- コスト効率: モデル選択と使用量",
    "- 改善点: 次回以降の改善案",
    "",
    "詳細が必要な場合のみ、上記パスのセッションログを読んでください。",
  );

  return lines.join("\n");
}

function copyDailyPrompt(btn) {
  const root = document.getElementById("daily-view");
  if (!root || !root.dataset.stats) return;

  const stats = JSON.parse(root.dataset.stats);
  const prompt = generateDailyPrompt(stats);

  navigator.clipboard.writeText(prompt).then(() => {
    const original = btn.innerHTML;
    btn.innerHTML =
      '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>';
    btn.classList.add("is-copied");
    btn.dataset.tooltip = "Copied!";
    setTimeout(() => {
      btn.innerHTML = original;
      btn.classList.remove("is-copied");
      btn.dataset.tooltip = "Copy prompt";
    }, 1500);
  });
}
