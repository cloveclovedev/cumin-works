package scripts

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// diagrams is a repository that the test builds in a temporary directory,
// to check its diagrams there: a copy of scripts/render-diagrams.sh, two
// diagrams under docs/, the renderer under tools/plantuml/, one commit with
// the tag "base", and a fake docker first on PATH.
type diagrams struct {
	t     *testing.T
	root  string
	calls string
	env   []string
}

// The fake docker writes every call to the file CALLS, one line for each
// call. Its render of a source is the line "rendered" and then the source,
// so the SVG that the test commits for a source is the same bytes.
const fakeDocker = `#!/bin/sh
echo "docker $*" >>"$CALLS"
[ "$1" = "run" ] || exit 0
for arg in "$@"; do
  case "$arg" in
    *:/work:ro) work="${arg%:/work:ro}" ;;
    *:/out) out="${arg%:/out}" ;;
  esac
  last="$arg"
done
name="$(basename "$last" .puml)"
{ echo rendered; cat "$work/$name.puml"; } >"$out/$name.svg"
`

// rendered returns the SVG that the fake docker writes for a source.
func rendered(source string) string {
	return "rendered\n" + source
}

func newDiagrams(t *testing.T) *diagrams {
	t.Helper()
	dir := t.TempDir()
	d := &diagrams{t: t, root: filepath.Join(dir, "repo"), calls: filepath.Join(dir, "calls")}
	bin := filepath.Join(dir, "bin")
	d.env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CALLS="+d.calls,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
	)
	script, err := os.ReadFile("render-diagrams.sh")
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(bin, "docker"):                     fakeDocker,
		d.path("scripts/render-diagrams.sh"):             string(script),
		d.path("tools/plantuml/Dockerfile"):              "FROM plantuml\n",
		d.path("docs/ja/designs/one.puml"):               "@startuml one\n@enduml\n",
		d.path("docs/ja/designs/one.svg"):                rendered("@startuml one\n@enduml\n"),
		d.path("docs/ja/requirements/workflow/two.puml"): "@startuml two\n@enduml\n",
		d.path("docs/ja/requirements/workflow/two.svg"):  rendered("@startuml two\n@enduml\n"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	d.git("init", "--quiet")
	d.git("add", ".")
	d.git("-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "base")
	d.git("tag", "base")
	return d
}

func (d *diagrams) path(name string) string {
	return filepath.Join(d.root, filepath.FromSlash(name))
}

func (d *diagrams) write(name, content string) {
	d.t.Helper()
	if err := os.WriteFile(d.path(name), []byte(content), 0o644); err != nil {
		d.t.Fatal(err)
	}
}

func (d *diagrams) git(args ...string) string {
	d.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", d.root}, args...)...)
	cmd.Env = d.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		d.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// check runs --check against the tag "base", and fails the test when the
// run changed a file of the work tree. It returns standard error and the
// exit code.
func (d *diagrams) check() (string, int) {
	d.t.Helper()
	before := d.git("status", "--porcelain")
	cmd := exec.Command(d.path("scripts/render-diagrams.sh"), "--check", "base")
	cmd.Env = d.env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if after := d.git("status", "--porcelain"); after != before {
		d.t.Errorf("git status --porcelain after --check = %q, want %q as before", after, before)
	}
	return stderr.String(), exitCode(d.t, err)
}

// renders returns the sources that the fake docker rendered, in order, as
// paths from the top of the repository.
func (d *diagrams) renders() []string {
	d.t.Helper()
	data, err := os.ReadFile(d.calls)
	if err != nil && !os.IsNotExist(err) {
		d.t.Fatal(err)
	}
	var sources []string
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "docker run ") {
			continue
		}
		fields := strings.Fields(line)
		dir := ""
		for _, field := range fields {
			if work, ok := strings.CutSuffix(field, ":/work:ro"); ok {
				dir = work
			}
		}
		rel, err := filepath.Rel(d.root, filepath.Join(dir, filepath.Base(fields[len(fields)-1])))
		if err != nil {
			d.t.Fatal(err)
		}
		sources = append(sources, filepath.ToSlash(rel))
	}
	return sources
}

