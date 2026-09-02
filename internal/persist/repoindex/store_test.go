package repoindex

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	sqlitestate "github.com/fwtllh-png/QCode/internal/persist/state/sqlite"
)

func TestApplyReplacesTheSymbolsOfAPath(t *testing.T) {
	store := openStore(t)
	ctx := t.Context()
	apply(t, store, Record{
		File: File{Path: "api.go", Language: "go", Size: 12, Digest: "sha256:one"},
		Symbols: []Symbol{
			{Name: "Serve", Kind: "function", Line: 3, Exported: true},
			{Name: "handle", Kind: "function", Line: 9},
		},
	})

	files, err := store.Files(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files["api.go"].Digest != "sha256:one" || files["api.go"].SymbolCount != 2 {
		t.Fatalf("files = %#v", files)
	}

	// Re-indexing the same path replaces its declarations rather than adding to
	// them, so a deleted function stops being findable.
	apply(t, store, Record{
		File:    File{Path: "api.go", Language: "go", Size: 8, Digest: "sha256:two"},
		Symbols: []Symbol{{Name: "Serve", Kind: "function", Line: 4, Exported: true}},
	})
	symbols, err := store.Symbols(ctx, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) != 1 || symbols[0].Name != "Serve" || symbols[0].Line != 4 {
		t.Fatalf("symbols = %#v", symbols)
	}
	files, err = store.Files(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if files["api.go"].Digest != "sha256:two" || files["api.go"].SymbolCount != 1 {
		t.Fatalf("file = %#v", files["api.go"])
	}
}

func TestSymbolsFilterAndOrderDeterministically(t *testing.T) {
	store := openStore(t)
	ctx := t.Context()
	apply(t, store,
		Record{
			File: File{Path: "b.go", Language: "go", Digest: "sha256:b"},
			Symbols: []Symbol{
				{Name: "Handler", Kind: "type", Line: 5, Exported: true},
				{Name: "ServeHandler", Kind: "function", Line: 20, Exported: true},
			},
		},
		Record{
			File: File{Path: "a.py", Language: "python", Digest: "sha256:a"},
			Symbols: []Symbol{
				{Name: "handler", Kind: "function", Line: 2},
				{Name: "unrelated", Kind: "function", Line: 30},
			},
		},
	)

	substring, err := store.Symbols(ctx, Query{Name: "handler"})
	if err != nil {
		t.Fatal(err)
	}
	// Shortest name first, then name, then path: the closest matches lead and the
	// order does not depend on insertion.
	if names := names(substring); !equal(names, []string{"Handler", "handler", "ServeHandler"}) {
		t.Fatalf("substring names = %#v", names)
	}

	exact, err := store.Symbols(ctx, Query{Name: "HANDLER", Exact: true})
	if err != nil {
		t.Fatal(err)
	}
	if names := names(exact); !equal(names, []string{"Handler", "handler"}) {
		t.Fatalf("exact names = %#v", names)
	}

	kinds, err := store.Symbols(ctx, Query{Name: "handler", Kinds: []string{"type"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(kinds) != 1 || kinds[0].Name != "Handler" || kinds[0].Path != "b.go" {
		t.Fatalf("kind filtered = %#v", kinds)
	}

	scoped, err := store.Symbols(ctx, Query{Paths: []string{"a.py"}})
	if err != nil {
		t.Fatal(err)
	}
	if names := names(scoped); !equal(names, []string{"handler", "unrelated"}) {
		t.Fatalf("path scoped = %#v", names)
	}

	limited, err := store.Symbols(ctx, Query{Name: "handler", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].Name != "Handler" {
		t.Fatalf("limited = %#v", limited)
	}
}

func TestSymbolsTreatQueryWildcardsAsLiterals(t *testing.T) {
	store := openStore(t)
	apply(t, store, Record{
		File: File{Path: "a.go", Digest: "sha256:a"},
		Symbols: []Symbol{
			{Name: "read_file", Kind: "function", Line: 1},
			{Name: "readXfile", Kind: "function", Line: 2},
			{Name: "percent%name", Kind: "function", Line: 3},
		},
	})
	underscore, err := store.Symbols(t.Context(), Query{Name: "read_file"})
	if err != nil {
		t.Fatal(err)
	}
	if names := names(underscore); !equal(names, []string{"read_file"}) {
		t.Fatalf("underscore query = %#v", names)
	}
	percent, err := store.Symbols(t.Context(), Query{Name: "%"})
	if err != nil {
		t.Fatal(err)
	}
	if names := names(percent); !equal(names, []string{"percent%name"}) {
		t.Fatalf("percent query = %#v", names)
	}
}

func TestDeleteAndResetRemoveSymbolsWithTheirFiles(t *testing.T) {
	store := openStore(t)
	ctx := t.Context()
	apply(t, store,
		Record{
			File:    File{Path: "gone.go", Digest: "sha256:gone"},
			Symbols: []Symbol{{Name: "Gone", Kind: "function", Line: 1}},
		},
		Record{
			File:    File{Path: "kept.go", Digest: "sha256:kept"},
			Symbols: []Symbol{{Name: "Kept", Kind: "function", Line: 1}},
		},
	)

	if err := store.Delete(ctx, []string{"gone.go"}); err != nil {
		t.Fatal(err)
	}
	symbols, err := store.Symbols(ctx, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if names := names(symbols); !equal(names, []string{"Kept"}) {
		t.Fatalf("symbols after delete = %#v", names)
	}

	if err := store.SetMeta(ctx, Meta{IndexerVersion: 1, FileCount: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	files, err := store.Files(ctx)
	if err != nil {
		t.Fatal(err)
	}
	symbols, err = store.Symbols(ctx, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Meta(ctx); err != nil || found {
		t.Fatalf("meta found = %v, err = %v", found, err)
	}
	if len(files) != 0 || len(symbols) != 0 {
		t.Fatalf("files = %#v, symbols = %#v", files, symbols)
	}
}

func TestMetaRoundTripsAndReportsAbsence(t *testing.T) {
	store := openStore(t)
	ctx := t.Context()
	if _, found, err := store.Meta(ctx); err != nil || found {
		t.Fatalf("meta found = %v before any refresh, err = %v", found, err)
	}
	refreshed := time.Now().UTC().Truncate(time.Millisecond)
	want := Meta{
		IndexerVersion: 3, Source: "git", FileCount: 7,
		SymbolCount: 21, Truncated: true, RefreshedAt: refreshed,
	}
	if err := store.SetMeta(ctx, want); err != nil {
		t.Fatal(err)
	}
	// A second write updates the row in place rather than failing on the key.
	want.FileCount = 8
	if err := store.SetMeta(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.Meta(ctx)
	if err != nil || !found {
		t.Fatalf("found = %v, err = %v", found, err)
	}
	if !got.RefreshedAt.Equal(refreshed) {
		t.Fatalf("refreshed at = %s, want %s", got.RefreshedAt, refreshed)
	}
	got.RefreshedAt = want.RefreshedAt
	if got != want {
		t.Fatalf("meta = %+v, want %+v", got, want)
	}
}

func TestStoresOfDifferentRootsStaySeparate(t *testing.T) {
	database := openDatabase(t)
	first, err := NewStore(database, "/first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewStore(database, "/second")
	if err != nil {
		t.Fatal(err)
	}
	apply(t, first, Record{
		File:    File{Path: "a.go", Digest: "sha256:a"},
		Symbols: []Symbol{{Name: "First", Kind: "function", Line: 1}},
	})
	apply(t, second, Record{
		File:    File{Path: "a.go", Digest: "sha256:b"},
		Symbols: []Symbol{{Name: "Second", Kind: "function", Line: 1}},
	})

	if err := second.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	symbols, err := first.Symbols(t.Context(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if names := names(symbols); !equal(names, []string{"First"}) {
		t.Fatalf("first root symbols = %#v", names)
	}
}

func TestNewStoreRequiresADatabaseAndRoot(t *testing.T) {
	if _, err := NewStore(nil, "/root"); err == nil {
		t.Fatal("nil database accepted")
	}
	if _, err := NewStore(openDatabase(t), "  "); err == nil {
		t.Fatal("blank root accepted")
	}
}

func openStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(openDatabase(t), "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func openDatabase(t *testing.T) *sql.DB {
	t.Helper()
	state, err := sqlitestate.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	return state.DB()
}

func apply(t *testing.T, store *Store, records ...Record) {
	t.Helper()
	if err := store.Apply(t.Context(), records); err != nil {
		t.Fatal(err)
	}
}

func names(symbols []Symbol) []string {
	result := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		result = append(result, symbol.Name)
	}
	return result
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
