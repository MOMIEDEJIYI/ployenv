package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/envx"
	"github.com/MOMIEDEJIYI/ployenv/internal/install"
	"github.com/MOMIEDEJIYI/ployenv/internal/logx"
	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
	"github.com/MOMIEDEJIYI/ployenv/internal/resolve"
	"github.com/MOMIEDEJIYI/ployenv/internal/runtime"
)

func cmdInstall(rest []string, g globals) error {
	pos, flags := splitFlags(rest)
	if len(pos) == 0 {
		return fmt.Errorf("usage: ployenv install <lang>@<version> [--from <dir>] [--force]")
	}
	lang, ver := splitAt(pos[0])
	spec, err := specOrFail(lang)
	if err != nil {
		return err
	}
	if ver == "" && len(pos) > 1 {
		ver = pos[1]
	}
	opts := install.Options{Force: has(flags, "force"), From: value(flags, "from")}
	logx.Stepf("Installing %s@%s", spec.Name, orDefault(ver, "latest"))
	got, already, err := install.Ensure(spec, ver, opts)
	if err != nil {
		return err
	}
	if already {
		fmt.Printf("%s@%s is already registered (use --force to replace)\n", spec.Name, got)
		return nil
	}
	fmt.Printf("Installed %s@%s\n", spec.Name, got)
	if r := resolve.For(spec); r.Version == "" {
		fmt.Printf("hint: no default version yet. Run ployenv use %s@%s, then ployenv shell to activate this session.\n", spec.Name, got)
	}
	return nil
}

func cmdUninstall(rest []string, g globals) error {
	pos, flags := splitFlags(rest)
	if len(pos) == 0 {
		return fmt.Errorf("usage: ployenv uninstall <lang>@<version> | <lang> --all")
	}
	lang, ver := splitAt(pos[0])
	spec, err := specOrFail(lang)
	if err != nil {
		return err
	}
	if ver == "" && len(pos) > 1 {
		ver = pos[1]
	}

	if has(flags, "all") {
		installed := config.InstalledVersions(spec.Name)
		if len(installed) == 0 {
			fmt.Printf("%s has no versions installed\n", spec.Name)
			return nil
		}
		for _, v := range installed {
			if err := install.Uninstall(spec.Name, v); err != nil {
				return err
			}
		}
		fmt.Printf("Removed all %s versions: %s\n", spec.Name, strings.Join(installed, ", "))
		return nil
	}

	if ver == "" {
		return fmt.Errorf("specify a version, e.g. ployenv uninstall %s@22.11.0 (or ployenv uninstall %s --all)", spec.Name, spec.Name)
	}
	if err := install.Uninstall(spec.Name, ver); err != nil {
		return err
	}
	fmt.Printf("Uninstalled %s@%s\n", spec.Name, ver)
	return nil
}

func cmdUse(rest []string, g globals) error {
	pos, flags := splitFlags(rest)
	if len(pos) == 0 {
		return fmt.Errorf("usage: ployenv use <lang>@<version> [--project]")
	}
	lang, ver := splitAt(pos[0])
	spec, err := specOrFail(lang)
	if err != nil {
		return err
	}
	if ver == "" && len(pos) > 1 {
		ver = pos[1]
	}
	if ver == "" {
		return fmt.Errorf("specify a version, e.g. ployenv use %s@lts", spec.Name)
	}
	// Share the resolution rule, so "latest" means the same thing in `use` as
	// it does in `current` (BUG-004).
	match := resolve.MatchInstalled(spec.Name, ver)
	if match == "" {
		if ver == "lts" {
			return fmt.Errorf("%s@lts is resolved online and is not installed; run ployenv install %s@lts first", spec.Name, spec.Name)
		}
		return fmt.Errorf("%s@%s is not installed; run ployenv install %s@%s first", spec.Name, ver, spec.Name, ver)
	}
	if has(flags, "project") {
		path, err := writeProjectFile(spec.Name, match)
		if err != nil {
			return err
		}
		fmt.Printf("Updated project file: %s (%s = %s)\n", path, spec.Name, match)
		printActivateHint()
		return nil
	}
	cfg := config.Load()
	cfg.Defaults[spec.Name] = match
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("Global default set: %s = %s\n", spec.Name, match)
	printActivateHint()
	return nil
}

