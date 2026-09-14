// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// ---------------------------------------------------------------------------
// String utilities
// ---------------------------------------------------------------------------

// Regex for acronym fixup: matches "Id", "Url" etc. only when followed by
// uppercase, end-of-string, or a non-letter — so "Identifier" is not touched.
var acronymFixups = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`Ip([AV])`), "IP$1"},
	{regexp.MustCompile(`Uuid($|[A-Z])`), "UUID$1"},
	{regexp.MustCompile(`Udid($|[A-Z])`), "UDID$1"},
	{regexp.MustCompile(`Url($|[A-Z])`), "URL$1"},
	{regexp.MustCompile(`Odv($|[A-Z])`), "ODV$1"},
	{regexp.MustCompile(`Mdm($|[A-Z])`), "MDM$1"},
	{regexp.MustCompile(`Id($|[A-Z])`), "ID$1"},
}

func exportedGoName(name string) string {
	// Exact matches for single-word properties.
	exact := map[string]string{
		"id": "ID", "ids": "IDs", "url": "URL", "urls": "URLs",
		"udid": "UDID", "ip": "IP", "os": "OS", "odv": "ODV",
		"mdm": "MDM", "uuid": "UUID", "uri": "URI", "href": "Href",
		"macAddress": "MacAddress",
	}
	if v, ok := exact[name]; ok {
		return v
	}

	// camelCase → PascalCase
	var b strings.Builder
	upper := true
	for _, r := range name {
		if r == '_' || r == '-' {
			upper = true
			continue
		}
		if upper {
			b.WriteRune(unicode.ToUpper(r))
			upper = false
		} else {
			b.WriteRune(r)
		}
	}
	s := b.String()

	// Fix acronyms at word boundaries.
	for _, fix := range acronymFixups {
		s = fix.re.ReplaceAllString(s, fix.repl)
	}
	return s
}

func toLowerCamelCase(s string) string {
	// kebab-case → lowerCamel: "panel-id" → "panelId", "some-thing" → "someThing".
	// Required because Go identifiers cannot contain hyphens; Jamf Pro spec
	// uses kebab-case for a handful of path-param names.
	if strings.Contains(s, "-") {
		parts := strings.Split(s, "-")
		var out strings.Builder
		out.WriteString(parts[0])
		for _, p := range parts[1:] {
			if p == "" {
				continue
			}
			out.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
		s = out.String()
	}
	if s == "id" {
		return "id"
	}
	if strings.HasSuffix(s, "Id") {
		return s[:len(s)-2] + "ID"
	}
	return s
}

func cleanComment(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	// A paragraph ending in a colon introduces something that follows — a list,
	// or (since GitOps build v1369) a markdown table. Appending a full stop
	// there yields "Valid IPs per datacenter:." Any other terminal punctuation
	// is already an ending, so only a bare word needs the period.
	if !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, ":") &&
		!strings.HasSuffix(s, "!") && !strings.HasSuffix(s, "?") {
		s += "."
	}
	return s
}

// specHTMLTagRe matches the inline HTML fragments Jamf embeds in spec prose
// (<br/>, </br>, <p>, <li>, …). Godoc has no HTML, so the tags are dropped.
// The alternation is an explicit tag allowlist rather than a generic <...>
// pattern because some descriptions use angle brackets as placeholder syntax
// ("sort in the format <field_name>:asc") and those must survive.
var specHTMLTagRe = regexp.MustCompile(`(?i)</?(br|p|div|span|ul|ol|li|b|i|em|strong|code|pre|a|table|tr|td|th)\b[^>]*>`)

// specHTMLEntities decodes the handful of entities Jamf's spec prose uses.
var specHTMLEntities = strings.NewReplacer(
	"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ",
)

// stripSpecHTML renders spec prose as plain text for godoc: known tags become
// a space (so "a</br>b" doesn't fuse into "ab") and entities are decoded.
// Whitespace normalisation is left to cleanComment.
func stripSpecHTML(s string) string {
	return specHTMLEntities.Replace(specHTMLTagRe.ReplaceAllString(s, " "))
}

// descriptionParagraphRe splits spec prose on blank lines so each paragraph
// is wrapped as its own run of godoc lines.
var descriptionParagraphRe = regexp.MustCompile(`\n[ \t]*\n`)

// docParagraphs renders spec prose as wrapped godoc lines: HTML stripped,
// entities decoded, and each blank-line-separated paragraph wrapped
// independently. Wrapping the whole description in one pass would fuse the
// last sentence of a paragraph into the first of the next — Jamf ends several
// of its field lists on a bare identifier with no terminating period, so the
// joint reads as one broken sentence.
//
// Returns nil for empty or whitespace-only input, letting callers treat "the
// spec says nothing" as a distinct case from "the spec says something short".
func docParagraphs(text string, width int) []string {
	var out []string
	for _, para := range descriptionParagraphRe.Split(stripSpecHTML(text), -1) {
		if strings.TrimSpace(para) == "" {
			continue
		}
		if rows := markdownTableRows(para); rows != nil {
			out = append(out, rows...)
			continue
		}
		out = append(out, wrapCommentText(cleanComment(para), width)...)
	}
	return out
}

