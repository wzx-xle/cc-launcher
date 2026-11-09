/* ccl 控制台 —— 提供商视图
 *
 * 左栏：provider 列表（+ 新建）
 * 右栏：额度卡 + 原文 JSON 编辑器（带语法高亮）
 *
 * 口径约定（决策 6）：额度卡的阈值、条形图、数值格式**与状态栏一致**——
 * 网页显示 72%、状态栏显示 68% 会让人开始怀疑哪个是真的。
 */
(function () {
  'use strict';

  var state = {
    list: [],
    current: '',
    etag: '',
    quota: null,
    creating: false
  };

  /* ------------------------------------------------------------------ */
  /* 小工具                                                               */
  /* ------------------------------------------------------------------ */

  function el(tag, cls, text) {
    var node = document.createElement(tag);
    if (cls) { node.className = cls; }
    if (text !== undefined && text !== null) { node.textContent = text; }
    return node;
  }

  function sidebarHost() { return document.getElementById('sidebar'); }
  function mainHost() { return document.getElementById('view-providers'); }

  /* fmtAmount 格式化绝对量（去掉无意义的尾随零）。 */
  function fmtAmount(v) {
    if (typeof v !== 'number' || !isFinite(v)) { return '--'; }
    var s = v.toFixed(2);
    if (s.indexOf('.') >= 0) { s = s.replace(/\.?0+$/, ''); }
    return s;
  }

  /* unitSuffix：label 本身就是 unit 时不重复显示（与状态栏同规则）。 */
  function unitSuffix(label, unit) {
    if (!unit || label === unit) { return ''; }
    return ' ' + unit;
  }

  /* pctColor：与状态栏同一套阈值（90/75/50）。 */
  function pctColor(pct) {
    if (pct >= 90) { return 'bad-bar'; }
    if (pct >= 75) { return 'bad-bar'; }
    if (pct >= 50) { return 'warn-bar'; }
    return 'ok-bar';
  }

  /* ------------------------------------------------------------------ */
  /* 额度渲染口径                                                        */
  /* ------------------------------------------------------------------ */

  /* 下面的函数与终端状态栏**逐条对齐**（~/.claude/statusline.js 的
   * renderDimension / rateLimitStr / balanceStr / quotaKindStr）。
   *
   * **按 kind 分派**，不是"一律显示已用/总量"：
   *
   *   window  → 只有百分比 + 条形      （rateLimitStr）
   *   balance → 只有数字，无条形        （balanceStr）
   *   其余    → 百分比 + 条形 + 已用/总量（quotaKindStr）
   *
   * 之前网页不分 kind，window 维度也显示"已用/总量" → **与状态栏不一致**
   * （minimax 这类就是 window），顺带看着冗余："6% 6/100 %"。spec 要求
   * "the same value formats as the terminal status line" —— 这条本来就没
   * 被满足。
   *
   * 纯函数（不碰 DOM）→ 能被 node 单测直接断言，见 internal/web/jstest/。
   */

  /* dimKind 返回某个维度的渲染类别：'balance' | 'window' | 'quota'。 */
  function dimKind(d) {
    if (d.kind === 'balance') { return 'balance'; }
    if (d.kind === 'window') { return 'window'; }
    return 'quota';
  }

  /* dimPctText 返回百分比文本。
   *
   * window（rateLimitStr）与 quota（quotaKindStr）的百分比口径**不同**：
   *   - window：一律四舍五入（0.025% → "0%"）
   *   - quota ：小于 1% 时保留两位小数（0.025% → "0.03%"），免得看着像没用过
   *
   * 之前网页不分 kind，一律用 quota 的口径 → window 维度上与状态栏不一致。
   */
  function dimPctText(pct, kind) {
    if (kind === 'window') { return String(Math.round(pct)); }
    if (pct > 0 && pct < 1) { return pct.toFixed(2); }
    return String(Math.round(pct));
  }

  /* dimText 返回维度要显示的数值文本（不含条形、不含重置时间）。 */
  function dimText(d) {
    var label = d.label || d.id || '';
    var kind = dimKind(d);
    var unit = unitSuffix(label, d.unit);

    if (kind === 'balance') {
      return fmtAmount(d.remaining) + unit;
    }
    var pct = d.used_percentage;
    if (typeof pct !== 'number') {
      // 没有百分比（分母未知）→ 只显示能显示的量（quotaKindStr 同规则）
      return fmtAmount(d.remaining) + unit;
    }
    var text = dimPctText(pct, kind) + '%';
    // 已用/总量：只有 quota 类别有（window 的 rateLimitStr 没有这一段）
    if (kind === 'quota' && typeof d.used === 'number' && typeof d.limit === 'number') {
      text += ' ' + fmtAmount(d.used) + '/' + fmtAmount(d.limit) + unit;
    }
    return text;
  }

  /* barRatio 返回条形的填充比例（0..1）。
   *
   * 状态栏用 10 格离散（Math.round(pct/100*10)），网页用连续条 → 比例一样，
   * 只是介质不同（终端字符 vs 网页 CSS）。小百分比（0.03%）在连续条上能看出
   * 一丝，状态栏的四舍五入会显示成 0 格 —— 各有各的取舍，**数值口径一致**。
   */
  function barRatio(pct) {
    if (typeof pct !== 'number' || !isFinite(pct)) { return 0; }
    var r = pct / 100;
    if (r < 0) { return 0; }
    if (r > 1) { return 1; }
    return r;
  }

  /* fmtReset：重置时间（与状态栏同规则——跨天带日期）。 */
  function fmtReset(resetsAt, durationSeconds) {
    var d = new Date(resetsAt * 1000);
    var now = new Date();
    var pad = function (n) { return (n < 10 ? '0' : '') + n; };
    var hh = pad(d.getHours());
    var mm = pad(d.getMinutes());
    var sameDay = d.toDateString() === now.toDateString();
    var includeDate = typeof durationSeconds === 'number' && durationSeconds > 86400;
    if (includeDate || !sameDay) {
      return ' -> ' + (d.getMonth() + 1) + '/' + d.getDate() + ' ' + hh + ':' + mm;
    }
    return ' -> ' + hh + ':' + mm;
  }

  /* ageText：把 fetched_at 说成"多久之前"。 */
  function ageText(fetchedAt) {
    var sec = Math.max(0, Math.floor(Date.now() / 1000) - fetchedAt);
    if (sec < 60) { return sec + ' 秒前'; }
    if (sec < 3600) { return Math.floor(sec / 60) + ' 分钟前'; }
    if (sec < 86400) { return Math.floor(sec / 3600) + ' 小时前'; }
    return Math.floor(sec / 86400) + ' 天前';
  }

  /* ------------------------------------------------------------------ */
  /* 额度卡                                                               */
  /* ------------------------------------------------------------------ */

  /* barEl 造一个 CSS 进度条。
   *
   * **为什么不用字符 █░**：那两个字形在浏览器里高度不同（█ 填满字框、░ 只占
   * 下半部分）→ 拼出来上下参差；很多字体还把 ░ 渲染成斜线纹理。终端里是
   * 点阵等宽字体才整齐 —— 网页不该沿用。
   *
   * **数值口径一个字没动**（阈值 90/75/50、百分比口径、比例分母 10 格），
   * 只是呈现介质不同：终端字符 vs 网页 CSS。
   */
  function barEl(pct) {
    var bar = el('span', 'bar ' + pctColor(pct));
    var fill = el('span', 'fill');
    var ratio = barRatio(pct);
    fill.style.width = (ratio * 100) + '%';
    // 有占用就留一点最小宽度（0.03% 也不至于完全看不见）。
    // **只在真有占用时给** —— 用 CSS 的 min-width 会让 0% 也露出一小条色块，
    // 看着像"用了一点点"，实际是 0。
    if (ratio > 0) { fill.style.minWidth = '2px'; }
    bar.appendChild(fill);
    return bar;
  }

  function dimRow(d) {
    var label = d.label || d.id || '';
    var kind = dimKind(d);
    var row = el('div', 'quota-dim');

    row.appendChild(el('span', 'label', label));

    // balance（无分母）与"没有百分比"（分母未知）：只显示数字，无条形
    if (kind === 'balance' || typeof d.used_percentage !== 'number') {
      row.appendChild(el('span', 'val', dimText(d)));
      return row;
    }

    row.appendChild(barEl(d.used_percentage));
    row.appendChild(el('span', 'val', dimText(d)));

    // 重置时间：只有 window 类别有（rateLimitStr 有这一段，quotaKindStr 没有）
    if (kind === 'window' && typeof d.resets_at === 'number') {
      row.appendChild(el('span', 'reset', fmtReset(d.resets_at, d.duration_seconds)));
    }
    return row;
  }

  function renderQuota(host, quota) {
    /* 头部与内容**分开**：内容区整体替换，head（含刷新按钮）保留。
     *
     * 之前是 `host.textContent = ''` 换掉整张卡 → 刷新失败时**刷新按钮一起消失**
     * → 用户没法重试，只能切 provider 走一圈。
     */
    var head = host.querySelector('.quota-head');
    if (!head) {
      head = el('div', 'quota-head');
      host.appendChild(head);
    }
    head.textContent = '';
    head.appendChild(el('h3', null, '额度'));
    head.appendChild(el('span', 'spacer')); /* 把刷新按钮推到标题行最右 */
    var refreshBtn = el('button', 'btn ghost', '刷新');
    refreshBtn.type = 'button';
    refreshBtn.title = '联网查一次（按需，不受"只刷活跃 provider"的限制）';
    refreshBtn.addEventListener('click', function () { doRefreshQuota(refreshBtn); });
    head.appendChild(refreshBtn);

    var body = host.querySelector('.quota-body');
    if (!body) {
      body = el('div', 'quota-body');
      host.appendChild(body);
    }
    body.textContent = '';

    if (!quota) {
      body.appendChild(el('div', 'quota-note', '加载中…'));
      return;
    }

    if (!quota.has_quota) {
      body.appendChild(el('div', 'quota-note', '这个 provider 没有额度接口。'));
      return;
    }

    if (!quota.found) {
      /* **"没采到" ≠ "无限额"**：serve 只刷新最近有活动的 provider。
       * 画成空条会让人放心地把量用爆。 */
      var miss = el('div', 'quota-note');
      miss.appendChild(el('div', null, '近期无活动，未采集额度。'));
      miss.appendChild(el('div', null, '（这不是"无限额"——点「刷新」按需查一次。）'));
      body.appendChild(miss);
      return;
    }

    var dims = quota.dimensions || [];
    if (dims.length === 0) {
      body.appendChild(el('div', 'quota-note', '没有额度维度。'));
    }
    dims.forEach(function (d) { body.appendChild(dimRow(d)); });

    var note = el('div', 'quota-note');
    note.appendChild(el('span', null, ageText(quota.fetched_at) + '采集'));
    if (quota.stale) {
      note.appendChild(el('span', null, ' · 数据已过期'));
    }
    body.appendChild(note);
  }

  function doRefreshQuota(btn) {
    if (!state.current) { return; }
    var provider = state.current; /* 快照：期间切走了就不往新卡上画 */
    btn.disabled = true;
    btn.textContent = '刷新中…';
    window.CCL.api.send('POST', '/api/providers/' + encodeURIComponent(provider) + '/quota/refresh', {})
      .then(function (q) {
        if (provider !== state.current) { return; }
        state.quota = q;
        renderQuota(quotaHost(), q);
      })
      .catch(function (err) {
        var host = quotaHost();
        if (!host) { return; }
        /* 只换**内容区** —— head（含刷新按钮）留着，用户能直接再点一次重试 */
        var body = host.querySelector('.quota-body');
        if (!body) { return; }
        body.textContent = '';
        if (err.status === 429) {
          var wait = err.data && err.data.retry_after;
          body.appendChild(el('div', 'quota-note',
            '厂商限流中，' + Math.ceil((wait || 0) / 60) + ' 分钟后可再试。'));
          /* 退避是持久化的：手动刷冲不掉它（打的是同一个接口、同一把 key）。 */
        } else {
          body.appendChild(el('div', 'quota-note', '刷新失败：' + err.message));
        }
      })
      .then(function () {
        btn.disabled = false;
        btn.textContent = '刷新';
      });
  }

  /* ------------------------------------------------------------------ */
  /* 左栏                                                                 */
  /* ------------------------------------------------------------------ */

  function renderSidebar() {
    var host = sidebarHost();
    host.textContent = '';

    var head = el('div', 'side-head');
    head.appendChild(el('span', null, 'PROVIDER'));
    var add = el('button', 'btn ghost', '+ 新建');
    add.type = 'button';
    add.addEventListener('click', function () {
      state.creating = true;
      renderMain();
      renderSidebar();
    });
    head.appendChild(add);
    host.appendChild(head);

    var list = el('ul', 'side-list');
    state.list.forEach(function (p) {
      var li = document.createElement('li');
      var btn = el('button', 'side-item');
      btn.type = 'button';
      btn.setAttribute('aria-selected', p.name === state.current ? 'true' : 'false');
      btn.appendChild(el('span', null, p.name));
      if (p.note) { btn.appendChild(el('span', 'sub', p.note)); }
      if (p.error) { btn.appendChild(el('span', 'warn', p.error)); }
      else if (p.validation_errors && p.validation_errors.length) {
        btn.appendChild(el('span', 'warn', '配置有问题'));
      } else if (!p.has_credential) {
        btn.appendChild(el('span', 'warn', '没有凭证'));
      }
      btn.addEventListener('click', function () {
        state.creating = false;
        selectProvider(p.name);
      });
      li.appendChild(btn);
      list.appendChild(li);
    });
    host.appendChild(list);
  }

  /* ------------------------------------------------------------------ */
  /* 右栏                                                                 */
  /* ------------------------------------------------------------------ */

  var editor = null;
  var statusHost = null;
  var quotaHostEl = null;

  function quotaHost() { return quotaHostEl; }

  /* renderSeq 是渲染轮次序号。
   *
   * **修跨渲染竞态**：模块级的 `editor` / `statusHost` / `quotaHostEl` 会被下一轮
   * renderMain 重新赋值 → **迟到的旧响应会写进新 provider 的界面**（旧配置灌进
   * 新编辑器、`state.etag` 被覆盖 → 用户看着新 provider 的内容，点保存存成旧的）。
   *
   * 每轮 renderMain 自增序号；异步回调里比对，不是本轮的直接丢弃。
   */
  var renderSeq = 0;

  function setStatus(text, kind, seq) {
    // 过期轮次的状态不写进当前的标题行
    if (seq !== undefined && seq !== renderSeq) { return; }
    if (!statusHost) { return; }
    statusHost.className = 'hint' + (kind === 'err' ? ' err' : (kind === 'ok' ? ' ok' : ''));
    statusHost.textContent = text || '';
  }

  function renderMain() {
    var host = mainHost();
    host.textContent = '';
    editor = null;
    var seq = ++renderSeq;

    if (state.creating) {
      renderCreateForm(host);
      return;
    }

    var info = findProvider(state.current);
    if (!info) {
      host.appendChild(el('div', 'hint', '左侧选一个 provider。'));
      return;
    }

    var head = el('div', 'page-head');
    head.appendChild(el('h2', null, info.name));
    head.appendChild(el('span', 'sub', '~/.cc-launcher/providers/' + info.name + '.json'));
    host.appendChild(head);

    quotaHostEl = el('div', 'quota-card');
    host.appendChild(quotaHostEl);
    renderQuota(quotaHostEl, state.quota);

    /* 编辑区：标题行（状态 + 按钮）+ 编辑器本体。
     *
     * **.editor 直接挂 .editor-zone 下**（中间不隔普通 div）：隔了的话 .editor 的
     * 父不是 flex 容器 → `.editor { flex: 1 }` 完全无效 → 编辑器塌成 2px。
     */
    var zone = el('div', 'editor-zone');
    host.appendChild(zone);

    var editorHead = el('div', 'editor-head');
    editorHead.appendChild(el('h3', null, 'JSON'));
    statusHost = el('span', 'hint');
    editorHead.appendChild(statusHost);
    editorHead.appendChild(el('span', 'spacer'));

    var saveBtn = el('button', 'btn', '保存'); /* 保存排最前 */
    saveBtn.type = 'button';
    saveBtn.addEventListener('click', doSave);
    editorHead.appendChild(saveBtn);

    var copyBtn = el('button', 'btn ghost', '复制');
    copyBtn.type = 'button';
    copyBtn.addEventListener('click', doCopy);
    editorHead.appendChild(copyBtn);

    var delBtn = el('button', 'btn ghost', '删除');
    delBtn.type = 'button';
    delBtn.addEventListener('click', doDelete);
    editorHead.appendChild(delBtn);

    zone.appendChild(editorHead);

    editor = window.CCL.createEditor(zone, {
      value: state.content || '',
      mode: 'json',
      onChange: function (value) {
        state.content = value;
        var err = editor.validate();
        if (err) {
          setStatus('JSON 解析失败：' + err, 'err');
          saveBtn.disabled = true;
        } else {
          setStatus('');
          saveBtn.disabled = false;
        }
      }
    });

    /* 载入内容 */
    window.CCL.api.get('/api/providers/' + encodeURIComponent(state.current))
      .then(function (data) {
        if (seq !== renderSeq) { return; } /* 已经切到别的 provider 了 */
        state.etag = data.etag;
        state.content = data.content;
        editor.setValue(data.content);
        var err = editor.validate();
        if (err) { setStatus('JSON 解析失败：' + err, 'err'); saveBtn.disabled = true; }
      })
      .catch(function (err) { setStatus('读取失败：' + err.message, 'err', seq); });

    /* 载入额度（零网络：读本地额度数据文件） */
    window.CCL.api.get('/api/providers/' + encodeURIComponent(state.current) + '/quota')
      .then(function (q) {
        if (seq !== renderSeq) { return; }
        state.quota = q;
        if (quotaHostEl) { renderQuota(quotaHostEl, q); }
      })
      .catch(function () { /* 额度不可用不影响编辑 */ });
  }

  function findProvider(name) {
    for (var i = 0; i < state.list.length; i++) {
      if (state.list[i].name === name) { return state.list[i]; }
    }
    return null;
  }

  function selectProvider(name) {
    state.current = name;
    state.content = '';
    state.quota = null;
    renderSidebar();
    renderMain();
  }

  /* ------------------------------------------------------------------ */
  /* 动作                                                                 */
  /* ------------------------------------------------------------------ */

  function doSave() {
    if (!editor) { return; }
    var seq = renderSeq; /* 期间切走了就别写当前的 state / 提示 */
    var err = editor.validate();
    if (err) { setStatus('JSON 解析失败，改动没有保存', 'err'); return; }

    window.CCL.api.send('PUT', '/api/providers/' + encodeURIComponent(state.current),
      { content: editor.getValue() }, state.etag)
      .then(function (data) {
        if (seq !== renderSeq) { return; }
        state.etag = data.etag;
        state.content = data.content;
        setStatus('已保存。下次启动 ccl 生效（已在跑的会话不会热更新）。', 'ok');
        loadList();
      })
      .catch(function (e) {
        if (seq !== renderSeq) { return; }
        if (e.status === 409) {
          setStatus('文件已被其他程序修改（' + (e.data && e.data.error ? e.data.error : '冲突') + '）。' +
            '请重新加载后再保存——改动还在编辑框里，没丢。', 'err');
        } else if (e.status === 400 && e.data && e.data.errors) {
          setStatus('配置校验失败：' + e.data.errors.join('；'), 'err');
        } else {
          setStatus('保存失败：' + e.message, 'err');
        }
      });
  }

  function doCopy() {
    var name = window.prompt('复制成新 provider（名字）', state.current + '-copy');
    if (!name) { return; }
    var seq = renderSeq;
    window.CCL.api.send('POST', '/api/providers/' + encodeURIComponent(state.current) + '/copy', { name: name })
      .then(function () {
        if (seq !== renderSeq) { return; }
        loadList().then(function () { selectProvider(name); });
      })
      .catch(function (e) { if (seq === renderSeq) { setStatus('复制失败：' + e.message, 'err'); } });
  }

  function doDelete() {
    if (!window.confirm('删除 provider「' + state.current + '」？\n' +
      '配置文件会被删掉（额度数据里的条目也会清掉）。\n' +
      '删错了可以从 ~/.cc-launcher/backups 里找回。')) { return; }
    var seq = renderSeq;
    window.CCL.api.send('DELETE', '/api/providers/' + encodeURIComponent(state.current), {})
      .then(function () {
        if (seq !== renderSeq) { return; }
        state.current = '';
        state.content = '';
        state.quota = null;
        loadList();
      })
      .catch(function (e) { if (seq === renderSeq) { setStatus('删除失败：' + e.message, 'err'); } });
  }

  function renderCreateForm(host) {
    var head = el('div', 'page-head');
    head.appendChild(el('h2', null, '新建 provider'));
    host.appendChild(head);

    var form = el('div', 'card');
    var nameLabel = el('label');
    nameLabel.appendChild(document.createTextNode('名称'));
    var nameInput = document.createElement('input');
    nameInput.type = 'text';
    nameInput.placeholder = '如 my-deepseek';
    nameInput.autocomplete = 'off';
    nameLabel.appendChild(nameInput);
    form.appendChild(nameLabel);

    var tplLabel = el('label');
    tplLabel.appendChild(document.createTextNode('模板'));
    var tplSelect = document.createElement('select');
    ['deepseek', 'minimax', 'glm', 'aliyun-coding', 'aliyun-pay', 'aliyun-token', 'anthropic']
      .forEach(function (name) {
        var opt = document.createElement('option');
        opt.value = name;
        opt.textContent = name;
        tplSelect.appendChild(opt);
      });
    tplLabel.appendChild(tplSelect);
    form.appendChild(tplLabel);

    form.appendChild(el('div', 'hint',
      '用模板起一个 JSON 骨架，然后就能在右边直接改（凭证、base_url、透传键都在那儿）。'));

    var foot = el('div', 'editor-foot');
    statusHost = el('span');
    foot.appendChild(statusHost);
    foot.appendChild(el('span', 'spacer'));
    var cancel = el('button', 'btn ghost', '取消');
    cancel.type = 'button';
    cancel.addEventListener('click', function () {
      state.creating = false;
      renderSidebar();
      renderMain();
    });
    foot.appendChild(cancel);
    var create = el('button', 'btn', '创建');
    create.type = 'button';
    create.addEventListener('click', function () {
      var name = nameInput.value.trim();
      if (!name) { setStatus('名字不能为空', 'err'); return; }
      window.CCL.api.send('POST', '/api/providers', { name: name, template: tplSelect.value })
        .then(function () {
          state.creating = false;
          loadList().then(function () { selectProvider(name); });
        })
        .catch(function (e) { setStatus('创建失败：' + e.message, 'err'); });
    });
    foot.appendChild(create);
    form.appendChild(foot);

    host.appendChild(form);
    nameInput.focus();
  }

  /* loadList 拉 provider 列表。 */
  function loadList() {
    return window.CCL.api.get('/api/providers')
      .then(function (items) {
        state.list = items || [];
        renderSidebar();
        if (!state.current && state.list.length > 0) {
          selectProvider(state.list[0].name);
        } else {
          renderMain();
        }
      })
      .catch(function (e) {
        var host = mainHost();
        host.textContent = '';
        host.appendChild(el('div', 'hint err', '读取 provider 列表失败：' + e.message));
      });
  }

  window.CCL = window.CCL || {};
  window.CCL.views = window.CCL.views || {};

  /* 额度渲染的**纯逻辑**导出 —— 给 node 单测断言（internal/web/jstest/）。
   *
   * 这些函数不碰 DOM，可以在 vm 沙箱里加载本文件后直接调用。
   *
   * 为什么要测：口径对齐状态栏是**看不见**的契约 —— 网页显示 "6%"、状态栏
   * 显示 "6% 6/100 %" 这种漂移，界面上看不出问题，只有断言能守住。
   */
  window.CCL.quotaFormat = {
    dimKind: dimKind,
    dimPctText: dimPctText,
    dimText: dimText,
    barRatio: barRatio,
    pctColor: pctColor,
    fmtAmount: fmtAmount,
    unitSuffix: unitSuffix
  };

  window.CCL.views.providers = {
    sidebar: true,
    activate: function () { loadList(); },
    deactivate: function () { /* 没有常驻定时器 */ }
  };
})();
