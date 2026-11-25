package sub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"faro/internal/rules"
	"faro/internal/singcfg"
)

// Subscription is a provider's URL and the nodes last fetched from it.
type Subscription struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	URL     string    `json:"url"`
	Updated time.Time `json:"updated"`
	Nodes   []Node    `json:"nodes"`
	// From the subscription-userinfo header, in bytes and unix seconds.
	Used, Total, Expire int64
}

// Store keeps subscriptions and the chosen node in one JSON file.
type Store struct {
	mu   sync.Mutex
	path string
	Subs []*Subscription `json:"subs"`
	// Choices is the node picked for each route, by tag, "proxy" being the
	// main group. A route not in it is on its default.
	Choices  map[string]string `json:"choices"`
	Selected string            `json:"selected,omitempty"` // before Choices: the main group's
}

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, err
	}
	if s.Selected != "" { // an older file
		if s.Choices == nil {
			s.Choices = map[string]string{}
		}
		if _, ok := s.Choices[singcfg.TagProxy]; !ok {
			s.Choices[singcfg.TagProxy] = s.Selected
		}
		s.Selected = ""
	}
	return s, nil
}

func (s *Store) save() error {
	b, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Snapshot returns the subscriptions and the choices, for display.
func (s *Store) Snapshot() ([]Subscription, map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Subscription, len(s.Subs))
	for i, x := range s.Subs {
		out[i] = *x
	}
	return out, s.choices()
}

func (s *Store) choices() map[string]string {
	c := make(map[string]string, len(s.Choices))
	for k, v := range s.Choices {
		c[k] = v
	}
	return c
}

// Choices returns the node picked for each route.
func (s *Store) ChoicesCopy() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.choices()
}

// SetChoice remembers the node of a route; "" forgets it.
func (s *Store) SetChoice(route, tag string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Choices == nil {
		s.Choices = map[string]string{}
	}
	if tag == "" {
		delete(s.Choices, route)
	} else {
		s.Choices[route] = tag
	}
	return s.save()
}

// LocalPath returns the file a subscription reference names: a path, a
// path under ~, or a file:// URL. ok is false for anything else.
func LocalPath(ref string) (path string, ok bool) {
	ref = strings.TrimSpace(ref)
	switch {
	case strings.HasPrefix(ref, "file://"):
		u, err := url.Parse(ref)
		if err != nil {
			return "", false
		}
		ref = u.Path
	case strings.HasPrefix(ref, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		ref = filepath.Join(home, ref[2:])
	}
	return ref, strings.HasPrefix(ref, "/")
}

// Add downloads and stores a subscription: from a URL, or from a file that
// another client downloaded, which Faro then reads again on every refresh.
func (s *Store) Add(ctx context.Context, ref string) error {
	ref = strings.TrimSpace(ref)
	x := &Subscription{ID: newID(), URL: ref}
	if path, ok := LocalPath(ref); ok {
		x.URL, x.Name = path, filepath.Base(path)
	} else {
		u, err := url.Parse(ref)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("enter an http(s) subscription URL, or the path of a subscription file")
		}
		x.Name = u.Hostname()
	}
	if err := fetchInto(ctx, x); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Subs = append(s.Subs, x)
	return s.save()
}

// Refresh downloads a subscription again. A failed refresh keeps the
// nodes it had.
func (s *Store) Refresh(ctx context.Context, id string) error {
	s.mu.Lock()
	var cur *Subscription
	for _, x := range s.Subs {
		if x.ID == id {
			c := *x
			cur = &c
		}
	}
	s.mu.Unlock()
	if cur == nil {
		return errors.New("no such subscription")
	}
	if err := fetchInto(ctx, cur); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.Subs {
		if x.ID == id {
			s.Subs[i] = cur
		}
	}
	return s.save()
}

// Replace puts these subscriptions and choices in place of all that is kept,
// as an import does, and saves them. Each subscription is copied.
func (s *Store) Replace(subs []Subscription, choices map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Subs = make([]*Subscription, len(subs))
	for i := range subs {
		x := subs[i]
		s.Subs[i] = &x
	}
	s.Choices = make(map[string]string, len(choices))
	for k, v := range choices {
		s.Choices[k] = v
	}
	return s.save()
}

func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.Subs {
		if x.ID == id {
			s.Subs = append(s.Subs[:i], s.Subs[i+1:]...)
			break
		}
	}
	return s.save()
}

