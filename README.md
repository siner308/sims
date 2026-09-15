# sims

k9s-style terminal UI for Android emulators, iOS simulators, and the physical devices plugged in or on the same wifi.

```
 sims dev  android+ios
 devices
 <b> boot  <ctrl+k> shutdown  <ctrl+e> erase  <ctrl+d> delete  <a>/<enter> apps  <l> logs  <n> new  <w> wifi  <x> disconnect
┌ devices [5] ─────────────────────────────────────────────────────────────────────────────┐
│ PLATFORM  VIA   NAME             MODEL          RUNTIME      STATE      ID                │
│ android   usb   SM S928N         e1q            Android 15   Connected  R3CT40ABCDE       │
│ android   wifi  Pixel 7          panther        Android 14   Connected  192.168.0.23:5555 │
│ android   avd   Pixel_7_API_35                  API 35       Booted     Pixel_7_API_35    │
│ ios       wifi  jeonghyun.an     iPhone 14 Pro  iOS 27.0     Offline    BBBBC218-A593-... │
│ ios       sim   iPhone 17 Pro                   iOS 26.4     Shutdown   0728E045-9CAF-... │
└──────────────────────────────────────────────────────────────────────────────────────────┘
```

`VIA` says how the device is reached: `avd` and `sim` are virtual, `usb` and `wifi` are physical. Boot, shutdown, erase and delete apply to virtual devices only.

`LAST` is when the device was last booted (AVD: mtime of `emu-launch-params.txt`; simulator: `lastBootedAt`) or last connected (iOS device: `lastConnectionDate`). adb exposes no such record for Android phones, so they show `-`.

## Install

No toolchain needed; grab the binary for your machine.

```sh
# macOS / Linux: latest release into /usr/local/bin (or ~/.local/bin when that is not writable)
curl -fsSL https://raw.githubusercontent.com/siner308/sims/main/install.sh | sh

# pin a version, or choose the directory
SIMS_VERSION=v0.1.0 SIMS_INSTALL_DIR=$HOME/bin sh -c "$(curl -fsSL https://raw.githubusercontent.com/siner308/sims/main/install.sh)"
```

