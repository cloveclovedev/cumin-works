// Package plantuml holds the renderer image of the diagrams (Dockerfile) and
// a test that the committed SVGs are rendered from their current sources.
package plantuml

import (
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// PlantUML writes the source of a diagram into the SVG as a comment
// <!--SRC=[...]-->: UTF-8, then raw Deflate, then its own base64 alphabet
// (https://plantuml.com/text-encoding). The lines @startuml and @enduml are
// not part of it.
var sourceComment = regexp.MustCompile(`<!--SRC=\[([0-9A-Za-z_-]*)\]-->`)

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-_"

// decodeSource returns the diagram source that PlantUML encoded.
func decodeSource(encoded string) (string, error) {
	var packed bytes.Buffer
	for i := 0; i < len(encoded); i += 4 {
		var word uint32
		for j := range 4 {
			var v int
			if i+j < len(encoded) {
				v = strings.IndexByte(alphabet, encoded[i+j])
				if v < 0 {
					return "", fmt.Errorf("character %q is not in the alphabet", encoded[i+j])
				}
			}
			word = word<<6 | uint32(v)
		}
		packed.Write([]byte{byte(word >> 16), byte(word >> 8), byte(word)})
	}
	// A complete stream ends by itself; the padding of the last group after
	// it is never read. So every error, a truncated stream too, is an error.
	text, err := io.ReadAll(flate.NewReader(&packed))
	if err != nil {
		return "", err
	}
	return string(text), nil
}

// normalize returns the source with Unix line ends and without the blank
// lines around it.
func normalize(source string) string {
	return strings.TrimSpace(strings.ReplaceAll(source, "\r\n", "\n"))
}

// inner returns the lines between the first and the last line of a source,
// the lines that PlantUML writes into the SVG. wrapped checks the two outer
// lines first.
func inner(source string) string {
	lines := strings.Split(normalize(source), "\n")
	if len(lines) < 2 {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}

// wrapped reports whether the first line of the source is "@startuml <name>"
// and the last line is "@enduml", ignoring blank lines around them.
func wrapped(source, name string) bool {
	lines := strings.Split(normalize(source), "\n")
	return len(lines) >= 2 && strings.TrimSpace(lines[0]) == "@startuml "+name && strings.TrimSpace(lines[len(lines)-1]) == "@enduml"
}

// checkDiagrams returns one problem for each .puml under root/docs that does
// not start with "@startuml <file name>" and end with "@enduml", or whose SVG
// next to it is missing, has no source comment, or was rendered from another
// text. It does not render, so it needs no Docker, and it does not see an SVG
// rendered with other fonts: docs/ja/development/diagrams.md forbids that.
func checkDiagrams(root string) ([]string, error) {
	const fix = "run scripts/render-diagrams.sh and commit the SVG"
	var problems []string
	var count int
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".puml" {
			return err
		}
		count++
		rel, _ := filepath.Rel(root, path)
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// The comparison drops @startuml and @enduml, so check them here. The
		// name after @startuml names the SVG that the render writes.
		name := strings.TrimSuffix(filepath.Base(path), ".puml")
		if !wrapped(string(source), name) {
			problems = append(problems, fmt.Sprintf("%s: the first line must be \"@startuml %s\" and the last line \"@enduml\"", rel, name))
			return nil
		}
		svg, err := os.ReadFile(strings.TrimSuffix(path, ".puml") + ".svg")
		if errors.Is(err, fs.ErrNotExist) {
			problems = append(problems, fmt.Sprintf("%s has no SVG next to it: %s", rel, fix))
			return nil
		}
		if err != nil {
			return err
		}
		match := sourceComment.FindSubmatch(svg)
		if match == nil {
			problems = append(problems, fmt.Sprintf("%s: the SVG has no source comment <!--SRC=[...]-->: %s", rel, fix))
			return nil
		}
		embedded, err := decodeSource(string(match[1]))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: the source comment of the SVG does not decode (%v): %s", rel, err, fix))
			return nil
		}
		if normalize(embedded) != inner(string(source)) {
			problems = append(problems, fmt.Sprintf("%s: the SVG was rendered from another text of the .puml: %s", rel, fix))
		}
		return nil
	})
	if err == nil && count == 0 {
		err = errors.New("found no .puml under docs/")
	}
	return problems, err
}

// TestDiagrams_SVGMatchesSource checks that every committed SVG under docs/
// was rendered from the current text of its .puml.
func TestDiagrams_SVGMatchesSource(t *testing.T) {
	problems, err := checkDiagrams(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

func TestCheckDiagrams_FindsEachProblem(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	comment := "<svg><!--SRC=[" + encodeForTest(t, "A -> B") + "]--></svg>"
	write("same.puml", "@startuml same\nA -> B\n@enduml\n")
	write("renamed.puml", "@startuml other\nA -> B\n@enduml\n")
	write("renamed.svg", comment)
	write("same.svg", comment)
	write("changed.puml", "@startuml changed\nA -> C\n@enduml\n")
	write("changed.svg", comment)
	write("missing.puml", "@startuml missing\nA -> B\n@enduml\n")
	write("nocomment.puml", "@startuml nocomment\nA -> B\n@enduml\n")
	write("nocomment.svg", "<svg></svg>")

	problems, err := checkDiagrams(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"changed.puml: the SVG was rendered from another text", "missing.puml has no SVG", "nocomment.puml: the SVG has no source comment", `renamed.puml: the first line must be "@startuml renamed"`}
	if len(problems) != len(want) {
		t.Fatalf("problems = %q, want one for each of %q", problems, want)
	}
	for i, w := range want {
		if !strings.Contains(problems[i], w) {
			t.Errorf("problem %d = %q, want %q", i, problems[i], w)
		}
	}
}

func TestDecodeSource_ReadsTheEncodingOfPlantUML(t *testing.T) {
	// The SVGs under docs/ test the decoding of real PlantUML output; this
	// round trip tests the alphabet and the padding of the last group.
	got, err := decodeSource(encodeForTest(t, "Bob -> Alice : hello"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bob -> Alice : hello" {
		t.Errorf("decodeSource = %q", got)
	}
	if _, err := decodeSource("a!b"); err == nil {
		t.Error("a character outside the alphabet decodes without an error")
	}
	if _, err := decodeSource(""); err == nil {
		t.Error("an empty source comment decodes without an error")
	}
	full := encodeForTest(t, "Bob -> Alice : hello")
	if _, err := decodeSource(full[:len(full)/2]); err == nil {
		t.Error("a truncated source comment decodes without an error")
	}
}

func TestInner_KeepsInteriorDirectives(t *testing.T) {
	source := "@startuml name\r\nA -> B\r\n@enduml\r\nC -> D\r\n@enduml\r\n"
	if got, want := inner(source), "A -> B\n@enduml\nC -> D"; got != want {
		t.Errorf("inner = %q, want %q", got, want)
	}
}

// encodeForTest is the inverse of decodeSource, for the round trip test.
func encodeForTest(t *testing.T, text string) string {
	t.Helper()
	var compressed bytes.Buffer
	w, err := flate.NewWriter(&compressed, flate.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data := compressed.Bytes()
	var out strings.Builder
	for i := 0; i < len(data); i += 3 {
		var group [3]byte
		copy(group[:], data[i:])
		word := uint32(group[0])<<16 | uint32(group[1])<<8 | uint32(group[2])
		for shift := 18; shift >= 0; shift -= 6 {
			out.WriteByte(alphabet[word>>uint(shift)&63])
		}
	}
	return out.String()
}
