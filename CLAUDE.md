# monarch

Personal screen/audio/video recorder + compress pipeline for jelmer's
lux box. Runs as user services. Goal: never let a recording orphan the
disk or CPU.

## Layout

| Path | Purpose |
|------|---------|
| `core/main.go` | Entry point. NOTE: re-runs app on error then exits 0 — action errors are silently swallowed for every subcommand. Pre-existing. |
| `core/cmd/` | Subcommand actions (`v4l2`, `x11grab`, `arecord`, `air`, `xidle`, `compress`, `screencast`). |
| `core/compress/` | Pipeline scanner: locks (`.lock`/`.done`), partial-file cleanup, ffmpeg re-encode, raw-deletion on success. |
| `sd/` | systemd `--user` units. `BIN` is a placeholder; `make install` rewrites it to the absolute binary path. |
| `Makefile` | Build + deploy targets. See "Deploy" below. |
| `notes/` | Scratch + old shell-script prototypes (`srec.sh`, `v4l2.sh`, etc). Reference only. |
| `PKGFILE/` | Arch packaging stub. |

## Subcommands

- `v4l2` / `x11grab` / `arecord` / `air` — long-running capture services
  wrapped in `xidle.Idlemon`: spawn while user is active, kill when idle
  for `IdleOverTimeout`. Output to `/data/mon/<name>/`. Hardcoded paths.
- `compress [<pipeline>]` — one scan over `Pipelines["<name>"].Indir`.
  No arg = scan all. Triggered by `monarch_compress.timer` every 30 min.
- `screencast {start|stop|status}` — user-triggered (G4/G5 in i3).
  Different model from the others; see below.
- `xidle` — debug helper for the idle library.

## screencast (G4/G5)

i3 binds `XF86Launch4` → `screencast start`, `XF86Launch5` →
`screencast stop`, via `/.../linux/hooks/screencast.sh`
(lives in the dotfiles repo, not here). That wrapper exports
`MONARCH_LED_HOOK` and calls this binary.

Supervisor (one Go process per session):

- ffmpeg `x11grab` + pulse mic + default-sink monitor mix, segmented
  every 30 min via `-f segment -strftime 1`. Finished segments are
  closed on rotation so the compress pipeline can chew them while
  recording continues — no big stop-time CPU spike.
- 15-min idle stop via `JelmerDeHen/scrnsaver`
  (`XScreenSaverQueryInfo.Idle`).
- 4-hour wall-clock hard cap, driven by the supervisor (ffmpeg's own
  `-t` is unreliable when combined with `-f segment`).
- 20 GB disk-free guard on the output dir.
- Heartbeat JSON at `$XDG_RUNTIME_DIR/monarch_screencast.json`
  containing pid, ffmpeg pid, start ts, raw glob, and the *resolved*
  idle/cap thresholds (so `status` from another shell reports what the
  supervisor is actually using, not whatever env vars the caller has).
- `stop` sends SIGTERM → supervisor sends SIGINT to ffmpeg → 30s drain
  → SIGKILL fallback.
- LED hook (default `~/.local/bin/monarch-led-hook`, override with
  `MONARCH_LED_HOOK`) fired on start/stop. Reaped in a goroutine —
  non-blocking, no zombies under the long-lived supervisor.

Env overrides (Go duration strings):

| Var | Default | Notes |
|-----|---------|-------|
| `MONARCH_SC_IDLE_STOP` | `15m` | Set tiny (e.g. `3s`) to smoke-test idle path. |
| `MONARCH_SC_HARD_CAP` | `4h` | |
| `MONARCH_SC_DISK_MIN_GB` | `20` | Integer GB. |
| `MONARCH_LED_HOOK` | `~/.local/bin/monarch-led-hook` | Missing hook = no-op. |
| `MONARCH_LED_DEV` | `G815` | solaar device codename. G915 reports as `G815` here. |

## Pipelines (compress)

Registered in `core/compress/pipeline.go` `init()`. All paths hardcoded
under `/data/mon/<name>` → `/data/mon/<name>_compress`.

| Name | In ext | Out ext | ffmpeg argv |
|------|--------|---------|-------------|
| `x11grab` | mkv | mp4 | `-an -vf monochrome,mpdecimate -fps_mode vfr -crf 30` |
| `arecord` | wav | mp3 | (default) |
| `v4l2` | mkv | mp4 | `-an` |
| `screencast` | mkv | mp4 | `-c:v libx264 -preset slow -crf 22 -pix_fmt yuv420p -c:a copy -movflags +faststart` |

`screencast` deliberately omits `-profile/-level`: 7680×2160 captures
overflow Level 5.1 and x264 picks the right level automatically.

Auto-delete: `RemoveProcessedFiles` deletes the raw infile once the
matching `.done` lock exists in the outdir. Runs on the *next* scan
after a successful compress.

The `Scan()` previously gated all compression behind a 1-minute idle
check — currently commented out so compression runs regardless of
desktop activity.

## Deploy

Single workflow. All targets are idempotent.

```sh
make deploy        # build + install + (re)start timers
make build         # go build -> bin/monarch
make install       # install binary + sd units, sed BIN, enable timers
make start         # start timers (screencast is user-triggered, no timer)
make stop          # stop all monarch_*
make status        # systemctl --user status monarch_*
make uninstall     # disable timers + remove units + remove binary
make clean         # rm -rf bin
```

Install locations:

| Artifact | Location |
|----------|----------|
| Binary | `~/.local/bin/monarch` |
| systemd units | `~/.config/systemd/user/monarch_*.{service,timer}` |
| Output dirs | `/data/mon/<pipeline>/` (created on first use by `make install`) |

`MONARCH_LED_HOOK` is set by the i3 wrapper in the dotfiles repo —
this repo does **not** install the LED hook itself.

## Constraints worth knowing

- Output dirs (`/data/mon/...`) are hardcoded in source. Changing
  requires editing both the capture action AND the pipeline entry.
- `core/main.go` swallows action errors. Subcommands that need to
  surface failures (`screencast stop`) print to stderr directly.
- The `sd/*.service` files use `BIN` as a literal placeholder;
  `make install` rewrites it via `sed`. Don't pre-substitute and
  commit the rewritten files.
- Binary needs `libXss` + `libX11` at runtime (CGO via scrnsaver).

## Related repos

- `github.com/JelmerDeHen/xidle` — idle-time poller and CmdJob wrapper.
- `github.com/JelmerDeHen/scrnsaver` — X11 `XScreenSaverQueryInfo` cgo
  binding.
- jelmer's dotfiles (private) — `/.../linux/hooks/screencast.sh`
  (i3 wrapper) and `/.../linux/hooks/monarch-led-hook.sh` (solaar
  blinker). i3 G4/G5 bindings live there too.
