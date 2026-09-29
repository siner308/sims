# Architecture

This is a map of the code for people who want to read or change it. For what sims does, see the [README](../README.md); for the exact SDK command behind each action, see [What it runs underneath](../README.md#what-it-runs-underneath).

## Layers

```
cmd/sims                      main: builds the Manager, hands it to the CLI
   |
   +-- internal/cli           cobra commands            (never imports ui)
   +-- internal/ui            tview terminal UI         (never imports cli)
          |
          v
   internal/sims              Manager: the one API both front ends use
          |                   routes by platform, merges lists, owns captures
          +---------------------------+
          v                           v
   internal/device                internal/capture
     Provider interface             a capture session: proxy + device setup
     android/  ios/  desktop/       + crash journal
     devicetest/ (fakes)                  |
                                          v
                                   internal/proxy
                                     MITM HTTP(S) proxy, CA, flow store
                                     (knows nothing about devices)
```

Three rules keep the layers apart:

- `internal/cli` and `internal/ui` never import each other. A bare `sims` runs the TUI only because `main` passes `RunTUI` in [`cli.Options`](../internal/cli/root.go); a CLI-only binary leaves it nil and a bare `sims` prints help.
- Providers do not know about each other or about the front ends. Anything that needs more than one platform lives in `internal/sims`.
- `internal/proxy` knows nothing about devices. `internal/capture` is the only place that joins a proxy to a device.

## Packages

| Package | Owns |
|---------|------|
| [`cmd/sims`](../cmd/sims) | `main`. Builds `sims.New(android.New(), ios.New(), desktop.New())`, resolves the version, wires the update check into the TUI. |
| [`internal/device`](../internal/device) | The `Provider` interface, the optional capability interfaces, and the plain types: `Device`, `App`, `Image`, `DeviceType`, `Hardware`, the proxy types. |
| [`internal/device/android`](../internal/device/android) | adb, emulator, avdmanager, sdkmanager, aapt2. |
| [`internal/device/ios`](../internal/device/ios) | `xcrun simctl`, `xcrun devicectl`, plutil, idevicesyslog. |
| [`internal/device/desktop`](../internal/device/desktop) | The machine sims runs on, listed as one device (`localhost`) so its apps, log and traffic work like a phone's. On macOS it also installs and removes iPad apps ("Designed for iPad") through the system's iOS App Installer and the Finder. |
| [`internal/device/devicetest`](../internal/device/devicetest) | In-memory providers for testing the layers above. |
| [`internal/sims`](../internal/sims) | `Manager`: routing, merged lists, device resolution, capability dispatch, capture and standby ownership. |
| [`internal/capture`](../internal/capture) | One capture: starts a proxy, points the device at it, restores everything on stop, and keeps a journal so a killed capture can be undone later. |
| [`internal/proxy`](../internal/proxy) | The MITM proxy, its root CA, the flow store, body decoding, HAR export. |
| [`internal/ui`](../internal/ui) | The k9s-style TUI. |
| [`internal/cli`](../internal/cli) | The cobra command tree. |
| [`internal/doctor`](../internal/doctor) | `sims doctor`: runs each provider's checks and prints the fix commands. |
| [`internal/update`](../internal/update) | `sims update` and the TUI's background release check. |
| [`skills`](../skills) | The agent skill (`SKILL.md`), embedded in the binary so an installed copy matches the version. |

## Start-up

1. [`main`](../cmd/sims/main.go) builds the three providers and `sims.New` them. `sims.New` calls `Available()` on each: a provider whose toolchain is present becomes usable, the others are kept aside with the reason, so a machine without Xcode still runs everything Android and `sims doctor` can still probe iOS.
2. `cli.Execute` runs cobra. The root command takes no arguments, so `sims` alone calls `RunTUI` and anything else is a subcommand.
3. `RunTUI` builds `ui.New(version, manager)`, starts the release check unless `SIMS_NO_UPDATE_CHECK` is set, and runs the app.

## Devices and providers

### The Provider interface

[`device.Provider`](../internal/device/device.go) is the part every platform has: `List`, `Boot`, `Shutdown`, `Erase`, `Delete`, `Apps`, `InstallApp`, `UninstallApp`, `LaunchApp`, `LogCmd`, `Images`, `InstallImage`, `Create`, `DeviceTypes`, plus `Platform` and `Available`. `LogCmd` returns an `*exec.Cmd` rather than lines, so each front end decides how to read the stream.

Everything only some platforms can do is a separate small interface. The Manager finds it by type assertion and returns an error wrapping `errors.ErrUnsupported` on a platform without it, so callers never check the platform themselves.

| Interface | What it adds | Implemented by |
|-----------|--------------|----------------|
| `HardwareEditor` | read and change RAM, cores, disk | android |
| `KeySender` | home, back, overview keys | android |
| `Screenshotter` | screenshot bytes | android, ios |
| `Rebooter` | reboot | android, ios |
| `Describer` | facts for the TUI header | android, ios, desktop |
| `Wireless` | adb pair, connect, disconnect, switch a USB phone to tcpip | android |
| `Connector` | open the CoreDevice tunnel to a phone | ios |
| `Pairer` | pair a phone | ios |
| `SettingsOpener` | open Settings on the phone | ios |
| `Proxier` | point the device at a proxy, clear it, report its state | android, ios, desktop |
| `Checker` | the checks `sims doctor` prints | android, ios |

`device.Device` is a plain value. Its `ID` is the AVD name on Android, the UDID on an iOS simulator, the CoreDevice identifier on an iPhone and `localhost` for the desktop. `Serial` is the adb serial while an Android device is reachable. `Kind` (virtual, physical, host) and `Transport` (avd, sim, usb, wifi, local) are what most branching looks at.

### How providers run the tools

Providers shell out and parse the output; there is no daemon and no cached device state. A few habits hold across them:

- Errors carry the tail of the tool's stderr, because the useful line (`Error: ...`) is usually near the end. See `output` and `tail` in [`android.go`](../internal/device/android/android.go) and `simctl` in [`ios.go`](../internal/device/ios/ios.go).
- A tool that exits 0 on failure is checked another way. `adb connect` output is read, and an Android proxy setting is read back after `settings put`.
- The emulator is started detached, so it outlives the TUI.
- Android finds the SDK from `ANDROID_HOME`, then `ANDROID_SDK_ROOT`, then the platform default, and falls back to `PATH` per binary ([`sdk.go`](../internal/device/android/sdk.go)). iOS relies on `xcrun` resolving the selected Xcode.

Comments in each provider record the quirks the code works around: why `monkey` is not used to launch an app, why a simulator screenshot goes through a temp file, why `devicectl` JSON is read from a file. Read them before changing a command line.

### The Manager

[`sims.Manager`](../internal/sims/manager.go) is what both front ends call. On top of routing each call to `d.Platform`'s provider it:

- runs every provider's `List` in parallel and returns the merged list together with `errors.Join` of the failures, so one broken toolchain does not hide the other platform;
- resolves a device reference by ID, then adb serial, then case-insensitive name, and reports an ambiguous tier as an error rather than guessing;
- waits for a boot by polling `List`, and returns the fresh record because the adb serial only exists after boot;
- finds a newly created device by name, since the SDK tools do not return an id;
- owns the running captures (one per device) and the phone standbys ([`proxy.go`](../internal/sims/proxy.go), [`standby.go`](../internal/sims/standby.go)).

## The terminal UI

[`internal/ui`](../internal/ui) is built on [tview](https://github.com/rivo/tview) and [tcell](https://github.com/gdamore/tcell).

### Views and keys

`App` ([`app.go`](../internal/ui/app.go)) holds a header, a body of tview pages, a status line and the `:` command bar. Screens are `view`s (`Name`, `Hints`, `Primitive`, `Refresh`) on a stack: `push` adds one and refreshes it, `pop` closes the top one and never pops the devices view at the root, `replaceTop` swaps the top. A view that holds a stream or a goroutine implements `close()`, which runs whenever it leaves the stack.

| View | File | Shows |
|------|------|-------|
| `devicesView` | [devices.go](../internal/ui/devices.go) | the root table of devices; boot, wipe, delete, wifi, pairing |
| `appsView` | [apps.go](../internal/ui/apps.go) | a device's apps; launch, install, uninstall |
| `logsView` | [logs.go](../internal/ui/logs.go) | a log stream, optionally with the device's traffic mixed in |
| `flowsView` | [flows.go](../internal/ui/flows.go) | captured traffic as a table; HAR export |
| `readerView` | [reader.go](../internal/ui/reader.go) | one exchange as text |
| `typesView` | [types.go](../internal/ui/types.go) | which resource types the traffic views hide |
| `pickerView` | [picker.go](../internal/ui/picker.go) | a file browser for builds, used when there is no OS file dialog |
| `imagesView`, `createView`, `hardwareView` | [images.go](../internal/ui/images.go) | system images, the new-device form, AVD hardware |
| `helpView` | [help.go](../internal/ui/help.go) | keys of the current view |
| `editorChoiceView` | [editorchoice.go](../internal/ui/editorchoice.go) | GUI editors to open an exchange in |

Keys go through two layers. `App.onKey` runs first: it leaves everything to the command bar or an input field when one has focus, and otherwise handles `:`, `?`, Esc (pop), Ctrl+C and `r`/F5 (refresh the top view). What it does not handle reaches the focused view's own `onKey`.

### Threading

Only the tview goroutine touches widgets or view fields. Anything slow runs through `App.async(caption, work, then)`: `work` runs on its own goroutine and must not touch tview, then `then` (or an error flash) runs back on the UI goroutine through `QueueUpdateDraw`. Streams and watchers follow the same rule and hand their results over with `QueueUpdateDraw`.

### The status line

The status line has three sources, and one function draws it:

- a **message** from `setStatus`, which stays until something newer replaces it;
- a **flash** from `flash` or `flashErr`, held for 4 seconds (6 for an error). An error is also left behind as the message, so it does not vanish before the user reads it;
- the **runner** (the running mascot), shown while any `async` job or log wait is in flight, captioned by the newest job that has a caption.

Each write takes the next number from `statusSeq`, and `renderStatus` shows whichever standing source was written last. The runner's ticker and the flash expiry timer only call `renderStatus`; nothing reads the widget's text back to decide what to show. [`status_test.go`](../internal/ui/status_test.go) pins these rules down.

### Logs and the timeline

`logsView` runs the provider's `LogCmd`, merges stderr into stdout, and reads it on a goroutine that hands lines to the UI in batches (every 100 ms or 256 lines), keeping the last 5000. With a capture running, log lines and exchanges go into one [`timeline`](../internal/ui/timeline.go) ordered by time: a log line by the timestamp parsed from it (or its arrival when it has none), an exchange by its request start, updated in place when the response finishes. The view watches the flow store and redraws at most every 250 ms.

Traffic is device-wide. When the log is narrowed to one app, the view says so, because the proxy cannot tell which app on a phone sent a request.

### Leaving the terminal

Opening an exchange suspends the TUI and runs `$PAGER` (or `less`), off the UI goroutine because `Suspend` blocks ([external.go](../internal/ui/external.go)). Editors are GUI editors the OS reports for text files, never terminal editors, and the choice is remembered for the session. Installing a build uses the OS file dialog (`osascript` on macOS, PowerShell on Windows) and falls back to `pickerView` elsewhere ([nativepicker.go](../internal/ui/nativepicker.go)).

## Traffic capture

### The proxy

[`internal/proxy`](../internal/proxy) is a MITM HTTP(S) proxy on the standard library.

- Plain HTTP is forwarded and both bodies are teed into the flow as they stream.
- A `CONNECT` is hijacked. If the first byte is a TLS ClientHello, TLS is terminated with a leaf certificate signed on demand by sims's own root CA, and the inner requests are served one by one. Anything else is tunnelled and recorded as a tunnel. A failed handshake is recorded too, since it usually means the device does not trust the CA or the app pins its certificate.
- WebSocket and other upgrades record the handshake, then pipe bytes both ways.
- The upstream transport ignores this machine's own proxy settings and never decompresses a response itself, so bodies are stored as they came off the wire and decoded (gzip, deflate, brotli) only for display, JSON and HAR.
- `Server.Attribute` is asked once per client connection, with a 400 ms budget, who the connection belongs to. The answer labels the flows and can tell the proxy to relay a connection untouched.
- `Publish` serves a file to requests addressed to the proxy itself, which is how an iPhone downloads its profile.

The root CA is an ECDSA P-256 certificate valid for 10 years, stored as `ca.pem` and `ca-key.pem` (mode 0600, and a key others can read is refused) ([ca.go](../internal/proxy/ca.go)). Leaves are valid for 365 days, under Apple's 398-day limit.

Flows live in a bounded [`Store`](../internal/proxy/flow.go) (2000 flows, 100 MiB per body, 1 GiB of bodies in total). Readers get copies. Consumers learn about changes in two ways: `Changed()` returns a channel that is closed on the next change (take a fresh one after each wake-up, and subscribe before starting the goroutine), and `OnDone` callbacks receive each finished flow on the proxying goroutine.

### A capture session

[`capture.Start`](../internal/capture/capture.go) joins a proxy to a device, in this order:

1. The device's provider must implement `Proxier`.
2. Load or create the CA.
3. Listen: on `127.0.0.1` for virtual devices, on `0.0.0.0` for physical ones, which have to reach it over the network. A physical iPhone uses a fixed port (9797, or the one its phone record saved) so its profile keeps working.
4. Write the journal, then call the provider's `SetProxy` with a target: `10.0.2.2` (the emulator's alias for the host) for an Android emulator, `127.0.0.1` when the host's own proxy is used, the machine's LAN address for a phone.
5. For an iOS simulator or the desktop, which have no proxy setting of their own, record this Mac's web proxy settings in the journal and then point them at the capture ([hostproxy_darwin.go](../internal/capture/hostproxy_darwin.go)).

Any failure stops the session, and `Stop` runs every undo step even when one fails: clear the device's proxy, restore the Mac's settings, close the proxy, remove the journal.

How each platform is pointed at the proxy:

- **Android emulator**: `settings put global http_proxy`, read back. The CA goes into the system store through `adb root` and `remount` when the image allows it, otherwise to Downloads with steps for the user.
- **iPhone**: a CMS-signed `.mobileconfig` with the CA and a wifi proxy for the current SSID, published by the proxy and opened in Safari on the phone through `devicectl`. The user taps Allow, installs it and turns on trust. Clearing leaves the profile in place on purpose, because nothing on the Mac can remove it.
- **iOS simulator**: `simctl keychain add-root-cert`, plus the Mac's system proxy.
- **The desktop**: the Mac's system proxy. Trusting the CA is left to `sims proxy ca localhost --install`, because `security add-trusted-cert` shows a panel that would hang the TUI.

### Scope and attribution

By default (`ScopeDevice`) only the watched device's traffic is opened and everything else is relayed untouched; `sims proxy run --all` opens everything. Attribution decides which is which: a connection from the network is the phone or emulator; a loopback connection is looked up with `lsof` on macOS and counts as the device only when it comes from a simulator process (or when the Mac itself is the device).

### Crash safety

Changing a device's or this Mac's proxy and then dying would leave it without a network. So each capture writes `in-flight/<pid>-<port>.json` in the certificate directory before changing anything, with the device and the Mac's previous proxy settings, and removes it on stop ([journal.go](../internal/capture/journal.go)). On the next start, the TUI looks for a journal whose process is gone and whose changes are still in place, and offers to put things back; the CLI does it with `sims proxy clean`. The TUI and `sims proxy run` also stop their captures on SIGINT, SIGTERM and SIGHUP.

### Phone standby

An iPhone's profile points at the fixed port for good. While the TUI is open (or `sims proxy standby` runs) and no capture is on the phone, a standby listens on that port and relays everything without recording, so the phone keeps its network ([standby.go](../internal/sims/standby.go)). A capture on the phone pauses the standby, and stopping it brings the standby back.

## State on disk and environment

| What | Where |
|------|-------|
| Root CA and key, capture journals, phone records | `$SIMS_PROXY_DIR`, else the user cache dir + `sims/proxy` (`~/Library/Caches/sims/proxy` on macOS). No temp-dir fallback, so the key never lands where other accounts can read it. |
| Android app labels read with aapt2 | the user cache dir + `sims/apk-labels.json` |
| Android SDK and AVDs | `ANDROID_HOME`, `ANDROID_SDK_ROOT`, `ANDROID_AVD_HOME`, `ANDROID_USER_HOME` |
| Skip the TUI's release check | `SIMS_NO_UPDATE_CHECK=1` |

## Command line

[`internal/cli`](../internal/cli) is a cobra tree: `device`, `app`, `image`, `device-type`, `proxy`, `doctor`, `update`, `skill`. Each command resolves its device with `Manager.Resolve` (honouring `--platform`) and calls the matching Manager method. With `--json` most commands print the record they acted on (a few print an address or a path), a list prints a bare array, and `sims proxy run --json` streams one flow per line. A list that is missing a platform still prints and reports the failure on stderr. The exit code is 0 or 1.

`sims doctor` runs every provider's `Checker`, including providers whose toolchain is missing, prints a line per check as it finishes, and ends with the fix commands to copy. `sims update` reads the latest tag from GitHub's `releases/latest` redirect (never the rate-limited API), verifies the archive against `checksums.txt` and replaces the binary by rename. `sims skill install` writes the embedded `SKILL.md` under `~/.claude/skills` and `~/.agents/skills`, for whichever of `~/.claude` and `~/.agents` already exists.

## Testing

`go test ./... -race` runs in CI on Linux, along with `gofmt`, `go vet`, and Windows and macOS builds ([ci.yml](../.github/workflows/ci.yml)). Most tests need no device and no SDK:

- **Parsers** are tested on captured tool output (`adb devices -l`, `pm list packages`, `aapt2 dump badging`, `devicectl` JSON, `config.ini`).
- **Command lines** are tested against fake binaries: `android.NewWithADB` points at a generated shell script that records its arguments, and the iOS provider's `runDevicectl` is a swappable function.
- **The Manager and the CLI** run on the in-memory providers in [`devicetest`](../internal/device/devicetest). Keep those in step with the optional interfaces of the real providers.
- **The TUI** runs headless on tcell's simulation screen (`runHeadless` in [app_test.go](../internal/ui/app_test.go)). Read widgets only through `onUI` and poll with `waitFor`, both of which run on the UI goroutine. `runHeadless` points `SIMS_PROXY_DIR` at a temp dir and `PAGER` and `EDITOR` at `true`, so a test never touches the real certificate or opens the user's editor.
- **Real toolchains**: tests that use the real providers skip when `Available()` fails.
- **Screenshots** in `docs/img` are rendered by a test from the same views with fixture devices: `SIMS_SCREENSHOTS=1 go test ./internal/ui -run TestGenerateScreenshots`.

## Where a change usually goes

- **A new action on one platform**: add a small interface in `internal/device`, implement it in the provider, dispatch it in the Manager with an `errors.ErrUnsupported` fallback, then call the Manager from the CLI and the UI. Add it to the matching `devicetest` fake.
- **A new screen**: a type that implements `view`, pushed from an existing view's `onKey`. Do slow work through `App.async` and give the job a caption.
- **A new command**: a cobra command in `internal/cli` that resolves the device and calls one Manager method, with `result` for the human and `--json` output.
- **A new SDK quirk**: fix it in the provider, next to a comment that says what the tool does wrong, and test the parser on the output that showed it.
