#!/usr/bin/env zsh

setopt NO_GLOB

ash() {
  print -r -- "ASH:$*"
}

_ash_delegate() {
  local cmd="$1"
  shift
  print -r -- "DELEGATE:${cmd}:$*"
}

_ash_should_route() {
  local cmd="$1"
  shift
  local -a args
  args=("$@")
  local argc=${#args}

  [[ $argc -eq 0 ]] && return 1

  local a
  for a in "${args[@]}"; do
    [[ "$a" == -* ]] && return 1
  done

  for a in "${args[@]}"; do
    [[ "$a" == */* || "$a" == ./* || "$a" == ../* ]] && return 1
  done

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
      [[ $argc -ge 2 ]] && return 0
      ;;
  esac

  return 1
}

_ash_route_or_delegate() {
  local cmd="$1"
  shift
  if _ash_should_route "$cmd" "$@"; then
    ash "$cmd" "$@"
    return $?
  fi
  _ash_delegate "$cmd" "$@"
}

_ash_route_or_delegate_builtin() {
  local builtin_name="$1"
  shift
  if _ash_should_route "$builtin_name" "$@"; then
    ash "$builtin_name" "$@"
    return $?
  fi
  _ash_delegate "$builtin_name" "$@"
}

what()  { _ash_route_or_delegate what  "$@"; }
What()  { _ash_route_or_delegate What  "$@"; }
which() { _ash_route_or_delegate which "$@"; }
Which() { _ash_route_or_delegate Which "$@"; }
who()   { _ash_route_or_delegate who   "$@"; }
Who()   { _ash_route_or_delegate Who   "$@"; }
where() { _ash_route_or_delegate_builtin where "$@"; }
Where() { _ash_route_or_delegate_builtin where "$@"; }

test()  { _ash_route_or_delegate_builtin test "$@"; }
Test()  { _ash_route_or_delegate_builtin test "$@"; }
type()  { _ash_route_or_delegate_builtin type "$@"; }
Type()  { _ash_route_or_delegate_builtin type "$@"; }
Time()  { _ash_route_or_delegate Time "$@"; }
