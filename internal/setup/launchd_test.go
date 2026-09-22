package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testAgent(t *testing.T) LaunchAgent {
	t.Helper()
	home := t.TempDir()
	config := filepath.Join(home, ".config", "cumin", "config.toml")
	return NewLaunchAgent(home, "/usr/local/bin/cumin", config, "/usr/local/bin:/usr/bin:/bin")
}

func TestNewLaunchAgent_UsesTheDirectoriesOfTheDesignNote(t *testing.T) {
	home := t.TempDir()
	agent := NewLaunchAgent(home, "/usr/local/bin/cumin", "/c/config.toml", "/usr/bin")

	if want := filepath.Join(home, "Library", "LaunchAgents", LaunchAgentLabel+".plist"); agent.PlistPath != want {
		t.Errorf("plist = %q, want %q", agent.PlistPath, want)
	}
	if want := filepath.Join(home, ".local", "state", "cumin"); agent.StateDir != want {
		t.Errorf("state directory = %q, want %q", agent.StateDir, want)
	}
	out, errorLog := agent.LogPaths()
	if out != filepath.Join(agent.StateDir, "cumin.log") || errorLog != filepath.Join(agent.StateDir, "cumin.err.log") {
		t.Errorf("logs = %q and %q", out, errorLog)
	}
}

func TestPlist_HoldsTheKeysOfTheRequirement(t *testing.T) {
	agent := testAgent(t)
	plist, err := agent.Plist()
	if err != nil {
		t.Fatal(err)
	}
	out, errorLog := agent.LogPaths()
	for _, want := range []string{
		"<key>Label</key>\n\t<string>" + LaunchAgentLabel + "</string>",
		"<string>/usr/local/bin/cumin</string>",
		"<string>run</string>",
		"<string>--config</string>",
		"<string>" + agent.ConfigPath + "</string>",
		"<key>RunAtLoad</key>\n\t<true/>",
		"<key>KeepAlive</key>\n\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>",
		"<key>StandardOutPath</key>\n\t<string>" + out + "</string>",
		"<key>StandardErrorPath</key>\n\t<string>" + errorLog + "</string>",
		"<key>PATH</key>\n\t\t<string>/usr/local/bin:/usr/bin:/bin</string>",
		"<key>ProcessType</key>\n\t<string>Standard</string>",
		"<key>ExitTimeOut</key>\n\t<integer>60</integer>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("the plist does not hold:\n%s\n\ngot:\n%s", want, plist)
		}
	}
}

// The plist holds paths, never a secret. It names the settings file; it
// does not copy what is in it.
func TestPlist_HoldsNoSecret(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.toml")
	const secret = "client-id-that-must-not-be-copied"
	if err := os.WriteFile(configPath, []byte("[github_apps.example-org]\ncumin-core = \""+secret+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plist, err := NewLaunchAgent(home, "/usr/local/bin/cumin", configPath, "/usr/bin").Plist()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plist, secret) || strings.Contains(plist, "github_apps") {
		t.Errorf("the plist holds the contents of the settings file:\n%s", plist)
	}
	if !strings.Contains(plist, configPath) {
		t.Error("the plist does not name the settings file")
	}
}

func TestPlist_EscapesAValueForXML(t *testing.T) {
	plist, err := NewLaunchAgent("/home/a&b", "/opt/cumin & tools/cumin", "/c/config.toml", "/usr/bin").Plist()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plist, "/opt/cumin &amp; tools/cumin") {
		t.Errorf("the plist does not escape the path:\n%s", plist)
	}
}

func TestCheckProgram_RefusesATemporaryBuild(t *testing.T) {
	tests := []struct {
		name    string
		program string
		wantErr bool
	}{
		{"a built binary", "/usr/local/bin/cumin", false},
		{"a go run binary", filepath.Join(os.TempDir(), "go-build123", "b001", "exe", "cumin"), true},
		{"a relative path", "cumin", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckProgram(tt.program)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckProgram(%q) = %v, want an error: %v", tt.program, err, tt.wantErr)
			}
		})
	}
}

func TestInstallLaunchAgent_WritesThePlistAndCreatesTheStateDirectory(t *testing.T) {
	agent := testAgent(t)
	result, err := InstallLaunchAgent(agent, false)
	if err != nil || result != "written" {
		t.Fatalf("InstallLaunchAgent = %q, %v", result, err)
	}
	plist, err := agent.Plist()
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(agent.PlistPath)
	if err != nil {
		t.Fatalf("read the plist: %v", err)
	}
	if string(written) != plist {
		t.Error("the file on disk is not the plist of the job")
	}
	if info, err := os.Stat(agent.StateDir); err != nil || !info.IsDir() {
		t.Errorf("the state directory is missing: %v", err)
	}

	// Running again changes nothing.
	if result, err := InstallLaunchAgent(agent, false); err != nil || result != "unchanged" {
		t.Errorf("the second run = %q, %v, want unchanged", result, err)
	}
}

func TestInstallLaunchAgent_KeepsADifferentFileWithoutForce(t *testing.T) {
	agent := testAgent(t)
	if err := os.MkdirAll(filepath.Dir(agent.PlistPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const other = "<?xml version=\"1.0\"?>\n<plist><dict><key>Label</key><string>dev.cumin-works.cumin</string></dict></plist>\n"
	if err := os.WriteFile(agent.PlistPath, []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := InstallLaunchAgent(agent, false)
	if err == nil {
		t.Fatal("InstallLaunchAgent replaced a different file without --force")
	}
	if result != "kept" {
		t.Errorf("result = %q, want kept", result)
	}
	if !strings.Contains(err.Error(), "--force") || !strings.Contains(err.Error(), "+ ") {
		t.Errorf("the error does not show the difference and the way out:\n%v", err)
	}
	if current, _ := os.ReadFile(agent.PlistPath); string(current) != other {
		t.Error("the file changed")
	}

	if result, err := InstallLaunchAgent(agent, true); err != nil || result != "written" {
		t.Errorf("with --force = %q, %v, want written", result, err)
	}
	plist, _ := agent.Plist()
	if current, _ := os.ReadFile(agent.PlistPath); string(current) != plist {
		t.Error("--force did not replace the file")
	}
}

func TestLaunchctlCommands_UseTheGuiDomainOfTheUser(t *testing.T) {
	agent := testAgent(t)
	commands := agent.LaunchctlCommands(501)
	want := []string{
		"launchctl bootstrap gui/501 " + agent.PlistPath,
		"launchctl kill SIGTERM gui/501/" + LaunchAgentLabel,
		"launchctl kickstart -k gui/501/" + LaunchAgentLabel,
		"launchctl bootout gui/501/" + LaunchAgentLabel,
	}
	for i, command := range want {
		if commands[i] != command {
			t.Errorf("command %d = %q, want %q", i, commands[i], command)
		}
	}
}

// plutil reads the plist as launchd does. It is part of macOS, so the test
// is skipped on another system.
func TestPlist_PassesPlutilLint(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("plutil is macOS only")
	}
	agent := testAgent(t)
	if _, err := InstallLaunchAgent(agent, false); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("/usr/bin/plutil", "-lint", agent.PlistPath).CombinedOutput()
	if err != nil {
		t.Fatalf("plutil -lint: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "OK") {
		t.Errorf("plutil -lint said: %s", output)
	}
}
