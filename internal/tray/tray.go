package tray

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"fyne.io/systray"

	"inference/internal/catalogue"
	"inference/internal/config"
	"inference/internal/proxy"
	"inference/internal/snapd"
)

const (
	poolSize     = 8                // max rows shown in dynamic submenus
	pollInterval = 3 * time.Second  // status refresh cadence
)

// Run starts the indicator. It blocks until the user quits. It fails fast (with
// guidance) when there is no session bus to register a StatusNotifierItem on.
func Run(cfg *config.Config) error {
	if !sessionBusAvailable() {
		return fmt.Errorf("no desktop session bus found; the tray needs a graphical login.\n" +
			"On GNOME, enable the \"AppIndicator and KStatusNotifierItem Support\" extension, then run `inference tray`.")
	}
	t := &tray{cfg: cfg, refreshCh: make(chan struct{}, 1)}
	systray.Run(t.onReady, func() {})
	return nil
}

type tray struct {
	cfg       *config.Config
	refreshCh chan struct{}

	mu             sync.Mutex
	currentModels  []string // index-aligned with modelItems
	currentInstall []string // index-aligned with removeItems

	header       *systray.MenuItem
	backendItems []*systray.MenuItem
	modelParent  *systray.MenuItem
	modelItems   []*systray.MenuItem
	removeParent *systray.MenuItem
	removeItems  []*systray.MenuItem
	autostart    *systray.MenuItem
}

func (t *tray) onReady() {
	systray.SetIcon(iconFor(StateIdle))
	systray.SetTitle("")
	systray.SetTooltip("Inference")

	t.header = systray.AddMenuItem("Inference", "")
	t.header.Disable()
	systray.AddSeparator()

	// Backends (read-only status rows).
	backends := systray.AddMenuItem("Backends", "Discovered model backends")
	for i := 0; i < poolSize; i++ {
		it := backends.AddSubMenuItem("", "")
		it.Disable()
		it.Hide()
		t.backendItems = append(t.backendItems, it)
	}

	// Default model (radio: sets the `default` alias the CLI honours).
	t.modelParent = systray.AddMenuItem("Default model", "Model used when none is specified")
	for i := 0; i < poolSize; i++ {
		it := t.modelParent.AddSubMenuItemCheckbox("", "", false)
		it.Hide()
		t.modelItems = append(t.modelItems, it)
		go t.onModelClick(i, it)
	}
	systray.AddSeparator()

	// Install model (static catalogue list, brokered through the daemon).
	install := systray.AddMenuItem("Install model", "Install a model snap")
	for _, name := range catalogue.Names() {
		it := install.AddSubMenuItem(name, "Install "+name)
		go t.onInstallClick(name, it)
	}

	// Remove model (dynamic: installed snaps).
	t.removeParent = systray.AddMenuItem("Remove model", "Remove an installed model")
	for i := 0; i < poolSize; i++ {
		it := t.removeParent.AddSubMenuItem("", "")
		it.Hide()
		t.removeItems = append(t.removeItems, it)
		go t.onRemoveClick(i, it)
	}
	systray.AddSeparator()

	openAPI := systray.AddMenuItem("Open proxy API in browser", "")
	copyChat := systray.AddMenuItem("Copy `inference chat` command", "")
	refresh := systray.AddMenuItem("Refresh now", "")
	systray.AddSeparator()
	t.autostart = systray.AddMenuItemCheckbox("Start on login", "Launch the indicator at login", AutostartEnabled())
	quit := systray.AddMenuItem("Quit", "Quit the indicator")

	go func() {
		for range openAPI.ClickedCh {
			t.openBrowser(fmt.Sprintf("http://%s:%d/v1/models", t.cfg.Proxy.Bind, t.cfg.Proxy.Port))
		}
	}()
	go func() {
		for range copyChat.ClickedCh {
			t.copyChatCommand()
		}
	}()
	go func() {
		for range refresh.ClickedCh {
			t.kick()
		}
	}()
	go func() {
		for range t.autostart.ClickedCh {
			t.toggleAutostart()
		}
	}()
	go func() {
		<-quit.ClickedCh
		systray.Quit()
	}()

	go t.loop()
}

// loop refreshes status on a ticker and on demand.
func (t *tray) loop() {
	t.update()
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			t.update()
		case <-t.refreshCh:
			t.update()
		}
	}
}

// kick requests an immediate refresh without blocking.
func (t *tray) kick() {
	select {
	case t.refreshCh <- struct{}{}:
	default:
	}
}

