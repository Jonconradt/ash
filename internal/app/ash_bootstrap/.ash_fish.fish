# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

# >>> ash install >>>
if not status is-interactive
	return
end

if not set -q __ash_local_bin_on_path
	set -gx PATH "$HOME/.local/bin" $PATH
	set -g __ash_local_bin_on_path 1
end

set -g __ash_config_base "$HOME/.config"
set -g __ash_data_base "$HOME/.local/share"
set -g __ash_state_base "$HOME/.local/state"
if set -q XDG_CONFIG_HOME
	string match -q '/*' -- "$XDG_CONFIG_HOME"; or return
	set -g __ash_config_base "$XDG_CONFIG_HOME"
end
if set -q XDG_DATA_HOME
	string match -q '/*' -- "$XDG_DATA_HOME"; or return
	set -g __ash_data_base "$XDG_DATA_HOME"
end
if set -q XDG_STATE_HOME
	string match -q '/*' -- "$XDG_STATE_HOME"; or return
	set -g __ash_state_base "$XDG_STATE_HOME"
end
set -g __ash_config_dir "$__ash_config_base/ash"
set -g __ash_data_dir "$__ash_data_base/ash"
set -g __ash_state_dir "$__ash_state_base/ash"
if set -q XDG_RUNTIME_DIR
	string match -q '/*' -- "$XDG_RUNTIME_DIR"; or return
	set -g __ash_runtime_dir "$XDG_RUNTIME_DIR/ash"
else
	set -g __ash_runtime_dir "$__ash_state_dir/runtime"
end
if test -f "$__ash_config_dir/.ash_fish_env.fish"
	source "$__ash_config_dir/.ash_fish_env.fish"
end

function _ash_ensure_broker
	set -l mcp_credential_key ""
	if set -q ASH_MCP_CREDENTIAL_KEY
		set mcp_credential_key "$ASH_MCP_CREDENTIAL_KEY"
		set -e ASH_MCP_CREDENTIAL_KEY
	end
	if not set -q ASH_AI_ENDPOINT; or not set -q ASH_AI_MODEL
		return 1
	end
	if test -z "$ASH_AI_ENDPOINT"; or test -z "$ASH_AI_MODEL"
		return 1
	end
	# Without this probe a missing ash-broker reaches fish_command_not_found, which
	# would forward the broker's own command line to the AI as a prompt.
	if not command -q ash-broker
		return 1
	end
	if set -q ASH_BROKER_SOCKET ASH_BROKER_TOKEN ASH_BROKER_PID
		if test -S "$ASH_BROKER_SOCKET"; and command kill -0 "$ASH_BROKER_PID" 2>/dev/null
			return 0
		end
	end

	set -l runtime_dir "$__ash_runtime_dir"
	set -l session_id ""
	if set -q ASH_SESSION_ID; and test -n "$ASH_SESSION_ID"
		set session_id "$ASH_SESSION_ID"
	else
		set session_id (command head -c 100 /dev/urandom | command tr -dc 'a-zA-Z0-9' | command fold -w 16 | command head -n 1)
		test -n "$session_id"; or return 1
		set -gx ASH_SESSION_ID "$session_id"
	end
	set -l socket_path "$runtime_dir/$session_id.sock"
	command mkdir -p -m 700 "$runtime_dir"; or return 1
	set -l token (command head -c 32 /dev/urandom | command od -An -tx1 | string replace -a ' ' '' | string join '')
	test -n "$token"; or return 1
	set -l parent_pid $fish_pid

	begin
		set -lx ASH_BROKER_TOKEN "$token"
		set -lx ASH_MCP_CREDENTIAL_KEY "$mcp_credential_key"
		command ash-broker --socket "$socket_path" --session-id "$session_id" --parent-pid "$parent_pid" </dev/null >/dev/null 2>&1
	end &
	set -l broker_pid $last_pid
	disown "$broker_pid" 2>/dev/null; or true
	for attempt in (command seq 1 40)
		if test -S "$socket_path"
			set -gx ASH_BROKER_SOCKET "$socket_path"
			set -gx ASH_BROKER_TOKEN "$token"
			set -gx ASH_BROKER_PID "$broker_pid"
			return 0
		end
		command kill -0 "$broker_pid" 2>/dev/null; or return 1
		command sleep 0.01
	end
	return 1
end

function _ash_prompt_processing_enabled
	set -l snooze_file "$__ash_state_dir/.ash_snooze_until"
	if not test -r "$snooze_file"
		return 0
	end
	set -l expires_at (string trim < "$snooze_file")
	string match -qr '^[0-9]+$' -- "$expires_at"; or return 0
	set -l now (command date +%s)
	test "$expires_at" -le "$now"
end

function _ash_prepare_prompt
	_ash_ensure_broker >/dev/null 2>&1
end

if not set -q __ash_wrap_command_not_found
	if functions -q fish_command_not_found
		functions -c fish_command_not_found __ash_original_command_not_found
	end
	set -g __ash_wrap_command_not_found 1
end

function fish_command_not_found
	if _ash_prompt_processing_enabled
		if not command -q ash
			if functions -q __ash_original_command_not_found
				__ash_original_command_not_found $argv
				return $status
			end
			printf '%s: command not found\n' "$argv[1]" >&2
			return 127
		end
		_ash_prepare_prompt
		command ash -- $argv
		return $status
	end
	if functions -q __ash_original_command_not_found
		__ash_original_command_not_found $argv
		return $status
	end
	return 127
end

