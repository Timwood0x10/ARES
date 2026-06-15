// GoAgentX Dashboard — Unified API v2
(function() {
    'use strict';
    let ws = null;
    let currentView = 'overview';
    let selectedAgentId = null;
    let arenaAgents = [];

    // ── DOM Builder ───────────────────────
    function h(tag, attrs, ...children) {
        var el = document.createElement(tag);
        if (attrs) {
            for (var k in attrs) {
                if (k === 'className') { el.className = attrs[k]; }
                else if (k === 'style' && typeof attrs[k] === 'object') {
                    for (var sk in attrs[k]) el.style[sk] = attrs[k][sk];
                } else if (k.startsWith('on')) {
                    el.addEventListener(k.slice(2).toLowerCase(), attrs[k]);
                } else if (k === 'dangerousHTML') {
                    el.innerHTML = attrs[k];
                } else {
                    el.setAttribute(k, attrs[k]);
                }
            }
        }
        for (var i = 0; i < children.length; i++) {
            var c = children[i];
            if (c == null || c === false || c === true) continue;
            if (typeof c === 'string' || typeof c === 'number') {
                el.appendChild(document.createTextNode(String(c)));
            } else if (Array.isArray(c)) {
                for (var j = 0; j < c.length; j++) {
                    if (c[j]) el.appendChild(c[j]);
                }
            } else if (c.nodeType) {
                el.appendChild(c);
            }
        }
        return el;
    }

    function clear(el) {
        while (el.firstChild) el.removeChild(el.firstChild);
    }

    function setContent(el) {
        clear(el);
        for (var i = 1; i < arguments.length; i++) {
            var c = arguments[i];
            if (c == null) continue;
            if (typeof c === 'string') { el.appendChild(document.createTextNode(c)); }
            else if (c.nodeType) { el.appendChild(c); }
            else if (Array.isArray(c)) {
                for (var j = 0; j < c.length; j++) {
                    if (c[j] && c[j].nodeType) el.appendChild(c[j]);
                }
            }
        }
    }

    // ── API ──────────────────────────────
    async function api(path, opts) {
        try {
            var headers = (opts && opts.headers) || {};
            if (!headers['Accept']) headers['Accept'] = 'application/json';
            var r = await fetch(path, Object.assign({}, opts, { headers: headers }));
            if (!r.ok) {
                var e = await r.json().catch(function() { return {}; });
                throw new Error(e.error || r.statusText);
            }
            return await r.json();
        } catch(e) { console.error('API:', path, e); return null; }
    }

    // ── Router ───────────────────────────
    var views = { overview: overview, agents: agents, mcp: mcp, orchestrator: orchestrator, arena: arena, flight: flight };
    document.querySelectorAll('[data-view]').forEach(function(a) {
        a.addEventListener('click', function(e) { e.preventDefault(); show(a.dataset.view); });
    });

    function show(name) {
        currentView = name;
        document.querySelectorAll('[data-view]').forEach(function(a) {
            a.classList.toggle('active', a.dataset.view === name);
        });
        document.querySelectorAll('.view').forEach(function(v) {
            v.classList.toggle('active', v.id === 'view-' + name);
        });
        if (views[name]) views[name]();
    }

    // ── Overview ─────────────────────────
    async function overview() {
        var d = await api('/');
        if (!d) return;
        var el = document.getElementById('view-overview');
        setContent(el,
            h('div', { className: 'page-header' },
                h('h2', null, 'System Overview'),
                h('div', { className: 'page-desc' }, 'Real-time runtime intelligence')
            ),
            h('div', { className: 'stat-grid' },
                statCard('Active Agents', String(d.agents || 0)),
                statCard('MCP Servers', String(d.mcp_servers || 0)),
                statCard('MCP Tools', String(d.mcp_tools || 0)),
                statCard('Uptime', d.uptime || '-')
            ),
            h('div', { className: 'card' },
                h('h3', null, 'Quick Actions'),
                h('div', { style: { display: 'flex', gap: '0.75rem', flexWrap: 'wrap', marginTop: '0.5rem' } },
                    h('button', { className: 'btn btn-primary', onClick: function() { show('orchestrator'); } }, 'Launch Agent'),
                    h('button', { className: 'btn btn-outline', onClick: function() { show('agents'); } }, 'View Agents'),
                    h('button', { className: 'btn btn-outline', onClick: function() { show('mcp'); } }, 'MCP Tools')
                )
            )
        );
    }

    function statCard(label, value) {
        return h('div', { className: 'stat-card' },
            h('div', { className: 'label' }, label),
            h('div', { className: 'value' }, value)
        );
    }

    // ── Agents ───────────────────────────
    async function agents() {
        var list = await api('/agents') || [];
        var el = document.getElementById('view-agents');

        if (!list.length) {
            setContent(el,
                h('div', { className: 'page-header' }, h('h2', null, 'Agents')),
                h('div', { className: 'empty-state' }, 'No agents running. Launch from the Orchestrator tab.')
            );
            return;
        }

        var rows = list.map(function(a) {
            return h('tr', null,
                h('td', null, h('strong', null, esc(a.name))),
                h('td', null, badge(a.status)),
                h('td', null,
                    progressBar(a.progress || 0),
                    h('span', { style: { marginLeft: '0.5rem', fontSize: '0.75rem', color: 'var(--text-secondary)' } }, String(a.progress || 0) + '%')
                ),
                h('td', { style: { color: 'var(--text-secondary)' } }, esc(a.duration || '-')),
                h('td', null,
                    h('button', { className: 'btn btn-outline btn-sm', onClick: function() { viewAgent(a.id); } }, 'View')
                )
            );
        });

        setContent(el,
            h('div', { className: 'page-header' },
                h('h2', null, 'Agents'),
                h('div', { className: 'page-desc' }, String(list.length) + ' agent' + (list.length > 1 ? 's' : '') + ' tracked')
            ),
            h('div', { className: 'card' },
                h('table', null,
                    h('thead', null, h('tr', null,
                        h('th', null, 'Name'), h('th', null, 'Status'), h('th', null, 'Progress'), h('th', null, 'Duration'), h('th', null)
                    )),
                    h('tbody', null, rows)
                )
            ),
            h('div', { id: 'agent-detail' })
        );

        if (list.some(function(a) { return a.status === 'running' || a.status === 'pending' || a.status.indexOf('analyzing') !== -1; })) {
            setTimeout(function() { if (currentView === 'agents') agents(); }, 3000);
        }
    }

    function viewAgent(id) {
        api('/agents/' + id).then(function(a) {
            if (!a) return;
            var el = document.getElementById('agent-detail');
            var parts = [
                h('div', { className: 'card result-card' },
                    h('div', { className: 'result-header' },
                        h('h3', null, esc(a.name)),
                        badge(a.status)
                    ),
                    h('div', { style: { display: 'flex', gap: '2rem', marginBottom: '1rem', fontSize: '0.8125rem', color: 'var(--text-secondary)' } },
                        h('span', null, 'Tool: ', h('code', null, esc(a.mcp_tool || '-'))),
                        h('span', null, 'Duration: ', esc(a.duration || '-')),
                        h('span', null, 'Data: ', String(a.raw_data_len || 0), ' bytes')
                    )
                )
            ];
            if (a.error) {
                parts[0].appendChild(
                    h('div', { style: { color: 'var(--danger)', padding: '0.75rem', background: 'var(--danger-glow)', borderRadius: 'var(--radius-sm)', marginBottom: '1rem', fontSize: '0.875rem' } }, esc(a.error))
                );
            }
            parts[0].appendChild(
                a.analysis
                    ? h('div', { className: 'analysis-block' }, esc(a.analysis))
                    : h('div', { className: 'empty-state' }, 'No analysis available')
            );
            setContent(el, parts[0]);
            el.scrollIntoView({ behavior: 'smooth' });
        });
    }

    // ── MCP ──────────────────────────────
    async function mcp() {
        var list = await api('/mcp') || [];
        var el = document.getElementById('view-mcp');

        if (!list.length) {
            setContent(el,
                h('div', { className: 'page-header' }, h('h2', null, 'MCP Tools')),
                h('div', { className: 'empty-state' }, 'No MCP servers connected.')
            );
            return;
        }

        var sections = list.map(function(s) {
            var toolRows = (s.tools || []).map(function(t) {
                return h('tr', null,
                    h('td', null, h('code', null, esc(t.name))),
                    h('td', { style: { color: 'var(--text-secondary)', maxWidth: '500px' } }, truncate(t.description || '-'))
                );
            });
            return [
                h('div', { className: 'page-header' },
                    h('h2', null, esc(s.name)),
                    h('div', { className: 'page-desc' }, badge(s.connected ? 'connected' : 'disconnected'), ' \u00B7 ', String((s.tools || []).length), ' tools')
                ),
                h('div', { className: 'card' },
                    h('table', null,
                        h('thead', null, h('tr', null, h('th', null, 'Tool'), h('th', null, 'Description'))),
                        h('tbody', null, toolRows)
                    )
                )
            ];
        });
        setContent(el, sections.flat());
    }

    // ── Orchestrator ─────────────────────
    async function orchestrator() {
        var list = await api('/agents') || [];
        var completed = list.filter(function(a) { return a.status === 'completed' && a.analysis; });
        var el = document.getElementById('view-orchestrator');

        var completedCards = completed.length > 0 ? completed.map(function(a) {
            return h('div', { className: 'result-card', style: { marginBottom: '1rem', padding: '1rem', borderLeft: '3px solid var(--accent)', background: 'rgba(99,102,241,0.03)', borderRadius: '0 var(--radius-sm) var(--radius-sm) 0' } },
                h('div', { style: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '0.75rem' } },
                    h('strong', null, esc(a.name)),
                    h('span', { style: { fontSize: '0.75rem', color: 'var(--text-secondary)' } }, esc(a.duration || '-'))
                ),
                h('div', { className: 'analysis-block' }, esc((a.analysis || '').slice(0, 3000)))
            );
        }) : [];

        setContent(el,
            h('div', { className: 'page-header' },
                h('h2', null, 'Orchestrator'),
                h('div', { className: 'page-desc' }, 'Create and launch analysis agents')
            ),
            h('div', { className: 'card' },
                h('h3', null, 'Launch Agent'),
                h('div', { className: 'orch-form' },
                    h('div', { className: 'field' },
                        h('label', null, 'Template'),
                        h('select', { id: 'tpl' },
                            h('option', { value: '' }, '-- Custom --'),
                            h('option', { value: 'tpl-structure' }, 'Architecture Review'),
                            h('option', { value: 'tpl-error-review' }, 'Error Handling'),
                            h('option', { value: 'tpl-concurrency' }, 'Concurrency'),
                            h('option', { value: 'tpl-impact' }, 'Change Impact'),
                            h('option', { value: 'tpl-api' }, 'API Surface')
                        )
                    ),
                    h('div', { className: 'field', id: 'custom-fields', style: { display: 'none' } },
                        h('label', null, 'Agent Name'),
                        h('input', { type: 'text', id: 'custom-name', placeholder: 'My Review' })
                    ),
                    h('div', { className: 'field', id: 'custom-tool-field', style: { display: 'none' } },
                        h('label', null, 'MCP Tool'),
                        h('input', { type: 'text', id: 'custom-tool', placeholder: 'codegraph_context' })
                    ),
                    h('div', { className: 'field', id: 'custom-prompt-field', style: { display: 'none' } },
                        h('label', null, 'LLM Prompt'),
                        h('textarea', { id: 'custom-prompt', rows: '2', placeholder: 'Analyze: {{.raw_data}}' })
                    ),
                    h('div', { className: 'field', style: { alignSelf: 'flex-end' } },
                        h('button', { className: 'btn btn-primary', id: 'launch-btn' }, 'Launch')
                    )
                ),
                h('div', { id: 'launch-msg', style: { marginTop: '0.75rem', fontSize: '0.8125rem' } })
            ),
            completed.length > 0
                ? h('div', { className: 'card' },
                      h('h3', null, 'Results (' + String(completed.length) + ')'),
                      completedCards
                  )
                : null
        );

        var tplSelect = document.getElementById('tpl');
        var toggle = function() {
            var isCustom = !tplSelect.value;
            document.getElementById('custom-fields').style.display = isCustom ? 'flex' : 'none';
            document.getElementById('custom-tool-field').style.display = isCustom ? 'flex' : 'none';
            document.getElementById('custom-prompt-field').style.display = isCustom ? 'flex' : 'none';
        };
        tplSelect.addEventListener('change', toggle);
        toggle();

        document.getElementById('launch-btn').addEventListener('click', async function() {
            var btn = document.getElementById('launch-btn');
            var msg = document.getElementById('launch-msg');
            btn.disabled = true;
            msg.textContent = 'Launching...';
            msg.style.color = 'var(--text-secondary)';

            var tid = tplSelect.value;
            var body = {};
            if (tid) {
                body.template_id = tid;
            } else {
                var name = document.getElementById('custom-name').value;
                var tool = document.getElementById('custom-tool').value;
                var prompt = document.getElementById('custom-prompt').value;
                if (!name) { msg.textContent = 'Name is required'; msg.style.color = 'var(--danger)'; btn.disabled = false; return; }
                body.name = name;
                if (tool) body.mcp_tool = tool;
                if (prompt) body.llm_prompt = prompt;
            }

            var r = await api('/agents', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
            btn.disabled = false;
            if (r && r.id) {
                msg.textContent = 'Agent ' + r.id + ' launched!';
                msg.style.color = 'var(--success)';
                setTimeout(function() { orchestrator(); }, 2000);
            } else {
                msg.textContent = 'Failed to launch';
                msg.style.color = 'var(--danger)';
            }
        });
    }

    // ── Arena ─────────────────────────────
    async function arena() {
        var results = await Promise.all([
            api('/arena/stats'),
            api('/arena/history'),
            api('/agents'),
            api('/arena/score'),
        ]);
        var stats = results[0], history = results[1], agents = results[2], score = results[3];
        arenaAgents = agents || [];
        var el = document.getElementById('view-arena');

        var recoveryRate = stats && stats.total_actions > 0
            ? Math.round((stats.successful_actions / stats.total_actions) * 100) : 0;

        var scoreValue = (score && score.score != null) ? score.score.toFixed(1) : '-';
        var scoreGrade = (score && score.grade) || '-';
        var scoreRecovery = (score && score.recovery_rate != null) ? score.recovery_rate.toFixed(1) + '%' : '-';
        var scoreAvgTime = (score && score.avg_recovery_time)
            ? (score.avg_recovery_time / 1000000000).toFixed(1) + 's' : '-';
        var gradeColor = scoreGrade === 'A+' || scoreGrade === 'A' ? '#22c55e'
            : scoreGrade === 'B' ? '#eab308'
            : scoreGrade === 'C' ? '#f97316'
            : scoreGrade === 'D' || scoreGrade === 'F' ? '#ef4444'
            : '#6b7280';

        var historyExists = history && history.length > 0;
        var historyRows = historyExists ? history.slice().reverse().map(function(r) {
            return h('tr', null,
                h('td', null, h('code', null, esc((r.action && r.action.type) || '-'))),
                h('td', null, esc((r.action && r.action.target_id) || (r.action && r.action.source_id) || '-')),
                h('td', null, r.success
                    ? h('span', { className: 'badge badge-success' }, 'success')
                    : h('span', { className: 'badge badge-danger' }, 'failed')),
                h('td', { style: { color: 'var(--text-secondary)' } },
                    r.duration ? Math.round(r.duration / 1000000) + 'ms' : '-')
            );
        }) : [];

        setContent(el,
            h('div', { className: 'page-header' },
                h('h2', null, 'Agent Arena'),
                h('div', { className: 'page-desc' }, 'Chaos engineering \u2014 break it, watch it recover')
            ),
            h('div', { style: { display: 'flex', gap: '1rem', flexWrap: 'wrap', marginBottom: '1rem' } },
                h('div', { className: 'card', style: { flex: '0 0 220px', textAlign: 'center', padding: '1.5rem', border: '2px solid ' + gradeColor } },
                    h('div', { style: { fontSize: '0.8125rem', color: 'var(--text-secondary)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '0.5rem' } }, 'Resilience Score'),
                    h('div', { style: { fontSize: '2.5rem', fontWeight: '700', color: gradeColor } }, scoreValue),
                    h('div', { style: { fontSize: '1.5rem', fontWeight: '600', color: gradeColor, marginBottom: '0.75rem' } }, scoreGrade),
                    h('div', { style: { fontSize: '0.8125rem', color: 'var(--text-secondary)' } }, 'Recovery: ' + scoreRecovery),
                    h('div', { style: { fontSize: '0.8125rem', color: 'var(--text-secondary)' } }, 'Avg Time: ' + scoreAvgTime)
                ),
                h('div', { style: { flex: 1, minWidth: 0 } },
                    h('div', { className: 'stat-grid' },
                        statCard('Recovery Rate', String(recoveryRate) + '%'),
                        statCard('Total Faults', String((stats && stats.total_actions) || 0)),
                        statCard('Recovered', String((stats && stats.successful_actions) || 0)),
                        statCard('Failed', String((stats && stats.failed_actions) || 0))
                    )
                )
            ),
            h('div', { className: 'card' },
                h('h3', null, 'Agent Graph'),
                h('svg', { id: 'dag-svg', width: '100%', height: '200', style: { background: 'var(--bg-primary)', borderRadius: 'var(--radius-sm)' } }),
                h('div', { id: 'arena-selected-info', style: { marginTop: '0.75rem' } })
            ),
            h('div', { className: 'card' },
                h('h3', null, 'Inject Fault'),
                h('div', { style: { display: 'flex', flexWrap: 'wrap', gap: '0.75rem', marginTop: '0.5rem' } },
                    h('button', { className: 'btn btn-danger', style: { background: 'linear-gradient(135deg,#ef4444,#dc2626)', color: 'white' }, onClick: function() { arenaAction('kill_leader'); } },
                        '\u2620 Assassinate Leader'
                    ),
                    h('button', { className: 'btn btn-danger', style: { background: 'linear-gradient(135deg,#f97316,#ea580c)', color: 'white' }, onClick: function() { arenaKillOrchestrator(); } },
                        '\u2699 Kill Orchestrator'
                    ),
                    h('select', { id: 'arena-agent-select', style: { padding: '0.625rem 1rem', background: 'var(--bg-primary)', border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', color: 'var(--text-primary)' } },
                        h('option', { value: '' }, '-- Select Agent --'),
                        (agents || []).map(function(a) {
                            return h('option', { value: a.id }, esc(a.name), ' (', esc(a.status), ')');
                        })
                    ),
                    h('button', { className: 'btn btn-outline', style: { borderColor: 'var(--danger)', color: 'var(--danger)' }, onClick: function() { arenaKillAgent(); } },
                        '\uD83D\uDD25 Kill Agent'
                    )
                ),
                h('div', { style: { display: 'flex', flexWrap: 'wrap', gap: '0.75rem', marginTop: '0.75rem' } },
                    h('input', { type: 'text', id: 'arena-node-id', placeholder: 'Node ID', style: { padding: '0.625rem 1rem', background: 'var(--bg-primary)', border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', color: 'var(--text-primary)' } }),
                    h('button', { className: 'btn btn-outline', onClick: function() { arenaRemoveNode(); } }, '\uD83D\uDCA3 Remove Node'),
                    h('input', { type: 'text', id: 'arena-edge-from', placeholder: 'From', style: { width: '100px', padding: '0.625rem 1rem', background: 'var(--bg-primary)', border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', color: 'var(--text-primary)' } }),
                    h('input', { type: 'text', id: 'arena-edge-to', placeholder: 'To', style: { width: '100px', padding: '0.625rem 1rem', background: 'var(--bg-primary)', border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', color: 'var(--text-primary)' } }),
                    h('button', { className: 'btn btn-outline', onClick: function() { arenaRemoveEdge(); } }, '\u2716 Remove Edge'),
                    h('button', { className: 'btn btn-outline', onClick: function() { arenaPauseAgent(); } }, '\u23F8 Pause Agent'),
                    h('button', { className: 'btn btn-outline', onClick: function() { arenaResumeAgent(); } }, '\u25B6 Resume Agent'),
                    h('button', { className: 'btn btn-outline', onClick: function() { arenaSlowAgent(); } }, '\uD83D\uDC0C Slow Agent'),
                    h('button', { className: 'btn btn-outline', onClick: function() { arenaNetworkPartition(); } }, '\uD83D\uDDE1 Network Partition')
                ),
                h('div', { id: 'arena-action-result', style: { marginTop: '0.75rem', fontSize: '0.8125rem' } })
            ),
            h('div', { className: 'card' },
                h('h3', null, 'Action History'),
                h('div', { id: 'arena-history' },
                    historyExists
                        ? h('table', null,
                              h('thead', null, h('tr', null,
                                  h('th', null, 'Action'), h('th', null, 'Target'), h('th', null, 'Result'), h('th', null, 'Duration')
                              )),
                              h('tbody', null, historyRows)
                          )
                        : h('div', { className: 'empty-state' }, 'No actions yet. Inject a fault above.')
                )
            )
        );

        setTimeout(function() { if (currentView === 'arena') arena(); }, 3000);

        renderDAG(arenaAgents);
        var agentSelect = document.getElementById('arena-agent-select');
        if (agentSelect) {
            if (selectedAgentId) agentSelect.value = selectedAgentId;
            agentSelect.addEventListener('change', function() {
                var id = agentSelect.value;
                if (id) {
                    var a = arenaAgents.find(function(ag) { return ag.id === id; });
                    selectedAgentId = id;
                    updateSelectedInfo(a || null);
                } else {
                    selectedAgentId = null;
                    updateSelectedInfo(null);
                }
                renderDAG(arenaAgents);
            });
        }
        if (selectedAgentId) {
            var a = arenaAgents.find(function(ag) { return ag.id === selectedAgentId; });
            updateSelectedInfo(a || null);
        }
    }

    function arenaAction(type) {
        var el = document.getElementById('arena-action-result');
        el.textContent = 'Injecting...';
        el.style.color = 'var(--text-secondary)';
        api('/arena/leader/kill', { method: 'POST' }).then(function(r) {
            setContent(el,
                r && r.success
                    ? h('span', { className: 'badge badge-success' }, 'Leader killed \u2014 watching recovery...')
                    : h('span', { className: 'badge badge-danger' }, 'Failed: ' + esc((r && r.error) || 'unknown'))
            );
            setTimeout(function() { if (currentView === 'arena') arena(); }, 1000);
        });
    }

    function arenaKillAgent() {
        var id = document.getElementById('arena-agent-select').value || selectedAgentId;
        if (!id) { alert('Select an agent first (use dropdown or click a node)'); return; }
        var el = document.getElementById('arena-action-result');
        el.textContent = 'Killing agent...';
        el.style.color = 'var(--text-secondary)';
        api('/arena/agent/' + id + '/kill', { method: 'POST' }).then(function(r) {
            setContent(el,
                r && r.success
                    ? h('span', { className: 'badge badge-success' }, 'Agent ', esc(id), ' killed \u2014 resurrection pending...')
                    : h('span', { className: 'badge badge-danger' }, 'Failed: ' + esc((r && r.error) || 'unknown'))
            );
            setTimeout(function() { if (currentView === 'arena') arena(); }, 1000);
        });
    }

    function arenaRemoveNode() {
        var id = document.getElementById('arena-node-id').value;
        if (!id) { alert('Enter a node ID'); return; }
        var el = document.getElementById('arena-action-result');
        el.textContent = 'Removing node...';
        api('/arena/node/' + id + '/remove', { method: 'POST' }).then(function(r) {
            setContent(el,
                r && r.success
                    ? h('span', { className: 'badge badge-success' }, 'Node ', esc(id), ' removed')
                    : h('span', { className: 'badge badge-danger' }, 'Failed: ' + esc((r && r.error) || 'unknown'))
            );
            setTimeout(function() { if (currentView === 'arena') arena(); }, 1000);
        });
    }

    function arenaRemoveEdge() {
        var from = document.getElementById('arena-edge-from').value;
        var to = document.getElementById('arena-edge-to').value;
        if (!from || !to) { alert('Enter both From and To'); return; }
        var el = document.getElementById('arena-action-result');
        el.textContent = 'Removing edge...';
        api('/arena/edge/remove', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ from: from, to: to }),
        }).then(function(r) {
            setContent(el,
                r && r.success
                    ? h('span', { className: 'badge badge-success' }, 'Edge ', esc(from), '\u2192', esc(to), ' removed')
                    : h('span', { className: 'badge badge-danger' }, 'Failed: ' + esc((r && r.error) || 'unknown'))
            );
            setTimeout(function() { if (currentView === 'arena') arena(); }, 1000);
        });
    }

    function arenaPauseAgent() {
        var id = document.getElementById('arena-agent-select').value || selectedAgentId;
        if (!id) { alert('Select an agent first'); return; }
        var el = document.getElementById('arena-action-result');
        el.textContent = 'Pausing agent...';
        api('/arena/agent/' + id + '/pause', { method: 'POST' }).then(function(r) {
            setContent(el,
                r && r.success
                    ? h('span', { className: 'badge badge-warning' }, 'Agent ', esc(id), ' paused')
                    : h('span', { className: 'badge badge-danger' }, 'Failed: ' + esc((r && r.error) || 'unknown'))
            );
            setTimeout(function() { if (currentView === 'arena') arena(); }, 1000);
        });
    }

    function arenaResumeAgent() {
        var id = document.getElementById('arena-agent-select').value || selectedAgentId;
        if (!id) { alert('Select an agent first'); return; }
        var el = document.getElementById('arena-action-result');
        el.textContent = 'Resuming agent...';
        api('/arena/agent/' + id + '/resume', { method: 'POST' }).then(function(r) {
            setContent(el,
                r && r.success
                    ? h('span', { className: 'badge badge-success' }, 'Agent ', esc(id), ' resumed')
                    : h('span', { className: 'badge badge-danger' }, 'Failed: ' + esc((r && r.error) || 'unknown'))
            );
            setTimeout(function() { if (currentView === 'arena') arena(); }, 1000);
        });
    }

    function arenaSlowAgent() {
        var id = document.getElementById('arena-agent-select').value || selectedAgentId;
        if (!id) { alert('Select an agent first'); return; }
        var el = document.getElementById('arena-action-result');
        el.textContent = 'Slowing agent...';
        var delay = prompt('Delay in seconds (default: 5):', '5');
        if (!delay) return;
        api('/arena/agent/' + id + '/slow', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ duration: delay + 's' }),
        }).then(function(r) {
            setContent(el,
                r && r.success
                    ? h('span', { className: 'badge badge-warning' }, 'Agent ', esc(id), ' slowed by ', delay, 's')
                    : h('span', { className: 'badge badge-danger' }, 'Failed: ' + esc((r && r.error) || 'unknown'))
            );
            setTimeout(function() { if (currentView === 'arena') arena(); }, 1000);
        });
    }

    function arenaKillOrchestrator() {
        var el = document.getElementById('arena-action-result');
        el.textContent = 'Killing orchestrator...';
        el.style.color = 'var(--text-secondary)';
        api('/arena/orchestrator/kill', { method: 'POST' }).then(function(r) {
            setContent(el,
                r && r.success
                    ? h('span', { className: 'badge badge-success' }, 'Orchestrator killed \u2014 watching recovery...')
                    : h('span', { className: 'badge badge-danger' }, 'Failed: ' + esc((r && r.error) || 'unknown'))
            );
            setTimeout(function() { if (currentView === 'arena') arena(); }, 1000);
        });
    }

    function arenaNetworkPartition() {
        var id = document.getElementById('arena-agent-select').value || selectedAgentId;
        if (!id) { alert('Select an agent first'); return; }
        var el = document.getElementById('arena-action-result');
        el.textContent = 'Partitioning network...';
        el.style.color = 'var(--text-secondary)';
        api('/arena/agent/' + id + '/partition', { method: 'POST' }).then(function(r) {
            setContent(el,
                r && r.success
                    ? h('span', { className: 'badge badge-warning' }, 'Agent ', esc(id), ' network partitioned')
                    : h('span', { className: 'badge badge-danger' }, 'Failed: ' + esc((r && r.error) || 'unknown'))
            );
            setTimeout(function() { if (currentView === 'arena') arena(); }, 1000);
        });
    }

    // ── Flight Recorder ─────────────────
    async function flight() {
        var el = document.getElementById('view-flight');
        setContent(el,
            h('div', { className: 'page-header' },
                h('h2', null, 'Flight Recorder'),
                h('div', { className: 'page-desc' }, 'Timeline, call graph, decisions, and diagnostics')
            ),
            h('div', { id: 'flight-loading', className: 'empty-state' }, 'Loading flight data...')
        );

        var results = await Promise.all([
            api('/flight/summary'),
            api('/flight/graph'),
            api('/flight/decisions'),
            api('/flight/diagnostics'),
            api('/flight/genealogy'),
        ]);
        var summary = results[0], graphData = results[1], decisions = results[2], diagData = results[3], genealogyData = results[4];

        var container = document.getElementById('flight-loading');
        if (!container) return;
        container.remove();

        setContent(el,
            h('div', { className: 'page-header' },
                h('h2', null, 'Flight Recorder'),
                h('div', { className: 'page-desc' }, 'Timeline, call graph, decisions, and diagnostics')
            ),
            renderTimelineSection(summary),
            renderGraphSection(graphData),
            renderDecisionsSection(decisions),
            renderDiagnosticsSection(diagData),
            renderGenealogySection(genealogyData)
        );

        renderMermaidDiagrams();
    }

    function renderTimelineSection(summary) {
        if (!summary || summary.event_count === 0) {
            return h('div', { className: 'card' },
                h('h3', null, 'Timeline'),
                h('div', { className: 'empty-state' }, 'No timeline events recorded yet.')
            );
        }

        var toolPct = summary.tool_percent || 0;
        var llmPct = summary.llm_percent || 0;
        var waitPct = summary.wait_percent || 0;
        var otherPct = Math.max(0, 100 - toolPct - llmPct - waitPct);

        return h('div', { className: 'card' },
            h('h3', null, 'Timeline \u2014 Time Distribution'),
            h('div', { className: 'timeline-chart' },
                timelineRow('Tool Calls', toolPct, 'linear-gradient(90deg,var(--accent),#a78bfa)'),
                timelineRow('LLM Calls', llmPct, 'linear-gradient(90deg,var(--success),#34d399)'),
                timelineRow('Waiting', waitPct, 'linear-gradient(90deg,var(--warning),#fbbf24)'),
                timelineRow('Other', otherPct, 'linear-gradient(90deg,var(--text-muted),var(--text-secondary))')
            ),
            h('div', { className: 'timeline-stats' },
                h('span', null, 'Events: ', h('strong', null, String(summary.event_count))),
                h('span', null, 'Total: ', h('strong', null, formatDuration(summary.total_duration)))
            )
        );
    }

    function timelineRow(label, pct, color) {
        return h('div', { className: 'timeline-row' },
            h('span', { className: 'timeline-label' }, label),
            h('div', { className: 'timeline-bar-track' },
                h('div', { className: 'timeline-bar', style: { width: String(pct) + '%', background: color } })
            ),
            h('span', { className: 'timeline-pct' }, pct.toFixed(1) + '%')
        );
    }

    function renderGraphSection(graphData) {
        var mermaidText = (graphData && graphData.mermaid) || '';
        if (!mermaidText || mermaidText.indexOf('No data') !== -1) {
            return h('div', { className: 'card' },
                h('h3', null, 'Call Graph'),
                h('div', { className: 'empty-state' }, 'No call graph data recorded yet.')
            );
        }
        return h('div', { className: 'card' },
            h('h3', null, 'Call Graph'),
            h('div', { className: 'mermaid', dangerousHTML: esc(mermaidText) }, mermaidText)
        );
    }

    function renderDecisionsSection(decisions) {
        if (!decisions || decisions.length === 0) {
            return h('div', { className: 'card' },
                h('h3', null, 'Decisions'),
                h('div', { className: 'empty-state' }, 'No decisions recorded yet.')
            );
        }

        var rows = decisions.map(function(d) {
            return h('tr', null,
                h('td', null, h('code', null, esc(d.agent_id || '-'))),
                h('td', null, badge(d.type || '-')),
                h('td', null, h('strong', null, esc(d.selected || '-'))),
                h('td', { style: { maxWidth: '300px', color: 'var(--text-secondary)' } }, truncate(d.reason || '-')),
                h('td', null,
                    h('div', { className: 'confidence-bar' },
                        h('div', { className: 'confidence-fill', style: { width: String(((d.confidence || 0) * 100)) + '%' } })
                    ),
                    h('span', { className: 'confidence-pct' }, String(((d.confidence || 0) * 100).toFixed(0)) + '%')
                )
            );
        });

        return h('div', { className: 'card' },
            h('h3', null, 'Decisions (' + String(decisions.length) + ')'),
            h('div', { className: 'table-scroll' },
                h('table', { className: 'decision-table' },
                    h('thead', null, h('tr', null,
                        h('th', null, 'Agent'), h('th', null, 'Type'), h('th', null, 'Selected'),
                        h('th', null, 'Reason'), h('th', null, 'Confidence')
                    )),
                    h('tbody', null, rows)
                )
            )
        );
    }

    function renderDiagnosticsSection(diagData) {
        var records = (diagData && diagData.records) || [];
        var dist = (diagData && diagData.distribution) || {};
        var categories = dist.categories || {};
        var catKeys = Object.keys(categories);

        if (records.length === 0 && catKeys.length === 0) {
            return h('div', { className: 'card' },
                h('h3', null, 'Diagnostics'),
                h('div', { className: 'empty-state' }, 'No diagnostic records yet.')
            );
        }

        var percentages = dist.percentages || {};
        var total = dist.total || 0;
        var pieColors = ['var(--danger)', 'var(--warning)', 'var(--info)', 'var(--success)', 'var(--accent)', 'var(--text-muted)', '#a78bfa', '#f472b6'];

        var conicParts = [];
        var acc = 0;
        catKeys.forEach(function(cat, i) {
            var pct = percentages[cat] || 0;
            var color = pieColors[i % pieColors.length];
            conicParts.push(color + ' ' + String(acc) + '% ' + String(acc + pct) + '%');
            acc += pct;
        });
        var conicCSS = conicParts.length > 0
            ? 'conic-gradient(' + conicParts.join(', ') + ')'
            : 'conic-gradient(var(--text-muted) 0% 100%)';

        var categoryRows = catKeys.map(function(cat, i) {
            var count = categories[cat] || 0;
            var pct = percentages[cat] || 0;
            var color = pieColors[i % pieColors.length];
            return h('tr', null,
                h('td', null, h('span', { className: 'dot-indicator', style: { background: color } }), esc(cat)),
                h('td', null, String(count)),
                h('td', null, pct.toFixed(1) + '%')
            );
        });

        return h('div', { className: 'card' },
            h('h3', null, 'Diagnostics'),
            h('div', { className: 'diagnostics-card' },
                h('div', { className: 'diagnostics-pie-wrap' },
                    h('div', { className: 'pie-chart', style: { background: conicCSS } }),
                    h('div', { className: 'pie-center' }, String(total))
                ),
                h('div', { className: 'diagnostics-table-wrap' },
                    h('table', { className: 'diagnostics-table' },
                        h('thead', null, h('tr', null, h('th', null, 'Category'), h('th', null, 'Count'), h('th', null, 'Percentage'))),
                        h('tbody', null, categoryRows)
                    )
                )
            )
        );
    }

    function renderGenealogySection(genealogyData) {
        var mermaidText = (genealogyData && genealogyData.mermaid) || '';
        if (!mermaidText || mermaidText.indexOf('No agents') !== -1) {
            return h('div', { className: 'card' },
                h('h3', null, 'Genealogy'),
                h('div', { className: 'empty-state' }, 'No genealogy data recorded yet.')
            );
        }
        return h('div', { className: 'card' },
            h('h3', null, 'Genealogy Tree'),
            h('div', { className: 'mermaid', dangerousHTML: esc(mermaidText) })
        );
    }

    function renderMermaidDiagrams() {
        if (typeof mermaid === 'undefined') return;
        try {
            mermaid.initialize({ startOnLoad: false, theme: 'dark' });
            document.querySelectorAll('.mermaid[data-mermaid]').forEach(function(el, i) {
                var text = el.getAttribute('data-mermaid');
                if (!text) return;
                try {
                    var id = 'mermaid-graph-' + String(i);
                    mermaid.render(id, text).then(function(res) {
                        el.innerHTML = res.svg;
                        el.removeAttribute('data-mermaid');
                    }).catch(function(err) {
                        console.error('Mermaid render error:', err);
                        el.innerHTML = '<div class="empty-state">Failed to render diagram</div>';
                    });
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
        if (typeof d === 'string') return d;
        if (typeof d === 'number') {
            if (d > 1e9) return (d / 1e9).toFixed(2) + 's';
            if (d > 1e6) return (d / 1e6).toFixed(1) + 'ms';
            if (d > 1e3) return (d / 1e3).toFixed(1) + '\u00B5s';
            return String(d) + 'ns';
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
                              (a.status.indexOf('running') !== -1 || a.status.indexOf('analyzing') !== -1 || a.status === 'pending') ? 'var(--warning)' :
                              'var(--info)';

            var isSelected = selectedAgentId === a.id;

            var g = document.createElementNS(SVG_NS, 'g');
            g.setAttribute('data-agent-id', a.id);
            g.style.cursor = 'pointer';

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

            var circle = document.createElementNS(SVG_NS, 'circle');
            circle.setAttribute('cx', x);
            circle.setAttribute('cy', y);
            circle.setAttribute('r', isSelected ? '30' : '26');
            circle.setAttribute('fill', statusColor);
            circle.setAttribute('fill-opacity', '0.15');
            circle.setAttribute('stroke', isSelected ? 'var(--accent)' : statusColor);
            circle.setAttribute('stroke-width', isSelected ? '3' : '2');
            g.appendChild(circle);

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

            var status = document.createElementNS(SVG_NS, 'text');
            status.setAttribute('x', x);
            status.setAttribute('y', y + 42);
            status.setAttribute('text-anchor', 'middle');
            status.setAttribute('fill', statusColor);
            status.setAttribute('font-size', '10');
            status.style.pointerEvents = 'none';
            status.textContent = a.status;
            g.appendChild(status);

            g.addEventListener('click', function() { selectAgent(a); });
            svg.appendChild(g);
        });

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
        var select = document.getElementById('arena-agent-select');
        if (select) select.value = agent.id;
        updateSelectedInfo(agent);
        renderDAG(arenaAgents);
    }

    function updateSelectedInfo(agent) {
        var el = document.getElementById('arena-selected-info');
        if (!el) return;
        if (!agent) { setContent(el); return; }

        var statusColor = agent.status === 'completed' ? 'var(--success)' :
                          agent.status === 'failed' ? 'var(--danger)' :
                          (agent.status.indexOf('running') !== -1 || agent.status.indexOf('analyzing') !== -1 || agent.status === 'pending') ? 'var(--warning)' :
                          'var(--info)';

        setContent(el,
            h('div', { style: { display: 'flex', alignItems: 'center', gap: '0.75rem', padding: '0.5rem 0.75rem', background: 'var(--bg-primary)', border: '1px solid var(--accent)', borderRadius: 'var(--radius-sm)', fontSize: '0.8125rem' } },
                h('span', { style: { width: '10px', height: '10px', borderRadius: '50%', background: statusColor, flexShrink: 0 } }),
                h('strong', null, esc(agent.name)),
                h('span', { style: { color: 'var(--text-secondary)' } }, esc(agent.status)),
                agent.duration ? h('span', { style: { color: 'var(--text-muted)' } }, esc(agent.duration)) : null,
                h('span', { style: { color: 'var(--text-muted)', marginLeft: 'auto' } }, 'ID: ', esc(agent.id))
            )
        );
    }

    // ── WebSocket ────────────────────────
    function connectWS() {
        if (ws && ws.readyState === WebSocket.OPEN) return;
        var proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
        ws = new WebSocket(proto + '//' + location.host + '/ws');
        ws.onopen = function() {
            ws.send(JSON.stringify({ type: 'subscribe', channel: 'agents' }));
            ws.send(JSON.stringify({ type: 'subscribe', channel: 'events' }));
        };
        ws.onmessage = function(e) {
            try {
                var msg = JSON.parse(e.data);
                if (msg.type === 'agent_update' && currentView === 'agents') agents();
            } catch(_) {}
        };
        ws.onclose = function() { setTimeout(connectWS, 5000); };
    }

    // ── Helpers ──────────────────────────
    function esc(s) { if (!s) return ''; var d = document.createElement('div'); d.textContent = s; return d.innerHTML; }

    function truncate(s) { if (!s || s.length <= 120) return s; return s.slice(0, 117) + '...'; }

    function badge(s) {
        if (!s) return '';
        var cls = s === 'completed' ? 'badge-success' :
                  s === 'failed' ? 'badge-danger' :
                  (s.indexOf('running') !== -1 || s.indexOf('analyzing') !== -1 || s === 'pending') ? 'badge-warning' : 'badge-info';
        return h('span', { className: 'badge ' + cls }, esc(s));
    }

    function progressBar(pct) {
        return h('div', { className: 'progress-bar' },
            h('div', { className: 'fill', style: { width: String(pct) + '%' } })
        );
    }

    // ── Init ─────────────────────────────
    show('overview');
    connectWS();
})();
