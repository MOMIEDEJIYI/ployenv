package cli

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/fsutil"
	"github.com/MOMIEDEJIYI/ployenv/internal/jsonx"
	"github.com/MOMIEDEJIYI/ployenv/internal/logx"
	"github.com/MOMIEDEJIYI/ployenv/internal/netx"
	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
	"github.com/MOMIEDEJIYI/ployenv/internal/resolve"
	"github.com/MOMIEDEJIYI/ployenv/internal/runtime"
	"github.com/MOMIEDEJIYI/ployenv/internal/version"
)

// Version is the ployenv release string.
const Version = "0.3.4"

// defaultAvailableLimit caps how many versions "available" prints by default,
// so a runtime with hundreds of releases does not scroll forever.
const defaultAvailableLimit = 20

// helpText is helpTextFor for the host platform.
func helpText() string { return helpTextFor(platform.IsWindows()) }

// helpTextFor takes the platform as an argument, in the same spirit as the OS
// seams elsewhere: it is what makes both spellings of the help assertable from
// any runner rather than only the one that happens to run there.
func helpTextFor(onWindows bool) string {
	shells := "bash|zsh|sh|json"
	if onWindows {
		shells = "pwsh|cmd|bash|json"
	}
	return strings.ReplaceAll(helpTemplate, "{{shells}}", shells)
}

const helpTemplate = `ployenv - portable multi-runtime version manager

usage: ployenv <command> [args]

managed runtimes: node, java, go, python (aliases: nodejs / jdk / golang / py)

inspect:
  ployenv support                     list the runtimes ployenv can manage
  ployenv list [lang]                 installed versions (all runtimes when no lang)
  ployenv available <lang> [filter]   installable versions (e.g. 22, lts, 3.12);
                                      shows the newest 20 unless --limit/--all
  ployenv current [lang]              effective version and where it comes from
  ployenv which <tool>                the program a tool would actually run
  ployenv doctor                      self-check and environment report

manage:
  ployenv install <lang>@<version>            download and install (22 / lts / latest)
  ployenv install <lang>@<version> --from <dir>   register an existing install, no download
  ployenv adopt <lang>[@version] --root <dir>     explicitly register an existing directory
  ployenv uninstall <lang>@<version>          remove (adopted dirs are only unregistered)
  ployenv uninstall <lang> --all              remove every version of a runtime
  ployenv use <lang>@<version> [--project]    set the global default / write .ployenv

session activation (never modifies the system environment):
  ployenv env [--shell {{shells}}]    print the session environment, eval it yourself
  ployenv shell [--shell ...]                 open a child terminal with it activated
  note: "use" only records a version; activate with one of the two commands above.

configuration:
  ployenv config                                show every setting
  ployenv config --set-mirror <lang> <url>      set a mirror (none restores the official source)
  ployenv config --set-default <lang> <ver>     set the global default version (none clears it)
  ployenv config --set-log-mode <mode>          log mode (off|on-error|always)
  ployenv config --set-cache-keep <true|false>  keep downloaded archives after install
  ployenv cache [--clean]                       show / empty the download cache

global options: --json  --verbose  --debug  -q  --log  --no-color

note: --dry-run is not implemented; the flag is rejected instead of
      pretending to dry-run while installing for real.

pinning a project: create .ployenv in the project root, one entry per line, e.g.
  node=22.11.0
  java=21
.nvmrc, .node-version, .java-version, .python-version, .go-version, .tool-versions
and go.mod are also read (read-only).
`

type globals struct {
	json     bool
	verbose  bool
	quiet    bool
	debug    bool
	forceLog bool
	noColor  bool
	yes      bool
}

