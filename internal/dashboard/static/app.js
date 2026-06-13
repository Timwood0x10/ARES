// GoAgentX Dashboard — unified API v2
(function() {
    'use strict';
    let ws = null;

    // ── API ──────────────────────────────────────
    async function api(path, opts) {
        try {
            const r = await fetch(path, opts);
            if (!r.ok) { const e = await r.json().catch(()=>({})); throw new Error(e.error||r.statusText); }
            return await r.json();
        } catch(e) { console.error('API:', path, e); return null; }
    }

    // ── Router ───────────────────────────────────
    const views = { overview, agents, mcp, orchestrator };
    document.querySelectorAll('[data-view]').forEach(a => {
        a.addEventListener('click', e => { e.preventDefault(); show(a.dataset.view); });
    });

    function show(name) {
        document.querySelectorAll('[data-view]').forEach(a => a.classList.toggle('active', a.dataset.view===name));
        document.querySelectorAll('.view').forEach(v => v.classList.toggle('active', v.id==='view-'+name));
        if (views[name]) views[name]();
    }

    // ── Overview ─────────────────────────────────
    async function overview() {
        const d = await api('/');
        if (!d) return;
        document.getElementById('view-overview').innerHTML = `
            <h3>System</h3>
            <div class="card-grid">
                <div class="card stat-card"><div class="value">${d.agents||0}</div><div class="label">Agents</div></div>
                <div class="card stat-card"><div class="value">${d.mcp_servers||0}</div><div class="label">MCP Servers</div></div>
                <div class="card stat-card"><div class="value">${d.mcp_tools||0}</div><div class="label">MCP Tools</div></div>
                <div class="card stat-card"><div class="value">${d.uptime||'-'}</div><div class="label">Uptime</div></div>
            </div>`;
    }

    // ── Agents ───────────────────────────────────
    async function agents() {
        const list = await api('/agents') || [];
        const el = document.getElementById('view-agents');
        if (!list.length) { el.innerHTML = '<div class="empty-state">No agents. Launch from Orchestrator.</div>'; return; }
        el.innerHTML = `
            <h3>Agents (${list.length})</h3>
            <div class="card"><table>
                <thead><tr><th>Name</th><th>Status</th><th>Progress</th><th>Duration</th><th></th></tr></thead>
                <tbody>${list.map(a => `<tr>
                    <td>${esc(a.name)}</td>
                    <td><span class="badge ${badge(a.status)}">${esc(a.status)}</span></td>
                    <td><div style="background:var(--bg-tertiary);border-radius:4px;height:8px;width:100px;display:inline-block;">
                        <div style="background:var(--accent);border-radius:4px;height:100%;width:${a.progress||0}%"></div>
                    </div> ${a.progress||0}%</td>
                    <td>${esc(a.duration||'-')}</td>
                    <td><button onclick="viewAgent('${a.id}')" style="padding:.25rem .5rem;font-size:.75rem">View</button></td>
                </tr>`).join('')}</tbody>
            </table></div>
            <div class="card" id="agent-detail" style="display:none">
                <h3 id="detail-title"></h3>
                <div id="detail-body" style="white-space:pre-wrap;font-family:monospace;font-size:.875rem;max-height:600px;overflow-y:auto"></div>
            </div>`;
    }

    window.viewAgent = async function(id) {
        const a = await api('/agents/'+id);
        if (!a) return;
        document.getElementById('agent-detail').style.display = 'block';
        document.getElementById('detail-title').textContent = a.name + ' — ' + a.status;
        let body = `Tool: ${a.mcp_tool||'-'}  |  Duration: ${a.duration||'-'}  |  Data: ${a.raw_data_len||0} bytes\n`;
        if (a.error) body += `\nError: ${a.error}\n`;
        if (a.analysis) body += `\n${a.analysis}`;
        document.getElementById('detail-body').textContent = body;
        document.getElementById('agent-detail').scrollIntoView({behavior:'smooth'});
    };

    // ── MCP ──────────────────────────────────────
    async function mcp() {
        const list = await api('/mcp') || [];
        const el = document.getElementById('view-mcp');
        if (!list.length) { el.innerHTML = '<div class="empty-state">No MCP servers.</div>'; return; }
        el.innerHTML = list.map(s => `
            <h3>${esc(s.name)} <span class="badge ${s.connected?'badge-success':'badge-danger'}">${s.connected?'Connected':'Disconnected'}</span></h3>
            <div class="card"><table>
                <thead><tr><th>Tool</th><th>Description</th></tr></thead>
                <tbody>${(s.tools||[]).map(t=>`<tr>
                    <td><code>${esc(t.name)}</code></td>
                    <td>${esc(t.description||'-')}</td>
                </tr>`).join('')}</tbody>
            </table></div>
        `).join('');
    }

    // ── Orchestrator ─────────────────────────────
    async function orchestrator() {
        const [tpls, list] = await Promise.all([api('/agents?status=completed'), api('/agents')]);
        const el = document.getElementById('view-orchestrator');
        el.innerHTML = `
            <h3>Launch Agent</h3>
            <div class="card">
                <div class="search-box">
                    <select id="tpl" style="padding:.5rem;background:var(--bg-tertiary);border:1px solid var(--border);border-radius:4px;color:var(--text-primary);min-width:200px">
                        <option value="">-- Custom --</option>
                        <option value="tpl-structure">Architecture Review</option>
                        <option value="tpl-error-review">Error Handling Review</option>
                        <option value="tpl-concurrency">Concurrency Review</option>
                        <option value="tpl-impact">Change Impact Analysis</option>
                        <option value="tpl-api">API Surface Review</option>
                    </select>
                    <button id="launch-btn" style="padding:.5rem 1rem;background:var(--accent);border:none;border-radius:4px;color:#fff;cursor:pointer">Launch</button>
                </div>
                <div id="launch-msg" style="margin-top:.5rem;font-size:.875rem;color:var(--text-secondary)"></div>
            </div>
            <h3 style="margin-top:1.5rem">Results (${(list||[]).filter(a=>a.status==='completed').length})</h3>
            ${(list||[]).filter(a=>a.status==='completed').map(a=>`
                <div class="card">
                    <strong>${esc(a.name)}</strong> — ${esc(a.duration||'-')}
                    <div style="white-space:pre-wrap;font-family:monospace;font-size:.875rem;margin-top:.5rem;max-height:300px;overflow-y:auto">${esc((a.analysis||'').slice(0,2000))}</div>
                </div>
            `).join('')}`;

        document.getElementById('launch-btn').onclick = async () => {
            const btn = document.getElementById('launch-btn');
            const msg = document.getElementById('launch-msg');
            btn.disabled = true; msg.textContent = 'Launching...';
            const tid = document.getElementById('tpl').value;
            const r = await api('/agents', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({template_id:tid||undefined})});
            btn.disabled = false;
            if (r&&r.id) { msg.textContent = 'Agent '+r.id+' launched!'; msg.style.color='var(--success)'; setTimeout(()=>orchestrator(),2000); }
            else { msg.textContent='Failed'; msg.style.color='var(--danger)'; }
        };
    }

    // ── WebSocket ────────────────────────────────
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
                if (msg.type==='agent_update') {
                    // Auto-refresh agents view if open.
                    const active = document.querySelector('.view.active');
                    if (active && active.id==='view-agents') agents();
                }
            } catch(_) {}
        };
        ws.onclose = () => setTimeout(connectWS, 5000);
    }

    // ── Helpers ──────────────────────────────────
    function esc(s) { if(!s)return''; const d=document.createElement('div'); d.textContent=s; return d.innerHTML; }
    function badge(s) {
        if (s==='completed') return 'badge-success';
        if (s==='running'||s==='pending') return 'badge-warning';
        if (s==='failed') return 'badge-danger';
        return 'badge-info';
    }

    // ── Init ─────────────────────────────────────
    show('overview');
    connectWS();
})();
