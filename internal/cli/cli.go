// Package cli wires the command-line surface to the internal packages.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"inference/internal/backend"
	"inference/internal/catalogue"
	"inference/internal/chat"
	"inference/internal/config"
	"inference/internal/hardware"
	"inference/internal/install"
	"inference/internal/proxy"
	"inference/internal/snapd"
	"inference/internal/spec"
	"inference/internal/ui"
)

const defaultModel = "gemma4"

type opts struct {
	yes    bool
	dryRun bool
}

// Main is the entry point; returns a process exit code.
func Main(args []string) int {
	rest, o := parseGlobals(args)
	cmd := ""
	if len(rest) > 0 {
		cmd = rest[0]
		rest = rest[1:]
	}

	cfg, err := config.Load()
	if err != nil {
		ui.Errf("%s config: %v\n", ui.Red(ui.SymErr), err)
		return 1
	}

	var cmdErr error
	switch cmd {
	case "":
		cmdErr = wizard(cfg)
	case "hardware", "hw":
		cmdErr = hardwareReport()
	case "doctor":
		cmdErr = doctor(cfg, rest)
	case "models", "list", "ls":
		cmdErr = models(cfg)
	case "catalogue", "catalog", "search", "store":
		cmdErr = catalogueCmd(cfg, rest)
	case "remove", "uninstall", "rm":
		cmdErr = removeCmd(cfg, rest, o)
	case "serve":
		cmdErr = serve(cfg)
	case "run":
		cmdErr = run(cfg, rest)
	case "chat":
		cmdErr = chatCmd(cfg, rest)
	case "install", "add":
		cmdErr = installCmd(cfg, rest, o)
	case "proxy":
		cmdErr = proxyCmd(cfg, rest)
	case "config":
		cmdErr = configCmd(cfg, rest)
	case "version", "--version", "-v":
		ui.Println("inference 0.1.0 (first pass)")
	case "help", "--help", "-h":
		usage()
	default:
		ui.Errf("%s unknown command %q\n\n", ui.Red(ui.SymErr), cmd)
		usage()
		return 2
	}
	if cmdErr != nil {
		ui.Errf("%s %v\n", ui.Red(ui.SymErr), cmdErr)
		return 1
	}
	return 0
}

// profileOverride is set by the global --profile flag; "" means auto-detect.
var profileOverride string

func parseGlobals(args []string) ([]string, opts) {
	var rest []string
	var o opts
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			ui.JSON = true
		case a == "--quiet", a == "-q":
			ui.Quiet = true
		case a == "--yes", a == "-y":
			o.yes = true
		case a == "--dry-run":
			o.dryRun = true
		case a == "--profile":
			if i+1 < len(args) {
				profileOverride = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--profile="):
			profileOverride = strings.TrimPrefix(a, "--profile=")
		default:
			rest = append(rest, a)
		}
	}
	return rest, o
}

// detect returns the host hardware info, applying the --profile override.
func detect() *hardware.Info {
	hw := hardware.Detect()
	if profileOverride != "" {
		hw.Profile = profileOverride
	}
	return hw
}

// ---- wizard / status ----

func wizard(cfg *config.Config) error {
	hw := detect()
	ui.Printf("%s  Let's get your machine ready for local AI.\n\n", ui.Bold("Welcome to Inference."))
	printHardware(hw)

	up := proxy.IsUp(cfg)
	bs, _ := discoverBackends(cfg)
	online := onlineWithModels(bs)
	ui.Println()

	if !up {
		ui.Printf("  %s proxy not running — it discovers & installs models for you\n", ui.Yellow(ui.SymWarn))
		ui.Printf("\n  Start it:  %s\n", ui.Bold("sudo snap start --enable inference.proxy"))
		return nil
	}
	ui.Printf("  %s proxy live at http://%s:%d/v1\n", ui.Green(ui.SymOK), cfg.Proxy.Bind, cfg.Proxy.Port)
	if len(online) > 0 {
		ui.Printf("  %s %d backend(s) ready: %s\n", ui.Green(ui.SymOK), len(online), namesOf(online))
		ui.Printf("\n  Try it:   %s\n", ui.Bold("inference chat"))
		return nil
	}

	// Proxy up but no models yet — recommend the default for this silicon.
	accel := firstAccel(hw)
	ui.Printf("  %s no models installed yet\n", ui.Yellow(ui.SymWarn))
	ui.Printf("\n  Recommended starting point\n")
	ui.Printf("    %s   text+vision   %s  %s\n", ui.Bold(defaultModel), ui.SymArrow, ui.Dim(accel))
	ui.Printf("\n  Get started:  %s\n", ui.Bold("inference install "+defaultModel))
	return nil
}