// Run parses global options, dispatches the command and flushes diagnostics.
func Run(args []string) int {
	// Print non-ASCII paths (e.g. a Chinese user name) correctly on legacy
	// Windows consoles, then restore the previous code page.
	restoreConsole := platform.UseUTF8Console()
	defer restoreConsole()

	g := parseGlobals(&args)

	cfg := config.Load()
	logDir := config.LogsDir()
	mode := logx.FileOnError
	keep := 20
	if cfg.Logs != nil {
		if cfg.Logs.Mode != "" {
			mode = logx.FileMode(cfg.Logs.Mode)
		}
		if cfg.Logs.Keep > 0 {
			keep = cfg.Logs.Keep
		}
	}
	if g.forceLog {
		mode = logx.FileAlways
	}
	level := logx.LevelNormal
	if g.quiet {
		level = logx.LevelQuiet
	} else if g.verbose {
		level = logx.LevelVerbose
	}
	if g.debug {
		level = logx.LevelDebug
	}
	color := !g.noColor && platform.IsTerminal(os.Stderr) && os.Getenv("NO_COLOR") == ""
	logx.Configure(level, color, logDir, mode, keep)

	// A flag the parser accepts but nothing implements must fail loudly: an
	// install that silently ignores --dry-run installs for real (BUG-020).
	if err := rejectUnimplementedFlags(args); err != nil {
		logx.Errorf("%v", err)
		logx.Flush(config.RootDir())
		return 1
	}

	// Ctrl+C must not leave a partial download behind; also sweep leftovers
	// from a run that was killed before it could clean up.
	netx.PurgePartials(config.CacheDir())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		netx.Abort()
		// os.Exit skips deferred calls, so restore the console code page here:
		// leaving the user's terminal on 65001 would be a visible side effect
		// (NFR-18, BUG-012).
		restoreConsole()
		logx.Flush(config.RootDir())
		fmt.Fprintln(os.Stderr, "\nInterrupted.")
		os.Exit(130)
	}()

	if len(args) == 0 {
		fmt.Print(helpText())
		logx.Flush(config.RootDir())
		return 0
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "-help", "--help", "-?", "version", "-v", "--version":
		// Pure help/version never touches the data directory.
	default:
		// FR-809: without a determinable data directory every path below would
		// silently become relative to the working directory, so the run stops
		// here with an actionable message instead of scattering data around.
		if err := config.RootUsable(); err != nil {
			logx.Errorf("%v", err)
			return 1
		}
		if err := maybeRunSetup(g); err != nil {
			logx.Errorf("%v", err)
			logx.Flush(config.RootDir())
			return 1
		}
	}
	if err := dispatch(cmd, rest, g); err != nil {
		logx.Errorf("%v", err)
		logx.Flush(config.RootDir())
		return 1
	}
	logx.Flush(config.RootDir())
	return 0
}

func parseGlobals(args *[]string) globals {
	var g globals
	var out []string
	for _, a := range *args {
		switch a {
		case "-q", "--quiet":
			g.quiet = true
		case "--verbose":
			g.verbose = true
		case "--debug":
			g.debug = true
		case "--json":
			g.json = true
		case "--log":
			g.forceLog = true
		case "--no-color":
			g.noColor = true
		case "-y", "--yes":
			g.yes = true
		default:
			out = append(out, a)
		}
	}
	*args = out
	return g
}

func dispatch(cmd string, rest []string, g globals) error {
	switch cmd {
	case "help", "-h", "-help", "--help", "-?":
		fmt.Print(helpText())
		return nil
	case "version", "-v", "--version":
		fmt.Printf("ployenv %s (%s/%s)\n", Version, platform.OS(), platform.Arch())
		return nil
	case "list", "ls":
		return cmdList(rest, g)
	case "available", "list-remote", "ls-remote":
		return cmdAvailable(rest, g)
	case "install", "add":
		return cmdInstall(rest, g)
	case "adopt", "import":
		return cmdAdopt(rest, g)
	case "uninstall", "remove", "rm":
		return cmdUninstall(rest, g)
	case "use":
		return cmdUse(rest, g)
	case "current":
		return cmdCurrent(rest, g)
	case "which":
		return cmdWhich(rest, g)
	case "support", "supported", "runtimes", "langs", "envs":
		return cmdRuntimes(rest, g)
	case "cache":
		return cmdCache(rest, g)
	case "env":
		return cmdEnv(rest, g)
	case "shell":
		return cmdShell(rest, g)
	case "config":
		return cmdConfig(rest, g)
	case "doctor":
		return cmdDoctor(rest, g)
	}
	return fmt.Errorf("unknown command %q (run \"ployenv help\" for usage)", cmd)
}