// markdownTableRows emits a pipe-delimited markdown table one row per comment
// line, or nil when para is not one.
//
// Reflowing a table destroys it: wrapCommentText collapses the rows into a
// single run of `| a | b | |---|---| | c | d |`, which is neither readable nor
// greppable for the one row a caller wants. Two specs ship tables in a
// description — blueprints (Apple payload field references) and, since GitOps
// build v1369, ztna, whose availabilityZones tables carry the per-datacenter
// source IPs a peer firewall has to allow. Those IPs are operational
// configuration, so a caller has to be able to read the row for their region.
//
// Rows are emitted verbatim and may exceed the wrap width. That is deliberate —
// a table is only legible with its columns intact, and no wrap point preserves
// them.
func markdownTableRows(para string) []string {
	var rows []string
	for line := range strings.SplitSeq(para, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			return nil // any non-row line means this is prose, not a table
		}
		rows = append(rows, line)
	}
	// A lone row is prose that happens to start with a pipe; a table needs at
	// least a header and its separator.
	if len(rows) < 2 {
		return nil
	}
	return rows
}

// wrapCommentText greedily wraps s to width columns without splitting a word.
// Needed for parameter documentation: a single Jamf description can run to
// thousands of characters (the Pro list endpoints inline every sortable field
// name), and cleanComment collapses it to one unreadable line.
func wrapCommentText(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var (
		lines []string
		cur   strings.Builder
	)
	for _, w := range words {
		switch {
		case cur.Len() == 0:
			cur.WriteString(w)
		case cur.Len()+1+len(w) <= width:
			cur.WriteByte(' ')
			cur.WriteString(w)
		default:
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(w)
		}
	}
	return append(lines, cur.String())
}

// enumIdentSplitRe splits an enum wire value into word segments. Jamf's enum
// values are SCREAMING_SNAKE in the Pro/Platform families, kebab in a few
// (mdm-byod), and occasionally dotted or spaced in Classic.
var enumIdentSplitRe = regexp.MustCompile(`[^A-Za-z0-9]+`)

// enumConstIdent converts an enum wire value into the identifier fragment
// appended to its type name: "APNS_CERT_REVOKED" → "ApnsCertRevoked",
// "mdm-byod" → "MDMByod". Segments are title-cased and the shared acronym
// fixups then run, so ID/URL/MDM land in their canonical spelling.
//
// A leading digit is fine — the fragment is only ever a suffix on the type
// name, so "10.15" yields "1015" and the emitted constant is still valid.
// Returns "" when the value holds no alphanumerics at all, leaving the caller
// to skip it and say so rather than emit something that will not compile.
func enumConstIdent(value string) string {
	var b strings.Builder
	for _, seg := range enumIdentSplitRe.Split(value, -1) {
		if seg == "" {
			continue
		}
		// An all-caps segment is a wire-format artefact, not an acronym the
		// spec is asserting: lowercase the tail so SCREAMING_SNAKE reads as
		// Go words. Mixed-case segments are left alone beyond the initial.
		if seg == strings.ToUpper(seg) {
			seg = strings.ToUpper(seg[:1]) + strings.ToLower(seg[1:])
		} else {
			seg = strings.ToUpper(seg[:1]) + seg[1:]
		}
		b.WriteString(seg)
	}
	s := b.String()
	if s == "" {
		return ""
	}
	for _, fix := range acronymFixups {
		s = fix.re.ReplaceAllString(s, fix.repl)
	}
	return s
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func camelToWords(s string) string {
	var words []string
	var cur strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			if cur.Len() > 0 {
				words = append(words, strings.ToLower(cur.String()))
				cur.Reset()
			}
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		words = append(words, strings.ToLower(cur.String()))
	}
	return strings.Join(words, " ")
}

func isScalar(goType string) bool {
	switch goType {
	case "string", "int", "int32", "int64", "float32", "float64", "bool":
		return true
	}
	return false
}

// coalesce returns val if non-empty, otherwise fallback.
func coalesce(val, fallback string) string {
	if val != "" {
		return val
	}
	return fallback
}

// coalesceInt returns val if non-zero, otherwise fallback.
func coalesceInt(val, fallback int) int {
	if val != 0 {
		return val
	}
	return fallback
}