function ash
	if not command -q ash
		printf '%s\n' "ash: command not found" >&2
		return 127
	end
	if test (count $argv) -ge 2; and test "$argv[1]" = mcp; and test "$argv[2]" = add
		_ash_prepare_prompt
	end
	command ash $argv
end

function _ash_trim_trailing_punctuation
	string replace -r '[?!.,:;]$' '' -- "$argv[1]"
end

function _ash_should_route
	set -l cmd "$argv[1]"
	set -e argv[1]
	set -l argc (count $argv)
	set -l cmd_lower (string lower -- "$cmd")
	set -l natural_wrapper 0
	switch "$cmd_lower"
		case what which who where at in for write
			set natural_wrapper 1
	end

	test "$argc" -eq 0; and return 1
	for arg in $argv
		string match -q -- '-*' "$arg"; and return 1
	end

	set -l has_path_like 0
	for arg in $argv
		if string match -q -- '*/*' "$arg"
			set has_path_like 1
			break
		end
	end
	if test "$has_path_like" -eq 1
		if test "$natural_wrapper" -eq 0; or test "$argc" -eq 1
			return 1
		end
	end

	if test "$cmd_lower" = at
		set -l first_at (_ash_trim_trailing_punctuation "$argv[1]" | string lower)
		string match -qr '[0-9:]' -- "$first_at"; and return 1
		switch "$first_at"
			case now today tomorrow teatime midnight noon am pm
				return 1
		end
	end

	switch "$cmd"
		case Time Test Type
			if test "$argc" -eq 1; and string match -qr '^[A-Za-z0-9_.-]+$' -- "$argv[1]"
				return 1
			end
	end

	set -l full (string join ' ' "$cmd" $argv)
	string match -qr '\?$' -- "$full"; and test "$argc" -ge 2; and return 0

	set -l first (_ash_trim_trailing_punctuation "$argv[1]" | string lower)
	switch "$first"
		case is are am do does did can could should would will shall may might must ought why how when where who whom whose
			if test "$argc" -ge 2
				if test "$has_path_like" -eq 0; or begin; test "$natural_wrapper" -eq 1; and test "$argc" -ge 3; end
					return 0
				end
			end
	end

	switch "$cmd_lower"
		case write
			if test "$argc" -ge 2
				switch (_ash_trim_trailing_punctuation "$argv[1]" | string lower)
					case a an the this that these those my our your please poem
						return 0
				end
			end
		case what which who where
			if test "$argc" -ge 3
				set -l limit 4
				if test "$argc" -lt "$limit"
					set limit "$argc"
				end
				for index in (command seq 2 "$limit")
					switch (_ash_trim_trailing_punctuation "$argv[$index]" | string lower)
						case is are am do does did can could should would will shall may might must ought why how when where who whom whose if
							return 0
					end
				end
			end
		case say
			if test "$argc" -ge 2
				switch (_ash_trim_trailing_punctuation "$argv[1]" | string lower)
					case out something a an the please why how when where who what can could should would
						return 0
				end
			end
		case in for
			if test "$argc" -ge 2
				switch (_ash_trim_trailing_punctuation "$argv[1]" | string lower)
					case this that these those the a an my our your please what when how why who where whom whose is are do can could should would will shall may might must ought
						return 0
				end
			end
		case at
			if test "$argc" -ge 2
				switch (_ash_trim_trailing_punctuation "$argv[1]" | string lower)
					case remind tell ask message note please what when how why who where
						return 0
				end
			end
	end
	return 1
end

function _ash_route_or_delegate
	set -l cmd "$argv[1]"
	set -e argv[1]
	if not _ash_prompt_processing_enabled
		command "$cmd" $argv
		return $status
	end
	if _ash_should_route "$cmd" $argv
		_ash_prepare_prompt
		command ash -- "$cmd" $argv
		return $status
	end
	command "$cmd" $argv
end

function _ash_route_or_delegate_say
	set -l cmd "$argv[1]"
	set -e argv[1]
	if not _ash_prompt_processing_enabled
		command "$cmd" $argv
		return $status
	end
	if _ash_should_route "$cmd" $argv
		if command -q say
			command ash --say "$cmd" $argv
			return $status
		end
		command ash -- "$cmd" $argv
		return $status
	end
	command "$cmd" $argv
end

function what; _ash_route_or_delegate what $argv; end
function What; _ash_route_or_delegate What $argv; end
function write; _ash_route_or_delegate write $argv; end
function Write; _ash_route_or_delegate Write $argv; end
function which; _ash_route_or_delegate which $argv; end
function Which; _ash_route_or_delegate Which $argv; end
function who; _ash_route_or_delegate who $argv; end
function Who; _ash_route_or_delegate Who $argv; end
function say; _ash_route_or_delegate_say say $argv; end
function Say; _ash_route_or_delegate_say Say $argv; end
function at; _ash_route_or_delegate at $argv; end
function At; _ash_route_or_delegate At $argv; end
function In; _ash_route_or_delegate In $argv; end
function For; _ash_route_or_delegate For $argv; end
function Test
	if not _ash_prompt_processing_enabled
		builtin test $argv
		return $status
	end
	if _ash_should_route Test $argv
		_ash_prepare_prompt
		command ash -- Test $argv
		return $status
	end
	builtin test $argv
end
function Type
	if not _ash_prompt_processing_enabled
		builtin type $argv
		return $status
	end
	if _ash_should_route Type $argv
		_ash_prepare_prompt
		command ash -- Type $argv
		return $status
	end
	builtin type $argv
end
function Time; _ash_route_or_delegate Time $argv; end

_ash_ensure_broker >/dev/null 2>&1
# <<< ash install <<<