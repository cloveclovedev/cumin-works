package setup

// This file writes the LaunchAgent that keeps `cumin run` running on the
// Host: it starts at login, launchd starts it again when it ends with an
// error, and its logs go to a file under the state directory. The plist
// belongs to the Host, so the repository holds no path of the Owner; the
// command takes every value from the process that runs it.
//
// The keys come from `man launchd.plist`, and the reason for each is in
// docs/ja/designs/cumin-core.md, the topic on launchd.

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// LaunchAgentLabel identifies the job in launchd. It is also the name of
// the plist and the last part of a service target
// (gui/<uid>/<LaunchAgentLabel>).
const LaunchAgentLabel = "dev.cumin-works.cumin"

// Names of the log files under the state directory.
const (
	logFile      = "cumin.log"
	errorLogFile = "cumin.err.log"
)

// exitTimeOut is the seconds that launchd waits between SIGTERM and
// SIGKILL when the job is stopped (`man launchd.plist`). `cumin run`
// cancels the running request and waits for the grace of the agent
// adapter, so the value must be longer than that grace and its margin.
const exitTimeOut = 60

// LaunchAgent is the job to write: every value that the plist needs.
type LaunchAgent struct {
	Label string
	// Program is the absolute path of the cumin executable.
	Program string
	// ConfigPath is the Host settings file that `cumin run` reads.
	ConfigPath string
	// StateDir holds the log files. cumin creates it.
	StateDir string
	// PlistPath is where the plist goes.
	PlistPath string
	// PathEnv is the PATH of the job. launchd gives a job a small PATH, so
	// the CLI of an agent, git, and gh would not be found without it.
	PathEnv string
}

// NewLaunchAgent returns the job of the current user. program is the
// executable to run (the absolute path of the running cumin), configPath
// the Host settings file, home the home directory, and pathEnv the PATH of
// the login shell.
func NewLaunchAgent(home, program, configPath, pathEnv string) LaunchAgent {
	return LaunchAgent{
		Label:      LaunchAgentLabel,
		Program:    program,
		ConfigPath: configPath,
		StateDir:   filepath.Join(home, ".local", "state", "cumin"),
		PlistPath:  filepath.Join(home, "Library", "LaunchAgents", LaunchAgentLabel+".plist"),
		PathEnv:    pathEnv,
	}
}

// goBuildDir matches the directory that `go run` and `go test` create for
// a build: go-build followed by digits. A directory that a person named
// (go-builds, for example) does not match.
var goBuildDir = regexp.MustCompile(`(^|/)go-build[0-9]+(/|$)`)

// CheckProgram reports why the executable cannot be the program of a
// LaunchAgent. A binary that `go run` built lives in a temporary directory
// and is removed when the command ends, so launchd would never find it
// again.
func CheckProgram(program string) error {
	if !filepath.IsAbs(program) {
		return fmt.Errorf("the path of cumin (%s) is not absolute", program)
	}
	temporary := goBuildDir.MatchString(program)
	for _, dir := range temporaryDirs() {
		temporary = temporary || strings.HasPrefix(program, dir)
	}
	if temporary {
		return fmt.Errorf("%s is a temporary build: build the command first (go build -o cumin ./cmd/cumin) and run that binary", program)
	}
	return nil
}

// temporaryDirs returns the temporary directory of the Host, both as the
// environment gives it and with its symbolic links resolved. On macOS the
// two differ (/var/folders/... and /private/var/folders/...), and
// os.Executable can give either.
func temporaryDirs() []string {
	separator := string(filepath.Separator)
	temporary := filepath.Clean(os.TempDir()) + separator
	dirs := []string{temporary}
	if resolved, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		if cleaned := filepath.Clean(resolved) + separator; cleaned != temporary {
			dirs = append(dirs, cleaned)
		}
	}
	return dirs
}

// LogPaths returns the files that the job writes: the standard output of
// `cumin run`, which holds its JSON logs, and its standard error.
func (a LaunchAgent) LogPaths() (out, err string) {
	return filepath.Join(a.StateDir, logFile), filepath.Join(a.StateDir, errorLogFile)
}

