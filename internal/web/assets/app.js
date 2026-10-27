/* ccl 用量看板前端
 *
 * 零依赖：没有框架、没有图表库——图表是手写 inline SVG。
 * 配色取自 dataviz skill 参考调色板（浅/深两套各自选定，已过校验脚本）。
 *
 * 关键约定：
 *   - 不可信数据（项目名 / 提供商名 / 模型名）一律用 textContent 插入，
 *     绝不 innerHTML 拼接。
 *   - 切换筛选 / 窗口时用 AbortController 取消在途请求，迟到的旧响应不渲染。
 *   - 重新取数期间保留上一帧画面（降透明度），不闪骨架、不跳布局。
 */
(function () {
  'use strict';

  var SVG_NS = 'http://www.w3.org/2000/svg';

  /* 四类 token 的系列定义。颜色按固定槽位分配（不随筛选重排）。 */
  var SERIES = [
    { key: 'input',          label: '输入',     color: 'var(--series-1)' },
    { key: 'output',         label: '输出',     color: 'var(--series-2)' },
    { key: 'cache_creation', label: '缓存写入', color: 'var(--series-3)' },
    { key: 'cache_read',     label: '缓存读取', color: 'var(--series-4)' }
  ];

  var state = {
    window: '7d',
    project: '',
    provider: '',
    model: '',
    auto: true,
    trendAsTable: false,
    data: null
  };

  var inflight = null;   /* 当前在途请求的 AbortController */
  var timer = null;      /* 自动刷新定时器 */
  var resizeTimer = null;

  /* ------------------------------------------------------------------ */
  /* 小工具                                                               */
  /* ------------------------------------------------------------------ */

  function $(id) { return document.getElementById(id); }

  function svg(tag, attrs) {
    var e = document.createElementNS(SVG_NS, tag);
    if (attrs) {
      for (var k in attrs) {
        if (Object.prototype.hasOwnProperty.call(attrs, k) && attrs[k] !== null && attrs[k] !== undefined) {
          e.setAttribute(k, String(attrs[k]));
        }
      }
    }
    return e;
  }

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) { e.className = cls; }
    if (text !== undefined && text !== null) { e.textContent = String(text); }
    return e;
  }

  function fmtInt(n) {
    n = Number(n) || 0;
    return n.toLocaleString('zh-CN');
  }

  function trimNum(v) {
    if (v < 10) { return v.toFixed(2); }
    if (v < 100) { return v.toFixed(1); }
    return v.toFixed(0);
  }

  function fmtCompact(n) {
    n = Number(n) || 0;
    var abs = Math.abs(n);
    if (abs < 1000) { return String(Math.round(n)); }
    if (abs < 1e6) { return trimNum(n / 1e3) + 'K'; }
    if (abs < 1e9) { return trimNum(n / 1e6) + 'M'; }
    return trimNum(n / 1e9) + 'B';
  }

  function fmtCost(v) {
    v = Number(v) || 0;
    if (v === 0) { return '$0'; }
    if (Math.abs(v) < 0.01) { return '$' + v.toFixed(4); }
    return '$' + v.toFixed(2);
  }

  function fmtPct(v) {
    return ((Number(v) || 0) * 100).toFixed(1) + '%';
  }

  function fmtTime(iso) {
    if (!iso) { return '—'; }
    var d = new Date(iso);
    if (isNaN(d.getTime())) { return String(iso); }
    var p = function (n) { return (n < 10 ? '0' : '') + n; };
    return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) +
      ' ' + p(d.getHours()) + ':' + p(d.getMinutes());
  }

  function totalTokens(t) {
    if (!t) { return 0; }
    return (t.input || 0) + (t.output || 0) + (t.cache_creation || 0) + (t.cache_read || 0);
  }

  /* ------------------------------------------------------------------ */
  /* 提示气泡                                                             */
  /* ------------------------------------------------------------------ */

  var tip = $('tooltip');

  function showTip(evt, title, rows) {
    tip.textContent = '';
    if (title) { tip.appendChild(el('div', 'tt-title', title)); }
    rows.forEach(function (r) {
      var line = el('div', 'tt-row');
      if (r.color) {
        var key = el('span', 'tt-key');
        key.style.background = r.color;
        line.appendChild(key);
      }
      line.appendChild(el('span', 'tt-val', r.value));
      if (r.name) { line.appendChild(el('span', 'tt-name', r.name)); }
      tip.appendChild(line);
    });
    tip.hidden = false;
    moveTip(evt);
  }

  function moveTip(evt) {
    var pad = 14;
    var x = evt.clientX + pad;
    var y = evt.clientY + pad;
    var r = tip.getBoundingClientRect();
    if (x + r.width > window.innerWidth - 8) { x = evt.clientX - r.width - pad; }
    if (y + r.height > window.innerHeight - 8) { y = evt.clientY - r.height - pad; }
    tip.style.left = Math.max(4, x) + 'px';
    tip.style.top = Math.max(4, y) + 'px';
  }

  function hideTip() { tip.hidden = true; }

  /* ------------------------------------------------------------------ */
  /* 取数                                                                 */
  /* ------------------------------------------------------------------ */

  function buildQuery() {
    var p = new URLSearchParams();
    p.set('window', state.window);
    if (state.project) { p.set('project', state.project); }
    if (state.provider) { p.set('provider', state.provider); }
    if (state.model) { p.set('model', state.model); }
    return p.toString();
  }

  function load() {
    /* 取消在途请求：窗口 / 筛选变了以后，迟到的旧响应不得覆盖当前视图 */
    if (inflight) { inflight.abort(); }
    var ctrl = new AbortController();
    inflight = ctrl;
    $('content').classList.add('loading');

    fetch('/api/stats?' + buildQuery(), { signal: ctrl.signal, credentials: 'same-origin' })
      .then(function (r) {
        if (!r.ok) {
          return r.json().catch(function () { return {}; }).then(function (j) {
            throw new Error(j.error || ('HTTP ' + r.status));
          });
        }
        return r.json();
      })
      .then(function (data) {
        if (ctrl.signal.aborted) { return; }
        state.data = data;
        render(data);
      })
      .catch(function (err) {
        if (err && err.name === 'AbortError') { return; }
        showError(err && err.message ? err.message : String(err));
      })
      .then(function () {
        if (inflight === ctrl) {
          inflight = null;
          $('content').classList.remove('loading');
        }
      });
  }

  function showError(msg) {
    var box = $('sessions');
    box.textContent = '';
    box.appendChild(el('div', 'empty', '加载失败：' + msg));
  }

  function scheduleAuto() {
    if (timer) { clearInterval(timer); timer = null; }
    if (state.auto) {
      timer = setInterval(function () {
        /* 用户正在输入筛选词时不打断 */
        if (document.activeElement && document.activeElement.tagName === 'INPUT' &&
            document.activeElement.type === 'text') { return; }
        load();
      }, 30000);
    }
  }

  /* ------------------------------------------------------------------ */
  /* 渲染                                                                 */
  /* ------------------------------------------------------------------ */

  function render(data) {
    renderKpis(data);
    renderLegend();
    renderTrend(data);
    renderTrendTable(data);
    renderShare('share-project', data.projects);
    renderShare('share-provider', data.providers);
    renderShare('share-model', data.models);
    renderSessions(data);
    $('generated-at').textContent = '更新于 ' + fmtTime(data.generated_at);
  }

  function renderKpis(data) {
    var host = $('kpis');
    host.textContent = '';
    var t = data.totals || {};
    var cards = [
      { label: '总 token', value: fmtCompact(totalTokens(t)), hero: true },
      { label: '输入 token', value: fmtCompact(t.input) },
      { label: '输出 token', value: fmtCompact(t.output) },
      { label: '缓存写入', value: fmtCompact(t.cache_creation) },
      { label: '缓存读取', value: fmtCompact(t.cache_read) },
      { label: '总 cost', value: fmtCost(t.cost_usd) },
      { label: '会话数', value: fmtInt(t.sessions) }
    ];
    cards.forEach(function (c) {
      var card = el('div', 'kpi' + (c.hero ? ' hero' : ''));
      card.appendChild(el('div', 'label', c.label));
      card.appendChild(el('div', 'value', c.value));
      host.appendChild(card);
    });
  }

  function renderLegend() {
    var host = $('legend');
    host.textContent = '';
    SERIES.forEach(function (s) {
      var item = el('span', 'item');
      var sw = el('span', 'swatch');
      sw.style.background = s.color;
      item.appendChild(sw);
      item.appendChild(el('span', null, s.label));
      host.appendChild(item);
    });
  }

  /* ---------------- 趋势图（token 堆叠柱 + cost 折线，两个独立面板） --- */

  var GAP = 2;            /* 段与段之间的表面缝（px） */
  var PAD = { l: 56, r: 14, t: 10, b: 26 };

  function renderTrend(data) {
    var host = $('trend');
    host.textContent = '';
    var buckets = data.buckets || [];
    var totals = data.totals || {};

    if (!buckets.length || !totals.sessions) {
      host.appendChild(el('div', 'empty', '该窗口内没有用量'));
      return;
    }

    var W = Math.max(320, host.clientWidth || 640);
    var tokH = 190, costH = 96, gapH = 40;
    var tokTop = PAD.t;
    var costTop = tokTop + tokH + gapH;
    var xAxisY = costTop + costH;
    var H = xAxisY + PAD.b;

    var plotW = W - PAD.l - PAD.r;
    var n = buckets.length;
    var band = plotW / n;
    var barW = Math.max(3, Math.min(24, band * 0.62));
    var center = function (i) { return PAD.l + band * (i + 0.5); };

    /* 轴上限 */
    var maxTok = 0, maxCost = 0;
    buckets.forEach(function (b) {
      var tt = totalTokens(b);
      if (tt > maxTok) { maxTok = tt; }
      if ((b.cost_usd || 0) > maxCost) { maxCost = b.cost_usd; }
    });
    if (maxTok <= 0) { maxTok = 1; }
    if (maxCost <= 0) { maxCost = 1; }

    var root = svg('svg', {
      width: W, height: H, viewBox: '0 0 ' + W + ' ' + H,
      role: 'img', 'aria-label': '用量趋势图'
    });

    /* --- token 面板网格 + 轴刻度 --- */
    drawPanel(root, PAD.l, tokTop, plotW, tokH, maxTok, function (v) { return fmtCompact(v); });
    /* --- cost 面板网格 + 轴刻度 --- */
    drawPanel(root, PAD.l, costTop, plotW, costH, maxCost, function (v) { return fmtCost(v); });

    /* --- 堆叠柱 --- */
    buckets.forEach(function (b, i) {
      var x = center(i) - barW / 2;
      var baseline = tokTop + tokH;
      var cursor = baseline;
      var segs = SERIES.filter(function (s) { return (b[s.key] || 0) > 0; });
      segs.forEach(function (s, si) {
        var v = b[s.key] || 0;
        var h = (v / maxTok) * tokH;
        var top = cursor - h;
        var isTop = si === segs.length - 1;
        /* 非最底段从底部收 GAP —— 形成段间表面缝，段高不失真 */
        var bottom = cursor - (si === 0 ? 0 : GAP);
        var rect = isTop ? roundedTopPath(x, top, barW, Math.max(0, bottom - top), 4)
                         : svg('rect', { x: x, y: top, width: barW, height: Math.max(0, bottom - top) });
        if (isTop) {
          var p = svg('path', { d: rect, fill: s.color, class: 'seg' });
          root.appendChild(p);
          attachSegTip(p, b, s);
        } else {
          rect.setAttribute('fill', s.color);
          rect.setAttribute('class', 'seg');
          root.appendChild(rect);
          attachSegTip(rect, b, s);
        }
        cursor = top;
      });
    });

    /* --- cost 折线（独立面板，单一 y 轴；不与 token 共用刻度） --- */
    var pts = buckets.map(function (b, i) {
      return [center(i), costTop + costH - ((b.cost_usd || 0) / maxCost) * costH];
    });
    if (pts.length > 1) {
      var d = pts.map(function (p, i) { return (i === 0 ? 'M' : 'L') + p[0].toFixed(1) + ' ' + p[1].toFixed(1); }).join(' ');
      root.appendChild(svg('path', {
        d: d, fill: 'none', stroke: 'var(--series-1)', 'stroke-width': 2,
        'stroke-linejoin': 'round', 'stroke-linecap': 'round'
      }));
    }
    /* 末端标记（r>=4 + 2px 表面环） */
    if (pts.length) {
      var last = pts[pts.length - 1];
      root.appendChild(svg('circle', {
        cx: last[0], cy: last[1], r: 4.5, fill: 'var(--series-1)',
        stroke: 'var(--surface-1)', 'stroke-width': 2
      }));
    }
    /* 折线命中点（>=24px 命中区） */
    buckets.forEach(function (b, i) {
      var hit = svg('circle', {
        cx: pts[i][0], cy: pts[i][1], r: 12, fill: 'transparent', class: 'hit', tabindex: '0'
      });
      hit.addEventListener('pointerenter', function (e) { showTip(e, b.label || b.bucket_iso, costRows(b)); });
      hit.addEventListener('pointermove', moveTip);
      hit.addEventListener('pointerleave', hideTip);
      root.appendChild(hit);
    });

    /* --- x 轴 --- */
    root.appendChild(svg('line', {
      x1: PAD.l, y1: xAxisY, x2: PAD.l + plotW, y2: xAxisY,
      stroke: 'var(--axis)', 'stroke-width': 1
    }));
    var step = Math.max(1, Math.ceil(n / Math.max(2, Math.floor(plotW / 62))));
    buckets.forEach(function (b, i) {
      if (i % step !== 0 && i !== n - 1) { return; }
      var t = svg('text', {
        x: center(i), y: xAxisY + 16, 'text-anchor': 'middle', class: 'tick'
      });
      t.textContent = b.label || '';
      root.appendChild(t);
    });

    host.appendChild(root);
  }

  /* roundedTopPath 生成"顶端 4px 圆角、底端方角"的柱形路径。 */
  function roundedTopPath(x, y, w, h, r) {
    if (h <= 0) { return ''; }
    var rr = Math.min(r, w / 2, h);
    return 'M' + x + ' ' + (y + h) +
      ' L' + x + ' ' + (y + rr) +
      ' Q' + x + ' ' + y + ' ' + (x + rr) + ' ' + y +
      ' L' + (x + w - rr) + ' ' + y +
      ' Q' + (x + w) + ' ' + y + ' ' + (x + w) + ' ' + (y + rr) +
      ' L' + (x + w) + ' ' + (y + h) + ' Z';
  }

  /* drawPanel 画一个面板的网格线、基线与 y 轴刻度。 */
  function drawPanel(root, x, y, w, h, max, fmt) {
    [0, 0.5, 1].forEach(function (frac) {
      var gy = y + h - frac * h;
      root.appendChild(svg('line', {
        x1: x, y1: gy, x2: x + w, y2: gy,
        stroke: frac === 0 ? 'var(--axis)' : 'var(--grid)', 'stroke-width': 1
      }));
      var t = svg('text', {
        x: x - 8, y: gy + 4, 'text-anchor': 'end', class: 'tick'
      });
      t.textContent = fmt(max * frac);
      root.appendChild(t);
    });
  }

  function attachSegTip(node, b, s) {
    node.addEventListener('pointerenter', function (e) {
      showTip(e, b.label || b.bucket_iso, [{
        color: s.color,
        value: fmtInt(b[s.key] || 0),
        name: s.label
      }, {
        value: fmtCompact(totalTokens(b)),
        name: '总 token'
      }, {
        value: fmtCost(b.cost_usd),
        name: 'cost'
      }]);
    });
    node.addEventListener('pointermove', moveTip);
    node.addEventListener('pointerleave', hideTip);
  }

  function costRows(b) {
    return [{
      color: 'var(--series-1)',
      value: fmtCost(b.cost_usd),
      name: 'cost'
    }, {
      value: fmtCompact(totalTokens(b)),
      name: '总 token'
    }];
  }

  /* ---------------- 趋势的表格视图（浅色模式对比度 relief） ------------- */

  function renderTrendTable(data) {
    var host = $('trend-table');
    host.textContent = '';
    var buckets = data.buckets || [];
    if (!buckets.length) { return; }

    var table = el('table');
    var thead = el('thead');
    var hr = el('tr');
    ['时间', '会话', '输入', '输出', '缓存写入', '缓存读取', '总 token', 'COST'].forEach(function (h, i) {
      var th = el('th', i >= 1 ? 'num' : null, h);
      hr.appendChild(th);
    });
    thead.appendChild(hr);
    table.appendChild(thead);

    var tbody = el('tbody');
    buckets.forEach(function (b) {
      var tr = el('tr');
      tr.appendChild(el('td', null, b.label || b.bucket_iso));
      tr.appendChild(el('td', 'num', fmtInt(b.sessions)));
      tr.appendChild(el('td', 'num', fmtInt(b.input)));
      tr.appendChild(el('td', 'num', fmtInt(b.output)));
      tr.appendChild(el('td', 'num', fmtInt(b.cache_creation)));
      tr.appendChild(el('td', 'num', fmtInt(b.cache_read)));
      tr.appendChild(el('td', 'num', fmtInt(totalTokens(b))));
      tr.appendChild(el('td', 'num', fmtCost(b.cost_usd)));
      tbody.appendChild(tr);
    });
    table.appendChild(tbody);
    host.appendChild(table);
  }

  /* ---------------- 占比图（横向条形，单系列） ------------------------- */

  function renderShare(hostId, rows) {
    var host = $(hostId);
    host.textContent = '';
    if (!rows || !rows.length) {
      host.appendChild(el('div', 'empty', '暂无数据'));
      return;
    }

    var W = Math.max(240, host.clientWidth || 300);
    var rowsShown = rows.slice(0, 8);
    var rowH = 30, barH = 14;
    /* 名称列要放得下模型名（"MiniMax-M3.1-Flash[1m]" 22 字符），
     * 否则两个不同 model 截断后看起来像同一个。 */
    var labelW = Math.max(96, Math.min(200, Math.round(W * 0.42)));
    var valueW = 72;
    var plotW = Math.max(40, W - labelW - valueW);
    var H = rowsShown.length * rowH + 6;

    var root = svg('svg', {
      width: W, height: H, viewBox: '0 0 ' + W + ' ' + H,
      role: 'img', 'aria-label': '占比条形图'
    });

    var maxTokens = 0;
    rowsShown.forEach(function (r) { if (r.tokens > maxTokens) { maxTokens = r.tokens; } });
    if (maxTokens <= 0) { maxTokens = 1; }

    rowsShown.forEach(function (r, i) {
      var y = i * rowH + 4;
      var barY = y + (rowH - barH) / 2 - 4;

      /* 名称（文本用 ink 色，不用数据色） */
      var name = svg('text', { x: 0, y: barY + barH - 2, class: 'tick' });
      name.setAttribute('fill', 'var(--text-secondary)');
      name.textContent = truncate(r.name, Math.floor((labelW - 6) / 6.3));
      var title = svg('title');
      title.textContent = r.name;
      name.appendChild(title);
      root.appendChild(name);

      /* 条形：单系列 → 统一用槽位 1 的颜色 */
      var w = Math.max(2, (r.tokens / maxTokens) * plotW);
      var rect = svg('rect', {
        x: labelW, y: barY, width: w, height: barH, rx: 4, ry: 4,
        fill: 'var(--series-1)', class: 'seg'
      });
      root.appendChild(rect);

      /* 末端直接标数值（relief：浅色模式下颜色对比度不足，靠可见标签） */
      var val = svg('text', {
        x: labelW + plotW + 6, y: barY + barH - 2, class: 'tick'
      });
      val.setAttribute('fill', 'var(--text-secondary)');
      val.textContent = fmtPct(r.token_share);
      root.appendChild(val);

      var hit = svg('rect', {
        x: 0, y: y - 2, width: W, height: rowH, fill: 'transparent', class: 'hit', tabindex: '0'
      });
      hit.addEventListener('pointerenter', function (e) {
        showTip(e, r.name, [
          { color: 'var(--series-1)', value: fmtCompact(r.tokens), name: 'token' },
          { value: fmtPct(r.token_share), name: 'token 占比' },
          { value: fmtCost(r.cost_usd), name: 'cost' },
          { value: fmtPct(r.cost_share), name: 'cost 占比' }
        ]);
      });
      hit.addEventListener('pointermove', moveTip);
      hit.addEventListener('pointerleave', hideTip);
      root.appendChild(hit);
    });

    host.appendChild(root);

    if (rows.length > rowsShown.length) {
      host.appendChild(el('div', 'empty', '另有 ' + (rows.length - rowsShown.length) + ' 项未显示（明细见会话表）'));
    }
  }

  function truncate(s, n) {
    s = String(s === null || s === undefined ? '' : s);
    if (n <= 1) { return s; }
    return s.length > n ? s.slice(0, n - 1) + '…' : s;
  }

  /* ---------------- 会话明细表 ---------------------------------------- */

  function renderSessions(data) {
    var host = $('sessions');
    host.textContent = '';
    var rows = data.sessions || [];
    if (!rows.length) {
      host.appendChild(el('div', 'empty', '该窗口内没有会话'));
      return;
    }

    var table = el('table');
    var thead = el('thead');
    var hr = el('tr');
    var cols = ['时间', '项目', '提供商', '模型', '输入', '输出', '缓存写入', '缓存读取', 'COST'];
    cols.forEach(function (h, i) {
      hr.appendChild(el('th', i >= 4 ? 'num' : null, h));
    });
    thead.appendChild(hr);
    table.appendChild(thead);

    var tbody = el('tbody');
    rows.forEach(function (r) {
      var tr = el('tr');
      tr.appendChild(el('td', null, fmtTime(r.started_at)));
      tr.appendChild(el('td', null, r.project));
      tr.appendChild(el('td', null, r.provider));
      tr.appendChild(el('td', null, r.model));
      tr.appendChild(el('td', 'num', fmtInt(r.input)));
      tr.appendChild(el('td', 'num', fmtInt(r.output)));
      tr.appendChild(el('td', 'num', fmtInt(r.cache_creation)));
      tr.appendChild(el('td', 'num', fmtInt(r.cache_read)));
      tr.appendChild(el('td', 'num', fmtCost(r.cost_usd)));
      tbody.appendChild(tr);
    });
    table.appendChild(tbody);
    host.appendChild(table);
  }

  /* ------------------------------------------------------------------ */
  /* 事件绑定                                                             */
  /* ------------------------------------------------------------------ */

  function applyWindow(w) {
    state.window = w;
    Array.prototype.forEach.call(document.querySelectorAll('.preset'), function (b) {
      b.setAttribute('aria-pressed', b.dataset.window === w ? 'true' : 'false');
    });
    load();
  }

  function bind() {
    Array.prototype.forEach.call(document.querySelectorAll('.preset'), function (b) {
      b.addEventListener('click', function () { applyWindow(b.dataset.window); });
    });

    $('refresh').addEventListener('click', function () { load(); });

    ['f-project', 'f-provider', 'f-model'].forEach(function (id) {
      var input = $(id);
      var key = id === 'f-project' ? 'project' : (id === 'f-provider' ? 'provider' : 'model');
      var timerId = null;
      input.addEventListener('input', function () {
        if (timerId) { clearTimeout(timerId); }
        timerId = setTimeout(function () {
          state[key] = input.value.trim();
          load();
        }, 350);
      });
      input.addEventListener('keydown', function (e) {
        if (e.key === 'Enter') {
          if (timerId) { clearTimeout(timerId); }
          state[key] = input.value.trim();
          load();
        }
      });
    });

    $('auto').addEventListener('change', function (e) {
      state.auto = e.target.checked;
      scheduleAuto();
    });

    $('trend-toggle').addEventListener('click', function () {
      state.trendAsTable = !state.trendAsTable;
      this.setAttribute('aria-pressed', state.trendAsTable ? 'true' : 'false');
      this.textContent = state.trendAsTable ? '图表视图' : '表格视图';
      $('trend').hidden = state.trendAsTable;
      $('trend-table').hidden = !state.trendAsTable;
    });

    $('theme-toggle').addEventListener('click', function () {
      var root = document.documentElement;
      var cur = root.getAttribute('data-theme');
      if (!cur) {
        cur = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
      }
      root.setAttribute('data-theme', cur === 'dark' ? 'light' : 'dark');
    });

    window.addEventListener('resize', function () {
      if (resizeTimer) { clearTimeout(resizeTimer); }
      resizeTimer = setTimeout(function () {
        if (state.data) { render(state.data); }
      }, 160);
    });

    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') { hideTip(); }
    });
  }

  bind();
  scheduleAuto();
  load();
})();
