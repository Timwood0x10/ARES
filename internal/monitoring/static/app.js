/* ═══════════════════════════════════════════════════════════════
   ARES Console — Premium Monitoring Dashboard
   ═══════════════════════════════════════════════════════════════ */
(function () {
  'use strict';

  // ── Constants ───────────────────────────
  var NS = 'http://www.w3.org/2000/svg';
  var API = '/api';
  var POLL_MS = 2000;

  // ── State ───────────────────────────────
  var State = {
    snap: null,
    dag: null,
    costBar: null,
    selectedAgentId: null,
    activeTab: 'events',
    activeDrawerTab: 'overview',
    tabData: {},
    searchQuery: '',
    agentDetail: null
  };

  var SSE = null;
  var PollTimer = null;

  // Rich color palette for agents
  var PALETTE = [
    '#818cf8', '#34d399', '#fbbf24', '#f87171', '#60a5fa',
    '#a78bfa', '#f472b6', '#22d3ee', '#fb923c', '#4ade80',
    '#facc15', '#c084fc', '#38bdf8', '#fb7185', '#2dd4bf'
  ];

  // ── HTTP Helpers ────────────────────────
  function get(path) {
    return fetch(API + path, { headers: { Accept: 'application/json' } })
      .then(function (r) { return r.ok ? r.json() : null; })
      .catch(function () { return null; });
  }

  function post(path, body) {
    return fetch(API + path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: body ? JSON.stringify(body) : null,
      signal: AbortSignal.timeout(15000)
    })
      .then(function (r) { return r.ok ? r.json() : null; })
      .catch(function () { return null; });
  }

  // ── Utility Functions ───────────────────
  function esc(s) {
    if (!s && s !== 0) return '';
    var d = document.createElement('div');
    d.textContent = String(s);
    return d.innerHTML;
  }

  function fmtTime(ts) {
    if (!ts) return '--:--:--';
    var d = new Date(ts);
    if (isNaN(d.getTime())) {
      var s = String(ts);
      return s.length > 8 ? s.slice(11, 19) : s;
    }
    var p = function (n) { return n < 10 ? '0' + n : String(n); };
    return p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds());
  }

  function fmtDateTime(ts) {
    if (!ts) return '--';
    var d = new Date(ts);
    if (isNaN(d.getTime())) return String(ts);
    var p = function (n) { return n < 10 ? '0' + n : String(n); };
    return p(d.getMonth() + 1) + '/' + p(d.getDate()) + ' ' +
      p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds());
  }

  function fmtDur(d) {
    if (!d) return '-';
    if (typeof d === 'number') {
      if (d > 1e9) return (d / 1e9).toFixed(1) + 's';
      if (d > 1e6) return (d / 1e6).toFixed(0) + 'ms';
      return d + 'ns';
    }
    if (typeof d === 'string') {
      return d;
    }
    return String(d);
  }

  function fmtNum(n) {
    if (n == null || isNaN(n)) return '0';
    if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
    if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K';
    return String(n);
  }

  function fmtCost(c) {
    if (c == null || isNaN(c)) return '$0.0000';
    return '$' + Number(c).toFixed(4);
  }

  function stColor(st) {
    switch (st) {
      case 'running': return 'var(--warn)';
      case 'completed': return 'var(--ok)';
      case 'dead': case 'failed': return 'var(--fail)';
      case 'resurrecting': return 'var(--warn)';
      case 'pending': return 'var(--info)';
      default: return 'var(--info)';
    }
  }

  function stClass(st) {
    if (!st) return 'pending';
    if (st === 'running' || st === 'resurrecting') return 'running';
    if (st === 'completed') return 'completed';
    if (st === 'dead' || st === 'failed') return 'dead';
    return 'pending';
  }

  function agentColor(idx) {
    return PALETTE[idx % PALETTE.length];
  }

  function showToast(msg, type) {
    type = type || 'info';
    var container = document.getElementById('toast-container');
    if (!container) return;
    var toast = document.createElement('div');
    toast.className = 'toast ' + type;
    var icon = type === 'success' ? '✓' : type === 'error' ? '✕' : type === 'warning' ? '⚠' : 'ℹ';
    toast.innerHTML = '<span class="toast-icon">' + icon + '</span><span>' + esc(msg) + '</span>';
    container.appendChild(toast);
    setTimeout(function () {
      toast.style.opacity = '0';
      toast.style.transform = 'translateX(20px)';
      toast.style.transition = 'all 0.3s ease';
      setTimeout(function () { toast.remove(); }, 300);
    }, 3000);
  }

  // ── SVG Helpers ─────────────────────────
  function mk(tag, attrs) {
    var el = document.createElementNS(NS, tag);
    for (var k in attrs) el.setAttribute(k, attrs[k]);
    return el;
  }

  // ════════════════════════════════════════
  // INITIALIZATION
  // ════════════════════════════════════════
  function init() {
    bindTabs();
    bindDrawerTabs();
    bindDetailClose();
    bindSearch();

    tick();
    PollTimer = setInterval(tick, POLL_MS);
    connectSSE();
  }

  // ── Data Polling ────────────────────────
  function tick() {
    Promise.all([
      get('/console'),
      get('/console/dag'),
      get('/console/cost-bar')
    ]).then(function (r) {
      State.snap = r[0];
      State.dag = r[1];
      State.costBar = r[2];

      renderGlobalStats();
      renderAgentList();
      renderCostBreakdownList();
      renderDAG();
      renderCostBar();
      renderActivityFeed();
      renderTaskList();
      renderAlerts();

      // Update time
      var timeEl = document.getElementById('update-time');
      if (timeEl && State.snap && State.snap.update_time) {
        timeEl.textContent = fmtTime(State.snap.update_time);
      }

      fetchActiveTab();

      // Refresh detail if open
      if (State.selectedAgentId && State.agentDetail) {
        refreshAgentDetail();
      }
    });
  }

  // ── SSE ─────────────────────────────────
  function connectSSE() {
    try {
      SSE = new EventSource(API + '/subscribe');
      SSE.addEventListener('snapshot', function (e) {
        try {
          State.snap = JSON.parse(e.data);
          renderGlobalStats();
          renderAgentList();
          renderDAG();
          renderActivityFeed();
          renderTaskList();
          renderAlerts();
        } catch (_) { }
      });
      SSE.onerror = function () {
        if (SSE) { SSE.close(); SSE = null; }
        setTimeout(connectSSE, 5000);
      };
    } catch (_) { }
  }

  // ════════════════════════════════════════
  // GLOBAL STATS (Top Bar)
  // ════════════════════════════════════════
  function renderGlobalStats() {
    if (!State.snap) return;

    var cost = State.snap.cost || {};
    var agents = State.snap.agents || [];
    var tasks = State.snap.tasks || [];

    var running = agents.filter(function (a) { return a.status === 'running' || a.status === 'resurrecting'; }).length;

    var costEl = document.getElementById('gs-cost');
    var tokensEl = document.getElementById('gs-tokens');
    var agentsEl = document.getElementById('gs-agents');
    var tasksEl = document.getElementById('gs-tasks');

    if (costEl) costEl.textContent = fmtCost(cost.total);
    if (tokensEl) {
      var totalTokens = 0;
      if (cost.by_agent) {
        Object.values(cost.by_agent).forEach(function (ac) {
          totalTokens += (ac.total_tokens || 0);
        });
      }
      tokensEl.textContent = fmtNum(totalTokens);
    }
    if (agentsEl) agentsEl.textContent = running + ' / ' + agents.length;
    if (tasksEl) tasksEl.textContent = tasks.length;
  }

  // ════════════════════════════════════════
  // AGENT LIST (Left Sidebar)
  // ════════════════════════════════════════
  function bindSearch() {
    var search = document.getElementById('agent-search');
    if (search) {
      search.addEventListener('input', function (e) {
        State.searchQuery = e.target.value.toLowerCase();
        renderAgentList();
      });
    }
  }

  function renderAgentList() {
    var listEl = document.getElementById('agent-list');
    var countEl = document.getElementById('agent-count-badge');
    if (!listEl || !State.snap) return;

    var agents = State.snap.agents || [];
    var costByAgent = (State.snap.cost && State.snap.cost.by_agent) || {};

    // Filter by search
    if (State.searchQuery) {
      agents = agents.filter(function (a) {
        return (a.name || a.id || '').toLowerCase().indexOf(State.searchQuery) >= 0 ||
          (a.role || '').toLowerCase().indexOf(State.searchQuery) >= 0;
      });
    }

    // Sort: running first, then pending, then completed, then failed
    var order = { running: 0, resurrecting: 0, pending: 1, completed: 2, dead: 3, failed: 3 };
    agents.sort(function (a, b) {
      var oa = order[a.status] != null ? order[a.status] : 99;
      var ob = order[b.status] != null ? order[b.status] : 99;
      if (oa !== ob) return oa - ob;
      return (a.name || a.id || '').localeCompare(b.name || b.id || '');
    });

    if (countEl) countEl.textContent = agents.length;

    if (!agents.length) {
      listEl.innerHTML = '<div class="empty-state" style="padding:1.5rem"><svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" class="empty-state-icon"><circle cx="12" cy="7" r="4"/><path d="M6 21v-2a4 4 0 014-4h4a4 4 0 014 4v2"/></svg>No agents found</div>';
      return;
    }

    var html = '';
    agents.forEach(function (a) {
      var sc = stClass(a.status);
      var sel = State.selectedAgentId === a.id ? ' selected' : '';
      var ac = costByAgent[a.id] || {};
      var cost = ac.estimated_cost != null ? ac.estimated_cost : 0;
      var isRunning = a.status === 'running' || a.status === 'resurrecting';
      var runTime = '';
      if (isRunning && a.started_at) {
        var diff = Date.now() - new Date(a.started_at);
        if (diff > 0) {
          var mins = Math.floor(diff / 60000);
          var secs = Math.floor((diff % 60000) / 1000);
          runTime = mins > 0 ? mins + 'm ' + secs + 's' : secs + 's';
        }
      }

      html += '<div class="agent-row' + sel + '" data-id="' + esc(a.id) + '">' +
        '<span class="agent-status-dot ' + sc + '"></span>' +
        '<div class="agent-row-info">' +
          '<div class="agent-row-name">' + esc(a.name || a.id) + '</div>' +
          '<div class="agent-row-meta">' +
            (a.role ? '<span class="role">' + esc(a.role) + '</span>' : '') +
            (a.model_name ? '<span class="model">' + esc(a.model_name) + '</span>' : '') +
            (runTime ? '<span class="runtime">' + runTime + '</span>' : '') +
          '</div>' +
          (a.tags && a.tags.length ? '<div class="agent-row-tags">' + a.tags.slice(0, 3).map(function (t) { return '<span class="agent-tag">' + esc(t) + '</span>'; }).join('') + '</div>' : '') +
        '</div>' +
        (cost > 0 ? '<span class="agent-row-cost">' + fmtCost(cost) + '</span>' : '') +
      '</div>';
    });

    listEl.innerHTML = html;

    // Bind clicks
    var rows = listEl.querySelectorAll('.agent-row');
    for (var i = 0; i < rows.length; i++) {
      rows[i].addEventListener('click', function () {
        var id = this.getAttribute('data-id');
        selectAgent(id);
      });
    }
  }

  // ════════════════════════════════════════
  // COST BREAKDOWN (Left Sidebar)
  // ════════════════════════════════════════
  function renderCostBreakdownList() {
    var listEl = document.getElementById('cost-breakdown-list');
    if (!listEl) return;

    var cb = State.costBar;
    if (!cb || !cb.entries || !cb.entries.length) {
      listEl.innerHTML = '<div class="empty-state" style="padding:1rem;font-size:0.65rem">No cost data</div>';
      return;
    }

    var total = cb.total || 1;
    var entries = cb.entries.slice().sort(function (a, b) {
      return (b.estimated_cost || 0) - (a.estimated_cost || 0);
    }).slice(0, 8);

    var html = '';
    entries.forEach(function (e, i) {
      var pct = total > 0 ? ((e.estimated_cost || 0) / total * 100).toFixed(1) : 0;
      var color = agentColor(i);
      html += '<div class="cost-item" data-agent="' + esc(e.agent_id) + '" style="cursor:pointer">' +
        '<div class="cost-item-header">' +
          '<span class="cost-item-name" title="' + esc(e.agent_id) + '">' + esc(e.agent_id) + '</span>' +
          '<span class="cost-item-amount">' + fmtCost(e.estimated_cost) + '</span>' +
        '</div>' +
        '<div class="cost-item-bar">' +
          '<div class="cost-item-bar-fill" style="width:' + pct + '%;background:' + color + ';box-shadow:0 0 6px ' + color + '40"></div>' +
        '</div>' +
      '</div>';
    });

    listEl.innerHTML = html;

    // Bind clicks
    var costItems = listEl.querySelectorAll('.cost-item');
    for (var i = 0; i < costItems.length; i++) {
      costItems[i].addEventListener('click', function () {
        var agentId = this.getAttribute('data-agent');
        if (agentId) {
          selectAgent(agentId);
        }
      });
    }
  }

  // ════════════════════════════════════════
  // DAG TOPOLOGY (Center)
  // ════════════════════════════════════════
  function renderDAG() {
    var svg = document.getElementById('dag');
    if (!svg) return;

    var W = svg.clientWidth || 800;
    var H = svg.clientHeight || 350;
    svg.setAttribute('viewBox', '0 0 ' + W + ' ' + H);
    svg.innerHTML = '';

    // Defs
    var defs = mk('defs', {});
    defs.innerHTML =
      '<pattern id="grid-pattern" width="40" height="40" patternUnits="userSpaceOnUse">' +
        '<circle cx="20" cy="20" r="0.8" fill="rgba(129,140,248,0.08)"/>' +
      '</pattern>' +
      '<filter id="node-glow"><feGaussianBlur stdDeviation="4" result="blur"/><feMerge><feMergeNode in="blur"/><feMergeNode in="SourceGraphic"/></feMerge></filter>' +
      '<filter id="edge-glow"><feGaussianBlur stdDeviation="2" result="blur"/><feMerge><feMergeNode in="blur"/><feMergeNode in="SourceGraphic"/></feMerge></filter>' +
      '<marker id="arrowhead" markerWidth="8" markerHeight="6" refX="8" refY="3" orient="auto">' +
        '<polygon points="0 0, 8 3, 0 6" fill="rgba(129,140,248,0.35)"/>' +
      '</marker>' +
      '<marker id="arrowhead-dep" markerWidth="8" markerHeight="6" refX="8" refY="3" orient="auto">' +
        '<polygon points="0 0, 8 3, 0 6" fill="rgba(251,191,36,0.4)"/>' +
      '</marker>' +
      '<marker id="arrowhead-data" markerWidth="8" markerHeight="6" refX="8" refY="3" orient="auto">' +
        '<polygon points="0 0, 8 3, 0 6" fill="rgba(52,211,153,0.4)"/>' +
      '</marker>' +
      '<radialGradient id="center-glow" cx="50%" cy="50%" r="50%">' +
        '<stop offset="0%" stop-color="rgba(129,140,248,0.1)"/>' +
        '<stop offset="100%" stop-color="rgba(129,140,248,0)"/>' +
      '</radialGradient>';
    svg.appendChild(defs);

    // Background grid
    svg.appendChild(mk('rect', { x: 0, y: 0, width: W, height: H, fill: 'url(#grid-pattern)' }));
    svg.appendChild(mk('rect', { x: 0, y: 0, width: W, height: H, fill: 'url(#center-glow)' }));

    // Get nodes from DAG API or fallback to agents
    var nodes = [];
    var edges = [];
    var dagStats = null;

    if (State.dag && State.dag.nodes) {
      var nodeMap = State.dag.nodes;
      var edgeMap = State.dag.edges;
      dagStats = State.dag.stats;
      nodes = Object.values(nodeMap);
      edges = Object.values(edgeMap);
    } else if (State.snap && State.snap.agents) {
      nodes = State.snap.agents.map(function (a) {
        return {
          id: a.id,
          name: a.name,
          type: 'agent',
          status: a.status,
          parent_id: a.parent_id,
          label: a.name || a.id,
          role: a.role,
          model_name: a.model_name,
          task_id: a.task_id,
          tags: a.tags,
          metadata: a.metadata,
          started_at: a.started_at,
          updated_at: a.updated_at
        };
      });
      // Build edges from parent relationships
      var agentMap = {};
      nodes.forEach(function (n) { agentMap[n.id] = n; });
      nodes.forEach(function (n) {
        if (n.parent_id && agentMap[n.parent_id]) {
          edges.push({
            id: n.parent_id + '-' + n.id,
            from_id: n.parent_id,
            to_id: n.id,
            type: 'parent',
            label: ''
          });
        }
      });
    }

    // Stats display
    var statsEl = document.getElementById('dag-stats');
    var subtitleEl = document.getElementById('dag-subtitle');

    if (dagStats) {
      if (statsEl) statsEl.textContent = dagStats.total_nodes + ' nodes · ' + dagStats.total_edges + ' edges';
      if (subtitleEl) {
        subtitleEl.textContent = (dagStats.running_nodes || 0) + ' running · ' + (dagStats.completed_nodes || 0) + ' completed · ' + nodes.length + ' total';
      }
    } else {
      if (statsEl) statsEl.textContent = nodes.length + ' agents';
      if (subtitleEl) {
        var running = nodes.filter(function (n) { return n.status === 'running' || n.status === 'resurrecting'; }).length;
        subtitleEl.textContent = running + ' active · ' + nodes.length + ' total agents';
      }
    }

    if (!nodes.length) {
      var t = mk('text', { x: W / 2, y: H / 2, 'text-anchor': 'middle', fill: 'var(--text-faint)', 'font-size': '13', 'font-weight': '500' });
      t.textContent = 'Waiting for agents...';
      svg.appendChild(t);
      var st = mk('text', { x: W / 2, y: H / 2 + 20, 'text-anchor': 'middle', fill: 'var(--text-muted)', 'font-size': '10' });
      st.textContent = 'Agent topology will appear here';
      svg.appendChild(st);
      return;
    }

    // Build node map
    var nodeMap = {};
    nodes.forEach(function (n) { nodeMap[n.id] = n; });

    // Compute positions using hierarchical layout
    var positions = {};

    // Find root nodes (no parent or parent not in list)
    var roots = nodes.filter(function (n) {
      var pid = n.parent_id || n.ParentID;
      return !pid || !nodeMap[pid];
    });

    if (roots.length > 0 && roots.length < nodes.length) {
      // BFS to determine levels
      var visited = {};
      var queue = roots.map(function (r) { return { id: r.id, level: 0 }; });
      var levelMap = {};

      while (queue.length) {
        var item = queue.shift();
        if (visited[item.id]) continue;
        visited[item.id] = true;
        if (!levelMap[item.level]) levelMap[item.level] = [];
        levelMap[item.level].push(item.id);

        // Find children from edges
        edges.forEach(function (e) {
          var fromId = e.from_id || e.FromID;
          var toId = e.to_id || e.ToID;
          if (fromId === item.id && !visited[toId]) {
            queue.push({ id: toId, level: item.level + 1 });
          }
        });

        // Also check parent_id field as fallback
        nodes.forEach(function (n) {
          var pid = n.parent_id || n.ParentID;
          if (pid === item.id && !visited[n.id]) {
            queue.push({ id: n.id, level: item.level + 1 });
          }
        });
      }

      // Add unvisited nodes to a final level
      var unvisited = [];
      nodes.forEach(function (n) {
        if (!visited[n.id]) unvisited.push(n.id);
      });
      if (unvisited.length) {
        var maxLevel = Math.max.apply(null, Object.keys(levelMap).map(Number));
        if (!levelMap[maxLevel + 1]) levelMap[maxLevel + 1] = [];
        levelMap[maxLevel + 1] = levelMap[maxLevel + 1].concat(unvisited);
      }

      var levelKeys = Object.keys(levelMap).map(Number).sort(function (a, b) { return a - b; });
      var numLevels = levelKeys.length;

      levelKeys.forEach(function (lvl, li) {
        var ids = levelMap[lvl];
        var lx = W * 0.1 + (li / Math.max(1, numLevels - 1)) * (W * 0.8);
        ids.forEach(function (id, ni) {
          var ly = H * 0.15 + (ni / Math.max(1, ids.length - 1)) * (H * 0.7);
          positions[id] = { x: lx, y: ly };
        });
      });
    } else {
      // Radial layout
      var cx = W / 2, cy = H / 2;
      var radiusX = Math.min(W * 0.38, 280);
      var radiusY = Math.min(H * 0.35, 140);
      nodes.forEach(function (a, i) {
        var angle = (i / nodes.length) * 2 * Math.PI - Math.PI / 2;
        positions[a.id] = {
          x: cx + radiusX * Math.cos(angle),
          y: cy + radiusY * Math.sin(angle)
        };
      });
    }

    // Draw edges first (behind nodes)
    var edgesGroup = mk('g', { class: 'edges-group' });
    var drawnEdges = {};

    edges.forEach(function (edge) {
      var fromId = edge.from_id || edge.FromID;
      var toId = edge.to_id || edge.ToID;
      var edgeType = edge.type || edge.Type || 'parent';

      if (!fromId || !toId) return;
      if (!positions[fromId] || !positions[toId]) return;

      var edgeKey = fromId + '->' + toId;
      if (drawnEdges[edgeKey]) return;
      drawnEdges[edgeKey] = true;

      var p1 = positions[fromId];
      var p2 = positions[toId];

      // Curved path
      var dx = p2.x - p1.x;
      var dy = p2.y - p1.y;
      var cx1 = p1.x + dx * 0.5;
      var cy1 = p1.y;
      var cx2 = p1.x + dx * 0.5;
      var cy2 = p2.y;

      // Edge color based on type
      var edgeColor, markerEnd;
      switch (edgeType) {
        case 'depends':
          edgeColor = 'rgba(251, 191, 36, 0.35)';
          markerEnd = 'url(#arrowhead-dep)';
          break;
        case 'data':
          edgeColor = 'rgba(52, 211, 153, 0.35)';
          markerEnd = 'url(#arrowhead-data)';
          break;
        case 'trigger':
          edgeColor = 'rgba(244, 114, 182, 0.35)';
          markerEnd = 'url(#arrowhead)';
          break;
        default: // parent
          edgeColor = 'rgba(129, 140, 248, 0.3)';
          markerEnd = 'url(#arrowhead)';
      }

      var path = mk('path', {
        d: 'M' + p1.x + ',' + p1.y + ' C' + cx1 + ',' + cy1 + ' ' + cx2 + ',' + cy2 + ' ' + p2.x + ',' + p2.y,
        fill: 'none',
        stroke: edgeColor,
        'stroke-width': '1.5',
        'stroke-dasharray': '5 5',
        class: 'edge-path',
        'marker-end': markerEnd
      });
      edgesGroup.appendChild(path);

      // Glow layer
      var glowPath = mk('path', {
        d: 'M' + p1.x + ',' + p1.y + ' C' + cx1 + ',' + cy1 + ' ' + cx2 + ',' + cy2 + ' ' + p2.x + ',' + p2.y,
        fill: 'none',
        stroke: edgeColor.replace('0.3', '0.1').replace('0.35', '0.1'),
        'stroke-width': '4',
        filter: 'url(#edge-glow)'
      });
      edgesGroup.insertBefore(glowPath, path);

      // Edge label (if any)
      if (edge.label || edge.Label) {
        var midX = (p1.x + p2.x) / 2;
        var midY = (p1.y + p2.y) / 2 - 8;
        var labelText = mk('text', {
          x: midX,
          y: midY,
          'text-anchor': 'middle',
          fill: 'var(--text-muted)',
          'font-size': '9',
          'font-weight': '500'
        });
        labelText.textContent = edge.label || edge.Label;
        edgesGroup.appendChild(labelText);
      }
    });
    svg.appendChild(edgesGroup);

    // Draw nodes
    var nodesGroup = mk('g', { class: 'nodes-group' });

    // Also include agent cost map
    var costByAgent = (State.snap && State.snap.cost && State.snap.cost.by_agent) || {};

    nodes.forEach(function (node, idx) {
      var pos = positions[node.id];
      if (!pos) return;

      var st = node.status || 'pending';
      var color = stColor(st);
      var isSel = State.selectedAgentId === node.id;
      var nodeType = node.type || 'agent';
      var r = isSel ? 28 : 24;
      if (nodeType === 'task') r = isSel ? 22 : 18;

      var g = mk('g', {
        class: 'node-group',
        'data-id': node.id,
        transform: 'translate(' + pos.x + ',' + pos.y + ')'
      });

      // Outer glow for running
      if (st === 'running' || st === 'resurrecting') {
        g.appendChild(mk('circle', {
          r: r + 8,
          fill: 'none',
          stroke: color,
          'stroke-width': '1',
          'stroke-opacity': '0.2',
          class: 'node-pulse-ring'
        }));
      }

      // Selection ring
      if (isSel) {
        g.appendChild(mk('circle', {
          r: r + 6,
          fill: 'none',
          stroke: 'var(--accent)',
          'stroke-width': '2',
          class: 'node-selected-ring'
        }));
        g.appendChild(mk('circle', {
          r: r + 12,
          fill: 'none',
          stroke: 'var(--accent)',
          'stroke-width': '1',
          'stroke-opacity': '0.15'
        }));
      }

      // Main node with gradient
      var gradId = 'node-grad-' + idx;
      var grad = document.createElementNS(NS, 'radialGradient');
      grad.setAttribute('id', gradId);
      grad.setAttribute('cx', '30%');
      grad.setAttribute('cy', '30%');
      var stop1 = document.createElementNS(NS, 'stop');
      stop1.setAttribute('offset', '0%');
      stop1.setAttribute('stop-color', color);
      stop1.setAttribute('stop-opacity', '0.25');
      var stop2 = document.createElementNS(NS, 'stop');
      stop2.setAttribute('offset', '100%');
      stop2.setAttribute('stop-color', color);
      stop2.setAttribute('stop-opacity', '0.05');
      grad.appendChild(stop1);
      grad.appendChild(stop2);
      defs.appendChild(grad);

      g.appendChild(mk('circle', {
        r: r,
        fill: 'url(#' + gradId + ')',
        stroke: isSel ? 'var(--accent)' : color,
        'stroke-width': isSel ? '2.5' : '1.5',
        class: 'node-circle-main'
      }));

      // Inner ring
      g.appendChild(mk('circle', {
        r: r - 5,
        fill: 'none',
        stroke: color,
        'stroke-width': '1',
        'stroke-opacity': '0.2'
      }));

      // Status dot
      g.appendChild(mk('circle', {
        r: 3.5,
        fill: color,
        filter: 'url(#node-glow)'
      }));

      // Agent initial/icon
      var initial = (node.name || node.label || node.id || '?').charAt(0).toUpperCase();
      var label = mk('text', {
        y: '4',
        'text-anchor': 'middle',
        'dominant-baseline': 'central',
        fill: 'var(--text-bright)',
        'font-size': nodeType === 'task' ? '10' : '11',
        'font-weight': '700',
        'pointer-events': 'none',
        'font-family': 'var(--font-sans)'
      });
      label.textContent = initial;
      g.appendChild(label);

      // Type indicator (small icon for task nodes)
      if (nodeType === 'task') {
        var typeIcon = mk('text', {
          y: -r - 2,
          'text-anchor': 'middle',
          fill: 'var(--text-muted)',
          'font-size': '8',
          'pointer-events': 'none'
        });
        typeIcon.textContent = '⚙';
        g.appendChild(typeIcon);
      }

      // Name label below
      var name = (node.name || node.label || node.id || '?');
      var displayName = name.length > 16 ? name.slice(0, 14) + '..' : name;
      var nameLabel = mk('text', {
        y: r + 14,
        'text-anchor': 'middle',
        fill: 'var(--text-dim)',
        'font-size': '9.5',
        'font-weight': '500',
        'pointer-events': 'none',
        'font-family': 'var(--font-sans)'
      });
      nameLabel.textContent = displayName;
      g.appendChild(nameLabel);

      // Status pill
      var statusLabel = mk('text', {
        y: r + 26,
        'text-anchor': 'middle',
        fill: color,
        'font-size': '8',
        'font-weight': '600',
        'pointer-events': 'none',
        'text-transform': 'uppercase',
        'letter-spacing': '0.08em',
        'font-family': 'var(--font-sans)'
      });
      statusLabel.textContent = st;
      g.appendChild(statusLabel);

      // Cost badge
      var ac = costByAgent[node.id] || {};
      if (ac.estimated_cost > 0) {
        var badgeR = 10;
        var bx = r - 2;
        var by = -r + 2;

        g.appendChild(mk('circle', {
          cx: bx, cy: by, r: badgeR,
          fill: 'var(--bg-card)',
          stroke: 'var(--ok)',
          'stroke-width': '1.2'
        }));
        var costLbl = mk('text', {
          x: bx, y: by + 1,
          'text-anchor': 'middle',
          'dominant-baseline': 'central',
          fill: 'var(--ok)',
          'font-size': '6.5',
          'font-weight': '800',
          'pointer-events': 'none',
          'font-family': 'var(--font-sans)'
        });
        costLbl.textContent = '$';
        g.appendChild(costLbl);
      }

      // Hit area
      g.appendChild(mk('circle', {
        r: r + 16,
        fill: 'transparent',
        style: 'cursor:pointer'
      }));

      // Events
      g.addEventListener('click', function () { selectAgent(node.id); });
      g.addEventListener('mouseenter', function (e) { showDAGTooltip(e, node); });
      g.addEventListener('mousemove', function (e) { moveDAGTooltip(e); });
      g.addEventListener('mouseleave', hideDAGTooltip);

      nodesGroup.appendChild(g);
    });

    svg.appendChild(nodesGroup);
  }

  // ── DAG Tooltip ─────────────────────────
  var tooltipX = 0, tooltipY = 0;

  function showDAGTooltip(evt, node) {
    var tip = document.getElementById('dag-tooltip');
    if (!tip) return;

    var costByAgent = (State.snap && State.snap.cost && State.snap.cost.by_agent) || {};
    var ac = costByAgent[node.id] || {};
    var st = node.status || 'pending';

    var html = '<div class="tt-name">' + esc(node.name || node.label || node.id) + '</div>';

    html += '<div class="tt-row">' +
      '<span class="k">Status</span>' +
      '<span class="v"><span class="tt-status ' + stClass(st) + '">' + esc(st) + '</span></span>' +
    '</div>';
    html += '<div class="tt-row"><span class="k">Type</span><span class="v">' + esc(node.type || 'agent') + '</span></div>';
    if (node.role) html += '<div class="tt-row"><span class="k">Role</span><span class="v">' + esc(node.role) + '</span></div>';
    if (node.model_name) html += '<div class="tt-row"><span class="k">Model</span><span class="v">' + esc(node.model_name) + '</span></div>';
    if (node.task_id) html += '<div class="tt-row"><span class="k">Task ID</span><span class="v mono">' + esc(node.task_id) + '</span></div>';
    if (node.agent_type) html += '<div class="tt-row"><span class="k">Agent Type</span><span class="v">' + esc(node.agent_type) + '</span></div>';
    if (node.source) html += '<div class="tt-row"><span class="k">Source</span><span class="v">' + esc(node.source) + '</span></div>';
    if (node.tags && node.tags.length) {
      html += '<div class="tt-section-title">Tags</div>';
      html += '<div style="display:flex;flex-wrap:wrap;gap:4px">';
      node.tags.forEach(function (t) {
        html += '<span class="tt-tag">' + esc(t) + '</span>';
      });
      html += '</div>';
    }

    if (ac.estimated_cost > 0 || ac.total_tokens > 0 || ac.call_count > 0) {
      html += '<div class="tt-section-title">Cost</div>';
      if (ac.estimated_cost) html += '<div class="tt-row"><span class="k">Est. Cost</span><span class="v" style="color:var(--ok)">' + fmtCost(ac.estimated_cost) + '</span></div>';
      if (ac.total_tokens) html += '<div class="tt-row"><span class="k">Tokens</span><span class="v">' + fmtNum(ac.total_tokens) + '</span></div>';
      if (ac.call_count) html += '<div class="tt-row"><span class="k">LLM Calls</span><span class="v">' + ac.call_count + '</span></div>';
    }

    html += '<div class="tt-section-title">Timing</div>';
    if (node.started_at || node.CreatedAt) html += '<div class="tt-row"><span class="k">Created</span><span class="v">' + fmtDateTime(node.started_at || node.created_at || node.CreatedAt) + '</span></div>';
    if (node.updated_at || node.UpdatedAt) html += '<div class="tt-row"><span class="k">Updated</span><span class="v">' + fmtTime(node.updated_at || node.UpdatedAt) + '</span></div>';

    if (node.parent_id || node.ParentID) html += '<div class="tt-row"><span class="k">Parent</span><span class="v" style="font-family:var(--font-mono);font-size:0.62rem">' + esc(node.parent_id || node.ParentID) + '</span></div>';

    if (node.message) html += '<div class="tt-row"><span class="k">Message</span><span class="v">' + esc(node.message) + '</span></div>';

    tip.innerHTML = html;
    tip.className = 'dag-tooltip visible';
    moveDAGTooltip(evt);
  }

  function moveDAGTooltip(evt) {
    var tip = document.getElementById('dag-tooltip');
    if (!tip) return;
    var canvas = document.querySelector('.dag-canvas-wrap');
    if (!canvas) return;
    var rect = canvas.getBoundingClientRect();
    var x = evt.clientX - rect.left + 16;
    var y = evt.clientY - rect.top + 16;

    // Keep within bounds
    var tipRect = tip.getBoundingClientRect();
    if (x + tipRect.width > rect.width) x = evt.clientX - rect.left - tipRect.width - 16;
    if (y + tipRect.height > rect.height) y = evt.clientY - rect.top - tipRect.height - 16;

    tip.style.left = x + 'px';
    tip.style.top = y + 'px';
  }

  function hideDAGTooltip() {
    var tip = document.getElementById('dag-tooltip');
    if (tip) tip.className = 'dag-tooltip';
  }

  // ════════════════════════════════════════
  // COST BAR (Center)
  // ════════════════════════════════════════
  function renderCostBar() {
    var barEl = document.getElementById('cost-bar');
    var legendEl = document.getElementById('cost-legend');
    var totalLabel = document.getElementById('cost-total-label');
    if (!barEl) return;

    var cb = State.costBar;
    if (!cb || !cb.entries || !cb.entries.length) {
      barEl.innerHTML = '';
      if (legendEl) legendEl.innerHTML = '';
      if (totalLabel) totalLabel.textContent = '$0.0000 total';
      return;
    }

    var total = cb.total || 1;
    if (totalLabel) totalLabel.textContent = fmtCost(total) + ' total';

    // Sort by cost descending
    var sorted = cb.entries.slice().sort(function (a, b) {
      return (b.estimated_cost || 0) - (a.estimated_cost || 0);
    });

    var segs = '';
    var legend = '';

    sorted.forEach(function (e, i) {
      var cost = e.estimated_cost || 0;
      var pct = total > 0 ? (cost / total * 100).toFixed(2) : 0;
      var color = agentColor(i);

      segs += '<div class="cost-bar-seg" style="width:' + pct + '%;background:linear-gradient(180deg,' + color + ',' + color + 'cc)" title="' + esc(e.agent_id) + ': ' + fmtCost(cost) + '"></div>';

      legend += '<div class="cost-legend-item" data-agent="' + esc(e.agent_id) + '" title="' + esc(e.agent_id) + '">' +
        '<span class="cost-legend-dot" style="background:' + color + ';color:' + color + '"></span>' +
        '<span class="cost-legend-name">' + esc(e.agent_id) + '</span>' +
        '<span class="cost-legend-val">' + fmtCost(cost) + '</span>' +
      '</div>';
    });

    barEl.innerHTML = segs;
    if (legendEl) {
      legendEl.innerHTML = legend;

      var legendItems = legendEl.querySelectorAll('.cost-legend-item');
      for (var i = 0; i < legendItems.length; i++) {
        legendItems[i].addEventListener('click', function () {
          var agentId = this.getAttribute('data-agent');
          if (agentId) {
            selectAgent(agentId);
          }
        });
      }
    }
  }

  // ════════════════════════════════════════
  // ACTIVITY FEED (Right Sidebar)
  // ════════════════════════════════════════
  function renderActivityFeed() {
    var feedEl = document.getElementById('activity-feed');
    if (!feedEl || !State.snap) return;

    var tasks = State.snap.tasks || [];
    var events = State.snap.events || [];

    // Combine tasks and events for activity
    var items = [];

    tasks.slice().sort(function (a, b) {
      return new Date(b.started_at || 0) - new Date(a.started_at || 0);
    }).slice(0, 15).forEach(function (t) {
      items.push({
        time: t.started_at,
        type: 'task',
        status: t.status,
        title: t.name || t.id,
        detail: t.agent_id ? '@' + t.agent_id : ''
      });
    });

    events.slice(0, 30).forEach(function (e) {
      items.push({
        time: e.timestamp,
        type: 'event',
        status: 'info',
        title: e.type,
        detail: e.module_name || e.stream_id || ''
      });
    });

    items.sort(function (a, b) {
      return new Date(b.time || 0) - new Date(a.time || 0);
    });

    if (!items.length) {
      feedEl.innerHTML = '<div class="empty-state" style="padding:1.5rem;font-size:0.65rem">No activity yet</div>';
      return;
    }

    var html = '';
    items.slice(0, 30).forEach(function (item) {
      var sc = stClass(item.status);
      var statusLabel = item.status || item.type;
      html += '<div class="activity-item ' + sc + '">' +
        '<span class="activity-time">' + fmtTime(item.time) + '</span>' +
        '<div class="activity-content">' +
          '<span class="activity-tag ' + sc + '">' + esc(statusLabel) + '</span>' +
          '<strong>' + esc(item.title) + '</strong>' +
          (item.detail ? ' <span style="color:var(--text-faint)">' + esc(item.detail) + '</span>' : '') +
        '</div>' +
      '</div>';
    });

    feedEl.innerHTML = html;
  }

  // ════════════════════════════════════════
  // TASK LIST (Right Sidebar)
  // ════════════════════════════════════════
  function renderTaskList() {
    var listEl = document.getElementById('task-list');
    var countEl = document.getElementById('task-count-badge');
    if (!listEl || !State.snap) return;

    var tasks = State.snap.tasks || [];
    if (countEl) countEl.textContent = tasks.length;

    if (!tasks.length) {
      listEl.innerHTML = '<div class="empty-state" style="padding:1.5rem;font-size:0.65rem">No tasks</div>';
      return;
    }

    // Sort: running first
    tasks = tasks.slice().sort(function (a, b) {
      var order = { running: 0, resurrecting: 0, pending: 1, completed: 2, failed: 3, dead: 3 };
      var oa = order[a.status] != null ? order[a.status] : 99;
      var ob = order[b.status] != null ? order[b.status] : 99;
      if (oa !== ob) return oa - ob;
      return new Date(b.started_at || 0) - new Date(a.started_at || 0);
    });

    var html = '';
    tasks.slice(0, 15).forEach(function (t) {
      var sc = stClass(t.status);
      var hasProgress = t.progress != null && t.progress >= 0;
      html += '<div class="task-item" data-id="' + esc(t.id) + '" data-agent="' + esc(t.agent_id || '') + '">' +
        '<span class="task-status ' + sc + '"></span>' +
        '<div class="task-info">' +
          '<div class="task-name">' + esc(t.name || t.id) + '</div>' +
          '<div class="task-meta">' +
            (t.agent_id ? '<span style="color:var(--accent)">' + esc(t.agent_id) + '</span>' : '') +
            '<span>' + fmtTime(t.started_at) + '</span>' +
          '</div>' +
          (hasProgress ? '<div class="task-progress-mini"><div class="task-progress-fill-mini" style="width:' + (t.progress * 100).toFixed(0) + '%"></div></div>' : '') +
        '</div>' +
      '</div>';
    });

    listEl.innerHTML = html;

    // Bind clicks on tasks to select agent
    var taskItems = listEl.querySelectorAll('.task-item');
    for (var i = 0; i < taskItems.length; i++) {
      taskItems[i].addEventListener('click', function () {
        var agentId = this.getAttribute('data-agent');
        if (agentId) {
          selectAgent(agentId);
        }
      });
    }
  }

  // ════════════════════════════════════════
  // ALERTS (Right Sidebar)
  // ════════════════════════════════════════
  function renderAlerts() {
    var listEl = document.getElementById('alerts-list');
    var countEl = document.getElementById('alert-count');
    if (!listEl || !State.snap) return;

    var alerts = State.snap.alerts || [];

    // Also generate alerts from failed/dead agents
    var agents = State.snap.agents || [];
    var failedAgents = agents.filter(function (a) {
      return a.status === 'dead' || a.status === 'failed';
    });

    var allAlerts = alerts.slice();
    failedAgents.forEach(function (a) {
      allAlerts.push({
        severity: 'error',
        message: 'Agent ' + (a.name || a.id) + ' has ' + a.status,
        agent_id: a.id,
        triggered_at: a.updated_at || a.started_at
      });
    });

    if (countEl) {
      if (allAlerts.length > 0) {
        countEl.textContent = allAlerts.length;
        countEl.classList.remove('hidden');
      } else {
        countEl.classList.add('hidden');
      }
    }

    if (!allAlerts.length) {
      listEl.innerHTML = '<div class="empty-state" style="padding:1.5rem;font-size:0.65rem">No alerts</div>';
      return;
    }

    var html = '';
    allAlerts.slice(0, 8).forEach(function (a) {
      var sev = a.severity === 'warning' ? 'warning' : '';
      html += '<div class="alert-item ' + sev + '" data-agent="' + esc(a.agent_id || '') + '">' +
        '<div class="alert-title">' + esc(a.message || a.alert_type || 'Alert') + '</div>' +
        (a.agent_id ? '<div class="alert-desc">' + esc(a.agent_id) + '</div>' : '') +
        '<div class="alert-time">' + fmtDateTime(a.triggered_at || a.timestamp) + '</div>' +
      '</div>';
    });

    listEl.innerHTML = html;

    // Bind clicks on alerts to select agent
    var alertItems = listEl.querySelectorAll('.alert-item');
    for (var i = 0; i < alertItems.length; i++) {
      alertItems[i].addEventListener('click', function () {
        var agentId = this.getAttribute('data-agent');
        if (agentId) {
          selectAgent(agentId);
        }
      });
    }
  }

  // ════════════════════════════════════════
  // TABS (Bottom)
  // ════════════════════════════════════════
  function bindTabs() {
    var tabs = document.querySelectorAll('.tab-btn');
    for (var i = 0; i < tabs.length; i++) {
      tabs[i].addEventListener('click', function () {
        setActiveTab(this.getAttribute('data-tab'));
      });
    }
  }

  function setActiveTab(name) {
    State.activeTab = name;
    var tabs = document.querySelectorAll('.tab-btn');
    for (var i = 0; i < tabs.length; i++) {
      tabs[i].className = 'tab-btn' + (tabs[i].getAttribute('data-tab') === name ? ' active' : '');
    }
    fetchActiveTab();
  }

  function fetchActiveTab() {
    get('/tabs/' + State.activeTab).then(function (data) {
      State.tabData[State.activeTab] = data;
      renderTabContent();
      updateTabCounts();
    });
  }

  function updateTabCounts() {
    var data = State.tabData[State.activeTab];
    if (!data) return;

    if (State.activeTab === 'events' && data.total != null) {
      var badge = document.getElementById('tab-count-events');
      if (badge) {
        badge.textContent = fmtNum(data.total);
        badge.classList.remove('hidden');
      }
    }
    if (State.activeTab === 'llm' && data.stats && data.stats.total_calls != null) {
      var llmBadge = document.getElementById('tab-count-llm');
      if (llmBadge) {
        llmBadge.textContent = fmtNum(data.stats.total_calls);
        llmBadge.classList.remove('hidden');
      }
    }
    if (State.activeTab === 'workflow' && data.total != null) {
      var wfBadge = document.getElementById('tab-count-workflow');
      if (!wfBadge) {
        var wfBtn = document.querySelector('.tab-btn[data-tab="workflow"]');
        if (wfBtn) {
          var span = document.createElement('span');
          span.className = 'tab-count';
          span.id = 'tab-count-workflow';
          span.textContent = fmtNum(data.total);
          wfBtn.appendChild(span);
        }
      } else {
        wfBadge.textContent = fmtNum(data.total);
        wfBadge.classList.remove('hidden');
      }
    }
  }

  function renderTabContent() {
    var el = document.getElementById('tab-content');
    if (!el) return;
    var data = State.tabData[State.activeTab];
    if (!data) {
      el.innerHTML = '<div class="empty-state">Loading...</div>';
      return;
    }

    switch (State.activeTab) {
      case 'events': renderEventsTab(el, data); break;
      case 'workflow': renderWorkflowTab(el, data); break;
      case 'mcp': renderMCPTab(el, data); break;
      case 'llm': renderLLMTab(el, data); break;
      case 'memory': renderMemoryTab(el, data); break;
      case 'evolution': renderEvolutionTab(el, data); break;
      case 'arena': renderArenaTab(el, data); break;
      default: el.innerHTML = '<div class="empty-state">Unknown tab</div>';
    }
  }

  // ── Events Tab ──────────────────────────
  function renderEventsTab(el, data) {
    var evts = data.events || [];
    var total = data.total || evts.length;

    if (!evts.length) {
      el.innerHTML = '<div class="empty-state">No events</div>';
      return;
    }

    // Count by type for bar chart
    var typeCounts = {};
    evts.forEach(function (e) {
      var t = e.type || 'unknown';
      typeCounts[t] = (typeCounts[t] || 0) + 1;
    });
    var types = Object.keys(typeCounts).sort(function (a, b) {
      return typeCounts[b] - typeCounts[a];
    });
    var maxCount = Math.max.apply(null, Object.values(typeCounts));

    var html = '<div class="tab-section-title">Event Distribution</div>';
    html += '<div class="bar-chart">';
    types.slice(0, 8).forEach(function (t) {
      var pct = maxCount > 0 ? (typeCounts[t] / maxCount * 100).toFixed(1) : 0;
      html += '<div class="bar-row">' +
        '<span class="bar-label">' + esc(t) + '</span>' +
        '<div class="bar-track"><div class="bar-fill" style="width:' + pct + '%"></div></div>' +
        '<span class="bar-value">' + typeCounts[t] + '</span>' +
      '</div>';
    });
    html += '</div>';

    html += '<div class="tab-section-title">Recent Events (' + total + ' total)</div>';
    html += '<table class="tab-table"><thead><tr>' +
      '<th>Time</th><th>Type</th><th>Module</th><th>Stream</th><th>Version</th>' +
    '</tr></thead><tbody>';

    evts.slice(0, 50).forEach(function (e) {
      html += '<tr data-agent="' + esc(e.module_name || '') + '">' +
        '<td class="mono">' + fmtTime(e.timestamp) + '</td>' +
        '<td><span class="status-pill">' + esc(e.type) + '</span></td>' +
        '<td class="mono" style="color:var(--accent)">' + esc(e.module_name || '-') + '</td>' +
        '<td class="mono">' + esc(e.stream_id || '-') + '</td>' +
        '<td class="num">' + (e.version || '-') + '</td>' +
      '</tr>';
    });

    html += '</tbody></table>';
    el.innerHTML = html;

    // Bind row clicks to filter by agent
    var rows = el.querySelectorAll('tbody tr');
    for (var i = 0; i < rows.length; i++) {
      rows[i].addEventListener('click', function () {
        var agentId = this.getAttribute('data-agent');
        if (agentId && agentId !== '-') {
          selectAgent(agentId);
        }
      });
    }
  }

  // ── Workflow Tab ────────────────────────
  function renderWorkflowTab(el, data) {
    var execs = data.executions || data.tasks || [];
    var total = data.total || execs.length;

    var html = '';

    var running = execs.filter(function (e) { return e.status === 'running' || e.status === 'resurrecting'; }).length;
    var completed = execs.filter(function (e) { return e.status === 'completed'; }).length;
    var failed = execs.filter(function (e) { return e.status === 'failed' || e.status === 'dead'; }).length;
    var pending = execs.filter(function (e) { return e.status === 'pending'; }).length;

    html += '<div class="stats-grid">';
    html += '<div class="stat-card"><div class="stat-card-label">Total</div><div class="stat-card-value accent">' + total + '</div></div>';
    html += '<div class="stat-card"><div class="stat-card-label">Running</div><div class="stat-card-value warn">' + running + '</div></div>';
    html += '<div class="stat-card"><div class="stat-card-label">Completed</div><div class="stat-card-value ok">' + completed + '</div></div>';
    html += '<div class="stat-card"><div class="stat-card-label">Failed</div><div class="stat-card-value fail">' + failed + '</div></div>';
    html += '<div class="stat-card"><div class="stat-card-label">Pending</div><div class="stat-card-value info">' + pending + '</div></div>';
    if (data.total_duration != null) {
      html += '<div class="stat-card"><div class="stat-card-label">Total Duration</div><div class="stat-card-value">' + fmtDur(data.total_duration) + '</div></div>';
    }
    html += '</div>';

    if (execs.length) {
      html += '<div class="tab-section-title">Executions</div>';
      html += '<table class="tab-table"><thead><tr>' +
        '<th>Task ID</th><th>Name</th><th>Agent</th><th>Status</th><th>Progress</th><th>Started</th><th>Duration</th>' +
      '</tr></thead><tbody>';
      execs.slice(0, 50).forEach(function (e) {
        var dur = '-';
        if (e.started_at) {
          if (e.completed_at) {
            dur = fmtDur(new Date(e.completed_at) - new Date(e.started_at));
          } else {
            dur = fmtDur(Date.now() - new Date(e.started_at)) + ' (running)';
          }
        }
        var hasProgress = e.progress != null && e.progress >= 0;
        var progressPct = hasProgress ? (e.progress * 100).toFixed(0) : 0;
        html += '<tr data-agent="' + esc(e.agent_id || '') + '">' +
          '<td class="mono">' + esc(e.task_id || e.id || '-') + '</td>' +
          '<td>' + esc(e.name || '-') + '</td>' +
          '<td class="mono" style="color:var(--accent)">' + esc(e.agent_id || '-') + '</td>' +
          '<td><span class="status-pill ' + stClass(e.status) + '">' + esc(e.status || '-') + '</span></td>' +
          '<td class="progress-cell">' +
            (hasProgress
              ? '<div class="table-progress"><div class="table-progress-fill" style="width:' + progressPct + '%"></div></div>' +
                '<span class="progress-pct">' + progressPct + '%</span>'
              : '<span style="color:var(--text-faint)">-</span>') +
          '</td>' +
          '<td class="mono">' + fmtTime(e.started_at) + '</td>' +
          '<td class="num">' + dur + '</td>' +
        '</tr>';
      });
      html += '</tbody></table>';
    }

    if (!execs.length) {
      html += '<div class="empty-state">No workflow data</div>';
    }

    el.innerHTML = html;

    var rows = el.querySelectorAll('tbody tr');
    for (var i = 0; i < rows.length; i++) {
      rows[i].addEventListener('click', function () {
        var agentId = this.getAttribute('data-agent');
        if (agentId && agentId !== '-') {
          selectAgent(agentId);
        }
      });
    }
  }

  // ── MCP Tab ─────────────────────────────
  function renderMCPTab(el, data) {
    var tools = data.tools || [];
    var calls = data.calls || [];

    var html = '';

    html += '<div class="tab-section-title">Registered Tools (' + tools.length + ')</div>';
    if (tools.length) {
      html += '<table class="tab-table"><thead><tr>' +
        '<th>Tool Name</th><th>Description</th>' +
      '</tr></thead><tbody>';
      tools.forEach(function (t) {
        html += '<tr>' +
          '<td class="mono"><span style="color:var(--accent)">' + esc(t.name) + '</span></td>' +
          '<td>' + esc(t.description || '-') + '</td>' +
        '</tr>';
      });
      html += '</tbody></table>';
    } else {
      html += '<div class="empty-state" style="padding:1rem">No tools registered</div>';
    }

    if (calls.length) {
      html += '<div class="tab-section-title">Recent Calls (' + calls.length + ')</div>';
      html += '<table class="tab-table"><thead><tr>' +
        '<th>Time</th><th>Tool</th><th>Agent</th><th>Status</th><th>Duration</th>' +
      '</tr></thead><tbody>';
      calls.slice(0, 25).forEach(function (c) {
        var statusClass = c.status === 'ok' ? 'ok' : 'error';
        html += '<tr data-agent="' + esc(c.agent_id || '') + '">' +
          '<td class="mono">' + fmtTime(c.timestamp) + '</td>' +
          '<td class="mono">' + esc(c.tool_name || '-') + '</td>' +
          '<td class="mono" style="color:var(--accent)">' + esc(c.agent_id || '-') + '</td>' +
          '<td><span class="status-pill ' + statusClass + '">' + esc(c.status || '-') + '</span></td>' +
          '<td class="num">' + fmtDur(c.duration) + '</td>' +
        '</tr>';
      });
      html += '</tbody></table>';
    }

    el.innerHTML = html;

    // Bind row clicks
    var rows = el.querySelectorAll('tbody tr');
    for (var i = 0; i < rows.length; i++) {
      rows[i].addEventListener('click', function () {
        var agentId = this.getAttribute('data-agent');
        if (agentId && agentId !== '-') {
          selectAgent(agentId);
        }
      });
    }
  }

  // ── LLM Tab ─────────────────────────────
  function renderLLMTab(el, data) {
    var calls = data.calls || [];
    var stats = data.stats || {};

    var html = '<div class="stats-grid">';
    html += '<div class="stat-card"><div class="stat-card-label">Total Calls</div><div class="stat-card-value accent">' + fmtNum(stats.total_calls || 0) + '</div></div>';
    html += '<div class="stat-card"><div class="stat-card-label">Total Tokens</div><div class="stat-card-value">' + fmtNum(stats.total_tokens || 0) + '</div></div>';
    html += '<div class="stat-card"><div class="stat-card-label">Input Tokens</div><div class="stat-card-value info">' + fmtNum(stats.total_input_tokens || 0) + '</div></div>';
    html += '<div class="stat-card"><div class="stat-card-label">Output Tokens</div><div class="stat-card-value ok">' + fmtNum(stats.total_output_tokens || 0) + '</div></div>';
    html += '<div class="stat-card"><div class="stat-card-label">Avg Input</div><div class="stat-card-value">' + Math.round(stats.avg_input_tokens || 0) + '</div></div>';
    html += '<div class="stat-card"><div class="stat-card-label">Avg Output</div><div class="stat-card-value">' + Math.round(stats.avg_output_tokens || 0) + '</div></div>';
    if (stats.total_cost != null) {
      html += '<div class="stat-card"><div class="stat-card-label">Total Cost</div><div class="stat-card-value ok">' + fmtCost(stats.total_cost) + '</div></div>';
    }
    html += '</div>';

    // Model distribution
    var modelCounts = {};
    calls.forEach(function (c) {
      var m = c.model_name || 'unknown';
      modelCounts[m] = (modelCounts[m] || 0) + 1;
    });
    var models = Object.keys(modelCounts).sort(function (a, b) { return modelCounts[b] - modelCounts[a]; });
    var maxModel = Math.max(1, Math.max.apply(null, Object.values(modelCounts)));

    if (models.length > 1) {
      html += '<div class="tab-section-title">Model Distribution</div>';
      html += '<div class="bar-chart">';
      models.forEach(function (m, i) {
        var pct = (modelCounts[m] / maxModel * 100).toFixed(1);
        var color = agentColor(i);
        html += '<div class="bar-row">' +
          '<span class="bar-label">' + esc(m) + '</span>' +
          '<div class="bar-track"><div class="bar-fill" style="width:' + pct + '%;background:linear-gradient(90deg,' + color + ',' + color + 'cc)"></div></div>' +
          '<span class="bar-value">' + modelCounts[m] + '</span>' +
        '</div>';
      });
      html += '</div>';
    }

    if (calls.length) {
      html += '<div class="tab-section-title">Call History</div>';
      html += '<table class="tab-table"><thead><tr>' +
        '<th>Time</th><th>Agent</th><th>Model</th><th>In</th><th>Out</th><th>Duration</th><th>Cost</th>' +
      '</tr></thead><tbody>';
      calls.slice(0, 30).forEach(function (c) {
        var callCost = c.estimated_cost != null ? c.estimated_cost : null;
        html += '<tr data-agent="' + esc(c.agent_id || '') + '">' +
          '<td class="mono">' + fmtTime(c.timestamp) + '</td>' +
          '<td class="mono" style="color:var(--accent)">' + esc(c.agent_id || '-') + '</td>' +
          '<td>' + esc(c.model_name || '-') + '</td>' +
          '<td class="num">' + fmtNum(c.input_tokens) + '</td>' +
          '<td class="num">' + fmtNum(c.output_tokens) + '</td>' +
          '<td class="num">' + fmtDur(c.duration) + '</td>' +
          '<td class="num" style="color:var(--ok)">' + (callCost != null ? fmtCost(callCost) : '-') + '</td>' +
        '</tr>';
      });
      html += '</tbody></table>';
    }

    el.innerHTML = html;

    // Bind row clicks
    var rows = el.querySelectorAll('tbody tr');
    for (var i = 0; i < rows.length; i++) {
      rows[i].addEventListener('click', function () {
        var agentId = this.getAttribute('data-agent');
        if (agentId && agentId !== '-') {
          selectAgent(agentId);
        }
      });
    }
  }

  // ── Memory Tab ──────────────────────────
  function renderMemoryTab(el, data) {
    var dist = data.distillations || [];
    var ret = data.retrievals || [];
    var shortTerm = data.short_term || [];
    var longTerm = data.long_term || [];

    var html = '';

    // Stats
    html += '<div class="stats-grid">';
    html += '<div class="stat-card"><div class="stat-card-label">Distillations</div><div class="stat-card-value accent">' + dist.length + '</div></div>';
    html += '<div class="stat-card"><div class="stat-card-label">Retrievals</div><div class="stat-card-value info">' + ret.length + '</div></div>';
    html += '<div class="stat-card"><div class="stat-card-label">Short Term</div><div class="stat-card-value warn">' + shortTerm.length + '</div></div>';
    html += '<div class="stat-card"><div class="stat-card-label">Long Term</div><div class="stat-card-value ok">' + longTerm.length + '</div></div>';
    html += '</div>';

    if (dist.length) {
      html += '<div class="tab-section-title">Distillations (' + dist.length + ')</div>';
      html += '<table class="tab-table"><thead><tr>' +
        '<th>Time</th><th>Agent</th><th>Category</th><th>Content</th><th>Score</th>' +
      '</tr></thead><tbody>';
      dist.slice(0, 20).forEach(function (r) {
        var content = (r.content || '').slice(0, 100);
        html += '<tr data-agent="' + esc(r.agent_id || '') + '">' +
          '<td class="mono">' + fmtTime(r.created_at || r.timestamp) + '</td>' +
          '<td class="mono" style="color:var(--accent)">' + esc(r.agent_id || '-') + '</td>' +
          '<td>' + esc(r.category || '-') + '</td>' +
          '<td>' + esc(content) + (content.length < (r.content || '').length ? '...' : '') + '</td>' +
          '<td class="num">' + (r.relevance != null ? r.relevance.toFixed(2) : '-') + '</td>' +
        '</tr>';
      });
      html += '</tbody></table>';
    }

    if (ret.length) {
      html += '<div class="tab-section-title">Retrievals (' + ret.length + ')</div>';
      html += '<table class="tab-table"><thead><tr>' +
        '<th>Time</th><th>Agent</th><th>Category</th><th>Content</th><th>Score</th>' +
      '</tr></thead><tbody>';
      ret.slice(0, 20).forEach(function (r) {
        var content = (r.content || '').slice(0, 100);
        html += '<tr data-agent="' + esc(r.agent_id || '') + '">' +
          '<td class="mono">' + fmtTime(r.created_at || r.timestamp) + '</td>' +
          '<td class="mono" style="color:var(--accent)">' + esc(r.agent_id || '-') + '</td>' +
          '<td>' + esc(r.category || '-') + '</td>' +
          '<td>' + esc(content) + (content.length < (r.content || '').length ? '...' : '') + '</td>' +
          '<td class="num">' + (r.relevance != null ? r.relevance.toFixed(2) : '-') + '</td>' +
        '</tr>';
      });
      html += '</tbody></table>';
    }

    if (shortTerm.length) {
      html += '<div class="tab-section-title">Short Term Memory (' + shortTerm.length + ')</div>';
      html += '<table class="tab-table"><thead><tr>' +
        '<th>Time</th><th>Agent</th><th>Category</th><th>Content</th><th>Relevance</th>' +
      '</tr></thead><tbody>';
      shortTerm.slice(0, 20).forEach(function (r) {
        var content = (r.content || '').slice(0, 100);
        html += '<tr data-agent="' + esc(r.agent_id || '') + '">' +
          '<td class="mono">' + fmtTime(r.created_at || r.timestamp) + '</td>' +
          '<td class="mono" style="color:var(--accent)">' + esc(r.agent_id || '-') + '</td>' +
          '<td>' + esc(r.category || '-') + '</td>' +
          '<td>' + esc(content) + (content.length < (r.content || '').length ? '...' : '') + '</td>' +
          '<td class="num">' + (r.relevance != null ? r.relevance.toFixed(2) : '-') + '</td>' +
        '</tr>';
      });
      html += '</tbody></table>';
    }

    if (longTerm.length) {
      html += '<div class="tab-section-title">Long Term Memory (' + longTerm.length + ')</div>';
      html += '<table class="tab-table"><thead><tr>' +
        '<th>Time</th><th>Agent</th><th>Category</th><th>Content</th><th>Relevance</th>' +
      '</tr></thead><tbody>';
      longTerm.slice(0, 20).forEach(function (r) {
        var content = (r.content || '').slice(0, 100);
        html += '<tr data-agent="' + esc(r.agent_id || '') + '">' +
          '<td class="mono">' + fmtTime(r.created_at || r.timestamp) + '</td>' +
          '<td class="mono" style="color:var(--accent)">' + esc(r.agent_id || '-') + '</td>' +
          '<td>' + esc(r.category || '-') + '</td>' +
          '<td>' + esc(content) + (content.length < (r.content || '').length ? '...' : '') + '</td>' +
          '<td class="num">' + (r.relevance != null ? r.relevance.toFixed(2) : '-') + '</td>' +
        '</tr>';
      });
      html += '</tbody></table>';
    }

    if (!dist.length && !ret.length && !shortTerm.length && !longTerm.length) {
      html = '<div class="empty-state">No memory data</div>';
    }

    el.innerHTML = html;

    // Bind row clicks
    var rows = el.querySelectorAll('tbody tr');
    for (var i = 0; i < rows.length; i++) {
      rows[i].addEventListener('click', function () {
        var agentId = this.getAttribute('data-agent');
        if (agentId && agentId !== '-') {
          selectAgent(agentId);
        }
      });
    }
  }

  // ── Evolution Tab ───────────────────────
  function renderEvolutionTab(el, data) {
    var genomes = data.genomes || [];
    var muts = data.mutations || [];

    var html = '';

    // Stats
    html += '<div class="stats-grid">';
    html += '<div class="stat-card"><div class="stat-card-label">Genomes</div><div class="stat-card-value accent">' + genomes.length + '</div></div>';
    html += '<div class="stat-card"><div class="stat-card-label">Mutations</div><div class="stat-card-value warn">' + muts.length + '</div></div>';
    html += '</div>';

    if (genomes.length) {
      html += '<div class="tab-section-title">Genomes</div>';
      html += '<table class="tab-table"><thead><tr>' +
        '<th>Agent</th><th>Generation</th><th>Parent</th><th>Fitness</th><th>Started</th>' +
      '</tr></thead><tbody>';
      genomes.slice(0, 25).forEach(function (g) {
        html += '<tr data-agent="' + esc(g.agent_id || g.id || '') + '">' +
          '<td class="mono" style="color:var(--accent)">' + esc(g.agent_id || g.id || '-') + '</td>' +
          '<td class="num">G' + (g.generation || 0) + '</td>' +
          '<td class="mono">' + esc(g.parent_id || '-') + '</td>' +
          '<td class="num">' + (g.fitness != null ? g.fitness.toFixed(2) : '-') + '</td>' +
          '<td class="mono">' + fmtTime(g.started_at) + '</td>' +
        '</tr>';
      });
      html += '</tbody></table>';
    }

    if (muts.length) {
      html += '<div class="tab-section-title">Mutations (' + muts.length + ')</div>';
      html += '<table class="tab-table"><thead><tr>' +
        '<th>Time</th><th>Agent</th><th>Type</th><th>Description</th>' +
      '</tr></thead><tbody>';
      muts.slice(0, 20).forEach(function (m) {
        html += '<tr data-agent="' + esc(m.agent_id || '') + '">' +
          '<td class="mono">' + fmtTime(m.timestamp) + '</td>' +
          '<td class="mono" style="color:var(--accent)">' + esc(m.agent_id || '-') + '</td>' +
          '<td><span class="status-pill">' + esc(m.type || '-') + '</span></td>' +
          '<td>' + esc(m.description || '-') + '</td>' +
        '</tr>';
      });
      html += '</tbody></table>';
    }

    if (!genomes.length && !muts.length) {
      html = '<div class="empty-state">No evolution data</div>';
    }

    el.innerHTML = html;

    // Bind row clicks
    var rows = el.querySelectorAll('tbody tr');
    for (var i = 0; i < rows.length; i++) {
      rows[i].addEventListener('click', function () {
        var agentId = this.getAttribute('data-agent');
        if (agentId && agentId !== '-') {
          selectAgent(agentId);
        }
      });
    }
  }

  // ── Arena Tab ───────────────────────────
  function renderArenaTab(el, data) {
    var fi = data.fault_injections || [];
    var st = data.survival_tests || [];

    var html = '';

    // Stats
    var survivedCount = fi.filter(function (f) { return f.survived; }).length;
    var passCount = st.filter(function (t) { return t.passed; }).length;

    html += '<div class="stats-grid">';
    html += '<div class="stat-card"><div class="stat-card-label">Fault Injections</div><div class="stat-card-value warn">' + fi.length + '</div></div>';
    html += '<div class="stat-card"><div class="stat-card-label">Survived</div><div class="stat-card-value ok">' + survivedCount + '</div></div>';
    html += '<div class="stat-card"><div class="stat-card-label">Survival Tests</div><div class="stat-card-value info">' + st.length + '</div></div>';
    html += '<div class="stat-card"><div class="stat-card-label">Passed</div><div class="stat-card-value ok">' + passCount + '</div></div>';
    html += '</div>';

    if (fi.length) {
      html += '<div class="tab-section-title">Fault Injections</div>';
      html += '<table class="tab-table"><thead><tr>' +
        '<th>Time</th><th>Agent</th><th>Type</th><th>Survived</th><th>Duration</th>' +
      '</tr></thead><tbody>';
      fi.slice(0, 20).forEach(function (f) {
        var dur = f.completed_at
          ? fmtDur(new Date(f.completed_at) - new Date(f.triggered_at))
          : 'running';
        html += '<tr data-agent="' + esc(f.agent_id || '') + '">' +
          '<td class="mono">' + fmtTime(f.triggered_at) + '</td>' +
          '<td class="mono" style="color:var(--accent)">' + esc(f.agent_id || '-') + '</td>' +
          '<td>' + esc(f.type || '-') + '</td>' +
          '<td style="color:' + (f.survived ? 'var(--ok)' : 'var(--fail)') + ';font-weight:600">' +
            (f.survived ? '✓ YES' : '✗ NO') + '</td>' +
          '<td class="num">' + dur + '</td>' +
        '</tr>';
      });
      html += '</tbody></table>';
    }

    if (st.length) {
      html += '<div class="tab-section-title">Survival Tests</div>';
      html += '<table class="tab-table"><thead><tr>' +
        '<th>Time</th><th>Agent</th><th>Test Type</th><th>Result</th>' +
      '</tr></thead><tbody>';
      st.slice(0, 20).forEach(function (t) {
        html += '<tr data-agent="' + esc(t.agent_id || '') + '">' +
          '<td class="mono">' + fmtTime(t.timestamp) + '</td>' +
          '<td class="mono" style="color:var(--accent)">' + esc(t.agent_id || '-') + '</td>' +
          '<td>' + esc(t.test_type || '-') + '</td>' +
          '<td style="color:' + (t.passed ? 'var(--ok)' : 'var(--fail)') + ';font-weight:600">' +
            (t.passed ? '✓ PASS' : '✗ FAIL') + '</td>' +
        '</tr>';
      });
      html += '</tbody></table>';
    }

    if (!fi.length && !st.length) {
      html = '<div class="empty-state">No arena data</div>';
    }

    el.innerHTML = html;

    // Bind row clicks
    var rows = el.querySelectorAll('tbody tr');
    for (var i = 0; i < rows.length; i++) {
      rows[i].addEventListener('click', function () {
        var agentId = this.getAttribute('data-agent');
        if (agentId && agentId !== '-') {
          selectAgent(agentId);
        }
      });
    }
  }

  // ════════════════════════════════════════
  // AGENT DETAIL DRAWER
  // ════════════════════════════════════════
  function selectAgent(id) {
    State.selectedAgentId = State.selectedAgentId === id ? null : id;
    renderAgentList();
    renderDAG();

    if (State.selectedAgentId) {
      openDetailDrawer(State.selectedAgentId);
    } else {
      closeDetailDrawer();
    }
  }

  function bindDrawerTabs() {
    var tabs = document.querySelectorAll('.drawer-tab');
    for (var i = 0; i < tabs.length; i++) {
      tabs[i].addEventListener('click', function () {
        var tabName = this.getAttribute('data-drawer-tab');
        State.activeDrawerTab = tabName;
        var allTabs = document.querySelectorAll('.drawer-tab');
        for (var j = 0; j < allTabs.length; j++) {
          allTabs[j].classList.toggle('active', allTabs[j].getAttribute('data-drawer-tab') === tabName);
        }
        var contents = document.querySelectorAll('.drawer-tab-content');
        for (var k = 0; k < contents.length; k++) {
          contents[k].classList.toggle('active', contents[k].id === 'drawer-tab-' + tabName);
        }
      });
    }
  }

  function bindDetailClose() {
    var btn = document.getElementById('detail-close');
    if (btn) btn.addEventListener('click', closeDetailDrawer);
    var overlay = document.getElementById('detail-overlay');
    if (overlay) overlay.addEventListener('click', closeDetailDrawer);
  }

  function openDetailDrawer(id) {
    var panel = document.getElementById('detail-panel');
    var overlay = document.getElementById('detail-overlay');
    if (!panel) return;

    get('/agents/' + id).then(function (detail) {
      if (!detail || !detail.agent) return;
      State.agentDetail = detail;
      renderDetailDrawer(detail);
      panel.className = 'detail-drawer open';
      overlay.className = 'detail-overlay open';
    });
  }

  function refreshAgentDetail() {
    if (!State.selectedAgentId) return;
    get('/agents/' + State.selectedAgentId).then(function (detail) {
      if (detail && detail.agent) {
        State.agentDetail = detail;
        renderDetailDrawer(detail);
      }
    });
  }

  function closeDetailDrawer() {
    var panel = document.getElementById('detail-panel');
    var overlay = document.getElementById('detail-overlay');
    if (panel) panel.className = 'detail-drawer';
    if (overlay) overlay.className = 'detail-overlay';
    State.selectedAgentId = null;
    State.agentDetail = null;
    renderAgentList();
    renderDAG();
  }

  function renderDetailDrawer(detail) {
    var a = detail.agent || {};
    var tasks = detail.tasks || [];
    var rel = detail.relationships || {};
    var evts = detail.events || {};

    // Header
    var titleEl = document.getElementById('detail-title');
    var subtitleEl = document.getElementById('detail-subtitle');
    var statusDot = document.querySelector('#detail-status-indicator .si-dot');
    var statusText = document.querySelector('#detail-status-indicator .si-text');

    if (titleEl) titleEl.textContent = a.name || a.id;
    if (subtitleEl) subtitleEl.textContent = a.id;
    if (statusDot) {
      statusDot.className = 'si-dot ' + stClass(a.status);
    }
    if (statusText) {
      statusText.textContent = a.status || 'unknown';
      statusText.style.color = stColor(a.status);
    }

    // Get cost
    var costByAgent = (State.snap && State.snap.cost && State.snap.cost.by_agent) || {};
    var ac = costByAgent[a.id] || {};

    // ── Overview Tab ─────────────────────
    var overviewHTML = '';

    overviewHTML += '<div class="drawer-section">';
    overviewHTML += '<div class="drawer-section-title">Identity</div>';
    overviewHTML += '<div class="kv-grid">';
    overviewHTML += kvItem('Agent ID', a.id, 'mono full');
    overviewHTML += kvItem('Name', a.name || '-');
    overviewHTML += kvItem('Status', a.status || '-', (a.status === 'completed' ? 'ok' : a.status === 'failed' || a.status === 'dead' ? 'fail' : a.status === 'running' || a.status === 'resurrecting' ? 'warn' : 'info'));
    overviewHTML += kvItem('Role', a.role || '-', 'accent');
    if (a.model_name) overviewHTML += kvItem('Model', a.model_name);
    if (a.task_id) overviewHTML += kvItem('Task ID', a.task_id, 'mono full');
    if (a.source) overviewHTML += kvItem('Source', a.source);
    if (a.agent_type) overviewHTML += kvItem('Agent Type', a.agent_type);
    overviewHTML += kvItem('Started', fmtDateTime(a.started_at), 'mono');
    if (a.updated_at) overviewHTML += kvItem('Updated', fmtTime(a.updated_at), 'mono');
    overviewHTML += '</div></div>';

    // Cost
    if (ac.estimated_cost || ac.total_tokens || ac.call_count) {
      overviewHTML += '<div class="drawer-section">';
      overviewHTML += '<div class="drawer-section-title">Cost Summary</div>';
      overviewHTML += '<div class="kv-grid">';
      if (ac.estimated_cost != null) overviewHTML += kvItem('Estimated Cost', fmtCost(ac.estimated_cost), 'ok');
      if (ac.total_tokens != null) overviewHTML += kvItem('Total Tokens', fmtNum(ac.total_tokens));
      if (ac.input_tokens != null) overviewHTML += kvItem('Input Tokens', fmtNum(ac.input_tokens));
      if (ac.output_tokens != null) overviewHTML += kvItem('Output Tokens', fmtNum(ac.output_tokens));
      if (ac.call_count != null) overviewHTML += kvItem('LLM Calls', ac.call_count, 'info');
      if (ac.currency) overviewHTML += kvItem('Currency', ac.currency);
      overviewHTML += '</div></div>';
    }

    // Quick stats
    overviewHTML += '<div class="drawer-section">';
    overviewHTML += '<div class="drawer-section-title">Quick Stats</div>';
    overviewHTML += '<div class="kv-grid">';
    overviewHTML += kvItem('Tasks Assigned', tasks.length);
    var runningTasks = tasks.filter(function (t) { return t.status === 'running' || t.status === 'resurrecting'; }).length;
    var completedTasks = tasks.filter(function (t) { return t.status === 'completed'; }).length;
    var failedTasks = tasks.filter(function (t) { return t.status === 'failed' || t.status === 'dead'; }).length;
    if (runningTasks > 0) overviewHTML += kvItem('Running Tasks', runningTasks, 'warn');
    if (completedTasks > 0) overviewHTML += kvItem('Completed Tasks', completedTasks, 'ok');
    if (failedTasks > 0) overviewHTML += kvItem('Failed Tasks', failedTasks, 'fail');
    overviewHTML += kvItem('Events', evts.total || 0);
    if (a.started_at) {
      var uptime = Date.now() - new Date(a.started_at);
      if (uptime > 0) {
        var upStr = '';
        var hours = Math.floor(uptime / 3600000);
        var mins = Math.floor((uptime % 3600000) / 60000);
        var secs = Math.floor((uptime % 60000) / 1000);
        if (hours > 0) upStr = hours + 'h ' + mins + 'm';
        else if (mins > 0) upStr = mins + 'm ' + secs + 's';
        else upStr = secs + 's';
        overviewHTML += kvItem('Uptime', upStr, 'ok');
      }
    }
    if (rel.parent) overviewHTML += kvItem('Parent', '1', 'accent');
    if (rel.children && rel.children.length) overviewHTML += kvItem('Children', rel.children.length, 'ok');
    if (rel.peers && rel.peers.length) overviewHTML += kvItem('Peers', rel.peers.length, 'info');
    overviewHTML += '</div></div>';

    // Task progress summary
    if (tasks.length) {
      var totalProgress = 0;
      var tasksWithProgress = 0;
      tasks.forEach(function (t) {
        if (t.progress != null && t.progress >= 0) {
          totalProgress += t.progress;
          tasksWithProgress++;
        }
      });
      if (tasksWithProgress > 0) {
        var avgProgress = totalProgress / tasksWithProgress;
        overviewHTML += '<div class="drawer-section">';
        overviewHTML += '<div class="drawer-section-title">Overall Progress</div>';
        overviewHTML += '<div class="task-progress-bar" style="margin-bottom:0.5rem"><div class="task-progress-fill" style="width:' + (avgProgress * 100).toFixed(0) + '%"></div></div>';
        overviewHTML += '<div style="display:flex;justify-content:space-between;font-size:0.65rem;color:var(--text-muted)">';
        overviewHTML += '<span>Avg: ' + (avgProgress * 100).toFixed(1) + '%</span>';
        overviewHTML += '<span>' + tasksWithProgress + ' / ' + tasks.length + ' tasks</span>';
        overviewHTML += '</div></div>';
      }
    }

    // Tags
    if (a.tags && a.tags.length) {
      overviewHTML += '<div class="drawer-section">';
      overviewHTML += '<div class="drawer-section-title">Tags</div>';
      overviewHTML += '<div style="display:flex;flex-wrap:wrap;gap:0.3rem">';
      a.tags.forEach(function (t) {
        overviewHTML += '<span class="status-pill" style="background:var(--accent-dim);color:var(--accent)">' + esc(t) + '</span>';
      });
      overviewHTML += '</div></div>';
    }

    // Metadata
    if (a.metadata && Object.keys(a.metadata).length) {
      overviewHTML += '<div class="drawer-section">';
      overviewHTML += '<div class="drawer-section-title">Metadata</div>';
      overviewHTML += '<div class="kv-grid">';
      Object.keys(a.metadata).forEach(function (k) {
        var val = a.metadata[k];
        if (typeof val === 'object') val = JSON.stringify(val);
        overviewHTML += kvItem(k, String(val), 'mono');
      });
      overviewHTML += '</div></div>';
    }

    document.getElementById('drawer-tab-overview').innerHTML = overviewHTML;

    // ── Tasks Tab ────────────────────────
    var tasksHTML = '';
    if (tasks.length) {
      tasks.forEach(function (t) {
        var sc = stClass(t.status);
        var dur = '-';
        if (t.started_at) {
          if (t.completed_at) {
            dur = fmtDur(new Date(t.completed_at) - new Date(t.started_at));
          } else {
            dur = fmtDur(Date.now() - new Date(t.started_at)) + ' (running)';
          }
        }
        tasksHTML += '<div class="task-item-lg">' +
          '<span class="task-status-lg ' + sc + '"></span>' +
          '<div class="task-info-lg">' +
            '<div class="task-name-lg">' + esc(t.name || t.id) + '</div>' +
            '<div class="task-desc-lg">' + esc(t.id) + ' · ' + fmtTime(t.started_at) + ' · ' + dur + '</div>' +
            (t.progress != null ? '<div class="task-progress-bar"><div class="task-progress-fill" style="width:' + (t.progress * 100).toFixed(0) + '%"></div></div>' : '') +
          '</div>' +
          '<span class="status-pill ' + sc + '">' + esc(t.status || '-') + '</span>' +
        '</div>';
      });
    } else {
      tasksHTML = '<div class="empty-state">No tasks assigned to this agent</div>';
    }
    document.getElementById('drawer-tab-tasks').innerHTML = tasksHTML;

    // ── Timeline Tab ─────────────────────
    var timelineHTML = '';
    var timelineItems = [];

    // Start event
    if (a.started_at) {
      timelineItems.push({
        time: a.started_at,
        type: 'Agent Started',
        desc: 'Agent ' + (a.name || a.id) + ' was initialized',
        status: 'running'
      });
    }

    // Task events
    tasks.forEach(function (t) {
      timelineItems.push({
        time: t.started_at,
        type: 'Task: ' + (t.name || t.id),
        desc: 'Task ' + (t.status || 'started'),
        status: t.status
      });
    });

    // Sort
    timelineItems.sort(function (x, y) {
      return new Date(x.time || 0) - new Date(y.time || 0);
    });

    if (timelineItems.length) {
      timelineHTML += '<div class="timeline-list">';
      timelineItems.forEach(function (item) {
        var sc = stClass(item.status);
        timelineHTML += '<div class="timeline-item ' + sc + '">' +
          '<div class="timeline-time">' + fmtDateTime(item.time) + '</div>' +
          '<div class="timeline-type">' + esc(item.type) + '</div>' +
          '<div class="timeline-desc">' + esc(item.desc || '') + '</div>' +
        '</div>';
      });
      timelineHTML += '</div>';
    } else {
      timelineHTML = '<div class="empty-state">No timeline data</div>';
    }
    document.getElementById('drawer-tab-timeline').innerHTML = timelineHTML;

    // ── Cost Tab ─────────────────────────
    var costHTML = '';
    if (ac.estimated_cost != null || ac.total_tokens != null) {
      costHTML += '<div class="drawer-section">';
      costHTML += '<div class="drawer-section-title">Cost Breakdown</div>';
      costHTML += '<div class="kv-grid">';
      costHTML += kvItem('Total Estimated Cost', fmtCost(ac.estimated_cost || 0), 'ok full');
      costHTML += kvItem('Input Tokens', fmtNum(ac.input_tokens || 0), 'info');
      costHTML += kvItem('Output Tokens', fmtNum(ac.output_tokens || 0), 'info');
      costHTML += kvItem('Total Tokens', fmtNum(ac.total_tokens || 0), 'accent');
      costHTML += kvItem('LLM Call Count', ac.call_count || 0);
      costHTML += kvItem('Currency', ac.currency || 'USD');
      costHTML += '</div></div>';

      // Token ratio bar
      if (ac.input_tokens || ac.output_tokens) {
        var total = (ac.input_tokens || 0) + (ac.output_tokens || 0);
        var inPct = total > 0 ? ((ac.input_tokens || 0) / total * 100).toFixed(1) : 0;
        var outPct = total > 0 ? ((ac.output_tokens || 0) / total * 100).toFixed(1) : 0;
        costHTML += '<div class="drawer-section">';
        costHTML += '<div class="drawer-section-title">Token Distribution</div>';
        costHTML += '<div style="display:flex;height:12px;border-radius:6px;overflow:hidden;margin-bottom:0.5rem">' +
          '<div style="width:' + inPct + '%;background:var(--info);position:relative">' +
            '<div style="position:absolute;top:0;left:0;right:0;height:50%;background:linear-gradient(180deg,rgba(255,255,255,0.15),transparent)"></div>' +
          '</div>' +
          '<div style="width:' + outPct + '%;background:var(--ok);position:relative">' +
            '<div style="position:absolute;top:0;left:0;right:0;height:50%;background:linear-gradient(180deg,rgba(255,255,255,0.15),transparent)"></div>' +
          '</div>' +
        '</div>';
        costHTML += '<div style="display:flex;justify-content:space-between;font-size:0.65rem">' +
          '<span style="color:var(--info)">Input: ' + fmtNum(ac.input_tokens || 0) + ' (' + inPct + '%)</span>' +
          '<span style="color:var(--ok)">Output: ' + fmtNum(ac.output_tokens || 0) + ' (' + outPct + '%)</span>' +
        '</div></div>';
      }

      // Event type breakdown
      if (evts.by_type && Object.keys(evts.by_type).length) {
        var types = Object.keys(evts.by_type);
        var maxEvt = Math.max.apply(null, Object.values(evts.by_type));
        costHTML += '<div class="drawer-section">';
        costHTML += '<div class="drawer-section-title">Event Types (' + (evts.total || 0) + ' total)</div>';
        costHTML += '<div class="bar-chart">';
        types.forEach(function (t, i) {
          var pct = maxEvt > 0 ? (evts.by_type[t] / maxEvt * 100).toFixed(1) : 0;
          var color = agentColor(i);
          costHTML += '<div class="bar-row">' +
            '<span class="bar-label" style="min-width:100px">' + esc(t) + '</span>' +
            '<div class="bar-track"><div class="bar-fill" style="width:' + pct + '%;background:linear-gradient(90deg,' + color + ',' + color + 'cc)"></div></div>' +
            '<span class="bar-value">' + evts.by_type[t] + '</span>' +
          '</div>';
        });
        costHTML += '</div></div>';
      }
    } else {
      costHTML = '<div class="empty-state">No cost data for this agent</div>';
    }
    document.getElementById('drawer-tab-cost').innerHTML = costHTML;

    // ── Trace Tab ───────────────────────
    var traceHTML = '';
    var traceSpans = detail.trace_spans || [];

    if (traceSpans.length) {
      traceHTML += '<div class="drawer-section">';
      traceHTML += '<div class="drawer-section-title">Trace Spans (' + traceSpans.length + ')</div>';
      traceHTML += '<div class="trace-timeline">';

      var sortedSpans = traceSpans.slice().sort(function (x, y) {
        return new Date(x.start_time || 0) - new Date(y.start_time || 0);
      });

      var spanStartTime = sortedSpans.length ? new Date(sortedSpans[0].start_time).getTime() : 0;
      var spanEndTime = sortedSpans.length ? new Date(sortedSpans[sortedSpans.length - 1].end_time || sortedSpans[sortedSpans.length - 1].start_time).getTime() : 0;
      var totalDuration = Math.max(1, spanEndTime - spanStartTime);

      sortedSpans.forEach(function (span, i) {
        var start = new Date(span.start_time || 0).getTime();
        var end = span.end_time ? new Date(span.end_time).getTime() : Date.now();
        var dur = end - start;
        var offset = totalDuration > 0 ? ((start - spanStartTime) / totalDuration * 100).toFixed(1) : 0;
        var width = totalDuration > 0 ? Math.max(2, (dur / totalDuration * 100)).toFixed(1) : 100;
        var color = agentColor(i);

        traceHTML += '<div class="trace-span-item">';
        traceHTML += '<div class="trace-span-header">';
        traceHTML += '<span class="trace-span-name">' + esc(span.name || span.span_id || 'span') + '</span>';
        traceHTML += '<span class="trace-span-dur mono">' + fmtDur(dur * 1000000) + '</span>';
        traceHTML += '</div>';
        traceHTML += '<div class="trace-span-bar">';
        traceHTML += '<div class="trace-span-fill" style="left:' + offset + '%;width:' + width + '%;background:' + color + ';box-shadow:0 0 8px ' + color + '60"></div>';
        traceHTML += '</div>';
        traceHTML += '<div class="trace-span-meta">';
        traceHTML += '<span class="mono" style="color:var(--text-muted)">' + fmtTime(span.start_time) + '</span>';
        if (span.status) traceHTML += '<span class="status-pill ' + stClass(span.status) + '">' + esc(span.status) + '</span>';
        if (span.agent_id) traceHTML += '<span style="color:var(--accent)">' + esc(span.agent_id) + '</span>';
        traceHTML += '</div>';
        if (span.attributes && Object.keys(span.attributes).length) {
          traceHTML += '<div class="trace-span-attrs">';
          Object.keys(span.attributes).slice(0, 5).forEach(function (k) {
            var v = span.attributes[k];
            if (typeof v === 'object') v = JSON.stringify(v);
            traceHTML += '<div class="kv-item small"><span class="kv-key">' + esc(k) + '</span><span class="kv-val mono">' + esc(String(v).slice(0, 50)) + '</span></div>';
          });
          traceHTML += '</div>';
        }
        traceHTML += '</div>';
      });

      traceHTML += '</div></div>';
    } else if (a.task_id) {
      traceHTML += '<div class="drawer-section">';
      traceHTML += '<div class="drawer-section-title">Trace Info</div>';
      traceHTML += '<div class="kv-grid">';
      traceHTML += kvItem('Task ID', a.task_id, 'mono full');
      traceHTML += kvItem('Trace ID', a.task_id ? a.task_id + '-trace' : '-', 'mono');
      traceHTML += kvItem('Status', a.status || '-', a.status === 'completed' ? 'ok' : a.status === 'failed' || a.status === 'dead' ? 'fail' : a.status === 'running' ? 'warn' : 'info');
      traceHTML += kvItem('Started', fmtDateTime(a.started_at), 'mono');
      if (a.updated_at) traceHTML += kvItem('Last Update', fmtTime(a.updated_at), 'mono');
      traceHTML += '</div></div>';
      traceHTML += '<div class="empty-state" style="margin-top:1rem">No detailed trace spans available</div>';
    } else {
      traceHTML = '<div class="empty-state">No trace data for this agent</div>';
    }

    document.getElementById('drawer-tab-trace').innerHTML = traceHTML;

    // ── Relations Tab ────────────────────
    var relHTML = '';

    if (rel.parent) {
      relHTML += '<div class="relation-group">';
      relHTML += '<div class="relation-group-title">↑ Upstream (Parent)</div>';
      relHTML += relationCard(rel.parent, 'upstream');
      relHTML += '</div>';
    }

    if (rel.children && rel.children.length) {
      relHTML += '<div class="relation-group">';
      relHTML += '<div class="relation-group-title">↓ Downstream (' + rel.children.length + ' children)</div>';
      rel.children.forEach(function (c) {
        relHTML += relationCard(c, 'downstream');
      });
      relHTML += '</div>';
    }

    if (rel.peers && rel.peers.length) {
      relHTML += '<div class="relation-group">';
      relHTML += '<div class="relation-group-title">↔ Peers (' + rel.peers.length + ')</div>';
      rel.peers.forEach(function (p) {
        relHTML += relationCard(p, 'peer');
      });
      relHTML += '</div>';
    }

    if (!rel.parent && (!rel.children || !rel.children.length) && (!rel.peers || !rel.peers.length)) {
      relHTML = '<div class="empty-state">No relationships detected for this agent</div>';
    }

    document.getElementById('drawer-tab-relations').innerHTML = relHTML;
  }

  function kvItem(key, val, cls) {
    cls = cls || '';
    return '<div class="kv-item ' + (cls.indexOf('full') >= 0 ? 'full' : '') + '">' +
      '<span class="kv-key">' + esc(key) + '</span>' +
      '<span class="kv-val ' + cls.replace('full', '').trim() + '">' + esc(String(val)) + '</span>' +
    '</div>';
  }

  function relationCard(agent, type) {
    var sc = stClass(agent.status);
    return '<div class="relation-card ' + type + '" data-id="' + esc(agent.id) + '" onclick="window._selectAgent(\'' + esc(agent.id) + '\')">' +
      '<span class="agent-status-dot ' + sc + '"></span>' +
      '<div class="relation-card-info">' +
        '<div class="relation-card-name">' + esc(agent.name || agent.id) + '</div>' +
        '<div class="relation-card-meta">' + esc(agent.id) + ' · ' + esc(agent.role || agent.status || '-') + '</div>' +
      '</div>' +
    '</div>';
  }

  // ════════════════════════════════════════
  // ACTIONS
  // ════════════════════════════════════════
  window._killCurrent = function () {
    if (!State.selectedAgentId) return;
    post('/agents/' + State.selectedAgentId + '/kill').then(function (r) {
      if (r && r.success) {
        showToast('Agent killed successfully', 'success');
      } else {
        showToast('Kill action: ' + (r ? r.message || 'completed' : 'failed'), 'warning');
      }
      tick();
    });
  };

  window._retryCurrent = function () {
    if (!State.selectedAgentId) return;
    post('/agents/' + State.selectedAgentId + '/retry').then(function (r) {
      if (r && r.success) {
        showToast('Retry initiated', 'success');
      } else {
        showToast('Retry action: ' + (r ? r.message || 'completed' : 'failed'), 'warning');
      }
      tick();
    });
  };

  window._resumeCurrent = function () {
    if (!State.selectedAgentId) return;
    post('/agents/' + State.selectedAgentId + '/resume').then(function (r) {
      if (r && r.success) {
        showToast('Agent resumed', 'success');
      } else {
        showToast('Resume action: ' + (r ? r.message || 'completed' : 'failed'), 'warning');
      }
      tick();
    });
  };

  window._selectAgent = function (id) {
    selectAgent(id);
  };

  // ── Boot ────────────────────────────────
  document.addEventListener('DOMContentLoaded', init);
})();
