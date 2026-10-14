// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

// Package declarations validates Apple declarative device management declarations before they are
// sent, against a generated table of Apple's own schemas, built from the `declarative` directory of
// apple/device-management by `make apple-schemas`.
//
// The table is derived from github.com/apple/device-management, Copyright © Apple Inc., MIT
// licensed; the licence text is in ../LICENSE-apple.
//
// # Why this exists
//
// Jamf validates nothing here. Wire probing on 2026-09-10 established that the blueprints service
// accepts and stores verbatim every malformed declaration put to it — an unknown declaration type,
// an invented key, a wrong-cased key, a value of the wrong type, a value outside a declared enum,
// an integer outside a declared range, a required key omitted — and returns 201 for all of them. A
// deploy of the same blueprint returns 202 and reports SUCCEEDED. The Jamf Pro editor then renders
// the declaration's generated form with the offending key blank, and the device never receives it.
// So the failure is silent at every layer the operator can see, which is exactly why the check
// belongs in the consumer's plan or pre-flight check, and why every finding here is an error rather than a warning: there
// is no point letting through a declaration that cannot work on the device.
//
// # How the service behaves, and why the findings are classified as they are
//
// The editor's behaviour distinguishes two stages, which is what made the rules observable: a
// recognised key ticks its own checkbox, and its value populates only if the value is acceptable.
// "Ticked but empty" therefore means the name was understood and the value rejected, while
// "unticked" means the name itself was never recognised.
//
//   - Key names are matched CASE-SENSITIVELY and a wrong-cased key is discarded. This is the
//     opposite of configuration profile payloads, where Jamf silently restores Apple's spelling —
//     see package profiles (appleschemas/profiles). The two packages must not share case handling.
//   - A declaration type is likewise matched case-sensitively; a wrong-cased type renders no card
//     at all.
//   - A value outside a declared `rangelist` is dropped, so an enum violation never applies.
//   - A numeric value outside a declared `range` is NOT rejected: the service stores and renders
//     it unchanged. It is still an error here, because the device is what rejects it, and a warning
//     would leave the operator with a declaration that silently never applies.
//   - `kind` is accepted in any pairing with `type`, so a mismatch reaches the device as a
//     malformed declaration. The pairing is determined by the type's prefix, so no table is needed.
//
// # Snapshot currency
//
// The table is the union of Apple's release branch and its newest seed (pre-release) branch,
// because Jamf's generative-declarations service tracks seed: at the time of writing seed carries
// 12 configuration declaration types and keys such as `siri.settings.AllowSiriAI` that release does
// not, all of them already offered in the Jamf UI. Validating against release alone would reject
// working configurations. Schema.SeedOnly reports which side a key came from so a diagnostic can
// say so.
//
// Because an unrecognised name is an error, a stale table blocks a working configuration. Two
// things mitigate that: the table is refreshed daily by a scheduled pull request, and a consumer
// should offer an escape hatch that downgrades these findings, for the window between Apple
// publishing and the consumer picking up the SDK release that carries the refresh.
//
// What none of this can prove is that Jamf tracks the *same* seed branch the table unions. That is
// inferred from `AllowSiriAI` appearing in the Jamf picker while absent from release. If false
// positives reappear for keys the UI offers, re-probe that assumption first.
package declarations

import (
	_ "embed"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/blueprints"
)

//go:embed declarations.json
var embeddedTable []byte

// Kind is the value type Apple declares for a key.
type Kind string

// The declared value types. KindAny accepts anything, and is also what the generator falls back to
// for a type it does not recognise, so an upstream vocabulary change cannot become a false finding.
const (
	KindBoolean    Kind = "boolean"
	KindInteger    Kind = "integer"
	KindReal       Kind = "real"
	KindString     Kind = "string"
	KindData       Kind = "data"
	KindDate       Kind = "date"
	KindArray      Kind = "array"
	KindDictionary Kind = "dictionary"
	KindAny        Kind = "any"
)

