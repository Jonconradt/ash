package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

func bashRCPath(home string) string {
	return filepath.Join(home, ".bashrc")
}

func bashWrapperFileName() string {
	return ".ash_bashrc"
}

func bashInstallSourceBlock() string {
	return strings.TrimSpace(`
` + installStartMarker + `
[ -f "$HOME/.ash/.ash_env" ] && . "$HOME/.ash/.ash_env"
[ -f "$HOME/.ash/.ash_bashrc" ] && . "$HOME/.ash/.ash_bashrc"
` + installEndMarker)
}

func bashInstallWrapperContent() string {
	content, err := readEmbeddedBootstrapAsset("ash_bootstrap/.ash_bashrc")
	if err == nil {
		return strings.TrimSpace(string(content))
	}
	return fallbackBashInstallWrapperContent()
}

func fallbackBashInstallWrapperContent() string {
	return strings.TrimSpace(`
` + installStartMarker + `
[ -f "$HOME/.ash/.ash_env" ] && . "$HOME/.ash/.ash_env"
case "$-" in
	*i*) ;;
	*) return ;;
esac

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

command_not_found_handle() {
	if _ash_prompt_processing_enabled; then
		ash "$@"
		return $?
	fi
	return 127
}

_ash_should_route() {
  local cmd="$1"
  shift
  local args=("$@")
  local argc=${#args[@]}
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
		first_at="$(printf '%s' "${args[0]}" | tr '[:upper:]' '[:lower:]')"
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
    if [[ $argc -eq 1 && "${args[0]}" =~ ^[A-Za-z0-9_.-]+$ ]]; then
      return 1
    fi
  fi

  local full="$cmd"
  for a in "${args[@]}"; do
    full+=" $a"
  done

  [[ "$full" == *\? && $argc -ge 2 ]] && return 0

  local first
  first="$(printf '%s' "${args[0]}" | tr '[:upper:]' '[:lower:]')"
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
				for (( i=1; i<limit; i++ )); do
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
		say)
			if [[ $argc -ge 2 ]]; then
				local first_token
				first_token="$(printf '%s' "${args[0]}" | tr '[:upper:]' '[:lower:]')"
				first_token="${first_token%%[?!.,:;]}"
				case "$first_token" in
					out|something|a|an|the|please|why|how|when|where|who|what|can|could|should|would)
						return 0
						;;
				esac
			fi
			;;
		in|for)
			if [[ $argc -ge 2 ]]; then
				local first_token
				first_token="$(printf '%s' "${args[0]}" | tr '[:upper:]' '[:lower:]')"
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
				first_token="$(printf '%s' "${args[0]}" | tr '[:upper:]' '[:lower:]')"
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
say()   { _ash_route_or_delegate say   "$@"; }
Say()   { _ash_route_or_delegate Say   "$@"; }
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

func ensureBashProfileSourcingForInstall(dryRun bool, stdout io.Writer) error {
	home, err := osUserHomeDir()
	if err != nil {
		return err
	}
	profilePath := filepath.Join(home, ".bash_profile")
	line := `[ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"`

	existing, err := readFileIfExists(profilePath)
	if err != nil {
		return err
	}
	updated := normalizeBashProfileForInstall(existing, line)
	if updated == existing {
		return nil
	}

	if dryRun {
		fmt.Fprintf(stdout, "[dry-run] would append ash source line to %s\n", profilePath)
		return nil
	}
	return osWriteFile(profilePath, []byte(updated), 0o600)
}
func normalizeBashProfileForInstall(content, bashRCLine string) string {
	lines := strings.Split(content, "\n")
	filtered := make([]string, 0, len(lines)+1)
	hasBashRC := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			filtered = append(filtered, line)
		case strings.Contains(trimmed, ".ash/.ash_env") || strings.Contains(trimmed, ".ash/.ash_bashrc"):
			continue
		default:
			if isBashRCSourceLine(trimmed) {
				if hasBashRC {
					continue
				}
				hasBashRC = true
				filtered = append(filtered, bashRCLine)
				continue
			}
			filtered = append(filtered, line)
		}
	}

	updated := strings.Join(filtered, "\n")
	if !hasBashRC {
		if updated != "" && !strings.HasSuffix(updated, "\n") {
			updated += "\n"
		}
		updated += bashRCLine + "\n"
	} else if content != "" && strings.HasSuffix(content, "\n") && !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	return updated
}

func isBashRCSourceLine(line string) bool {
	patterns := []string{
		`[ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"`,
		`[ -f ~/.bashrc ] && . ~/.bashrc`,
		`[ -f "$HOME/.bashrc" ] && source "$HOME/.bashrc"`,
		`[ -f ~/.bashrc ] && source ~/.bashrc`,
		`. "$HOME/.bashrc"`,
		`. ~/.bashrc`,
		`source "$HOME/.bashrc"`,
		`source ~/.bashrc`,
	}
	for _, pattern := range patterns {
		if line == pattern {
			return true
		}
	}
	return false
}
