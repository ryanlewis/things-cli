package db

import "testing"

func TestGetAuthTokenPresent(t *testing.T) {
	d := newTestDB(t)
	mustExec(t, d, `INSERT INTO TMSettings (uuid, uriSchemeAuthenticationToken) VALUES ('s1', 'secret-token')`)

	got, err := d.GetAuthToken()
	if err != nil {
		t.Fatalf("GetAuthToken: %v", err)
	}
	if got != "secret-token" {
		t.Errorf("got %q, want %q", got, "secret-token")
	}
}

func TestGetAuthTokenNull(t *testing.T) {
	d := newTestDB(t)
	mustExec(t, d, `INSERT INTO TMSettings (uuid) VALUES ('s1')`)

	got, err := d.GetAuthToken()
	if err != nil {
		t.Fatalf("GetAuthToken: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestGetAuthTokenMissing(t *testing.T) {
	d := newTestDB(t)
	got, err := d.GetAuthToken()
	if err != nil {
		t.Fatalf("GetAuthToken: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