// ---- hardware ----

func hardwareReport() error {
	hw := detect()
	if ui.JSON {
		return printJSON(hw)
	}
	printHardware(hw)
	return nil
}

func printHardware(hw *hardware.Info) {
	ui.Printf("  %s\n", ui.Bold("Hardware"))
	t := ui.NewTable().Indent("    ")
	t.Row("CPU", fmt.Sprintf("%s · %d threads · %s", hw.CPUModel, hw.Threads, hw.ISA))
	if len(hw.GPUs) == 0 {
		t.Row("GPU", ui.Dim("none detected"))
	}
	for _, g := range hw.GPUs {
		extra := ""
		if g.VRAMGB > 0 {
			extra += fmt.Sprintf(" · %d GB VRAM", g.VRAMGB)
		}
		if g.Driver != "" {
			extra += " · driver " + g.Driver
		}
		t.Row("GPU", fmt.Sprintf("%s%s  %s", g.Name, extra, ui.Dim("["+g.Accel+"]")))
	}
	if hw.NPU != "" {
		t.Row("NPU", hw.NPU)
	}
	t.Row("RAM", fmt.Sprintf("%.0f GB total · %.0f GB free", hw.MemGB, hw.AvailGB))
	t.Row("Disk", fmt.Sprintf("%.0f GB free on /", hw.DiskGB))
	t.Row("Profile", fmt.Sprintf("%s   %s", ui.Bold(hw.Profile), ui.Dim("("+strings.Join(hw.Accelerators(), ", ")+")")))
	t.Render()
}

// ---- doctor ----

// privilegedPlugs are the interfaces inference needs connected to do its job.
var privilegedPlugs = []string{"snapd-control", "hardware-observe", "system-observe"}