// defaultMaxPageSize returns the page size ListAllPages requests per call when
// an operation doesn't set "maxPageSize" explicitly in config.json.
//
// Wire-verified 2026-08-10: every "totalCount"-style pagination operation in
// the pro package that was pushed past 2000 real rows (33 endpoints sampled —
// ListGroupsV1/V2, AccountsV1, EnrollmentAccessGroupV3, and 29 History
// endpoints spanning wildly different resource types) clamped at exactly
// 2000, silently, with zero exceptions. That is strong enough evidence to
// treat 2000 as the pro API gateway's pagination ceiling for this pagination
// style, rather than requiring per-operation verification before raising it.
//
// Scoped narrowly on purpose: only pro + totalCount. Two pro operations use a
// different pagination mechanism (ListUsersV1 is "hasNext", ListSiteObjectsV1
// is "rawArray") and were not part of the verified sample — both mechanisms
// compute continuation from the requested page size in a way that fails
// differently under a wrong cap (hasNext can still skip a chunk; rawArray can
// stop early and silently truncate), so they stay at the conservative
// default until independently verified. Every other package (devices,
// devicegroups, blueprints, ddmreport, compliancebenchmarks) is a distinct
// backend service — devices/v1 was probed in the same session and behaves
// completely differently (hard 400 rejection above page-size=1000, not a
// silent clamp), which is exactly why this default doesn't extend to them.
func defaultMaxPageSize(pkg, paginationStyle string) int {
	if pkg == "pro" && paginationStyle == "totalCount" {
		return 2000
	}
	return 100
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// orderedProps returns map keys in the order given by explicit, with any
// remaining keys appended alphabetically. If explicit is nil or empty it
// degenerates to sortedKeys. Prop keys that carry deprecation metadata
// (e.g. "field_name deprecated=10.48") are matched by their bare name.
func orderedProps[V any](m map[string]V, explicit []string) []string {
	if len(explicit) == 0 {
		return sortedKeys(m)
	}
	// Build a stripped-name → raw-key index so we can match explicit entries
	// against keys that may carry deprecation suffixes.
	stripped := make(map[string]string, len(m)) // stripped name → raw map key
	for k := range m {
		bare := k
		if i := strings.IndexAny(bare, " \t"); i >= 0 {
			bare = bare[:i]
		}
		stripped[bare] = k
	}
	seen := make(map[string]bool, len(m))
	result := make([]string, 0, len(m))
	for _, name := range explicit {
		raw, ok := stripped[name]
		if !ok {
			continue // name in explicit list but absent from map — skip
		}
		result = append(result, raw)
		seen[raw] = true
	}
	// Append remaining keys alphabetically.
	remaining := make([]string, 0, len(m)-len(seen))
	for k := range m {
		if !seen[k] {
			remaining = append(remaining, k)
		}
	}
	sort.Strings(remaining)
	return append(result, remaining...)
}

// toSnakeCase converts titles to snake_case filenames.
// Handles spaces ("Device Inventory API" → "device_inventory_api"),
// camelCase ("DDMReport" → "ddm_report"), and mixed input.
func toSnakeCase(s string) string {
	// Insert underscore before uppercase runs: "DDMReport" → "DDM_Report"
	s = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`).ReplaceAllString(s, "${1}_${2}")
	// Insert underscore at lower→upper boundary: "deviceAction" → "device_Action"
	s = regexp.MustCompile(`([a-z0-9])([A-Z])`).ReplaceAllString(s, "${1}_${2}")
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "_")
	return strings.Trim(s, "_")
}

func toSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

// testHeaderValue is the sentinel a generated stub sends for one header param
// and asserts came back. Derived from the wire name so a method taking two
// headers cannot pass by stamping one value twice.
func testHeaderValue(specName string) string {
	return "hdr-" + strings.ToLower(http.CanonicalHeaderKey(specName))
}

// previewSummaryPrefixRe matches the state prefix Jamf prepends to an
// operation summary when the endpoint is in preview — "Preview - ",
// "Preview – " (en dash), "Preview: " and the bare-colon form alike. GitOps
// v2192 introduced it on all twelve ai-governance summaries, and the summary
// is what becomes the method's godoc sentence, so the prefix has to come off
// or every method reads "ListPolicies preview - List active …".
var previewSummaryPrefixRe = regexp.MustCompile(`^\s*Preview\s*[-–—:]\s*`)

// stripPreviewPrefix removes that prefix, but only when the operation
// actually declares x-preview: true. Gating on the extension rather than on
// the text means a summary that legitimately begins with the word "Preview"
// — a hypothetical "Preview a report before sending it" — survives intact,
// and the strip cannot silently rewrite prose on an operation whose state the
// spec never claimed.
func stripPreviewPrefix(summary string, preview bool) string {
	if !preview {
		return summary
	}
	return previewSummaryPrefixRe.ReplaceAllString(summary, "")
}
