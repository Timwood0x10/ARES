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
    var arenaEventSource = null;
    var arenaSSEReconnectDelay = 1000;
    var arenaEventLogEntries = [];
    var survivalPollTimer = null;

    async function arena() {
        var results = await Promise.all([
            api('/arena/stats'),
            api('/arena/history'),
            api('/agents'),
            api('/arena/score'),
            api('/arena/metrics'),
        ]);
        var stats = results[0], history = results[1], agents = results[2], score = results[3], metrics = results[4];
        arenaAgents = agents || [];
        var el = document.getElementById('view-arena');

        var recoveryRate = stats && stats.total_actions > 0
            ? Math.round((stats.successful_actions / stats.total_actions) * 100) : 0;

        var scoreValue = (score && score.score != null) ? parseFloat(score.score) : null;
        var scoreGrade = (score && score.grade) || '-';
        var scoreRecovery = (score && score.recovery_rate != null) ? score.recovery_rate.toFixed(1) + '%' : '-';
        var scoreAvgTime = (score && score.avg_recovery_time)
            ? (score.avg_recovery_time / 1000000000).toFixed(1) + 's' : '-';
        var gradeColor = scoreGrade === 'A+' || scoreGrade === 'A' ? '#22c55e'
            : scoreGrade === 'B' ? '#eab308'
            : scoreGrade === 'C' ? '#f97316'
            : scoreGrade === 'D' || scoreGrade === 'F' ? '#ef4444'
            : '#6b7280';

        var dimAvailability = (score && score.dimensions && score.dimensions.availability != null) ? parseFloat(score.dimensions.availability) : 0;
        var dimRecovery = (score && score.dimensions && score.dimensions.recovery != null) ? parseFloat(score.dimensions.recovery) : 0;
        var dimConsistency = (score && score.dimensions && score.dimensions.consistency != null) ? parseFloat(score.dimensions.consistency) : 0;

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

        var hasMetrics = metrics && typeof metrics === 'object';
        var metricsContent = hasMetrics
            ? h('div', { style: { display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(140px,1fr))', gap: '0.75rem' } },
                h('div', { style: { textAlign: 'center', padding: '0.5rem 0' } },
                    h('div', { style: { fontSize: '0.7rem', color: 'var(--text-secondary)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '0.25rem' } }, 'Avg Recovery'),
                    h('div', { style: { fontSize: '1.25rem', fontWeight: '700', color: 'var(--accent)' } }, metrics.avg_recovery_time != null ? (metrics.avg_recovery_time / 1000000000).toFixed(1) + 's' : 'N/A')
                ),
                h('div', { style: { textAlign: 'center', padding: '0.5rem 0' } },
                    h('div', { style: { fontSize: '0.7rem', color: 'var(--text-secondary)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '0.25rem' } }, 'Failovers'),
                    h('div', { style: { fontSize: '1.25rem', fontWeight: '700', color: 'var(--warning)' } }, String(metrics.failover_count || 0))
                ),
                h('div', { style: { textAlign: 'center', padding: '0.5rem 0' } },
                    h('div', { style: { fontSize: '0.7rem', color: 'var(--text-secondary)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '0.25rem' } }, 'Min Time'),
                    h('div', { style: { fontSize: '1.25rem', fontWeight: '700', color: 'var(--success)' } }, metrics.min_recovery_time != null ? (metrics.min_recovery_time / 1000000000).toFixed(1) + 's' : 'N/A')
                ),
                h('div', { style: { textAlign: 'center', padding: '0.5rem 0' } },
                    h('div', { style: { fontSize: '0.7rem', color: 'var(--text-secondary)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '0.25rem' } }, 'Max Time'),
                    h('div', { style: { fontSize: '1.25rem', fontWeight: '700', color: 'var(--danger)' } }, metrics.max_recovery_time != null ? (metrics.max_recovery_time / 1000000000).toFixed(1) + 's' : 'N/A')
                )
              )
            : null;

        setContent(el,
            h('div', { className: 'page-header' },
                h('h2', null, 'Agent Arena'),
                h('div', { className: 'page-desc' }, 'Chaos engineering \u2014 break it, watch it recover')
            ),
            h('div', { style: { display: 'flex', gap: '1rem', flexWrap: 'wrap', marginBottom: '1rem' } },
                h('div', { className: 'card', id: 'arena-score-card', style: { flex: '0 0 260px', padding: '1.5rem', border: '2px solid ' + gradeColor, position: 'relative', overflow: 'hidden' } },
                    h('div', { style: { position: 'absolute', top: '-30px', right: '-30px', width: '100px', height: '100px', background: gradeColor, opacity: 0.08, borderRadius: '50%' } }),
                    h('div', { style: { position: 'absolute', bottom: '-40px', left: '-20px', width: '80px', height: '80px', background: gradeColor, opacity: 0.06, borderRadius: '50%' } }),
                    h('div', { style: { display: 'flex', alignItems: 'center', gap: '1rem', marginBottom: '0.75rem' } },
                        h('div', { className: 'score-ring' },
                            h('svg', { width: '90', height: '90', viewBox: '0 0 120 120' },
                                h('circle', { cx: '60', cy: '60', r: '52', fill: 'none', stroke: 'var(--bg-primary)', strokeWidth: '8' }),
                                h('circle', {
                                    cx: '60', cy: '60', r: '52', fill: 'none',
                                    stroke: gradeColor, strokeWidth: '8',
                                    strokeLinecap: 'round',
                                    strokeDasharray: String(327 * ((scoreValue || 0) / 100)) + ' 327',
                                    transform: 'rotate(-90 60 60)',
                                    style: { transition: 'stroke-dasharray 0.8s ease' }
                                }),
                                h('text', { id: 'arena-score-value', x: '60', y: '58', textAnchor: 'middle', dominantBaseline: 'middle', fill: gradeColor, fontSize: '22', fontWeight: '700' }, scoreValue != null ? String(Math.round(scoreValue)) : '-'),
                                h('text', { x: '60', y: '76', textAnchor: 'middle', dominantBaseline: 'middle', fill: 'var(--text-muted)', fontSize: '9', fontWeight: '500' }, '/100')
                            )
                        ),
                        h('div', null,
                            h('div', { style: { fontSize: '0.75rem', color: 'var(--text-secondary)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '0.15rem' } }, 'Resilience Score'),
                            h('span', { id: 'arena-score-grade', className: 'grade-badge-large', style: { background: gradeColor, boxShadow: '0 0 20px ' + gradeColor + '40', color: '#fff' } }, scoreGrade)
                        )
                    ),
                    h('div', { style: { marginTop: '0.75rem' } },
                        h('div', { style: { fontSize: '0.7rem', color: 'var(--text-secondary)', marginBottom: '0.35rem', textTransform: 'uppercase', letterSpacing: '0.04em' } }, 'Dimension Breakdown'),
                        h('div', { className: 'dimension-bar' },
                            h('div', { style: { flex: '0 0 33.33%', textAlign: 'center' } },
                                h('div', { style: { height: '6px', borderRadius: '3px', background: 'var(--bg-primary)', overflow: 'hidden', marginBottom: '0.2rem' } },
                                    h('div', { style: { width: String(dimAvailability) + '%', height: '100%', borderRadius: '3px', background: 'linear-gradient(90deg,#3b82f6,#60a5fa)', transition: 'width 0.6s ease' } })
                                ),
                                h('div', { style: { fontSize: '0.65rem', color: '#60a5fa', fontWeight: '600' } }, 'Avail ' + dimAvailability.toFixed(0) + '%')
                            ),
                            h('div', { style: { flex: '0 0 33.33%', textAlign: 'center' } },
                                h('div', { style: { height: '6px', borderRadius: '3px', background: 'var(--bg-primary)', overflow: 'hidden', marginBottom: '0.2rem' } },
                                    h('div', { style: { width: String(dimRecovery) + '%', height: '100%', borderRadius: '3px', background: 'linear-gradient(90deg,#22c55e,#4ade80)', transition: 'width 0.6s ease' } })
                                ),
                                h('div', { style: { fontSize: '0.65rem', color: '#4ade80', fontWeight: '600' } }, 'Recov ' + dimRecovery.toFixed(0) + '%')
                            ),
                            h('div', { style: { flex: '0 0 33.33%', textAlign: 'center' } },
                                h('div', { style: { height: '6px', borderRadius: '3px', background: 'var(--bg-primary)', overflow: 'hidden', marginBottom: '0.2rem' } },
                                    h('div', { style: { width: String(dimConsistency) + '%', height: '100%', borderRadius: '3px', background: 'linear-gradient(90deg,#a78bfa,#c4b5fd)', transition: 'width 0.6s ease' } })
                                ),
                                h('div', { style: { fontSize: '0.65rem', color: '#c4b5fd', fontWeight: '600' } }, 'Consi ' + dimConsistency.toFixed(0) + '%')
                            )
                        )
                    ),
                    h('div', { style: { display: 'flex', justifyContent: 'space-between', marginTop: '0.75rem', paddingTop: '0.75rem', borderTop: '1px solid var(--border)', fontSize: '0.8125rem', color: 'var(--text-secondary)' } },
                        h('span', null, 'Recovery: ', h('strong', { style: { color: gradeColor } }, scoreRecovery)),
                        h('span', null, 'Avg Time: ', h('strong', null, scoreAvgTime))
                    )
                ),
                h('div', { style: { flex: 1, minWidth: 0 } },
                    h('div', { className: 'stat-grid' },
                        h('div', { className: 'stat-card' }, h('div', { className: 'label' }, 'Recovery Rate'), h('div', { className: 'value arena-stat-value' }, String(recoveryRate) + '%')),
                        h('div', { className: 'stat-card' }, h('div', { className: 'label' }, 'Total Faults'), h('div', { className: 'value arena-stat-value' }, String((stats && stats.total_actions) || 0))),
                        h('div', { className: 'stat-card' }, h('div', { className: 'label' }, 'Recovered'), h('div', { className: 'value arena-stat-value' }, String((stats && stats.successful_actions) || 0))),
                        h('div', { className: 'stat-card' }, h('div', { className: 'label' }, 'Failed'), h('div', { className: 'value arena-stat-value' }, String((stats && stats.failed_actions) || 0)))
                    ),
                    metricsContent
                        ? h('div', { className: 'card', style: { marginTop: '1rem' } },
                              h('h3', null, 'Metrics'),
                              metricsContent
                          )
                        : null
                )
            ),
            h('div', { className: 'card' },
                h('h3', null, 'Agent Topology'),
                h('svg', { id: 'dag-svg', width: '100%', height: '320', style: { background: 'var(--bg-primary)', borderRadius: 'var(--radius-sm)' } }),
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
                h('div', { style: { display: 'flex', flexWrap: 'wrap', gap: '0.75rem', marginTop: '0.75rem' } },
                    h('button', { className: 'btn btn-outline', style: { background: 'linear-gradient(135deg,#f97316,#ea580c)', color: 'white', border: 'none' }, onClick: function() { arenaToolTimeout(); } },
                        '\u23F3 Tool Timeout'
                    ),
                    h('button', { className: 'btn btn-outline', style: { background: 'linear-gradient(135deg,#ef4444,#dc2626)', color: 'white', border: 'none' }, onClick: function() { arenaMemoryCorrupt(); } },
                        '\uD83D\uDCDA Memory Corrupt'
                    ),
                    h('button', { className: 'btn btn-outline', style: { background: 'linear-gradient(135deg,#a78bfa,#8b5cf6)', color: 'white', border: 'none' }, onClick: function() { arenaMCPDisconnect(); } },
                        '\uD83D\uDCF1 MCP Disconnect'
                    ),
                    h('button', { className: 'btn btn-outline', style: { background: 'linear-gradient(135deg,#f472b6,#ec4899)', color: 'white', border: 'none' }, onClick: function() { arenaLLMFailure(); } },
                        '\uD83E\uDDE0 LLM Failure'
                    )
                ),
                h('div', { id: 'arena-action-result', style: { marginTop: '0.75rem', fontSize: '0.8125rem' } })
            ),
            h('div', { className: 'card survival-panel' },
                h('h3', null, 'Survival Mode'),
                h('div', { style: { display: 'flex', flexWrap: 'wrap', gap: '0.75rem', alignItems: 'flex-end', marginTop: '0.5rem' } },
                    h('div', { style: { display: 'flex', flexDirection: 'column', gap: '0.25rem' } },
                        h('label', { style: { fontSize: '0.7rem', color: 'var(--text-secondary)' } }, 'Duration'),
                        h('input', { type: 'text', id: 'survival-duration', value: '30m', placeholder: '30m', style: { padding: '0.5rem 0.75rem', background: 'var(--bg-primary)', border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', color: 'var(--text-primary)', width: '80px', fontSize: '0.8125rem' } })
                    ),
                    h('div', { style: { display: 'flex', flexDirection: 'column', gap: '0.25rem' } },
                        h('label', { style: { fontSize: '0.7rem', color: 'var(--text-secondary)' } }, 'Interval'),
                        h('input', { type: 'text', id: 'survival-interval', value: '10s', placeholder: '10s', style: { padding: '0.5rem 0.75rem', background: 'var(--bg-primary)', border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', color: 'var(--text-primary)', width: '80px', fontSize: '0.8125rem' } })
                    ),
                    h('div', { style: { display: 'flex', flexDirection: 'column', gap: '0.25rem' } },
                        h('label', { style: { fontSize: '0.7rem', color: 'var(--text-secondary)' } }, 'Agent Count'),
                        h('input', { type: 'number', id: 'survival-agent-count', value: '0', min: '0', style: { padding: '0.5rem 0.75rem', background: 'var(--bg-primary)', border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', color: 'var(--text-primary)', width: '70px', fontSize: '0.8125rem' } })
                    ),
                    h('button', { className: 'btn btn-primary', id: 'arena-survival-start', onClick: function() { arenaStartSurvival(); } },
                        '\uD83C\uDFC5 Start Survival'
                    ),
                    h('button', { className: 'btn btn-danger', id: 'arena-survival-stop', onClick: function() { arenaStopSurvival(); }, style: { display: 'none' } },
                        '\u23F9 Stop Survival'
                    )
                ),
                h('div', { id: 'survival-status-panel', style: { marginTop: '0.75rem' } })
            ),
            h('div', { className: 'card' },
                h('h3', null, 'Action History'),
                h('div', { id: 'arena-history' },
                    historyExists
                        ? h('table', null,
                              h('thead', null, h('tr', null,
                                  h('th', null, 'Action'), h('th', null, 'Target'), h('th', null, 'Result'), h('th', null, 'Duration')
                              )),
                              h('tbody', { id: 'arena-history-tbody' }, historyRows)
                          )
                        : h('div', { className: 'empty-state' }, 'No actions yet. Inject a fault above.')
                )
            ),
            h('div', { className: 'card' },
                h('div', { style: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '0.75rem' } },
                    h('h3', { style: { marginBottom: 0 } }, 'Event Log'),
                    h('div', { id: 'sse-status-indicator', className: 'status-indicator', style: { display: 'flex', alignItems: 'center', gap: '0.35rem', fontSize: '0.7rem', color: 'var(--text-muted)' } },
                        h('span', { id: 'sse-status-dot', style: { width: '8px', height: '8px', borderRadius: '50%', background: 'var(--text-muted)', flexShrink: 0 } }),
                        h('span', { id: 'sse-status-text' }, 'disconnected')
                    )
                ),
                h('div', { id: 'arena-event-log', className: 'event-log', style: { background: 'var(--bg-primary)', borderRadius: 'var(--radius-sm)', border: '1px solid var(--border)' } },
                    h('div', { className: 'empty-state', padding: '2rem', fontSize: '0.8125rem' }, 'Connecting to event stream...')
                )
            )
        );

        // NOTE: No auto-refresh timer here. arena() is called on tab-switch
        // and after each action via refreshArenaData(). Auto-refresh caused
        // infinite DOM rebuild loop that broke DAG rendering and interactions.

        // Defer DAG render so virtual DOM has flushed to real DOM.
        requestAnimationFrame(function() { renderDAG(arenaAgents); });
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

        connectArenaEventSource();
        arenaPollSurvivalStatus();
    }

    // Lightweight data refresh after actions — does NOT rebuild entire DOM.
    // Only re-fetches agents/score/stats and updates DAG + score display in-place.
    function refreshArenaData() {
        if (currentView !== 'arena') return;
        Promise.all([
            api('/agents'),
            api('/arena/score'),
            api('/arena/stats'),
            api('/arena/history'),
            api('/arena/metrics'),
        ]).then(function(results) {
            if (currentView !== 'arena') return;
            arenaAgents = results[0] || [];
            var score = results[1];
            var stats = results[2];
            var history = results[3];
            var metrics = results[4];

            // Re-render DAG with updated agents.
            renderDAG(arenaAgents);

            // Update score card in-place.
            var scoreEl = document.getElementById('arena-score-value');
            var gradeEl = document.getElementById('arena-score-grade');
            if (score && score.score != null && scoreEl) scoreEl.textContent = String(Math.round(parseFloat(score.score)));
            if (score && gradeEl) gradeEl.textContent = (score.grade || '-');

            // Update stat cards.
            var statEls = document.querySelectorAll('.arena-stat-value');
            if (statEls.length >= 4 && stats) {
                statEls[0].textContent = (stats.total_actions || 0).toString();
                statEls[1].textContent = ((stats.successful_actions || 0)).toString();
                statEls[2].textContent = ((stats.failed_actions || 0)).toString();
                var recoveryRate = stats.total_actions > 0 ? Math.round((stats.successful_actions / stats.total_actions) * 100) : 0;
                statEls[3].textContent = recoveryRate + '%';
            }

            // Update history table if it exists.
            var tbody = document.getElementById('arena-history-tbody');
            if (tbody && history && history.length > 0) {
                var rows = history.slice().reverse().map(function(r) {
                    return h('tr', null,
                        h('td', null, h('code', null, esc((r.action && r.action.type) || '-'))),
                        h('td', null, esc((r.action && r.action.target_id) || (r.action && r.action.source_id) || '-')),
                        h('td', null, r.success
                            ? h('span', { className: 'badge badge-success' }, 'success')
                            : h('span', { className: 'badge badge-danger' }, 'failed')),
                        h('td', { style: { color: 'var(--text-secondary)' } },
                            r.duration ? Math.round(r.duration / 1000000) + 'ms' : '-')
                    );
                });
                setContent(tbody, rows);
            }
        }).catch(function() {});
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
refreshArenaData();
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
refreshArenaData();
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
refreshArenaData();
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
refreshArenaData();
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
refreshArenaData();
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
refreshArenaData();
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
refreshArenaData();
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
refreshArenaData();
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
refreshArenaData();
        });
    }

    function arenaToolTimeout() {
        var id = document.getElementById('arena-agent-select').value || selectedAgentId;
        if (!id) { alert('Select an agent first'); return; }
        var el = document.getElementById('arena-action-result');
        el.textContent = 'Injecting tool timeout...';
        el.style.color = 'var(--text-secondary)';
        api('/arena/agent/' + id + '/tool-timeout', { method: 'POST' }).then(function(r) {
            setContent(el,
                r && r.success
                    ? h('span', { className: 'badge badge-warning' }, 'Tool timeout injected on ', esc(id))
                    : h('span', { className: 'badge badge-danger' }, 'Failed: ' + esc((r && r.error) || 'unknown'))
            );
refreshArenaData();
        });
    }

    function arenaMemoryCorrupt() {
        var id = document.getElementById('arena-agent-select').value || selectedAgentId;
        if (!id) { alert('Select an agent first'); return; }
        var el = document.getElementById('arena-action-result');
        el.textContent = 'Injecting memory corruption...';
        el.style.color = 'var(--text-secondary)';
        api('/arena/agent/' + id + '/memory-corrupt', { method: 'POST' }).then(function(r) {
            setContent(el,
                r && r.success
                    ? h('span', { className: 'badge badge-danger' }, 'Memory corruption injected on ', esc(id))
                    : h('span', { className: 'badge badge-danger' }, 'Failed: ' + esc((r && r.error) || 'unknown'))
            );
refreshArenaData();
        });
    }

    function arenaMCPDisconnect() {
        var id = document.getElementById('arena-agent-select').value || selectedAgentId;
        if (!id) { alert('Select an agent first'); return; }
        var el = document.getElementById('arena-action-result');
        el.textContent = 'Injecting MCP disconnect...';
        el.style.color = 'var(--text-secondary)';
        api('/arena/agent/' + id + '/mcp-disconnect', { method: 'POST' }).then(function(r) {
            setContent(el,
                r && r.success
                    ? h('span', { className: 'badge badge-warning' }, 'MCP disconnect injected on ', esc(id))
                    : h('span', { className: 'badge badge-danger' }, 'Failed: ' + esc((r && r.error) || 'unknown'))
            );
refreshArenaData();
        });
    }

    function arenaLLMFailure() {
        var id = document.getElementById('arena-agent-select').value || selectedAgentId;
        if (!id) { alert('Select an agent first'); return; }
        var el = document.getElementById('arena-action-result');
        el.textContent = 'Injecting LLM failure...';
        el.style.color = 'var(--text-secondary)';
        api('/arena/agent/' + id + '/llm-failure', { method: 'POST' }).then(function(r) {
            setContent(el,
                r && r.success
                    ? h('span', { className: 'badge badge-danger' }, 'LLM failure injected on ', esc(id))
                    : h('span', { className: 'badge badge-danger' }, 'Failed: ' + esc((r && r.error) || 'unknown'))
            );
refreshArenaData();
        });
    }

    // ── SSE Event Log ─────────────────────
    function connectArenaEventSource() {
        if (arenaEventSource) {
            arenaEventSource.close();
            arenaEventSource = null;
        }

        updateSSEStatus('connecting', '#eab308');
        try {
            arenaEventSource = new EventSource('/arena/stream');
        } catch(e) {
            updateSSEStatus('error', '#ef4444');
            scheduleSSEReconnect();
            return;
        }

        arenaEventSource.onopen = function() {
            updateSSEStatus('connected', '#22c55e');
            arenaSSEReconnectDelay = 1000;
        };

        arenaEventSource.onmessage = function(e) {
            try {
                var ev = JSON.parse(e.data);
                addArenaEventLogEntry(ev);
            } catch(err) {}
        };

        arenaEventSource.onerror = function(err) {
            updateSSEStatus('disconnected', '#ef4444');
            if (arenaEventSource) {
                arenaEventSource.close();
                arenaEventSource = null;
            }
            scheduleSSEReconnect();
        };
    }

    function scheduleSSEReconnect() {
        if (currentView !== 'arena') return;
        updateSSEStatus('reconnecting', '#eab308');
        setTimeout(function() {
            if (currentView === 'arena') connectArenaEventSource();
        }, arenaSSEReconnectDelay);
        arenaSSEReconnectDelay = Math.min(arenaSSEReconnectDelay * 2, 30000);
    }

    function updateSSEStatus(status, color) {
        var dot = document.getElementById('sse-status-dot');
        var text = document.getElementById('sse-status-text');
        if (dot) dot.style.background = color;
        if (text) text.textContent = status;
    }

    function getEventTypeColor(type) {
        if (!type) return 'var(--text-secondary)';
        var t = String(type).toLowerCase();
        if (t.indexOf('fault') !== -1 || t.indexOf('kill') !== -1 || t.indexOf('error') !== -1) return 'var(--danger)';
        if (t.indexOf('recover') !== -1 || t.indexOf('resurrect') !== -1 || t.indexOf('elect') !== -1) return 'var(--success)';
        if (t.indexOf('slow') !== -1 || t.indexOf('pause') !== -1 || t.indexOf('timeout') !== -1) return 'var(--warning)';
        if (t.indexOf('partition') !== -1 || t.indexOf('disconnect') !== -1) return '#a78bfa';
        if (t.indexOf('survival') !== -1) return '#f472b6';
        return 'var(--accent)';
    }

    function formatEventTime(ts) {
        if (!ts) return '--:--:--';
        var d = new Date(ts);
        if (isNaN(d.getTime())) return String(ts).slice(11, 19);
        var pad = function(n) { return n < 10 ? '0' + n : String(n); };
        return pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
    }

    function addArenaEventLogEntry(ev) {
        if (!ev) return;

        arenaEventLogEntries.unshift(ev);
        if (arenaEventLogEntries.length > 200) {
            arenaEventLogEntries = arenaEventLogEntries.slice(0, 200);
        }

        var logEl = document.getElementById('arena-event-log');
        if (!logEl) return;

        var typeColor = getEventTypeColor(ev.type || ev.event_type || '');
        var isSuccess = ev.success === true || ev.status === 'success';

        var entry = h('div', { className: 'event-log-entry' + (isSuccess ? ' event-success' : !isSuccess && ev.success === false ? ' event-error' : '') },
            h('span', { style: { color: 'var(--text-muted)', fontSize: '0.7rem', flexShrink: 0, fontFamily: "'SF Mono','Fira Code',monospace" } },
                formatEventTime(ev.timestamp || ev.time)
            ),
            h('span', { style: { color: typeColor, fontWeight: '600', fontSize: '0.75rem', flexShrink: 0, padding: '0.1rem 0.35rem', background: typeColor.replace(')', ',0.15)').replace('rgb', 'rgba'), borderRadius: '3px', marginRight: '0.5rem' } },
                esc(ev.type || ev.event_type || 'event')
            ),
            h('span', { style: { color: 'var(--text-primary)', fontSize: '0.8125rem', flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' } },
                ev.target_id || ev.target || ev.agent_id || ''
                    ? ('Target: ' + esc(ev.target_id || ev.target || ev.agent_id || ''))
                    : esc(ev.message || ev.description || '')
            ),
            h('span', { style: { fontSize: '0.7rem', fontWeight: '600', flexShrink: 0, marginLeft: '0.5rem' } },
                isSuccess
                    ? h('span', { style: { color: 'var(--success)' } }, '\u2713')
                    : ev.success === false
                        ? h('span', { style: { color: 'var(--danger)' } }, '\u2717')
                        : null
            )
        );

        var emptyState = logEl.querySelector('.empty-state');
        if (emptyState) logEl.removeChild(emptyState);

        logEl.insertBefore(entry, logEl.firstChild);

        while (logEl.children.length > 200) {
            logEl.removeChild(logEl.lastChild);
        }
    }

    // ── Survival Mode ─────────────────────
    function arenaStartSurvival() {
        var duration = document.getElementById('survival-duration').value || '30m';
        var interval = document.getElementById('survival-interval').value || '10s';
        var agentCount = parseInt(document.getElementById('survival-agent-count').value) || 0;

        var startBtn = document.getElementById('arena-survival-start');
        var stopBtn = document.getElementById('arena-survival-stop');
        if (startBtn) startBtn.disabled = true;

        api('/arena/survival', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ duration: duration, interval: interval, agent_count: agentCount }),
        }).then(function(r) {
            if (startBtn) startBtn.disabled = false;
            if (r && (r.running || r.started || r.status === 'started')) {
                if (startBtn) startBtn.style.display = 'none';
                if (stopBtn) stopBtn.style.display = '';
                arenaPollSurvivalStatus();
            } else {
                alert('Failed to start survival mode: ' + ((r && r.error) || 'unknown'));
            }
        }).catch(function() {
            if (startBtn) startBtn.disabled = false;
            alert('Failed to reach server for survival mode');
        });
    }

    function arenaStopSurvival() {
        var stopBtn = document.getElementById('arena-survival-stop');
        if (stopBtn) stopBtn.disabled = true;

        api('/arena/survival/stop', { method: 'POST' }).then(function(r) {
            if (stopBtn) stopBtn.disabled = false;
            if (r && r.stopped) {
                var startBtn = document.getElementById('arena-survival-start');
                if (startBtn) startBtn.style.display = '';
                if (stopBtn) stopBtn.style.display = 'none';
                arenaPollSurvivalStatus();
            }
        }).catch(function() {
            if (stopBtn) stopBtn.disabled = false;
        });
    }

    function arenaPollSurvivalStatus() {
        if (currentView !== 'arena') {
            if (survivalPollTimer) { clearTimeout(survivalPollTimer); survivalPollTimer = null; }
            return;
        }

        api('/arena/survival/status').then(function(st) {
            if (currentView !== 'arena') return;

            var panel = document.getElementById('survival-status-panel');
            var startBtn = document.getElementById('arena-survival-start');
            var stopBtn = document.getElementById('arena-survival-stop');

            if (!panel) return;

            if (!st || typeof st !== 'object') {
                setContent(panel,
                    h('div', { style: { fontSize: '0.8125rem', color: 'var(--text-muted)' } }, 'Survival status unavailable')
                );
                return;
            }

            var isRunning = st.running === true || st.status === 'running';
            if (isRunning) {
                if (startBtn) startBtn.style.display = 'none';
                if (stopBtn) stopBtn.style.display = '';
            } else {
                if (startBtn) startBtn.style.display = '';
                if (stopBtn) stopBtn.style.display = 'none';
            }

            var elapsedStr = '-';
            if (st.elapsed != null) {
                var sec = Math.floor(st.elapsed / 1000000000);
                var min = Math.floor(sec / 60);
                sec = sec % 60;
                elapsedStr = String(min).padStart(2, '0') + ':' + String(sec).padStart(2, '0');
            }

            var timelineEvents = (st.recent_events || st.timeline || []).slice(-10);

            setContent(panel,
                h('div', { style: { display: 'flex', gap: '1.5rem', alignItems: 'flex-start', flexWrap: 'wrap' } },
                    h('div', { style: { display: 'flex', alignItems: 'center', gap: '0.5rem' } },
                        h('span', { className: 'status-indicator-dot', style: { width: '10px', height: '10px', borderRadius: '50%', background: isRunning ? 'var(--success)' : 'var(--text-muted)', animation: isRunning ? 'pulse-glow 1.5s infinite' : 'none', boxShadow: isRunning ? '0 0 8px rgba(16,185,129,0.5)' : 'none' } }),
                        h('strong', { style: { fontSize: '0.875rem', color: isRunning ? 'var(--success)' : 'var(--text-muted)' } },
                            isRunning ? '\u25B6 Running' : '\u23F8 Stopped'
                        )
                    ),
                    h('div', { style: { fontSize: '0.8125rem' } },
                        h('span', { style: { color: 'var(--text-secondary)' } }, 'Elapsed: '),
                        h('strong', { style: { color: 'var(--text-primary)', fontVariantNumeric: 'tabular-nums' } }, elapsedStr)
                    ),
                    h('div', { style: { fontSize: '0.8125rem' } },
                        h('span', { style: { color: 'var(--text-secondary)' } }, 'Actions: '),
                        h('strong', { style: { color: 'var(--accent)' } }, String(st.actions_run || st.action_count || 0))
                    )
                ),
                timelineEvents.length > 0
                    ? h('div', { style: { marginTop: '0.75rem', paddingTop: '0.75rem', borderTop: '1px solid var(--border)' } },
                          h('div', { style: { fontSize: '0.7rem', color: 'var(--text-secondary)', marginBottom: '0.35rem', textTransform: 'uppercase', letterSpacing: '0.04em' } }, 'Recent Events'),
                          h('div', { style: { display: 'flex', flexDirection: 'column', gap: '0.2rem', maxHeight: '120px', overflowY: 'auto' } },
                              timelineEvents.map(function(te) {
                                  return h('div', { style: { display: 'flex', gap: '0.5rem', fontSize: '0.75rem', padding: '0.2rem 0.4rem', borderRadius: '3px', background: 'rgba(255,255,255,0.02)' } },
                                      h('span', { style: { color: 'var(--text-muted)', fontSize: '0.65rem', flexShrink: 0, fontFamily: "'SF Mono','Fira Code',monospace" } },
                                          formatEventTime(te.timestamp || te.time)
                                      ),
                                      h('span', { style: { color: getEventTypeColor(te.type || ''), fontWeight: '500' } },
                                          esc(te.type || te.action || '-')
                                      ),
                                      h('span', { style: { color: 'var(--text-secondary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: 1 } },
                                          esc(te.target || te.target_id || '')
                                      )
                                  );
                              })
                          )
                      )
                    : null
            );
        });

        survivalPollTimer = setTimeout(function() { arenaPollSurvivalStatus(); }, 5000);
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

    // ── DAG / Topology Visualization ───
    var SVG_NS = 'http://www.w3.org/2000/svg';

    function renderDAG(agents) {
        var svg = document.getElementById('dag-svg');
        if (!svg) return;
        svg.innerHTML = '';

        var hasAgents = agents && agents.length > 0;

        var W = Math.max(svg.clientWidth || 800, 800);
        var H = hasAgents ? Math.max(320, 140 + Math.ceil(agents.length / 5) * 110) : 260;
        svg.setAttribute('height', String(H));
        svg.setAttribute('viewBox', '0 0 ' + W + ' ' + H);

        if (!hasAgents) {
            renderEmptyDAG(svg, W, H);
            return;
        }

        var cx = W / 2;
        var cy = 70;
        var radiusX = Math.min(W * 0.38, 280);
        var radiusY = 100;

        // Draw edges (orchestrator -> agents).
        agents.forEach(function(a, i) {
            var angle = (i / agents.length) * 2 * Math.PI - Math.PI / 2;
            var ax = cx + radiusX * Math.cos(angle);
            var ay = cy + 60 + radiusY * Math.sin(angle);

            var line = document.createElementNS(SVG_NS, 'line');
            line.setAttribute('x1', String(cx));
            line.setAttribute('y1', String(cy));
            line.setAttribute('x2', String(ax));
            line.setAttribute('y2', String(ay));
            line.setAttribute('stroke', 'var(--border)');
            line.setAttribute('stroke-width', '1.5');
            line.setAttribute('stroke-dasharray', '4 3');
            line.style.transition = 'stroke 0.3s';
            svg.appendChild(line);
        });

        // Orchestrator node.
        var orchG = document.createElementNS(SVG_NS, 'g');
        orchG.style.cursor = 'pointer';
        var orchCircle = document.createElementNS(SVG_NS, 'circle');
        orchCircle.setAttribute('cx', String(cx));
        orchCircle.setAttribute('cy', String(cy));
        orchCircle.setAttribute('r', '28');
        orchCircle.setAttribute('fill', 'rgba(99,102,241,0.12)');
        orchCircle.setAttribute('stroke', 'var(--accent)');
        orchCircle.setAttribute('stroke-width', '2');
        orchG.appendChild(orchCircle);

        var orchLabel = document.createElementNS(SVG_NS, 'text');
        orchLabel.setAttribute('x', String(cx));
        orchLabel.setAttribute('y', String(cy - 1));
        orchLabel.setAttribute('text-anchor', 'middle');
        orchLabel.setAttribute('dominant-baseline', 'middle');
        orchLabel.setAttribute('fill', 'var(--accent)');
        orchLabel.setAttribute('font-size', '10');
        orchLabel.setAttribute('font-weight', '700');
        orchLabel.style.pointerEvents = 'none';
        orchLabel.textContent = 'ORCH';
        orchG.appendChild(orchLabel);

        var orchSub = document.createElementNS(SVG_NS, 'text');
        orchSub.setAttribute('x', String(cx));
        orchSub.setAttribute('y', String(cy + 12));
        orchSub.setAttribute('text-anchor', 'middle');
        orchSub.setAttribute('fill', 'var(--text-muted)');
        orchSub.setAttribute('font-size', '7.5');
        orchSub.style.pointerEvents = 'none';
        orchSub.textContent = 'Orchestrator';
        orchG.appendChild(orchSub);

        var crown = document.createElementNS(SVG_NS, 'text');
        crown.setAttribute('x', String(cx));
        crown.setAttribute('y', String(cy - 34));
        crown.setAttribute('text-anchor', 'middle');
        crown.setAttribute('fill', '#f59e0b');
        crown.setAttribute('font-size', '14');
        crown.style.pointerEvents = 'none';
        crown.textContent = '\u265A';
        orchG.appendChild(crown);

        orchG.addEventListener('mouseover', function() { orchCircle.setAttribute('r', '32'); });
        orchG.addEventListener('mouseout', function() { orchCircle.setAttribute('r', '28'); });
        svg.appendChild(orchG);

        // Agent nodes in ring layout.
        agents.forEach(function(a, i) {
            var angle = (i / agents.length) * 2 * Math.PI - Math.PI / 2;
            var ax = cx + radiusX * Math.cos(angle);
            var ay = cy + 60 + radiusY * Math.sin(angle);

            var statusColor = a.status === 'completed' ? 'var(--success)' :
                              a.status === 'failed' ? 'var(--danger)' :
                              (a.status.indexOf('running') !== -1 || a.status.indexOf('analyzing') !== -1 || a.status === 'pending') ? 'var(--warning)' :
                              'var(--info)';
            var isSelected = selectedAgentId === a.id;
            var isRunning = a.status.indexOf('running') !== -1 || a.status.indexOf('analyzing') !== -1;

            var g = document.createElementNS(SVG_NS, 'g');
            g.setAttribute('data-agent-id', a.id);
            g.style.cursor = 'pointer';

            if (isSelected) {
                var glow = document.createElementNS(SVG_NS, 'circle');
                glow.setAttribute('cx', String(ax));
                glow.setAttribute('cy', String(ay));
                glow.setAttribute('r', '30');
                glow.setAttribute('fill', 'none');
                glow.setAttribute('stroke', 'var(--accent)');
                glow.setAttribute('stroke-width', '2');
                glow.setAttribute('stroke-opacity', '0.35');
                g.appendChild(glow);
            }

            var circle = document.createElementNS(SVG_NS, 'circle');
            circle.setAttribute('cx', String(ax));
            circle.setAttribute('cy', String(ay));
            circle.setAttribute('r', isSelected ? '24' : '20');
            circle.setAttribute('fill-opacity', '0.15');
            circle.setAttribute('fill', statusColor);
            circle.setAttribute('stroke', isSelected ? 'var(--accent)' : statusColor);
            circle.setAttribute('stroke-width', isSelected ? '2.5' : '1.8');
            if (isRunning) { circle.setAttribute('class', 'dag-running-pulse'); }
            g.appendChild(circle);

            var name = document.createElementNS(SVG_NS, 'text');
            name.setAttribute('x', String(ax));
            name.setAttribute('y', String(ay - 1));
            name.setAttribute('text-anchor', 'middle');
            name.setAttribute('dominant-baseline', 'middle');
            name.setAttribute('fill', 'var(--text-primary)');
            name.setAttribute('font-size', '9.5');
            name.setAttribute('font-weight', '600');
            name.style.pointerEvents = 'none';
            name.textContent = truncName(a.name, 9);
            g.appendChild(name);

            var statusText = document.createElementNS(SVG_NS, 'text');
            statusText.setAttribute('x', String(ax));
            statusText.setAttribute('y', String(ay + 33));
            statusText.setAttribute('text-anchor', 'middle');
            statusText.setAttribute('fill', statusColor);
            statusText.setAttribute('font-size', '8.5');
            statusText.setAttribute('font-weight', '500');
            statusText.style.pointerEvents = 'none';
            statusText.textContent = a.status;
            g.appendChild(statusText);

            // Invisible larger hit area for easier clicking.
            var hitArea = document.createElementNS(SVG_NS, 'circle');
            hitArea.setAttribute('cx', String(ax));
            hitArea.setAttribute('cy', String(ay));
            hitArea.setAttribute('r', '34');
            hitArea.setAttribute('fill', 'transparent');
            g.appendChild(hitArea);

            g.addEventListener('click', function() { selectAgent(a); });
            svg.appendChild(g);
        });

        // Legend bar at bottom.
        var legendY = H - 22;
        var legendItems = [
            { color: 'var(--success)', label: 'Completed' },
            { color: 'var(--warning)', label: 'Running' },
            { color: 'var(--danger)', label: 'Failed' },
            { color: 'var(--info)', label: 'Other' },
        ];
        var legendStartX = W / 2 - (legendItems.length * 90) / 2;
        legendItems.forEach(function(item, i) {
            var lx = legendStartX + i * 90;
            var dot = document.createElementNS(SVG_NS, 'circle');
            dot.setAttribute('cx', String(lx));
            dot.setAttribute('cy', String(legendY));
            dot.setAttribute('r', '4');
            dot.setAttribute('fill', item.color);
            svg.appendChild(dot);
            var lbl = document.createElementNS(SVG_NS, 'text');
            lbl.setAttribute('x', String(lx + 8));
            lbl.setAttribute('y', String(legendY + 1));
            lbl.setAttribute('fill', 'var(--text-muted)');
            lbl.setAttribute('font-size', '7.5');
            lbl.textContent = item.label;
            svg.appendChild(lbl);
        });
    }

    function renderEmptyDAG(svg, w, h) {
        var rect = document.createElementNS(SVG_NS, 'rect');
        rect.setAttribute('x', '10%');
        rect.setAttribute('y', '20%');
        rect.setAttribute('width', '80%');
        rect.setAttribute('height', '60%');
        rect.setAttribute('rx', '16');
        rect.setAttribute('fill', 'var(--bg-secondary)');
        rect.setAttribute('stroke', 'var(--border)');
        rect.setAttribute('stroke-width', '1');
        rect.setAttribute('stroke-dasharray', '6 4');
        svg.appendChild(rect);

        var t = document.createElementNS(SVG_NS, 'text');
        t.setAttribute('x', '50%');
        t.setAttribute('y', '48%');
        t.setAttribute('text-anchor', 'middle');
        t.setAttribute('dominant-baseline', 'middle');
        t.setAttribute('fill', 'var(--text-muted)');
        t.setAttribute('font-size', '13');
        t.textContent = 'No active agents \u2014 launch from Orchestrator tab';
        svg.appendChild(t);

        var sub = document.createElementNS(SVG_NS, 'text');
        sub.setAttribute('x', '50%');
        sub.setAttribute('y', '58%');
        sub.setAttribute('text-anchor', 'middle');
        sub.setAttribute('fill', 'var(--text-muted)');
        sub.setAttribute('font-size', '10');
        sub.setAttribute('opacity', '0.6');
        sub.textContent = 'Agent topology will appear here once agents are running';
        svg.appendChild(sub);
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
