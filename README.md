<p align="center">
  <img src="docs/img/logo.png" alt="sims logo" width="220">
</p>

<h1 align="center">sims</h1>

<p align="center">Android emulators, iOS simulators and real phones, in one k9s-style terminal UI and one command line.</p>

<p align="center">
  <a href="https://github.com/siner308/sims/actions/workflows/ci.yml"><img src="https://github.com/siner308/sims/actions/workflows/ci.yml/badge.svg" alt="ci"></a>
  <a href="https://github.com/siner308/sims/releases"><img src="https://img.shields.io/github/v/release/siner308/sims?include_prereleases&sort=semver" alt="release"></a>
  <a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/siner308/sims" alt="go version"></a>
  <img src="https://img.shields.io/badge/platforms-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey" alt="platforms">
</p>

**sims** is a terminal UI for the devices a mobile developer keeps around: Android emulators, iOS simulators, the phones plugged in over USB or sitting on the same wifi, and the machine you are sitting at. It wraps `adb`, `emulator`, `avdmanager`, `sdkmanager`, `xcrun simctl` and `xcrun devicectl` behind one k9s-style screen, so booting, installing a build, tailing logs or wiping a device is a keystroke instead of a command you have to remember.

<p align="center">
  <img src="docs/img/devices.svg" alt="devices view" width="100%">
</p>

## Why

Android Studio and Xcode both ship a device manager, and both take a while to open when all you want is "boot that emulator and put this APK on it". The command line tools underneath are scattered across two SDKs with different argument styles. sims puts them on one screen with the same keys for both platforms, and adds what the GUI managers leave out: which phones are reachable right now, how they are connected, and when each device was last used.

