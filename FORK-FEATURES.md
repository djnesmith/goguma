# Fork features

Read this before merging upstream. Every item must still work after the merge.

Branch: `main`. Base: `upstream/main` (junnam586/goguma).

Go tests cover the daemon items: `go test ./...`. Build the app with `macos/scripts/make-app.sh` (output `macos/build/goguma.app`).

Not fork features: `4e57ad4`, `843e855`, `de178b5`, `142e34a`, `14a8773` are upstream's own commits (notarization, advisory feed, blog), cherry-picked here with new SHAs. Merge `efbe85a` records upstream's originals as already merged.

## 1. No upstream promotion in the app

Removes the GitHub star ask, the author's LinkedIn credit and the mailing-list sign-up from the popover and Settings. LICENSE and copyright headers stay.

- Commits: `1afd4bc`
- Files: `macos/Sources/GogumaUI/Views/PopoverView.swift`, `SettingsWindowView.swift`
- Check: open the popover; no star, LinkedIn or mailing-list links. `grep -rn -i linkedin macos/Sources` prints nothing.

## 2. Restart button

"Restart" in the popover footer restarts the background service through `launchctl kickstart -k` and reports the result.

- Commits: `3337768`
- Files: `macos/Sources/GogumaUI/App/WindowCoordinator.swift`, `PopoverView.swift`
- Check: click Restart in the popover footer; the message row reports success and the popover reconnects.

## 3. Agents observed, not held, when `agent_hooks` is off

With `agent_hooks` off the hooks stay installed; agent sessions show in `status` as `agent_sessions` and the popover says "N agents are working · the Mac will still sleep". No hold is opened. `goguma hooks remove` is remembered in `hooks-opted-out` in the state dir.

- Commits: `054fc63`, `7363f8b`, `1796c6e`
- Files: `internal/daemon/runhold.go`, `agenthooks.go`, `setconfig.go`, `internal/model/status.go`, `internal/cli/hookscmd.go`, `internal/paths/paths.go`, `macos/Sources/GogumaUI/Model/Models.swift`, `PopoverView.swift`
- Check: `go test ./internal/daemon/` (`agenthold_gate_test.go`, `agenthooks_test.go`). By hand: `goguma config set agent_hooks off`, start a Claude Code session, the popover shows the "will still sleep" line and `pmset -g` shows `SleepDisabled 0`.

## 4. Footer layout

Footer reads Jobs · Settings · Restart · Quit with labels on one line; CPU temperature moved above the controls.

- Commits: `1796c6e`
- Files: `PopoverView.swift`, `macos/Sources/GogumaUI/Theme.swift`
- Check: open the popover; no label breaks mid-word.

## 5. Menu bar mark: outline template, coloured while holding

The status item is a drawn outline template that follows the menu bar's light/dark tint, and turns a warm ink while sleep is held off.

- Commits: `1b8610a`, `a83bf8b`
- Files: `macos/Sources/GogumaUI/Views/SweetPotatoMark.swift`, `MarkProofSheet.swift`, `App/GogumaApp.swift`, `Theme.swift`
- Check: menu bar icon is a monochrome outline at rest; start Keep Awake and it turns coloured.

## 6. Paste and editing shortcuts in windows

Titled Edit and Window menus, and a local key monitor so ⌘V/⌘C/⌘X/⌘A/⌘Z reach text fields. Debug self-test: `GogumaUI --selftest-paste`.

- Commits: `59e920a`, `49a6c56`
- Files: `macos/Sources/GogumaUI/App/AppDelegate.swift`, `App/EditingKeys.swift`, `App/PasteSelfTest.swift`, `WindowCoordinator.swift`
- Check: Settings > Advanced, click the webhook field, ⌘V pastes.

## 7. Ice scene CPU fix

The empty-list ice scene draws at display rate instead of 8x, and pauses when not visible. Idle app CPU went from ~53% of a core to ~0%.

- Commits: `89491da`, `3ac306d`
- Files: `macos/Sources/GogumaUI/Views/IceScene.swift`, `IceSceneMotion.swift`
- Check: launch the app, leave the popover closed 30s, Activity Monitor shows goguma near 0% CPU.

## 8. Open at login

Settings > General > "Open goguma when I log in", via `SMAppService.mainApp`. Shows the live state, not a stored bool. General is a new tab, reachable even when the daemon is down.

- Commits: `473ca7f`
- Files: `SettingsWindowView.swift`
- Check: turn it on; System Settings > General > Login Items lists goguma.

## 9. Real SDK version in the app binary

`make-app.sh` runs `vtool -set-build-version macos 14.0 27.0` before signing, so the app gets the macOS 26+ control design while still running on 14.

- Commits: `3bc7e7d`
- Files: `macos/scripts/make-app.sh`
- Check: after a build, `vtool -show-build macos/build/goguma.app/Contents/MacOS/*` shows `minos 14.0` and `sdk 27.0`.

## 10. Readable quiet text

`textTertiary` drops its extra 0.75 alpha so secondary text clears 4.5:1 on the macOS 26 glass popover.

- Commits: `d8a240e`
- Files: `Theme.swift`
- Check: `grep -n -A3 textTertiary macos/Sources/GogumaUI/Theme.swift` shows no `.opacity`.

## 11. Keep the screen awake too

Popover checkbox "Keep the screen awake too" (setting `keep_display_awake`, off by default). During a manual Keep Awake it also holds a display-sleep assertion, released whenever the hold ends.

- Commits: `cee569b`
- Files: `internal/daemon/keepawake.go`, `hold.go`, `setconfig.go`, `internal/config/config.go`, `internal/power/platform_darwin.go`, `PopoverView.swift`, `StatusStore.swift`
- Check: `go test ./internal/daemon/ -run KeepDisplay` and `go test ./internal/power/`. By hand: tick it, start Keep Awake, `pmset -g assertions` lists `PreventUserIdleDisplaySleep`.

## 12. Wrangler cache not tracked (security)

`site/.wrangler/` is gitignored and its two cache files removed; they carried a Cloudflare account id. Upstream still tracks them, so a merge can add them back.

- Commits: `07828fa`
- Files: `.gitignore`
- Check: `git ls-files site/.wrangler` prints nothing after the merge. If it lists files, `git rm --cached` them.