func specOrFail(name string) (*runtime.Spec, error) {
	s := runtime.Lookup(name)
	if s == nil {
		return nil, fmt.Errorf("unsupported runtime %q (supported: %s)", name, runtime.NamesText())
	}
	return s, nil
}

func cmdList(rest []string, g globals) error {
	pos, _ := splitFlags(rest)
	explicit := len(pos) > 0
	var specs []*runtime.Spec
	if explicit {
		s, err := specOrFail(pos[0])
		if err != nil {
			return err
		}
		specs = []*runtime.Spec{s}
	} else {
		specs = runtime.All()
	}

	// BUG-016: --json has to be real JSON here too, not a silent fallback to the
	// human-readable listing.
	if g.json {
		rows := []listRow{}
		for _, s := range specs {
			installed := config.InstalledVersions(s.Name)
			if len(installed) == 0 && !explicit {
				continue
			}
			active := resolve.For(s)
			for _, v := range installed {
				row := listRow{Lang: s.Name, Version: v, Dir: config.VersionDir(s.Name, v)}
				if active.Version == v {
					row.Current = true
					row.Source = active.Source.Label()
					row.Detail = active.Detail
				}
				rows = append(rows, row)
			}
		}
		return printJSON(rows)
	}

	printed := false
	for _, s := range specs {
		installed := config.InstalledVersions(s.Name)
		// Without an explicit language, show only what is actually installed.
		if len(installed) == 0 && !explicit {
			continue
		}
		if printed {
			fmt.Println()
		}
		printed = true
		fmt.Printf("%s (%s)\n", s.Name, s.Title)
		if len(installed) == 0 {
			fmt.Println("  (not installed)")
			continue
		}
		active := resolve.For(s)
		for _, v := range installed {
			line := fmt.Sprintf("  %-14s", v)
			if active.Version == v {
				line += fmt.Sprintf("  <- current [%s]%s", active.Source.Label(), detailSuffix(active.Detail))
			}
			fmt.Println(strings.TrimRight(line, " "))
		}
	}
	if !printed {
		fmt.Println("No runtimes installed yet.")
		fmt.Println("  supported languages:  ployenv support")
		fmt.Println("  installable versions: ployenv available <lang>")
	}
	return nil
}

