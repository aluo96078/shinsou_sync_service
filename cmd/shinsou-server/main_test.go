package main

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/aluo96078/shinsou_sync_service/internal/testutil"
)

func TestAdminCommands(t *testing.T) {
	d := testutil.NewDB(t)
	t.Setenv("SHINSOU_DATABASE_URL", d.URL+"&search_path="+d.Schema)
	codeRe := regexp.MustCompile(`^[A-Z2-7]{4}(-[A-Z2-7]{4}){3}\n$`)
	runCmd := func(args ...string) (string, error) {
		var out, errOut bytes.Buffer
		err := run(args, &out, &errOut)
		return out.String(), err
	}

	out, err := runCmd("admin", "reset-setup-code")
	if err != nil || !codeRe.MatchString(out) {
		t.Fatalf("reset-setup-code = %q, %v", out, err)
	}
	out, err = runCmd("admin", "create-invite", "--max-uses", "3", "--expires", "1h")
	if err != nil || !codeRe.MatchString(out) {
		t.Fatalf("create-invite = %q, %v", out, err)
	}
	if _, err := runCmd("admin", "set-role", "nobody", "admin"); err == nil {
		t.Fatal("set-role for an unknown user must fail")
	}
	if _, err := d.Pool.Exec(context.Background(), `insert into users (id, username, password_hash) values (gen_random_uuid(), 'alice', 'x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd("admin", "set-role", "alice", "owner"); err == nil {
		t.Fatal("invalid role must fail")
	}
	if out, err := runCmd("admin", "set-role", "ALICE", "admin"); err != nil || !strings.Contains(out, "admin") {
		t.Fatalf("set-role = %q, %v", out, err)
	}
	// With an administrator present the setup code can no longer be reset.
	if _, err := runCmd("admin", "reset-setup-code"); err == nil {
		t.Fatal("reset-setup-code must fail once an admin exists")
	}
	if _, err := runCmd("bogus"); err == nil {
		t.Fatal("unknown command must fail")
	}
}

func TestMigrateCommandIsIdempotent(t *testing.T) {
	d := testutil.NewDB(t)
	t.Setenv("SHINSOU_DATABASE_URL", d.URL+"&search_path="+d.Schema)
	var out bytes.Buffer
	if err := run([]string{"migrate"}, &out, &out); err != nil || !strings.Contains(out.String(), "applied 0") {
		t.Fatalf("migrate = %q, %v", out.String(), err)
	}
}
