package cmd

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/urfave/cli/v2"

	"github.com/JelmerDeHen/scrnsaver"
)

// User-triggered screen recording (G4/G5 binding) with idle, wall-clock and
// disk safeguards. Output is segmented so a runaway capture is recoverable
// per chunk and so the compress pipeline can chew finished segments while
// recording continues.
const (
	scOutdir     = "/data/mon/screencast"
	scSegmentSec = 1800
	scFramerate  = "30"
	scCrf        = "18"
	scPreset     = "ultrafast"
	scLogPath    = "/tmp/monarch_screencast.log"
)

func scIdleStopDur() time.Duration {
	if v := os.Getenv("MONARCH_SC_IDLE_STOP"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 15 * time.Minute
}

func scHardCapDur() time.Duration {
	if v := os.Getenv("MONARCH_SC_HARD_CAP"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 4 * time.Hour
}

func scDiskFreeMinByte() uint64 {
	if v := os.Getenv("MONARCH_SC_DISK_MIN_GB"); v != "" {
		if gb, err := strconv.ParseUint(v, 10, 64); err == nil {
			return gb * 1024 * 1024 * 1024
		}
	}
	return 20 * 1024 * 1024 * 1024
}

type screencastState struct {
	Pid       int    `json:"pid"`
	FfmpegPid int    `json:"ffmpeg_pid"`
	Started   string `json:"started"`
	RawGlob   string `json:"raw_glob"`
	// Resolved supervisor thresholds. Recorded so `status` reflects
	// what the supervisor is actually using, not whatever env vars
	// the caller of `status` happens to have set.
	IdleStopNs int64 `json:"idle_stop_ns"`
	HardCapNs  int64 `json:"hard_cap_ns"`
}

func scHeartbeatPath() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "monarch_screencast.json")
	}
	return "/tmp/monarch_screencast.json"
}

func scWriteHeartbeat(s screencastState) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(scHeartbeatPath(), b, 0o644)
}

