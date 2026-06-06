package compress

import (
	"fmt"
	"os"
	"time"

	"github.com/JelmerDeHen/scrnsaver"
)

type Pipeline struct {
	Service    string
	Indir      string
	Outdir     string
	InfileExt  string
	OutfileExt string
	Argv       []string
}

// var Pipelines map[string]*Pipeline
var Pipelines = make(map[string]*Pipeline)

/*
TODO: parse this from config file like
{
  "x11grab": {
    "Service": "monarch_x11grab",
    "Indir": "/data/mon/srec_new",
    "Outdir": "/data/mon/srec_new_compress",
    "IndirExt": "mkv",
    "OutdirExt": "mp4",
    "Argv": ["-an"],
  },
  ...
}
*/

// ffmpeg -t 10 -f x11grab -i :0 -r 30 -video_size 5120x1440 -draw_mouse 0 -vcodec libx265 -vf "monochrome,mpdecimate" -fps_mode vfr -crf 30  /tmp/copy30.mp4
func init() {
	Pipelines["x11grab"] = &Pipeline{
		Service:    "monarch_x11grab",
		Indir:      "/data/mon/x11grab",
		Outdir:     "/data/mon/x11grab_compress",
		InfileExt:  "mkv",
		OutfileExt: "mp4",
		Argv:       []string{
      "-an",
      "-vf", "monochrome,mpdecimate",
      "-fps_mode", "vfr",
      "-crf", "30",
    },
	}
	Pipelines["arecord"] = &Pipeline{
		Service:    "monarch_arecord",
		Indir:      "/data/mon/arecord",
		Outdir:     "/data/mon/arecord_compress",
		InfileExt:  "wav",
		OutfileExt: "mp3",
		Argv:       []string{},
	}
	Pipelines["v4l2"] = &Pipeline{
		Service:    "monarch_v4l2",
		Indir:      "/data/mon/v4l2",
		Outdir:     "/data/mon/v4l2_compress",
		InfileExt:  "mkv",
		OutfileExt: "mp4",
		Argv:       []string{"-an"},
	}
	// screencast: re-encode ffmpeg segment chunks from the G4-triggered
	// recorder. Keep audio (mic+sysmon mix recorded inside the segment).
	Pipelines["screencast"] = &Pipeline{
		Service:    "monarch_screencast",
		Indir:      "/data/mon/screencast",
		Outdir:     "/data/mon/screencast_compress",
		InfileExt:  "mkv",
		OutfileExt: "mp4",
		Argv: []string{
			// No explicit -profile/-level: multi-monitor captures
			// (e.g. 7680x2160) blow past level 5.1 constraints.
			"-c:v", "libx264", "-preset", "slow", "-crf", "22",
			"-pix_fmt", "yuv420p",
			"-c:a", "copy",
			"-movflags", "+faststart",
		},
	}
}

func (p *Pipeline) VerifyDirAccess() error {
	// Check p.Indir
	fi, err := os.Stat(p.Indir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", p.Indir)
	}

	//fmt.Println(fi.IsDir(), err)

	// Check if p.Outdir exists
	fi, err = os.Stat(p.Outdir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", p.Outdir)
	}

	// Check if outdir is writable
	tmpfile := fmt.Sprintf("%s/.test", p.Outdir)
	_, err = os.Create(tmpfile)
	if err != nil {
		return err
	}
	err = os.Remove(tmpfile)
	if err != nil {
		return err
	}

	return nil
}

func errIfNotIdleFor(t time.Duration) error {
	// Check if user has been idle for long enough to start compressing
	info, err := scrnsaver.GetXScreenSaverInfo()
	if err != nil {
		return err
	}

	if info.Idle < t {
		return fmt.Errorf("Stop: user is not afk")
	}
	return nil
}

func (p *Pipeline) Scan() error {
	// Finds & removes empty files
	err := p.RemoveEmptyFiles()
	if err != nil {
		return err
	}

	// Finds & removes lockfiles
	err = p.ReleaseLocks()
	if err != nil {
		return err
	}

	// Find & removes files in Outdir that have not been completely processed
	err = p.RemovePartialCompressions()
	if err != nil {
		return err
	}

	// Look for completed jobs & remove infiles
	err = p.RemoveProcessedFiles()
	if err != nil {
		return err
	}

	// Verify access to Indir & Outdir
	err = p.VerifyDirAccess()
	if err != nil {
		return err
	}

	// Read Indir contents
	fps, err := p.getFilePathsByIndir()
	if err != nil {
		return err
	}

	for _, fp := range fps {
    /*
		// Only run when user was afk for at least one minute
		err = errIfNotIdleFor(time.Minute)
		if err != nil {
			return err
		}
    */

		// Check for locks
		if fp.InLock.IsLocked() {
			fmt.Printf("Scan(): Skip: found infile lock:\t\t%q\n", fp.InLock)
			continue
		}

		if fp.Done.IsLocked() {
			fmt.Printf("Scan(): Skip: found done lock:\t\t%q\n", fp.Done)
			continue
		}

		if fp.OutLock.IsLocked() {
			fmt.Printf("Scan(): Skip: found outfile lock:\t%q\n", fp.OutLock)
			continue
		}

		// Check if outfile already exists
		_, err = os.Stat(fp.Out)
		if err == nil {
			// We could just rm it here and continue execution
			// This will be picked up by RemovePartialCompressions() in future run
			//fmt.Printf("Scan(): Outfile has no lock or done file: rm %q\n", fp.Out)
			//os.Remove(fp.Out)
			continue
		}

		if isFileBusy(fp.In) {
			fmt.Printf("Scan(): Infile is busy: %q\n", fp.In)
			continue
		}

		// Prepare for exec - setup locks
		err = fp.InLock.Lock()
		if err != nil {
			fmt.Println(err)
			continue
		}
		err = fp.OutLock.Lock()
		if err != nil {
			fmt.Println(err)
			continue
		}

		// Compress
		err = FfmpegCompress(fp.In, fp.Out, p.Argv)
		if err != nil {
			fmt.Println(err)
			continue
		}

		err = fp.Done.Lock()
		if err != nil {
			fmt.Println(err)
			continue
		}

		// Release the infile and outfile locks
		err = fp.InLock.Release()
		if err != nil {
			fmt.Println(err)
			continue
		}

		err = fp.OutLock.Release()
		if err != nil {
			fmt.Println(err)
			continue
		}
	}
	return nil
}
