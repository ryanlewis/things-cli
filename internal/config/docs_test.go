package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docsPage is the configuration page on the docs site. It reproduces the
// `config init` template and the key table, both of which go stale silently
// unless something checks them.
const docsPage = "../../docs/_tabs/configuration.md"

func readDocsPage(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.FromSlash(docsPage))
	if err != nil {
		t.Fatalf("read %s: %v", docsPage, err)
	}
	return string(body)
}

// The page prints the template verbatim and tells the reader it is what
// `things config init` writes, so it has to actually be that.
func TestDocsPageQuotesTheTemplate(t *testing.T) {
	page := readDocsPage(t)
	tpl := strings.TrimRight(Template(), "\n")
	if !strings.Contains(page, tpl) {
		t.Errorf("%s does not contain the current `config init` template verbatim — "+
			"regenerate the ```toml block from `things config init`", docsPage)
	}
}

// A key added to the registry without a row in the tables readers actually
// consult is a key nobody can find out about.
func TestDocsTablesDocumentEveryKey(t *testing.T) {
	for _, path := range []string{docsPage, "../../README.md"} {
		body, err := os.ReadFile(filepath.FromSlash(path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, k := range Keys {
			if !strings.Contains(string(body), "| `"+k.Name+"` |") {
				t.Errorf("%s has no key-table row for %q", path, k.Name)
			}
		}
	}
}

// The error example on the page lists the valid keys, which is the one place
// the key set is spelled out in prose rather than a table.
func TestDocsPageListsValidKeysInTheErrorExample(t *testing.T) {
	page := readDocsPage(t)
	want := "valid keys: " + strings.Join(KeyNames(), ", ")
	if !strings.Contains(page, want) {
		t.Errorf("%s does not show the current key list (%q) in its unknown-key example", docsPage, want)
	}
}
