# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

set -l config_base "$HOME/.config"
if set -q XDG_CONFIG_HOME
	string match -q '/*' -- "$XDG_CONFIG_HOME"; or return
	set config_base "$XDG_CONFIG_HOME"
end
if test -f "$config_base/ash/.ash_fish.fish"
	source "$config_base/ash/.ash_fish.fish"
end
