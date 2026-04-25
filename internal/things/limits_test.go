package things

import (
	"os/exec"
	"strings"
	"testing"
)

func repeat(n int) string { return strings.Repeat("a", n) }

func checklist(items int) string {
	parts := make([]string, items)
	for i := range parts {
		parts[i] = "item"
	}
	return strings.Join(parts, "\n")
}

func TestValidateNotes(t *testing.T) {
	tests := []struct {
		name    string
		val     string
		wantErr bool
	}{
		{"below", repeat(MaxNotesLen - 1), false},
		{"at", repeat(MaxNotesLen), false},
		{"above", repeat(MaxNotesLen + 1), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNotes("notes", tt.val)
			if (err != nil) != tt.wantErr {
				t.Fatalf("got err=%v, wantErr=%v", err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "notes") {
				t.Errorf("error should name the field: %v", err)
			}
		})
	}
}

func TestValidateString(t *testing.T) {
	tests := []struct {
		name    string
		val     string
		wantErr bool
	}{
		{"below", repeat(MaxStringLen - 1), false},
		{"at", repeat(MaxStringLen), false},
		{"above", repeat(MaxStringLen + 1), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateString("title", tt.val)
			if (err != nil) != tt.wantErr {
				t.Fatalf("got err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateChecklist_Count(t *testing.T) {
	tests := []struct {
		name    string
		val     string
		wantErr bool
	}{
		{"empty", "", false},
		{"below", checklist(MaxChecklistItems - 1), false},
		{"at", checklist(MaxChecklistItems), false},
		{"above", checklist(MaxChecklistItems + 1), true},
		{"trailing-newline-at-limit", checklist(MaxChecklistItems) + "\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateChecklist("checklist", tt.val)
			if (err != nil) != tt.wantErr {
				t.Fatalf("got err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateChecklist_ItemLength(t *testing.T) {
	tests := []struct {
		name    string
		val     string
		wantErr bool
	}{
		{"below", "ok\n" + repeat(MaxStringLen-1), false},
		{"at", "ok\n" + repeat(MaxStringLen), false},
		{"above", "ok\n" + repeat(MaxStringLen+1), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateChecklist("checklist", tt.val)
			if (err != nil) != tt.wantErr {
				t.Fatalf("got err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateTags_PerTag(t *testing.T) {
	tests := []struct {
		name    string
		val     string
		wantErr bool
	}{
		{"below", "ok," + repeat(MaxStringLen-1), false},
		{"at", "ok," + repeat(MaxStringLen), false},
		{"above", "ok," + repeat(MaxStringLen+1), true},
		{"many-small-tags", strings.Repeat("a,", 5000) + "b", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTags("tags", tt.val)
			if (err != nil) != tt.wantErr {
				t.Fatalf("got err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

// Multi-byte input must be measured in characters, not bytes — a 10k-rune
// note of CJK or emoji is well within the documented limit.
func TestValidateNotes_CountsRunesNotBytes(t *testing.T) {
	notes := strings.Repeat("好", MaxNotesLen) // each rune is 3 bytes
	if err := validateNotes("notes", notes); err != nil {
		t.Fatalf("10k-rune notes should pass: %v", err)
	}
	overshoot := strings.Repeat("好", MaxNotesLen+1)
	if err := validateNotes("notes", overshoot); err == nil {
		t.Fatal("10k+1-rune notes should fail")
	}
}

func TestValidateAdd(t *testing.T) {
	if err := validateAdd(AddParams{Title: repeat(MaxStringLen + 1)}); err == nil {
		t.Fatal("expected title limit error")
	}
	if err := validateAdd(AddParams{Notes: repeat(MaxNotesLen + 1)}); err == nil {
		t.Fatal("expected notes limit error")
	}
	if err := validateAdd(AddParams{Checklist: checklist(MaxChecklistItems + 1)}); err == nil {
		t.Fatal("expected checklist limit error")
	}
	if err := validateAdd(AddParams{
		Title:     "ok",
		Notes:     repeat(MaxNotesLen),
		Checklist: checklist(MaxChecklistItems),
		Tags:      "one,two",
	}); err != nil {
		t.Fatalf("valid params should pass: %v", err)
	}
}

func TestValidateUpdate(t *testing.T) {
	big := repeat(MaxNotesLen + 1)
	if err := validateUpdate(UpdateParams{ID: "x", AuthToken: "t", Notes: &big}); err == nil {
		t.Fatal("expected notes limit error")
	}

	bigChk := checklist(MaxChecklistItems + 1)
	if err := validateUpdate(UpdateParams{AppendChecklist: &bigChk}); err == nil {
		t.Fatal("expected append-checklist limit error")
	}

	// Nil optional fields must not trip validation.
	if err := validateUpdate(UpdateParams{ID: "x", AuthToken: "t"}); err != nil {
		t.Fatalf("empty update should pass: %v", err)
	}
}

func TestValidateUpdateProject(t *testing.T) {
	big := repeat(MaxNotesLen + 1)
	if err := validateUpdateProject(UpdateProjectParams{PrependNotes: &big}); err == nil {
		t.Fatal("expected prepend-notes limit error")
	}
	if err := validateUpdateProject(UpdateProjectParams{}); err != nil {
		t.Fatalf("empty update-project should pass: %v", err)
	}
}

// stubExec swaps execCommand and reports whether it was invoked.
func stubExec(t *testing.T) *bool {
	t.Helper()
	called := false
	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = func(name string, args ...string) *exec.Cmd {
		called = true
		return exec.Command("true")
	}
	return &called
}

func TestRejectsBeforeOpen(t *testing.T) {
	bigNotes := repeat(MaxNotesLen + 1)

	cases := []struct {
		name string
		call func() error
	}{
		{"AddTask", func() error { return AddTask(AddParams{Notes: bigNotes}) }},
		{"AddProject", func() error { return AddProject(AddProjectParams{Notes: bigNotes}) }},
		{"UpdateTask", func() error {
			return UpdateTask(UpdateParams{ID: "x", AuthToken: "t", Notes: &bigNotes})
		}},
		{"UpdateProject", func() error {
			return UpdateProject(UpdateProjectParams{ID: "x", AuthToken: "t", Notes: &bigNotes})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := stubExec(t)
			if err := tc.call(); err == nil {
				t.Fatal("expected validation error")
			}
			if *called {
				t.Fatal("execCommand must not run when validation fails")
			}
		})
	}
}
