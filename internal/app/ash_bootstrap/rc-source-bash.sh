_ash_config_base="${XDG_CONFIG_HOME:-$HOME/.config}"
case "$_ash_config_base" in
	/*) ;;
	*) return ;;
esac
[ -f "$_ash_config_base/ash/.ash_bashrc" ] && . "$_ash_config_base/ash/.ash_bashrc"
