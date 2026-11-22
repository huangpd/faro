package main

import "github.com/egoist/mygo/ui"

// Design tokens: see design-system/MASTER.md. The interface refers to these
// by role and never writes a color of its own, so a skin is another set of
// tokens and no component changes.
type tokens struct {
	dark bool

	bg       ui.Color // the window under the content
	sidebar  ui.Color // the sidebar, where there is no material behind it
	surface  ui.Color // cards
	surface2 ui.Color // hover, fields, segmented controls
	border   ui.Color // hairlines
	text     ui.Color
	text2    ui.Color // secondary text, at least 4.5:1
	accent   ui.Color // the brand: what is chosen, the main action, a switch that is on
	onAccent ui.Color
	ok       ui.Color // working: running, a fast exit. Never the brand: green means "fine"
	danger   ui.Color // cut, rejected, error
	warn     ui.Color
	info     ui.Color

	lanes [5]ui.Color // one hue per route: a thin bar beside a name, never alone
}

func tokensFor(dark bool) tokens {
	if dark {
		return tokens{
			dark: true,
			bg:   ui.Hex("#0F1623"), sidebar: ui.Hex("#0A101B"),
			surface: ui.Hex("#1B2336"), surface2: ui.Hex("#273248"),
			border: ui.RGBA(255, 255, 255, 0.10),
			text:   ui.Hex("#F8FAFC"), text2: ui.Hex("#94A3B8"),
			accent: ui.Hex("#3B82F6"), onAccent: ui.Hex("#FFFFFF"),
			ok:     ui.Hex("#34D399"),
			danger: ui.Hex("#F87171"), warn: ui.Hex("#FBBF24"), info: ui.Hex("#38BDF8"),
			lanes: [5]ui.Color{ui.Hex("#38BDF8"), ui.Hex("#818CF8"), ui.Hex("#F472B6"), ui.Hex("#2DD4BF"), ui.Hex("#FBBF24")},
		}
	}
	return tokens{
		bg: ui.Hex("#F8FAFC"), sidebar: ui.Hex("#EEF2F7"),
		surface: ui.Hex("#FFFFFF"), surface2: ui.Hex("#F1F5F9"),
		border: ui.RGBA(15, 23, 42, 0.10),
		text:   ui.Hex("#0F172A"), text2: ui.Hex("#475569"),
		accent: ui.Hex("#2563EB"), onAccent: ui.Hex("#FFFFFF"),
		ok:     ui.Hex("#15803D"),
		danger: ui.Hex("#DC2626"), warn: ui.Hex("#B45309"), info: ui.Hex("#0369A1"),
		lanes: [5]ui.Color{ui.Hex("#0284C7"), ui.Hex("#6366F1"), ui.Hex("#DB2777"), ui.Hex("#0D9488"), ui.Hex("#D97706")},
	}
}

// theme is what the library's own widgets (buttons, inputs, switches, lists)
// draw with, made of the tokens.
func (k tokens) theme() *ui.Theme {
	var t ui.Theme
	if k.dark {
		t = *ui.DarkTheme()
	} else {
		t = *ui.LightTheme()
	}
	t.Background, t.Surface, t.Border = k.bg, k.surface2, k.border
	t.SurfaceHover = k.surface2.Mix(k.text, 0.06)
	t.SurfacePressed = k.surface2.Mix(k.text, 0.12)
	t.Text, t.TextMuted = k.text, k.text2
	t.Accent, t.AccentText = k.accent, k.onAccent
	t.AccentHover = k.accent.Mix(k.text, 0.10)
	t.AccentPressed = k.accent.Mix(k.text, 0.20)
	t.InactiveAccent = k.accent
	t.Danger, t.Warning, t.Success = k.danger, k.warn, k.ok
	t.Selection = k.accent.Alpha(0.28)
	t.Focus = k.info
	t.Radius, t.Spacing, t.FontSize = 8, 4, 13
	return &t
}

// The type scale (MASTER.md): 11 caption, 12 meta, 13 body, 15 heading,
// 20 page title, 28 figure.
const (
	fsCaption = 11
	fsMeta    = 12
	fsBody    = 13
	fsHeading = 15
	fsTitle   = 20
	fsFigure  = 28
)

// monoFont is for what is data: addresses, latencies, speeds, log lines.
const monoFont = "SF Mono, Menlo, monospace"
