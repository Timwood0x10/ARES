// Chaos Arena — real-time, interactive, self-healing.
(function() {
'use strict';

var NS = 'http://www.w3.org/2000/svg';
var S = { agents: [], score: null, stats: null, events: [], selected: null, nodeId: '', edgeFrom: '', edgeTo: '' };
var POLL = null, SSE = null;

function get(p) { return fetch(p, {headers:{Accept:'application/json'}}).then(function(r){return r.ok?r.json():null}).catch(function(){return null}); }
function post(p, b) {
    return fetch(p, {method:'POST',headers:{'Content-Type':'application/json',Accept:'application/json'},body:b?JSON.stringify(b):null,signal:AbortSignal.timeout(15000)})
        .then(function(r){return r.ok?r.json():null})
        .catch(function(){return null});
}

// ── Boot ───────────────────────────
function init() {
    buildUI();
    tick();
    POLL = setInterval(tick, 3000);
    connectSSE();
}

function tick() {
    Promise.all([get('/agents'), get('/arena/score'), get('/arena/stats'), get('/arena/history'), get('/arena/metrics')]).then(function(r) {
        S.agents = r[0] || [];
        S.score = r[1];
        S.stats = r[2];
        var history = r[3] || [];
        var metrics = r[4];
        renderScore();
        renderDAG();
        renderCards();
        if (history.length) mergeHistory(history);
        if (metrics) updateMetrics(metrics);
    });
}

function connectSSE() {
    try {
        SSE = new EventSource('/arena/stream');
        SSE.onmessage = function(e) {
            try {
                var ev = JSON.parse(e.data);
                addEvent({time:ev.timestamp||new Date().toISOString(),type:ev.success?'recover':'fault',action:(ev.action&&ev.action.type)||'event',target:(ev.action&&ev.action.target_id)||'',success:ev.success,duration:ev.duration});
            } catch(_) {}
        };
        SSE.onerror = function() {
            if (SSE) { SSE.close(); SSE = null; }
            setTimeout(connectSSE, 5000);
        };
    } catch(_) {}
}

// ── Build UI ───────────────────────
function buildUI() {
    var top = document.querySelector('.topbar');
    top.innerHTML =
        '<div class="logo"><span class="logo-icon">☠</span><span class="logo-text">CHAOS ARENA</span></div>' +
        '<div id="score-area" class="score-area"></div>' +
        '<div id="stats-area" class="stats-area"></div>' +
        '<div class="faults" id="faults"></div>';

    var topRow = document.createElement('div');
    topRow.className = 'fault-row';
    topRow.innerHTML =
        '<button id="btn-leader" class="danger">☠Leader</button>' +
        '<button id="btn-orch" class="danger">⚙Orch</button>' +
        '<button id="btn-kill-sel" class="danger-kill">⚙Kill</button>' +
        '<span class="fault-inline"><input id="node-id" placeholder="Node ID"/><button id="btn-rm-node" class="danger">✕Node</button></span>' +
        '<span class="fault-inline"><input id="edge-from" placeholder="From" style="width:70px"/>' +
        '<input id="edge-to" placeholder="To" style="width:70px"/><button id="btn-rm-edge" class="danger">✕Edge</button></span>';
    document.getElementById('faults').appendChild(topRow);

    var botRow = document.createElement('div');
    botRow.className = 'fault-row';
    botRow.innerHTML =
        '<button id="btn-pause">⏸Pause</button>' +
        '<button id="btn-resume">▶Resume</button>' +
        '<button id="btn-slow">🐌Slow</button>' +
        '<button id="btn-partition">🗡Partition</button>' +
        '<button id="btn-timeout">⏰Timeout</button>' +
        '<button id="btn-mem">📚MemCorrupt</button>' +
        '<button id="btn-mcp">📱MCP DC</button>' +
        '<button id="btn-llm">🧠LLM Fail</button>';
    document.getElementById('faults').appendChild(botRow);

    document.getElementById('btn-leader').onclick = doLeaderKill;
    document.getElementById('btn-orch').onclick = doOrchKill;
    document.getElementById('btn-kill-sel').onclick = doKillSelected;
    document.getElementById('btn-rm-node').onclick = doRemoveNode;
    document.getElementById('btn-rm-edge').onclick = doRemoveEdge;
    document.getElementById('btn-pause').onclick = function(){doAgentFault('pause');};
    document.getElementById('btn-resume').onclick = function(){doAgentFault('resume');};
    document.getElementById('btn-slow').onclick = doAgentSlow;
    document.getElementById('btn-partition').onclick = function(){doAgentFault('partition');};
    document.getElementById('btn-timeout').onclick = function(){doAgentFault('tool-timeout');};
    document.getElementById('btn-mem').onclick = function(){doAgentFault('memory-corrupt');};
    document.getElementById('btn-mcp').onclick = function(){doAgentFault('mcp-disconnect');};
    document.getElementById('btn-llm').onclick = function(){doAgentFault('llm-failure');};

    document.getElementById('node-id').oninput = function(){S.nodeId=this.value;};
    document.getElementById('edge-from').oninput = function(){S.edgeFrom=this.value;};
    document.getElementById('edge-to').oninput = function(){S.edgeTo=this.value;};
    document.getElementById('node-id').addEventListener('keydown',function(e){if(e.key==='Enter')doRemoveNode();});
    document.getElementById('edge-from').addEventListener('keydown',function(e){if(e.key==='Enter')document.getElementById('edge-to').focus();});
    document.getElementById('edge-to').addEventListener('keydown',function(e){if(e.key==='Enter')doRemoveEdge();});
}



// ── Score + Stats ──────────────────
function renderScore() {
    var el = document.getElementById('score-area');
    if (!el) return;
    var s = S.score || {};
    var v = (s.score != null) ? Math.round(parseFloat(s.score)) : 0;
    var grade = s.grade || '-';
    var rec = (s.recovery_rate != null) ? parseFloat(s.recovery_rate).toFixed(1)+'%' : '-';
    var gc = grade==='A+'||grade==='A'?'var(--ok)':grade==='B'?'#a3e635':grade==='C'?'var(--warn)':'var(--fail)';
    var c = 2*Math.PI*12, o = c*(1-v/100);
    el.innerHTML =
        '<div class="score-ring-w"><svg viewBox="0 0 32 32"><circle cx="16" cy="16" r="12" fill="none" stroke="var(--border)" stroke-width="3"/>'+
        '<circle cx="16" cy="16" r="12" fill="none" stroke="'+gc+'" stroke-width="3" stroke-linecap="round"'+
        ' stroke-dasharray="'+c+'" stroke-dashoffset="'+o+'" style="transition:stroke-dashoffset 0.6s"/>'+
        '<text x="16" y="17" text-anchor="middle" fill="'+gc+'" font-size="7" font-weight="800">'+v+'</text></svg></div>'+
        '<span class="score-grade" style="background:'+gc+';box-shadow:0 0 14px '+gc+'40">'+grade+'</span>'+
        '<span class="score-stat">Recovery <strong>'+rec+'</strong></span>';
}

function updateMetrics(m) {
    var el = document.getElementById('stats-area');
    if (!el) return;
    el.innerHTML =
        '<span class="stat-item"><span class="stat-lbl">Faults</span><strong class="stat-val">'+(m.total_actions||'0')+'</strong></span>'+
        '<span class="stat-item"><span class="stat-lbl">Recovered</span><strong class="stat-val" style="color:var(--ok)">'+(m.successful_actions||'0')+'</strong></span>'+
        '<span class="stat-item"><span class="stat-lbl">Failed</span><strong class="stat-val" style="color:var(--fail)">'+((m.failed_actions)||'0')+'</strong></span>'+
        '<span class="stat-item"><span class="stat-lbl">AvgRec</span><strong class="stat-val">'+(m.avg_recovery_time?fmtDur(m.avg_recovery_time):'-')+'</strong></span>';
}

// ── DAG ────────────────────────────
function renderDAG() {
    var svg = document.getElementById('dag');
    if (!svg) return;
    var W = svg.clientWidth||800, H = svg.clientHeight||500;
    svg.setAttribute('viewBox','0 0 '+W+' '+H);
    svg.innerHTML = '';

    var d = document.createElementNS(NS,'defs');
    d.innerHTML = '<pattern id="g" width="40" height="40" patternUnits="userSpaceOnUse"><circle cx="20" cy="20" r="0.8" fill="var(--border)" opacity="0.35"/></pattern>';
    svg.appendChild(d);
    svg.appendChild(mk('rect',{x:0,y:0,width:W,height:H,fill:'url(#g)'}));

    var ag = S.agents;
    if (!ag||!ag.length) {
        var t = mk('text',{x:W/2,y:H/2,'text-anchor':'middle',fill:'var(--text-muted)','font-size':'14','font-family':'var(--font)'});
        t.textContent = 'No agents — launch from Orchestrator';
        svg.appendChild(t); return;
    }

    var cx = W/2, cy = H/2;
    var rx = Math.min(W*0.35, 280), ry = Math.min(H*0.33, 160);

    // Edges
    ag.forEach(function(a,i) {
        var a2 = (i/ag.length)*2*Math.PI - Math.PI/2;
        svg.appendChild(mk('line',{
            x1:cx,y1:cy, x2:cx+rx*Math.cos(a2),y2:cy+ry*Math.sin(a2),
            stroke:'var(--border)','stroke-width':'1','stroke-dasharray':'5 3'
        }));
    });

    // Orch
    var og = mk('g',{style:'cursor:pointer'});
    og.appendChild(mk('circle',{cx:cx,cy:cy,r:28,fill:'rgba(129,140,248,0.06)',stroke:'var(--accent)','stroke-width':'2'}));
    var ol = mk('text',{x:cx,y:cy,'text-anchor':'middle','dominant-baseline':'central',fill:'var(--accent)','font-size':'10','font-weight':'800','pointer-events':'none'});
    ol.textContent = 'ORCH'; og.appendChild(ol);
    var cr = mk('text',{x:cx,y:cy-30,'text-anchor':'middle',fill:'#fbbf24','font-size':'16','pointer-events':'none'});
    cr.textContent = '♚'; og.appendChild(cr);
    svg.appendChild(og);

    // Agent nodes
    ag.forEach(function(a,i) {
        var a2 = (i/ag.length)*2*Math.PI - Math.PI/2;
        var ax = cx+rx*Math.cos(a2), ay = cy+ry*Math.sin(a2);
        var st = a.status||'';
        var alive = st.indexOf('running')!==-1||st.indexOf('analyzing')!==-1||st==='pending';
        var col = st==='completed'?'var(--ok)':st==='failed'?'var(--fail)':alive?'var(--warn)':'var(--info)';
        var sel = S.selected===a.id, r = sel?26:22;
        var rc = a.resurrection_cnt||0;

        var g = mk('g',{'data-id':a.id,style:'cursor:pointer'});
        if (sel) g.appendChild(mk('circle',{cx:ax,cy:ay,r:r+6,fill:'none',stroke:'var(--accent)','stroke-width':'2','stroke-opacity':'0.3'}));
        if (alive) g.appendChild(mk('circle',{cx:ax,cy:ay,r:r+4,fill:'none',stroke:col,'stroke-width':'1.5','stroke-opacity':'0.2',class:'dp'}));

        // Outer ring for resurrected agents.
        if (rc>0 && alive) {
            g.appendChild(mk('circle',{cx:ax,cy:ay,r:r+8,fill:'none',stroke:'var(--warn)','stroke-width':'1','stroke-opacity':'0.3','stroke-dasharray':'3 3'}));
        }

        g.appendChild(mk('circle',{cx:ax,cy:ay,r:r,fill:col,'fill-opacity':'0.12',stroke:sel?'var(--accent)':col,'stroke-width':sel?'2.5':'1.8'}));

        var lbl = mk('text',{x:ax,y:ay,'text-anchor':'middle','dominant-baseline':'central',fill:'var(--text)','font-size':'10','font-weight':'600','pointer-events':'none'});
        lbl.textContent = (a.name||a.id||'?').slice(0,10); g.appendChild(lbl);

        var stl = mk('text',{x:ax,y:ay+r+14,'text-anchor':'middle',fill:col,'font-size':'8','font-weight':'500','pointer-events':'none'});
        stl.textContent = st; g.appendChild(stl);

        // Resurrect badge
        if (rc>0) {
            var bx = ax+r-4, by = ay-r+4;
            g.appendChild(mk('circle',{cx:bx,cy:by,r:9,fill:'var(--bg-card)',stroke:'var(--warn)','stroke-width':'1.2'}));
            var cl = mk('text',{x:bx,y:by+1,'text-anchor':'middle',fill:'var(--warn)','font-size':'7','font-weight':'800','pointer-events':'none'});
            cl.textContent = '↻'+rc; g.appendChild(cl);
        }

        // Invis hit area
        g.appendChild(mk('circle',{cx:ax,cy:ay,r:r+12,fill:'transparent'}));
        g.addEventListener('click',function(){select(a.id);});

        // Hover tooltip
        var tip = mk('title');
        tip.textContent = (a.name||a.id)+'\n'+st+(rc?'\nResurrections: '+rc:'')+(a.duration?'\n'+a.duration:'');
        g.appendChild(tip);

        svg.appendChild(g);
    });
}

function mk(t,a){var e=document.createElementNS(NS,t);for(var k in a)e.setAttribute(k,a[k]);return e;}

// ── Agent Cards ────────────────────
function renderCards() {
    var grid = document.getElementById('agent-grid');
    if (!grid) return;
    grid.innerHTML = '';
    var ag = S.agents;
    if (!ag||!ag.length) { grid.innerHTML = '<div class="empty">No agents</div>'; return; }

    ag.forEach(function(a) {
        var st = a.status||'';
        var dc = st==='completed'?'ok':st==='failed'?'dead':(st.indexOf('running')!==-1||st.indexOf('analyzing')!==-1||st==='pending')?'running':'pending';
        var sel = S.selected===a.id, dead = st==='failed';
        var rc = a.resurrection_cnt||0;

        var card = document.createElement('div');
        card.className = 'agent-card'+(sel?' selected':'')+(dead?' dead':'');
        card.onclick = function(){select(a.id);};

        card.innerHTML =
            '<span class="agent-dot '+dc+'"></span>'+
            '<div class="agent-info">'+
              '<div class="agent-name">'+esc(a.name||a.id)+'</div>'+
              '<div class="agent-meta">'+
                '<span>'+st+'</span>'+
                (rc?'<span class="agent-rc">↻ ×'+rc+'</span>':'')+
                (a.duration?'<span>'+a.duration+'</span>':'')+
              '</div>'+
            '</div>'+
            '<div class="agent-actions">'+
              '<button class="kill">KILL</button>'+
              (rc>0?'<button class="rc-badge" title="Resurrections">↻'+rc+'</button>':'')+
            '</div>';

        var kb = card.querySelector('button.kill');
        kb.onclick = function(e){e.stopPropagation();kill(a.id);};
        grid.appendChild(card);
    });
}

function select(id) {
    S.selected = S.selected===id ? null : id;
    renderDAG(); renderCards();
    var info = document.getElementById('dag-info');
    var a = S.agents.find(function(x){return x.id===id;});
    if (a&&S.selected) {
        info.textContent = a.name+' — '+(a.status||'?')+(a.resurrection_cnt?' | Resurrections: '+a.resurrection_cnt:'')+(a.duration?' | '+a.duration:'');
        info.className = 'dag-info visible';
    } else { info.className = 'dag-info'; }
}

// ── Event Log ──────────────────────
function mergeHistory(h) {
    h.forEach(function(e){addEvent({time:e.timestamp||e.time||new Date().toISOString(),type:e.success?'recover':'fault',action:(e.action&&e.action.type)||'event',target:(e.action&&e.action.target_id)||'',success:e.success,duration:e.duration});});
}

function addEvent(ev) {
    if (S.events.length) {
        var l = S.events[0];
        if (l.action===ev.action&&l.target===ev.target&&l.success===ev.success&&l.time===ev.time) return;
    }
    S.events.unshift(ev);
    if (S.events.length>200) S.events.length=200;
    renderLog();
}

function renderLog() {
    var el = document.getElementById('event-log');
    if (!el) return;
    var html = '<div class="log-h">TIMELINE</div>';
    if (!S.events.length) { el.innerHTML = html+'<div class="empty">Waiting for chaos...</div>'; return; }

    // Build narrative groupings.
    html += S.events.slice(0,40).map(function(ev) {
        var tc = ev.type==='fault'?'fault':ev.type==='recover'?'recover':'info';
        var icon = ev.success?'✓':'✗';
        var lbl = ev.action.replace(/_/g,' ');
        // Narrative enrichment.
        var msg = ev.target||'system';
        if (ev.success && ev.action.indexOf('kill')!==-1) msg = msg+' resurrected';
        else if (ev.success && ev.action.indexOf('leader')!==-1) msg = msg+' elected';
        else if (ev.success) msg = msg+' recovered';
        else if (ev.action.indexOf('kill')!==-1) msg = msg+' killed';
        var ic = ev.success?'var(--ok)':'var(--fail)';
        return '<div class="evt">'+
            '<span class="evt-t">'+fmtTime(ev.time)+'</span>'+
            '<span class="evt-icn" style="color:'+ic+'">'+icon+'</span>'+
            '<span class="evt-msg">'+esc(lbl)+' → '+esc(msg)+'</span>'+
            (ev.duration?'<span class="evt-d">'+fmtDur(ev.duration)+'</span>':'')+
        '</div>';
    }).join('');
    el.innerHTML = html;
}

// ── Fault Actions ──────────────────
function doLeaderKill() { fire('/arena/leader/kill','kill_leader','Leader assassinated'); }
function doOrchKill() { fire('/arena/orchestrator/kill','kill_orchestrator','Orchestrator killed'); }
function kill(id) { fire('/arena/agent/'+id+'/kill','kill_agent',id); }

function doKillSelected() {
    if (!S.selected) { alert('Select an agent first'); return; }
    kill(S.selected);
}

function doRemoveNode() {
    var id = S.nodeId || (S.selected||'');
    if (!id) { alert('Enter a Node ID or select one'); return; }
    addEvent({time:new Date().toISOString(),type:'fault',action:'remove_node',target:id,success:false,duration:0});
    post('/arena/node/'+id+'/remove').then(function(r){
        addEvent({time:new Date().toISOString(),type:r&&r.success?'recover':'fault',action:'remove_node',target:id,success:r&&r.success,duration:r&&r.duration});
        S.nodeId='';
        if(document.getElementById('node-id'))document.getElementById('node-id').value='';
        tick();
    });
}

function doRemoveEdge() {
    var f = S.edgeFrom, t = S.edgeTo;
    if (!f||!t) { alert('Enter both From and To'); return; }
    addEvent({time:new Date().toISOString(),type:'fault',action:'remove_edge',target:f+'→'+t,success:false,duration:0});
    post('/arena/edge/remove',{from:f,to:t}).then(function(r){
        addEvent({time:new Date().toISOString(),type:r&&r.success?'recover':'fault',action:'remove_edge',target:f+'→'+t,success:r&&r.success,duration:r&&r.duration});
        S.edgeFrom='';S.edgeTo='';
        var ef=document.getElementById('edge-from'),et=document.getElementById('edge-to');
        if(ef)ef.value='';if(et)et.value='';
        tick();
    });
}

function doAgentFault(act) {
    var id = S.selected;
    if (!id) { alert('Select an agent first'); return; }
    addEvent({time:new Date().toISOString(),type:'fault',action:act,target:id,success:false,duration:0});
    post('/arena/agent/'+id+'/'+act).then(function(r){
        addEvent({time:new Date().toISOString(),type:r&&r.success?'recover':'fault',action:act,target:id,success:r&&r.success,duration:r&&r.duration});
        tick();
    });
}

function doAgentSlow() {
    var id = S.selected;
    if (!id) { alert('Select an agent first'); return; }
    var d = prompt('Delay (seconds):','5'); if (!d) return;
    addEvent({time:new Date().toISOString(),type:'fault',action:'slow',target:id,success:false,duration:0});
    post('/arena/agent/'+id+'/slow',{duration:d+'s'}).then(function(r){
        addEvent({time:new Date().toISOString(),type:r&&r.success?'recover':'fault',action:'slow',target:id,success:r&&r.success,duration:r&&r.duration});
        tick();
    });
}

function fire(path, label, target) {
    addEvent({time:new Date().toISOString(),type:'fault',action:label,target:target,success:false,duration:0});
    post(path).then(function(r){
        addEvent({time:new Date().toISOString(),type:r&&r.success?'recover':'fault',action:label,target:target,success:r&&r.success,duration:r&&r.duration});
        tick();
    });
}

// ── Helpers ────────────────────────
function esc(s){if(!s)return'';var d=document.createElement('div');d.textContent=String(s);return d.innerHTML;}
function fmtTime(ts){if(!ts)return'--:--';var d=new Date(ts);if(!isNaN(d.getTime())){var p=function(n){return n<10?'0'+n:String(n);};return p(d.getHours())+':'+p(d.getMinutes())+':'+p(d.getSeconds());}return(ts+'').length>8?(ts+'').slice(11,19):ts;}
function fmtDur(d){if(!d)return'';if(typeof d==='number'){if(d>1e9)return(d/1e9).toFixed(1)+'s';if(d>1e6)return(d/1e6).toFixed(0)+'ms';return d+'ns';}return d+'';}

// ── Anim loop ──────────────────────
(function loop() {
    var svg = document.getElementById('dag');
    if (svg) {
        var cs = svg.querySelectorAll('.dp');
        for (var i=0;i<cs.length;i++) {
            var op = parseFloat(cs[i].getAttribute('stroke-opacity')||'0.2');
            cs[i].setAttribute('stroke-opacity', String(op<0.5?op+0.005:0.1));
        }
    }
    requestAnimationFrame(loop);
})();

document.addEventListener('DOMContentLoaded', init);
})();