func scReadHeartbeat() (screencastState, error) {
	var s screencastState
	b, err := os.ReadFile(scHeartbeatPath())
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

func scAlive() (int, bool) {
	s, err := scReadHeartbeat()
	if err != nil {
		return 0, false
	}
	if err := syscall.Kill(s.Pid, 0); err != nil {
		return s.Pid, false
	}
	return s.Pid, true
}

func scPactl(arg ...string) (string, error) {
	out, err := exec.Command("pactl", arg...).Output()
	return strings.TrimSpace(string(out)), err
}

func scFreeBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// scLedHook fires an out-of-band recording indicator (G815/G915 LEDs via
// solaar by default). Resolution order: $MONARCH_LED_HOOK env var, then
// ~/.local/bin/monarch-led-hook. Missing hook is fine; recording is silent
// by design (meetings, no on-screen REC dot). Reaped in a goroutine so the
// hook (which may block briefly on solaar HID++ I/O) neither becomes a
// zombie under the long-lived supervisor nor delays supervisor shutdown.
func scLedHook(stage string) {
	hook := os.Getenv("MONARCH_LED_HOOK")
	if hook == "" {
		hook = filepath.Join(os.Getenv("HOME"), ".local/bin/monarch-led-hook")
	}
	if _, err := os.Stat(hook); err != nil {
		return
	}
	c := exec.Command(hook, stage)
	if err := c.Start(); err != nil {
		return
	}
	go func() { _ = c.Wait() }()
}

func (cli *Client) ScreencastStart(cCtx *cli.Context) error {
	if pid, alive := scAlive(); alive {
		log.Printf("screencast already running pid=%d", pid)
		return nil
	}
	if err := os.MkdirAll(scOutdir, 0o755); err != nil {
		return err
	}
	if free, err := scFreeBytes(scOutdir); err == nil && free < scDiskFreeMinByte() {
		return fmt.Errorf("disk free %d bytes < min %d", free, scDiskFreeMinByte())
	}

	resolution := "1920x1080"
	if scrnsaver.HasXorg() {
		resolution = scrnsaver.GetResolution()
	}
	display := os.Getenv("DISPLAY")
	if display == "" {
		display = ":0"
	}

	mic, _ := scPactl("get-default-source")
	sink, _ := scPactl("get-default-sink")
	if mic == "" || sink == "" {
		return fmt.Errorf("pulse default source/sink unavailable (mic=%q sink=%q)", mic, sink)
	}
	sysmon := sink + ".monitor"

	hostname, _ := os.Hostname()
	sessTs := time.Now().Format("20060102-150405")
	segPattern := fmt.Sprintf("%s/%s.%s.%%Y%%m%%d-%%H%%M%%S.raw.mkv", scOutdir, hostname, sessTs)
	rawGlob := fmt.Sprintf("%s/%s.%s.*.raw.mkv", scOutdir, hostname, sessTs)

	args := []string{
		"-nostdin", "-hide_banner", "-loglevel", "warning", "-y",
		"-t", strconv.Itoa(int(scHardCapDur().Seconds())),
		"-thread_queue_size", "4096", "-framerate", scFramerate, "-f", "x11grab",
		"-video_size", resolution, "-i", display,
		"-thread_queue_size", "4096", "-f", "pulse", "-i", mic,
		"-thread_queue_size", "4096", "-f", "pulse", "-i", sysmon,
		"-filter_complex", "[1:a][2:a]amix=inputs=2:duration=longest:dropout_transition=0,aresample=async=1:first_pts=0[a]",
		"-map", "0:v", "-map", "[a]",
		"-c:v", "libx264", "-preset", scPreset, "-crf", scCrf, "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "192k", "-ar", "48000",
		"-f", "segment", "-segment_time", strconv.Itoa(scSegmentSec),
		"-reset_timestamps", "1", "-strftime", "1",
		segPattern,
	}

	logf, err := os.Create(scLogPath)
	if err != nil {
		return err
	}
	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		logf.Close()
		return err
	}
	scLedHook("start")

	state := screencastState{
		Pid:        os.Getpid(),
		FfmpegPid:  cmd.Process.Pid,
		Started:    time.Now().Format(time.RFC3339),
		RawGlob:    rawGlob,
		IdleStopNs: int64(scIdleStopDur()),
		HardCapNs:  int64(scHardCapDur()),
	}
	if err := scWriteHeartbeat(state); err != nil {
		// No PID file means `screencast stop` can't find us, and a
		// supervisor crash would orphan ffmpeg. Refuse to start.
		_ = cmd.Process.Signal(syscall.SIGINT)
		_, _ = cmd.Process.Wait()
		_ = logf.Close()
		scLedHook("stop")
		return fmt.Errorf("heartbeat write failed: %w", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	done := make(chan error, 1)
	go func() {
		// close so the post-loop drain doesn't deadlock when ffmpeg
		// exits on its own (e.g. -t hard cap fires before any signal).
		defer close(done)
		done <- cmd.Wait()
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	start := time.Now()

	stopReason := ""
	stopFFmpeg := func(reason string) {
		if stopReason != "" {
			return
		}
		stopReason = reason
		log.Printf("stopping ffmpeg: %s", reason)
		_ = cmd.Process.Signal(syscall.SIGINT)
	}

loop:
	for {
		select {
		case sig := <-sigCh:
			stopFFmpeg(fmt.Sprintf("signal %s", sig))
			break loop
		case <-done:
			stopReason = "ffmpeg exited"
			break loop
		case <-ticker.C:
			if scrnsaver.HasXorg() {
				if info, err := scrnsaver.GetXScreenSaverInfo(); err == nil {
					if info.Idle > scIdleStopDur() {
						stopFFmpeg(fmt.Sprintf("idle %v > %v", info.Idle, scIdleStopDur()))
						break loop
					}
				}
			}
			if free, err := scFreeBytes(scOutdir); err == nil && free < scDiskFreeMinByte() {
				stopFFmpeg(fmt.Sprintf("disk free %d < %d", free, scDiskFreeMinByte()))
				break loop
			}
			// Wall-clock cap. ffmpeg's `-t` is unreliable as a real
			// hard cap when combined with `-f segment` — the segment
			// muxer keeps running. Drive the cap from the supervisor;
			// the post-loop 30s drain will SIGKILL if SIGINT stalls.
			if time.Since(start) > scHardCapDur() {
				stopFFmpeg(fmt.Sprintf("wall cap %v exceeded", scHardCapDur()))
				break loop
			}
		}
	}

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}

	scLedHook("stop")
	_ = os.Remove(scHeartbeatPath())
	_ = logf.Close()
	log.Printf("screencast finished: reason=%q", stopReason)
	return nil
}

func (cli *Client) ScreencastStop(cCtx *cli.Context) error {
	// Print explicitly: core/main.go's wrapper swallows action errors.
	st, err := scReadHeartbeat()
	if err != nil {
		fmt.Fprintln(os.Stderr, "screencast: not running (no heartbeat)")
		return err
	}
	if err := syscall.Kill(st.Pid, 0); err != nil {
		_ = os.Remove(scHeartbeatPath())
		fmt.Fprintf(os.Stderr, "screencast: stale heartbeat pid=%d removed\n", st.Pid)
		return err
	}
	if err := syscall.Kill(st.Pid, syscall.SIGTERM); err != nil {
		fmt.Fprintf(os.Stderr, "screencast: kill pid=%d: %v\n", st.Pid, err)
		return err
	}
	return nil
}

func (cli *Client) ScreencastStatus(cCtx *cli.Context) error {
	st, err := scReadHeartbeat()
	if err != nil {
		fmt.Println("not running")
		return nil
	}
	if err := syscall.Kill(st.Pid, 0); err != nil {
		fmt.Printf("stale heartbeat: pid %d gone\n", st.Pid)
		return nil
	}
	fmt.Printf("running pid=%d ffmpeg=%d started=%s\n",
		st.Pid, st.FfmpegPid, st.Started)

	// Elapsed + remaining-on-hard-cap. The supervisor watches both,
	// but printing it here lets the user spot-check without tailing logs.
	if started, err := time.Parse(time.RFC3339, st.Started); err == nil {
		elapsed := time.Since(started).Round(time.Second)
		cap := time.Duration(st.HardCapNs)
		idle := time.Duration(st.IdleStopNs)
		remain := (cap - elapsed).Round(time.Second)
		if remain < 0 {
			remain = 0
		}
		fmt.Printf("elapsed=%s  cap=%s  remaining=%s  idle_stop=%s\n",
			elapsed, cap, remain, idle)
	}

	// Segment count + total bytes on disk so far.
	segs, _ := filepath.Glob(st.RawGlob)
	var total int64
	for _, s := range segs {
		if fi, err := os.Stat(s); err == nil {
			total += fi.Size()
		}
	}
	fmt.Printf("segments=%d  bytes_on_disk=%d  glob=%s\n",
		len(segs), total, st.RawGlob)
	return nil
}