func doctor(cfg *config.Config, args []string) error {
	fix := false
	for _, a := range args {
		if a == "--fix" {
			fix = true
		}
	}
	hw := detect()
	var fixes []string // consolidated remediation commands

	ui.Printf("  %s\n", ui.Bold("Hardware"))
	ht := ui.NewTable().Indent("    ")
	for _, g := range hw.GPUs {
		if g.Vendor == "nvidia" && g.Driver == "" {
			ht.Row(g.Name, ui.Yellow("no driver"), ui.Dim(ui.SymArrow+" sudo ubuntu-drivers install"))
			fixes = append(fixes, "sudo ubuntu-drivers install")
		} else {
			ht.Row(g.Name, ui.Green("ok"), "")
		}
	}
	if hw.NPU != "" {
		ht.Row(hw.NPU, ui.Green("ok"), "")
	}
	ht.Row("Accelerators", ui.Dim(strings.Join(hw.Accelerators(), ", ")), "")
	ht.Render()

	// Snap interfaces — the privileged plugs must be connected for management
	// and detection to work (esp. on a --dangerous sideload, nothing auto-connects).
	if conns, ok := snapd.Connections(); ok {
		ui.Printf("\n  %s\n", ui.Bold("Snap interfaces"))
		it := ui.NewTable().Indent("    ")
		for _, plug := range privilegedPlugs {
			if conns[plug] {
				it.Row("inference:"+plug, ui.Green("connected"), "")
			} else {
				cmd := "sudo snap connect inference:" + plug
				it.Row("inference:"+plug, ui.Yellow("not connected"), ui.Dim(ui.SymArrow+" "+cmd))
				fixes = append(fixes, cmd)
			}
		}
		it.Render()
	}

	ui.Printf("\n  %s\n", ui.Bold("Proxy"))
	up := proxy.IsUp(cfg)
	addr := fmt.Sprintf("%s:%d", cfg.Proxy.Bind, cfg.Proxy.Port)
	pt := ui.NewTable().Indent("    ")
	if up {
		pt.Row(addr, ui.Green("listening"), "")
	} else {
		cmd := "sudo snap start --enable inference.proxy"
		pt.Row(addr, ui.Yellow("not running"), ui.Dim(ui.SymArrow+" "+cmd))
		fixes = append(fixes, cmd)
	}
	pt.Render()

	ui.Printf("\n  %s\n", ui.Bold("Inference backends"))
	bs, fromDaemon := discoverBackends(cfg)
	if len(bs) == 0 {
		hint := "inference install " + defaultModel
		if !up {
			hint = "start the proxy first (it discovers models)"
		}
		ui.Printf("    %s none discovered  %s\n", ui.Yellow("!"), ui.Dim(ui.SymArrow+" "+hint))
	} else {
		bt := ui.NewTable().Indent("    ")
		for _, b := range bs {
			st, note := ui.Green("online"), ""
			if !b.Online {
				st, note = ui.Yellow("offline"), ui.Dim("installed but not responding")
			}
			bt.Row(b.Name, ui.Dim(b.Engine), st, note)
		}
		bt.Render()
	}
	if !fromDaemon && up {
		ui.Println(ui.Dim("    (proxy did not return backends)"))
	}

	ui.Printf("\n  %s\n", ui.Bold("Disk"))
	dt := ui.NewTable().Indent("    ")
	dstat := ui.Green("ok")
	if hw.DiskGB <= 5 {
		dstat = ui.Yellow("low")
	}
	dt.Row("/", fmt.Sprintf("%.0f GB free", hw.DiskGB), dstat)
	dt.Render()

	// Remediation summary.
	if len(fixes) == 0 {
		ui.Printf("\n  %s everything looks healthy\n", ui.Green(ui.SymOK))
		return nil
	}
	plural := "issue"
	if len(fixes) > 1 {
		plural = "issues"
	}
	if !fix {
		ui.Printf("\n  %d %s can be fixed — run:  %s\n", len(fixes), plural, ui.Bold("inference doctor --fix"))
		return nil
	}
	// These steps need root (snap connect / driver install), which the confined
	// CLI can't do itself, so present one ordered, copy-paste block.
	ui.Printf("\n  %s\n", ui.Bold(fmt.Sprintf("Run these %d command(s) to fix:", len(fixes))))
	for _, c := range fixes {
		ui.Printf("    %s\n", c)
	}
	return nil
}

// ---- models ----

func models(cfg *config.Config) error {
	bs, _ := discoverBackends(cfg)
	if ui.JSON {
		return printJSON(bs)
	}
	var local, remote []backend.Backend
	for _, b := range bs {
		if b.Kind == "remote" {
			remote = append(remote, b)
		} else {
			local = append(local, b)
		}
	}
	section := func(title string, list []backend.Backend) {
		if len(list) == 0 {
			return
		}
		ui.Printf("  %s\n", ui.Bold(title))
		t := ui.NewTable().Indent("    ")
		for _, b := range list {
			state := ui.Dim("offline")
			if b.Online {
				state = ui.Green("online")
			}
			t.Row(b.Name, b.Engine, state, ui.Dim(strings.Join(b.Models, ", ")))
		}
		t.Render()
	}
	section("LOCAL", local)
	section("REMOTE", remote)
	if len(cfg.Aliases) > 0 {
		ui.Printf("  %s\n", ui.Bold("ALIASES"))
		t := ui.NewTable().Indent("    ")
		for a, tgt := range cfg.Aliases {
			t.Row(a, ui.SymArrow, tgt)
		}
		t.Render()
	}
	if len(bs) == 0 {
		ui.Printf("  nothing yet  %s\n", ui.Dim(ui.SymArrow+" inference install "+defaultModel))
	}
	return nil
}

