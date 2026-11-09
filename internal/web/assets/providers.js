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

  var BAR_LEN = 10;
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

  function dimRow(d) {
    var label = d.label || d.id || '';
    var row = el('div', 'quota-dim');

    var lab = el('span', 'label', label);
    row.appendChild(lab);

    if (d.kind === 'balance') {
      row.appendChild(el('span', 'val', fmtAmount(d.remaining) + unitSuffix(label, d.unit)));
      return row;
    }

    var pct = d.used_percentage;
    if (typeof pct !== 'number') {
      row.appendChild(el('span', 'val', fmtAmount(d.remaining) + unitSuffix(label, d.unit)));
      return row;
    }

    var filled = Math.round((pct / 100) * BAR_LEN);
    if (filled > BAR_LEN) { filled = BAR_LEN; }
    if (filled < 0) { filled = 0; }
    var bar = el('span', 'bar ' + pctColor(pct), '█'.repeat(filled) + '░'.repeat(BAR_LEN - filled));
    row.appendChild(bar);

    /* 令牌额度的已用百分比通常极小（0.025% 这种）——四舍五成 0% 会让人
     * 以为一点没用；小于 1% 时保留两位小数（与状态栏同规则）。 */
    var pctText = (pct > 0 && pct < 1) ? pct.toFixed(2) : String(Math.round(pct));
    var text = pctText + '%';
    if (typeof d.used === 'number' && typeof d.limit === 'number') {
      text += ' ' + fmtAmount(d.used) + '/' + fmtAmount(d.limit) + unitSuffix(label, d.unit);
    }
    row.appendChild(el('span', 'val', text));

    if (typeof d.resets_at === 'number') {
      row.appendChild(el('span', 'reset', fmtReset(d.resets_at, d.duration_seconds)));
    }
    return row;
  }

  function renderQuota(host, quota) {
    host.textContent = '';

    var head = el('div', 'quota-head');
    head.appendChild(el('h3', null, '额度'));
    var refreshBtn = el('button', 'btn ghost', '刷新');
    refreshBtn.type = 'button';
    refreshBtn.title = '联网查一次（按需，不受"只刷活跃 provider"的限制）';
    refreshBtn.addEventListener('click', function () { doRefreshQuota(refreshBtn); });
    head.appendChild(refreshBtn);
    host.appendChild(head);

    if (!quota) {
      host.appendChild(el('div', 'quota-note', '加载中…'));
      return;
    }

    if (!quota.has_quota) {
      host.appendChild(el('div', 'quota-note', '这个 provider 没有额度接口。'));
      return;
    }

    if (!quota.found) {
      /* **"没采到" ≠ "无限额"**：serve 只刷新最近有活动的 provider。
       * 画成空条会让人放心地把量用爆。 */
      var miss = el('div', 'quota-note');
      miss.appendChild(el('div', null, '近期无活动，未采集额度。'));
      miss.appendChild(el('div', null, '（这不是"无限额"——点「刷新」按需查一次。）'));
      host.appendChild(miss);
      return;
    }

    var dims = quota.dimensions || [];
    if (dims.length === 0) {
      host.appendChild(el('div', 'quota-note', '没有额度维度。'));
    }
    dims.forEach(function (d) { host.appendChild(dimRow(d)); });

    var note = el('div', 'quota-note');
    note.appendChild(el('span', null, ageText(quota.fetched_at) + '采集'));
    if (quota.stale) {
      note.appendChild(el('span', null, ' · 数据已过期'));
    }
    host.appendChild(note);
  }

  function doRefreshQuota(btn) {
    if (!state.current) { return; }
    btn.disabled = true;
    btn.textContent = '刷新中…';
    window.CCL.api.send('POST', '/api/providers/' + encodeURIComponent(state.current) + '/quota/refresh', {})
      .then(function (q) {
        state.quota = q;
        renderQuota(quotaHost(), q);
      })
      .catch(function (err) {
        var host = quotaHost();
        if (err.status === 429) {
          var wait = err.data && err.data.retry_after;
          host.textContent = '';
          host.appendChild(el('div', 'quota-note',
            '厂商限流中，' + Math.ceil((wait || 0) / 60) + ' 分钟后可再试。'));
          /* 退避是持久化的：手动刷冲不掉它（打的是同一个接口、同一把 key）。 */
        } else {
          host.textContent = '';
          host.appendChild(el('div', 'quota-note', '刷新失败：' + err.message));
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

  function setStatus(text, kind) {
    if (!statusHost) { return; }
    statusHost.textContent = '';
    if (!text) { return; }
    statusHost.appendChild(el('span', kind === 'err' ? 'hint err' : (kind === 'ok' ? 'hint ok' : 'hint'), text));
  }

  function renderMain() {
    var host = mainHost();
    host.textContent = '';
    editor = null;

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

    var editorHost = el('div');
    host.appendChild(editorHost);

    var foot = el('div', 'editor-foot');
    statusHost = el('span');
    foot.appendChild(statusHost);
    foot.appendChild(el('span', 'spacer'));

    var copyBtn = el('button', 'btn ghost', '复制');
    copyBtn.type = 'button';
    copyBtn.addEventListener('click', doCopy);
    foot.appendChild(copyBtn);

    var delBtn = el('button', 'btn ghost', '删除');
    delBtn.type = 'button';
    delBtn.addEventListener('click', doDelete);
    foot.appendChild(delBtn);

    var saveBtn = el('button', 'btn', '保存');
    saveBtn.type = 'button';
    saveBtn.addEventListener('click', doSave);
    foot.appendChild(saveBtn);

    host.appendChild(foot);

    editor = window.CCL.createEditor(editorHost, {
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
        state.etag = data.etag;
        state.content = data.content;
        if (editor) {
          editor.setValue(data.content);
          var err = editor.validate();
          if (err) { setStatus('JSON 解析失败：' + err, 'err'); saveBtn.disabled = true; }
        }
      })
      .catch(function (err) { setStatus('读取失败：' + err.message, 'err'); });

    /* 载入额度（零网络：读本地额度数据文件） */
    window.CCL.api.get('/api/providers/' + encodeURIComponent(state.current) + '/quota')
      .then(function (q) {
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
    var err = editor.validate();
    if (err) { setStatus('JSON 解析失败，改动没有保存', 'err'); return; }

    window.CCL.api.send('PUT', '/api/providers/' + encodeURIComponent(state.current),
      { content: editor.getValue() }, state.etag)
      .then(function (data) {
        state.etag = data.etag;
        state.content = data.content;
        setStatus('已保存。下次启动 ccl 生效（已在跑的会话不会热更新）。', 'ok');
        loadList();
      })
      .catch(function (e) {
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
    window.CCL.api.send('POST', '/api/providers/' + encodeURIComponent(state.current) + '/copy', { name: name })
      .then(function () {
        loadList().then(function () { selectProvider(name); });
      })
      .catch(function (e) { setStatus('复制失败：' + e.message, 'err'); });
  }

  function doDelete() {
    if (!window.confirm('删除 provider「' + state.current + '」？\n' +
      '配置文件会被删掉（额度数据里的条目也会清掉）。\n' +
      '删错了可以从 ~/.cc-launcher/backups 里找回。')) { return; }
    window.CCL.api.send('DELETE', '/api/providers/' + encodeURIComponent(state.current), {})
      .then(function () {
        state.current = '';
        state.content = '';
        state.quota = null;
        loadList();
      })
      .catch(function (e) { setStatus('删除失败：' + e.message, 'err'); });
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
  window.CCL.views.providers = {
    sidebar: true,
    activate: function () { loadList(); },
    deactivate: function () { /* 没有常驻定时器 */ }
  };
})();
