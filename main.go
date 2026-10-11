package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BurntSushi/toml"
)

type Rule struct {
	Exe     string   `toml:"exe" json:"exe"`
	Domains []string `toml:"domains,omitempty" json:"domains"`
}

type Config struct {
	WireGuard string `toml:"wireguard" json:"wireguard"`
	Listen    string `toml:"listen,omitempty" json:"listen"`
	// AllowQUIC (macOS only) leaves UDP 443 alone. There it can only be
	// refused for every program at once, not just the exes in the rules.
	AllowQUIC bool   `toml:"allow_quic,omitempty" json:"allow_quic"`
	Rules     []Rule `toml:"rule" json:"rules"`
}

var (
	cfgPath string
	logPath string
	// debug logs every captured connection, not just the tunneled ones
	debug bool
	// current is swapped whenever the config file changes on disk
	current atomic.Pointer[Config]
	// enabled is the tray's on/off switch; when off, no new connections are
	// captured
	enabled atomic.Bool

	tunnelUp  atomic.Bool
	trayReady atomic.Bool
	quitting  atomic.Bool

	// cleanup undoes system-wide changes before the process exits
	cleanup = func() {}
)

// match reports whether host is one of the rule's domains or a subdomain of
// one.
func (r *Rule) match(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return false
	}
	for _, d := range r.Domains {
		d = strings.ToLower(d)
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// matchDomain is used by the SOCKS5 proxy, which doesn't know the exe.
func (c *Config) matchDomain(host string) bool {
	for i := range c.Rules {
		if c.Rules[i].match(host) {
			return true
		}
	}
	return false
}

// ruleForExe takes a lowercase exe file name.
func (c *Config) ruleForExe(name string) *Rule {
	if name == "" {
		return nil
	}
	for i := range c.Rules {
		if strings.ToLower(c.Rules[i].Exe) == name {
			return &c.Rules[i]
		}
	}
	return nil
}

func (c *Config) logRules() {
	for _, rule := range c.Rules {
		if len(rule.Domains) == 0 {
			log.Printf("rule: %s -> all TCP traffic through the tunnel", rule.Exe)
		} else {
			log.Printf("rule: %s -> %s", rule.Exe, strings.Join(rule.Domains, ", "))
		}
	}
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// fatal replaces log.Fatal: there is no console, so show the error.
func fatal(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(msg)
	cleanup()
	showError(msg)
	os.Exit(1)
}

// openLog sends the log to a file, starting it over once it gets large.
func openLog() {
	if st, err := os.Stat(logPath); err == nil && st.Size() > 1<<20 {
		os.Remove(logPath)
	}
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		log.SetOutput(f)
	}
}

// run brings everything up and then stays in the packet loop.
func run() {
	cfg := current.Load()
	wgPath := cfg.WireGuard
	if !filepath.IsAbs(wgPath) {
		wgPath = filepath.Join(filepath.Dir(cfgPath), wgPath)
	}

	// At logon the network may not be up yet, so keep retrying the endpoint
	// lookup. Anything else is a real error.
	tnet, err := startTunnel(wgPath)
	for errors.Is(err, errResolve) {
		log.Printf("%v; retrying in 5 s", err)
		time.Sleep(5 * time.Second)
		tnet, err = startTunnel(wgPath)
	}
	if err != nil {
		fatal("Could not start the tunnel: %v", err)
	}
	log.Print("tunnel ready")
	tunnelUp.Store(true)
	setTrayState()

	// optional: plain SOCKS5 proxy
	if cfg.Listen != "" {
		p := &proxy{tnet: tnet}
		go func() { fatal("SOCKS5: %v", p.listen(cfg.Listen)) }()
		log.Printf("SOCKS5 listening on %s", cfg.Listen)
	}

	r, err := startRedirector(tnet)
	if err != nil {
		fatal("%v", err)
	}
	cfg.logRules()
	go watchConfig(r)
	fatal("%v", r.run())
}

// watchConfig reloads the rules when the config file is saved. Changes to
// "wireguard" and "listen" still need a restart.
func watchConfig(r *redirector) {
	var last time.Time
	if st, err := os.Stat(cfgPath); err == nil {
		last = st.ModTime()
	}
	for range time.Tick(2 * time.Second) {
		st, err := os.Stat(cfgPath)
		if err != nil || st.ModTime().Equal(last) {
			continue
		}
		last = st.ModTime()
		cfg, err := loadConfig(cfgPath)
		if err != nil {
			log.Printf("config is invalid, keeping the old rules: %v", err)
			continue
		}
		current.Store(cfg)
		r.forgetVerdicts()
		log.Print("config reloaded")
		cfg.logRules()
	}
}
