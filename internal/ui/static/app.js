// ── State ───────────────────────────────────────────

let nextCursor = "";
let loading = false;
let currentCWD = "";
let currentContentSearch = "";
let isIndexing = false;
let conversationReversed = false;

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

// ── Router ──────────────────────────────────────────

function navigate() {
  const hash = location.hash;
  const homeView = document.getElementById("home-view");
  const detailView = document.getElementById("detail-view");

  if (hash.startsWith("#/sessions/")) {
    const sessionId = decodeURIComponent(hash.slice("#/sessions/".length));
    homeView.hidden = true;
    detailView.hidden = false;
    loadSessionDetail(sessionId);
  } else {
    homeView.hidden = false;
    detailView.hidden = true;
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

window.addEventListener("hashchange", navigate);

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
  const models = detail.models || [];
  const totalCost = calculateCostFromModels(models);

  root.innerHTML = `
    <div class="detail-header">
      <a href="#/" class="back">\u2190 Sessions</a>
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
  const hooks = new Set();

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
        if (b.type === "hook" && b.text) {
          for (const cmd of b.text.split("\n")) {
            if (cmd.trim()) hooks.add(cmd.trim());
          }
        }
        if (b.type === "skill" && b.text) {
          const m = b.text.match(/<command-name>([^<]+)<\/command-name>/);
          if (m) skills.add(m[1]);
        }
      }
    }
  }

  if (!skills.size && !hooks.size) return "";

  let html =
    '<div class="detail-section"><h3>Skills & Hooks</h3><div class="badge-row">';
  for (const s of skills) {
    html += `<span class="badge badge-skill">${escapeHTML(s)}</span>`;
  }
  for (const h of hooks) {
    const short = h.length > 50 ? h.slice(0, 50) + "..." : h;
    html += `<span class="badge badge-hook" title="${escapeHTML(h)}">${escapeHTML(short)}</span>`;
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
            const m = b.text.match(/<command-name>([^<]+)<\/command-name>/);
            if (m)
              parts.push(
                `<span class="msg-role msg-role-system">Skill</span> <span class="badge badge-skill">${escapeHTML(m[1])}</span>`,
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
                return text
                  ? `<p class="msg-text">${escapeHTML(text)}</p>`
                  : "";
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
