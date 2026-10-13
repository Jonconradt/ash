#!/usr/bin/env bash
set -euo pipefail

# Test Ash's native package installation flow in a disposable VM.
#
# Usage: scripts/vm.sh [--platform macos|ubuntu|debian|fedora|rocky] [--tart-home DIR]
#        [--channel nightly|beta|release|stable]
#        [--mode automated|interactive] | --stop VM_NAME
# Use --tart-home to redirect Tart's local VM and image cache.

host_os="$(uname -s)"
case "$host_os" in
	Darwin) default_platform="macos" ;;
	Linux) default_platform="ubuntu" ;;
	*)
		echo "Unsupported host OS '$host_os'; vm.sh supports macOS and Linux hosts." >&2
		exit 1
		;;
esac

platform="${ASH_TEST_PLATFORM:-$default_platform}"
mode="${ASH_TEST_MODE:-automated}"
channel="beta"
tart_home_override=""
stop_name=""
while (($#)); do
	case "$1" in
		--platform | --os)
			if (($# < 2)); then
				echo "$1 requires macos, ubuntu, debian, fedora, or rocky." >&2
				exit 2
			fi
			platform="$2"
			shift 2
			;;
		--mode)
			if (($# < 2)); then
				echo "--mode requires automated or interactive." >&2
				exit 2
			fi
			mode="$2"
			shift 2
			;;
		--tart-home)
			if (($# < 2)) || [[ -z "$2" ]]; then
				echo "--tart-home requires a non-empty directory path." >&2
				exit 2
			fi
			tart_home_override="$2"
			shift 2
			;;
		--channel)
			if (($# < 2)); then
				echo "--channel requires nightly, beta, release, or stable." >&2
				exit 2
			fi
			case "$2" in
				nightly | beta | release) channel="$2" ;;
				stable) channel="release" ;;
				*)
					echo "Invalid channel '$2'; expected nightly, beta, release, or stable." >&2
					exit 2
					;;
			esac
			shift 2
			;;
		--stop)
			if (($# < 2)); then
				echo "--stop requires the VM name printed by interactive mode." >&2
				exit 2
			fi
			stop_name="$2"
			shift 2
			;;
		-h | --help)
			echo "Test Ash's native package installation flow in a disposable VM."
			echo "Usage: $0 [--platform macos|ubuntu|debian|fedora|rocky] [--tart-home DIR] [--channel nightly|beta|release|stable] [--mode automated|interactive]"
			echo "       $0 --stop VM_NAME"
			echo "Defaults follow the host: macOS -> macos; Linux -> ubuntu."
			echo "The package channel defaults to beta; stable is an alias for release."
			echo "ASH_TEST_PLATFORM, ASH_TEST_MODE, and backend-specific image variables can override defaults."
			echo "Tart stores local VMs and image cache under ~/.tart by default."
			echo "--tart-home sets TART_HOME for Tart commands and requires a macOS host."
			echo "Linux cloud-image URL override: ASH_TEST_QEMU_IMAGE=<URL>."
			echo
			echo "Host backends:"
			echo "  macOS            Tart; Apple Silicon required; can run macOS and Linux guests."
			echo "  Linux            QEMU/KVM; x86_64 host and accessible /dev/kvm required; Linux guests only."
			echo "                   Requires qemu-system-x86_64, qemu-img, cloud-localds, curl,"
			echo "                   ssh, ssh-keygen, and python3. Example packages:"
			echo "                   Debian/Ubuntu: qemu-system-x86 qemu-utils cloud-image-utils curl openssh-client python3"
			echo "                   Fedora/Rocky: qemu-kvm qemu-img cloud-utils curl openssh-clients python3"
			echo
			echo "Platforms:"
			echo "  macos             Homebrew on macOS Golden Gate"
			echo "  ubuntu, debian    APT on Ubuntu 24.04 or Debian Trixie"
			echo "  fedora, rocky     DNF on Fedora 42 or Rocky Linux 9"
			echo "  Linux guests use native package managers; Homebrew-on-macOS is Mac-host-only."
			echo
			echo "Modes:"
			echo "  automated         Run smoke tests and stop/delete the VM (default)."
			echo "  interactive       Leave the installed VM running for manual testing."
			echo "  --stop VM_NAME    Stop and remove a retained Linux/QEMU VM."
			echo
			echo "Arch Linux is not currently configured as a guest-image/package test target."
			echo "FreeBSD is not supported by either guest setup."
			exit 0
			;;
		*)
			echo "Unknown argument: $1" >&2
			echo "Usage: $0 [--platform macos|ubuntu|debian|fedora|rocky] [--tart-home DIR] [--channel nightly|beta|release|stable] [--mode automated|interactive]" >&2
			exit 2
			;;
	esac
done

state_root="${ASH_TEST_VM_STATE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/ash/vm}"
image_cache="${ASH_TEST_VM_CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/ash/vm-images}"
if [[ -n "$stop_name" ]]; then
	if [[ "$host_os" != "Linux" ]]; then
		echo "--stop is currently for retained Linux/QEMU VMs; stop Tart VMs with 'tart stop'." >&2
		exit 2
	fi
	if [[ ! "$stop_name" =~ ^ash-(ubuntu|debian|fedora|rocky)-test-[0-9]{14}-[0-9]+$ ]]; then
		echo "Invalid Linux VM name: $stop_name" >&2
		exit 2
	fi
	stop_dir="$state_root/$stop_name"
	pid_file="$stop_dir/qemu.pid"
	if [[ ! -f "$pid_file" ]]; then
		echo "No retained QEMU VM found for '$stop_name' under $state_root." >&2
		exit 1
	fi
	vm_pid="$(<"$pid_file")"
	if [[ ! "$vm_pid" =~ ^[0-9]+$ ]]; then
		echo "Invalid QEMU PID in $pid_file." >&2
		exit 1
	fi
	if kill -0 "$vm_pid" 2>/dev/null; then
		pid_command="$(ps -p "$vm_pid" -o args= 2>/dev/null || true)"
		if [[ "$pid_command" != *qemu-system-x86_64* || "$pid_command" != *"$stop_name"* ]]; then
			echo "PID $vm_pid is not the QEMU process for '$stop_name'; VM files were preserved." >&2
			exit 1
		fi
		kill "$vm_pid"
		for ((i = 0; i < 50; i++)); do
			kill -0 "$vm_pid" 2>/dev/null || break
			sleep 0.2
		done
		if kill -0 "$vm_pid" 2>/dev/null; then
			echo "QEMU process $vm_pid did not stop; VM files were preserved at $stop_dir." >&2
			exit 1
		fi
	fi
	rm -f "$stop_dir/disk.qcow2" "$stop_dir/seed.iso" "$stop_dir/user-data" \
		"$stop_dir/id_ed25519" "$stop_dir/id_ed25519.pub" "$stop_dir/qemu.pid" \
		"$stop_dir/qemu.log" "$stop_dir/serial.log"
	rmdir "$stop_dir"
	echo "Stopped and removed Linux VM $stop_name. The downloaded base image remains cached."
	exit 0
fi

if [[ "$mode" != "automated" && "$mode" != "interactive" ]]; then
	echo "Invalid mode '$mode'; expected automated or interactive." >&2
	exit 2
fi

if [[ -n "$tart_home_override" && "$host_os" != "Darwin" ]]; then
	echo "--tart-home can only be used with the Tart backend on macOS." >&2
	exit 2
fi

case "$platform" in
	macos)
		if [[ "$host_os" == "Linux" ]]; then
			echo "A Linux host cannot run the macOS guest. Select a Linux platform, or run this on an Apple Silicon Mac." >&2
			exit 2
		fi
		guest_name="macOS Golden Gate"
		vm_image="${ASH_TEST_VM_IMAGE:-${ASH_TEST_MACOS_IMAGE:-ghcr.io/cirruslabs/macos-golden-gate-base:latest}}"
		package_manager="brew"
		guest_shell="/bin/zsh"
		vm_image_url=""
		;;
	ubuntu)
		guest_name="Ubuntu 24.04"
		if [[ "$host_os" == "Linux" ]]; then
			vm_image_url="${ASH_TEST_QEMU_IMAGE:-https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img}"
		else
			vm_image="${ASH_TEST_VM_IMAGE:-ghcr.io/cirruslabs/ubuntu:24.04}"
			vm_image_url=""
		fi
		package_manager="apt"
		guest_shell="/bin/bash"
		;;
	debian)
		guest_name="Debian Trixie"
		if [[ "$host_os" == "Linux" ]]; then
			vm_image_url="${ASH_TEST_QEMU_IMAGE:-https://cloud.debian.org/images/cloud/trixie/latest/debian-13-generic-amd64.qcow2}"
		else
			vm_image="${ASH_TEST_VM_IMAGE:-ghcr.io/cirruslabs/debian:trixie}"
			vm_image_url=""
		fi
		package_manager="apt"
		guest_shell="/bin/bash"
		;;
	fedora)
		guest_name="Fedora 42"
		if [[ "$host_os" == "Linux" ]]; then
			vm_image_url="${ASH_TEST_QEMU_IMAGE:-https://download.fedoraproject.org/pub/fedora/linux/releases/42/Cloud/x86_64/images/Fedora-Cloud-Base-Generic-42-1.1.x86_64.qcow2}"
		else
			vm_image="${ASH_TEST_VM_IMAGE:-ghcr.io/cirruslabs/fedora:42}"
			vm_image_url=""
		fi
		package_manager="dnf"
		guest_shell="/bin/bash"
		;;
	rocky)
		guest_name="Rocky Linux 9"
		if [[ "$host_os" == "Linux" ]]; then
			vm_image_url="${ASH_TEST_QEMU_IMAGE:-https://dl.rockylinux.org/pub/rocky/9/images/x86_64/Rocky-9-GenericCloud.latest.x86_64.qcow2}"
		else
			vm_image="${ASH_TEST_VM_IMAGE:-ghcr.io/cirruslabs/rocky:9}"
			vm_image_url=""
		fi
		package_manager="dnf"
		guest_shell="/bin/bash"
		;;
	arch)
		echo "The published Pacman package is x86_64-only; Arch is not currently a supported VM test target." >&2
		exit 2
		;;
	freebsd)
		echo "FreeBSD is not supported as a Tart or QEMU guest by this script." >&2
		exit 2
		;;
	*)
		echo "Unsupported platform '$platform'; expected macos, ubuntu, debian, fedora, or rocky." >&2
		exit 2
		;;
