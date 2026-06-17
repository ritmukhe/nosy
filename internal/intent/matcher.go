package intent

import (
	"fmt"
	"strings"
)

// fuzzyThreshold is the minimum trigram similarity for an alias to be
// considered a fuzzy match. Below this, the query is treated as unrecognized.
const fuzzyThreshold = 0.6

// ExactMatcher resolves a query against the intent name verbatim
// (e.g. "bgp.neighbors").
type ExactMatcher struct {
	lib *Library
}

func NewExactMatcher(lib *Library) *ExactMatcher { return &ExactMatcher{lib: lib} }

// Match returns the intent whose name equals query and whose nos_version range
// satisfies version, scoped to nos.
func (m *ExactMatcher) Match(query, nos, version string) (*Intent, error) {
	var candidates []*Intent
	for _, it := range m.lib.intents {
		if it.NOS == nos && it.Name == query {
			candidates = append(candidates, it)
		}
	}
	if len(candidates) == 0 {
		return nil, notFoundError(query, nos)
	}
	return pickVersion(candidates, nos, version)
}

// AliasMatcher resolves a query against intent aliases: case-insensitive exact
// match first, then trigram similarity as a fallback.
type AliasMatcher struct {
	lib *Library
}

func NewAliasMatcher(lib *Library) *AliasMatcher { return &AliasMatcher{lib: lib} }

// Match resolves query to an intent for the given nos/version. An exact
// (case-insensitive) alias hit wins outright; otherwise the highest-scoring
// alias above fuzzyThreshold is used.
func (m *AliasMatcher) Match(query, nos, version string) (*Intent, error) {
	var nosIntents []*Intent
	for _, it := range m.lib.intents {
		if it.NOS == nos {
			nosIntents = append(nosIntents, it)
		}
	}
	if len(nosIntents) == 0 {
		return nil, notFoundError(query, nos)
	}

	var exact []*Intent
	for _, it := range nosIntents {
		for _, alias := range it.Aliases {
			if strings.EqualFold(alias, query) {
				exact = append(exact, it)
				break
			}
		}
	}
	if len(exact) > 0 {
		return pickVersion(exact, nos, version)
	}

	var best *Intent
	bestScore := 0.0
	for _, it := range nosIntents {
		for _, alias := range it.Aliases {
			if s := trigramSimilarity(query, alias); s > bestScore {
				bestScore, best = s, it
			}
		}
	}
	if best == nil || bestScore < fuzzyThreshold {
		return nil, notFoundError(query, nos)
	}
	return pickVersion([]*Intent{best}, nos, version)
}

// pickVersion selects, from candidates already matched by name/alias and nos,
// the one whose range satisfies version. Because overlapping ranges are
// rejected at load time, at most one candidate per intent name can match.
// If none match, the error lists the available ranges per ADR-002.
func pickVersion(candidates []*Intent, nos, version string) (*Intent, error) {
	var matched []*Intent
	ranges := make([]string, 0, len(candidates))
	name := candidates[0].Name
	for _, it := range candidates {
		ranges = append(ranges, it.NOSVersion)
		ok, err := it.MatchesVersion(version)
		if err != nil {
			return nil, err
		}
		if ok {
			matched = append(matched, it)
		}
	}
	if len(matched) == 0 {
		return nil, versionError(name, nos, version, ranges)
	}
	return matched[0], nil
}

func notFoundError(query, nos string) error {
	return fmt.Errorf("no intent matches %q for nos %q", query, nos)
}

// versionError reproduces the actionable hard-stop from ADR-002 when an intent
// exists for the query but none of its ranges cover the device version.
func versionError(name, nos, version string, ranges []string) error {
	return fmt.Errorf(`No intent found for %s on %s/%s.
 Available version ranges: %s
 Your device: %s

 Options:
   - Update nosy: go install github.com/ritmukhe/nosy/cmd/nosy@latest
   - Contribute an intent: https://github.com/ritmukhe/nosy/blob/main/CONTRIBUTING.md
   - Use --ai flag to attempt path inference (requires schema validation to pass)`,
		name, nos, version, strings.Join(ranges, ", "), version)
}

// trigramSimilarity is the Sørensen–Dice coefficient over the two strings'
// trigram sets: 2·|A∩B| / (|A|+|B|), in [0,1]. Implemented inline to avoid a
// dependency for ~20 lines of logic.
func trigramSimilarity(a, b string) float64 {
	ta, tb := trigrams(a), trigrams(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	inter := 0
	for t := range ta {
		if tb[t] {
			inter++
		}
	}
	return 2 * float64(inter) / float64(len(ta)+len(tb))
}

func trigrams(s string) map[string]bool {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	padded := "  " + s + " "
	set := make(map[string]bool)
	for i := 0; i+3 <= len(padded); i++ {
		set[padded[i:i+3]] = true
	}
	return set
}
