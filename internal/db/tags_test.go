package db

import "testing"

func TestListTagsOrdered(t *testing.T) {
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMTag (uuid, title, shortcut, parent, "index") VALUES
		('t1', 'urgent', 'u',  '',     2),
		('t2', 'home',   '',   '',     1),
		('t3', 'child',  '',   't1',   3)`)

	tags, err := d.ListTags()
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 3 {
		t.Fatalf("got %d tags, want 3", len(tags))
	}
	if tags[0].Title != "home" || tags[1].Title != "urgent" || tags[2].Title != "child" {
		t.Errorf("unexpected order: %+v", tags)
	}
	if tags[1].Shortcut != "u" {
		t.Errorf("urgent.Shortcut = %q, want u", tags[1].Shortcut)
	}
	if tags[2].ParentUUID != "t1" {
		t.Errorf("child.ParentUUID = %q, want t1", tags[2].ParentUUID)
	}
}

func TestFindTagUUID(t *testing.T) {
	d := newTestDB(t)
	mustExec(t, d, `INSERT INTO TMTag (uuid, title, "index") VALUES ('t1', 'urgent', 0)`)

	for _, ref := range []string{"urgent", "t1"} {
		got, err := d.FindTagUUID(ref)
		if err != nil {
			t.Fatalf("FindTagUUID(%q): %v", ref, err)
		}
		if got != "t1" {
			t.Errorf("FindTagUUID(%q) = %q, want t1", ref, got)
		}
	}

	got, err := d.FindTagUUID("missing")
	if err != nil {
		t.Fatalf("FindTagUUID(missing): %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string for missing tag, got %q", got)
	}
}

func TestListTagsEmpty(t *testing.T) {
	d := newTestDB(t)
	tags, err := d.ListTags()
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("expected empty, got %d", len(tags))
	}
}

func TestUnknownTags(t *testing.T) {
	d := newTestDB(t)
	mustExec(t, d, `INSERT INTO TMTag (uuid, title, "index") VALUES
		('t1', 'urgent', 0),
		('t2', 'Work',   1)`)

	cases := []struct {
		name  string
		input []string
		want  []string
	}{
		{"allKnown", []string{"urgent", "Work"}, nil},
		{"someUnknown", []string{"urgent", "cifas-auto-reject"}, []string{"cifas-auto-reject"}},
		{"allUnknown", []string{"nope", "nada"}, []string{"nope", "nada"}},
		{"caseInsensitive", []string{"URGENT", "work"}, nil},
		{"dedupes", []string{"nope", "NOPE", "nope"}, []string{"nope"}},
		{"trimsWhitespace", []string{"  urgent  "}, nil},
		{"skipsEmpty", []string{"", "   "}, nil},
		{"none", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := d.UnknownTags(c.input)
			if err != nil {
				t.Fatalf("UnknownTags: %v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("UnknownTags(%v) = %v, want %v", c.input, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("UnknownTags(%v)[%d] = %q, want %q", c.input, i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestUnknownTagsEmptyDatabase(t *testing.T) {
	d := newTestDB(t)
	got, err := d.UnknownTags([]string{"anything"})
	if err != nil {
		t.Fatalf("UnknownTags: %v", err)
	}
	if len(got) != 1 || got[0] != "anything" {
		t.Errorf("got %v, want [anything]", got)
	}
}