esac

if [[ -n "$tart_home_override" ]]; then
	export TART_HOME="$tart_home_override"
fi

case "$channel" in
	release)
		brew_formula="ash"
		apt_distribution="stable"
		rpm_repository_url="https://www.versionx.ai/rpm/"
		;;
	beta)
		brew_formula="ash-beta"
		apt_distribution="beta"
		rpm_repository_url="https://www.versionx.ai/rpm/beta/"
		;;
	nightly)
		brew_formula="ash-nightly"
		apt_distribution="nightly"
		rpm_repository_url="https://www.versionx.ai/rpm/nightly/"
		;;
esac

if [[ "$host_os" == "Darwin" ]]; then
	if [[ "$(uname -m)" != "arm64" ]]; then
		echo "The Tart workflow requires an Apple Silicon host for native macOS and Linux guests." >&2
		exit 1
	fi
	if ! command -v tart >/dev/null 2>&1; then
		cat >&2 <<'EOF'
Tart is required to create and run the VM.

On an Apple Silicon Mac with Homebrew installed, run:
  brew install openai/tools/tart

If Homebrew is not installed on the host, install it from https://brew.sh first.
For other installation options and host requirements, see https://tart.run/.
EOF
		exit 1
	fi
	backend="tart"
else
	if [[ "$(uname -m)" != "x86_64" ]]; then
		echo "The Linux QEMU backend currently requires an x86_64 host and x86_64 guest images." >&2
		exit 1
	fi
	if [[ ! -r /dev/kvm || ! -w /dev/kvm ]]; then
		cat >&2 <<'EOF'