// Plist returns the contents of the plist.
//
// Both paths must be absolute. launchd gives a job no working directory of
// its own, so a relative path would be resolved somewhere else, `cumin run`
// would not find its settings, and KeepAlive would start it again and
// again.
func (a LaunchAgent) Plist() (string, error) {
	if err := CheckProgram(a.Program); err != nil {
		return "", err
	}
	if !filepath.IsAbs(a.ConfigPath) {
		return "", fmt.Errorf("the path of the settings file (%s) is not absolute: launchd gives the job no working directory", a.ConfigPath)
	}
	out, errorLog := a.LogPaths()
	data := struct {
		Label, StandardOutPath, StandardErrorPath, PathEnv string
		ProgramArguments                                   []string
		ExitTimeOut                                        int
	}{
		Label:             a.Label,
		StandardOutPath:   out,
		StandardErrorPath: errorLog,
		PathEnv:           a.PathEnv,
		ProgramArguments:  []string{a.Program, "run", "--config", a.ConfigPath},
		ExitTimeOut:       exitTimeOut,
	}
	var b strings.Builder
	if err := plistTemplate.Execute(&b, data); err != nil {
		return "", fmt.Errorf("build the plist: %w", err)
	}
	return b.String(), nil
}

// InstallLaunchAgent creates the state directory and writes the plist.
//
// It reports what happened: "written", "unchanged" when the file already
// holds the same contents, or "kept" when a different file is there and
// force is false. In the last case the difference is in the error, so that
// the Owner sees what would change.
func InstallLaunchAgent(a LaunchAgent, force bool) (string, error) {
	plist, err := a.Plist()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(a.StateDir, 0o700); err != nil {
		return "", fmt.Errorf("create the state directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(a.PlistPath), 0o755); err != nil {
		return "", fmt.Errorf("create the LaunchAgents directory: %w", err)
	}
	switch current, err := os.ReadFile(a.PlistPath); {
	case err == nil && string(current) == plist:
		return "unchanged", nil
	case err == nil && !force:
		return "kept", fmt.Errorf("%s holds a different job. Check the difference, then run again with --force to replace it:\n%s",
			a.PlistPath, difference(string(current), plist))
	case err != nil && !os.IsNotExist(err):
		return "", fmt.Errorf("read %s: %w", a.PlistPath, err)
	}
	if err := os.WriteFile(a.PlistPath, []byte(plist), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", a.PlistPath, err)
	}
	return "written", nil
}

// PlistState says what stands at the path of the plist.
type PlistState string

const (
	// PlistAbsent: there is no file.
	PlistAbsent PlistState = "absent"
	// PlistOfThisJob: the file declares this job.
	PlistOfThisJob PlistState = "this job"
	// PlistOfAnotherJob: the file declares another job, or no job at all.
	PlistOfAnotherJob PlistState = "another job"
)

// PlistState reads the file at the path of the plist and says whose job it
// is. The check is the Label, not the whole file: a plist holds the PATH of
// the shell that wrote it, so two files of the same job can differ without
// meaning anything.
func (a LaunchAgent) PlistState() (PlistState, error) {
	data, err := os.ReadFile(a.PlistPath)
	switch {
	case os.IsNotExist(err):
		return PlistAbsent, nil
	case err != nil:
		return "", fmt.Errorf("read %s: %w", a.PlistPath, err)
	}
	label, err := plistLabel(data)
	if err != nil {
		return "", fmt.Errorf("read the job of %s: %w", a.PlistPath, err)
	}
	if label != a.Label {
		return PlistOfAnotherJob, nil
	}
	return PlistOfThisJob, nil
}

// plistLabel returns the value of the Label key of the job, or an empty
// string when the file has none. The plist of a job is a dict of keys and
// values, so the value of a key is the element that follows it.
//
// Only the dict of the job counts. A dict or an array that stands inside
// it (EnvironmentVariables, for example) can hold a key named Label of its
// own, and that key names nothing.
func plistLabel(data []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth := 0 // the dict of the job is depth 1
	afterLabelKey := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		switch element := token.(type) {
		case xml.StartElement:
			var text string
			switch element.Name.Local {
			case "dict", "array":
				depth++
				afterLabelKey = false
			case "key":
				if err := decoder.DecodeElement(&text, &element); err != nil {
					return "", err
				}
				afterLabelKey = depth == 1 && strings.TrimSpace(text) == "Label"
			case "string":
				if err := decoder.DecodeElement(&text, &element); err != nil {
					return "", err
				}
				if afterLabelKey {
					return strings.TrimSpace(text), nil
				}
			default:
				afterLabelKey = false
			}
		case xml.EndElement:
			if element.Name.Local == "dict" || element.Name.Local == "array" {
				depth--
				afterLabelKey = false
			}
		}
	}
}

