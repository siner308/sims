---
name: sims-cli
description: >
  Drive Android emulators, iOS simulators and physical phones from the shell with the `sims` CLI
  instead of raw adb, emulator, avdmanager, sdkmanager, xcrun simctl or xcrun devicectl. Use this
  skill whenever a task touches a mobile device from the command line: listing or booting an
  emulator or simulator, creating or deleting one, wiping it, installing an .apk or .app, launching
  an app, tailing device or app logs, pairing or connecting a phone over wifi, or checking the
  Android SDK and Xcode toolchain. Trigger even when the user names the underlying tool ("run adb
  install", "boot the simulator with simctl", "create an AVD") or a device by name ("Pixel_7",
  "iPhone 16", "my phone"), as long as `sims` is installed or can be installed.
---

# sims

`sims` is one command over the Android and iOS device tools. Every subcommand addresses a device by name or id, takes `--json`, exits 0 or 1, and never asks a question, so it fits a script or an agent better than the tools underneath. This file covers the whole command surface.

Check it is there with `command -v sims`. If not, install with `curl -fsSL https://raw.githubusercontent.com/siner308/sims/main/install.sh | sh` (macOS and Linux; Windows takes the zip from the releases page). `sims doctor` reports which of adb, emulator, avdmanager, sdkmanager, aapt2, xcrun simctl, devicectl and idevicesyslog it found.

A bare `sims` opens an interactive terminal UI, so always give a subcommand.

## How to work

1. **List first, then act by id.** Run `sims device list --json` (`-p android` or `-p ios` narrows it) and pick the `id`. Lookup by `name` is an exact, case-insensitive match: `pixel` does not find `Pixel_7`. When a name matches two devices the command fails and prints both ids.
2. **Read state before acting.** A device is usable when `state` is `Booted` (virtual) or `Connected` (physical). `app *`, `logs` and `key` fail with `device is not running` on a `Shutdown` device, so boot it first with `--wait`.
3. **Pass `--json` and parse it** rather than reading the table. The record shapes are below.
4. **Ask before `erase`, `delete` and `uninstall`.** sims does not confirm; these act at once. Say what will be lost and wait for the user unless they asked for exactly that.
5. **Bound anything that streams or waits.** `device logs` and `app logs` run until killed, so run them under `timeout 30 ...` (GNU coreutils; on macOS `brew install coreutils`) or in the background with output redirected to a file and kill them after. `timeout` exits 124 when it cuts the stream, which is the normal end of a capture, so a script under `set -e` treats 124 as success. `boot --wait`, `wait` and `create --boot` block up to `--timeout` (default 3m). `image install` takes minutes. `device pair <iphone>` waits up to 2 minutes for the user to tap Trust on the phone, so tell them before running it.
6. **On any failure read stderr.** The reason follows `sims:`; the table at the end maps the common ones to the next step.

## Commands

Aliases: `device` = `devices` = `dev`, `app` = `apps`, `image` = `images` = `img`, `device-type` = `device-types` = `types`, every `list` = `ls`. Global flags: `--json` and `--platform android|ios` (`-p`). `<device>` is an id (AVD name or simulator UDID), an adb serial, or an exact name.

