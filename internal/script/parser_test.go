package script

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseScript(t *testing.T) {
	tmp := t.TempDir()
	wl := filepath.Join(tmp, "users.txt")
	if err := os.WriteFile(wl, []byte("admin\nroot\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	scriptContent := `
# Header comment
#
%define SEP :
%define PORT #[80-81]
%define HOSTPORT localhost$SEP;$PORT;

%include ./users.txt
^@$HOSTPORT; # inline comment

#[0-255].0.0.1
`
	scriptPath := filepath.Join(tmp, "test.gmap")
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := ParseFile(scriptPath)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].Expression != "^@localhost:#[80-81]" {
		t.Errorf("entries[0].Expression = %q", entries[0].Expression)
	}
	if len(entries[0].Files) != 1 || entries[0].Files[0] != wl {
		t.Errorf("entries[0].Files = %v, want [%s]", entries[0].Files, wl)
	}
	// Second expression must have cleared %include files AND preserved #[0-255]!
	if entries[1].Expression != "#[0-255].0.0.1" || len(entries[1].Files) != 0 {
		t.Errorf("entries[1] = %+v", entries[1])
	}

	badScripts := []struct {
		src  string
		want string
	}{
		{"%define\n", "requires 2 arguments"},
		{"%define $bad 123\n", "cannot contain"},
		{"%define bad; 123\n", "cannot contain"},
		{"%include\n", "requires 1 argument"},
	}
	for _, bc := range badScripts {
		_, err := ParseReader(strings.NewReader(bc.src), tmp)
		if err == nil || !strings.Contains(err.Error(), bc.want) {
			t.Errorf("ParseReader(%q) err = %v, want %q", bc.src, err, bc.want)
		}
	}
}