Windows: download `sims_windows_amd64.zip` (or `arm64`) from the [releases page](https://github.com/siner308/sims/releases), unzip, and put `sims.exe` on your `PATH`.

Manual download for any OS: every release ships `sims_<os>_<arch>.tar.gz` (`.zip` on Windows) plus `checksums.txt`.

With a Go toolchain (the module is public, so this works from any machine, mirrors included):

```sh
go install github.com/siner308/sims/cmd/sims@latest
```

`sims --version` prints the installed version, from the release build or from the module version `go install` fetched.

### What sims needs on the machine

Either platform is enough; the header shows what was found.

| Platform | Needs | How sims finds it |
|----------|-------|-------------------|
| android | `adb`, `emulator`, `avdmanager`, `sdkmanager`; `aapt2` (build-tools) for app names | `ANDROID_HOME` or `ANDROID_SDK_ROOT`, then the default SDK path (`~/Library/Android/sdk`, `%LOCALAPPDATA%\Android\Sdk`, `~/Android/Sdk`), then `PATH` |
| ios | Xcode command line tools (`xcrun simctl`, `xcrun devicectl`) | macOS only. Physical-device logs also need `idevicesyslog` (`brew install libimobiledevice`) |

## Keys

| Scope | Key | Action |
|-------|-----|--------|
| global | `:` | command bar (`:dev` `:apps` `:logs` `:img` `:connect HOST:PORT` `:pair HOST:PORT CODE`) |
| global | `?` / `esc` / `ctrl+c` | help / back / quit |
| global | `r` | refresh |
| devices | `b` | boot |
| devices | `ctrl+k` `ctrl+e` `ctrl+d` | shutdown; wipe data (factory reset, the device stays); delete the device itself |
| devices | `a` or `enter` / `l` | apps / log stream of the selected device. On a stopped virtual device `enter` asks to boot it first and opens apps once it is up |
| devices | `n` | new device (opens images) |
| devices | `w` / `x` | android: switch a USB device to adb over wifi / disconnect a wifi device. ios: open the wifi tunnel to a paired phone (`devicectl device info details`) |
| devices | `p` | ios: pair a physical device (`devicectl manage pair`) |
| devices, apps | `h` / `backspace` / `o` | send Home / Back / Overview to the device (android: `adb shell input keyevent`) |
| devices | `shift+P` `V` `N` `M` `R` `S` `L` | sort by platform, via, name, model, runtime, state, last; same key again flips direction. Default: state (running, offline, shutdown), then most recent; ties by name desc, runtime desc |
| apps | `enter` / `i` / `I` / `ctrl+u` | launch / install via the OS file dialog (Finder on macOS, Explorer on Windows; falls back to the TUI picker elsewhere) / install via the TUI picker / uninstall |
| picker | `enter` `backspace` `~` `d` `.` `t` `/` | open or pick, parent, home, Downloads, hidden files, type a path (tab completes), filter |
| apps | `s` / `/` | toggle preinstalled apps (hidden by default) / filter |
| logs | `/` `c` `p` `g` `G` | filter, clear, pause, top, bottom |
| images | `n` or `enter` / `i` | new device from image / install image (android) |

Anything that stops or removes something (shutdown, wipe, delete, uninstall) takes a ctrl chord so a stray key cannot fire it; wipe, delete and uninstall also ask for confirmation, and the prompt spells out what is lost. `ctrl+s` was avoided because terminals with XON/XOFF flow control on may swallow it before it reaches sims.

## Keyboard and nav keys on Android emulators

avdmanager writes `hw.keyboard = no` (the emulator default). With that setting the guest gets no keyboard input device at all (`adb shell getevent -pl` lists only `gpio-keys` and touch devices; with `yes` a `qwerty2` device appears), so typing from the host is dropped, and the toolbar's Back / Home / Overview buttons appear to go the same way. sims sets it to `yes` when it creates an AVD and again on every boot, so an AVD booted through sims gets a keyboard on its next start. The same pass sets `hw.gpu.enabled = yes`, `hw.camera.front = emulated`, and `PlayStore.enabled = yes` on `google_apis_playstore` images. RAM (`hw.ramSize`), heap (`vm.heapSize`) and `/data` size (`disk.dataPartition.size`) are left alone; edit `config.ini` per project.

## Wireless android

1. Plug the phone in over USB once, select it, press `w`. sims reads the wifi address, runs `adb tcpip 5555` and connects to `IP:5555`.
2. Or, with Android 11+ wireless debugging: `:pair 192.168.0.23:37099 123456` using the code the phone shows, then `:connect 192.168.0.23:5555`.

## Physical ios

Devices show up from `devicectl list devices` once they have been paired. Select an `Unpaired` one and press `p` to run `devicectl manage pair`; the phone shows a trust prompt.

A paired phone on the same wifi shows as `Offline` until a CoreDevice tunnel is open, and the tunnel is opened lazily by the first command that addresses the device. Select it and press `w`: sims runs `devicectl device info details --device <id>`, which brings the state to `Connected` in a few seconds. No Xcode project needed. Requirements on the phone: unlocked, Developer Mode on, same network as the Mac (or plugged in over USB).

## What it runs underneath

| Action | android | ios |
|--------|---------|-----|
| list | `emulator -list-avds` + `adb devices -l` + `adb emu avd name` | `simctl list devices --json` + `devicectl list devices` |
| boot | `hw.keyboard = yes` in `config.ini`, then `emulator -avd NAME` (detached) | `simctl boot UDID` + `open -a Simulator` |
| shutdown | `adb emu kill` | `simctl shutdown UDID` |
| erase | `emulator -avd NAME -wipe-data` | `simctl erase UDID` |
| delete | `avdmanager delete avd -n NAME` | `simctl delete UDID` |
| apps | `adb shell pm list packages -f -i` (+ `-3` to tell yours from preinstalled; `installer=` tells adb from store). Apps you installed get their display name and version from `aapt2 dump badging` on the pulled APK, cached under the user cache dir; preinstalled apps keep the package name | sim: `simctl listapps` (via `plutil`, `ApplicationType`), device: `devicectl device info apps` (+ `--include-all-apps`) |
| install / uninstall / launch | `adb install -r`, `adb uninstall`, `cmd package resolve-activity` + `am start -n` | sim: `simctl install/uninstall/launch`, device: `devicectl device install app / uninstall app / process launch` |
| logs | `adb logcat -v time` | sim: `simctl spawn UDID log stream`, device: `idevicesyslog -u UDID` |
| wifi / pair | `adb tcpip 5555`, `adb connect`, `adb pair`, `adb disconnect` | `devicectl manage pair`, `devicectl device info details` (opens the tunnel) |
| images | `sdkmanager --list` | `simctl list runtimes --json` |
| create | `avdmanager create avd -n -k -d`, then `hw.keyboard = yes` in `config.ini` | `simctl create NAME TYPE RUNTIME` |

iOS runtimes cannot be downloaded from sims. Use `xcodebuild -downloadPlatform iOS` and refresh.

## Develop

```sh
go build ./cmd/sims
go test ./... -race    # parsers run everywhere; provider and UI tests skip when the toolchain is absent
GOOS=windows go build ./cmd/sims
```

Releases are cut by tagging: `git tag v0.1.0 && git push origin v0.1.0` runs GoReleaser in GitHub Actions and publishes the archives and `checksums.txt` that `install.sh` downloads. GoReleaser releases to whichever repo runs the workflow, so a mirror that receives the tag gets its own release.
