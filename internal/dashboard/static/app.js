// GoAgentX Dashboard - Single Page Application
(function() {
    'use strict';

    const API_BASE = '/api/dashboard';
    let ws = null;
    let currentView = 'overview';
    let reconnectTimer = null;

    // --- Navigation ---
    document.querySelectorAll('.sidebar a[data-view]').forEach(link => {
        link.addEventListener('click', (e) => {
            e.preventDefault();
            const view = link.dataset.view;
            switchView(view);
        });
    });

    function switchView(view) {
        document.querySelectorAll('.sidebar a').forEach(a => a.classList.remove('active'));
        document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));

        const link = document.querySelector(`[data-view="${view}"]`);
        const viewEl = document.getElementById(`view-${view}`);
        if (link) link.classList.add('active');
        if (viewEl) viewEl.classList.add('active');

        currentView = view;
        loadView(view);
    }

    // --- API Helpers ---
    async function fetchJSON(path, options) {
        try {
            const resp = await fetch(API_BASE + path, options);
            if (!resp.ok) {
                const err = await resp.json().catch(() => ({ error: resp.statusText }));
                throw new Error(err.error || resp.statusText);
            }
            return await resp.json();
        } catch (err) {
            console.error(`API error: ${path}`, err);
            return null;
        }
    }

    // --- View Loaders ---
    async function loadView(view) {
        switch (view) {
            case 'overview':  return loadOverview();
            case 'agents':    return loadAgents();
            case 'workflows': return loadWorkflows();
            case 'memory':    return loadMemory();
            case 'events':    return loadEvents();
            case 'mcp':       return loadMCP();
        }
    }

    async function loadOverview() {
        const data = await fetchJSON('/overview');
        if (!data) return;

        const el = document.getElementById('view-overview');
        el.innerHTML = `
            <h3>System Overview</h3>
            <div class="card-grid">
                <div class="card stat-card">
                    <div class="value">${data.agent_count || 0}</div>
                    <div class="label">Active Agents</div>
                </div>
                <div class="card stat-card">
                    <div class="value">${data.runtime_stats?.total_restarts || 0}</div>
                    <div class="label">Total Restarts</div>
                </div>
                <div class="card stat-card">
                    <div class="value">${data.uptime || '0s'}</div>
                    <div class="label">Uptime</div>
                </div>
                <div class="card stat-card">
                    <div class="value">${data.mcp_status?.total_tools || 0}</div>
                    <div class="label">MCP Tools</div>
                </div>
            </div>
            <div class="card">
                <h3>Runtime Stats</h3>
                <table>
                    <tr><td>Active Agents</td><td>${data.runtime_stats?.active_agents || 0}</td></tr>
                    <tr><td>Total Restarts</td><td>${data.runtime_stats?.total_restarts || 0}</td></tr>
                    <tr><td>Uptime (seconds)</td><td>${data.runtime_stats?.uptime_seconds || 0}</td></tr>
                </table>
            </div>
            ${data.mcp_status ? `
            <div class="card">
                <h3>MCP Status</h3>
                <table>
                    <tr><td>Servers</td><td>${data.mcp_status.server_count}</td></tr>
                    <tr><td>Connected</td><td>${data.mcp_status.connected_count}</td></tr>
                    <tr><td>Total Tools</td><td>${data.mcp_status.total_tools}</td></tr>
                </table>
            </div>` : ''}
        `;
    }

    async function loadAgents() {
        const agents = await fetchJSON('/agents');
        if (!agents) return;

        const el = document.getElementById('view-agents');
        if (agents.length === 0) {
            el.innerHTML = '<div class="empty-state">No agents registered</div>';
            return;
        }

        el.innerHTML = `
            <h3>Agents</h3>
            <div class="card">
                <table>
                    <thead>
                        <tr><th>ID</th><th>Type</th><th>Status</th><th>Restarts</th></tr>
                    </thead>
                    <tbody>
                        ${agents.map(a => `
                            <tr>
                                <td>${escapeHtml(a.id)}</td>
                                <td>${escapeHtml(a.type)}</td>
                                <td><span class="badge ${statusBadge(a.status)}">${escapeHtml(a.status)}</span></td>
                                <td>${a.restarts}</td>
                            </tr>
                        `).join('')}
                    </tbody>
                </table>
            </div>
        `;
    }

    async function loadWorkflows() {
        const workflows = await fetchJSON('/workflows');
        const el = document.getElementById('view-workflows');

        if (!workflows || workflows.length === 0) {
            el.innerHTML = '<div class="empty-state">No workflow executions</div>';
            return;
        }

        el.innerHTML = `
            <h3>Workflow Executions</h3>
            <div class="card">
                <table>
                    <thead>
                        <tr><th>ID</th><th>Workflow</th><th>Status</th><th>Started</th><th>Duration</th></tr>
                    </thead>
                    <tbody>
                        ${workflows.map(w => `
                            <tr>
                                <td>${escapeHtml(w.id)}</td>
                                <td>${escapeHtml(w.workflow_id)}</td>
                                <td><span class="badge ${statusBadge(w.status)}">${escapeHtml(w.status)}</span></td>
                                <td>${formatTime(w.started_at)}</td>
                                <td>${escapeHtml(w.duration || '-')}</td>
                            </tr>
                        `).join('')}
                    </tbody>
                </table>
            </div>
        `;
    }

    async function loadMemory() {
        const el = document.getElementById('view-memory');
        el.innerHTML = `
            <h3>Memory Explorer</h3>
            <div class="search-box">
                <input type="text" id="memory-search-input" placeholder="Search distilled memories...">
                <button id="memory-search-btn">Search</button>
            </div>
            <div id="memory-results"></div>
        `;

        document.getElementById('memory-search-btn').addEventListener('click', searchMemory);
        document.getElementById('memory-search-input').addEventListener('keypress', (e) => {
            if (e.key === 'Enter') searchMemory();
        });
    }

    async function searchMemory() {
        const query = document.getElementById('memory-search-input').value.trim();
        if (!query) return;

        const results = await fetchJSON('/memory/search', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ query, limit: 20 })
        });

        const el = document.getElementById('memory-results');
        if (!results || results.length === 0) {
            el.innerHTML = '<div class="empty-state">No results found</div>';
            return;
        }

        el.innerHTML = `
            <div class="card">
                <table>
                    <thead>
                        <tr><th>ID</th><th>Type</th><th>Content</th><th>Source</th><th>Created</th></tr>
                    </thead>
                    <tbody>
                        ${results.map(r => `
                            <tr>
                                <td>${escapeHtml(r.id)}</td>
                                <td>${escapeHtml(r.type)}</td>
                                <td>${escapeHtml(truncate(r.content, 100))}</td>
                                <td>${escapeHtml(r.source)}</td>
                                <td>${formatTime(r.created_at)}</td>
                            </tr>
                        `).join('')}
                    </tbody>
                </table>
            </div>
        `;
    }

    async function loadEvents() {
        const events = await fetchJSON('/events?limit=100');
        const el = document.getElementById('view-events');

        el.innerHTML = `
            <h3>Event Stream <span class="ws-status disconnected" id="ws-indicator"></span></h3>
            <div class="card">
                <div class="event-log" id="event-log">
                    ${(events || []).map(e => eventEntryHTML(e)).join('')}
                </div>
            </div>
        `;

        connectWebSocket();
    }

    async function loadMCP() {
        const servers = await fetchJSON('/mcp/servers');
        const el = document.getElementById('view-mcp');

        if (!servers || servers.length === 0) {
            el.innerHTML = '<div class="empty-state">No MCP servers configured</div>';
            return;
        }

        el.innerHTML = `
            <h3>MCP Servers</h3>
            <div class="card">
                <table>
                    <thead>
                        <tr><th>Name</th><th>Status</th><th>Tools</th><th>Version</th><th>Error</th></tr>
                    </thead>
                    <tbody>
                        ${servers.map(s => `
                            <tr>
                                <td>${escapeHtml(s.name)}</td>
                                <td><span class="badge ${s.connected ? 'badge-success' : 'badge-danger'}">${s.connected ? 'Connected' : 'Disconnected'}</span></td>
                                <td>${(s.tools || []).length}</td>
                                <td>${escapeHtml(s.version || '-')}</td>
                                <td>${escapeHtml(s.error || '-')}</td>
                            </tr>
                        `).join('')}
                    </tbody>
                </table>
            </div>
        `;
    }

    // --- WebSocket ---
    function connectWebSocket() {
        if (ws && ws.readyState === WebSocket.OPEN) return;

        const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
        const url = `${protocol}//${location.host}/api/dashboard/events/stream`;

        ws = new WebSocket(url);

        ws.onopen = () => {
            updateWSStatus(true);
            ws.send(JSON.stringify({ type: 'subscribe', channel: 'events' }));
            ws.send(JSON.stringify({ type: 'subscribe', channel: 'agents' }));
        };

        ws.onmessage = (event) => {
            try {
                const msg = JSON.parse(event.data);
                handleWSMessage(msg);
            } catch (e) {
                console.error('WS parse error:', e);
            }
        };

        ws.onclose = () => {
            updateWSStatus(false);
            scheduleReconnect();
        };

        ws.onerror = () => {
            updateWSStatus(false);
        };
    }

    function handleWSMessage(msg) {
        if (msg.type === 'event' && currentView === 'events') {
            const log = document.getElementById('event-log');
            if (log) {
                log.insertAdjacentHTML('afterbegin', eventEntryHTML(msg.data));
                // Keep log manageable.
                while (log.children.length > 500) {
                    log.removeChild(log.lastChild);
                }
            }
        }
    }

    function updateWSStatus(connected) {
        const indicator = document.getElementById('ws-indicator');
        if (indicator) {
            indicator.className = `ws-status ${connected ? 'connected' : 'disconnected'}`;
        }
    }

    function scheduleReconnect() {
        if (reconnectTimer) return;
        reconnectTimer = setTimeout(() => {
            reconnectTimer = null;
            if (currentView === 'events') {
                connectWebSocket();
            }
        }, 5000);
    }

    // --- Helpers ---
    function escapeHtml(str) {
        if (!str) return '';
        const div = document.createElement('div');
        div.textContent = str;
        return div.innerHTML;
    }

    function truncate(str, max) {
        if (!str) return '';
        return str.length > max ? str.slice(0, max) + '...' : str;
    }

    function formatTime(ts) {
        if (!ts) return '-';
        try {
            return new Date(ts).toLocaleString();
        } catch {
            return ts;
        }
    }

    function statusBadge(status) {
        switch (status) {
            case 'ready':
            case 'completed':
            case 'success':  return 'badge-success';
            case 'busy':
            case 'running':
            case 'processing': return 'badge-warning';
            case 'offline':
            case 'failed':
            case 'error':    return 'badge-danger';
            default:         return 'badge-info';
        }
    }

    function eventEntryHTML(e) {
        return `
            <div class="event-entry">
                <span class="time">${formatTime(e.timestamp)}</span>
                <span class="type">${escapeHtml(e.type)}</span>
                <span>${escapeHtml(e.stream_id)}</span>
            </div>
        `;
    }

    // --- Init ---
    switchView('overview');
})();
