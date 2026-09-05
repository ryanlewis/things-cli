// Package config loads persistent defaults for the things CLI from a TOML
// file. The file only seeds kong's flag resolution, so precedence stays
// flag > config file > built-in default with no second code path.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alecthomas/kong"
	toml "github.com/pelletier/go-toml/v2"
)

// EnvVar names the environment variable that overrides the default path.
const EnvVar = "THINGS_CLI_CONFIG"

// Where the path in use came from, reported by `things config path`.
const (
	SourceFlag    = "flag"
	SourceEnv     = "env"
	SourceDefault = "default"
)

const (
	dirName  = "things-cli"
	fileName = "config.toml"

	// dbKey is both the TOML key and the flag name for the database path; it
	// is the one setting that needs validating before it reaches kong.
	dbKey = "db"
)

// Error is a problem with the config file itself: unreadable, malformed, or
// carrying a key or value the CLI cannot use. It is reported on its own rather
// than through kong's usage output, because usage is not what is wrong.
type Error struct {
	Path string
	Err  error
}

func (e *Error) Error() string { return fmt.Sprintf("config file %s: %v", e.Path, e.Err) }
func (e *Error) Unwrap() error { return e.Err }

func configErr(path string, format string, args ...any) *Error {
	return &Error{Path: path, Err: fmt.Errorf(format, args...)}
}

// Key describes one setting: the TOML key it is written as, the flag it seeds,
// and the value that applies when neither the file nor a flag supplies one.
type Key struct {
	Name     string   // canonical TOML key, snake_case
	Flag     string   // kong flag name this key resolves
	Default  any      // built-in default; bool or string
	Enum     []string // permitted values, for keys with a fixed set
	Excludes []string // keys this one cannot be combined with
	Commands []string // commands whose flags this key may seed; empty means all
	Comment  []string // template comment, one line per entry
	Example  string   // template assignment, written commented out
}

// Keys is the full set of settings the config file may carry. Every entry must
// name a flag that exists on the CLI; TestConfigKeysMatchFlags enforces that.
var Keys = []Key{
	{
		Name:    "json",
		Flag:    "json",
		Default: false,
		Comment: []string{
			"Print JSON instead of the plain text listing. Same as --json / -j.",
			"Turning this on changes the output of every command, including for",
			"agents and scripts that expect plain text.",
		},
		Example: "json = false",
	},
	{
		Name:    "color",
		Flag:    "color",
		Default: "auto",
		Enum:    []string{"auto", "always", "never"},
		Comment: []string{
			`Color mode: "auto", "always" or "never". Same as --color.`,
			`"auto" colours only when stdout is a terminal.`,
		},
		Example: `color = "auto"`,
	},
	{
		Name:    "hints",
		Flag:    "hints",
		Default: true,
		Comment: []string{
			"Print the hint line under a plain task listing. Same as --hints /",
			"--no-hints. The hint only ever appears when stdout is a terminal and",
			"the output is not JSON, so turning it off is for terminal use.",
		},
		Example: "hints = true",
	},
	{
		Name:    "db",
		Flag:    "db",
		Default: "",
		Comment: []string{
			"Path to the Things3 SQLite database. Same as --db.",
			"Leave unset to let things-cli find it. The file must exist.",
		},
		Example: `db = "~/Library/Group Containers/JLMPQHK86H.com.culturedcode.ThingsMac/Things Database.thingsdatabase/main.sqlite"`,
	},
	{
		Name:    "no_verify",
		Flag:    "no-verify",
		Default: false,
		Comment: []string{
			"Skip the read-back that confirms a complete/cancel actually landed.",
			"Same as --no-verify. Faster, but a write Things silently drops is",
			"then reported as a success.",
		},
		Example: "no_verify = false",
	},
	{
		Name:     "strict_tags",
		Flag:     "strict-tags",
		Default:  false,
		Excludes: []string{"create_tags"},
		Comment: []string{
			"Fail instead of writing when a tag does not exist in Things.",
			"Same as --strict-tags. Off by default, which warns and writes anyway.",
			"Mutually exclusive with create_tags.",
		},
		Example: "strict_tags = false",
	},
	{
		Name:     "create_tags",
		Flag:     "create-tags",
		Default:  false,
		Excludes: []string{"strict_tags"},
		Comment: []string{
			"Create tags that do not exist in Things before writing.",
			"Same as --create-tags. Mutually exclusive with strict_tags.",
		},
		Example: "create_tags = false",
	},
	{
		Name:     "assume_yes",
		Flag:     "yes",
		Default:  false,
		Commands: []string{"complete", "cancel"},
		Comment: []string{
			"Answer the confirmation asked before a project-wide complete or",
			"cancel, instead of prompting. Same as --yes / -y on those two",
			"commands; the --yes on `skill install` and `skill uninstall` is",
			"deliberately not covered, so this cannot delete an installed skill.",
			"Turning this on removes the last check before a project and every",
			"task in it changes status.",
		},
		Example: "assume_yes = false",
	},
}