// ---- catalogue ----

func catalogueCmd(cfg *config.Config, args []string) error {
	hw := detect()
	hwAccels := map[string]bool{}
	for _, a := range hw.Accelerators() {
		hwAccels[a] = true
	}
	// State of installed/online models by snap name.
	bs, _ := discoverBackends(cfg)
	state := map[string]bool{} // name -> online
	installed := map[string]bool{}
	for _, b := range bs {
		installed[b.Name] = true
		if b.Online {
			state[b.Name] = true
		}
	}

	query := strings.Join(args, " ")
	list := catalogue.Search(query) // typo-tolerant; all models when query is empty
	if len(list) == 0 {
		ui.Printf("  no models match %q\n", query)
		if m, d := catalogue.Closest(query); d > 0 && d <= 4 {
			ui.Printf("  did you mean %s?\n", ui.Bold(m.Name))
		}
		return nil
	}

	title := "Model catalogue"
	if query != "" {
		title = fmt.Sprintf("Catalogue matches for %q", query)
	}
	ui.Printf("  %s   %s\n", ui.Bold(title), ui.Dim("(profile: "+hw.Profile+")"))
	t := ui.NewTable().Indent("    ")
	t.Row(ui.Dim("MODEL"), ui.Dim("DESCRIPTION"), ui.Dim("STATUS"), ui.Dim("FIT"))
	for _, m := range list {
		status := ui.Dim("available")
		switch {
		case state[m.Name]:
			status = ui.Green("online")
		case installed[m.Name]:
			status = ui.Yellow("installed")
		}
		// best accelerator on this machine
		fit := ui.Dim("cpu")
		for _, a := range m.Accels {
			if a != "cpu" && hwAccels[a] {
				fit = ui.Green(a)
				break
			}
		}
		desc := ui.Truncate(m.Summary+" · "+m.Modalities, 60)
		t.Row(ui.Bold(m.Name), ui.Dim(desc), status, fit)
	}
	t.Render()
	ui.Printf("\n  %s\n", ui.Dim("FIT = best accelerator on this box · install with: inference install <model>"))
	return nil
}

// ---- remove ----

func removeCmd(cfg *config.Config, args []string, o opts) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: inference remove <model>...")
	}
	bs, _ := discoverBackends(cfg)
	installed := map[string]bool{}
	var names []string
	for _, b := range bs {
		installed[b.Name] = true
		names = append(names, b.Name)
	}
	for _, a := range args {
		// Compute the snap targets for this arg. `base+addon` removes just the
		// addon snap(s) (symmetric with install); a bare base removes the base.
		sp := spec.Parse(a)
		var targets []string
		if len(sp.Addons) > 0 {
			for _, ad := range sp.Addons {
				if snap, ok := install.AddonSnap(ad); ok {
					targets = append(targets, snap)
				} else {
					ui.Printf("  %s no removable snap for addon %q\n", ui.Yellow(ui.SymWarn), ad)
				}
			}
		} else {
			name := sp.Base
			if name == "" {
				name = a
			}
			targets = []string{name}
		}

		for _, name := range targets {
			switch {
			case installed[name]:
				if !o.yes && !confirm(fmt.Sprintf("  Remove %s?", ui.Bold(name))) {
					ui.Println(ui.Dim("  skipped"))
					continue
				}
			default:
				// Not installed — offer the nearest installed model (typo tolerance).
				near, d := catalogue.Nearest(name, names)
				if len(names) == 0 || d > 3 {
					ui.Printf("  %s %q isn't installed\n", ui.Yellow(ui.SymWarn), name)
					continue
				}
				if !o.yes && !confirm(fmt.Sprintf("  %q isn't installed — remove %s?", name, ui.Bold(near))) {
					ui.Println(ui.Dim("  skipped"))
					continue
				}
				name = near
			}
			err := doRemove(cfg, name)
			fmt.Println() // finish progress line
			if err != nil {
				return fmt.Errorf("remove %s: %w", name, err)
			}
			ui.Printf("  %s %s removed\n", ui.Green(ui.SymOK), name)
		}
	}
	return nil
}