The same operations are subcommands, so a script or a CI job addresses a device by name and never learns which tool sits underneath: `sims device boot Pixel_7 --wait`, `sims app install Pixel_7 app.apk`, `sims device list --json`. See [Command line](#command-line).

## Install

No toolchain needed; grab the binary for your machine.

```sh
# macOS / Linux: the latest release into /usr/local/bin (or ~/.local/bin when that is not writable)
curl -fsSL https://raw.githubusercontent.com/siner308/sims/main/install.sh | sh
```

The script downloads through GitHub's `releases/latest/download` redirect, so this line never needs updating and it does not touch the rate-limited API. Two optional knobs: `SIMS_VERSION=<tag>` installs a specific release and `SIMS_INSTALL_DIR=<dir>` picks the directory.

```sh
SIMS_VERSION=<tag> SIMS_INSTALL_DIR="$HOME/bin" sh -c "$(curl -fsSL https://raw.githubusercontent.com/siner308/sims/main/install.sh)"
```

Windows: download `sims_windows_amd64.zip` (or `arm64`) from the [releases page](https://github.com/siner308/sims/releases), unzip, and put `sims.exe` on your `PATH`.

Every release ships `sims_<os>_<arch>.tar.gz` (`.zip` on Windows) plus `checksums.txt`, which `install.sh` verifies.

With a Go toolchain (the module is public, so this works from any machine, mirrors included):

```sh
go install github.com/siner308/sims/cmd/sims@latest
```

`sims --version` prints the installed version, from the release build or from the module version `go install` fetched.

### Updating

`sims update` downloads the latest release for this machine, verifies it against `checksums.txt` and replaces the running binary in place (`sims update --check` only reports). When the TUI starts, sims looks up the latest release in the background and, when it is newer, shows it under the logo and offers `:update` from the command bar; set `SIMS_NO_UPDATE_CHECK=1` to skip that lookup. The subcommands never look, so a script pays nothing for it. The check reads the `releases/latest` redirect, so it does not touch the rate-limited API.

A sims older than v0.1.2 has no `update` command; run the install line above again and it overwrites the binary in place (`SIMS_INSTALL_DIR` if it went somewhere other than the default). `go install github.com/siner308/sims/cmd/sims@latest` does the same for a `go install` build.

### What sims needs on the machine

`sims doctor` probes every tool below, printing each line as it finishes (the first `xcrun` call after installing or updating Xcode can take a minute), and ends with a copy-paste block of the commands that install whatever is missing; `install.sh` runs it at the end. Either platform is enough; the header shows what was found.

```
$ sims doctor
sims v0.1.0
android
  ok       Android SDK    /Users/me/Library/Android/sdk
  ok       adb            /Users/me/Library/Android/sdk/platform-tools/adb (36.0.0)
  ok       emulator       /Users/me/Library/Android/sdk/emulator/emulator
  MISSING  avdmanager     command line tools missing
  MISSING  sdkmanager     command line tools missing
  skip     aapt2          optional: build-tools missing; app names fall back to package ids
ios
  ok       xcrun          /usr/bin/xcrun
  ok       Xcode          26.6
  ok       simctl         xcrun simctl
  ok       devicectl      xcrun devicectl
  skip     idevicesyslog  optional: needed only for logs from physical iPhones

to install what is missing:
# avdmanager
unzip Google's commandlinetools zip into /Users/me/Library/Android/sdk/cmdline-tools/latest   # or: brew install --cask android-commandlinetools (then pass --sdk_root=/Users/me/Library/Android/sdk to sdkmanager)
# aapt2
sdkmanager --sdk_root=/Users/me/Library/Android/sdk "build-tools;35.0.0"
# idevicesyslog
brew install libimobiledevice
```

The `sdkmanager` lines carry `--sdk_root` because `sdkmanager` installs into the SDK that contains the binary, not into `ANDROID_HOME`; the Homebrew cask keeps it under the brew prefix, so without the flag packages land there. sims passes the same flag when it lists or installs system images, so with the SDK on another disk (`ANDROID_HOME=/Volumes/data/Android/sdk`, and `ANDROID_AVD_HOME=/Volumes/data/Android/avd` for the AVDs) packages and AVDs land there too.

| Platform | Needs | How sims finds it |
|----------|-------|-------------------|
| android | `adb`, `emulator`, `avdmanager`, `sdkmanager`; `aapt2` (build-tools) for app names | `ANDROID_HOME` or `ANDROID_SDK_ROOT`, then the default SDK path (`~/Library/Android/sdk`, `%LOCALAPPDATA%\Android\Sdk`, `~/Android/Sdk`), then `PATH` |
| ios | Xcode command line tools (`xcrun simctl`, `xcrun devicectl`) | macOS only. Physical-device logs also need `idevicesyslog` (`brew install libimobiledevice`) |

## Tour

### Devices

Everything in one table: `VIA` says how a device is reached (`avd` and `sim` are virtual, `usb` and `wifi` are physical), `STATE` is colored, `LAST` is the last boot or last connection. Simulators that have never been booted (a fresh Xcode lists dozens) stay hidden until you press `s`; the title shows how many. Running devices sort first, then the most recently used. `shift+letter` sorts by a column; the same key again flips the direction.

`enter` opens the apps of the selected device. On a stopped virtual device it asks to boot it first and opens apps once it is up.

### Apps

<p align="center">
  <img src="docs/img/apps.svg" alt="apps view" width="100%">
</p>

Apps you installed come first with their display name and version; preinstalled apps are hidden until you press `s`. `SOURCE` tells `adb` sideloads from `store` installs. `i` opens the OS file dialog (Finder, Explorer) to pick an `.apk` or `.app`; `shift+i` opens the built-in file picker instead.

### Logs

<p align="center">
  <img src="docs/img/logs.svg" alt="logs view" width="100%">
</p>

Live `logcat` or `log stream` with a substring filter, pause, clear, and a wrap toggle for long lines. From the apps view, `l` narrows the stream to the selected app (`logcat --pid` on Android, `log stream --predicate` on iOS). Works for simulators and for physical devices (`idevicesyslog` on iOS).

### Traffic

Press `t` on a device and sims becomes the proxy it talks to. An exchange is one line, carrying its
status, size, timing, method and url, and it fills in as the response arrives. `enter` opens one in
full, with headers and a pretty-printed body, `/` filters, `s` writes a HAR file that Proxyman,
Charles or the browser dev tools can read, and `ctrl+k` stops the capture and puts every setting
back.

Opening HTTPS needs the device to trust a certificate sims signs with, and sims installs it as part of starting the capture: `simctl keychain add-root-cert` on a simulator, an `adb push` into the system trust store on an emulator that allows it, and on an iPhone a configuration profile served from the capture's own port, since neither devicectl nor libimobiledevice can install one.
What a phone will only let its owner do, sims lists as numbered taps before the stream opens, and does the parts a Mac can reach: on an iPhone it opens the profile's URL in the phone's Safari (`devicectl device process launch --payload-url`), and once the phone has fetched it offers to open Settings there, leaving Allow, Install and the switch under Certificate Trust Settings to the owner; on an Android phone without root the certificate is put in Downloads and picked from Settings, which is the only place Android 11 and later accept a CA certificate. On an emulator whose image takes `adb root` (the Play Store images refuse it) sims writes the system store itself and nothing is left to tap.
That happens once per phone: the profile names a fixed port (9797), sims records what the phone was given, and every later capture on the same wifi with the same certificate finds it already there and installs nothing. The profile stays after a capture, because nothing on the Mac can remove it, so the phone keeps sending its traffic here: the TUI relays it untouched while open, `sims proxy standby <phone>` does the same from a terminal, and `--setup` sends the profile again when the phone lost it or moved to another wifi.
The certificate is made once and kept, so the second capture on a device needs no setup at all.

The machine sims runs on is in the list too, as a `desktop` device. `enter` opens what is running
here and what is installed, with the process name that ties each app to a row in a capture, and
`enter` there launches one. `l` on one of them follows that app's own log, the way it does for a
simulator. It boots nothing and installs nothing, and says so in a line when asked, but its traffic
and its system log are the same two things sims shows for everything else: `t` captures what the apps on this Mac are sending, with each row named
by the process behind it. Trusting the certificate there is a command rather than part of starting a
capture, because macOS puts up an authorisation panel: `sims proxy ca localhost --install`. The
capture reads the keychain and only asks when the root is missing, so a machine already set up is
not sent to run it again. Windows lists the same way and trusts through `certutil -user`.

A capture puts every setting back when it stops, including on ctrl+c or a SIGTERM. When it cannot,
because it was killed outright or the machine lost power, the record it wrote before changing
anything is still there: the next capture, the next `sims` start and `sims proxy clean` each read
those records and undo the rest, so a machine is never left pointing at a proxy that is gone. Each
capture keeps its own record, so several running at once do not erase each other's.

`g` groups the table by domain, which is the handle left when a device will not say which app sent
what. Each domain carries how many exchanges it holds, how much came back, and how many answered
4xx/5xx, errored outright or could not be opened, so a service in trouble shows without being
unfolded. `space` folds one domain, `shift+g` folds or unfolds them all, and the list keeps the most
recently active domain at the top.

What sims could not read is still listed rather than hidden. An app that pins its certificate refuses
every proxy, sims included; those rows show as `tunnel` and say why, which is the difference between
a limit and a bug.

The log and the traffic are two layers of one stream, each toggled on its own: `t` adds or removes
the traffic, `l` the log. Start from either, `t` on a device for the traffic or `l` for the log, and
press the other key to bring in the second layer. They run in time order, so each request lands
between the log lines around it and "the app logged this, then sent that, and got a 401 back" reads
top to bottom. Both platforms' log timestamps are read for the ordering, and a line sims cannot date
keeps the place it arrived in. The last remaining layer stays on, since an empty stream says
nothing. `shift+t` on a device opens the same capture as a table instead, which groups by domain and
sorts. `ctrl+k` stops the capture and leaves the log running.

From the apps view, `t` opens the device's traffic and remembers which app was selected, so `l`
there adds that app's log rather than the whole device's. The two layers have different scopes and
the screen says so: the log is that app's, the traffic is everything the device sends. A device's
connections do not carry the app that opened them, so a request is never labelled with the app whose
log is on screen. Where sims can see the sender, on a
simulator or on this machine, the row carries its process name.

In that stream the arrows step between exchanges, and the newest is selected as it arrives so a key
always has something to act on. `o` opens the one selected in place: once for its headers, again for
its body, a third time to fold it away. `shift+o` opens every exchange at once, for reading a whole
conversation. `f` holds the view still, for reading something while requests keep landing.

`enter` opens the exchange on a page of its own, which `esc` closes: `/` finds, `n` and `shift+n`
step the matches. `enter` there hands the same text to `$PAGER`. `e` opens it in a windowed editor,
chosen the first time from what this machine has registered to open a file and remembered after
that, with `shift+e` to pick another. `v` opens the response body in whatever opens that kind of
file, so an image or a video is something to look at rather than a byte count.

```
12:04:01.220  I/MyApp  ( 1234): tapped sign in
12:04:01.244  200     66 B   146ms POST    https://api.example.com/v1/login
              REQUEST
              Authorization: Bearer eyJhbGciOiJIUzI1NiJ9
              Content-Type: application/json
              body 43 B
              {
                "email": "kim@example.com",
                "remember": true
              }
              RESPONSE
              Cache-Control: no-store
              body 66 B
              {
                "token": "eyJ0eXAi",
                "expiresIn": 3600
              }
12:04:01.402  I/MyApp  ( 1234): token stored
```

### Images and new devices

<p align="center">
  <img src="docs/img/images.svg" alt="images view" width="100%">
</p>

Installed system images and iOS runtimes by default; `s` adds everything `sdkmanager` can still download. `enter` on an installed image opens a form and creates the AVD or simulator: name, device profile with its screen size where the SDK knows it (`pixel_7  1080x2400`, `iPhone 17 Pro  1206x2622 @3x`), and for Android also RAM, CPU cores and disk. Those three can be changed later from the devices view with `e`. Android images install from here with `i`; iOS runtimes come from `xcodebuild -downloadPlatform iOS`.

### Help

<p align="center">
  <img src="docs/img/help.svg" alt="help view" width="100%">
</p>

`?` lists every key for the current view, plus commands and navigation.

## Command line

A bare `sims` opens the TUI; anything else is a command over the same layer the TUI uses, so the two never disagree about what a device is or how to reach it. Two flags work everywhere: `--json` prints the record instead of a table or a confirmation line (a device after `boot --wait` is the booted record, serial included; `connect` and `pair` by address print the address; `doctor`, `update` and the log streams ignore it), and `--platform android|ios` (`-p`) narrows a listing or a lookup to one platform. `device` is also `devices` or `dev`, `app` is `apps`, `image` is `images` or `img`, `device-type` is `types`, and every `list` is also `ls`. `sims completion zsh|bash|fish|powershell` prints a completion script.

A device is addressed by id (AVD name, simulator UDID), adb serial, or name, case-insensitive. An exact id wins over a serial, a serial over a name, and two devices sharing the winning match is an error that lists both, so pass the id.

```sh
sims device list [--all] [--json]           # --all includes simulators that were never booted
sims device get <device>
sims device boot <device> [--wait] [--timeout 3m]
sims device wait <device> [--timeout 3m]    # until it reports Booted or Connected
sims device shutdown | erase | delete <device>   # delete on a phone forgets it: ios unpairs, android drops the wifi connection
sims device create <name> --image <image> [--type <type>] [--ram MB --cores N --disk GB] [--boot] [--timeout 3m]
sims device hardware <device> [--ram MB --cores N --disk GB]   # AVD only; no flags prints the current values
sims device key <device> home|back|overview # android only; a simulator takes keys in its own window
sims device screenshot <device> [path]      # PNG; no path names it after the device and the time, - writes it to stdout
sims device reboot <device> [--wait] [--timeout 3m]   # a simulator shuts down and boots again, since simctl has no reboot
sims device logs <device> [--app <bundle>]
sims device connect <device>                # iPhone: open the wifi tunnel (idle drops it to Offline, and any command reopens it); USB Android phone: switch to adb over wifi
sims device connect <host:port>             # adb connect
sims device pair <device>                   # iPhone: devicectl manage pair (accept the prompt on the phone)
sims device pair <host:port> <code>         # Android 11+ wireless debugging
sims device disconnect <device>             # adb disconnect

sims app list <device> [--all]              # --all includes preinstalled apps; running apps sort first
sims app install <device> <path>            # .apk on android; .app or .ipa on a simulator; .app on an iPhone. A device-built ipa is refused for a simulator, which installs one and then cannot launch it
sims app uninstall | launch <device> <bundle>
sims app logs <device> <bundle>             # android: the app must be running (logcat --pid)

sims proxy run <device> [--port N] [--har out.har] [--for 30s] [--all] [--quiet] [--ssid <wifi>] [--setup] [--json]
sims proxy ca [device] [--install]          # print the root certificate, or trust it on a device
sims proxy standby <phone>                  # relay an iPhone's traffic untouched while no capture runs
sims proxy clean                            # put back what a killed capture left behind

sims image list [--all]                     # --all includes what sdkmanager can still download
sims image install <image>                  # android; iOS runtimes come from xcodebuild -downloadPlatform iOS
sims device-type list [--image <image>]     # --image keeps only the types that can run it

sims doctor
sims update [--check]
```

`--json` prints a record per exchange with its headers and both bodies, decoded where the server
compressed them; a body that is not text comes back base64 with `requestBodyEncoding` or
`responseBodyEncoding` saying so. `--max-body` bounds how much of each is kept.

`proxy run` prints one line per exchange and holds until ctrl+c (or `--for`), then puts the device and
this machine back as they were. `--har` writes the flows in HAR 1.2. A simulator has no network
settings of its own and follows this Mac's, so its capture points the Mac's web proxy at sims; other
apps on the Mac keep working, because traffic that is not the device's is relayed untouched and never
captured. `--all` widens that to everything the proxy receives. The root certificate lives in the user
cache directory and is reused, so `proxy ca --install` is a one-off per device.

`SIMS_PROXY_DIR` moves the root certificate, its key and the in-flight records somewhere other than the user cache directory.

`--image` and `--type` take an id or a name from the matching `list`, and the image must be installed (`sims image install` for Android). Without `--type`, Android takes `pixel_7` and iOS takes `iPhone 17 Pro`; when the SDK has neither, the first iPhone simctl lists (its newest), else the first type that can run the image. `--ram`, `--cores` and `--disk` are refused on iOS. `connect` and `pair` by address give adb 20 seconds, because `adb connect` blocks for over a minute on an unreachable host.

Exit status is 0 on success and 1 on any failure, with the reason on stderr; an argument or flag mistake adds a `--help` hint, a device that is not found does not. A listing whose one platform failed still prints the other and reports the failure on stderr, so a broken adb does not hide the simulators. Nothing asks for confirmation: `erase`, `delete` and `uninstall` act at once, the way `adb` and `simctl` do, and the TUI keeps its ctrl chords and prompts.

A script that boots an emulator, installs a build, launches it and follows its log:

```sh
sims device boot Pixel_7 --wait
sims app install Pixel_7 app/build/outputs/apk/debug/app-debug.apk
sims app launch Pixel_7 com.example.app
sims app logs Pixel_7 com.example.app
```

### AI agents

The binary carries a skill that teaches an AI coding agent the command line above: list and act by id, parse `--json`, bound the log streams, ask before `erase`, `delete` and `uninstall`, and what each message on stderr means. `install.sh` installs it for the agents it finds on the machine, `sims update` keeps the installed copies at the binary's version, and `sims skill install` does the same for a zip or `go install` build. Agents found: Claude Code (`~/.claude/skills`) and anything that reads `~/.agents/skills` (Codex among them); `--dir` names another skills directory, and `sims skill` prints the file for anything else. The source is [`skills/sims-cli/SKILL.md`](skills/sims-cli/SKILL.md).

```sh
sims skill install                      # ~/.claude/skills/sims-cli and ~/.agents/skills/sims-cli, whichever agents exist
sims skill install --dir .claude/skills # into this project, so the checkout carries it
sims skill > SKILL.md                   # for any other agent
```

## Keys

| Scope | Key | Action |
|-------|-----|--------|
| global | `:` | command bar (`:dev` `:apps` `:logs` `:proxy` `:img` `:update` `:connect HOST:PORT` `:pair HOST:PORT CODE`) |
| global | `?` / `esc` / `ctrl+c` | help / back / quit |
| global | `r` | refresh |
| devices | `b` | boot |
| devices | `ctrl+k` `ctrl+e` `ctrl+d` | shutdown; wipe data (factory reset, the device stays); delete the device itself. On a physical device `ctrl+d` forgets it instead: iOS unpairs (`devicectl manage unpair`), Android drops the wifi connection (`adb disconnect`); a USB phone simply leaves when unplugged |
| devices | `a` or `enter` / `l` | apps / log stream of the selected device. On a stopped virtual device `enter` asks to boot it first and opens apps once it is up |
| devices | `n` / `e` / `s` / `/` | new device (opens images) / edit hardware of an AVD (RAM, cores, disk; applied at its next boot) / show never-booted simulators / filter |
| devices | `w` / `x` | android: switch a USB device to adb over wifi / disconnect a wifi device. ios: open the wifi tunnel to a paired phone (`devicectl device info details`) |
| devices | `p` | ios: pair a physical device (`devicectl manage pair`) |
| devices | `t` / `shift+t` | the device's traffic as a stream, which `l` adds its log to / the same capture as a table |
| proxy | `enter` `v` `e` `shift+e` | read an exchange on its own page (`esc` closes it, `enter` there sends the text to `$PAGER`) / open the response body in whatever opens that kind of file / open the exchange in a windowed editor / pick a different editor |
| proxy | `/` `c` `p` `d` `s` | filter, clear, pause, hide this machine's own apps, save a HAR file |
| proxy | `g` / `space` / `shift+g` | group by domain (`enter` on a heading folds it) / fold one domain / fold or unfold every domain |
| proxy | `y` | filter by resource type the way a browser's network panel does: a list of xhr, doc, js, css, img, font, media, ws and other with counts, where `space` shows or hides one, `o` keeps only that one and `a` shows all; the table's TYPE column carries the same bucket, and the stream honours the same filter |
| proxy | `ctrl+k` / `esc` | stop the capture and restore every setting / leave the view with the capture running |
| stream | `up` / `down` `o` `shift+o` `f` | step between exchanges / open the selected one in place, headers then body / open every exchange / hold the view still while requests keep landing |
| devices, apps | `h` / `backspace` / `o` | send Home / Back / Overview to the device (android: `adb shell input keyevent`) |
| devices | `shift+p` `shift+v` `shift+n` `shift+m` `shift+r` `shift+s` `shift+l` | sort by platform, via, name, model, runtime, state, last; same key again flips direction. Default: state (running, offline, shutdown), then most recent; ties by name desc, runtime desc |
| apps | `enter` / `i` / `shift+i` / `ctrl+u` | launch / install via the OS file dialog (Finder on macOS, Explorer on Windows; falls back to the TUI picker elsewhere) / install via the TUI picker / uninstall |
| apps | `t` | the selected app's log with the device's traffic beside it; the traffic is the whole device, which the title says |
| apps | `l` | logs of the selected app only (android: `logcat --pid`, so the app must be running; ios: `log stream --predicate`) |
| apps | `s` / `/` | toggle preinstalled apps (hidden by default) / filter |
| picker | `enter` `backspace` `~` `d` `.` `t` `/` | open or pick, parent, home, Downloads, hidden files, type a path (tab completes), filter |
| logs | `/` `c` `p` `w` `g` `shift+g` | filter, clear, pause, toggle line wrap (on by default), top, bottom |
| logs, traffic | `t` / `l` | add or remove the traffic layer / the log layer; they are one stream and the last layer stays |
| logs+traffic | `n` / `shift+n` / `o` / `shift+o` | step to the next exchange, the previous one, open the selected one (headers, then body, then closed), open or close every exchange |
| logs+traffic | `enter` / `e` / `ctrl+k` | read the selected exchange in `$PAGER` / open it in `$EDITOR` / stop the capture and keep the log |
| proxy | `l` | show these exchanges in the log's stream instead of the table |
| images | `n` or `enter` / `i` / `s` | new device from image / install image (android) / show downloadable images |

Filtering works the same everywhere: `/` opens an empty prompt, `enter` applies the text as a case-insensitive substring match and highlights every hit in the rows (or log lines) that pass, an empty `enter` clears the filter, and `esc` leaves the current filter alone.

Anything that stops or removes something (shutdown, wipe, delete, uninstall) takes a ctrl chord so a stray key cannot fire it; wipe, delete and uninstall also ask for confirmation, and the prompt spells out what is lost. A confirmation starts on Yes, since it follows a key you already pressed, except for those three: there the cursor starts on No so enter alone never destroys anything.

## Android emulators: keyboard and nav keys

avdmanager writes `hw.keyboard = no` (the emulator default). With that setting the guest gets no keyboard input device at all (`adb shell getevent -pl` lists only `gpio-keys` and touch devices; with `yes` a `qwerty2` device appears), so typing from the host is dropped, and the toolbar's Back / Home / Overview buttons appear to go the same way. sims sets it to `yes` when it creates an AVD and again on every boot, so an AVD booted through sims gets a keyboard on its next start. The same pass sets `hw.gpu.enabled = yes`, `hw.camera.front = emulated`, and `PlayStore.enabled = yes` on `google_apis_playstore` images. RAM (`hw.ramSize`), heap (`vm.heapSize`) and `/data` size (`disk.dataPartition.size`) are left alone; edit `config.ini` per project.

## Wireless android

1. Plug the phone in over USB once, select it, press `w`. sims reads the wifi address, runs `adb tcpip 5555` and connects to `IP:5555`.
2. Or, with Android 11+ wireless debugging: `:pair 192.168.0.23:37099 123456` using the code the phone shows, then `:connect 192.168.0.23:5555`.

## Physical ios

Devices show up from `devicectl list devices` once they have been paired. Select an `Unpaired` one and press `p`: sims shows the checklist (Developer Mode on, phone unlocked, plugged in over USB for a phone this Mac has never seen), then runs `devicectl manage pair` and the phone shows a pairing prompt. Apple's own steps start with a cable too ([Pair a wireless device with Xcode](https://help.apple.com/xcode/mac/current/en.lproj/devbc48d1bad.html)); after that first pairing, unplug it and use `w` from the same wifi.

A paired phone on the same wifi shows as `Offline` until a CoreDevice tunnel is open, and the tunnel is opened lazily by the first command that addresses the device. Select it and press `w`: sims runs `devicectl device info details --device <id>`, which brings the state to `Connected` in a few seconds. No Xcode project needed. Requirements on the phone: unlocked, Developer Mode on, same network as the Mac (or plugged in over USB).

## What it runs underneath

| Action | android | ios |
|--------|---------|-----|
| list | `emulator -list-avds` + `adb devices -l` + `adb emu avd name` | `simctl list devices --json` + `devicectl list devices` |
| boot | `hw.keyboard = yes` in `config.ini`, then `emulator -avd NAME` (detached) | `simctl boot UDID` + `open -a Simulator` |
| shutdown | `adb emu kill` | `simctl shutdown UDID` |
| erase | `emulator -avd NAME -wipe-data` | `simctl erase UDID` |
| delete | `avdmanager delete avd -n NAME`; wifi phone: `adb disconnect` | `simctl delete UDID`; phone: `devicectl manage unpair` |
| apps | `adb shell pm list packages -f -i` (+ `-3` to tell yours from preinstalled; `installer=` tells adb from store). Apps you installed get their display name and version from `aapt2 dump badging` on the pulled APK, cached under the user cache dir; preinstalled apps keep the package name | sim: `simctl listapps` (via `plutil`), device: `devicectl device info apps` (+ `--include-all-apps`) |
| install / uninstall / launch | `adb install -r`, `adb uninstall`, `cmd package resolve-activity` + `am start -n` | sim: `simctl install/uninstall/launch`, device: `devicectl device install app / uninstall app / process launch` |
| logs | `adb logcat -v time` (+ `--pid=$(pidof pkg)` for one app) | sim: `simctl spawn UDID log stream` (+ `--predicate 'process == NAME'`), device: `idevicesyslog -u UDID` (+ `-p NAME`, per the libimobiledevice docs; untested here) |
| wifi / pair | `adb tcpip 5555`, `adb connect`, `adb pair`, `adb disconnect` | `devicectl manage pair`, `devicectl device info details` (opens the tunnel) |
| proxy | `adb shell settings put global http_proxy HOST:PORT` (`:0` clears it); certificate by `adb push` into the system store where the image allows it, otherwise the user store | sim: `simctl keychain add-root-cert`, plus this Mac's `networksetup -setwebproxy` / `-setsecurewebproxy`; device: a CMS-signed `.mobileconfig` carrying the root and the proxy, served at `http://<mac>:<port>/sims-proxy.mobileconfig` from the capture's own listener for Safari on the phone to fetch (devicectl has no command that installs one) |
| images | `sdkmanager --list` | `simctl list runtimes --json` |
| create | `avdmanager create avd -n -k -d`, then `hw.keyboard = yes` and the chosen `hw.ramSize` / `hw.cpu.ncore` / `disk.dataPartition.size` in `config.ini` | `simctl create NAME TYPE RUNTIME` |
| device types | `avdmanager list device -c`; screen size from `<sdk>/skins/<id>/layout` when that skin is installed | `simctl list devicetypes --json`; screen size from each type's `profile.plist` |
| edit hardware | `config.ini` (`hw.ramSize`, `hw.cpu.ncore`, `disk.dataPartition.size`) | not applicable |

iOS runtimes cannot be downloaded from sims. Use `xcodebuild -downloadPlatform iOS` and refresh.

## Develop

```sh
go build ./cmd/sims
go test ./... -race    # parsers run everywhere; provider and UI tests skip when the toolchain is absent
GOOS=windows go build ./cmd/sims

SIMS_SCREENSHOTS=1 go test ./internal/ui -run TestGenerateScreenshots   # regenerates docs/img/*.svg
```

Screenshots are rendered from the same views on tcell's simulation screen with fixture devices, so they stay in step with the code.

The code is three layers. `internal/device` holds the `Provider` interface and the android and ios implementations that shell out to the SDK tools. `internal/sims` is the layer both front ends use: it merges the providers, resolves a device reference, and turns optional provider abilities (wireless adb, hardware edits, pairing) into methods that fail with `errors.ErrUnsupported` on the other platform. `internal/cli` (cobra) and `internal/ui` (tview) sit on top and never import each other, so the CLI can become its own binary by adding a `main` that leaves `RunTUI` unset. `internal/device/devicetest` has in-memory providers for testing the two upper layers.

Releases are cut by tagging: `git tag vX.Y.Z && git push origin vX.Y.Z` runs GoReleaser in GitHub Actions and publishes the archives and `checksums.txt` that `install.sh` downloads. GoReleaser releases to whichever repo runs the workflow, so a mirror that receives the tag gets its own release.

## Acknowledgements

The layout, the `:` command bar and the hotkey block are borrowed from [k9s](https://github.com/derailed/k9s). Built with [tview](https://github.com/rivo/tview) and [tcell](https://github.com/gdamore/tcell).
