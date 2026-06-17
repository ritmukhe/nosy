package schemapack

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/openconfig/goyang/pkg/yang"
)

// Compile parses every .yang module under yangDir and builds a schema pack:
// a flat index from canonical data path (no keys or predicates) to node type,
// description, and list keys.
//
// MULTI-VENDOR: compilation is NOS-agnostic. The nos/version are recorded in
// metadata but never influence parsing — pointing this at EOS or JunOS YANG
// produces a valid pack for that NOS with no code change.
//
// Resolution errors are common and benign in a large multi-vendor YANG tree
// (cross-module augments, optional deviations), so Process errors are not
// fatal: whatever resolves cleanly is indexed. A compile only fails if no
// files are found or nothing at all could be indexed.
func Compile(yangDir, nos, version string) (*SchemaPack, error) {
	files, dirs, err := collectYANG(yangDir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .yang files found under %s", yangDir)
	}

	ms := yang.NewModules()
	// Every directory holding YANG is a search path so imports/includes
	// resolve regardless of which file references them.
	for _, d := range dirs {
		ms.AddPath(d)
	}
	for _, f := range files {
		// A single unreadable/unparseable file must not abort the whole tree;
		// it may also be reachable as an import via the search path above.
		_ = ms.Read(f)
	}
	ms.Process()

	index := make(map[string]PathEntry)
	for _, entry := range moduleEntries(ms) {
		for _, child := range sortedChildren(entry) {
			indexEntry(child, "", index)
		}
	}
	if len(index) == 0 {
		return nil, fmt.Errorf("no schema paths indexed from %s (parsed %d files)", yangDir, len(files))
	}

	return &SchemaPack{
		Metadata: Metadata{
			NOS:     nos,
			Version: version,
			Built:   time.Now().UTC().Format(time.RFC3339),
		},
		PathIndex: index,
	}, nil
}

// collectYANG returns all .yang files under root (sorted) and the unique set of
// directories containing them (sorted), for use as module search paths.
func collectYANG(root string) (files, dirs []string, err error) {
	dirSet := make(map[string]bool)
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.ToLower(filepath.Ext(path)) != ".yang" {
			return nil
		}
		files = append(files, path)
		dirSet[filepath.Dir(path)] = true
		return nil
	})
	if walkErr != nil {
		return nil, nil, fmt.Errorf("walking yang dir %s: %w", root, walkErr)
	}
	for d := range dirSet {
		dirs = append(dirs, d)
	}
	sort.Strings(files)
	sort.Strings(dirs)
	return files, dirs, nil
}

// moduleEntries returns the resolved entry tree for each distinct module,
// deduplicated by name and sorted for deterministic indexing.
func moduleEntries(ms *yang.Modules) []*yang.Entry {
	seen := make(map[string]bool)
	var names []string
	mods := make(map[string]*yang.Module)
	for _, m := range ms.Modules {
		if !seen[m.Name] {
			seen[m.Name] = true
			names = append(names, m.Name)
			mods[m.Name] = m
		}
	}
	sort.Strings(names)

	entries := make([]*yang.Entry, 0, len(names))
	for _, n := range names {
		entries = append(entries, yang.ToEntry(mods[n]))
	}
	return entries
}

// indexEntry walks one node, recording its canonical path and recursing into
// children. choice/case nodes carry no data-path segment, so they are
// traversed transparently; RPCs, notifications, and anydata are skipped.
func indexEntry(e *yang.Entry, prefix string, index map[string]PathEntry) {
	if e == nil || e.RPC != nil {
		return
	}
	if e.IsChoice() || e.IsCase() {
		for _, c := range sortedChildren(e) {
			indexEntry(c, prefix, index)
		}
		return
	}

	path := prefix + "/" + stripModulePrefix(e.Name)

	switch {
	case e.IsLeaf():
		index[path] = PathEntry{Type: "leaf", Description: e.Description}
		return
	case e.IsLeafList():
		index[path] = PathEntry{Type: "leaf-list", Description: e.Description}
		return
	case e.IsList():
		index[path] = PathEntry{Type: "list", Description: e.Description, Keys: strings.Fields(e.Key)}
	case e.IsContainer():
		index[path] = PathEntry{Type: "container", Description: e.Description}
	default:
		return // notification / anydata — out of scope for v0.1 state queries
	}

	for _, c := range sortedChildren(e) {
		indexEntry(c, path, index)
	}
}

// sortedChildren returns an entry's data children in name order, for
// deterministic traversal.
func sortedChildren(e *yang.Entry) []*yang.Entry {
	if len(e.Dir) == 0 {
		return nil
	}
	names := make([]string, 0, len(e.Dir))
	for n := range e.Dir {
		names = append(names, n)
	}
	sort.Strings(names)

	children := make([]*yang.Entry, 0, len(names))
	for _, n := range names {
		children = append(children, e.Dir[n])
	}
	return children
}

// stripModulePrefix drops a leading "module-name:" qualifier so path segments
// are bare node names, matching the canonical form ValidatePath expects.
func stripModulePrefix(name string) string {
	if i := strings.LastIndex(name, ":"); i >= 0 {
		return name[i+1:]
	}
	return name
}