// printActivateHint explains that recording a version does not put the tools on
// the current terminal's PATH; the session must be activated first. The recipes
// are the ones the current platform can run (REQ-008 A7).
func printActivateHint() {
	fmt.Println("note: this only records the version; it does not change PATH in the current terminal.")
	if platform.IsWindows() {
		fmt.Println("  cmd          ployenv shell")
		fmt.Println("  PowerShell   ployenv env | Invoke-Expression")
		return
	}
	fmt.Println("  bash / zsh   eval \"$(ployenv env --shell bash)\"")
	fmt.Println("  any shell    ployenv shell")
}

// writeProjectFile sets key=value in the current directory's .ployenv,
// preserving any other lines.
func writeProjectFile(key, val string) (string, error) {
	path := ".ployenv"
	lines := []string{}
	replaced := false
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			name := trimmed
			if i := strings.IndexAny(trimmed, "=:"); i > 0 {
				name = strings.TrimSpace(trimmed[:i])
			} else if fields := strings.Fields(trimmed); len(fields) > 0 {
				name = fields[0]
			}
			if strings.EqualFold(name, key) {
				lines = append(lines, key+"="+val)
				replaced = true
				continue
			}
			lines = append(lines, line)
		}
	}
	if !replaced {
		lines = append(lines, key+"="+val)
	}
	abs, _ := filepath.Abs(path)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return "", err
	}
	return abs, nil
}

func cmdAdopt(rest []string, g globals) error {
	pos, flags := splitFlags(rest)
	if len(pos) == 0 {
		return fmt.Errorf("usage: ployenv adopt <lang>[@version] --root <dir> [--force]")
	}
	lang, ver := splitAt(pos[0])
	spec, err := specOrFail(lang)
	if err != nil {
		return err
	}
	root := value(flags, "root")
	if root == "" {
		return fmt.Errorf("use --root to point at an existing runtime directory (ployenv never probes for you)")
	}
	got, already, err := install.Adopt(spec, ver, root, has(flags, "force"))
	if err != nil {
		return err
	}
	// BUG-017: say what actually happened instead of reporting a fresh adopt.
	if already {
		fmt.Printf("%s@%s is already registered (use --force to replace)\n", spec.Name, got)
		return nil
	}
	fmt.Printf("Adopted %s@%s (from %s)\n", spec.Name, got, root)
	return nil
}

