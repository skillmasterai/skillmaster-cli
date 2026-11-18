#!/bin/sh
# Installs the SkillMaster CLI on macOS and Linux.
#
# The artifact names, the checksum file and this script's behaviour are specified in
# skillmaster-cli/RELEASE.md §7; that document is the authority, this file is the implementation.
#
# **Picking the package is deterministic work, so it is done here rather than by a person or a
# model.** An architecture this script does not recognise is a refusal, not a guess: it used to fall
# back to amd64 "because Apple Silicon runs x86_64 under Rosetta 2", but macOS only ever reports
# `arm64` or `x86_64`, so that branch was unreachable — and everything that does reach it (i686,
# armv7l, riscv64, s390x) cannot execute an amd64 binary at all. Installing one there replaced a
# working command with a file that cannot exec.
set -eu

BASE_URL="${SKILLMASTER_DL_URL:-https://dl.skillmasterai.com}"
# An override exists for testing and for a mirror; the default is the only address users see.

say() { printf '%s\n' "$*"; }
die() { printf 'install: %s\n' "$*" >&2; exit 1; }

# `set -u` turns a missing HOME into `install.sh: 17: HOME: parameter not set` — no `install:`
# prefix, no hint at what to do. Agent sandboxes run this with a stripped environment, so say it.
# A caller that supplies `SKILLMASTER_INSTALL_DIR` does not need HOME at all, which is why this is
# a check rather than `${HOME:?}`: that form would refuse the very case it should accept.
if [ -z "${SKILLMASTER_INSTALL_DIR:-}" ] && [ -z "${HOME:-}" ]; then
  die "HOME 没有设置，无法决定安装目录（设 SKILLMASTER_INSTALL_DIR 也可以）"
fi
INSTALL_DIR="${SKILLMASTER_INSTALL_DIR:-$HOME/.local/bin}"

# **Pin https only when the base is https.** The override exists for mirrors and for local test
# servers; `--proto '=https'` on an `http://` override refuses every download, which is how the
# published script and the install page would disagree about what a mirror may be.
case "$BASE_URL" in
  https://*) proto="--proto =https --proto-redir =https" ;;
  *) proto="" ;;
esac

# --- 1. Pick the package -----------------------------------------------------

system="$(uname -s)"
machine="$(uname -m)"

case "$system" in
  Darwin) os=darwin ;;
  Linux)  os=linux ;;
  *) die "不支持的系统：${system}（这个脚本只装 macOS 与 Linux；Windows 用 install.ps1）" ;;
esac

case "$machine" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64)  arch=amd64 ;;
  *)
    die "认不出架构「${machine}」，没有为它构建的包。产物清单与手工安装步骤见 ${BASE_URL%/}/skillmaster.md —— 如果那边也没有，这个平台暂时装不了。"
    ;;
esac

artifact="skillmaster_${os}_${arch}.tar.gz"

# --- 2. Fetch the package and the checksums ---------------------------------

command -v curl >/dev/null 2>&1 || die "需要 curl（没有它下不了包）"

# **An explicit template.** In the agent sandboxes this installer runs in, `mktemp -d` without one
# asks macOS for the per-user temp directory (`/var/folders/...`), which the sandbox refuses; a
# template under `$TMPDIR` (the writable one the sandbox provides) is what works.
tmp="$(mktemp -d "${TMPDIR:-/tmp}/skillmaster-install.XXXXXX")" ||
  die "建不了临时目录：${TMPDIR:-/tmp}（如果沙箱拒绝了这次写入，请在你自己的终端里重新运行这条命令）"
# Best effort: a sandbox may refuse deletion. Cleanup is not worth failing an install that has
# already placed the binary, and the temp directory is the installer's own.
trap 'rm -rf "$tmp" 2>/dev/null || true' EXIT
# **In-place copy is not atomic.** If the process is killed while `cp` is writing, the installed
# binary can be half a file with no backup. Say that instead of leaving a silent broken command.
trap 'say "安装被中断；如果 $INSTALL_DIR/skmr 已存在，它可能不完整，请重跑安装。" >&2; exit 130' INT TERM