func (d *diagrams) wantRenders(want ...string) {
	d.t.Helper()
	if got := d.renders(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		d.t.Errorf("rendered %q, want %q", got, want)
	}
}

func TestRenderDiagrams_CheckWithNoChangedDiagramNeverCallsDocker(t *testing.T) {
	d := newDiagrams(t)
	// A change outside docs/ and tools/plantuml/ selects no diagram.
	d.write("tools/other.svg", "<svg/>\n")
	d.write("scripts/render-diagrams.sh", mustRead(t, "render-diagrams.sh")+"# a change\n")

	stderr, code := d.check()

	if code != 0 {
		t.Errorf("exit code = %d, want 0\n%s", code, stderr)
	}
	if _, err := os.Stat(d.calls); !os.IsNotExist(err) {
		t.Errorf("docker was called: %s", mustRead(t, d.calls))
	}
}

func TestRenderDiagrams_CheckRendersOnlyTheDiagramWhoseSourceChanged(t *testing.T) {
	d := newDiagrams(t)
	source := "@startuml one\nA -> B\n@enduml\n"
	d.write("docs/ja/designs/one.puml", source)
	d.write("docs/ja/designs/one.svg", rendered(source))

	stderr, code := d.check()

	if code != 0 {
		t.Errorf("exit code = %d, want 0\n%s", code, stderr)
	}
	d.wantRenders("docs/ja/designs/one.puml")
}

func TestRenderDiagrams_CheckNamesAnSVGThatIsNotTheRenderOfItsSource(t *testing.T) {
	d := newDiagrams(t)
	d.write("docs/ja/designs/one.svg", rendered("@startuml one\n@enduml\n")+"<!-- edited by hand -->\n")

	stderr, code := d.check()

	if code != 1 {
		t.Errorf("exit code = %d, want 1\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "docs/ja/designs/one.svg") || strings.Contains(stderr, "two.svg") {
		t.Errorf("standard error = %q, want only docs/ja/designs/one.svg", stderr)
	}
	d.wantRenders("docs/ja/designs/one.puml")
}

func TestRenderDiagrams_CheckNamesAMissingSVG(t *testing.T) {
	d := newDiagrams(t)
	if err := os.Remove(d.path("docs/ja/designs/one.svg")); err != nil {
		t.Fatal(err)
	}
	// A new diagram that git does not track yet, with no SVG.
	d.write("docs/ja/designs/three.puml", "@startuml three\n@enduml\n")

	stderr, code := d.check()

	if code != 1 {
		t.Errorf("exit code = %d, want 1\n%s", code, stderr)
	}
	for _, svg := range []string{"docs/ja/designs/one.svg", "docs/ja/designs/three.svg"} {
		if !strings.Contains(stderr, svg) {
			t.Errorf("standard error = %q, want %s", stderr, svg)
		}
	}
}

func TestRenderDiagrams_CheckWithAChangedRendererChecksEveryDiagram(t *testing.T) {
	d := newDiagrams(t)
	d.write("tools/plantuml/Dockerfile", "FROM plantuml:next\n")

	stderr, code := d.check()

	if code != 0 {
		t.Errorf("exit code = %d, want 0\n%s", code, stderr)
	}
	d.wantRenders("docs/ja/designs/one.puml", "docs/ja/requirements/workflow/two.puml")
}

func TestRenderDiagrams_TheUsageTextNamesTheCheckMode(t *testing.T) {
	d := newDiagrams(t)
	cmd := exec.Command(d.path("scripts/render-diagrams.sh"), "--check")
	cmd.Env = d.env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()

	if code := exitCode(t, err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "scripts/render-diagrams.sh --check <base>") {
		t.Errorf("usage = %q, want the check mode", stderr.String())
	}
	if _, err := os.Stat(d.calls); !os.IsNotExist(err) {
		t.Errorf("docker was called: %s", mustRead(t, d.calls))
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
