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