```sh
sims device list [--all] [--json]           # --all includes never-booted simulators (hidden by default)
sims device get <device>
sims device boot <device> [--wait] [--timeout 3m]
sims device wait <device> [--timeout 3m]    # until Booted or Connected
sims device shutdown <device>
sims device erase <device>                  # factory reset; needs Shutdown first; no confirmation
sims device delete <device>                 # remove an AVD/simulator; on a phone: ios unpairs, android drops wifi
sims device create <name> --image <image> [--type <type>] [--ram MB --cores N --disk GB] [--boot] [--timeout 3m]
sims device hardware <device> [--ram MB --cores N --disk GB]   # AVD only; no flags prints current values
sims device key <device> home|back|overview # android only
sims device screenshot <device> [path]      # PNG; no path names it after the device and the time, - writes to stdout
sims device reboot <device> [--wait] [--timeout 3m]   # simulator: shutdown and boot again
sims device logs <device> [--app <bundle-or-name>]
sims device connect <device>                # iPhone: open wifi tunnel; USB Android phone: switch to adb over wifi
sims device connect <host:port>             # adb connect (20s limit)
sims device pair <device>                   # iPhone over USB; user must accept the prompt on the phone
sims device pair <host:port> <code>         # Android 11+ wireless debugging
sims device disconnect <device>             # adb disconnect

sims app list <device> [--all]              # --all includes system/preinstalled apps; running apps sort first
sims app install <device> <path>            # .apk (android) or .app bundle (ios simulator)
sims app uninstall <device> <bundle-id>     # no confirmation
sims app launch <device> <bundle-id>
sims app logs <device> <bundle-id-or-name>  # android: the app must already be running

sims image list [--all]                     # installed system images and simulator runtimes; --all adds downloadable
sims image install <image>                  # android only; iOS runtimes: xcodebuild -downloadPlatform iOS
sims device-type list [--image <image>]     # hardware profiles; --image keeps only those that run it

sims doctor                                 # toolchain check, exit 1 if something is missing
sims update [--check]                       # also refreshes installed copies of this skill
sims skill [install [--dir <skills-dir>] [--refresh]]   # print this file / install it for the agents on this machine
```

`--image` and `--type` accept an id or name from the matching `list`, but Android image names repeat across API levels (`google_apis_playstore arm64-v8a` exists for 30, 31, 35 and 36), so pass the Android image id. Without `--type` sims picks `pixel_7` on Android and `iPhone 17 Pro` on iOS, falling back to the newest iPhone or the first type that runs the image. `--ram`, `--cores` and `--disk` are refused on iOS. `--help` on any subcommand prints the exact flags of the installed version.

## JSON shapes

`device list`, `device get` and every device action print this record. Actions print the device they acted on; `boot --wait` and `wait` print the refreshed record, so `state` and `serial` are current. Fields marked * are omitted when empty.

| Field | Values |
|---|---|
| `id` | AVD name (android) or simulator/device UDID (ios). Stable; use this. |
| `name` | Display name; the exact-match lookup key. |
| `platform` | `android`, `ios` |
| `kind` | `virtual`, `physical` |
| `transport` | `avd`, `sim` (virtual); `usb`, `wifi` (physical) |
| `state` | `Booted`, `Booting`, `Shutting Down`, `Shutdown`, `Unknown` (virtual); `Connected`, `Offline`, `Unauthorized`, `Unpaired` (physical). An Android emulator stays `Booting` until `sys.boot_completed`, so `Booted` means it will accept an install. |
| `model` * | e.g. `iPhone 14 Pro`; empty for most AVDs |
| `runtime` * | `API 36`, `iOS 26.5` |
| `serial` * | adb serial while an Android device is reachable (`emulator-5554`, wifi `host:port`) |
| `lastActiveAt` * | RFC 3339; last boot or last connection |

`app list`: `bundleId`, `name`, `version`*, `system` (bool), `running` (bool), `source`* (`preinstalled`, `store`, `adb`, `simctl`, `process`), `process`*. Running apps sort first, then by name.

On a physical iPhone devicectl often lists no apps at all, even unlocked with apps plainly running. sims falls back to the running processes there, which is why those entries carry `"source": "process"`, a name and no `bundleId`. Commands that need a bundle id do not work on them.

`image list`: `id` (`system-images;android-36;google_apis_playstore;arm64-v8a` or `com.apple.CoreSimulator.SimRuntime.iOS-26-5`), `name`, `version`* (`36`, `26.5`), `installed`, `os`*, `platform`.

`device-type list`: `id` (`pixel_7`, `com.apple.CoreSimulator.SimDeviceType.iPhone-16`), `name`, `screen`*, `minRuntime`*, `maxRuntime`*, `family`*, `platform`.

`device hardware`: `ramMb`, `cores`, `diskGb`.

`connect` and `pair` by address: `{"address": "host:port"}`. `doctor`, `update` and the log streams ignore `--json`.

## Recipes

Boot, install, launch, follow the log for a minute:

