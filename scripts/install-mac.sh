#!/bin/bash
# Compatible with the Bash 3.2 shipped with macOS. Run with: bash install-mac.sh
set -euo pipefail
umask 077

fail() { printf '\n错误：%s\n' "$*" >&2; exit 1; }
usage() {
  cat <<'HELP'
mywormhole Mac 配置脚本

  bash install-mac.sh [--server https://你的服务地址] [--skip-health-check]

自动选择 Apple Silicon / Intel 程序，安装到当前用户目录并配置发送和接收。
邀请令牌会隐藏输入，仅保存在权限为 600 的本地配置文件中。

  --server URL          预填 Cloudflare Worker 地址
  --skip-health-check   跳过联网检查（离线配置时使用）
  --install-root DIR    安装到指定用户目录（默认当前用户主目录）
  -h, --help            显示说明
HELP
}

server_arg=''
skip_health=0
install_root="$HOME"
while [ "$#" -gt 0 ]; do
  case "$1" in
    --server)
      [ "$#" -ge 2 ] || fail '--server 后面需要服务地址'
      server_arg="$2"; shift 2 ;;
    --skip-health-check) skip_health=1; shift ;;
    --install-root)
      [ "$#" -ge 2 ] || fail '--install-root 后面需要目录'
      install_root="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) fail "未知参数：$1（可运行 --help 查看说明）" ;;
  esac
done

[ "$(uname -s)" = Darwin ] || fail '此脚本适用于 macOS。'
[ -d "$install_root" ] || fail '安装根目录不存在。'
install_root="$(cd "$install_root" && pwd -P)"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
machine="$(uname -m)"
# Detect Apple Silicon even when Terminal itself is running under Rosetta.
if [ "$machine" = arm64 ] || [ "$(sysctl -in hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
  arch=arm64
elif [ "$machine" = x86_64 ]; then
  arch=amd64
else
  fail "暂不支持此芯片：$machine"
fi
binary_name="mywormhole-darwin-$arch"
binary_source="$script_dir/$binary_name"
if [ ! -f "$binary_source" ]; then binary_source="$script_dir/../dist/$binary_name"; fi
[ -f "$binary_source" ] || fail "未找到 $binary_name。请把安装包完整解压后再运行。"

if [ -f "$script_dir/SHA256SUMS" ]; then
  expected="$(awk -v name="$binary_name" '$2 == name { print $1 }' "$script_dir/SHA256SUMS")"
  hash_pattern='^[0-9a-fA-F]{64}$'
  [[ "$expected" =~ $hash_pattern ]] || fail '校验清单无效，请重新获取安装包。'
  command -v shasum >/dev/null 2>&1 || fail '未找到 macOS 的 shasum 命令。'
  actual="$(shasum -a 256 "$binary_source" | awk '{print $1}')"
  [ "$actual" = "$expected" ] || fail '程序 SHA-256 不匹配，请重新获取安装包。'
fi

bin_dir="$install_root/.local/bin"
data_dir="$install_root/.local/share/mywormhole"
config_dir="$install_root/.config/mywormhole"
config_file="$config_dir/settings"
wrapper_path="$bin_dir/mywormhole"
binary_path="$data_dir/mywormhole"
[ ! -d "$wrapper_path" ] || fail "$wrapper_path 是目录，无法安装命令。"
mkdir -p "$bin_dir" "$data_dir" "$config_dir"
chmod 700 "$data_dir" "$config_dir"

stage_binary=''; stage_config=''; stage_wrapper=''
cleanup() {
  for temporary in "$stage_binary" "$stage_config" "$stage_wrapper"; do
    if [ -n "$temporary" ] && [ -f "$temporary" ]; then rm -f -- "$temporary"; fi
  done
}
trap cleanup EXIT
chmod u+x "$binary_source"
if ! "$binary_source" version; then
  fail 'Mac 程序未能运行。若系统拦截此自建程序，请按随包说明中的 Apple 官方指引处理后重试。'
fi
stage_binary="$(mktemp "$data_dir/.install-binary.XXXXXX")"
cp "$binary_source" "$stage_binary"
chmod 700 "$stage_binary"

existing_server=''; existing_token=''
if [ -f "$config_file" ]; then
  { IFS= read -r existing_server || true; IFS= read -r existing_token || true; } < "$config_file"
fi
server="${server_arg:-${existing_server:-${MYWORMHOLE_SERVER:-}}}"
printf '\n正在配置 mywormhole（%s）\n' "$arch"
if [ -n "$server_arg" ]; then
  printf '服务地址：%s\n' "$server_arg"
else
  if [ -n "$server" ]; then
    printf 'Cloudflare Worker 地址 [回车保留 %s]：' "$server"
  else
    printf 'Cloudflare Worker 地址（https://...）：'
  fi
  IFS= read -r answer || fail '未读到服务地址。请在终端交互运行脚本。'
  if [ -n "$answer" ]; then server="$answer"; fi
fi
server="${server%/}"
origin_pattern='^https://[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]+)?$'
[[ "$server" =~ $origin_pattern ]] || fail '请输入完整 HTTPS 服务地址，不含路径、用户名、查询参数或空格。'

if [ "$skip_health" -eq 0 ]; then
  command -v curl >/dev/null 2>&1 || fail '未找到 macOS 的 curl 命令。'
  printf '检查服务连接……\n'
  health="$(curl --fail --silent --show-error --max-time 15 --proto '=https' "$server/health")" ||
    fail '无法连接服务，请检查地址和网络。只想离线保存配置时，可加 --skip-health-check 重新运行。'
  service_pattern='"service"[[:space:]]*:[[:space:]]*"mywormhole"'
  protocol_pattern='"protocol"[[:space:]]*:[[:space:]]*1([,}]|[[:space:]])'
  ready_pattern='"ready"[[:space:]]*:[[:space:]]*true([,}]|[[:space:]])'
  [[ "$health" =~ $service_pattern ]] && [[ "$health" =~ $protocol_pattern ]] || fail '此地址不是兼容的 mywormhole 服务。'
  [[ "$health" =~ $ready_pattern ]] || fail '服务配置尚未就绪（ready 不是 true），请联系部署者。'
fi

token="${existing_token:-${MYWORMHOLE_INVITE_TOKEN:-}}"
if [ -n "$token" ]; then
  printf '邀请令牌 INVITE_TOKEN（输入不回显，回车保留已有令牌）：'
else
  printf '邀请令牌 INVITE_TOKEN（输入不回显）：'
fi
IFS= read -r -s answer || fail '未读到邀请令牌。请在终端交互运行脚本。'
printf '\n'
if [ -n "$answer" ]; then token="$answer"; fi
[ "${#token}" -ge 24 ] || fail '邀请令牌至少应有 24 个字符，请使用部署者设置的 INVITE_TOKEN。'
case "$token" in *$'\r'*|*$'\n'*) fail '邀请令牌不能包含换行符。' ;; esac