// correctTypo offers a did-you-mean correction for a misspelled model base in an
// install spec (leaving unknown-but-plausible names alone for the hybrid path).
func correctTypo(arg string, installed map[string]bool, yes bool) string {
	base := spec.Parse(arg).Base
	if base == "" {
		return arg
	}
	if _, ok := catalogue.Get(base); ok || installed[base] {
		return arg
	}
	m, d := catalogue.Closest(base)
	if d == 0 || d > 3 {
		return arg
	}
	if yes || confirm(fmt.Sprintf("  %q isn't a known model — did you mean %s?", base, ui.Bold(m.Name))) {
		return strings.Replace(arg, base, m.Name, 1)
	}
	return arg
}

// doRemove removes a model snap, via the root daemon when confined.
func doRemove(cfg *config.Config, name string) error {
	if proxy.IsUp(cfg) {
		return proxy.RequestRemove(cfg, name, renderProgress)
	}
	if os.Getenv("SNAP") != "" {
		return fmt.Errorf("the inference proxy must be running to remove models")
	}
	return snapd.Remove(name, nil)
}

// ---- serve ----

// serve is the daemon entrypoint (run as the inference.proxy service); it is not
// a user-facing verb. If a user runs it while the service is up, explain instead
// of colliding on the port.
func serve(cfg *config.Config) error {
	if proxy.IsUp(cfg) {
		ui.Printf("  %s the proxy is already running as the %s service\n",
			ui.Green(ui.SymOK), ui.Bold("inference.proxy"))
		ui.Println(ui.Dim("  manage it with:  sudo snap restart|stop inference.proxy"))
		return nil
	}
	return proxy.New(cfg).ListenAndServe()
}

// ---- run / chat ----

func run(cfg *config.Config, args []string) error {
	model, prompt := splitModelPrompt(cfg, args)
	return chat.Run(cfg, model, prompt)
}

func chatCmd(cfg *config.Config, args []string) error {
	model := ""
	if len(args) > 0 {
		model = args[0]
	}
	return chat.REPL(cfg, model)
}

// splitModelPrompt decides whether args[0] is a model id/alias or part of the prompt.
func splitModelPrompt(cfg *config.Config, args []string) (model, prompt string) {
	if len(args) == 0 {
		return "", ""
	}
	known := map[string]bool{}
	bs, _ := discoverBackends(cfg)
	for _, b := range bs {
		known[b.Name] = true
		for _, m := range b.Models {
			known[m] = true
		}
	}
	for a := range cfg.Aliases {
		known[a] = true
	}
	if known[args[0]] {
		return args[0], strings.Join(args[1:], " ")
	}
	return "", strings.Join(args, " ")
}

// ---- install ----

func installCmd(cfg *config.Config, args []string, o opts) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: inference install <model>[+engine][+accel][+addon] ...")
	}
	hw := detect()
	bs, _ := discoverBackends(cfg)
	installed := map[string]bool{}
	for _, b := range bs {
		installed[b.Name] = true
	}
	var specs []spec.Spec
	for _, a := range args {
		a = correctTypo(a, installed, o.yes)
		specs = append(specs, spec.Parse(a))
	}
	plan := install.Resolve(specs, hw)
	ui.Printf("  Resolving %s for your %s…\n", ui.Bold(strings.Join(args, " ")), hw.Profile)
	plan.Print()

	if plan.HasErrors() {
		return fmt.Errorf("can't resolve that combination on this machine")
	}
	if o.dryRun || !plan.HasInstallSteps() {
		return nil
	}
	if !o.yes && !confirm("\n  Proceed?") {
		ui.Println(ui.Dim("  aborted"))
		return nil
	}
	for _, name := range plan.SnapNames() {
		ui.Printf("  %s installing %s…\n", ui.Cyan(ui.SymArrow), name)
		if err := doInstall(cfg, name); err != nil {
			return fmt.Errorf("install %s: %w", name, err)
		}
		ui.Printf("  %s %s installed and registered with the proxy\n", ui.Green(ui.SymOK), name)
	}
	return nil
}

