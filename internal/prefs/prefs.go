// Package prefs keeps the few settings the user chooses.
package prefs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type Prefs struct {
	SystemProxy bool `json:"systemProxy"` // point the system proxy at the core while it runs
	// LightMode keeps the window shut: Faro opens in the menu bar alone, and the
	// window is made only when asked for.
	LightMode bool `json:"lightMode"`
	// MixedPort is the local proxy's port. It is chosen once and kept, so
	// that what is pointed at it (a shell's HTTPS_PROXY, a tool's settings)
	// keeps working. 0 until the first run picks one.
	MixedPort int `json:"mixedPort"`
	// DNSDomestic and DNSForeign are the ids of the DNS servers used (see the
	// list in internal/singcfg). Empty is the first of each.
	DNSDomestic string `json:"dnsDomestic"`
	DNSForeign  string `json:"dnsForeign"`
	// RuleSetsChecked is when the rule sets were last all fetched from their
	// mirrors, whether or not anything had changed (unix seconds).
	RuleSetsChecked int64 `json:"ruleSetsChecked"`
	// ExitCheck is when the exits of routes with exit:<range> are asked for on a
	// timer. It is not in the window: it is set in this file, and read again
	// after every check, so a change counts without restarting.
	ExitCheck ExitCheck `json:"exitCheck"`

	path string
}

// ExitCheck: every is a Go duration ("2m", "30s", "1h"), empty for 2 minutes;
// hours is the part of the day the timer runs in, "09:00-23:30", which may cross
// midnight ("22:00-06:00"), and empty for all day. Outside those hours a route
// keeps what its last check said; it is still checked when the core starts and
// when its node is changed, so it is never left unchecked.
type ExitCheck struct {
	Every string `json:"every,omitempty"`
	Hours string `json:"hours,omitempty"`
}

// DefaultExitEvery is how often an exit is checked when nothing is set, and
// MinExitEvery the least that is allowed.
const (
	DefaultExitEvery = 2 * time.Minute
	MinExitEvery     = 10 * time.Second
)

// Schedule reads the setting: how often, and whether a time is inside the hours.
// What cannot be read gives the default for that part, and the error says why.
func (x ExitCheck) Schedule() (every time.Duration, inHours func(time.Time) bool, err error) {
	every, inHours = DefaultExitEvery, func(time.Time) bool { return true }
	var errs []string
	if s := strings.TrimSpace(x.Every); s != "" {
		d, e := time.ParseDuration(s)
		switch {
		case e != nil:
			errs = append(errs, fmt.Sprintf("every %q: 应为 30s、2m、1h 这样的时长", s))
		case d < MinExitEvery:
			every = MinExitEvery
		default:
			every = d
		}
	}
	if s := strings.TrimSpace(x.Hours); s != "" {
		from, to, ok := parseHours(s)
		if !ok {
			errs = append(errs, fmt.Sprintf("hours %q: 应为 09:00-23:30 这样的时段", s))
		} else {
			inHours = func(t time.Time) bool {
				m := t.Hour()*60 + t.Minute()
				if from <= to {
					return m >= from && m < to
				}
				return m >= from || m < to // across midnight
			}
		}
	}
	if len(errs) > 0 {
		err = errors.New("exitCheck " + strings.Join(errs, "；"))
	}
	return every, inHours, err
}

// parseHours reads "HH:MM-HH:MM" as minutes of the day; the two must differ.
func parseHours(s string) (from, to int, ok bool) {
	a, b, found := strings.Cut(s, "-")
	if !found {
		return 0, 0, false
	}
	clock := func(v string) (int, bool) {
		t, err := time.Parse("15:04", strings.TrimSpace(v))
		if err != nil {
			return 0, false
		}
		return t.Hour()*60 + t.Minute(), true
	}
	from, ok1 := clock(a)
	to, ok2 := clock(b)
	if b = strings.TrimSpace(b); b == "24:00" {
		to, ok2 = 24*60, true
	}
	return from, to, ok1 && ok2 && from != to
}

// ReloadExitCheck reads the exit check setting from the file again, keeps it,
// and returns it: it may have been changed by hand while Faro runs.
func (p *Prefs) ReloadExitCheck() (ExitCheck, error) {
	if p.path == "" {
		return p.ExitCheck, nil
	}
	fresh, err := Load(p.path)
	if err != nil {
		return p.ExitCheck, err
	}
	p.ExitCheck = fresh.ExitCheck
	return p.ExitCheck, nil
}

// Load reads the file; a missing one gives the defaults.
func Load(path string) (*Prefs, error) {
	p := &Prefs{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	return p, json.Unmarshal(b, p)
}

func (p *Prefs) Save() error {
	b, err := json.MarshalIndent(p, "", " ")
	if err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}
