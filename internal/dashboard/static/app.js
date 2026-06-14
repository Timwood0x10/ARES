// GoAgentX Dashboard — Unified API v2
(function() {
    'use strict';
    let ws = null;
    let currentView = 'overview';
    let selectedAgentId = null;
    let arenaAgents = [];

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
    const views = { overview, agents, mcp, orchestrator, arena, flight };
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
        const [stats, history, agents, score] = await Promise.all([
            api('/arena/stats'),
            api('/arena/history'),
            api('/agents'),
            api('/arena/score'),
        ]);
        arenaAgents = agents || [];
        const el = document.getElementById('view-arena');

        const recoveryRate = stats && stats.total_actions > 0
            ? Math.round((stats.successful_actions / stats.total_actions) * 100) : 0;

        // Build resilience score card.
        const scoreValue = score?.score != null ? score.score.toFixed(1) : '-';
        const scoreGrade = score?.grade || '-';
        const scoreRecovery = score?.recovery_rate != null ? score.recovery_rate.toFixed(1) + '%' : '-';
        const scoreAvgTime = score?.avg_recovery_time
            ? (score.avg_recovery_time / 1000000000).toFixed(1) + 's' : '-';
        const gradeColor = scoreGrade === 'A+' ? '#22c55e'
            : scoreGrade === 'A' ? '#22c55e'
            : scoreGrade === 'B' ? '#eab308'
            : scoreGrade === 'C' ? '#f97316'
            : scoreGrade === 'D' ? '#ef4444'
            : '#6b7280';

        el.innerHTML = `
            <div class="page-header">
                <h2>Agent Arena</h2>
                <div class="page-desc">Chaos engineering — break it, watch it recover</div>
            </div>

            <div style="display:flex;gap:1rem;flex-wrap:wrap;margin-bottom:1rem">
                <div class="card" style="flex:0 0 220px;text-align:center;padding:1.5rem;border:2px solid ${gradeColor};border-radius:var(--radius)">
                    <div style="font-size:0.8125rem;color:var(--text-secondary);text-transform:uppercase;letter-spacing:0.05em;margin-bottom:0.5rem">Resilience Score</div>
                    <div style="font-size:2.5rem;font-weight:700;color:${gradeColor}">${scoreValue}</div>
                    <div style="font-size:1.5rem;font-weight:600;color:${gradeColor};margin-bottom:0.75rem">${scoreGrade}</div>
                    <div style="font-size:0.8125rem;color:var(--text-secondary)">Recovery: ${scoreRecovery}</div>
                    <div style="font-size:0.8125rem;color:var(--text-secondary)">Avg Time: ${scoreAvgTime}</div>
                </div>
                <div style="flex:1;min-width:0">
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
                </div>
            </div>

            <div class="card">
                <h3>Agent Graph</h3>
                <svg id="dag-svg" width="100%" height="200" style="background:var(--bg-primary);border-radius:var(--radius-sm)"></svg>
                <div id="arena-selected-info" style="margin-top:0.75rem"></div>
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

        // Render DAG and sync selection.
        renderDAG(arenaAgents);
        const agentSelect = document.getElementById('arena-agent-select');
        if (agentSelect) {
            if (selectedAgentId) agentSelect.value = selectedAgentId;
            agentSelect.addEventListener('change', () => {
                const id = agentSelect.value;
                if (id) {
                    const a = arenaAgents.find(ag => ag.id === id);
                    selectedAgentId = id;
                    updateSelectedInfo(a || null);
                } else {
                    selectedAgentId = null;
                    updateSelectedInfo(null);
                }
                renderDAG(arenaAgents);
            });
        }
        // Restore info panel for current selection.
        if (selectedAgentId) {
            const a = arenaAgents.find(ag => ag.id === selectedAgentId);
            updateSelectedInfo(a || null);
        }
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
        const id = document.getElementById('arena-agent-select').value || selectedAgentId;
        if (!id) { alert('Select an agent first (use dropdown or click a node)'); return; }
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

    // ── Flight Recorder ─────────────────
    async function flight() {
        const el = document.getElementById('view-flight');
        el.innerHTML = `
            <div class="page-header">
                <h2>Flight Recorder</h2>
                <div class="page-desc">Timeline, call graph, decisions, and diagnostics</div>
            </div>
            <div id="flight-loading" class="empty-state">Loading flight data...</div>`;

        const [summary, graphData, decisions, diagData, genealogyData] = await Promise.all([
            api('/flight/summary'),
            api('/flight/graph'),
            api('/flight/decisions'),
            api('/flight/diagnostics'),
            api('/flight/genealogy'),
        ]);

        const container = document.getElementById('flight-loading');
        if (!container) return; // navigated away

        container.remove();

        // Build all sections.
        el.innerHTML = `
            <div class="page-header">
                <h2>Flight Recorder</h2>
                <div class="page-desc">Timeline, call graph, decisions, and diagnostics</div>
            </div>
            ${renderTimelineSection(summary)}
            ${renderGraphSection(graphData)}
            ${renderDecisionsSection(decisions)}
            ${renderDiagnosticsSection(diagData)}
            ${renderGenealogySection(genealogyData)}`;

        // Render Mermaid diagrams after DOM insertion.
        renderMermaidDiagrams();
    }

    function renderTimelineSection(summary) {
        if (!summary || summary.event_count === 0) {
            return '<div class="card"><h3>Timeline</h3><div class="empty-state">No timeline events recorded yet.</div></div>';
        }

        const toolPct = summary.tool_percent || 0;
        const llmPct = summary.llm_percent || 0;
        const waitPct = summary.wait_percent || 0;
        const otherPct = Math.max(0, 100 - toolPct - llmPct - waitPct);

        return `
            <div class="card">
                <h3>Timeline — Time Distribution</h3>
                <div class="timeline-chart">
                    <div class="timeline-row">
                        <span class="timeline-label">Tool Calls</span>
                        <div class="timeline-bar-track">
                            <div class="timeline-bar" style="width:${toolPct}%;background:linear-gradient(90deg,var(--accent),#a78bfa)"></div>
                        </div>
                        <span class="timeline-pct">${toolPct.toFixed(1)}%</span>
                    </div>
                    <div class="timeline-row">
                        <span class="timeline-label">LLM Calls</span>
                        <div class="timeline-bar-track">
                            <div class="timeline-bar" style="width:${llmPct}%;background:linear-gradient(90deg,var(--success),#34d399)"></div>
                        </div>
                        <span class="timeline-pct">${llmPct.toFixed(1)}%</span>
                    </div>
                    <div class="timeline-row">
                        <span class="timeline-label">Waiting</span>
                        <div class="timeline-bar-track">
                            <div class="timeline-bar" style="width:${waitPct}%;background:linear-gradient(90deg,var(--warning),#fbbf24)"></div>
                        </div>
                        <span class="timeline-pct">${waitPct.toFixed(1)}%</span>
                    </div>
                    <div class="timeline-row">
                        <span class="timeline-label">Other</span>
                        <div class="timeline-bar-track">
                            <div class="timeline-bar" style="width:${otherPct}%;background:linear-gradient(90deg,var(--text-muted),var(--text-secondary))"></div>
                        </div>
                        <span class="timeline-pct">${otherPct.toFixed(1)}%</span>
                    </div>
                </div>
                <div class="timeline-stats">
                    <span>Events: <strong>${summary.event_count}</strong></span>
                    <span>Total: <strong>${formatDuration(summary.total_duration)}</strong></span>
                </div>
            </div>`;
    }

    function renderGraphSection(graphData) {
        const mermaidText = graphData?.mermaid || '';
        if (!mermaidText || mermaidText.includes('No data')) {
            return '<div class="card"><h3>Call Graph</h3><div class="empty-state">No call graph data recorded yet.</div></div>';
        }

        return `
            <div class="card">
                <h3>Call Graph</h3>
                <div class="mermaid" data-mermaid="${esc(mermaidText)}">${esc(mermaidText)}</div>
            </div>`;
    }

    function renderDecisionsSection(decisions) {
        if (!decisions || decisions.length === 0) {
            return '<div class="card"><h3>Decisions</h3><div class="empty-state">No decisions recorded yet.</div></div>';
        }

        return `
            <div class="card">
                <h3>Decisions (${decisions.length})</h3>
                <div class="table-scroll">
                    <table class="decision-table">
                        <thead>
                            <tr>
                                <th>Agent</th>
                                <th>Type</th>
                                <th>Selected</th>
                                <th>Reason</th>
                                <th>Confidence</th>
                            </tr>
                        </thead>
                        <tbody>
                            ${decisions.map(d => `
                                <tr>
                                    <td><code>${esc(d.agent_id || '-')}</code></td>
                                    <td>${badge(d.type || '-')}</td>
                                    <td><strong>${esc(d.selected || '-')}</strong></td>
                                    <td style="max-width:300px;color:var(--text-secondary)">${esc(truncate(d.reason || '-'))}</td>
                                    <td>
                                        <div class="confidence-bar">
                                            <div class="confidence-fill" style="width:${(d.confidence||0)*100}%"></div>
                                        </div>
                                        <span class="confidence-pct">${((d.confidence||0)*100).toFixed(0)}%</span>
                                    </td>
                                </tr>`).join('')}
                        </tbody>
                    </table>
                </div>
            </div>`;
    }

    function renderDiagnosticsSection(diagData) {
        const records = diagData?.records || [];
        const dist = diagData?.distribution || {};

        if (records.length === 0 && (!dist.categories || Object.keys(dist.categories).length === 0)) {
            return '<div class="card"><h3>Diagnostics</h3><div class="empty-state">No diagnostic records yet.</div></div>';
        }

        const categories = dist.categories || {};
        const percentages = dist.percentages || {};
        const total = dist.total || 0;

        // Build conic-gradient for pie chart.
        const pieColors = [
            'var(--danger)', 'var(--warning)', 'var(--info)', 'var(--success)',
            'var(--accent)', 'var(--text-muted)', '#a78bfa', '#f472b6'
        ];
        let conicParts = [];
        let acc = 0;
        const catKeys = Object.keys(categories);
        catKeys.forEach((cat, i) => {
            const pct = percentages[cat] || 0;
            const color = pieColors[i % pieColors.length];
            conicParts.push(`${color} ${acc}% ${acc + pct}%`);
            acc += pct;
        });
        const conicCSS = conicParts.length > 0
            ? `conic-gradient(${conicParts.join(', ')})`
            : 'conic-gradient(var(--text-muted) 0% 100%)';

        const categoryRows = catKeys.map((cat, i) => {
            const count = categories[cat] || 0;
            const pct = percentages[cat] || 0;
            const color = pieColors[i % pieColors.length];
            return `
                <tr>
                    <td><span class="dot-indicator" style="background:${color}"></span>${esc(cat)}</td>
                    <td>${count}</td>
                    <td>${pct.toFixed(1)}%</td>
                </tr>`;
        }).join('');

        return `
            <div class="card">
                <h3>Diagnostics</h3>
                <div class="diagnostics-card">
                    <div class="diagnostics-pie-wrap">
                        <div class="pie-chart" style="background:${conicCSS}"></div>
                        <div class="pie-center">${total}</div>
                    </div>
                    <div class="diagnostics-table-wrap">
                        <table class="diagnostics-table">
                            <thead>
                                <tr><th>Category</th><th>Count</th><th>Percentage</th></tr>
                            </thead>
                            <tbody>${categoryRows}</tbody>
                        </table>
                    </div>
                </div>
            </div>`;
    }

    function renderGenealogySection(genealogyData) {
        const mermaidText = genealogyData?.mermaid || '';
        if (!mermaidText || mermaidText.includes('No agents')) {
            return '<div class="card"><h3>Genealogy</h3><div class="empty-state">No genealogy data recorded yet.</div></div>';
        }

        return `
            <div class="card">
                <h3>Genealogy Tree</h3>
                <div class="mermaid" data-mermaid="${esc(mermaidText)}">${esc(mermaidText)}</div>
            </div>`;
    }

    function renderMermaidDiagrams() {
        if (typeof mermaid === 'undefined') return;
        try {
            mermaid.initialize({ startOnLoad: false, theme: 'dark' });
            document.querySelectorAll('.mermaid[data-mermaid]').forEach(async (el, i) => {
                const text = el.getAttribute('data-mermaid');
                if (!text) return;
                try {
                    const id = 'mermaid-graph-' + i;
                    const { svg } = await mermaid.render(id, text);
                    el.innerHTML = svg;
                    el.removeAttribute('data-mermaid');
                } catch (err) {
                    console.error('Mermaid render error:', err);
                    el.innerHTML = '<div class="empty-state">Failed to render diagram</div>';
                }
            });
        } catch (e) {
            console.error('Mermaid init error:', e);
        }
    }

    function formatDuration(d) {
        if (!d) return '-';
        // Go duration is in nanoseconds when serialized as int, or a string.
        if (typeof d === 'string') return d;
        if (typeof d === 'number') {
            if (d > 1e9) return (d / 1e9).toFixed(2) + 's';
            if (d > 1e6) return (d / 1e6).toFixed(1) + 'ms';
            if (d > 1e3) return (d / 1e3).toFixed(1) + 'us';
            return d + 'ns';
        }
        return String(d);
    }

    // ── DAG Visualization ───────────────
    var SVG_NS = 'http://www.w3.org/2000/svg';

    function renderDAG(agents) {
        var svg = document.getElementById('dag-svg');
        if (!svg) return;
        svg.innerHTML = '';

        if (!agents || agents.length === 0) {
            var t = document.createElementNS(SVG_NS, 'text');
            t.setAttribute('x', '50%');
            t.setAttribute('y', '50%');
            t.setAttribute('text-anchor', 'middle');
            t.setAttribute('dominant-baseline', 'middle');
            t.setAttribute('fill', 'var(--text-muted)');
            t.setAttribute('font-size', '14');
            t.textContent = 'No agents to display';
            svg.appendChild(t);
            return;
        }

        var cols = 4;
        var xStart = 100;
        var yStart = 70;
        var xGap = 180;
        var yGap = 120;

        agents.forEach(function(a, i) {
            var x = xStart + (i % cols) * xGap;
            var y = yStart + Math.floor(i / cols) * yGap;

            var statusColor = a.status === 'completed' ? 'var(--success)' :
                              a.status === 'failed' ? 'var(--danger)' :
                              (a.status.includes('running') || a.status.includes('analyzing') || a.status === 'pending') ? 'var(--warning)' :
                              'var(--info)';

            var isSelected = selectedAgentId === a.id;

            var g = document.createElementNS(SVG_NS, 'g');
            g.setAttribute('data-agent-id', a.id);
            g.style.cursor = 'pointer';

            // Glow ring for selected node.
            if (isSelected) {
                var glow = document.createElementNS(SVG_NS, 'circle');
                glow.setAttribute('cx', x);
                glow.setAttribute('cy', y);
                glow.setAttribute('r', '36');
                glow.setAttribute('fill', 'none');
                glow.setAttribute('stroke', 'var(--accent)');
                glow.setAttribute('stroke-width', '2');
                glow.setAttribute('stroke-opacity', '0.3');
                g.appendChild(glow);
            }

            // Node circle.
            var circle = document.createElementNS(SVG_NS, 'circle');
            circle.setAttribute('cx', x);
            circle.setAttribute('cy', y);
            circle.setAttribute('r', isSelected ? '30' : '26');
            circle.setAttribute('fill', statusColor);
            circle.setAttribute('fill-opacity', '0.15');
            circle.setAttribute('stroke', isSelected ? 'var(--accent)' : statusColor);
            circle.setAttribute('stroke-width', isSelected ? '3' : '2');
            g.appendChild(circle);

            // Name label (inside circle).
            var name = document.createElementNS(SVG_NS, 'text');
            name.setAttribute('x', x);
            name.setAttribute('y', y + 1);
            name.setAttribute('text-anchor', 'middle');
            name.setAttribute('dominant-baseline', 'middle');
            name.setAttribute('fill', 'var(--text-primary)');
            name.setAttribute('font-size', '10');
            name.setAttribute('font-weight', '600');
            name.style.pointerEvents = 'none';
            name.textContent = truncName(a.name, 8);
            g.appendChild(name);

            // Status label (below circle).
            var status = document.createElementNS(SVG_NS, 'text');
            status.setAttribute('x', x);
            status.setAttribute('y', y + 42);
            status.setAttribute('text-anchor', 'middle');
            status.setAttribute('fill', statusColor);
            status.setAttribute('font-size', '10');
            status.style.pointerEvents = 'none';
            status.textContent = a.status;
            g.appendChild(status);

            // Click handler.
            g.addEventListener('click', function() { selectAgent(a); });

            svg.appendChild(g);
        });

        // Adjust SVG height to fit all nodes.
        var rows = Math.ceil(agents.length / cols);
        var height = yStart + rows * yGap + 30;
        svg.setAttribute('height', Math.max(200, height));
    }

    function truncName(name, maxLen) {
        if (!name) return '?';
        return name.length > maxLen ? name.slice(0, maxLen - 1) + '..' : name;
    }

    function selectAgent(agent) {
        selectedAgentId = agent.id;
        // Sync the Kill Agent dropdown.
        var select = document.getElementById('arena-agent-select');
        if (select) select.value = agent.id;
        // Update info panel and re-render DAG.
        updateSelectedInfo(agent);
        renderDAG(arenaAgents);
    }

    function updateSelectedInfo(agent) {
        var el = document.getElementById('arena-selected-info');
        if (!el) return;
        if (!agent) { el.innerHTML = ''; return; }

        var statusColor = agent.status === 'completed' ? 'var(--success)' :
                          agent.status === 'failed' ? 'var(--danger)' :
                          (agent.status.includes('running') || agent.status.includes('analyzing') || agent.status === 'pending') ? 'var(--warning)' :
                          'var(--info)';

        el.innerHTML =
            '<div style="display:flex;align-items:center;gap:0.75rem;padding:0.5rem 0.75rem;background:var(--bg-primary);border:1px solid var(--accent);border-radius:var(--radius-sm);font-size:0.8125rem">' +
                '<span style="width:10px;height:10px;border-radius:50%;background:' + statusColor + ';flex-shrink:0"></span>' +
                '<strong>' + esc(agent.name) + '</strong>' +
                '<span style="color:var(--text-secondary)">' + esc(agent.status) + '</span>' +
                (agent.duration ? '<span style="color:var(--text-muted)">' + esc(agent.duration) + '</span>' : '') +
                '<span style="color:var(--text-muted);margin-left:auto">ID: ' + esc(agent.id) + '</span>' +
            '</div>';
    }

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
