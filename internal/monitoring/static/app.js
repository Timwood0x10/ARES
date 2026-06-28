// ARES Console — real-time monitoring dashboard.
(function() {
'use strict';

var NS = 'http://www.w3.org/2000/svg';
var API = '/api';
var POLL_MS = 2000;

var S = { snap: null, costBar: null, selected: null, activeTab: 'events', tabData: {} };
var SSE = null, POLL = null;

var PALETTE = ['#818cf8','#34d399','#fbbf24','#f87171','#60a5fa','#a78bfa','#f472b6','#22d3ee','#fb923c','#4ade80'];

// ── HTTP ──────────────────────────
function get(p) {
    return fetch(API + p, {headers:{Accept:'application/json'}})
        .then(function(r){return r.ok ? r.json() : null})
        .catch(function(){return null});
}
function post(p, b) {
    return fetch(API + p, {
        method:'POST', headers:{'Content-Type':'application/json',Accept:'application/json'},
        body: b ? JSON.stringify(b) : null, signal: AbortSignal.timeout(15000)
    }).then(function(r){return r.ok ? r.json() : null}).catch(function(){return null});
}

// ── Boot ──────────────────────────
function init() {
    bindTabs();
    bindDetailClose();
    tick();
    POLL = setInterval(tick, POLL_MS);
    connectSSE();
}

function tick() {
    Promise.all([get('/console'), get('/console/cost-bar')]).then(function(r) {
        S.snap = r[0]; S.costBar = r[1];
        renderMetrics(); renderCostBar(); renderDAG(); renderAgentGrid(); renderEventLog();
        fetchActiveTab();
    });
}

// ── SSE ───────────────────────────
function connectSSE() {
    try {
        SSE = new EventSource(API + '/subscribe');
        SSE.addEventListener('snapshot', function(e) {
            try {
                S.snap = JSON.parse(e.data);
                renderMetrics(); renderDAG(); renderAgentGrid(); renderEventLog();
            } catch(_) {}
        });
        SSE.onerror = function() {
            if (SSE) {SSE.close(); SSE = null;}
            setTimeout(connectSSE, 5000);
        };
    } catch(_) {}
}

// ── Metrics Strip ─────────────────
function renderMetrics() {
    var el = document.getElementById('metrics-strip');
    if (!el || !S.snap) return;
    var cost = S.snap.cost || {};
    var agents = S.snap.agents || [];
    var tasks = S.snap.tasks || [];
    var running = agents.filter(function(a){return a.status==='running';}).length;
    var done = agents.filter(function(a){return a.status==='completed';}).length;
    var dead = agents.filter(function(a){return a.status==='dead'||a.status==='failed';}).length;

    el.innerHTML =
        metric('Total Cost', '$' + (cost.total||0).toFixed(4), 'money') +
        metric('Agents', agents.length, 'count') +
        metric('Running', running, 'warn') +
        metric('Completed', done, 'count') +
        metric('Tasks', tasks.length, 'count') +
        (dead > 0 ? metric('Failed', dead, 'warn') : '');
}

function metric(label, value, cls) {
    return '<div class="metric"><span class="metric-label">' + label + '</span><span class="metric-value ' + (cls||'') + '">' + value + '</span></div>';
}

// ── Cost Bar ──────────────────────
function renderCostBar() {
    var wrap = document.getElementById('cost-bar-wrap');
    if (!wrap) return;
    var cb = S.costBar;
    if (!cb || !cb.entries || !cb.entries.length) { wrap.innerHTML = ''; return; }

    var total = cb.total || 1;
    var segs = '', legend = '';
    cb.entries.slice(0, 8).forEach(function(e, i) {
        var pct = (e.estimated_cost / total * 100).toFixed(1);
        var c = PALETTE[i % PALETTE.length];
        segs += '<div class="cost-bar-seg" style="width:' + pct + '%;background:' + c + '"></div>';
        legend += '<span class="cost-bar-legend-item"><span class="cost-bar-legend-dot" style="background:' + c + '"></span>' + esc(e.agent_id) + ' $' + e.estimated_cost.toFixed(4) + '</span>';
    });
    wrap.innerHTML = '<div class="cost-bar">' + segs + '</div>' + (legend ? '<div class="cost-bar-legend">' + legend + '</div>' : '');
}

// ── DAG ───────────────────────────
function renderDAG() {
    var svg = document.getElementById('dag');
    if (!svg || !S.snap) return;
    var W = svg.clientWidth || 800, H = svg.clientHeight || 400;
    svg.setAttribute('viewBox', '0 0 ' + W + ' ' + H);
    svg.innerHTML = '';

    // Defs
    var defs = document.createElementNS(NS, 'defs');
    defs.innerHTML =
        '<pattern id="grid" width="32" height="32" patternUnits="userSpaceOnUse"><circle cx="16" cy="16" r="0.5" fill="rgba(129,140,248,0.06)"/></pattern>' +
        '<filter id="glow"><feGaussianBlur stdDeviation="3" result="blur"/><feMerge><feMergeNode in="blur"/><feMergeNode in="SourceGraphic"/></feMerge></filter>';
    svg.appendChild(defs);
    svg.appendChild(mk('rect',{x:0,y:0,width:W,height:H,fill:'url(#grid)'}));

    var agents = S.snap.agents || [];
    var statsEl = document.getElementById('dag-stats');
    if (statsEl) statsEl.textContent = agents.length + ' agents';

    if (!agents.length) {
        var t = mk('text',{x:W/2,y:H/2,'text-anchor':'middle',fill:'var(--text-muted)','font-size':'13'});
        t.textContent = 'Waiting for agents...';
        svg.appendChild(t); return;
    }

    var cx = W/2, cy = H/2;
    var rx = Math.min(W*0.36, 300), ry = Math.min(H*0.34, 170);

    // Edges
    var amap = {};
    agents.forEach(function(a){amap[a.id]=a;});
    agents.forEach(function(a) {
        if (a.parent_id && amap[a.parent_id]) {
            var pi = agents.indexOf(amap[a.parent_id]), ci = agents.indexOf(a);
            if (pi>=0 && ci>=0) {
                var pa = (pi/agents.length)*2*Math.PI-Math.PI/2;
                var ca = (ci/agents.length)*2*Math.PI-Math.PI/2;
                var x1 = cx+rx*Math.cos(pa), y1 = cy+ry*Math.sin(pa);
                var x2 = cx+rx*Math.cos(ca), y2 = cy+ry*Math.sin(ca);
                // Gradient line
                var grad = document.createElementNS(NS,'linearGradient');
                grad.setAttribute('id','edge-'+i);
                grad.innerHTML = '<stop offset="0%" stop-color="var(--accent)" stop-opacity="0.3"/><stop offset="100%" stop-color="var(--accent)" stop-opacity="0.1"/>';
                svg.appendChild(grad);
                svg.appendChild(mk('line',{x1:x1,y1:y1,x2:x2,y2:y2,stroke:'var(--border-lit)','stroke-width':'1.5','stroke-dasharray':'6 4'}));
            }
        }
    });
    var i;

    // Central hub
    svg.appendChild(mk('circle',{cx:cx,cy:cy,r:6,fill:'var(--accent)','fill-opacity':'0.15',stroke:'var(--accent)','stroke-width':'1','stroke-opacity':'0.3'}));

    // Agent nodes
    agents.forEach(function(a, idx) {
        var angle = (idx/agents.length)*2*Math.PI-Math.PI/2;
        var ax = cx+rx*Math.cos(angle), ay = cy+ry*Math.sin(angle);
        var st = a.status||'pending';
        var col = stColor(st);
        var sel = S.selected === a.id;
        var r = sel ? 28 : 24;

        var g = mk('g',{'data-id':a.id,style:'cursor:pointer',filter:(st==='running'?'url(#glow)':'')});

        // Selection halo
        if (sel) {
            g.appendChild(mk('circle',{cx:ax,cy:ay,r:r+8,fill:'none',stroke:'var(--accent)','stroke-width':'2','stroke-opacity':'0.2'}));
        }

        // Pulse ring for running
        if (st === 'running' || st === 'resurrecting') {
            g.appendChild(mk('circle',{cx:ax,cy:ay,r:r+5,fill:'none',stroke:col,'stroke-width':'1.5','stroke-opacity':'0.15',class:'dp'}));
        }

        // Main circle with gradient fill
        g.appendChild(mk('circle',{cx:ax,cy:ay,r:r,fill:col,'fill-opacity':'0.08',stroke:sel?'var(--accent)':col,'stroke-width':sel?'2':'1.5'}));

        // Inner glow dot
        g.appendChild(mk('circle',{cx:ax,cy:ay,r:3,fill:col,'fill-opacity':'0.6'}));

        // Name
        var lbl = mk('text',{x:ax,y:ay,'text-anchor':'middle','dominant-baseline':'central',fill:'var(--text)','font-size':'10','font-weight':'600','pointer-events':'none'});
        lbl.textContent = (a.name||a.id||'?').slice(0,12);
        g.appendChild(lbl);

        // Status label
        var stl = mk('text',{x:ax,y:ay+r+14,'text-anchor':'middle',fill:col,'font-size':'8','font-weight':'500','pointer-events':'none','letter-spacing':'0.05em'});
        stl.textContent = st.toUpperCase();
        g.appendChild(stl);

        // Cost badge
        var cost = a.cost || {};
        if (cost.estimated_cost > 0) {
            var bx = ax+r-2, by = ay-r+2;
            g.appendChild(mk('circle',{cx:bx,cy:by,r:10,fill:'var(--bg-card)',stroke:'var(--ok)','stroke-width':'1.2'}));
            var ct = mk('text',{x:bx,y:by+1,'text-anchor':'middle',fill:'var(--ok)','font-size':'6.5','font-weight':'800','pointer-events':'none'});
            ct.textContent = '$';
            g.appendChild(ct);
        }

        // Hit area
        g.appendChild(mk('circle',{cx:ax,cy:ay,r:r+14,fill:'transparent'}));
        g.addEventListener('click',function(){selectAgent(a.id);});
        g.addEventListener('mouseenter',function(e){showTooltip(e,a);});
        g.addEventListener('mouseleave',hideTooltip);

        svg.appendChild(g);
    });
}

function mk(t,a){var e=document.createElementNS(NS,t);for(var k in a)e.setAttribute(k,a[k]);return e;}

function stColor(st) {
    switch(st) {
        case 'running': return 'var(--warn)';
        case 'completed': return 'var(--ok)';
        case 'dead': case 'failed': return 'var(--fail)';
        case 'resurrecting': return 'var(--warn)';
        default: return 'var(--info)';
    }
}

// ── Tooltip ───────────────────────
function showTooltip(evt, a) {
    var tip = document.getElementById('dag-tooltip');
    if (!tip) return;
    var cost = a.cost || {};
    tip.innerHTML =
        '<div class="tt-name">' + esc(a.name||a.id) + '</div>' +
        '<div class="tt-row"><span>Status</span><strong>' + (a.status||'-') + '</strong></div>' +
        '<div class="tt-row"><span>Role</span><strong>' + (a.role||'-') + '</strong></div>' +
        '<div class="tt-row"><span>Model</span><strong>' + (a.model_name||'-') + '</strong></div>' +
        '<div class="tt-divider"></div>' +
        (cost.estimated_cost ? '<div class="tt-row"><span>Cost</span><strong>$'+cost.estimated_cost.toFixed(4)+'</strong></div>' +
         '<div class="tt-row"><span>Tokens</span><strong>'+fmtNum(cost.total_tokens||0)+'</strong></div>' +
         '<div class="tt-row"><span>Calls</span><strong>'+(cost.call_count||0)+'</strong></div>' : '') +
        '<div class="tt-row"><span>Started</span><strong>'+fmtTime(a.started_at)+'</strong></div>';
    tip.style.left = Math.min(evt.clientX+14, window.innerWidth-320) + 'px';
    tip.style.top = (evt.clientY-10) + 'px';
    tip.className = 'dag-tooltip visible';
}
function hideTooltip() {
    var tip = document.getElementById('dag-tooltip');
    if (tip) tip.className = 'dag-tooltip';
}

// ── Agent Grid ────────────────────
function renderAgentGrid() {
    var grid = document.getElementById('agent-grid');
    if (!grid || !S.snap) return;
    grid.innerHTML = '';

    var agents = S.snap.agents || [];
    var countEl = document.getElementById('agent-count');
    if (countEl) countEl.textContent = agents.length;

    if (!agents.length) { grid.innerHTML = '<div class="empty">No agents</div>'; return; }

    agents.forEach(function(a) {
        var st = a.status||'pending';
        var dc = st==='completed'?'completed':(st==='dead'||st==='failed')?'dead':st==='running'?'running':st==='resurrecting'?'resurrecting':'pending';
        var sel = S.selected===a.id;
        var cost = a.cost||{};

        var card = document.createElement('div');
        card.className = 'agent-card'+(sel?' selected':'');
        card.onclick = function(){selectAgent(a.id);};

        card.innerHTML =
            '<span class="agent-dot '+dc+'"></span>'+
            '<div class="agent-info">'+
                '<div class="agent-name">'+esc(a.name||a.id)+'</div>'+
                '<div class="agent-meta">'+
                    (a.role?'<span class="role">'+esc(a.role)+'</span>':'')+
                    (a.model_name?'<span class="model">'+esc(a.model_name)+'</span>':'')+
                '</div>'+
            '</div>'+
            (cost.estimated_cost?'<span class="agent-cost">$'+cost.estimated_cost.toFixed(4)+'</span>':'');

        grid.appendChild(card);
    });
}

function selectAgent(id) {
    S.selected = S.selected===id ? null : id;
    renderDAG(); renderAgentGrid();
    if (S.selected) openDetail(S.selected); else closeDetail();
}

// ── Event Log ─────────────────────
function renderEventLog() {
    var el = document.getElementById('event-log');
    if (!el || !S.snap) return;
    var tasks = S.snap.tasks||[];
    if (!tasks.length) { el.innerHTML = '<div class="log-empty">No activity yet</div>'; return; }

    el.innerHTML = tasks.slice(0,25).map(function(t) {
        var st = t.status||'pending';
        return '<div class="evt">'+
            '<span class="evt-t">'+fmtTime(t.started_at)+'</span>'+
            '<span class="evt-type '+st+'">'+st+'</span>'+
            '<span class="evt-msg">'+esc(t.name||t.id)+(t.agent_id?' <span style="color:var(--text-muted)">@'+esc(t.agent_id)+'</span>':'')+'</span>'+
        '</div>';
    }).join('');
}

// ── Tabs ──────────────────────────
function bindTabs() {
    var tabs = document.querySelectorAll('.tab');
    for (var i=0;i<tabs.length;i++) {
        tabs[i].addEventListener('click',function(){
            setActiveTab(this.getAttribute('data-tab'));
        });
    }
}
function setActiveTab(name) {
    S.activeTab = name;
    var tabs = document.querySelectorAll('.tab');
    for (var i=0;i<tabs.length;i++) {
        tabs[i].className = 'tab'+(tabs[i].getAttribute('data-tab')===name?' active':'');
    }
    fetchActiveTab();
}
function fetchActiveTab() {
    get('/tabs/'+S.activeTab).then(function(data){
        S.tabData[S.activeTab] = data;
        renderTabContent();
    });
}
function renderTabContent() {
    var el = document.getElementById('tab-content');
    if (!el) return;
    var data = S.tabData[S.activeTab];
    if (!data) { el.innerHTML = '<div class="empty">Loading...</div>'; return; }
    switch(S.activeTab) {
        case 'events': renderEventsTab(el,data); break;
        case 'workflow': renderWorkflowTab(el,data); break;
        case 'mcp': renderMCPTab(el,data); break;
        case 'llm': renderLLMTab(el,data); break;
        case 'memory': renderMemoryTab(el,data); break;
        case 'evolution': renderEvolutionTab(el,data); break;
        case 'arena': renderArenaTab(el,data); break;
        default: el.innerHTML = '<div class="empty">Unknown tab</div>';
    }
}

function renderEventsTab(el, data) {
    var evts = data.events||[];
    if (!evts.length) { el.innerHTML = '<div class="empty">No events</div>'; return; }
    var h = '<table><tr><th>Time</th><th>Type</th><th>Stream</th><th>Module</th></tr>';
    h += evts.slice(0,50).map(function(e){
        return '<tr><td>'+fmtTime(e.timestamp)+'</td><td>'+esc(e.type)+'</td><td>'+esc(e.stream_id)+'</td><td>'+esc(e.module_name)+'</td></tr>';
    }).join('')+'</table>';
    el.innerHTML = h;
}

function renderWorkflowTab(el, data) {
    var execs = data.executions||[];
    if (!execs.length) { el.innerHTML = '<div class="empty">No executions</div>'; return; }
    var h = '<table><tr><th>Task</th><th>Name</th><th>Agent</th><th>Status</th><th>Started</th></tr>';
    h += execs.slice(0,30).map(function(e){
        return '<tr><td>'+esc(e.task_id)+'</td><td>'+esc(e.name||'-')+'</td><td>'+esc(e.agent_id)+'</td><td><span class="evt-type '+(e.status||'')+'">'+e.status+'</span></td><td>'+fmtTime(e.started_at)+'</td></tr>';
    }).join('')+'</table>';
    el.innerHTML = h;
}

function renderMCPTab(el, data) {
    var tools = data.tools||[], calls = data.calls||[];
    var h = '<div class="tab-section-title">Registered Tools ('+tools.length+')</div>';
    if (tools.length) {
        h += '<table><tr><th>Name</th><th>Description</th></tr>';
        h += tools.map(function(t){return '<tr><td>'+esc(t.name)+'</td><td>'+esc(t.description||'-')+'</td></tr>';}).join('')+'</table>';
    }
    h += '<div class="tab-section-title">Recent Calls ('+calls.length+')</div>';
    if (calls.length) {
        h += '<table><tr><th>Time</th><th>Tool</th><th>Agent</th><th>Status</th></tr>';
        h += calls.slice(0,20).map(function(c){
            return '<tr><td>'+fmtTime(c.timestamp)+'</td><td>'+esc(c.tool_name)+'</td><td>'+esc(c.agent_id)+'</td><td>'+esc(c.status)+'</td></tr>';
        }).join('')+'</table>';
    }
    el.innerHTML = h;
}

function renderLLMTab(el, data) {
    var calls = data.calls||[], stats = data.stats||{};
    var h = '<div style="display:flex;gap:2rem;flex-wrap:wrap;margin-bottom:0.5rem">'+
        statCard('Calls',stats.total_calls||0)+statCard('Tokens',fmtNum(stats.total_tokens||0))+
        statCard('Avg In',Math.round(stats.avg_input_tokens||0))+statCard('Avg Out',Math.round(stats.avg_output_tokens||0))+
    '</div>';
    if (calls.length) {
        h += '<table><tr><th>Time</th><th>Agent</th><th>Model</th><th>In</th><th>Out</th><th>Duration</th></tr>';
        h += calls.slice(0,20).map(function(c){
            return '<tr><td>'+fmtTime(c.timestamp)+'</td><td>'+esc(c.agent_id)+'</td><td>'+esc(c.model_name)+'</td><td>'+fmtNum(c.input_tokens)+'</td><td>'+fmtNum(c.output_tokens)+'</td><td>'+fmtDur(c.duration)+'</td></tr>';
        }).join('')+'</table>';
    }
    el.innerHTML = h;
}

function renderMemoryTab(el, data) {
    var dist = data.distillations||[], ret = data.retrievals||[];
    var h = '<div class="tab-section-title">Distillations ('+dist.length+')</div>';
    if (dist.length) {
        h += '<table><tr><th>Time</th><th>Agent</th><th>Content</th><th>Score</th></tr>';
        h += dist.slice(0,15).map(function(r){
            return '<tr><td>'+fmtTime(r.created_at)+'</td><td>'+esc(r.agent_id)+'</td><td>'+esc((r.content||'').slice(0,80))+'</td><td>'+(r.relevance||0).toFixed(2)+'</td></tr>';
        }).join('')+'</table>';
    }
    h += '<div class="tab-section-title">Retrievals ('+ret.length+')</div>';
    if (ret.length) {
        h += '<table><tr><th>Time</th><th>Agent</th><th>Content</th><th>Score</th></tr>';
        h += ret.slice(0,15).map(function(r){
            return '<tr><td>'+fmtTime(r.created_at)+'</td><td>'+esc(r.agent_id)+'</td><td>'+esc((r.content||'').slice(0,80))+'</td><td>'+(r.relevance||0).toFixed(2)+'</td></tr>';
        }).join('')+'</table>';
    }
    el.innerHTML = h;
}

function renderEvolutionTab(el, data) {
    var genomes = data.genomes||[], muts = data.mutations||[];
    var h = '<table><tr><th>Time</th><th>Agent</th><th>Generation</th><th>Parent</th></tr>';
    h += genomes.slice(0,20).map(function(g){
        return '<tr><td>'+fmtTime(g.started_at)+'</td><td>'+esc(g.agent_id)+'</td><td>'+g.generation+'</td><td>'+esc(g.parent_id||'-')+'</td></tr>';
    }).join('')+'</table>';
    if (muts.length) {
        h += '<div class="tab-section-title">Mutations ('+muts.length+')</div>';
        h += '<table><tr><th>Time</th><th>Agent</th><th>Description</th></tr>';
        h += muts.slice(0,10).map(function(m){
            return '<tr><td>'+fmtTime(m.timestamp)+'</td><td>'+esc(m.agent_id)+'</td><td>'+esc(m.description||'-')+'</td></tr>';
        }).join('')+'</table>';
    }
    el.innerHTML = h;
}

function renderArenaTab(el, data) {
    var fi = data.fault_injections||[], st = data.survival_tests||[];
    var h = '<div class="tab-section-title">Fault Injections ('+fi.length+')</div>';
    if (fi.length) {
        h += '<table><tr><th>Time</th><th>Agent</th><th>Type</th><th>Survived</th><th>Duration</th></tr>';
        h += fi.slice(0,15).map(function(f){
            var dur = f.completed_at ? fmtDur(new Date(f.completed_at)-new Date(f.triggered_at)) : '-';
            return '<tr><td>'+fmtTime(f.triggered_at)+'</td><td>'+esc(f.agent_id)+'</td><td>'+esc(f.type)+'</td><td style="color:'+(f.survived?'var(--ok)':'var(--fail)')+'">'+(f.survived?'YES':'NO')+'</td><td>'+dur+'</td></tr>';
        }).join('')+'</table>';
    }
    h += '<div class="tab-section-title">Survival Tests ('+st.length+')</div>';
    if (st.length) {
        h += '<table><tr><th>Time</th><th>Agent</th><th>Type</th><th>Passed</th></tr>';
        h += st.slice(0,15).map(function(t){
            return '<tr><td>'+fmtTime(t.timestamp)+'</td><td>'+esc(t.agent_id)+'</td><td>'+esc(t.test_type)+'</td><td style="color:'+(t.passed?'var(--ok)':'var(--fail)')+'">'+(t.passed?'PASS':'FAIL')+'</td></tr>';
        }).join('')+'</table>';
    }
    el.innerHTML = h;
}

function statCard(label, value) {
    return '<div style="text-align:center;padding:0.3rem 0.6rem;background:var(--bg-elevated);border-radius:var(--radius-xs);border:1px solid var(--border)">'+
        '<div style="font-size:0.55rem;color:var(--text-muted);text-transform:uppercase;letter-spacing:0.06em">'+label+'</div>'+
        '<div style="font-size:0.85rem;font-weight:700;color:var(--text);font-variant-numeric:tabular-nums">'+value+'</div></div>';
}

// ── Detail Panel ──────────────────
function openDetail(id) {
    var panel = document.getElementById('detail-panel');
    var overlay = document.getElementById('detail-overlay');
    var body = document.getElementById('detail-body');
    var title = document.getElementById('detail-title');
    var dot = document.getElementById('detail-dot');
    if (!panel||!body) return;

    get('/agents/'+id).then(function(detail) {
        if (!detail||!detail.agent) { body.innerHTML='<div class="empty">Not found</div>'; panel.className='detail-panel open'; overlay.className='detail-overlay open'; return; }
        var a = detail.agent;
        var tasks = detail.tasks || [];
        var rel = detail.relationships || {};
        var evts = detail.events || {};
        var cost = a.cost || {};

        title.textContent = a.name || a.id;
        dot.style.background = stColor(a.status||'pending');

        var h = '';

        // ── Identity ──
        h += '<div class="detail-section"><div class="detail-section-title">Identity</div>';
        h += kv('Agent ID', a.id);
        h += kv('Name', a.name || '-');
        h += kv('Status', '<span style="color:'+stColor(a.status)+'">'+(a.status||'-')+'</span>');
        h += kv('Role', a.role || '-');
        h += kv('Model', a.model_name || '-');
        h += kv('Started', fmtTime(a.started_at));
        h += kv('Updated', fmtTime(a.updated_at));
        h += '</div>';

        // ── Cost ──
        if (cost.estimated_cost || cost.call_count) {
            h += '<div class="detail-section"><div class="detail-section-title">Cost</div>';
            h += kv('Estimated', '<span style="color:var(--ok)">$'+(cost.estimated_cost||0).toFixed(4)+'</span>');
            h += kv('Input Tokens', fmtNum(cost.input_tokens||0));
            h += kv('Output Tokens', fmtNum(cost.output_tokens||0));
            h += kv('Total Tokens', fmtNum(cost.total_tokens||0));
            h += kv('LLM Calls', cost.call_count||0);
            h += '</div>';
        }

        // ── Tasks ──
        h += '<div class="detail-section"><div class="detail-section-title">Tasks ('+tasks.length+')</div>';
        if (tasks.length) {
            tasks.forEach(function(t) {
                var st = t.status || 'pending';
                h += '<div style="display:flex;align-items:center;gap:0.4rem;padding:0.3rem 0;border-bottom:1px solid var(--border)">';
                h += '<span class="agent-dot '+st+'" style="width:6px;height:6px"></span>';
                h += '<div style="flex:1;min-width:0">';
                h += '<div style="font-size:0.72rem;font-weight:600;color:var(--text)">'+esc(t.name||t.id)+'</div>';
                h += '<div style="font-size:0.6rem;color:var(--text-muted)">'+esc(t.id)+' · '+fmtTime(t.started_at)+'</div>';
                h += '</div>';
                h += '<span class="evt-type '+st+'" style="font-size:0.55rem">'+st+'</span>';
                h += '</div>';
            });
        } else {
            h += '<div style="font-size:0.65rem;color:var(--text-muted);padding:0.3rem 0">No tasks assigned</div>';
        }
        h += '</div>';

        // ── Relationships ──
        h += '<div class="detail-section"><div class="detail-section-title">Relationships</div>';

        // Parent
        if (rel.parent) {
            h += '<div style="margin-bottom:0.5rem">';
            h += '<div style="font-size:0.6rem;color:var(--text-muted);margin-bottom:0.2rem">↑ UPSTREAM (Parent)</div>';
            h += relCard(rel.parent, 'var(--accent)');
            h += '</div>';
        }

        // Children
        if (rel.children && rel.children.length) {
            h += '<div style="margin-bottom:0.5rem">';
            h += '<div style="font-size:0.6rem;color:var(--text-muted);margin-bottom:0.2rem">↓ DOWNSTREAM ('+rel.children.length+')</div>';
            rel.children.forEach(function(c) { h += relCard(c, 'var(--ok)'); });
            h += '</div>';
        }

        // Peers
        if (rel.peers && rel.peers.length) {
            h += '<div style="margin-bottom:0.5rem">';
            h += '<div style="font-size:0.6rem;color:var(--text-muted);margin-bottom:0.2rem">↔ PEERS ('+rel.peers.length+')</div>';
            rel.peers.forEach(function(p) { h += relCard(p, 'var(--info)'); });
            h += '</div>';
        }

        if (!rel.parent && (!rel.children||!rel.children.length) && (!rel.peers||!rel.peers.length)) {
            h += '<div style="font-size:0.65rem;color:var(--text-muted);padding:0.3rem 0">No relationships detected</div>';
        }
        h += '</div>';

        // ── Events ──
        h += '<div class="detail-section"><div class="detail-section-title">Events ('+(evts.total||0)+')</div>';
        if (evts.by_type) {
            var types = Object.keys(evts.by_type);
            if (types.length) {
                types.forEach(function(t) {
                    var pct = evts.total > 0 ? (evts.by_type[t]/evts.total*100).toFixed(0) : 0;
                    h += '<div style="display:flex;align-items:center;gap:0.4rem;padding:0.2rem 0">';
                    h += '<span style="font-size:0.65rem;color:var(--text-dim);min-width:8rem">'+esc(t)+'</span>';
                    h += '<div style="flex:1;height:4px;background:var(--bg-elevated);border-radius:2px;overflow:hidden">';
                    h += '<div style="width:'+pct+'%;height:100%;background:var(--accent);border-radius:2px"></div>';
                    h += '</div>';
                    h += '<span style="font-size:0.6rem;color:var(--text-muted);min-width:2rem;text-align:right">'+evts.by_type[t]+'</span>';
                    h += '</div>';
                });
            } else {
                h += '<div style="font-size:0.65rem;color:var(--text-muted)">No events recorded</div>';
            }
        }
        h += '</div>';

        // ── Actions ──
        h += '<div class="detail-actions">';
        h += '<button class="kill" onclick="window._kill(\''+esc(id)+'\')">Kill</button>';
        h += '<button onclick="window._resume(\''+esc(id)+'\')">Resume</button>';
        h += '<button onclick="window._retry(\''+esc(id)+'\')">Retry</button>';
        h += '</div>';

        body.innerHTML = h;
        panel.className = 'detail-panel open';
        overlay.className = 'detail-overlay open';
    });
}

function relCard(a, color) {
    return '<div style="display:flex;align-items:center;gap:0.4rem;padding:0.3rem 0.4rem;margin-bottom:2px;background:var(--bg-elevated);border-radius:var(--radius-xs);border-left:2px solid '+color+';cursor:pointer" onclick="window._selectAgent(\''+esc(a.id)+'\')">'+
        '<span class="agent-dot '+(a.status||'pending')+'" style="width:6px;height:6px"></span>'+
        '<div style="flex:1;min-width:0">'+
            '<div style="font-size:0.7rem;font-weight:600;color:var(--text)">'+esc(a.name||a.id)+'</div>'+
            '<div style="font-size:0.55rem;color:var(--text-muted)">'+esc(a.id)+' · '+(a.role||a.status||'-')+'</div>'+
        '</div>'+
    '</div>';
}

function closeDetail() {
    document.getElementById('detail-panel').className = 'detail-panel';
    document.getElementById('detail-overlay').className = 'detail-overlay';
    S.selected = null;
    renderDAG(); renderAgentGrid();
}

function bindDetailClose() {
    var btn = document.getElementById('detail-close');
    if (btn) btn.addEventListener('click', closeDetail);
    var overlay = document.getElementById('detail-overlay');
    if (overlay) overlay.addEventListener('click', closeDetail);
}

function kv(k,v) {
    return '<div class="detail-kv"><span class="k">'+esc(k)+'</span><span class="v">'+esc(String(v))+'</span></div>';
}

// ── Actions ───────────────────────
window._kill = function(id) { post('/agents/'+id+'/kill').then(function(){tick();}); };
window._resume = function(id) { post('/agents/'+id+'/resume').then(function(){tick();}); };
window._retry = function(id) { post('/agents/'+id+'/retry').then(function(){tick();}); };
window._selectAgent = function(id) { selectAgent(id); };

// ── Helpers ───────────────────────
function esc(s) { if(!s)return'';var d=document.createElement('div');d.textContent=String(s);return d.innerHTML; }
function fmtTime(ts) {
    if(!ts)return'--:--:--';
    var d=new Date(ts);
    if(!isNaN(d.getTime())){var p=function(n){return n<10?'0'+n:String(n);};return p(d.getHours())+':'+p(d.getMinutes())+':'+p(d.getSeconds());}
    return(ts+'').length>8?(ts+'').slice(11,19):ts;
}
function fmtDur(d) {
    if(!d)return'-';
    if(typeof d==='number'){if(d>1e9)return(d/1e9).toFixed(1)+'s';if(d>1e6)return(d/1e6).toFixed(0)+'ms';return d+'ns';}
    return d+'';
}
function fmtNum(n) {
    if(n>=1e6)return(n/1e6).toFixed(1)+'M';
    if(n>=1e3)return(n/1e3).toFixed(1)+'K';
    return String(n);
}

// ── SVG pulse animation ───────────
(function loop() {
    var svg = document.getElementById('dag');
    if (svg) {
        var cs = svg.querySelectorAll('.dp');
        for (var i=0;i<cs.length;i++) {
            var op = parseFloat(cs[i].getAttribute('stroke-opacity')||'0.15');
            cs[i].setAttribute('stroke-opacity',String(op<0.3?op+0.003:0.05));
        }
    }
    requestAnimationFrame(loop);
})();

document.addEventListener('DOMContentLoaded', init);
})();
