// Package prefs keeps the few settings the user chooses.
package prefs

import (
	"encoding/json"
	"errors"
	"os"
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

	path string
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
