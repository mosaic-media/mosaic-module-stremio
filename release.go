package stremio

import "strings"

// Candidate labelling. An addon's descriptive fields are free text with several
// facts packed into them, so naming a release is its own small problem — and it
// matters, because the label is what a person choosing between two candidates by
// hand actually reads.

// maxCandidates bounds how many releases one item keeps.
//
// An aggregator can return well over a hundred for one film, and the tail is
// duplicates and unusable releases. The source has already ranked them, so the
// head is the part worth keeping. Stated here rather than applied silently: a
// cap nobody can see reads as "we stored everything" when it did not
// (ADR 0049).
const maxCandidates = 40

// releaseLabel is the human name for a candidate — the filename where an addon
// gives one, otherwise the first line of its descriptive text.
func releaseLabel(s Stream) string {
	if s.BehaviorHints.Filename != "" {
		return s.BehaviorHints.Filename
	}
	for _, candidate := range []string{s.Title, s.Name, s.Description} {
		if line := strings.TrimSpace(firstLine(candidate)); line != "" {
			return line
		}
	}
	return ""
}

// firstLine takes the first line of an addon's descriptive text. Addons pack
// several facts into one field separated by newlines, and only the first is the
// release name.
func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
