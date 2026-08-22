package main

import (
	"path/filepath"
	"strings"
)

func zshRCPath(home string) string {
	return filepath.Join(home, ".zshrc")
}

func zshWrapperFileName() string {
	return ".ash_zshrc"
}

func zshInstallSourceBlock() string {
	return strings.TrimSpace(`
` + installStartMarker + `
[ -f "$HOME/.ash/.ash_env" ] && . "$HOME/.ash/.ash_env"
[ -f "$HOME/.ash/.ash_zshrc" ] && . "$HOME/.ash/.ash_zshrc"
` + installEndMarker)
}

func zshInstallWrapperContent() string {
	content, err := readEmbeddedBootstrapAsset("ash_bootstrap/.ash_zshrc")
	if err == nil {
		return strings.TrimSpace(string(content))
	}
	return fallbackZshInstallWrapperContent()
}

func fallbackZshInstallWrapperContent() string {
	return strings.TrimSpace(`
` + installStartMarker + `
[ -f "$HOME/.ash/.ash_env" ] && . "$HOME/.ash/.ash_env"
_ash_prompt_processing_enabled() {
	local snooze_file="$HOME/.ash/.ash_snooze_until"
	local expires_at now
	if [[ ! -r "$snooze_file" ]]; then
		return 0
	fi
	expires_at="$(<"$snooze_file")"
	if [[ ! "$expires_at" =~ ^[0-9]+$ ]]; then
		return 0
	fi
	now="$(date +%s)"
	(( expires_at <= now ))
}

command_not_found_handler() {
	if _ash_prompt_processing_enabled; then
		_ash_ensure_broker >/dev/null 2>&1
		[[ -n "${ASH_BROKER_LEASE:-}" ]] && touch "$ASH_BROKER_LEASE"
		ash "$@"
	else
		return 127
	fi
  return $?
}

_ash_should_route() {
  local cmd="$1"
  shift
  local -a args
  args=("$@")
  local argc=${#args}
	local cmd_lower
	cmd_lower="$(printf '%s' "$cmd" | tr '[:upper:]' '[:lower:]')"
	local natural_wrapper=0
	case "$cmd_lower" in
		what|which|who|where|at|in|for) natural_wrapper=1 ;;
	esac

  [[ $argc -eq 0 ]] && return 1

  local a
  for a in "${args[@]}"; do
    [[ "$a" == -* ]] && return 1
  done

	local has_path_like=0
	for a in "${args[@]}"; do
		if [[ "$a" == */* || "$a" == ./* || "$a" == ../* ]]; then
			has_path_like=1
			break
		fi
	done
	if [[ $has_path_like -eq 1 && ( $natural_wrapper -eq 0 || $argc -eq 1 ) ]]; then
		return 1
	fi

	if [[ "$cmd_lower" == "at" ]]; then
		local first_at
		first_at="$(printf '%s' "${args[1]}" | tr '[:upper:]' '[:lower:]')"
		first_at="${first_at%%[?!.,:;]}"
		if [[ "$first_at" =~ [0-9:] ]]; then
			return 1
		fi
		case "$first_at" in
			now|today|tomorrow|teatime|midnight|noon)
				return 1
				;;
			am|pm)
				return 1
				;;
		esac
	fi

  if [[ "$cmd" == "Time" || "$cmd" == "test" || "$cmd" == "Test" || "$cmd" == "type" || "$cmd" == "Type" ]]; then
    if [[ $argc -eq 1 && "${args[1]}" =~ '^[A-Za-z0-9_.-]+$' ]]; then
      return 1
    fi
  fi

  local full="$cmd"
  for a in "${args[@]}"; do
    full+=" $a"
  done

  [[ "$full" == *\? && $argc -ge 2 ]] && return 0

  local first
  first="$(printf '%s' "${args[1]}" | tr '[:upper:]' '[:lower:]')"
  case "$first" in
    is|are|am|do|does|did|can|could|should|would|will|why|how|when|where|who)
			if [[ $argc -ge 2 ]]; then
				if [[ $has_path_like -eq 0 || ( $natural_wrapper -eq 1 && $argc -ge 3 ) ]]; then
					return 0
				fi
			fi
      ;;
  esac

	case "$cmd_lower" in
		what|which|who|where)
			if [[ $argc -ge 3 ]]; then
				local limit=4
				(( argc < limit )) && limit=$argc
				local i token raw
				for (( i=2; i<=limit; i++ )); do
					raw="${args[$i]}"
					token="$(printf '%s' "$raw" | tr '[:upper:]' '[:lower:]')"
					token="${token%%[?!.,:;]}"
					case "$token" in
						is|are|am|do|does|did|can|could|should|would|will|why|how|when|where|who|if)
							return 0
							;;
					esac
				done
			fi
			;;
		in|for)
			if [[ $argc -ge 2 ]]; then
				local first_token
				first_token="$(printf '%s' "${args[1]}" | tr '[:upper:]' '[:lower:]')"
				first_token="${first_token%%[?!.,:;]}"
				case "$first_token" in
					this|that|these|those|the|a|an|my|our|your|please|what|when|how|why|who|where|is|are|do|can|should|would)
						return 0
						;;
				esac
			fi
			;;
		at)
			if [[ $argc -ge 2 ]]; then
				local first_token
				first_token="$(printf '%s' "${args[1]}" | tr '[:upper:]' '[:lower:]')"
				first_token="${first_token%%[?!.,:;]}"
				case "$first_token" in
					remind|tell|ask|message|note|please|what|when|how|why|who|where)
						return 0
						;;
				esac
			fi
			;;
	esac

  return 1
}

_ash_route_or_delegate() {
  local cmd="$1"
  shift
	if ! _ash_prompt_processing_enabled; then
		command "$cmd" "$@"
		return $?
	fi
  if _ash_should_route "$cmd" "$@"; then
    ash "$cmd" "$@"
    return $?
  fi
  command "$cmd" "$@"
}

_ash_route_or_delegate_builtin() {
  local builtin_name="$1"
  shift
	if ! _ash_prompt_processing_enabled; then
		builtin "$builtin_name" "$@"
		return $?
	fi
  if _ash_should_route "$builtin_name" "$@"; then
    ash "$builtin_name" "$@"
    return $?
  fi
  builtin "$builtin_name" "$@"
}

what()  { _ash_route_or_delegate what  "$@"; }
What()  { _ash_route_or_delegate What  "$@"; }
which() { _ash_route_or_delegate which "$@"; }
Which() { _ash_route_or_delegate Which "$@"; }
who()   { _ash_route_or_delegate who   "$@"; }
Who()   { _ash_route_or_delegate Who   "$@"; }
where() { _ash_route_or_delegate_builtin where "$@"; }
Where() { _ash_route_or_delegate_builtin where "$@"; }
at()    { _ash_route_or_delegate at    "$@"; }
At()    { _ash_route_or_delegate At    "$@"; }
In()    { _ash_route_or_delegate In    "$@"; }
For()   { _ash_route_or_delegate For   "$@"; }

test()  { _ash_route_or_delegate_builtin test "$@"; }
Test()  { _ash_route_or_delegate_builtin test "$@"; }
type()  { _ash_route_or_delegate_builtin type "$@"; }
Type()  { _ash_route_or_delegate_builtin type "$@"; }
Time()  { _ash_route_or_delegate Time "$@"; }
` + installEndMarker)
}