say "下载 $BASE_URL/$artifact"
# No `--retry-all-errors`: it needs curl 7.71, and the machines most likely to run this are the
# oldest ones. A 404 is not retried either way; `--retry` already covers the transient cases.
curl $proto -fsSL --retry 3 --retry-delay 2 -o "$tmp/$artifact" "$BASE_URL/$artifact" ||
  die "下载失败：$BASE_URL/${artifact}（网络，或这个平台暂时没有产物）"
# **A caller that already verified the manifest passes it in.** The page verifies a signed
# `checksums.txt` and then runs this script; without this, the script would download a fresh
# copy from the same origin and the signed one would protect nothing.
sums="${SKILLMASTER_CHECKSUMS:-}"
if [ -n "$sums" ]; then
  [ -f "$sums" ] || die "SKILLMASTER_CHECKSUMS 指向的文件不存在：$sums"
else
  sums="$tmp/checksums.txt"
  curl $proto -fsSL --retry 3 --retry-delay 2 -o "$sums" "$BASE_URL/checksums.txt" ||
    die "下载失败：$BASE_URL/checksums.txt"
fi

# --- 3. Check the checksum ---------------------------------------------------

want="$(awk -v name="$artifact" '$2 == name { print $1 }' "$sums")"
[ -n "$want" ] || die "checksums.txt 里没有 $artifact 这一行"

if command -v sha256sum >/dev/null 2>&1; then
  got="$(sha256sum "$tmp/$artifact" | awk '{ print $1 }')"
elif command -v shasum >/dev/null 2>&1; then
  got="$(shasum -a 256 "$tmp/$artifact" | awk '{ print $1 }')"
else
  die "需要 sha256sum 或 shasum 来核对校验和"
fi

[ "$want" = "$got" ] || die "校验和不符：${artifact}（期望 ${want}，实际 ${got}）——已停止，没有安装"
say "校验和正确"

# --- 4. Unpack and install ---------------------------------------------------

tar -xzf "$tmp/$artifact" -C "$tmp" || die "解包失败：$artifact"

mkdir -p "$INSTALL_DIR" || die "建不了安装目录：$INSTALL_DIR"
# **Later lines are commands a person or an agent will paste.** A relative override would stop
# meaning the same thing after `cd`, and an unquoted path with a space would split into two words.
resolved="$(cd "$INSTALL_DIR" && pwd)" || die "解析不了安装目录：$INSTALL_DIR"
INSTALL_DIR="$resolved"
# **Write the executable in place first.** A sandbox can allow writing a file and still refuse to
# rename over an existing one: replacing a name needs the delete the allowlist deliberately omits
# (WorkBuddy 5.8.6, measured 2026-11-11) — and this installer is often run by an agent. The
# `--version` probe below runs after this, and no skmr process is running while it runs.
# The staged + rename form stays as a fallback for the case where writing over a busy executable is
# refused (Linux `ETXTBSY`); outside a sandbox it is the more atomic of the two.
if ! cp "$tmp/skmr" "$INSTALL_DIR/skmr" 2>/dev/null; then
  cp "$tmp/skmr" "$INSTALL_DIR/skmr.new" ||
    die "复制失败：$INSTALL_DIR（如果这次执行被沙箱拒绝，请在你自己的终端里重新运行这条命令）"
  chmod 755 "$INSTALL_DIR/skmr.new" ||
    die "改不了 $INSTALL_DIR/skmr.new 的权限：如果这次执行被沙箱拒绝，请在你自己的终端里重新运行这条命令"
  mv -f "$INSTALL_DIR/skmr.new" "$INSTALL_DIR/skmr" ||
    die "写不进 $INSTALL_DIR/skmr，也改不了名：如果这次执行被沙箱拒绝，请在你自己的终端里重新运行这条命令"