// Schema is the declared shape of one key's value.
type Schema struct {
	Type     Kind     `json:"type"`
	Required bool     `json:"required,omitempty"`
	Enum     []any    `json:"enum,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	// Refs names the upstream branches declaring this key, in read order.
	Refs []string           `json:"refs,omitempty"`
	Keys map[string]*Schema `json:"keys,omitempty"`
	Any  *Schema            `json:"any,omitempty"`
	Item *Schema            `json:"item,omitempty"`
}

// SeedOnly reports whether this key is declared only on a pre-release branch.
func (s *Schema) SeedOnly() bool {
	if s == nil || len(s.Refs) == 0 {
		return false
	}
	return !slices.Contains(s.Refs, ReleaseRef())
}

// Declaration is the declared shape of one declaration type.
type Declaration struct {
	Title string             `json:"title,omitempty"`
	Kind  string             `json:"kind"`
	Keys  map[string]*Schema `json:"keys"`
	Any   *Schema            `json:"any,omitempty"`
	Refs  []string           `json:"refs,omitempty"`
}

// SeedOnly reports whether this declaration type is declared only on a pre-release branch.
func (d *Declaration) SeedOnly() bool {
	if d == nil || len(d.Refs) == 0 {
		return false
	}
	return !slices.Contains(d.Refs, ReleaseRef())
}

// table is the generated declaration table. It is unexported because nothing hands a caller the
// table itself: the package functions expose what a diagnostic needs, and keeping the shape private
// leaves the generator free to change it.
type table struct {
	Source  string            `json:"source"`
	Commits map[string]string `json:"commits"`
	// Refs lists the upstream branches unioned, in read order: the release branch first, the most
	// forward-looking last.
	Refs         []string                `json:"refs"`
	Release      string                  `json:"release,omitempty"`
	Declarations map[string]*Declaration `json:"declarations"`
	// StatusItems is the vocabulary com.apple.configuration.management.status-subscriptions accepts.
	StatusItems []string `json:"statusItems"`
}

// load decodes the embedded table once. A table that fails to decode yields an empty one rather
// than a panic, but the degradation is loud rather than quiet: with no declaration types in the
// table every declaration reports as UnknownDeclarationType. Only the status-item check goes quiet,
// since it has no vocabulary to compare against. The package's own tests fail on a table that
// decodes to nothing.
var load = sync.OnceValue(func() *table {
	decoded := &table{}
	if err := json.Unmarshal(embeddedTable, decoded); err != nil {
		return &table{Declarations: map[string]*Declaration{}}
	}
	if decoded.Declarations == nil {
		decoded.Declarations = map[string]*Declaration{}
	}
	return decoded
})

// ReleaseRef returns the branch the table treats as released, which is the first one read.
func ReleaseRef() string {
	refs := load().Refs
	if len(refs) == 0 {
		return ""
	}
	return refs[0]
}

// Refs returns the upstream branches the table unions, in read order.
func Refs() []string { return slices.Clone(load().Refs) }

// Provenance returns the commit of the release branch and the upstream release subject, for
// diagnostics that need to say how current the schemas are. The two can name different branches:
// the subject is the head commit's of the most forward-looking branch unioned, so while a seed
// branch is open it is the seed's ("Seed8") rather than release's. ProvenanceSummary names each
// branch with its own commit.
func Provenance() (commit, release string) {
	loaded := load()
	return loaded.Commits[ReleaseRef()], loaded.Release
}

// ProvenanceSummary renders the branches and commits the table was built from, e.g.
// "release@67045e2fa06f + seed_OS_27_0@b0180185a5e4". Preferred over Provenance in a diagnostic:
// Apple's commit subjects are terse internal labels ("Seed8"), which read as a typo when dropped
// into a sentence, whereas a branch and commit are things an operator can actually look up.
func ProvenanceSummary() string {
	loaded := load()
	parts := make([]string, 0, len(loaded.Refs))
	for _, ref := range loaded.Refs {
		commit := loaded.Commits[ref]
		if len(commit) > 12 {
			commit = commit[:12]
		}
		parts = append(parts, ref+"@"+commit)
	}
	return strings.Join(parts, " + ")
}

// DeclarationTypes returns every declaration type in the table, sorted.
func DeclarationTypes() []string {
	return slices.Sorted(maps.Keys(load().Declarations))
}

// StatusItems returns the subscribable status-item names, sorted.
func StatusItems() []string { return slices.Clone(load().StatusItems) }

// Lookup returns the declared shape of a declaration type. The match is case-sensitive, mirroring
// the service, which renders no form at all for a wrong-cased type.
//
// The returned value points into the package's decoded table, which every caller and every later
// Validate share, so it must not be modified. Unlike Refs and StatusItems it is not cloned, because
// Validate looks up every declaration it checks and a deep copy of a type's key tree would be paid
// on each call.
func Lookup(declarationType string) (*Declaration, bool) {
	declaration, ok := load().Declarations[declarationType]
	return declaration, ok
}

// The kinds a declaration type's reverse-domain prefix implies.
//
// kindConfiguration and kindAsset alias the SDK's generated enum. kindActivation and kindManagement
// are a gateway widening: blueprints.DeclarationKindValues() declares only the first two, while
// POST /blueprints/v1/blueprints on the EU gateway accepted `kind` values ACTIVATION and MANAGEMENT
// on 2026-09-10 under an environment-scoped integration and read both back stored verbatim. So the
// gap is spec-versus-gateway drift rather than a wire break, and this package must go on deriving
// all four — a declaration type under com.apple.activation. or com.apple.management. has no other
// kind it could carry.
//
// The rule for maintaining the widening: an entry is DELETED rather than edited once either half of
// its justification goes. When a spec ingest promotes these two into DeclarationKindValues(),
// replace the literal with the new constant and delete this paragraph — do not reword it. Nothing
// fails on the promotion, so check blueprints.DeclarationKindValues() when ingesting a blueprints
// build.
const (
	kindConfiguration = blueprints.DeclarationKindConfiguration
	kindAsset         = blueprints.DeclarationKindAsset
	kindActivation    = "ACTIVATION"
	kindManagement    = "MANAGEMENT"
)

// KindForType returns the kind the service expects alongside a declaration type, derived from its
// reverse-domain prefix, or "" when the prefix is not one Apple defines.
func KindForType(declarationType string) string {
	switch {
	case strings.HasPrefix(declarationType, "com.apple.configuration."):
		return kindConfiguration
	case strings.HasPrefix(declarationType, "com.apple.asset."):
		return kindAsset
	case strings.HasPrefix(declarationType, "com.apple.activation."):
		return kindActivation
	case strings.HasPrefix(declarationType, "com.apple.management."):
		return kindManagement
	default:
		return ""
	}
}

// statusSubscriptionsType is the one declaration whose values are checked against the status-item
// vocabulary rather than against a schema.
const statusSubscriptionsType = "com.apple.configuration.management.status-subscriptions"
