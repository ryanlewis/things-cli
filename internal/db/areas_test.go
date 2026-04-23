package db

import "testing"

func TestListAreasOrdered(t *testing.T) {
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMArea (uuid, title, visible, "index") VALUES
		('a1', 'Work',   1, 2),
		('a2', 'Home',   1, 1),
		('a3', 'Hidden', 0, 3)`)

	areas, err := d.ListAreas()
	if err != nil {
		t.Fatalf("ListAreas: %v", err)
	}
	if len(areas) != 3 {
		t.Fatalf("got %d areas, want 3", len(areas))
	}
	if areas[0].Title != "Home" || areas[1].Title != "Work" || areas[2].Title != "Hidden" {
		t.Errorf("unexpected order: %+v", areas)
	}
	if areas[2].Visible {
		t.Errorf("Hidden should not be visible")
	}
	if !areas[0].Visible {
		t.Errorf("Home should be visible")
	}
}

func TestFindAreaUUID(t *testing.T) {
	d := newTestDB(t)
	mustExec(t, d, `INSERT INTO TMArea (uuid, title, visible, "index") VALUES ('a1', 'Work', 1, 0)`)

	for _, ref := range []string{"Work", "a1"} {
		got, err := d.FindAreaUUID(ref)
		if err != nil {
			t.Fatalf("FindAreaUUID(%q): %v", ref, err)
		}
		if got != "a1" {
			t.Errorf("FindAreaUUID(%q) = %q, want a1", ref, got)
		}
	}

	got, err := d.FindAreaUUID("missing")
	if err != nil {
		t.Fatalf("FindAreaUUID(missing): %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string for missing area, got %q", got)
	}
}

func TestListAreasEmpty(t *testing.T) {
	d := newTestDB(t)
	areas, err := d.ListAreas()
	if err != nil {
		t.Fatalf("ListAreas: %v", err)
	}
	if len(areas) != 0 {
		t.Fatalf("expected empty, got %d", len(areas))
	}
}
