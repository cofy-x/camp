package hoststorage

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelperFiltersCredentialsAndBoundsItsOutput(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	script := filepath.Join(root, "storage.mjs")
	t.Setenv("CAMP_PRIVATE_CREDENTIAL_SENTINEL", "must-not-reach-node")
	if err = os.WriteFile(script, []byte(`import process from 'node:process'; let s='';for await(const b of process.stdin)s+=b;process.stdout.write(JSON.stringify({input:JSON.parse(s),credential:process.env.CAMP_PRIVATE_CREDENTIAL_SENTINEL??null}));`), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := Call(t.Context(), node, root, root, map[string]string{"command": "sample"}, nil)
	if err != nil || !bytes.Contains(data, []byte(`"credential":null`)) {
		t.Fatalf("%s %v", data, err)
	}
	if err = os.WriteFile(script, []byte(`process.stdout.write('x'.repeat(9*1024*1024));`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Call(t.Context(), node, root, root, map[string]string{}, nil); err == nil {
		t.Fatal("Oversized output accepted")
	}
	if err = os.WriteFile(script, []byte(`console.log(JSON.stringify({error:{code:'StalePlan',message:'Create a fresh plan.'}}));process.exitCode=1;`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Call(t.Context(), node, root, root, map[string]string{}, nil); err == nil || !strings.Contains(err.Error(), "StalePlan") {
		t.Fatal(err)
	}
}
