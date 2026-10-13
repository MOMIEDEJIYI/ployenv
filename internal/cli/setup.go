package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/fsutil"
	"github.com/MOMIEDEJIYI/ployenv/internal/logx"
	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
)

// maybeRunSetup performs the one-time base configuration. It runs the first
// time ployenv is used (no config.json yet) so the tool never silently decides
// where its data lives: it states the location and writes the config.
//
// It is interactive only on a real TTY; on a pipe/script, or with --yes, it
// proceeds with the detected defaults so automation never hangs.
func maybeRunSetup(g globals) error {
	cfgPath := config.ConfigPath()
	if fsutil.FileExists(cfgPath) {
		return nil
	}
	root, src, writable := config.RootInfo()

	logx.Infof("first run: no config file found, writing the base configuration.")
	logx.Infof("  data dir  %s", root)
	logx.Infof("  source    %s", src)

	if !writable {
		return fmt.Errorf("data directory is not writable: %s\nset PLOYENV_ROOT to a writable directory and try again", root)
	}

	interactive := !g.yes && !g.json &&
		platform.IsTerminal(os.Stdin) && platform.IsTerminal(os.Stderr)

	if interactive {
		fmt.Fprint(os.Stderr, "Use this data directory? [Y/n] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "n", "no":
			// The recipe has to be one the user's platform can actually run:
			// setx exists only on Windows (REQ-008 A7).
			hint := `  export PLOYENV_ROOT=/path/to/ployenv   (then restart the shell)`
			if platform.IsWindows() {
				hint = `  setx PLOYENV_ROOT "D:\PloyEnv"   (then reopen the terminal)`
			}
			return fmt.Errorf("cancelled. To pick a different data directory, set PLOYENV_ROOT first, e.g.:\n%s", hint)
		}
	}

	cfg := config.Load()
	cfg.Root = root
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("failed to write the base configuration: %w", err)
	}
	logx.Stepf("base configuration written: %s", cfgPath)
	return nil
}
