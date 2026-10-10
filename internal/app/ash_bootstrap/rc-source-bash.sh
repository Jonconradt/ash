# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

_ash_config_base="${XDG_CONFIG_HOME:-$HOME/.config}"
case "$_ash_config_base" in
	/*) ;;
	*) return ;;
esac
[ -f "$_ash_config_base/ash/.ash_bashrc" ] && . "$_ash_config_base/ash/.ash_bashrc"