// ---- proxy add ----

func proxyCmd(cfg *config.Config, args []string) error {
	if len(args) < 2 || args[0] != "add" {
		return fmt.Errorf("usage: inference proxy add <name> --key <KEY> [--base URL] [--type openai|anthropic] [--models a,b]")
	}
	p := config.Provider{Name: args[1], Type: "openai"}
	for i := 2; i < len(args); i++ {
		val := func() string {
			i++
			if i < len(args) {
				return args[i]
			}
			return ""
		}
		switch args[i] {
		case "--key":
			p.APIKey = val()
		case "--base":
			p.BaseURL = val()
		case "--type":
			p.Type = val()
		case "--models":
			p.Models = strings.Split(val(), ",")
		}
	}
	if p.BaseURL == "" {
		switch p.Type {
		case "anthropic":
			p.BaseURL = "https://api.anthropic.com/v1"
		default:
			p.BaseURL = "https://api.openai.com/v1"
		}
	}
	// Persist via the broker (daemon owns the writable store); AddProvider
	// replaces any existing provider with the same name.
	if err := applyConfig(cfg, config.Mutation{Provider: &p}); err != nil {
		return err
	}
	ui.Printf("  %s added provider %s (%s)\n", ui.Green(ui.SymOK), ui.Bold(p.Name), p.Type)
	return nil
}

// ---- config ----

func configCmd(cfg *config.Config, args []string) error {
	if len(args) == 0 || args[0] == "get" {
		conf := effectiveConfig(cfg)
		if len(args) >= 2 {
			ui.Println(conf.Get(args[1]))
			return nil
		}
		return printJSON(conf.Redacted())
	}
	if args[0] == "set" && len(args) >= 3 {
		if err := applyConfig(cfg, config.Mutation{Set: map[string]string{args[1]: args[2]}}); err != nil {
			return err
		}
		ui.Printf("  %s %s = %s\n", ui.Green(ui.SymOK), args[1], args[2])
		return nil
	}
	return fmt.Errorf("usage: inference config get [key] | set <key> <value>")
}

// effectiveConfig returns the daemon's authoritative config when the proxy is
// up (it owns the single writable store), else the locally loaded config.
func effectiveConfig(cfg *config.Config) *config.Config {
	if proxy.IsUp(cfg) {
		if c, ok := proxy.GetConfig(cfg); ok {
			return c
		}
	}
	return cfg
}

// applyConfig persists a config change. When the proxy is up the unprivileged
// CLI delegates to the root daemon — the only process that can write
// $SNAP_DATA — otherwise (development / no daemon) it writes the local file.
func applyConfig(cfg *config.Config, m config.Mutation) error {
	if proxy.IsUp(cfg) {
		return proxy.ApplyConfig(cfg, m)
	}
	if err := cfg.Apply(m); err != nil {
		return err
	}
	return cfg.Save()
}

// ---- helpers ----

func usage() {
	ui.Println(`inference — silicon-optimized local AI for Ubuntu

Usage: inference [command] [flags]

Commands:
  (none)              hardware + status getting-started screen
  catalogue           browse available models + install status
  install <+spec>...  resolve & install models/components (e.g. gemma4+cuda+webui)
  remove <model>...   uninstall a model
  models | list       show installed backends, models, aliases
  run <model> [text]  one-shot completion (reads stdin if no text)
  chat [model]        interactive REPL
  hardware            hardware detection report
  doctor [--fix]      diagnose drivers, interfaces, backends, proxy
  proxy add <name>    register an external provider (--key ...)
  config get|set      view/change configuration

The federated proxy runs automatically as the 'inference.proxy' service
(manage with: sudo snap start|stop|restart inference.proxy).

Flags:
  --json   machine-readable output      --yes        skip confirmations
  --quiet  suppress non-error output    --dry-run    plan only, don't execute
  --profile <edge|laptop|workstation|server>  override detected machine profile`)
}

