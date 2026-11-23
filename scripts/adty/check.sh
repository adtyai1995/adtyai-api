#!/usr/bin/env bash
# adtyai-api 本地检查（实施方案任务 0.3），每个任务结束都跑一遍。
# 只在 WSL 的测试副本（~/adtyai/mirror/main）里跑；仓库在 /mnt 下（Windows 的盘）就直接退出。
# 用法：bash scripts/adty/check.sh             后端、前端都跑
#       bash scripts/adty/check.sh --backend   只跑根模块、relaykit、tokenkit 的 go vet、go build
#       bash scripts/adty/check.sh --frontend  只跑 web 的 install、typecheck、build、lint、i18n、copyright、docs
# 任何一步失败就停。上游本来就有的失败记在 docs/adty/baseline.md；lint 和 copyright:check 由
# scripts/adty/ratchet.mjs 和同目录的两份清单比较，只拦清单以外的新问题。
set -euo pipefail

usage() {
  echo "用法：bash scripts/adty/check.sh [--backend | --frontend]" >&2
  exit 2
}

run_backend=1
run_frontend=1
if [ "$#" -gt 1 ]; then
  usage
fi
case "${1:-}" in
  "") ;;
  --backend) run_frontend=0 ;;
  --frontend) run_backend=0 ;;
  *) usage ;;
esac

step() {
  printf '\n==> %s\n' "$*"
}

# 脚本在 scripts/adty/ 下，仓库根目录在上两级
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

# v2.5：仓库在 Windows，检查只在 WSL 的测试副本里跑。在 /mnt 下跑会把 web/dist 占位和
# node_modules 写进 Windows 仓库，所以在任何会写文件的步骤之前检查；按实际路径判断，经符号链接进来的也算
repo_root=$(pwd -P)
case "$repo_root" in
  /mnt | /mnt/*)
    echo "check.sh: 仓库目录 $repo_root 在 /mnt 下（Windows 的盘），不能在这里跑，什么也没做。" >&2
    echo "check.sh: 只在 WSL 的测试副本 ~/adtyai/mirror/main 里跑：把命令写进任务脚本，经 wrun.sh 运行（约定 H）。" >&2
    exit 1
    ;;
esac

# 约定 H：只用 Linux 版工具。WSL 的 PATH 末尾带着 /mnt/c 下的 Windows 版 node、npm、git、gh。
step "工具位置和版本"
for tool in git go bun node npm gh rg; do
  tool_path=$(command -v "$tool") || {
    echo "check.sh: 找不到 $tool" >&2
    exit 1
  }
  for p in "$tool_path" "$(readlink -f "$tool_path")"; do
    case "$p" in
      /mnt/c/*)
        echo "check.sh: $tool 在 /mnt/c 下（$p），是 Windows 版；装 Linux 版并让它排在 PATH 前面" >&2
        exit 1
        ;;
    esac
  done
  if [ "$tool" = go ]; then
    version=$(go version)
  else
    version=$("$tool" --version)
  fi
  # 多行的版本信息只取第一行
  printf '%-5s %s  (%s)\n' "$tool" "${version%%$'\n'*}" "$tool_path"
done

# main.go 用 go:embed 嵌入 web/dist 和 web/dist/index.html，web/dist 在 .gitignore 里。
# 没有就建一个空占位（同上游 ci.yml）；已有的（比如 bun run build 的产物）不动。
if [ ! -e web/dist/index.html ]; then
  mkdir -p web/dist
  : > web/dist/index.html
  echo "已建空占位 web/dist/index.html"
fi

if [ "$run_backend" -eq 1 ]; then
  export GOWORK=off

  step "根模块：go vet ./..."
  go vet ./...
  step "根模块：go build ./..."
  go build ./...

  for module in relaykit tokenkit; do
    step "$module：go vet ./..."
    (cd "$module" && go vet ./...)
    step "$module：go build ./..."
    (cd "$module" && go build ./...)
  done
fi

if [ "$run_frontend" -eq 1 ]; then
  step "web：bun install --frozen-lockfile"
  (cd web && bun install --frozen-lockfile)
  step "plugins：lint 和格式检查"
  make check-plugins
  step "web：bun run typecheck"
  (cd web && bun run typecheck)
  step "web build"
  (cd web && bun run build)
  # 上游原样就有 lint error 和不合要求的文件头（docs/adty/baseline.md）。这两步由 ratchet.mjs 跑，
  # 只拦 scripts/adty/baseline-lint.txt、baseline-copyright.txt 以外的新问题
  step "web：bun run lint（和 scripts/adty/baseline-lint.txt 比较）"
  node scripts/adty/ratchet.mjs lint
  step "web：我们的语言键在简体中文和英文中齐全（scripts/adty/i18n-check.mjs）"
  (cd web && node scripts/adty/i18n-check.mjs)
  step "web：bun run copyright:check（和 scripts/adty/baseline-copyright.txt 比较）"
  node scripts/adty/ratchet.mjs copyright
  step "docs：文档与代码一致性检查（make check-docs）"
  make check-docs
fi

step "检查全部通过"
