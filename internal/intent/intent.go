package intent

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
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
	SchemaVersion int      `yaml:"schema_version"`
	Fields        []string `yaml:"fields"`
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
