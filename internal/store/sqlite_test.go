package store

import "testing"

func TestSQLiteDSNWindowsDrivePath(t *testing.T) {
	got := sqliteDSN(`C:/Users/runner/AppData/Local/pcast.db`)
	want := "file:///C:/Users/runner/AppData/Local/pcast.db"
	if got != want {
		t.Fatalf("sqliteDSN() = %q, want %q", got, want)
	}
}