// RemoveLaunchAgentFile removes the plist of the job.
//
// It reports what happened: "removed", "absent" when there is no file, or
// "kept" when the file at that path holds another job and force is false.
// Only the plist goes: the logs, the state, and the binary stay, because a
// person put them there and may want them.
func RemoveLaunchAgentFile(a LaunchAgent, force bool) (string, error) {
	// force removes whatever stands at that path, without reading it. A
	// file that does not parse is one of the reasons to give --force.
	if !force {
		state, err := a.PlistState()
		switch {
		case err != nil:
			return "", err
		case state == PlistAbsent:
			return "absent", nil
		case state == PlistOfAnotherJob:
			return "kept", fmt.Errorf("%s does not hold the job %s. Look at the file, then run again with --force to remove it anyway", a.PlistPath, a.Label)
		}
	}
	switch err := os.Remove(a.PlistPath); {
	case os.IsNotExist(err):
		return "absent", nil
	case err != nil:
		return "", fmt.Errorf("remove %s: %w", a.PlistPath, err)
	}
	return "removed", nil
}

// ServiceTarget is the job in the domain of the user, as launchctl wants
// it (`man launchctl`).
func (a LaunchAgent) ServiceTarget(uid int) string {
	return fmt.Sprintf("gui/%d/%s", uid, a.Label)
}

// LaunchctlCommands returns the commands that start, stop, restart, and
// remove the job, in the order of the setup guide. uid is the user id of
// the Owner (`man launchctl`: a LaunchAgent of a logged-in user lives in
// the gui domain).
func (a LaunchAgent) LaunchctlCommands(uid int) []string {
	target := a.ServiceTarget(uid)
	return []string{
		fmt.Sprintf("launchctl bootstrap gui/%d %s", uid, shellQuote(a.PlistPath)),
		fmt.Sprintf("launchctl kill SIGTERM %s", target),
		fmt.Sprintf("launchctl kickstart -k %s", target),
		fmt.Sprintf("launchctl bootout %s", target),
	}
}

// safeForShell matches the characters that a shell passes through as they
// are. Anything else is quoted, because the commands are printed to be
// copied into a shell and a home directory may hold a space.
var safeForShell = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote returns the path as one argument of a shell command.
func shellQuote(value string) string {
	if value != "" && safeForShell.MatchString(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// difference lists the lines that differ, by position. The two files are
// built from the same template, so a line by line comparison is enough to
// show what changed.
func difference(current, next string) string {
	currentLines, nextLines := strings.Split(current, "\n"), strings.Split(next, "\n")
	var b strings.Builder
	for i := 0; i < max(len(currentLines), len(nextLines)); i++ {
		var a, n string
		if i < len(currentLines) {
			a = currentLines[i]
		}
		if i < len(nextLines) {
			n = nextLines[i]
		}
		if a != n {
			if a != "" {
				fmt.Fprintf(&b, "  - %s\n", a)
			}
			if n != "" {
				fmt.Fprintf(&b, "  + %s\n", n)
			}
		}
	}
	return b.String()
}

// escapeXML is the template function that puts a value into the plist.
func escapeXML(value string) (string, error) {
	var b bytes.Buffer
	if err := xml.EscapeText(&b, []byte(value)); err != nil {
		return "", err
	}
	return b.String(), nil
}

// plistTemplate is the job. KeepAlive with SuccessfulExit false starts the
// job again when it ends with an error and leaves it stopped after a clean
// stop, and it implies RunAtLoad, which is written out for the reader
// (`man launchd.plist`). ProcessType Standard is the same as no
// ProcessType; Background would throttle the CPU and the I/O of cumin and
// of every agent that it starts.
var plistTemplate = template.Must(template.New("plist").Funcs(template.FuncMap{"xml": escapeXML}).Parse(
	`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{xml .Label}}</string>
	<key>ProgramArguments</key>
	<array>
{{- range .ProgramArguments}}
		<string>{{xml .}}</string>
{{- end}}
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>StandardOutPath</key>
	<string>{{xml .StandardOutPath}}</string>
	<key>StandardErrorPath</key>
	<string>{{xml .StandardErrorPath}}</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>{{xml .PathEnv}}</string>
	</dict>
	<key>ProcessType</key>
	<string>Standard</string>
	<key>ExitTimeOut</key>
	<integer>{{.ExitTimeOut}}</integer>
</dict>
</plist>
`))
