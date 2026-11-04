// Package certified holds the pure rules of the certified NQE library:
// what makes an entry well-formed, and what "certified" requires.
package certified

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/forward-mcp/internal/domain/entities"
)

var (
	idRe      = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	semverRe  = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	queryIDRe = regexp.MustCompile(`^FQ_[0-9a-f]{40}$`)
)

// ValidateVersion checks a library VERSION string.
func ValidateVersion(v string) error {
	if !semverRe.MatchString(v) {
		return fmt.Errorf("library version %q is not MAJOR.MINOR.PATCH", v)
	}
	return nil
}

// ValidateEntry checks one entry for structural validity. Every problem is
// reported (joined) so an author fixes a file in one pass. Field names in
// messages match the JSON keys.
func ValidateEntry(q entities.CertifiedQuery) error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if !idRe.MatchString(q.ID) {
		add("id %q must be lower-case words separated by single dashes", q.ID)
	}
	if q.Version < 1 {
		add("version must be >= 1")
	}
	if q.Title == "" {
		add("title is required")
	}
	if q.Intent == "" {
		add("intent is required")
	}
	if q.Owner == "" {
		add("owner is required")
	}
	if q.Fixture == "" {
		add("fixture is required")
	}
	switch {
	case q.Source.QueryID == "" && q.Source.Path == "":
		add("source: one of nqe_query_id or nqe_path is required")
	case q.Source.QueryID != "" && q.Source.Path != "":
		add("source: set only one of nqe_query_id or nqe_path")
	case q.Source.QueryID != "" && !queryIDRe.MatchString(q.Source.QueryID):
		add("source.nqe_query_id %q must look like FQ_<40 hex>", q.Source.QueryID)
	}
	if r := q.Source.Repository; r != "" && r != "org" && r != "fwd" {
		add("source.repository %q must be \"org\" or \"fwd\"", r)
	}
	if len(q.OutputSchema) == 0 {
		add("output_schema is required")
	} else if !json.Valid(q.OutputSchema) {
		add("output_schema is not valid JSON")
	} else {
		var obj map[string]any
		if err := json.Unmarshal(q.OutputSchema, &obj); err != nil {
			add("output_schema must be a JSON object")
		} else if t, _ := obj["type"].(string); t != "object" {
			add("output_schema.type must be \"object\" (one row)")
		}
	}
	seen := map[string]bool{}
	for i, p := range q.Parameters {
		if p.Name == "" {
			add("parameters[%d].name is required", i)
			continue
		}
		if seen[p.Name] {
			add("parameters[%d].name %q is duplicated", i, p.Name)
		}
		seen[p.Name] = true
		if !p.Type.IsValid() {
			add("parameters[%d] (%s): type %q is not one of string|int|bool|ip|cidr|device|list", i, p.Name, p.Type)
		}
		if p.Required && len(p.Default) > 0 {
			add("parameters[%d] (%s): a required parameter cannot have a default", i, p.Name)
		}
		if len(p.Default) > 0 && !json.Valid(p.Default) {
			add("parameters[%d] (%s): default is not valid JSON", i, p.Name)
		}
	}
	return errors.Join(errs...)
}

// ValidateLibrary checks cross-entry rules: unique ids and a valid version.
func ValidateLibrary(l *entities.CertifiedLibrary) error {
	var errs []error
	if err := ValidateVersion(l.Version); err != nil {
		errs = append(errs, err)
	}
	ids := map[string]bool{}
	for _, q := range l.Queries {
		if ids[q.ID] {
			errs = append(errs, fmt.Errorf("duplicate id %q", q.ID))
		}
		ids[q.ID] = true
	}
	return errors.Join(errs...)
}

// MayBeCertified reports whether an entry meets the bar for certified:true.
// The loader refuses a library that marks an entry certified without this.
func MayBeCertified(q entities.CertifiedQuery) bool {
	return q.Owner != "" && q.Fixture != "" && len(q.OutputSchema) > 0 && ValidateEntry(q) == nil
}