// Outbounds lists every node with a tag unique across subscriptions and
// clear of the names the config reserves.
func (s *Store) Outbounds() []singcfg.Outbound {
	s.mu.Lock()
	defer s.mu.Unlock()
	taken := map[string]bool{singcfg.TagProxy: true, singcfg.TagAuto: true, singcfg.TagDirect: true, rules.GateTag: true}
	var out []singcfg.Outbound
	for _, x := range s.Subs {
		for _, n := range x.Nodes {
			name := n.Name
			if strings.HasPrefix(name, rules.GroupPrefix) { // the prefix is the groups': rename first,
				name = "node " + name //                       or no suffix would ever clear it
			}
			tag := name
			for i := 2; taken[tag]; i++ {
				tag = fmt.Sprintf("%s #%d", name, i)
			}
			taken[tag] = true
			out = append(out, singcfg.Outbound{Tag: tag, Out: n.Out})
		}
	}
	return out
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// UserAgents are the client names Faro presents to a subscription server,
// in the order tried. Providers often answer only the clients they know and
// serve anything else a notice (or a 403); of those they know, Clash Verge
// and ClashX are the most widely accepted. At most these many requests are
// made, one after another, and only while the answer is a refusal.
var UserAgents = []string{"clash-verge/v2.2.3", "ClashX/1.118.0", "clash.meta"}

// FetchAny downloads a subscription and returns the first answer that holds
// nodes, with the client name that got it.
func FetchAny(ctx context.Context, rawURL string) (body []byte, hdr http.Header, ua string, err error) {
	for i, name := range UserAgents {
		r, ferr := FetchWith(ctx, rawURL, name, true)
		if ferr == nil {
			if _, perr := Parse(r.Body); perr == nil {
				return r.Body, r.Header, name, nil
			} else {
				ferr = perr
			}
		}
		err = ferr
		if i < len(UserAgents)-1 {
			time.Sleep(time.Second) // not a burst: a server counting requests may take it for abuse
		}
	}
	return nil, nil, "", fmt.Errorf("the server refused every client name tried (%d): %w", len(UserAgents), err)
}

// Fetch downloads a subscription's body.
func Fetch(ctx context.Context, rawURL string) ([]byte, http.Header, error) {
	body, hdr, _, err := FetchAny(ctx, rawURL)
	return body, hdr, err
}

// Response is what a server answered, kept even when it was a refusal.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// FetchWith downloads with the given User-Agent. viaEnvProxy says whether
// the proxy environment variables (HTTP_PROXY…) of this process apply.
func FetchWith(ctx context.Context, rawURL, ua string, viaEnvProxy bool) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("User-Agent", ua)
	client := http.DefaultClient
	if !viaEnvProxy {
		client = &http.Client{Transport: &http.Transport{Proxy: nil}}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	r := Response{Status: resp.StatusCode, Header: resp.Header, Body: body}
	if err == nil && resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("subscription: %s", resp.Status)
	}
	return r, err
}

func fetchInto(ctx context.Context, x *Subscription) error {
	var body []byte
	var hdr http.Header
	var err error
	if path, ok := LocalPath(x.URL); ok {
		body, err = readFile(path)
		hdr = http.Header{}
	} else {
		body, hdr, err = Fetch(ctx, x.URL)
	}
	if err != nil {
		return err
	}
	nodes, err := Parse(body)
	if err != nil {
		return err
	}
	x.Nodes, x.Updated = nodes, time.Now()
	x.Used, x.Total, x.Expire = parseUserinfo(hdr.Get("Subscription-Userinfo"))
	return nil
}

// readFile reads a subscription file, at most as large as one downloaded.
func readFile(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("subscription file: %w", err)
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("%s is a folder, not a subscription file", path)
	}
	if fi.Size() > 20<<20 {
		return nil, errors.New("subscription file is too large")
	}
	return os.ReadFile(path)
}

// parseUserinfo reads "upload=1; download=2; total=3; expire=4".
func parseUserinfo(h string) (used, total, expire int64) {
	var up, down int64
	for _, kv := range strings.Split(h, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if !ok {
			continue
		}
		n, _ := strconv.ParseInt(v, 10, 64)
		switch k {
		case "upload":
			up = n
		case "download":
			down = n
		case "total":
			total = n
		case "expire":
			expire = n
		}
	}
	return up + down, total, expire
}