KVM is required and the current user must be able to read and write /dev/kvm.
Enable hardware virtualization in firmware and grant your user access to KVM
(commonly through the kvm group), then log in again.
EOF
		exit 1
	fi
	for required_command in qemu-system-x86_64 qemu-img cloud-localds curl ssh ssh-keygen python3; do
		if ! command -v "$required_command" >/dev/null 2>&1; then
			echo "Linux VM testing requires '$required_command'. Install QEMU/KVM, cloud-image-utils, curl, OpenSSH client, and Python 3." >&2
			exit 1
		fi
	done
	backend="qemu"
fi

vm_name="ash-${platform}-test-$(date -u +%Y%m%d%H%M%S)-$$"
vm_state_dir="$state_root/$vm_name"
vm_log="$(mktemp "${TMPDIR:-/tmp}/ash-package-vm.XXXXXX")"
ai_env_file="$(mktemp "${TMPDIR:-/tmp}/ash-package-ai-env.XXXXXX")"
chmod 600 "$ai_env_file"
vm_pid=""
vm_created=0
ai_variable_count=0
base_image=""
download_tmp=""
ssh_port=""
ssh_key=""
guest_user="ash-test"
ssh_options=()

for variable_name in $(compgen -e); do
	case "$variable_name" in
		ASH_AI*)
			printf '%s\0%s\0' "$variable_name" "${!variable_name}" >>"$ai_env_file"
			ai_variable_count=$((ai_variable_count + 1))
			;;
	esac