func cmdEnv(rest []string, g globals) error {
	_, flags := splitFlags(rest)
	entry := envx.Build(resolve.All())
	if entry.Empty() {
		return fmt.Errorf("nothing is configured yet (install a runtime, then set it with ployenv use)")
	}
	shell := strings.TrimSpace(value(flags, "shell"))
	// `--json` and `--shell json` both go through the one JSON emitter, so the
	// machine interface stays pure ASCII (FR-18).
	if g.json || strings.EqualFold(shell, "json") {
		return printJSON(entry.JSONValue())
	}
	out, err := entry.Script(shell)
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

func cmdShell(rest []string, g globals) error {
	_, flags := splitFlags(rest)
	results := resolve.All()
	entry := envx.Build(results)
	if entry.Empty() {
		return fmt.Errorf("nothing is configured yet (install a runtime, then set it with ployenv use)")
	}
	var parts []string
	for _, r := range results {
		if r.Version != "" && r.Err == nil {
			logx.Infof("%s %s (%s)%s", r.Lang, r.Version, r.Source.Label(), detailSuffix(r.Detail))
			parts = append(parts, r.Lang+"@"+r.Version)
		}
	}
	title := "[ployenv] " + strings.Join(parts, " ")
	if wd, err := os.Getwd(); err == nil {
		title += " - " + filepath.Base(wd)
	}
	logx.Stepf("Activated. Close the child terminal to restore the previous environment.")
	return envx.Shell(value(flags, "shell"), entry, title)
}

func cmdConfig(rest []string, g globals) error {
	pos, flags := splitFlags(rest)
	cfg := config.Load()

	if has(flags, "set-mirror") {
		lang := value(flags, "set-mirror")
		if lang == "" {
			return fmt.Errorf("usage: ployenv config --set-mirror <lang> <url> (use none to restore the official source)")
		}
		spec, err := specOrFail(lang)
		if err != nil {
			return err
		}
		url := ""
		if len(pos) > 0 {
			url = pos[0]
		}
		if url == "" {
			return fmt.Errorf("usage: ployenv config --set-mirror <lang> <url> (use none to restore the official source)")
		}
		if cfg.Mirrors == nil {
			cfg.Mirrors = map[string]string{}
		}
		if url == "none" || url == "off" || url == "default" {
			delete(cfg.Mirrors, spec.Name)
		} else {
			cfg.Mirrors[spec.Name] = strings.TrimRight(url, "/")
		}
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Printf("Mirror updated: %s = %s\n", spec.Name, mirrorLabel(spec, cfg))
		return nil
	}

	if has(flags, "set-default") {
		lang := value(flags, "set-default")
		if lang == "" {
			return fmt.Errorf("usage: ployenv config --set-default <lang> <version>")
		}
		spec, err := specOrFail(lang)
		if err != nil {
			return err
		}
		ver := ""
		if len(pos) > 0 {
			ver = pos[0]
		}
		if ver == "" {
			return fmt.Errorf("usage: ployenv config --set-default <lang> <version>")
		}
		cfg.Defaults[spec.Name] = ver
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Printf("Default version updated: %s = %s\n", spec.Name, ver)
		return nil
	}

	if has(flags, "set-log-mode") {
		raw := value(flags, "set-log-mode")
		v := strings.ToLower(strings.TrimSpace(raw))
		if v != "off" && v != "on-error" && v != "always" {
			return fmt.Errorf("--set-log-mode must be one of: off | on-error | always (got %q)", raw)
		}
		if cfg.Logs == nil {
			cfg.Logs = &config.LogsConfig{}
		}
		cfg.Logs.Mode = v
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Printf("Log mode updated: %s\n", v)
		return nil
	}

	if has(flags, "set-cache-keep") {
		raw := value(flags, "set-cache-keep")
		if raw == "" {
			return fmt.Errorf("usage: ployenv config --set-cache-keep <true|false>")
		}
		keep, err := parseBoolFlag("--set-cache-keep", raw)
		if err != nil {
			return err
		}
		if cfg.Cache == nil {
			cfg.Cache = &config.CacheConfig{}
		}
		cfg.Cache.Keep = keep
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Printf("Cache keep updated: %v (true = keep downloaded archives)\n", keep)
		return nil
	}

	if g.json {
		return printJSON(cfg)
	}
	fmt.Printf("root     %s\n", config.RootDir())
	fmt.Printf("config   %s\n", config.ConfigPath())
	fmt.Println("defaults:")
	for _, s := range runtime.All() {
		fmt.Printf("  %-8s %s\n", s.Name, orDefault(cfg.Defaults[s.Name], "(not set)"))
	}
	fmt.Println("mirrors:")
	for _, s := range runtime.All() {
		fmt.Printf("  %-8s %s\n", s.Name, mirrorLabel(s, cfg))
	}
	logMode, logDir, logKeep := logsEffective(cfg)
	fmt.Println("logs:")
	fmt.Printf("  %-8s %s   (off | on-error | always)\n", "mode", logMode)
	fmt.Printf("  %-8s %s\n", "dir", logDir)
	fmt.Printf("  %-8s %d\n", "keep", logKeep)
	fmt.Println("cache:")
	fmt.Printf("  %-8s %v   (true = keep downloaded archives)\n", "keep", cfg.Cache != nil && cfg.Cache.Keep)
	fmt.Println()
	fmt.Println("change: config --set-default | --set-mirror | --set-log-mode | --set-cache-keep")
	fmt.Println("        or edit config.json directly.")
	return nil
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// mirrorLabel shows the configured mirror, or the upstream URL when none is
// set, so the value is always a real link rather than just a word.
func mirrorLabel(spec *runtime.Spec, cfg *config.Config) string {
	if m := strings.TrimSpace(cfg.Mirrors[spec.Name]); m != "" {
		return m
	}
	if u := spec.OfficialURL(); u != "" {
		return u + " (official)"
	}
	return "(official)"
}

// logsEffective returns the log settings with their defaults applied.
func logsEffective(cfg *config.Config) (mode, dir string, keep int) {
	mode, dir, keep = "on-error", "logs", 20
	if cfg.Logs != nil {
		if cfg.Logs.Mode != "" {
			mode = cfg.Logs.Mode
		}
		if cfg.Logs.Dir != "" {
			dir = cfg.Logs.Dir
		}
		if cfg.Logs.Keep > 0 {
			keep = cfg.Logs.Keep
		}
	}
	return mode, dir, keep
}