// update polls the daemon and re-renders the icon and menu.
func (t *tray) update() {
	up := proxy.IsUp(t.cfg)
	bs, _ := proxy.Backends(t.cfg)

	systray.SetIcon(iconFor(DeriveState(up, bs)))
	systray.SetTooltip(HeaderLine(up, bs))
	t.header.SetTitle(HeaderLine(up, bs))

	// Backend status rows.
	for i, it := range t.backendItems {
		if i < len(bs) {
			it.SetTitle(BackendLine(bs[i]))
			it.Show()
		} else {
			it.Hide()
		}
	}

	// Default-model radio.
	models := ModelIDs(bs)
	def := t.currentDefault()
	t.mu.Lock()
	t.currentModels = models
	t.mu.Unlock()
	for i, it := range t.modelItems {
		if i < len(models) {
			it.SetTitle(models[i])
			if models[i] == def {
				it.Check()
			} else {
				it.Uncheck()
			}
			it.Show()
		} else {
			it.Hide()
		}
	}
	if len(models) == 0 {
		t.modelParent.Disable()
	} else {
		t.modelParent.Enable()
	}

	// Remove list.
	installed := InstalledNames(bs)
	t.mu.Lock()
	t.currentInstall = installed
	t.mu.Unlock()
	for i, it := range t.removeItems {
		if i < len(installed) {
			it.SetTitle(installed[i])
			it.Show()
		} else {
			it.Hide()
		}
	}
	if len(installed) == 0 {
		t.removeParent.Disable()
	} else {
		t.removeParent.Enable()
	}
}

// currentDefault returns the configured default-model alias, if any.
func (t *tray) currentDefault() string {
	if c, ok := proxy.GetConfig(t.cfg); ok {
		return c.Aliases["default"]
	}
	return t.cfg.Aliases["default"]
}

func (t *tray) onModelClick(i int, it *systray.MenuItem) {
	for range it.ClickedCh {
		t.mu.Lock()
		var name string
		if i < len(t.currentModels) {
			name = t.currentModels[i]
		}
		t.mu.Unlock()
		if name == "" {
			continue
		}
		err := proxy.ApplyConfig(t.cfg, config.Mutation{Set: map[string]string{"alias.default": name}})
		if err != nil {
			notify("Inference", "Could not set default model: "+err.Error())
		} else {
			notify("Inference", "Default model is now "+name)
		}
		t.kick()
	}
}

func (t *tray) onInstallClick(name string, it *systray.MenuItem) {
	for range it.ClickedCh {
		go func() {
			notify("Inference", "Installing "+name+"…")
			err := proxy.RequestInstall(t.cfg, name, func(snapd.Progress) {})
			if err != nil {
				notify("Inference", "Install of "+name+" failed: "+err.Error())
			} else {
				notify("Inference", name+" installed")
			}
			t.kick()
		}()
	}
}

func (t *tray) onRemoveClick(i int, it *systray.MenuItem) {
	for range it.ClickedCh {
		t.mu.Lock()
		var name string
		if i < len(t.currentInstall) {
			name = t.currentInstall[i]
		}
		t.mu.Unlock()
		if name == "" {
			continue
		}
		go func() {
			notify("Inference", "Removing "+name+"…")
			err := proxy.RequestRemove(t.cfg, name, func(snapd.Progress) {})
			if err != nil {
				notify("Inference", "Remove of "+name+" failed: "+err.Error())
			} else {
				notify("Inference", name+" removed")
			}
			t.kick()
		}()
	}
}

func (t *tray) toggleAutostart() {
	on := !t.autostart.Checked()
	if err := SetAutostart(on); err != nil {
		notify("Inference", "Could not change autostart: "+err.Error())
		return
	}
	if on {
		t.autostart.Check()
		notify("Inference", "The indicator will start on login")
	} else {
		t.autostart.Uncheck()
		notify("Inference", "The indicator will no longer start on login")
	}
}

// openBrowser opens a URL with the desktop's default handler.
func (t *tray) openBrowser(url string) {
	if err := exec.Command("xdg-open", url).Start(); err != nil {
		notify("Inference", "Could not open browser: "+err.Error())
	}
}

// copyChatCommand copies `inference chat` to the clipboard. The tools are
// bundled in the snap (host binaries aren't reachable under confinement); xclip
// is tried first because GNOME bridges the Xwayland selection to the Wayland
// clipboard, then wl-copy for pure-Wayland sessions.
func (t *tray) copyChatCommand() {
	const cmd = "inference chat"
	candidates := [][]string{
		{snapBin("xclip"), "-selection", "clipboard"},
		{snapBin("wl-copy")},
	}
	for _, argv := range candidates {
		c := exec.Command(argv[0], argv[1:]...)
		stdin, err := c.StdinPipe()
		if err != nil {
			continue
		}
		if err := c.Start(); err != nil {
			continue
		}
		io.WriteString(stdin, cmd)
		stdin.Close()
		if c.Wait() == nil {
			notify("Inference", "Copied to clipboard: "+cmd)
			return
		}
	}
	notify("Inference", "Run in a terminal:  "+cmd)
}

// snapBin resolves a helper binary to its in-snap path when running confined
// ($SNAP set), else to its bare name for development.
func snapBin(name string) string {
	if snap := os.Getenv("SNAP"); snap != "" {
		if p := filepath.Join(snap, "usr", "bin", name); fileExists(p) {
			return p
		}
	}
	return name
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
