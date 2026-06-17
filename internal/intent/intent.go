package intent

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

// Intent represents a resolved operator query mapped to YANG paths.
// MULTI-VENDOR: NOS and version constraints are explicit fields —
// the core query/render pipeline has no NOS-specific logic.
type Intent struct {
	Name       string   `yaml:"intent"`
	NOS        string   `yaml:"nos"`
	NOSVersion string   `yaml:"nos_version"` // semver range, e.g. ">=23.10 <26.0"
	Aliases    []string `yaml:"aliases"`
	Paths      []string `yaml:"paths"`
	Output     Output   `yaml:"output"`
}

type Output struct {
	SchemaVersion int    `yaml:"schema_version"`
	// RowKey names the list whose items define one output row (e.g. "neighbor"
	// for bgp.neighbors). Empty means best-effort row detection.
	RowKey string   `yaml:"row_key"`
	Fields []string `yaml:"fields"`
}

// MatchesVersion returns true if deviceVersion satisfies the intent's nos_version range.
func (i *Intent) MatchesVersion(deviceVersion string) (bool, error) {
	constraint, err := semver.NewConstraint(i.NOSVersion)
	if err != nil {
		return false, fmt.Errorf("parsing nos_version constraint %q: %w", i.NOSVersion, err)
	}
	v, err := semver.NewVersion(deviceVersion)
	if err != nil {
		return false, fmt.Errorf("parsing device version %q: %w", deviceVersion, err)
	}
	return constraint.Check(v), nil
}

// Matcher resolves operator input to a concrete Intent.
// Implementations: ExactMatcher (curated library), FuzzyMatcher (alias similarity).
// MULTI-VENDOR: implementations must accept nos and version to scope resolution.
type Matcher interface {
	Match(query, nos, version string) (*Intent, error)
}

// Library is the loaded, validated set of intents from the on-disk YAML tree.
type Library struct {
	intents []*Intent
}

// Intents returns the loaded intents. The slice is owned by the library;
// callers must not mutate it.
func (l *Library) Intents() []*Intent { return l.intents }

// Load walks the on-disk directory root, deserializing every .yaml/.yml file
// into an Intent and validating the set. It is a thin wrapper over LoadFS.
func Load(root string) (*Library, error) {
	return LoadFS(os.DirFS(root), ".")
}

// LoadFS walks root within fsys, deserializing every .yaml/.yml file into an
// Intent and validating the set. Per ADR-002 validation is at load time so
// contributor mistakes fail fast rather than surfacing at query time.
//
// Operating on fs.FS lets the same loader serve the on-disk tree (Load) and the
// embedded tree baked into the binary (intentlib.LoadEmbedded).
func LoadFS(fsys fs.FS, root string) (*Library, error) {
	var intents []*Intent

	walkErr := fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".yaml", ".yml":
		default:
			return nil
		}

		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("reading intent %s: %w", path, err)
		}
		var it Intent
		if err := yaml.Unmarshal(data, &it); err != nil {
			return fmt.Errorf("parsing intent %s: %w", path, err)
		}
		if err := it.validate(); err != nil {
			return fmt.Errorf("invalid intent %s: %w", path, err)
		}
		intents = append(intents, &it)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("loading intent library: %w", walkErr)
	}

	if err := checkOverlaps(intents); err != nil {
		return nil, err
	}
	return &Library{intents: intents}, nil
}

// validate enforces the per-intent invariants from ADR-002.
func (i *Intent) validate() error {
	if i.Name == "" {
		return fmt.Errorf("missing intent name")
	}
	if i.NOS == "" {
		return fmt.Errorf("intent %q: missing nos", i.Name)
	}
	if i.NOSVersion == "" {
		return fmt.Errorf("intent %q: missing nos_version", i.Name)
	}
	if _, err := semver.NewConstraint(i.NOSVersion); err != nil {
		return fmt.Errorf("intent %q: invalid nos_version %q: %w", i.Name, i.NOSVersion, err)
	}
	if len(i.Paths) == 0 {
		return fmt.Errorf("intent %q: no paths", i.Name)
	}
	return nil
}

// checkOverlaps rejects any two intents sharing the same intent+nos whose
// version ranges admit a common device version. ADR-002: overlap is a load
// error, not a runtime warning — ambiguous resolution must never reach a query.
func checkOverlaps(intents []*Intent) error {
	type key struct{ name, nos string }
	groups := make(map[key][]*Intent)
	for _, it := range intents {
		k := key{it.Name, it.NOS}
		groups[k] = append(groups[k], it)
	}

	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].name != keys[b].name {
			return keys[a].name < keys[b].name
		}
		return keys[a].nos < keys[b].nos
	})

	for _, k := range keys {
		g := groups[k]
		for a := 0; a < len(g); a++ {
			for b := a + 1; b < len(g); b++ {
				overlap, err := rangesOverlap(g[a].NOSVersion, g[b].NOSVersion)
				if err != nil {
					return err
				}
				if overlap {
					return fmt.Errorf(
						"overlapping nos_version ranges for %s on %s: %q and %q both match some version",
						k.name, k.nos, g[a].NOSVersion, g[b].NOSVersion,
					)
				}
			}
		}
	}
	return nil
}

var versionLiteral = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)

// rangesOverlap reports whether two semver constraints admit a common version.
// Masterminds has no analytic overlap test, so we sample: any concrete version
// satisfying both constraints proves overlap. Sampling can miss an overlap but
// never invent one, so a true result is always sound.
func rangesOverlap(a, b string) (bool, error) {
	ca, err := semver.NewConstraint(a)
	if err != nil {
		return false, fmt.Errorf("parsing constraint %q: %w", a, err)
	}
	cb, err := semver.NewConstraint(b)
	if err != nil {
		return false, fmt.Errorf("parsing constraint %q: %w", b, err)
	}
	for _, v := range versionCandidates(a, b) {
		if ca.Check(v) && cb.Check(v) {
			return true, nil
		}
	}
	return false, nil
}

// versionCandidates yields sample versions to probe two constraints: a low
// sentinel (catches two upper-open ranges) plus every version literal in either
// constraint and its patch/minor/major increments (to land just inside an
// exclusive upper bound, e.g. testing 25.0.1 against "<25.1").
func versionCandidates(constraints ...string) []*semver.Version {
	seen := make(map[string]bool)
	var out []*semver.Version
	add := func(v *semver.Version) {
		if v == nil || seen[v.String()] {
			return
		}
		seen[v.String()] = true
		out = append(out, v)
	}

	if zero, err := semver.NewVersion("0.0.0"); err == nil {
		add(zero)
	}
	for _, c := range constraints {
		for _, lit := range versionLiteral.FindAllString(c, -1) {
			v, err := semver.NewVersion(lit)
			if err != nil {
				continue
			}
			add(v)
			patch := v.IncPatch()
			add(&patch)
			minor := v.IncMinor()
			add(&minor)
			major := v.IncMajor()
			add(&major)
		}
	}
	return out
}