```sh
ID=$(sims device list --json -p android | jq -r '.[] | select(.state=="Shutdown") | .id' | head -1)
sims device boot "$ID" --wait
sims app install "$ID" app/build/outputs/apk/debug/app-debug.apk
sims app launch "$ID" com.example.app
timeout 60 sims app logs "$ID" com.example.app > app.log || [ $? -eq 124 ]
```

Look at what is on screen. A screenshot is the way to check an app that draws its own interface, since a Unity or Flutter canvas puts no text in the accessibility tree:

```sh
sims device screenshot "$ID" /tmp/screen.png   # then read the image
sims device screenshot "$ID" - | <another command>
```

Find a running device on either platform:

```sh
sims device list --json | jq -r '.[] | select(.state=="Booted" or .state=="Connected") | "\(.platform) \(.id)"'
```

Create an emulator for an API level and boot it:

```sh
sims image list --json -p android | jq -r '.[] | select(.version=="35") | .id'   # empty? sims image list --all, then sims image install <id>
sims device-type list --json -p android | jq -r '.[].id' | grep -i pixel
sims device create Pixel_8_API_35 --image "system-images;android-35;google_apis_playstore;arm64-v8a" --type pixel_8 --boot
```

Create a simulator for a runtime (sims cannot install iOS runtimes):

```sh
sims image list --json -p ios                          # pick a runtime id or "iOS 26.5"
sims device-type list --json --image "iOS 26.5" | jq -r '.[].name'
sims device create "QA iPhone" --image "iOS 26.5" --type "iPhone 16"
```

Put a USB Android phone on wifi, or connect one that already has wireless debugging on:

```sh
sims device connect <serial>                 # switches the USB phone to adb over tcp, prints host:port
sims device pair 192.168.0.12:37123 482913   # pairing port and code from the phone's Wireless debugging screen
sims device connect 192.168.0.12:5555        # the connect port, not the pairing port
```

## When something fails

Exit status is 1 and the reason is on stderr after `sims:`. A usage mistake adds a `see 'sims ... --help'` line; a device that is not found does not. A listing whose one platform failed still prints the other and reports the failure on stderr, so check stderr even on exit 0.

| stderr says | Do |
|---|---|
| `no device matches "x"` | `sims device list --json` (with `--all` for never-booted simulators); names match exactly, so pick the `id`. |
| `"x" matches N devices, use the id: ...` | Rerun with one of the listed ids. |
| `device is not running` | `sims device boot <id> --wait`, or for a phone `sims device connect <id>`. |
| `devicectl cannot capture a physical device's screen` | Screenshots work on emulators and simulators only. |
| `<platform> cannot reboot a device from here` / `... capture a screen from here` | The platform has no such command; `sims device shutdown` then `boot` is the fallback. |
| `<name> did not finish booting within 3m0s` | `sims device wait <id> --timeout 5m`; the first boot of a fresh image is slow. |
| `shut down the device before erasing` / `... before deleting` | `sims device shutdown <id>` first. |
| `<bundle> is not running; launch it first` | Android app logs filter by pid: `sims app launch` first, or use `sims device logs <id>` unfiltered. |
| `<bundle> has no launcher activity` | The package is a library or service; nothing to launch. |
| `<image> is not installed; run sims image install ...` | Android: run that. iOS: `xcodebuild -downloadPlatform iOS`, then retry. |
| `no device type runs <image>; pass --type` | `sims device-type list --image <image>` and pick one. |
| `ios cannot send <key> from here` / `<name> has no editable hardware` | Android-only feature; on iOS use the simulator window or Xcode. |
| `pair the device first` / state `Unpaired` | `sims device pair <id>` with the iPhone on USB; the user taps Trust. |
| state `Unauthorized` | The Android phone shows an "Allow USB debugging" prompt; the user must accept it. |
| `missing adb` / `xcrun not found` / `idevicesyslog not found` | `sims doctor` for the full picture; install what it lists (`brew install libimobiledevice` for iPhone logs). |
| `this build cannot update itself` | Installed with `go install`; rerun the install line or `go install ...@latest`. |