done

qemu_process_matches() {
	local process_args
	process_args="$(ps -p "$1" -o args= 2>/dev/null || true)"
	[[ "$process_args" == *qemu-system-x86_64* && "$process_args" == *"$2"* ]]
}

cleanup() {
	local status=$?
	trap - EXIT

	if [[ "$mode" == "interactive" && "$vm_created" -eq 1 ]]; then
		echo "Leaving interactive VM running: $vm_name"
		if [[ "$backend" == "tart" ]]; then
			echo "Open a guest shell with: tart exec -it $vm_name $guest_shell -l"
			echo "Stop and remove it later with: tart stop $vm_name && tart delete $vm_name"
		else
			printf 'Open a guest shell with: ssh -i %q -p %s -o UserKnownHostsFile=%q -o StrictHostKeyChecking=accept-new %s@127.0.0.1\n' \
				"$ssh_key" "$ssh_port" "$vm_state_dir/known_hosts" "$guest_user"
			printf 'Stop and remove it later with: %q --stop %s\n' "$0" "$vm_name"
			echo "VM state is stored under: $vm_state_dir"
		fi
	else
		if [[ "$backend" == "tart" ]]; then
			if [[ -n "$vm_pid" ]]; then
				if tart stop "$vm_name" >/dev/null 2>&1; then
					wait "$vm_pid" 2>/dev/null || true
				else
					echo "Warning: could not stop VM $vm_name; attempting to stop its runner." >&2
					kill "$vm_pid" 2>/dev/null || true
					wait "$vm_pid" 2>/dev/null || true
				fi
			fi
			if [[ "$vm_created" -eq 1 ]] && ! tart delete "$vm_name"; then
				echo "Warning: could not delete VM $vm_name." >&2
			fi
		else
			if [[ -n "$vm_pid" ]] && kill -0 "$vm_pid" 2>/dev/null; then
				if qemu_process_matches "$vm_pid" "$vm_name"; then
					kill "$vm_pid" 2>/dev/null || true
					for ((i = 0; i < 50; i++)); do
						kill -0 "$vm_pid" 2>/dev/null || break
						sleep 0.2
					done
				else
					echo "Warning: PID $vm_pid is no longer the QEMU process; VM files were preserved at $vm_state_dir." >&2
					vm_created=0
				fi
				if [[ "$vm_created" -eq 1 ]] && kill -0 "$vm_pid" 2>/dev/null; then
					echo "Warning: QEMU did not stop; VM files were preserved at $vm_state_dir." >&2
					vm_created=0
				fi
			fi
			if [[ "$vm_created" -eq 1 ]]; then
				rm -f "$vm_state_dir/disk.qcow2" "$vm_state_dir/seed.iso" \
					"$vm_state_dir/user-data" "$vm_state_dir/id_ed25519" \
					"$vm_state_dir/id_ed25519.pub" "$vm_state_dir/qemu.pid" \
					"$vm_state_dir/qemu.log" "$vm_state_dir/serial.log" \
					"$vm_state_dir/known_hosts"
				rmdir "$vm_state_dir" 2>/dev/null || true
			fi
		fi
	fi
	rm -f "$vm_log" "$ai_env_file"
	if [[ -n "$download_tmp" ]]; then
		rm -f "$download_tmp"
	fi
	return "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if [[ "$backend" == "tart" ]]; then
	echo "Cloning $guest_name image $vm_image (the first run downloads the VM image)..."
	tart clone "$vm_image" "$vm_name"
	vm_created=1

	echo "Starting $guest_name VM $vm_name..."
	if [[ "$mode" == "interactive" && "$platform" == "macos" ]]; then
		nohup tart run "$vm_name" >"$vm_log" 2>&1 </dev/null &
	else
		nohup tart run --no-graphics "$vm_name" >"$vm_log" 2>&1 </dev/null &
	fi
	vm_pid=$!
else
	mkdir -p "$vm_state_dir" "$image_cache"
	chmod 700 "$vm_state_dir"
	vm_created=1
	image_file="${vm_image_url##*/}"
	base_image="$image_cache/$image_file"
	if [[ ! -s "$base_image" ]]; then
		download_tmp="$base_image.part.$$"
		echo "Downloading $guest_name cloud image $vm_image_url..."
		curl -fL --retry 3 --output "$download_tmp" "$vm_image_url"
		mv "$download_tmp" "$base_image"
		download_tmp=""
	else
		echo "Using cached $guest_name cloud image: $base_image"
	fi
	vm_disk="$vm_state_dir/disk.qcow2"
	seed_iso="$vm_state_dir/seed.iso"
	user_data="$vm_state_dir/user-data"
	pid_file="$vm_state_dir/qemu.pid"
	ssh_key="$vm_state_dir/id_ed25519"
	ssh_port="${ASH_TEST_SSH_PORT:-$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')}"
	if [[ ! "$ssh_port" =~ ^[0-9]+$ ]] || ((ssh_port < 1 || ssh_port > 65535)); then
		echo "ASH_TEST_SSH_PORT must be an available TCP port between 1 and 65535." >&2
		exit 2
	fi
	ssh-keygen -q -t ed25519 -N "" -f "$ssh_key"
	chmod 600 "$ssh_key"
	cat >"$user_data" <<EOF
#cloud-config
users:
  - default
  - name: $guest_user
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    ssh_authorized_keys:
      - $(<"$ssh_key.pub")
ssh_pwauth: false
EOF
	chmod 600 "$user_data"
	cloud-localds "$seed_iso" "$user_data"
	qemu-img create -f qcow2 -F qcow2 -b "$base_image" "$vm_disk" >/dev/null
	ssh_options=(
		-i "$ssh_key"
		-p "$ssh_port"
		-o "UserKnownHostsFile=$vm_state_dir/known_hosts"
		-o StrictHostKeyChecking=accept-new
		-o BatchMode=yes
		-o ConnectTimeout=5
	)
	echo "Starting $guest_name QEMU/KVM VM $vm_name (SSH forwarded to 127.0.0.1:$ssh_port)..."
	qemu-system-x86_64 \
		-name "$vm_name" \
		-machine q35,accel=kvm \
		-cpu host \
		-smp 2 \
		-m 4096 \
		-drive "file=$vm_disk,if=virtio,format=qcow2" \
		-drive "file=$seed_iso,media=cdrom,readonly=on" \
		-netdev "user,id=net0,hostfwd=tcp:127.0.0.1:$ssh_port-:22" \
		-device virtio-net-pci,netdev=net0 \
		-display none \
		-serial "file:$vm_state_dir/serial.log" \
		-monitor none \
		-daemonize \
		-pidfile "$pid_file" \
		-D "$vm_state_dir/qemu.log"
	vm_pid="$(<"$pid_file")"
fi

guest_exec() {
	if [[ "$backend" == "tart" ]]; then
		tart exec "$vm_name" "$@"
	else
		local remote_command
		printf -v remote_command '%q ' "$@"
		ssh "${ssh_options[@]}" "$guest_user@127.0.0.1" "$remote_command"
	fi
}

ready=0
if [[ "$backend" == "tart" ]]; then
	deadline=$((SECONDS + 360))
else
	deadline=$((SECONDS + 900))
fi
while ((SECONDS < deadline)); do
	if guest_exec /usr/bin/true >/dev/null 2>&1; then
		ready=1
		break
	fi
	if ! kill -0 "$vm_pid" 2>/dev/null; then
		echo "The VM stopped before it became ready:" >&2
		tail -n 40 "$vm_log" >&2
		exit 1
	fi
	sleep 2
done
if [[ "$ready" -ne 1 ]]; then
	echo "Timed out waiting for $guest_name to become reachable." >&2
	if [[ "$backend" == "tart" ]]; then
		tail -n 40 "$vm_log" >&2
	else
		tail -n 40 "$vm_state_dir/qemu.log" >&2
	fi
	exit 1
fi

if [[ "$backend" == "qemu" ]]; then
	echo "Waiting for cloud-init to finish guest setup..."
	guest_exec "$guest_shell" -lc 'cloud-init status --wait'
fi

if [[ "$ai_variable_count" -gt 0 ]]; then
	echo "Forwarding $ai_variable_count exported ASH_AI* variable(s) to the guest configuration."
	ai_setup_script='
			set -euo pipefail
			umask 077
			encoded_file="$(mktemp)"
			trap '\''rm -f "$encoded_file"'\'' EXIT
			if [[ "$(uname -s)" == "Darwin" ]]; then
				base64 -D >"$encoded_file"
			else
				base64 -d >"$encoded_file"
			fi
			config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/ash"
			mkdir -p "$config_dir"
			env_file="$config_dir/.ash_env"
			while IFS= read -r -d "" name && IFS= read -r -d "" value; do
				if [[ ! "$name" =~ ^ASH_AI[A-Za-z0-9_]*$ ]]; then
					echo "Skipping invalid shell environment variable name: $name" >&2
					continue
				fi
				printf "export %s=%q\n" "$name" "$value" >>"$env_file"
			done <"$encoded_file"
			chmod 600 "$env_file"
		'
	if [[ "$backend" == "tart" ]]; then
		base64 <"$ai_env_file" |
			tart exec -i "$vm_name" /bin/bash -c "$ai_setup_script"
	else
		printf -v remote_command '%q ' /bin/bash -c "$ai_setup_script"
		base64 <"$ai_env_file" |
			ssh "${ssh_options[@]}" "$guest_user@127.0.0.1" "$remote_command"
	fi
else
	echo "No exported ASH_AI* variables found on the host; guest AI requests may need configuration."
fi

case "$package_manager" in
	brew)
		echo "Checking Homebrew in the guest..."
		guest_exec "$guest_shell" -lc \
			'if ! command -v brew >/dev/null 2>&1; then NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"; fi; brew --version'
		echo "Installing and testing Ash $channel from the Homebrew tap..."
		guest_exec "$guest_shell" -lc \
			"brew install Jonconradt/ash/$brew_formula && brew test Jonconradt/ash/$brew_formula && ash version"
		;;
	apt)
		echo "Installing Ash $channel from the signed APT repository..."
		guest_exec "$guest_shell" -lc '
			set -euo pipefail
			sudo apt-get update
			sudo apt-get install -y ca-certificates curl gnupg
			sudo install -d -m 0755 /etc/apt/keyrings
			curl -fsSL https://www.versionx.ai/apt/gpg.key |
				sudo gpg --dearmor --batch --yes -o /etc/apt/keyrings/versionx-keyring.gpg
			sudo chmod 0644 /etc/apt/keyrings/versionx-keyring.gpg
			printf "deb [signed-by=/etc/apt/keyrings/versionx-keyring.gpg] https://www.versionx.ai/apt %s main\n" "$1" |
				sudo tee /etc/apt/sources.list.d/versionx.list >/dev/null
			sudo apt-get update
			sudo apt-get install -y ash
			ash --version
		' ash-vm "$apt_distribution"
		;;
	dnf)
		echo "Installing Ash $channel from the signed-metadata DNF repository..."
		guest_exec "$guest_shell" -lc '
			set -euo pipefail
			sudo dnf install -y curl
			sudo rpm --import https://www.versionx.ai/rpm/gpg.key
			sudo tee /etc/yum.repos.d/versionx.repo >/dev/null <<EOF
