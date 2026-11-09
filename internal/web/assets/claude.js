/* ccl 控制台 —— Claude 视图
 *
 * 只管**用户级**的两个文件：
 *   - ~/.claude/settings.json
 *   - ~/.claude/CLAUDE.md
 *
 * 刻意不管 ~/.claude.json（Claude Code 高频写它，乐观锁会一直撞；它是状态
 * 不是配置；里面还有 OAuth 账号信息与主机指纹）与任何项目级文件。
 */
(function () {
  'use strict';

  var state = {
    files: [],
    current: '',
    etag: '',
    content: ''
  };

  var editor = null;
  var statusHost = null;

  function el(tag, cls, text) {
    var node = document.createElement(tag);
    if (cls) { node.className = cls; }
    if (text !== undefined && text !== null) { node.textContent = text; }
    return node;
  }

  function sidebarHost() { return document.getElementById('sidebar'); }
  function mainHost() { return document.getElementById('view-claude'); }

  function setStatus(text, kind) {
    if (!statusHost) { return; }
    statusHost.textContent = '';
    if (!text) { return; }
    statusHost.appendChild(el('span', kind === 'err' ? 'hint err' : (kind === 'ok' ? 'hint ok' : 'hint'), text));
  }

  function fileLabel(id) {
    return id === 'settings' ? 'settings.json' : 'CLAUDE.md';
  }

  function isJSON(id) { return id === 'settings'; }

  function renderSidebar() {
    var host = sidebarHost();
    host.textContent = '';

    var head = el('div', 'side-head');
    head.appendChild(el('span', null, 'CLAUDE'));
    host.appendChild(head);

    var list = el('ul', 'side-list');
    state.files.forEach(function (f) {
      var li = document.createElement('li');
      var btn = el('button', 'side-item');
      btn.type = 'button';
      btn.setAttribute('aria-selected', f.id === state.current ? 'true' : 'false');
      btn.appendChild(el('span', null, fileLabel(f.id)));
      var sub = f.exists ? (f.path) : (f.path + '（还没有）');
      btn.appendChild(el('span', 'sub', sub));
      btn.addEventListener('click', function () { selectFile(f.id); });
      li.appendChild(btn);
      list.appendChild(li);
    });
    host.appendChild(list);
  }

  function renderMain() {
    var host = mainHost();
    host.textContent = '';
    editor = null;

    var file = null;
    for (var i = 0; i < state.files.length; i++) {
      if (state.files[i].id === state.current) { file = state.files[i]; }
    }
    if (!file) {
      host.appendChild(el('div', 'hint', '左侧选一个文件。'));
      return;
    }

    var head = el('div', 'page-head');
    head.appendChild(el('h2', null, fileLabel(file.id)));
    head.appendChild(el('span', 'sub', file.path));
    host.appendChild(head);

    if (!file.exists) {
      host.appendChild(el('div', 'hint', '文件还不存在。写点内容保存就会创建它。'));
    }

    var editorHost = el('div');
    host.appendChild(editorHost);

    var foot = el('div', 'editor-foot');
    statusHost = el('span');
    foot.appendChild(statusHost);
    foot.appendChild(el('span', 'spacer'));

    var saveBtn = el('button', 'btn', '保存');
    saveBtn.type = 'button';
    saveBtn.addEventListener('click', doSave);
    foot.appendChild(saveBtn);

    var revertBtn = el('button', 'btn ghost', '放弃修改');
    revertBtn.type = 'button';
    revertBtn.addEventListener('click', function () { selectFile(state.current); });
    foot.appendChild(revertBtn);

    if (isJSON(file.id)) {
      var fmt = el('button', 'btn ghost', '格式化');
      fmt.type = 'button';
      fmt.addEventListener('click', function () {
        try {
          var v = JSON.parse(editor.getValue());
          editor.setValue(JSON.stringify(v, null, 2) + '\n');
          setStatus('已格式化（还没保存）', 'ok');
        } catch (e) {
          setStatus('JSON 解析失败，不能格式化：' + e.message, 'err');
        }
      });
      foot.appendChild(fmt);
    }

    host.appendChild(foot);

    editor = window.CCL.createEditor(editorHost, {
      value: state.content,
      mode: isJSON(file.id) ? 'json' : 'text',
      onChange: function (value) {
        state.content = value;
        if (isJSON(file.id)) {
          var err = editor.validate();
          if (err) {
            setStatus('JSON 解析失败：' + err, 'err');
            saveBtn.disabled = true;
          } else {
            setStatus('');
            saveBtn.disabled = false;
          }
        }
      }
    });

    window.CCL.api.get('/api/claude/file/' + encodeURIComponent(file.id))
      .then(function (data) {
        state.etag = data.etag;
        state.content = data.content;
        if (editor) { editor.setValue(data.content); }
      })
      .catch(function (e) { setStatus('读取失败：' + e.message, 'err'); });
  }

  function selectFile(id) {
    state.current = id;
    state.content = '';
    renderSidebar();
    renderMain();
  }

  function doSave() {
    if (!editor) { return; }
    if (isJSON(state.current)) {
      var err = editor.validate();
      if (err) { setStatus('JSON 解析失败，改动没有保存', 'err'); return; }
    }

    window.CCL.api.send('PUT', '/api/claude/file/' + encodeURIComponent(state.current),
      { content: editor.getValue() }, state.etag)
      .then(function (data) {
        state.etag = data.etag;
        setStatus('已保存。已在运行的 Claude Code 不会自动生效（它只在启动时读配置）。', 'ok');
        loadFiles();
      })
      .catch(function (e) {
        if (e.status === 409) {
          setStatus('文件已被其他程序修改。请重新加载后再保存——改动还在编辑框里，没丢。', 'err');
        } else if (e.status === 400 && e.data && e.data.errors) {
          setStatus('配置校验失败：' + e.data.errors.join('；'), 'err');
        } else {
          setStatus('保存失败：' + e.message, 'err');
        }
      });
  }

  function loadFiles() {
    return window.CCL.api.get('/api/claude/files')
      .then(function (items) {
        state.files = items || [];
        renderSidebar();
        if (!state.current && state.files.length > 0) {
          selectFile(state.files[0].id);
        } else {
          renderMain();
        }
      })
      .catch(function (e) {
        var host = mainHost();
        host.textContent = '';
        host.appendChild(el('div', 'hint err', '读取文件列表失败：' + e.message));
      });
  }

  window.CCL = window.CCL || {};
  window.CCL.views = window.CCL.views || {};
  window.CCL.views.claude = {
    sidebar: true,
    activate: function () { loadFiles(); },
    deactivate: function () { /* 没有常驻定时器 */ }
  };
})();
