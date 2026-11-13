#!/usr/bin/env node
// bin/patch-statusline-git.js — 把状态栏脚本的"取分支"从子进程改成零进程
//
// 用途：Windows 上起一个进程要付 Defender 对未签名 exe 的实时深扫——实测
// （2026-11-13，Windows 11 24H2）：
//
//   node 启动（空脚本）          88~123ms
//   git branch 子进程          ~62ms     ← 这个可以省掉
//   cc-launcher.exe 启动      ~259ms
//   额度通道（HTTP，零进程）    5~6ms
//
// 状态栏的总预算是 250ms（Claude Code 的去抖窗口）。`git branch --show-current`
// 那一次子进程纯属浪费——分支名就在 `.git/HEAD` 里，直接读文件是 0.24ms。
//
// 用法:
//   node bin/patch-statusline-git.js                      # 改 ~/.claude/statusline.js
//   node bin/patch-statusline-git.js "C:\path\statusline.js"
//
// **幂等**：已经是 v2 就什么都不做；v1（早期的零进程版，没有向上找仓库根）
// 会自动升级到 v2。
// **安全**：先备份（.bak-<时间戳>），改完做语法自检，失败自动回滚。

'use strict';

const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const target = process.argv[2] ||
  path.join(os.homedir(), '.claude', 'statusline.js');

if (!fs.existsSync(target)) {
  console.error(`找不到状态栏脚本: ${target}`);
  process.exit(2);
}

let src = fs.readFileSync(target, 'utf8');

// v2 的赋值三行（**只有"旧版 → v2"这条路径需要**——v1 文件里已经有了）
const V2_ASSIGN = `const gitCwd = data.workspace?.current_dir ?? data.cwd;
const branchName = readGitBranch(gitCwd);
const gitBranch = branchName ? \`\${DIM}git(\${branchName})\${RESET}\` : '';

`;

// v2 的实现（注释 + 两个函数）
const V2 = `// readGitBranch 零进程读当前分支（直接读 .git/HEAD）。
//
// **GIT_BRANCH_V2** —— **向上找仓库根**：cwd 是**子目录**时也能显示分支
// （与 \`git branch --show-current\` 一致——git 从 cwd 逐级上溯找 .git）。
// v1 只看 cwd 那一层 → 一进子目录分支就没了。
//
// **为什么不再 execSync('git branch --show-current')**：Windows 上起一个进程
// 要付 **Defender 对未签名 exe 的实时深扫**——实测（2026-11-13，Windows 11 24H2）：
//
//   node 启动（空脚本）          88~123ms
//   git branch 子进程          ~62ms     ← 纯浪费
//   cc-launcher.exe 启动      ~259ms
//   额度通道（HTTP，零进程）    5~6ms
//
// 状态栏的总预算是 250ms（Claude Code 的去抖窗口）。零进程读 .git/HEAD 是
// 纯文件读（0.24ms）。
//
// **行为保持一致**：detached HEAD 时不显示分支（与 \`git branch --show-current\`
// 一致，它在 detached 时输出空）。
function readGitBranch(cwd) {
  if (!cwd) return '';
  let dir = path.resolve(cwd);
  try {
    if (!fs.statSync(dir).isDirectory()) dir = path.dirname(dir);
  } catch {
    return ''; // 目录不存在
  }
  // 逐级上溯找 .git（git 的规则：当前层没有就往上找，撞到 .git 就停）
  for (;;) {
    const dotgit = path.join(dir, '.git');
    let st = null;
    try {
      st = fs.statSync(dotgit);
    } catch {
      st = null; // 这一层没有 .git → 继续往上
    }
    if (st) {
      // 找到仓库根了 —— 不再往上找（与 git 一致）
      if (st.isDirectory()) return readHeadBranch(path.join(dotgit, 'HEAD'));
      if (st.isFile()) {
        // worktree / 子模块：.git 是文件，内容是 "gitdir: <路径>"
        let gitDir = '';
        try {
          const m = fs.readFileSync(dotgit, 'utf8').match(/gitdir:\\s*(.+)/);
          if (!m) return '';
          gitDir = m[1].trim();
          if (!path.isAbsolute(gitDir)) gitDir = path.resolve(dir, gitDir);
        } catch {
          return '';
        }
        return readHeadBranch(path.join(gitDir, 'HEAD'));
      }
      return ''; // .git 存在但既不是目录也不是文件 → 不正常
    }
    const parent = path.dirname(dir);
    if (parent === dir) return ''; // 到文件系统根了
    dir = parent;
  }
}

// readHeadBranch 从 HEAD 文件里取分支名。
//
// HEAD 的两种形态：
//   ref: refs/heads/main  → 在某个分支上 → "main"
//   <40 位 sha>            → detached HEAD → 与 \`git branch --show-current\`
//                            一致（输出空）→ 不显示分支
function readHeadBranch(headPath) {
  try {
    const head = fs.readFileSync(headPath, 'utf8').trim();
    const m = head.match(/^ref:\\s*refs\\/heads\\/(.+)$/);
    return m ? m[1] : '';
  } catch {
    return '';
  }
}
`;

if (src.includes('GIT_BRANCH_V2')) {
  console.log(`✓ 已经是 v2（零进程 + 向上找仓库根），无需改动: ${target}`);
  process.exit(0);
}

let patched;
let what;

if (src.includes('readGitBranch')) {
  // v1 → v2：替换"注释 + 函数"整段（优先），否则只替换函数体
  const reWithComment = /\/\/ readGitBranch[\s\S]*?\nfunction readGitBranch\(cwd\) \{[\s\S]*?\n\}\n/;
  const reFnOnly = /function readGitBranch\(cwd\) \{[\s\S]*?\n\}\n/;
  if (reWithComment.test(src)) {
    patched = src.replace(reWithComment, V2);
    what = 'v1 → v2（补上向上找仓库根）';
  } else if (reFnOnly.test(src)) {
    patched = src.replace(reFnOnly, V2);
    what = 'v1 → v2（只替换函数体）';
  } else {
    console.error('认出了 readGitBranch，但没匹配到它的函数体——脚本结构与已知形态不同。' +
      '没有改动任何文件。');
    process.exit(2);
  }
} else {
  // 旧版（git 子进程）→ v2：替换整块
  const re = /const gitCwd = [^;]*;\nlet gitBranch = '';\nif \(gitCwd\) \{[\s\S]*?\n\}\n/;
  if (!re.test(src)) {
    console.error('没找到预期的 git 分支代码块——你的脚本结构与已知形态不同，' +
      '请手工替换（见 docs/provider-quota.md 的说明）。没有改动任何文件。');
    process.exit(2);
  }
  patched = src.replace(re, V2_ASSIGN + V2);
  what = 'git 子进程 → v2（零进程 + 向上找仓库根）';
}

const backup = `${target}.bak-${new Date().toISOString().replace(/[:.]/g, '-')}`;
fs.writeFileSync(backup, src, 'utf8');
fs.writeFileSync(target, patched, 'utf8');

// 语法自检：跑 node --check，失败就回滚
const check = spawnSync(process.execPath, ['--check', target], { encoding: 'utf8' });
if (check.status !== 0) {
  fs.writeFileSync(target, src, 'utf8');
  console.error('✗ 改完语法检查失败，已回滚（原文件未变）。node --check 的输出：\n' + check.stderr);
  process.exit(1);
}

console.log(`✓ ${what}: ${target}`);
console.log(`  备份: ${backup}`);
console.log('  验证: node bin/statusline-probe.js --runs 30 --load 8 --cwd <项目目录>');
