// GoAgentX Dashboard — Unified API v2
(function() {
    'use strict';
    let ws = null;
    let currentView = 'overview';

    // ── API ──────────────────────────────
    async function api(path, opts) {
        try {
            const headers = opts?.headers || {};
            if (!headers['Accept']) headers['Accept'] = 'application/json';
            const r = await fetch(path, { ...opts, headers });
            if (!r.ok) { const e = await r.json().catch(()=>({})); throw new Error(e.error||r.statusText); }
            return await r.json();
        } catch(e) { console.error('API:', path, e); return null; }
    }

    // ── Router ───────────────────────────
    const views = { overview, agents, mcp, orchestrator, arena };
    document.querySelectorAll('[data-view]').forEach(a => {
        a.addEventListener('click', e => { e.preventDefault(); show(a.dataset.view); });
    });

    function show(name) {
        currentView = name;
        document.querySelectorAll('[data-view]').forEach(a => a.classList.toggle('active', a.dataset.view===name));
        document.querySelectorAll('.view').forEach(v => v.classList.toggle('active', v.id==='view-'+name));
        if (views[name]) views[name]();
    }

    // ── Overview ─────────────────────────
    async function overview() {
        const d = await api('/');
        if (!d) return;
        const el = document.getElementById('view-overview');
        el.innerHTML = `
            <div class="page-header">
                <h2>System Overview</h2>
                <div class="page-desc">Real-time runtime intelligence</div>
            </div>
            <div class="stat-grid">
                <div class="stat-card">
                    <div class="label">Active Agents</div>
                    <div class="value">${d.agents||0}</div>
                </div>
                <div class="stat-card">
                    <div class="label">MCP Servers</div>
                    <div class="value">${d.mcp_servers||0}</div>
                </div>
                <div class="stat-card">
                    <div class="label">MCP Tools</div>
                    <div class="value">${d.mcp_tools||0}</div>
                </div>
                <div class="stat-card">
                    <div class="label">Uptime</div>
                    <div class="value" style="font-size:1.25rem">${d.uptime||'-'}</div>
                </div>
            </div>
            <div class="card">
                <h3>Quick Actions</h3>
                <div style="display:flex;gap:0.75rem;flex-wrap:wrap;margin-top:0.5rem">
                    <button class="btn btn-primary" onclick="show('orchestrator')">Launch Agent</button>
                    <button class="btn btn-outline" onclick="show('agents')">View Agents</button>
                    <button class="btn btn-outline" onclick="show('mcp')">MCP Tools</button>
                </div>
            </div>`;
    }

    // ── Agents ───────────────────────────
    async function agents() {
        const list = await api('/agents') || [];
        const el = document.getElementById('view-agents');

        if (!list.length) {
            el.innerHTML = `
                <div class="page-header"><h2>Agents</h2></div>
                <div class="empty-state">No agents running. Launch from the Orchestrator tab.</div>`;
            return;
        }

        el.innerHTML = `
            <div class="page-header">
                <h2>Agents</h2>
                <div class="page-desc">${list.length} agent${list.length>1?'s':''} tracked</div>
            </div>
            <div class="card">
                <table>
                    <thead><tr><th>Name</th><th>Status</th><th>Progress</th><th>Duration</th><th></th></tr></thead>
                    <tbody>${list.map(a => `
                        <tr>
                            <td><strong>${esc(a.name)}</strong></td>
                            <td>${badge(a.status)}</td>
                            <td>
                                <div class="progress-bar"><div class="fill" style="width:${a.progress||0}%"></div></div>
                                <span style="margin-left:0.5rem;font-size:0.75rem;color:var(--text-secondary)">${a.progress||0}%</span>
                            </td>
                            <td style="color:var(--text-secondary)">${esc(a.duration||'-')}</td>
                            <td><button class="btn btn-outline btn-sm" onclick="viewAgent('${a.id}')">View</button></td>
                        </tr>`).join('')}
                    </tbody>
                </table>
            </div>
            <div id="agent-detail"></div>`;

        // Auto-refresh if agents are running.
        if (list.some(a => a.status==='running' || a.status==='pending' || a.status.includes('analyzing'))) {
            setTimeout(() => { if (currentView==='agents') agents(); }, 3000);
        }
    }

    window.viewAgent = async function(id) {
        const a = await api('/agents/'+id);
        if (!a) return;
        const el = document.getElementById('agent-detail');
        el.innerHTML = `
            <div class="card result-card">
                <div class="result-header">
                    <h3>${esc(a.name)}</h3>
                    ${badge(a.status)}
                </div>
                <div style="display:flex;gap:2rem;margin-bottom:1rem;font-size:0.8125rem;color:var(--text-secondary)">
                    <span>Tool: <code>${esc(a.mcp_tool||'-')}</code></span>
                    <span>Duration: ${esc(a.duration||'-')}</span>
                    <span>Data: ${a.raw_data_len||0} bytes</span>
                </div>
                ${a.error ? `<div style="color:var(--danger);padding:0.75rem;background:var(--danger-glow);border-radius:var(--radius-sm);margin-bottom:1rem;font-size:0.875rem">${esc(a.error)}</div>` : ''}
                ${a.analysis ? `<div class="analysis-block">${esc(a.analysis)}</div>` : '<div class="empty-state">No analysis available</div>'}
            </div>`;
        el.scrollIntoView({behavior:'smooth'});
    };

    // ── MCP ──────────────────────────────
    async function mcp() {
        const list = await api('/mcp') || [];
        const el = document.getElementById('view-mcp');

        if (!list.length) {
            el.innerHTML = `
                <div class="page-header"><h2>MCP Tools</h2></div>
                <div class="empty-state">No MCP servers connected.</div>`;
            return;
        }

        el.innerHTML = list.map(s => `
            <div class="page-header">
                <h2>${esc(s.name)}</h2>
                <div class="page-desc">${badge(s.connected?'connected':'disconnected')} &middot; ${(s.tools||[]).length} tools</div>
            </div>
            <div class="card">
                <table>
                    <thead><tr><th>Tool</th><th>Description</th></tr></thead>
                    <tbody>${(s.tools||[]).map(t => `
                        <tr>
                            <td><code>${esc(t.name)}</code></td>
                            <td style="color:var(--text-secondary);max-width:500px">${esc(truncate(t.description||'-'))}</td>
                        </tr>`).join('')}
                    </tbody>
                </table>
            </div>`).join('');
    }

    // ── Orchestrator ─────────────────────
    async function orchestrator() {
        const list = await api('/agents') || [];
        const completed = list.filter(a => a.status==='completed' && a.analysis);
        const el = document.getElementById('view-orchestrator');

        el.innerHTML = `
            <div class="page-header">
                <h2>Orchestrator</h2>
                <div class="page-desc">Create and launch analysis agents</div>
            </div>
            <div class="card">
                <h3>Launch Agent</h3>
                <div class="orch-form">
                    <div class="field">
                        <label>Template</label>
                        <select id="tpl">
                            <option value="">-- Custom --</option>
                            <option value="tpl-structure">Architecture Review</option>
                            <option value="tpl-error-review">Error Handling</option>
                            <option value="tpl-concurrency">Concurrency</option>
                            <option value="tpl-impact">Change Impact</option>
                            <option value="tpl-api">API Surface</option>
                        </select>
                    </div>
                    <div class="field" id="custom-fields" style="display:none">
                        <label>Agent Name</label>
                        <input type="text" id="custom-name" placeholder="My Review">
                    </div>
                    <div class="field" id="custom-tool-field" style="display:none">
                        <label>MCP Tool</label>
                        <input type="text" id="custom-tool" placeholder="codegraph_context">
                    </div>
                    <div class="field" id="custom-prompt-field" style="display:none">
                        <label>LLM Prompt</label>
                        <textarea id="custom-prompt" rows="2" placeholder="Analyze: {{.raw_data}}"></textarea>
                    </div>
                    <div class="field" style="align-self:flex-end">
                        <button class="btn btn-primary" id="launch-btn">Launch</button>
                    </div>
                </div>
                <div id="launch-msg" style="margin-top:0.75rem;font-size:0.8125rem"></div>
            </div>

            ${completed.length ? `
            <div class="card">
                <h3>Results (${completed.length})</h3>
                ${completed.map(a => `
                    <div class="result-card" style="margin-bottom:1rem;padding:1rem;border-left:3px solid var(--accent);background:rgba(99,102,241,0.03);border-radius:0 var(--radius-sm) var(--radius-sm) 0">
                        <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:0.75rem">
                            <strong>${esc(a.name)}</strong>
                            <span style="font-size:0.75rem;color:var(--text-secondary)">${esc(a.duration||'-')}</span>
                        </div>
                        <div class="analysis-block">${esc((a.analysis||'').slice(0,3000))}</div>
                    </div>`).join('')}
            </div>` : ''}`;

        // Toggle custom fields.
        const tplSelect = document.getElementById('tpl');
        const toggle = () => {
            const isCustom = !tplSelect.value;
            document.getElementById('custom-fields').style.display = isCustom ? 'flex' : 'none';
            document.getElementById('custom-tool-field').style.display = isCustom ? 'flex' : 'none';
            document.getElementById('custom-prompt-field').style.display = isCustom ? 'flex' : 'none';
        };
        tplSelect.addEventListener('change', toggle);
        toggle();

        // Launch handler.
        document.getElementById('launch-btn').addEventListener('click', async () => {
            const btn = document.getElementById('launch-btn');
            const msg = document.getElementById('launch-msg');
            btn.disabled = true;
            msg.textContent = 'Launching...';
            msg.style.color = 'var(--text-secondary)';

            const tid = tplSelect.value;
            const body = {};
            if (tid) {
                body.template_id = tid;
            } else {
                const name = document.getElementById('custom-name').value;
                const tool = document.getElementById('custom-tool').value;
                const prompt = document.getElementById('custom-prompt').value;
                if (!name) { msg.textContent = 'Name is required'; msg.style.color = 'var(--danger)'; btn.disabled=false; return; }
                body.name = name;
                if (tool) body.mcp_tool = tool;
                if (prompt) body.llm_prompt = prompt;
            }

            const r = await api('/agents', { method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(body) });
            btn.disabled = false;
            if (r && r.id) {
                msg.textContent = `Agent ${r.id} launched!`;
                msg.style.color = 'var(--success)';
                setTimeout(() => orchestrator(), 2000);
            } else {
                msg.textContent = 'Failed to launch';
                msg.style.color = 'var(--danger)';
            }
        });
    }

    // ── Arena ─────────────────────────────
    async function arena() {
        const [stats, history, agents] = await Promise.all([
            api('/arena/stats'),
            api('/arena/history'),
            api('/agents'),
        ]);
        const el = document.getElementById('view-arena');

        const recoveryRate = stats && stats.total_actions > 0
            ? Math.round((stats.successful_actions / stats.total_actions) * 100) : 0;

        el.innerHTML = `
            <div class="page-header">
                <h2>Agent Arena</h2>
                <div class="page-desc">Chaos engineering — break it, watch it recover</div>
            </div>

            <div class="stat-grid">
                <div class="stat-card">
                    <div class="label">Recovery Rate</div>
                    <div class="value">${recoveryRate}%</div>
                </div>
                <div class="stat-card">
                    <div class="label">Total Faults</div>
                    <div class="value">${stats?.total_actions||0}</div>
                </div>
                <div class="stat-card">
                    <div class="label">Recovered</div>
                    <div class="value">${stats?.successful_actions||0}</div>
                </div>
                <div class="stat-card">
                    <div class="label">Failed</div>
                    <div class="value">${stats?.failed_actions||0}</div>
                </div>
            </div>

            <div class="card">
                <h3>Inject Fault</h3>
                <div style="display:flex;flex-wrap:wrap;gap:0.75rem;margin-top:0.5rem">
                    <button class="btn btn-danger" onclick="arenaAction('kill_leader')" style="background:linear-gradient(135deg,#ef4444,#dc2626);color:white;box-shadow:0 2px 8px rgba(239,68,68,0.3)">
                        &#9760; Assassinate Leader
                    </button>
                    <select id="arena-agent-select" style="padding:0.625rem 1rem;background:var(--bg-primary);border:1px solid var(--border);border-radius:var(--radius-sm);color:var(--text-primary)">
                        <option value="">-- Select Agent --</option>
                        ${(agents||[]).map(a => `<option value="${a.id}">${esc(a.name)} (${a.status})</option>`).join('')}
                    </select>
                    <button class="btn btn-outline" onclick="arenaKillAgent()" style="border-color:var(--danger);color:var(--danger)">
                        &#128293; Kill Agent
                    </button>
                </div>
                <div style="display:flex;flex-wrap:wrap;gap:0.75rem;margin-top:0.75rem">
                    <input type="text" id="arena-node-id" placeholder="Node ID" style="padding:0.625rem 1rem;background:var(--bg-primary);border:1px solid var(--border);border-radius:var(--radius-sm);color:var(--text-primary)">
                    <button class="btn btn-outline" onclick="arenaRemoveNode()">&#128163; Remove Node</button>
                    <input type="text" id="arena-edge-from" placeholder="From" style="width:100px;padding:0.625rem 1rem;background:var(--bg-primary);border:1px solid var(--border);border-radius:var(--radius-sm);color:var(--text-primary)">
                    <input type="text" id="arena-edge-to" placeholder="To" style="width:100px;padding:0.625rem 1rem;background:var(--bg-primary);border:1px solid var(--border);border-radius:var(--radius-sm);color:var(--text-primary)">
                    <button class="btn btn-outline" onclick="arenaRemoveEdge()">&#9986; Remove Edge</button>
                </div>
                <div id="arena-action-result" style="margin-top:0.75rem;font-size:0.8125rem"></div>
            </div>

            <div class="card">
                <h3>Action History</h3>
                <div id="arena-history">
                    ${(history||[]).length === 0 ? '<div class="empty-state">No actions yet. Inject a fault above.</div>' : `
                    <table>
                        <thead><tr><th>Action</th><th>Target</th><th>Result</th><th>Duration</th></tr></thead>
                        <tbody>
                            ${(history||[]).reverse().map(r => `
                                <tr>
                                    <td><code>${esc(r.action?.type||'-')}</code></td>
                                    <td>${esc(r.action?.target_id||r.action?.source_id||'-')}</td>
                                    <td>${r.success ? '<span class="badge badge-success">success</span>' : '<span class="badge badge-danger">failed</span>'}</td>
                                    <td style="color:var(--text-secondary)">${r.duration ? Math.round(r.duration/1000000)+'ms' : '-'}</td>
                                </tr>
                            `).join('')}
                        </tbody>
                    </table>`}
                </div>
            </div>`;

        // Auto-refresh every 3s.
        setTimeout(() => { if (currentView==='arena') arena(); }, 3000);
    }

    window.arenaAction = async function(type) {
        const el = document.getElementById('arena-action-result');
        el.textContent = 'Injecting...';
        el.style.color = 'var(--text-secondary)';

        const r = await api('/arena/leader/kill', { method: 'POST' });
        if (r && r.success) {
            el.innerHTML = `<span class="badge badge-success">Leader killed — watching recovery...</span>`;
        } else {
            el.innerHTML = `<span class="badge badge-danger">Failed: ${esc(r?.error||'unknown')}</span>`;
        }
        setTimeout(() => { if (currentView==='arena') arena(); }, 1000);
    };

    window.arenaKillAgent = async function() {
        const id = document.getElementById('arena-agent-select').value;
        if (!id) { alert('Select an agent first'); return; }
        const el = document.getElementById('arena-action-result');
        el.textContent = 'Killing agent...';
        el.style.color = 'var(--text-secondary)';

        const r = await api(`/arena/agent/${id}/kill`, { method: 'POST' });
        if (r && r.success) {
            el.innerHTML = `<span class="badge badge-success">Agent ${esc(id)} killed — resurrection pending...</span>`;
        } else {
            el.innerHTML = `<span class="badge badge-danger">Failed: ${esc(r?.error||'unknown')}</span>`;
        }
        setTimeout(() => { if (currentView==='arena') arena(); }, 1000);
    };

    window.arenaRemoveNode = async function() {
        const id = document.getElementById('arena-node-id').value;
        if (!id) { alert('Enter a node ID'); return; }
        const el = document.getElementById('arena-action-result');
        el.textContent = 'Removing node...';

        const r = await api(`/arena/node/${id}/remove`, { method: 'POST' });
        if (r && r.success) {
            el.innerHTML = `<span class="badge badge-success">Node ${esc(id)} removed</span>`;
        } else {
            el.innerHTML = `<span class="badge badge-danger">Failed: ${esc(r?.error||'unknown')}</span>`;
        }
        setTimeout(() => { if (currentView==='arena') arena(); }, 1000);
    };

    window.arenaRemoveEdge = async function() {
        const from = document.getElementById('arena-edge-from').value;
        const to = document.getElementById('arena-edge-to').value;
        if (!from || !to) { alert('Enter both From and To'); return; }
        const el = document.getElementById('arena-action-result');
        el.textContent = 'Removing edge...';

        const r = await api('/arena/edge/remove', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ from, to }),
        });
        if (r && r.success) {
            el.innerHTML = `<span class="badge badge-success">Edge ${esc(from)}→${esc(to)} removed</span>`;
        } else {
            el.innerHTML = `<span class="badge badge-danger">Failed: ${esc(r?.error||'unknown')}</span>`;
        }
        setTimeout(() => { if (currentView==='arena') arena(); }, 1000);
    };

    // Add danger button style.
    const dangerStyle = document.createElement('style');
    dangerStyle.textContent = `.btn-danger{background:linear-gradient(135deg,#ef4444,#dc2626);color:white;box-shadow:0 2px 8px rgba(239,68,68,0.3)}.btn-danger:hover{transform:translateY(-1px);box-shadow:0 4px 16px rgba(239,68,68,0.3)}`;
    document.head.appendChild(dangerStyle);

    // ── WebSocket ────────────────────────
    function connectWS() {
        if (ws && ws.readyState===WebSocket.OPEN) return;
        const proto = location.protocol==='https:'?'wss:':'ws:';
        ws = new WebSocket(proto+'//'+location.host+'/ws');
        ws.onopen = () => {
            ws.send(JSON.stringify({type:'subscribe',channel:'agents'}));
            ws.send(JSON.stringify({type:'subscribe',channel:'events'}));
        };
        ws.onmessage = e => {
            try {
                const msg = JSON.parse(e.data);
                if (msg.type==='agent_update' && currentView==='agents') agents();
            } catch(_) {}
        };
        ws.onclose = () => setTimeout(connectWS, 5000);
    }

    // ── Helpers ──────────────────────────
    function esc(s) { if(!s)return''; const d=document.createElement('div'); d.textContent=s; return d.innerHTML; }
    function truncate(s) { if(!s||s.length<=120)return s; return s.slice(0,117)+'...'; }
    function badge(s) {
        if (!s) return '';
        const cls = s==='completed'?'badge-success':s==='failed'?'badge-danger':s.includes('running')||s.includes('analyzing')||s==='pending'?'badge-warning':'badge-info';
        return `<span class="badge ${cls}">${esc(s)}</span>`;
    }

    // ── Init ─────────────────────────────
    show('overview');
    connectWS();
})();