// listRow is one entry of `list --json`.
type listRow struct {
	Lang    string `json:"lang"`
	Version string `json:"version"`
	Current bool   `json:"current"`
	Source  string `json:"source,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Dir     string `json:"dir"`
}

func cmdAvailable(rest []string, g globals) error {
	pos, flags := splitFlags(rest)
	if len(pos) == 0 {
		return fmt.Errorf("usage: ployenv available <lang> [filter] [--limit N] (shows the newest %d by default)", defaultAvailableLimit)
	}
	s, err := specOrFail(pos[0])
	if err != nil {
		return err
	}
	filter := ""
	if len(pos) > 1 {
		filter = pos[1]
	}
	logx.Stepf("Fetching available %s versions...", s.Name)
	rels, err := s.List(filter)
	if err != nil {
		return err
	}
	sort.Slice(rels, func(i, j int) bool { return version.Compare(rels[i].Version, rels[j].Version) > 0 })

	if g.json {
		// Machine output keeps the full list unless --limit is given.
		if v := value(flags, "limit"); v != "" {
			if n, e := strconv.Atoi(v); e == nil && n > 0 && n < len(rels) {
				rels = rels[:n]
			}
		}
		return printJSON(rels)
	}
	limit := defaultAvailableLimit
	if has(flags, "all") {
		limit = 0
	} else if v := value(flags, "limit"); v != "" {
		if n, e := strconv.Atoi(v); e == nil && n >= 0 {
			limit = n
		}
	}
	if len(rels) == 0 {
		fmt.Println("No matching versions.")
		return nil
	}
	shown := len(rels)
	if limit > 0 && limit < shown {
		shown = limit
	}
	for i := 0; i < shown; i++ {
		r := rels[i]
		if r.Note != "" {
			fmt.Printf("  %-16s %s\n", r.Version, r.Note)
		} else {
			fmt.Printf("  %s\n", r.Version)
		}
	}
	if shown < len(rels) {
		fmt.Printf("  ... (%d total, showing the newest %d; --limit 0 or --all for the full list)\n", len(rels), shown)
	}
	return nil
}

func cmdCurrent(rest []string, g globals) error {
	pos, _ := splitFlags(rest)
	var results []resolve.Result
	if len(pos) > 0 {
		s, err := specOrFail(pos[0])
		if err != nil {
			return err
		}
		results = []resolve.Result{resolve.For(s)}
	} else {
		results = resolve.All()
	}
	return printResolutions(results, g)
}

func cmdWhich(rest []string, g globals) error {
	pos, _ := splitFlags(rest)
	if len(pos) == 0 {
		return fmt.Errorf("usage: ployenv which <tool>")
	}
	tool := strings.ToLower(strings.TrimSuffix(pos[0], ".exe"))
	spec := findTool(tool)
	if spec == nil {
		return fmt.Errorf("unknown tool %q", pos[0])
	}
	r := resolve.For(spec)
	if r.Err != nil {
		return r.Err
	}
	if r.Version == "" {
		return fmt.Errorf("%s has no version configured; run ployenv use %s@<version> first", spec.Name, spec.Name)
	}
	rel := spec.ToolPath(tool, r.Version)
	if rel == "" {
		rel = spec.ToolPath(spec.Primary, r.Version)
	}
	fmt.Println(filepath.Join(r.Dir, rel))
	return nil
}

// dirState is one managed directory as reported by `doctor --json`.
type dirState struct {
	Path    string `json:"path"`
	Present bool   `json:"present"`
}

// doctorReport is the machine-readable form of `doctor` (FR-18).
type doctorReport struct {
	Version       string          `json:"version"`
	OS            string          `json:"os"`
	Arch          string          `json:"arch"`
	Exe           string          `json:"exe"`
	DataDir       string          `json:"data_dir"`
	DataSource    string          `json:"data_source"`
	Writable      bool            `json:"writable"`
	Config        string          `json:"config"`
	ConfigPresent bool            `json:"config_present"`
	Versions      dirState        `json:"versions"`
	Cache         dirState        `json:"cache"`
	Logs          dirState        `json:"logs"`
	PathHint      string          `json:"path_hint,omitempty"`
	Runtimes      []resolutionRow `json:"runtimes"`
}

func dirStateOf(path string) dirState {
	return dirState{Path: path, Present: fsutil.DirExists(path)}
}

// pathHint returns the directory to add to PATH, or "" when it is already there.
func pathHint(exeDir string) string {
	if exeDir == "" || pathHas(exeDir) {
		return ""
	}
	return exeDir
}

func cmdDoctor(rest []string, g globals) error {
	dir, src, writable := config.RootInfo()
	exeDir := config.ExeDir()
	results := resolve.All()

	// BUG-016: doctor advertised --json but always printed its human report.
	if g.json {
		return printJSON(doctorReport{
			Version:       Version,
			OS:            platform.OS(),
			Arch:          platform.Arch(),
			Exe:           config.ExePath(),
			DataDir:       dir,
			DataSource:    string(src),
			Writable:      writable,
			Config:        config.ConfigPath(),
			ConfigPresent: fsutil.FileExists(config.ConfigPath()),
			Versions:      dirStateOf(config.VersionsDir()),
			Cache:         dirStateOf(config.CacheDir()),
			Logs:          dirStateOf(config.LogsDir()),
			PathHint:      pathHint(exeDir),
			Runtimes:      resolutionRows(results),
		})
	}

	fmt.Printf("ployenv %s (%s/%s)\n", Version, platform.OS(), platform.Arch())
	fmt.Printf("executable  %s\n", config.ExePath())
	fmt.Println()
	fmt.Printf("data dir    %s\n", dir)
	fmt.Printf("  source    %s\n", src)
	fmt.Printf("  writable  %s\n", yesNo(writable))
	fmt.Printf("  config    %s (%s)\n", config.ConfigPath(), presentLabel(config.ConfigPath()))
	for _, d := range []struct{ name, path string }{
		{"versions", config.VersionsDir()},
		{"cache", config.CacheDir()},
		{"logs", config.LogsDir()},
	} {
		state := "missing"
		if fsutil.DirExists(d.path) {
			state = "present"
		}
		fmt.Printf("  %-8s  %s (%s)\n", d.name, d.path, state)
	}
	if hint := pathHint(exeDir); hint != "" {
		fmt.Println()
		fmt.Println("hint: add this directory to PATH to run ployenv from anywhere:")
		fmt.Printf("      %s\n", hint)
	}
	fmt.Println()
	return printResolutions(results, g)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func presentLabel(p string) string {
	if fsutil.FileExists(p) {
		return "present"
	}
	return "not created yet"
}

// detailSuffix renders the "where it came from" part of a resolution line.
func detailSuffix(detail string) string {
	if strings.TrimSpace(detail) == "" {
		return ""
	}
	return " " + detail
}

// pathHas reports whether dir is one of the current PATH entries.
func pathHas(dir string) bool {
	for _, e := range filepath.SplitList(os.Getenv("PATH")) {
		if e != "" && strings.EqualFold(filepath.Clean(e), filepath.Clean(dir)) {
			return true
		}
	}
	return false
}

// cmdRuntimes lists the runtimes ployenv knows how to manage.
func cmdRuntimes(rest []string, g globals) error {
	cfg := config.Load()
	specs := runtime.All()

	if g.json {
		type row struct {
			Name      string   `json:"name"`
			Title     string   `json:"title"`
			Kind      string   `json:"kind"`
			Aliases   []string `json:"aliases,omitempty"`
			Tools     []string `json:"tools,omitempty"`
			Installed []string `json:"installed"`
			Default   string   `json:"default,omitempty"`
			Mirror    string   `json:"mirror,omitempty"`
		}
		rows := []row{}
		for _, s := range specs {
			rows = append(rows, row{
				Name: s.Name, Title: s.Title, Kind: s.Kind,
				Aliases:   s.Aliases,
				Tools:     s.Tools(),
				Installed: orEmpty(config.InstalledVersions(s.Name)),
				Default:   cfg.Defaults[s.Name],
				Mirror:    cfg.Mirrors[s.Name],
			})
		}
		return printJSON(rows)
	}

	fmt.Printf("ployenv manages %d runtimes (languages/environments):\n\n", len(specs))
	for _, s := range specs {
		fmt.Printf("  %-8s %s\n", s.Name, s.Title)
		if len(s.Aliases) > 0 {
			fmt.Printf("           aliases:   %s\n", strings.Join(s.Aliases, ", "))
		}
		fmt.Printf("           tools:     %s\n", strings.Join(s.Tools(), ", "))
		if s.Kind != "" && s.Kind != "language" {
			fmt.Printf("           kind:      %s\n", s.Kind)
		}
		fmt.Printf("           mirror:    %s\n", mirrorLabel(s, cfg))
		if installed := config.InstalledVersions(s.Name); len(installed) == 0 {
			fmt.Printf("           installed: none\n")
		} else {
			fmt.Printf("           installed: %s\n", strings.Join(installed, ", "))
		}
	}
	fmt.Println()
	fmt.Println("usage: ployenv install <lang>@<version>    e.g. ployenv install node@lts")
	fmt.Println("       ployenv available <lang>            list installable versions")
	return nil
}

func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// cmdCache shows, or with --clean empties, the download cache. Archives are
// removed after a successful install by default (config cache.keep turns that
// off).
func cmdCache(rest []string, g globals) error {
	_, flags := splitFlags(rest)
	dir := config.CacheDir()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	type cacheFile struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	files := []cacheFile{}
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		files = append(files, cacheFile{e.Name(), info.Size()})
		total += info.Size()
	}

	if has(flags, "clean") {
		for _, f := range files {
			if rerr := os.Remove(filepath.Join(dir, f.Name)); rerr != nil {
				return rerr
			}
		}
		fmt.Printf("Cache emptied: %d file(s), %s freed\n", len(files), fsutil.HumanBytes(total))
		return nil
	}

	if g.json {
		return printJSON(struct {
			Dir   string      `json:"dir"`
			Total int64       `json:"total_bytes"`
			Files []cacheFile `json:"files"`
		}{dir, total, files})
	}

	fmt.Printf("cache dir  %s\n", dir)
	if len(files) == 0 {
		fmt.Println("(empty)")
		return nil
	}
	for _, f := range files {
		fmt.Printf("  %-44s %s\n", f.Name, fsutil.HumanBytes(f.Size))
	}
	fmt.Printf("%d file(s), %s\n", len(files), fsutil.HumanBytes(total))
	fmt.Println("hint: ployenv cache --clean empties it; archives are deleted after a successful install unless cache.keep=true.")
	return nil
}

// resolutionRow is the machine-readable form of one resolved runtime.
type resolutionRow struct {
	Lang    string `json:"lang"`
	Version string `json:"version,omitempty"`
	Source  string `json:"source"`
	Detail  string `json:"detail,omitempty"`
	Dir     string `json:"dir,omitempty"`
	Error   string `json:"error,omitempty"`
}

// resolutionRows renders only the configured runtimes, shared by `current`,
// `doctor --json` and anything else that reports resolutions.
func resolutionRows(results []resolve.Result) []resolutionRow {
	rows := []resolutionRow{}
	for _, r := range results {
		if !r.Configured() {
			continue
		}
		rr := resolutionRow{Lang: r.Lang, Version: r.Version, Source: r.Source.Label(), Detail: r.Detail, Dir: r.Dir}
		if r.Err != nil {
			rr.Error = r.Err.Error()
		}
		rows = append(rows, rr)
	}
	return rows
}

// printResolutions renders resolved versions, only for configured runtimes.
func printResolutions(results []resolve.Result, g globals) error {
	if g.json {
		return printJSON(resolutionRows(results))
	}

	anyConfigured, anyGlobal, anyProject := false, false, false
	var firstErr error
	for _, r := range results {
		if !r.Configured() {
			continue
		}
		anyConfigured = true
		switch r.Source {
		case resolve.SourceGlobal:
			anyGlobal = true
		case resolve.SourceProject:
			anyProject = true
		}
		if r.Err != nil && firstErr == nil {
			firstErr = r.Err
		}
	}
	if !anyConfigured {
		fmt.Println("Nothing configured yet. Run ployenv use <lang>@<version> first.")
		return nil
	}
	if anyGlobal && !anyProject {
		fmt.Println("No project file (.ployenv) found; using global defaults:")
	}
	for _, r := range results {
		if !r.Configured() {
			continue
		}
		if r.Err != nil {
			fmt.Printf("%-8s %s\n", r.Lang, r.Err)
			continue
		}
		fmt.Printf("%-8s %-14s [%s]%s\n", r.Lang, r.Version, r.Source.Label(), detailSuffix(r.Detail))
	}
	return firstErr
}

func findTool(tool string) *runtime.Spec {
	for _, s := range runtime.All() {
		if tool == s.Primary {
			return s
		}
		for _, t := range s.Tools() {
			if t == tool {
				return s
			}
		}
	}
	return nil
}

// printJSON is the only place ployenv writes machine output: jsonx keeps it
// valid, pure ASCII and independent of the console code page (FR-18).
func printJSON(v any) error {
	out, err := jsonx.Encode(v)
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}