stage_config="$(mktemp "$config_dir/.install-settings.XXXXXX")"
stage_wrapper="$(mktemp "$bin_dir/.install-wrapper.XXXXXX")"
# Both values are plain data, committed together with one atomic rename.
printf '%s\n%s\n' "$server" "$token" > "$stage_config"
chmod 600 "$stage_config"
{
  printf '#!/bin/bash\n# mywormhole managed launcher v1\nset -euo pipefail\n'
  printf 'install_root=%q\n' "$install_root"
  cat <<'WRAPPER'
config_dir="$install_root/.config/mywormhole"
if [ ! -r "$config_dir/settings" ]; then
  printf 'mywormhole 配置缺失，请重新运行安装脚本。\n' >&2
  exit 1
fi
{
  IFS= read -r MYWORMHOLE_SERVER
  IFS= read -r MYWORMHOLE_INVITE_TOKEN
} < "$config_dir/settings"
export MYWORMHOLE_SERVER MYWORMHOLE_INVITE_TOKEN
exec "$install_root/.local/share/mywormhole/mywormhole" "$@"
WRAPPER
} > "$stage_wrapper"
chmod 700 "$stage_wrapper"
mv -f "$stage_binary" "$binary_path"; stage_binary=''
mv -f "$stage_config" "$config_file"; stage_config=''
mv -f "$stage_wrapper" "$wrapper_path"; stage_wrapper=''
if [ -f "$script_dir/THIRD_PARTY_NOTICES.md" ]; then
  cp "$script_dir/THIRD_PARTY_NOTICES.md" "$data_dir/THIRD_PARTY_NOTICES.md"
elif [ -f "$script_dir/../THIRD_PARTY_NOTICES.md" ]; then
  cp "$script_dir/../THIRD_PARTY_NOTICES.md" "$data_dir/THIRD_PARTY_NOTICES.md"
fi

profile=''
login_shell="${SHELL:-/bin/zsh}"
case "${login_shell##*/}" in
  zsh|'') profile="$install_root/.zshrc" ;;
  bash) profile="$install_root/.bash_profile" ;;
esac
if [ -n "$profile" ]; then
  printf -v path_line 'export PATH=%q:"$PATH" # mywormhole' "$bin_dir"
  if [ ! -f "$profile" ] || ! grep -Fqx -- "$path_line" "$profile"; then
    printf '\n%s\n' "$path_line" >> "$profile"
  fi
fi

printf '\n配置完成。服务地址：%s\n' "$server"
printf '邀请令牌已保存在本机私有配置目录，未写入 shell 配置或终端输出。\n'
printf '\n请新开一个终端窗口，再运行：\n'
printf '  mywormhole send "$HOME/Downloads/archive.zip"\n'
printf '  mywormhole receive 483921-amber-brook-cloud-river\n'
printf '\n当前终端也可直接运行：\n  %q version\n' "$wrapper_path"
if [ -z "$profile" ]; then printf '\n你的 shell 需要自行将 %s 加入 PATH。\n' "$bin_dir"; fi
