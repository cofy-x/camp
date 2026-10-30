package runnertools

import "testing"

func TestNativeNamespaceRetainsOpaqueTargetsAndRejectsSiblingPaths(t *testing.T) {
	tools := &Tools{token: "synthetic-test-token", workspacePath: "/Users/operator/Camp/attempt-1/workspace", referencesPath: "/Users/operator/Camp/references"}
	logical, err := tools.virtualFilePath("src/file.txt", tools.workspacePath)
	if err != nil || logical != "/workspace/src/file.txt" {
		t.Fatal(logical, err)
	}
	target := tools.target(logical)
	if target.Path != tools.workspacePath+"/src/file.txt" {
		t.Fatal(target)
	}
	if name, err := tools.targetPath(&target); err != nil || name != logical {
		t.Fatal(name, err)
	}
	for _, path := range []string{"/Users/operator/Camp/attempt-2/workspace/file.txt", tools.workspacePath + "-other/file.txt", "/Users/operator/.ssh/id_rsa"} {
		if _, err := tools.virtualFilePath(path, tools.workspacePath); err == nil {
			t.Fatal("native path escaped", path)
		}
	}
	target.Path = "/workspace/src/file.txt"
	if _, err := tools.targetPath(&target); err == nil {
		t.Fatal("a display alias reused an opaque target")
	}
	if name, err := tools.virtualFilePath(tools.referencesPath+"/evidence.txt", tools.workspacePath); err != nil || name != "/references/evidence.txt" {
		t.Fatal(name, err)
	}
}
