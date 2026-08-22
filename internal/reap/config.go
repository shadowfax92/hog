package reap

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds reap's defaults and the probe rules. It lives on disk so that
// policy — which programs deserve to be asked before they are killed — stays
// out of hog's code. hog itself knows nothing about any specific program; it
// only knows how to run a probe command and read its exit status.
type Config struct {
	Defaults Defaults `yaml:"defaults"`
	Protect  []string `yaml:"protect"`
	Probes   []Probe  `yaml:"probes"`
}

// Defaults are the predicate thresholds used when the corresponding flag is
// not given. They are strings so the file can use human units ("12h", "200M").
type Defaults struct {
	Older  string `yaml:"older"`
	Duty   string `yaml:"duty"`
	MinMem string `yaml:"min_mem"`
}

// Probe asks a matching process whether it is safe to kill. Ask runs under
// `sh -c` with {pid} substituted; exit status 0 means safe to reap, anything
// else means protect. This is the only mechanism by which reap can learn that
// a process holds unsaved state, and it is deliberately external: a passive
// signal (age, memory, process tree shape) cannot distinguish an editor with
// a dirty buffer from an identical one without.
type Probe struct {
	Match     string `yaml:"match"`
	Ask       string `yaml:"ask"`
	OnUnknown string `yaml:"on_unknown"` // "protect" (default) or "reap"
	Label     string `yaml:"label"`      // shown when the probe protects a process
}

// DefaultConfig is written on first run. The nvim rule is an example of the
// probe mechanism, not special-casing inside hog: it lives in user-editable
// config precisely so the same pattern can be copied for any other program
// that owns unsaved state.
const DefaultConfig = `# hog reap configuration
#
# Predicates select processes that are old, dormant, and expensive. Probes then
# ask matching processes whether they are actually safe to kill.

defaults:
  older: 12h     # process must have been alive at least this long
  duty: 1.0      # lifetime CPU / wall-clock must be below this percent
  min_mem: 200M  # footprint must be at least this large to be worth reaping

# Never reap a process whose executable name contains any of these.
# Uncomment what you would rather not lose to a sweep. Long-running agent
# sessions and browser renderers both qualify on the predicates — they are old,
# nearly idle, and large — but killing them discards live conversation state or
# an open tab, which no measurement can see.
protect: []
  # - claude
  # - codex
  # - Google Chrome

# Ask before killing. {pid} is substituted.
#   exit 0 = safe to reap    exit 2 = could not tell (on_unknown decides)
#   any other exit status = protect
probes:
  # Neovim traps SIGTERM as a deadly signal: it exits without prompting, and
  # with 'swapfile' off there is no recovery file either. So ask it over RPC
  # how many modified buffers it has, and only reap it when the answer is 0.
  - match: nvim
    label: unsaved buffers
    on_unknown: protect
    ask: |
      # Try every unix socket the process holds rather than guessing the
      # socket's name: --listen lets it be called anything.
      for sock in $(lsof -a -p {pid} -U -Fn 2>/dev/null | grep '^n/' | cut -c2-); do
        n=$(nvim --server "$sock" --remote-expr 'len(filter(getbufinfo({"bufloaded":1}),"v:val.changed"))' 2>/dev/null) || continue
        case "$n" in ''|*[!0-9]*) continue ;; esac
        [ "$n" = "0" ] && exit 0
        exit 1
      done
      exit 2
`

// ConfigPath is where reap reads and writes its configuration. It follows the
// XDG convention rather than os.UserConfigDir, which on macOS points at
// ~/Library/Application Support — a place command-line tools are not expected
// to keep editable config, and awkward to reach from a shell.
func ConfigPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "hog", "reap.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".config", "hog", "reap.yaml")
}

// LoadConfig reads the config, creating it from DefaultConfig on first run so
// the shipped probe rules are visible and editable rather than invisible
// defaults compiled into the binary. created reports whether it was just
// written, so the caller can tell the user where it landed.
func LoadConfig(path string) (cfg Config, created bool, err error) {
	raw, readErr := os.ReadFile(path)
	if os.IsNotExist(readErr) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return cfg, false, err
		}
		if err := os.WriteFile(path, []byte(DefaultConfig), 0o644); err != nil {
			return cfg, false, err
		}
		raw, created = []byte(DefaultConfig), true
	} else if readErr != nil {
		return cfg, false, readErr
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return cfg, created, err
	}
	return cfg, created, nil
}
