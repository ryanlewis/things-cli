package cache

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	uuids := []string{"a", "b", "c"}
	if err := WriteLastList(uuids); err != nil {
		t.Fatalf("WriteLastList: %v", err)
	}
	got, err := ReadLastList()
	if err != nil {
		t.Fatalf("ReadLastList: %v", err)
	}
	if !reflect.DeepEqual(got, uuids) {
		t.Fatalf("got %v, want %v", got, uuids)
	}
}

func TestWriteOverwrites(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := WriteLastList([]string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteLastList([]string{"x"}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLastList()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"x"}) {
		t.Fatalf("got %v, want [x]", got)
	}
}

func TestReadMissingReturnsError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := ReadLastList()
	if !os.IsNotExist(err) {
		t.Fatalf("expected not-exist error, got %v", err)
	}
}

func TestReadEmptyFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	d := dir()
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "last-list"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLastList()
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestWriteEmptyList(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := WriteLastList(nil); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLastList()
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}
