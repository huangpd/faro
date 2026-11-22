# Faro

[English](README.md) · [简体中文](README.zh-CN.md)

A native macOS client for [sing-box](https://github.com/SagerNet/sing-box).
Faro (Spanish for *lighthouse*) is a small, GPU-rendered app built on the
[mygo](https://github.com/egoist/mygo) UI framework: no WebView, no cgo. sing-box
runs as a separate process and is controlled through its Clash API.

> Status: early (0.1.0). Apple Silicon, not code-signed, no TUN mode yet.

## What it does

- **Subscriptions and nodes.** Import a subscription link or a local file; nodes
  are grouped by country, naturally sorted, with names shortened. Click a node to
  test it.
- **Exits, not a node table.** The home page shows where each kind of traffic
  leaves from. A route can hold *one* node and never switch on its own, and a
  **gate** cuts a route at once. A route whose region has no node **blocks**; it
  never falls back to another country.
- **Your own sites, in groups.** Name a group, say where it goes, add domains.
  Groups are checked before the rules text. From any live connection, one click
  adds its domain to a group.
- **Rules in a small language of their own** (not sing-box's or Clash's), with
  line-numbered errors. The page shows the config they generate. A button copies
  a prompt that teaches an AI tool the language, so you can have it write rules;
  nothing is sent anywhere by Faro.
- **Every connection explains itself.** Select one to see which rule matched and
  the path to the exit.
- **DNS split.** Domestic names are resolved directly, foreign names through the
  proxy; the system resolver is not used.
- **Community rule sets** (SagerNet's sing-geosite and sing-geoip) are kept
  current in the background, without restarting the core.
- **Light mode.** The window stays shut; the core and the menu bar keep running.
  A window costs about 150 MB, the menu bar alone about 20 MB.
- Menu-bar item with the speed, the gates and the nodes; system proxy switch;
  editable local port; dark and light appearance.

## The rules language

```
route work region DE gate      # a route: German nodes, held on one, with a gate
work domain:example.com        # a rule: this domain goes through that route
direct set:geosite-cn          # what is inside China goes direct
```

Targets are a route, `proxy` (the main group), `auto` (the fastest node),
`direct` or `block`. Rules run top to bottom; the first match wins. Run the app
and press "Copy prompt for an AI" on the Rules page for the full reference.

## Build

Requirements: macOS, Go 1.27 or later.

```sh
./scripts/fetch-singbox.sh     # the pinned sing-box core, for development and the app
./scripts/fetch-rulesets.sh    # the rule sets shipped inside the app
go test ./...                  # runs a real sing-box against fake nodes
go run .                       # run from source
./scripts/package.sh           # builds build/darwin-<arch>/faro.app and a disk image
```

Where things are: `internal/engine` holds everything Faro does (no UI);
`internal/rules` the rules language; `internal/singcfg` the sing-box config; the
pages are the `page_*.go` files in the root; `design-system/` has the design
notes and rendered previews.

## Privacy

Apart from the traffic you route through it, Faro contacts only the subscription
links you add, the rule-set mirrors, the DNS servers you pick, and one latency
test address (`cp.cloudflare.com`). There is no telemetry and no account.
Subscription data stays on your Mac in `~/Library/Application Support/Faro`.

## License

Faro is MIT licensed (see [LICENSE](LICENSE)). sing-box is a separate program with
its own license, fetched by the script above and run as its own process; see
[resources/licenses/NOTICE.txt](resources/licenses/NOTICE.txt) and check its terms
before redistributing a build that includes it. Icons are from
[Lucide](https://lucide.dev) (ISC).
