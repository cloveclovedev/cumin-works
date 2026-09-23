package roles

// The risk criteria is the text that the Planner and the Reviewer
// receive word for word when they give a change a risk/* label. cumin does
// not read it. It has the three levels of the other settings: the text that
// ships with cumin, risk-criteria.md next to the Host settings file, and
// .cumin/risk-criteria.md of the target repository, each stronger than the
// one before (docs/ja/requirements/cumin-core.md, the topic on settings).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloveclovedev/cumin-works/disciplines"
)

// RiskCriteriaFile is the name of the file that replaces the text that
// ships with cumin. It stands next to the Host settings file, and in the
// .cumin/ of a target repository.
const RiskCriteriaFile = "risk-criteria.md"

// repositoryRiskCriteria is the path of the file in a target repository,
// for the text of an error.
const repositoryRiskCriteria = ".cumin/" + RiskCriteriaFile

// RiskCriteriaSource says which of the three levels the text came from.
type RiskCriteriaSource string

const (
	RiskCriteriaFromDefault    RiskCriteriaSource = "default"
	RiskCriteriaFromHost       RiskCriteriaSource = "host"
	RiskCriteriaFromRepository RiskCriteriaSource = "repository"
)

// RiskCriteria returns the text of the strongest risk criteria that exists,
// and where it came from. repository is the text of .cumin/risk-criteria.md
// of the target repository, or nil when the repository does not have the
// file. hostDir is the directory of the Host settings file; an empty hostDir
// skips the Host level.
//
// The text is returned as it is. A file that exists and holds no character
// other than white space is an error that names the path: an empty
// instruction would reach an agent as an instruction.
func RiskCriteria(repository *string, hostDir string) (string, RiskCriteriaSource, error) {
	if repository != nil {
		if strings.TrimSpace(*repository) == "" {
			return "", "", fmt.Errorf("%s: the risk criteria is empty", repositoryRiskCriteria)
		}
		return *repository, RiskCriteriaFromRepository, nil
	}
	if hostDir != "" {
		path := filepath.Join(hostDir, RiskCriteriaFile)
		data, err := os.ReadFile(path)
		switch {
		case err == nil && strings.TrimSpace(string(data)) == "":
			return "", "", fmt.Errorf("%s: the risk criteria is empty", path)
		case err == nil:
			return string(data), RiskCriteriaFromHost, nil
		case !errors.Is(err, os.ErrNotExist):
			return "", "", fmt.Errorf("read the risk criteria of the Host: %w", err)
		}
	}
	text, err := DefaultRiskCriteria()
	if err != nil {
		return "", "", err
	}
	return text, RiskCriteriaFromDefault, nil
}

// DefaultRiskCriteria returns the text that ships with cumin, which the
// requirement of the Planner names as the built-in value. The text
// belongs to the discipline, because a risk criterion is the judgment of
// one field of work.
func DefaultRiskCriteria() (string, error) {
	return disciplines.RiskCriteria()
}
