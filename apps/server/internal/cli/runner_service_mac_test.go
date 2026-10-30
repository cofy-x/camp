package cli

import (
	"context"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMacServiceSeparatesInstallationStartAndLogin(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private & retained")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(directory, "journal.sqlite")
	if err := os.WriteFile(journal, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	var commands [][]string
	loaded := false
	manager := macRunnerService{domain: "gui/501", directory: directory, loginDirectory: t.TempDir(), loaded: func(context.Context, string) bool { return loaded }, command: func(_ context.Context, args ...string) error {
		commands = append(commands, append([]string(nil), args...))
		if args[0] == "bootstrap" {
			loaded = true
		}
		if args[0] == "bootout" {
			loaded = false
		}
		return nil
	}}
	ctx := context.Background()
	if err := manager.apply(ctx, "install", "/Applications/Camp & Tools/camp"); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 0 || loaded {
		t.Fatal("installation implicitly started a process", commands)
	}
	label := macServiceLabel(directory)
	login := filepath.Join(manager.loginDirectory, label+".plist")
	if _, err := os.Stat(login); !os.IsNotExist(err) {
		t.Fatal("installation implicitly enabled login startup", err)
	}
	data, err := readManagedMacPlist(filepath.Join(directory, "service", label+".plist"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal("invalid XML", err)
		}
	}
	if !strings.Contains(string(data), "Camp &amp; Tools") || !strings.Contains(string(data), "<key>KeepAlive</key><false/>") {
		t.Fatal("unescaped arguments or automatic failure replay", string(data))
	}
	for _, action := range []string{"start", "start", "enable-login", "disable-login", "stop", "remove"} {
		before := len(commands)
		if err := manager.apply(ctx, action, ""); err != nil {
			t.Fatal(action, err)
		}
		if strings.Contains(action, "login") && len(commands) != before {
			t.Fatal("changing login startup changed the active process", action)
		}
	}
	if len(commands) != 3 || commands[0][0] != "bootstrap" || commands[1][0] != "kickstart" || commands[2][0] != "bootout" {
		t.Fatal("incorrect process lifecycle", commands)
	}
	if data, err := os.ReadFile(journal); err != nil || string(data) != "retain" {
		t.Fatal("service changes discarded Runner data", err)
	}
}

func TestMacServiceRefusesUnmanagedOrLinkedFiles(t *testing.T) {
	directory := t.TempDir()
	name := filepath.Join(directory, "runner.plist")
	if err := os.WriteFile(name, []byte("unmanaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeMacPlist(name, []byte(macServiceHeader)); err == nil {
		t.Fatal("replaced unmanaged service")
	}
	linked := filepath.Join(directory, "linked.plist")
	if err := os.Symlink(name, linked); err != nil {
		t.Fatal(err)
	}
	if err := writeMacPlist(linked, []byte(macServiceHeader)); err == nil {
		t.Fatal("accepted a linked service file")
	}
}

func TestMacServiceFindsRunnerStartedBeforeGuiLogin(t *testing.T) {
	directory := t.TempDir()
	user := "user/501/" + macServiceLabel(directory)
	active := map[string]bool{user: true}
	var commands [][]string
	manager := macRunnerService{domain: "gui/501", otherDomains: []string{"user/501"}, directory: directory, loginDirectory: t.TempDir(), loaded: func(_ context.Context, target string) bool { return active[target] }, command: func(_ context.Context, args ...string) error {
		commands = append(commands, args)
		if args[0] == "bootout" {
			delete(active, args[1])
		}
		return nil
	}}
	data, err := macServicePlist("/Applications/Camp/camp", directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeMacPlist(filepath.Join(directory, "service", macServiceLabel(directory)+".plist"), data); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"start", "status", "stop"} {
		if err := manager.apply(context.Background(), action, ""); err != nil {
			t.Fatal(err)
		}
	}
	if len(commands) != 3 || commands[0][0] != "kickstart" || commands[1][0] != "print" || commands[2][0] != "bootout" {
		t.Fatal("existing Runner was replaced across login domains", commands)
	}
	for _, command := range commands {
		if command[1] != user {
			t.Fatal("command targeted another login domain", command)
		}
	}
	if len(active) != 0 {
		t.Fatal("Runner from the prior login context survived stop")
	}
}