else
  chmod 755 "$INSTALL_DIR/skmr" ||
    die "改不了 $INSTALL_DIR/skmr 的权限：如果这次执行被沙箱拒绝，请在你自己的终端里重新运行这条命令"
fi

# --- 5. Say what to do next ---------------------------------------------------

say ""
say "已安装到 $INSTALL_DIR/skmr"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    say ""
    say "$INSTALL_DIR 不在 PATH 里。把它加进去（选你用的 shell）："
    say "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.zshrc    # 或 ~/.bashrc"
    say "  然后重开一个终端。"
    ;;
esac

say ""
"$INSTALL_DIR/skmr" --version || die "装好了但跑不起来：$INSTALL_DIR/skmr --version"

# --- 8. Offer to configure the agent sandboxes ------------------------------
#
# **The installer prints; it never reads a terminal.** A consent prompt on `/dev/tty` cannot be
# answered here, and a prompt nobody sees is worse than a sentence. Whether to run `doctor --fix`
# is the user's decision; a person running it gives consent by running it. The keys, paths and
# backups stay in `skmr doctor --fix` (ADR 0186). No `/dev/tty`, no `[Y/n]`, and a failed read can
# never be taken as "yes".
#
# **Only print this when there is something to configure.** A terminal-only user with none of these
# tools installed should not be handed a security question about them.
has_clients() {
  # No HOME, no client directories to look at — and `set -u` would abort on the first `$HOME` below.
  [ -n "${HOME:-}" ] || return 1
  for dir in \
    "$HOME/.workbuddy" \
    "${CLAUDE_CONFIG_DIR:-$HOME/.claude}" \
    "${CODEX_HOME:-$HOME/.codex}" \
    "$HOME/.gemini" \
    "$HOME/.cursor" \
    "$HOME/.config/devin" \
    "$HOME/.config/zed" \
    "${COPILOT_HOME:-$HOME/.copilot}"
  do
    if [ -d "$dir" ]; then
      return 0
    fi
  done
  return 1
}

print_sandbox_step() {
  say ""
  say "可选：让 SkillMaster 在这些 AI 工具里正常干活，不用每次用都弹窗问你。"
  say "这一步不会自动执行；是否执行由你决定，并且请在你自己的终端里运行。"
  say ""
  say "先看会改什么："
  say "  \"$INSTALL_DIR/skmr\" doctor --fix --dry-run"
  say "确认后再运行："
  say "  \"$INSTALL_DIR/skmr\" doctor --fix"
  say "以后想撤回条目："
  say "  \"$INSTALL_DIR/skmr\" doctor --fix --undo"
  say "（为放行打开的开关不会还原，输出也会说明；Codex 与 Gemini 的例外也以 --dry-run 的输出为准。）"
}

if has_clients; then
  print_sandbox_step
else
  say ""
  say "没有检测到带沙箱配置的 AI 工具（Claude Code、WorkBuddy、Codex 等）。"
  say "如果你用的是 Cline、Aider 这类，运行 \"$INSTALL_DIR/skmr\" doctor --fix 会打印替代做法。"
fi

say ""
say "客户端默认最多每 24 小时检查一次更新，下载的更新包按 checksums.txt 校验。"
say "不想自动更新：在 shell 配置里设 SKILLMASTER_AUTO_UPDATE=0。"

say ""
say "下一步："
say "  \"$INSTALL_DIR/skmr\" auth status"
say "已登录后，新开一个会话，让 Agent 带你走一遍："
say "  把 /skill-name 当入口的 Agent（如 Claude Code）：/skillmaster 带我走一遍 SkillMaster 的新人引导"
say "  其他 Agent：\"$INSTALL_DIR/skmr\" startup"
say "云端 / 远程 Agent（每开一个会话就是新环境）不在这一版的安装范围：请换一台本地电脑或本机 Agent。"