// KeyNames lists the canonical key names in declaration order.
func KeyNames() []string {
	names := make([]string, len(Keys))
	for i, k := range Keys {
		names[i] = k.Name
	}
	return names
}

// lookup finds the key a TOML name refers to. Both the canonical snake_case
// name and the hyphenated flag spelling are accepted.
func lookup(name string) (Key, bool) {
	for _, k := range Keys {
		if name == k.Name || name == k.Flag {
			return k, true
		}
	}
	return Key{}, false
}

// DefaultPath is $XDG_CONFIG_HOME/things-cli/config.toml, falling back to
// ~/.config/things-cli/config.toml.
func DefaultPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("cannot locate the config file: neither $XDG_CONFIG_HOME nor $HOME is set")
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, dirName, fileName), nil
}

// ResolvePath picks the config file to use: an explicit --config path wins,
// then $THINGS_CLI_CONFIG, then the default location.
func ResolvePath(explicit string) (path, source string, err error) {
	if explicit != "" {
		return kong.ExpandPath(explicit), SourceFlag, nil
	}
	if env := os.Getenv(EnvVar); env != "" {
		return kong.ExpandPath(env), SourceEnv, nil
	}
	p, err := DefaultPath()
	if err != nil {
		return "", "", err
	}
	return p, SourceDefault, nil
}

// File is a loaded config file. A file that does not exist loads fine with
// Exists false and no values; a missing config file is not an error.
type File struct {
	Path   string
	Source string
	Exists bool

	// values holds only the keys the file actually set, under their canonical
	// names.
	values map[string]any
}

// Load reads and validates the config file at path. A file that is not there
// yields an empty File rather than an error.
func Load(path string) (*File, error) {
	f := &File{Path: path, Source: SourceDefault, values: map[string]any{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return nil, configErr(path, "cannot read: %w", err)
	}
	f.Exists = true

	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, configErr(path, "invalid TOML: %s", tomlErrorText(err))
	}

	for name, value := range raw {
		key, ok := lookup(name)
		if !ok {
			return nil, configErr(path, "unknown key %q (valid keys: %s)",
				name, strings.Join(KeyNames(), ", "))
		}
		if _, dup := f.values[key.Name]; dup {
			return nil, configErr(path, "key %q is set twice (%q and %q name the same setting)",
				key.Name, key.Name, key.Flag)
		}
		v, err := coerce(key, value)
		if err != nil {
			return nil, &Error{Path: path, Err: err}
		}
		f.values[key.Name] = v
	}

	if err := f.checkExclusions(); err != nil {
		return nil, err
	}
	return f, nil
}

// checkExclusions rejects two mutually exclusive settings both turned on. Kong
// would catch it too, but only once a command that carries the flags is run,
// and its message says nothing about the config file.
func (f *File) checkExclusions() error {
	for _, k := range Keys {
		if f.values[k.Name] != true {
			continue
		}
		for _, name := range k.Excludes {
			if f.values[name] == true {
				return configErr(f.Path, "%q and %q cannot both be true", k.Name, name)
			}
		}
	}
	return nil
}

