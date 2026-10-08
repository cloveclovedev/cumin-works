package workflow

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The list of actions holds no empty name and no name twice, and every
// name is written as issue-states.md writes it: lower case, single spaces.
func TestActionNamesAreSetAndUnique(t *testing.T) {
	seen := map[ActionName]bool{}
	for i, name := range ActionNames {
		text := string(name)
		if text == "" {
			t.Errorf("action %d of the list has an empty name", i)
			continue
		}
		if text != strings.ToLower(text) || text != strings.Join(strings.Fields(text), " ") {
			t.Errorf("action name %q is not lower case with single spaces", text)
		}
		if seen[name] {
			t.Errorf("action name %q is in the list twice", text)
		}
		seen[name] = true
	}
}

// The list of actions holds every constant of the type ActionName in
// action.go, and nothing else. A new constant that is missing from the list
// fails here, because the tests that range over the list skip it.
func TestActionNamesHoldEveryConstant(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "action.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The name of the constant of each action name.
	constants := map[ActionName]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value := spec.(*ast.ValueSpec)
			if kind, ok := value.Type.(*ast.Ident); !ok || kind.Name != "ActionName" {
				continue
			}
			for i, name := range value.Names {
				if i >= len(value.Values) {
					t.Errorf("constant %s has no value of its own", name.Name)
					continue
				}
				lit, ok := value.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Errorf("constant %s is not a string literal", name.Name)
					continue
				}
				text, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				constants[ActionName(text)] = name.Name
			}
		}
	}
	if len(constants) == 0 {
		t.Fatal("action.go holds no constant of the type ActionName")
	}

	for _, name := range ActionNames {
		if _, ok := constants[name]; !ok {
			t.Errorf("the entry %q of ActionNames is no constant of action.go", string(name))
		}
	}
	var missing []string
	for name, constant := range constants {
		if !slices.Contains(ActionNames, name) {
			missing = append(missing, constant)
		}
	}
	slices.Sort(missing)
	for _, constant := range missing {
		t.Errorf("ActionNames does not hold the constant %s", constant)
	}
}
