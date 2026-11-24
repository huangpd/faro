// Faro: a native macOS client for sing-box. This file only wires the
// engine (internal/engine) to a window and a menu-bar item; what Faro does
// is the engine's, what it looks like is in pages.go and tray.go.
package main

import (
	_ "embed"
	"log"
	"os"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"faro/internal/engine"
	"faro/internal/rules"
)

// trayIcon is Faro's lighthouse as a template image: black on transparent,
// which macOS tints for a light or a dark menu bar (go run ./cmd/genicon).
//
//go:embed resources/tray.png
var trayIcon []byte

// app is the interface's state. What Faro knows is in the engine, read as a
// snapshot (eng.State()) once per frame; here is only what a person is in the
// middle of: which page, what is typed, where a list is scrolled.
type app struct {
	eng  *engine.Engine
	win  *mygo.Window
	tray *mygo.Tray

	menuSig string // what the tray's menu was last built from
	title   string // the menu-bar title last set
	titleAt time.Time

	k    tokens // the design tokens of this frame
	page page

	subURL    string
	expanded  map[string]bool // lanes with their nodes shown
	rulesEdit string
	rulesErrs []rules.Error
	rulesTab  int // 0 the person's sites in groups, 1 the rules text

	rulesOpen       bool // the text is open to editing
	showConfig      bool
	unlockAsk       bool
	rulesCode       *editor.State // the rules text in a code editor
	rulesSeen       string        // the text the editor was last made the same as
	rulesVer        uint64        // the buffer's version then
	rulesMarked     uint64        // the version the mistakes were last marked at
	rulesMarkedErrs int

	domSel    map[string]*string   // the places' drop-downs, by group
	domTokens map[string]*[]string // each group's sites, as the field holds them
	domErr    string
	newGroup  newGroupDraft
	delAsk    bool // asking before a group is deleted
	delName   string

	previewFor   string // what the preview was made from
	previewText  string
	previewLines []string
	previewList  ui.ListState

	dnsSel      map[string]*string // the DNS drop-downs, by side
	portText    string             // the port field, while it is being edited
	portEditing bool
	portErr     string

	connQuery string
	connSel   string // the connection whose "why" is shown
	connList  ui.ListState

	logLevel int // 0 all, 1 info and up, 2 warnings and up, 3 errors
	logQuery string
	logList  ui.ListState
}

func newApp(eng *engine.Engine) *app {
	return &app{eng: eng, expanded: map[string]bool{}, dnsSel: map[string]*string{}, domSel: map[string]*string{}, domTokens: map[string]*[]string{}, rulesEdit: eng.State().RulesText}
}

// refresh redraws the window, when there is one, and the menu-bar item. It
// runs on the main thread.
func (a *app) refresh() {
	if a.win != nil {
		a.win.Invalidate()
	}
	if a.tray != nil {
		st := a.eng.State()
		now := time.Now()
		title := menuTitle(st, a.title, a.titleAt, now) // "" is the icon alone
		if title != a.title {                           // setting it again, unchanged, still costs
			a.title, a.titleAt = title, now
			a.tray.SetTitle(title)
		}
		if sig := menuSignature(st); sig != a.menuSig { // not every second, with the speed
			a.menuSig = sig
			a.tray.SetMenu(mygo.NewMenu(a.trayItems(st)))
		}
	}
}

// setLightMode keeps Faro to the menu bar, or lets the window be as before.
// Turning it on shuts the window that is open, which frees the interface; the
// core goes on, and the menu-bar item can open the window again.
func (a *app) setLightMode(on bool) {
	a.eng.SetLightMode(on)
	if on && a.win != nil { // no window, nothing to shut
		// From a goroutine: the switch that asked is drawn by this window, and a
		// window is not shut in the middle of its own frame.
		go a.win.Close()
	}
}

// opensWindowAtStart is whether Faro shows its window as it starts.
func opensWindowAtStart(st engine.State, background bool) bool { return !background && !st.LightMode }

// openWindow shows the window, or makes a new one: closing it frees the
// interface while the menu-bar item and the core keep running.
func (a *app) openWindow() {
	if a.win != nil {
		a.win.Show()
		return
	}
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: "Faro", Width: 1040, Height: 700, MinWidth: 880, MinHeight: 560,
		StateKey:      "main",
		TitleBarStyle: mygo.TitleBarHiddenInset,
		Vibrancy:      mygo.VibrancySidebar,
		Content:       ui.View(a.view),
	})
	w.OnClosed(func() {
		if a.page == pageConns {
			a.eng.WatchConnections(false) // nobody is looking
		}
		a.win = nil
	})
	if a.page == pageConns {
		a.eng.WatchConnections(true)
	}
	a.win = w
}

func main() {
	var a *app
	// Packaged, the core is in the app's resources; under go run, resources/.
	res, _ := mygo.App.Path(mygo.PathResources)
	eng, err := engine.New(engine.Options{
		ResourcesDir: res,
		Notify:       func() { mygo.RunOnMain(func() { a.refresh() }) },
	})
	if err != nil {
		log.Fatal(err)
	}
	a = newApp(eng)

	mygo.App.OnWindowAllClosed(func() {}) // keep running in the menu bar
	mygo.App.OnActivate(func(visible bool) {
		if !visible {
			a.openWindow()
		}
	})
	mygo.App.OnQuit(eng.Close) // the system proxy is put back, then the core stops
	mygo.App.WhenReady(func() {
		if opensWindowAtStart(eng.State(), os.Getenv("FARO_BACKGROUND") != "") { // menu bar only: light mode, or at login
			a.openWindow()
		}
		eng.Start()
		st := eng.State()
		a.menuSig = menuSignature(st)
		tray, err := mygo.NewTray(mygo.TrayOptions{
			Icon:            trayIcon,
			IconIsTemplate:  true, // macOS tints it to match the menu bar
			MonospacedTitle: true, // the speed changes unit without moving the menu bar
			ToolTip:         "Faro",
			Menu:            mygo.NewMenu(a.trayItems(st)),
		})
		if err != nil {
			log.Println("tray:", err)
			return
		}
		a.tray = tray
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