[versionx]
name=VersionX ash repository
baseurl=$1
enabled=1
gpgcheck=0
repo_gpgcheck=1
gpgkey=https://www.versionx.ai/rpm/gpg.key
EOF
			sudo dnf install -y ash
			ash --version
		' ash-vm "$rpm_repository_url"
		;;
esac

if [[ "$mode" == "interactive" ]]; then
	echo "Running the site shell-setup command in the guest user account..."
	if [[ "$platform" == "macos" ]]; then
		integration_shell="zsh"
	else
		integration_shell="bash"
	fi
	guest_exec "$guest_shell" -lc \
		"env_file=\"\${XDG_CONFIG_HOME:-\$HOME/.config}/ash/.ash_env\"; if [[ -f \"\$env_file\" ]]; then source \"\$env_file\"; fi; ash install --shell $integration_shell --shell-integration </dev/null"
	echo "Installation completed; the VM is available for manual testing."
else
	echo "Running the site shell-setup step in a temporary guest home..."
	if [[ "$platform" == "macos" ]]; then
		integration_shell="zsh"
		integration_file=".ash_zshrc"
		syntax_check="zsh -n"
	else
		integration_shell="bash"
		integration_file=".ash_bashrc"
		syntax_check="bash -n"
	fi
	guest_exec "$guest_shell" -lc \
		"home=\$(mktemp -d) && HOME=\"\$home\" ash install --shell $integration_shell --shell-integration </dev/null && test -s \"\$home/.config/ash/$integration_file\" && $syntax_check \"\$home/.config/ash/$integration_file\"; exit_code=\$?; if [[ -n \"\${home:-}\" ]]; then rm -rf \"\$home\"; fi; exit \"\$exit_code\""
	echo "Native package installation smoke test passed."
fi