// coerce checks a raw TOML value against the key's type and enum.
func coerce(key Key, value any) (any, error) {
	switch key.Default.(type) {
	case bool:
		b, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("key %q must be a boolean, got %s", key.Name, typeName(value))
		}
		return b, nil
	case string:
		s, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("key %q must be a string, got %s", key.Name, typeName(value))
		}
		if len(key.Enum) > 0 && !contains(key.Enum, s) {
			return nil, fmt.Errorf("key %q must be one of %s, got %q",
				key.Name, strings.Join(key.Enum, ", "), s)
		}
		return s, nil
	default:
		return nil, fmt.Errorf("key %q has an unsupported type %T", key.Name, key.Default)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func typeName(v any) string {
	switch v.(type) {
	case bool:
		return "boolean"
	case string:
		return "string"
	case int64, float64:
		return "number"
	case map[string]any:
		return "table"
	case []any:
		return "array"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// tomlErrorText renders a decode error on one line, with the row and column
// the parser stopped at, instead of go-toml's multi-line excerpt.
func tomlErrorText(err error) string {
	var de *toml.DecodeError
	if errors.As(err, &de) {
		row, col := de.Position()
		return fmt.Sprintf("line %d, column %d: %s", row, col, strings.TrimPrefix(de.Error(), "toml: "))
	}
	return err.Error()
}

// Setting is one key's effective value and where it came from, as reported by
// `things config show`.
type Setting struct {
	Key    string `json:"key"`
	Value  any    `json:"value"`
	Source string `json:"source"` // "config" or "default"
}

// Settings reports the default that applies to each key once the file is taken
// into account — that is, the value a command sees when no flag overrides it.
func (f *File) Settings() []Setting {
	out := make([]Setting, 0, len(Keys))
	for _, k := range Keys {
		s := Setting{Key: k.Name, Value: k.Default, Source: "default"}
		if f != nil {
			if v, ok := f.values[k.Name]; ok {
				s.Value, s.Source = v, "config"
			}
		}
		out = append(out, s)
	}
	return out
}

// JSON reports the json default the file establishes. It exists for the one
// decision that has to be made before kong parses: whether a failure is
// rendered as JSON or as a plain line.
func (f *File) JSON() bool {
	if f == nil {
		return false
	}
	v, _ := f.values["json"].(bool)
	return v
}

// Resolver seeds kong's flag resolution from the file. It returns nil for any
// flag the file does not mention, leaving kong's own default in place.
func (f *File) Resolver() kong.Resolver {
	values := map[string]any{}
	excludes := map[string][]string{}
	commands := map[string][]string{}
	path := ""
	if f != nil {
		path = f.Path
		for _, k := range Keys {
			v, ok := f.values[k.Name]
			if !ok {
				continue
			}
			// An empty db path is how a file says "leave it unset". Passing it
			// on would make kong's existingfile check stat the empty string,
			// which expands to the working directory.
			if k.Name == dbKey && v == "" {
				continue
			}
			// For a key in an exclusive pair, false means the same as absent,
			// and kong's exclusivity check counts any resolved value as set —
			// so `strict_tags = true` alongside `create_tags = false` would
			// read as both flags being passed at once.
			if len(k.Excludes) > 0 && v == false {
				continue
			}
			values[k.Flag] = v
			excludes[k.Flag] = excludingFlags(k)
			commands[k.Flag] = k.Commands
		}
	}
	return kong.ResolverFunc(func(_ *kong.Context, parent *kong.Path, flag *kong.Flag) (any, error) {
		v, ok := values[flag.Name]
		if !ok {
			return nil, nil
		}
		// A key restricted to certain commands must not seed a same-named flag
		// elsewhere: `--yes` also means "overwrite" and "delete" on the skill
		// commands, and assume_yes is not asking for either.
		if cmds := commands[flag.Name]; len(cmds) > 0 && !slices.Contains(cmds, parent.Node().Path()) {
			return nil, nil
		}
		// A flag on the command line beats the file, and so does the flag it
		// is mutually exclusive with: kong counts a resolved value as set, so
		// answering here would turn `--strict-tags` against a file that says
		// `create_tags = true` into "can't be used together" rather than the
		// override it is. Only one of a pair ever reaches this map, so a set
		// partner can only have come from the command line.
		for _, name := range excludes[flag.Name] {
			for _, other := range parent.Flags {
				if other.Name == name && other.Set {
					return nil, nil
				}
			}
		}
		// A db path that does not exist is reported here, naming the config
		// file, rather than as a bare flag error. Kong skips resolvers for
		// flags given on the command line, so --db still overrides a stale
		// entry instead of tripping over it.
		if flag.Name == dbKey {
			if s, ok := v.(string); ok {
				if _, err := os.Stat(kong.ExpandPath(s)); err != nil {
					return nil, configErr(path, "db: %s", err)
				}
			}
		}
		return v, nil
	})
}

// excludingFlags maps a key's excluded keys onto the flag names they seed.
func excludingFlags(k Key) []string {
	if len(k.Excludes) == 0 {
		return nil
	}
	flags := make([]string, 0, len(k.Excludes))
	for _, name := range k.Excludes {
		if other, ok := lookup(name); ok {
			flags = append(flags, other.Flag)
		}
	}
	return flags
}

// Template is the commented file `things config init` writes: every key, its
// built-in value, all commented out.
func Template() string {
	var b strings.Builder
	b.WriteString("# things-cli configuration\n")
	b.WriteString("#\n")
	b.WriteString("# Defaults for the flags below. A flag passed on the command line always\n")
	b.WriteString("# wins: flag > this file > built-in default.\n")
	b.WriteString("#\n")
	b.WriteString("# Read from $XDG_CONFIG_HOME/things-cli/config.toml, or\n")
	b.WriteString("# ~/.config/things-cli/config.toml. Override with --config PATH or\n")
	b.WriteString("# $" + EnvVar + ". A missing file is not an error.\n")
	b.WriteString("#\n")
	b.WriteString("# Uncomment a line to change the default.\n")
	for _, k := range Keys {
		b.WriteString("\n")
		for _, line := range k.Comment {
			b.WriteString("# " + line + "\n")
		}
		b.WriteString("# " + k.Example + "\n")
	}
	return b.String()
}