func confirm(prompt string) bool {
	ui.Printf("%s [Y/n] ", prompt)
	var resp string
	fmt.Scanln(&resp)
	resp = strings.ToLower(strings.TrimSpace(resp))
	return resp == "" || resp == "y" || resp == "yes"
}

func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	os.Stdout.Write(b)
	os.Stdout.Write([]byte("\n"))
	return nil
}

// discoverBackends returns the routable backends. Discovery needs root (reading
// other snaps' config), so the unprivileged CLI prefers the running daemon and
// only falls back to direct discovery when not confined (development).
// daemon=true means the list came from the privileged daemon.
func discoverBackends(cfg *config.Config) (bs []backend.Backend, daemon bool) {
	if proxy.IsUp(cfg) {
		if list, ok := proxy.Backends(cfg); ok {
			return list, true
		}
	}
	return backend.Discover(cfg), false
}

// doInstall installs a model snap, via the root daemon when confined, rendering
// live progress.
func doInstall(cfg *config.Config, name string) error {
	if proxy.IsUp(cfg) {
		err := proxy.RequestInstall(cfg, name, renderProgress)
		fmt.Println() // finish the progress line
		return err
	}
	if os.Getenv("SNAP") != "" {
		return fmt.Errorf("the inference proxy must be running to install models — start it:\n      sudo snap start --enable inference.proxy")
	}
	return snapd.Install(name, nil) // development: snap CLI shows its own progress
}

// lastProgress tracks the last summary printed in non-TTY mode so we emit one
// line per distinct step instead of spamming the log.
var lastProgress string

// renderProgress shows live install progress. On a TTY it redraws a single line
// in place, fitting the full snapd status to the terminal width (so the status
// text is never chopped). Off a TTY it logs each new step on its own line.
func renderProgress(p snapd.Progress) {
	if ui.Quiet {
		return
	}

	// The numeric meter (download bytes or task counter), kept as plain text so
	// it can be measured before coloring.
	var meter string
	switch {
	case p.Total > 0:
		pct := int(p.Done * 100 / p.Total)
		meter = fmt.Sprintf("%3d%%  %s / %s", pct, humanBytes(p.Done), humanBytes(p.Total))
	case p.TasksTotal > 0:
		meter = fmt.Sprintf("step %d of %d", p.TasksDone, p.TasksTotal)
	}

	if !ui.IsTTY() {
		if p.Summary != "" && p.Summary != lastProgress {
			lastProgress = p.Summary
			line := "  " + ui.SymArrow + " " + p.Summary
			if meter != "" {
				line += "  (" + meter + ")"
			}
			fmt.Println(line)
		}
		return
	}

	prefix := "  " + ui.Cyan(ui.SymArrow) + " "
	budget := ui.TermWidth() - ui.VisibleLen(prefix) - 1
	if meter != "" {
		budget -= len(meter) + 2
	}
	if budget < 10 {
		budget = 10
	}
	out := prefix + ui.Truncate(p.Summary, budget)
	if meter != "" {
		out += "  " + ui.Dim(meter)
	}
	// \r returns to the start of the line; \033[K clears to end-of-line so no
	// characters from a longer previous status are left behind.
	fmt.Printf("\r\033[K%s", out)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func onlineWithModels(bs []backend.Backend) []backend.Backend {
	var out []backend.Backend
	for _, b := range bs {
		if b.Online && len(b.Models) > 0 {
			out = append(out, b)
		}
	}
	return out
}

func namesOf(bs []backend.Backend) string {
	var n []string
	for _, b := range bs {
		n = append(n, b.Name)
	}
	return strings.Join(n, ", ")
}

func firstAccel(hw *hardware.Info) string {
	a := hw.Accelerators()
	if len(a) > 0 {
		return a[0]
	}
	return "cpu"
}
