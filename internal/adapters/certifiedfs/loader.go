// Package certifiedfs loads a certified NQE library from a directory tree:
//
//	<root>/VERSION            semver of the library
//	<root>/queries/<id>.json  one entities.CertifiedQuery per file
//	<root>/fixtures/<...>.json recorded NQERunResult referenced by entries
//
// It implements ports.CertifiedLibraryLoader and ports.FixtureReader over
// an fs.FS so the in-repo spec/certified tree, an embedded copy, or a test
// directory all load the same way.
package certifiedfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/domain/services/certified"
	"github.com/forward-mcp/internal/ports"
)

const (
	versionFile = "VERSION"
	queriesDir  = "queries"
	fixturesDir = "fixtures"
)

// Loader reads a library from fsys.
type Loader struct {
	fsys fs.FS
}

var (
	_ ports.CertifiedLibraryLoader = (*Loader)(nil)
	_ ports.FixtureReader          = (*Loader)(nil)
)

// New returns a loader over fsys, whose root is the library root.
func New(fsys fs.FS) *Loader { return &Loader{fsys: fsys} }

// Load implements ports.CertifiedLibraryLoader. It fails with every problem
// found (file and field named), never with a partial library.
func (l *Loader) Load(ctx context.Context) (*entities.CertifiedLibrary, error) {
	vb, err := fs.ReadFile(l.fsys, versionFile)
	if err != nil {
		return nil, fmt.Errorf("certified library: read %s: %w", versionFile, err)
	}
	lib := &entities.CertifiedLibrary{Version: strings.TrimSpace(string(vb))}

	names, err := fs.ReadDir(l.fsys, queriesDir)
	if err != nil {
		return nil, fmt.Errorf("certified library: read %s/: %w", queriesDir, err)
	}
	sort.Slice(names, func(i, j int) bool { return names[i].Name() < names[j].Name() })

	var errs []error
	for _, de := range names {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		file := path.Join(queriesDir, de.Name())
		q, err := l.readEntry(file)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if want := strings.TrimSuffix(de.Name(), ".json"); q.ID != want {
			errs = append(errs, fmt.Errorf("%s: id %q must equal the file name %q", file, q.ID, want))
		}
		if err := certified.ValidateEntry(q); err != nil {
			errs = append(errs, prefixErr(file, err))
		}
		if q.Fixture != "" {
			if _, err := fs.Stat(l.fsys, l.fixturePath(q)); err != nil {
				errs = append(errs, fmt.Errorf("%s: fixture %q not found under %s/", file, q.Fixture, fixturesDir))
			} else if _, err := l.ReadFixture(ctx, q); err != nil {
				errs = append(errs, fmt.Errorf("%s: fixture: %w", file, err))
			}
		}
		if q.Certified && !certified.MayBeCertified(q) {
			errs = append(errs, fmt.Errorf("%s: certified:true but entry does not meet the bar (owner, fixture, output_schema, valid)", file))
		}
		lib.Queries = append(lib.Queries, q)
	}
	if err := certified.ValidateLibrary(lib); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("certified library: %w", errors.Join(errs...))
	}
	return lib, nil
}

func (l *Loader) readEntry(file string) (entities.CertifiedQuery, error) {
	b, err := fs.ReadFile(l.fsys, file)
	if err != nil {
		return entities.CertifiedQuery{}, fmt.Errorf("%s: %w", file, err)
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var q entities.CertifiedQuery
	if err := dec.Decode(&q); err != nil {
		return entities.CertifiedQuery{}, fmt.Errorf("%s: %w", file, err)
	}
	return q, nil
}

func (l *Loader) fixturePath(q entities.CertifiedQuery) string {
	// Fixture paths are relative to the fixtures dir and may not escape it.
	return path.Join(fixturesDir, path.Clean("/"+q.Fixture))
}

// ReadFixture implements ports.FixtureReader.
func (l *Loader) ReadFixture(_ context.Context, q entities.CertifiedQuery) (*entities.NQERunResult, error) {
	b, err := fs.ReadFile(l.fsys, l.fixturePath(q))
	if err != nil {
		return nil, err
	}
	var r entities.NQERunResult
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: not an NQERunResult: %w", q.Fixture, err)
	}
	if r.SnapshotID == "" {
		return nil, fmt.Errorf("%s: snapshotId is required in a fixture", q.Fixture)
	}
	return &r, nil
}

func prefixErr(prefix string, err error) error {
	// errors.Join renders one problem per line; prefix each with the file.
	lines := strings.Split(err.Error(), "\n")
	for i := range lines {
		lines[i] = prefix + ": " + lines[i]
	}
	return errors.New(strings.Join(lines, "\n"))
}
