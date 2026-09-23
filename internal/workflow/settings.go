package workflow

// This file applies the settings that a target repository keeps in .cumin/
// on its default branch. The order of strength is the defaults, the Host
// settings file, then the repository file (cumin-core.md, the topic on
// settings). A repository whose file is wrong is skipped until the Owner
// merges a fix; the other repositories go on.

import (
	"fmt"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/roles"
)

// RepositorySettings is what the .cumin/ of one repository decided, with
// the Host settings under it.
type RepositorySettings struct {
	// Settings are the Host settings with the repository file applied.
	Settings *config.Settings
	// RiskCriteria is the text that the Planner and the Reviewer
	// receive word for word. cumin does not read it, and never logs it.
	RiskCriteria string
	// RiskCriteriaSource says which of the three levels the text came from.
	RiskCriteriaSource roles.RiskCriteriaSource
	// FromRepository is true when the repository has a .cumin/config.toml.
	FromRepository bool

	// The blobs that these settings were read from. The files are read
	// again only when a blob changed.
	configOID, criteriaOID string
}

// settingsFor returns the settings of one target repository, from the files
// that the poll read. The files are parsed again only when their blob
// changed, so an unchanged repository costs nothing after the first poll.
// The second return value is true when the files were parsed in this call.
//
// An error names the file and the key. Nothing is kept then, so the next
// poll reads the file again and a merged fix takes effect by itself.
func (s *Service) settingsFor(repository config.Repository, read github.RepositorySnapshot) (*RepositorySettings, bool, error) {
	if s.Settings == nil {
		return nil, false, fmt.Errorf("no Host settings are configured")
	}
	configOID, criteriaOID := blobOID(read.CuminConfig), blobOID(read.CuminRiskCriteria)

	key := repositoryKey(repository)
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	if kept, ok := s.repositorySettings[key]; ok && kept.configOID == configOID && kept.criteriaOID == criteriaOID {
		return kept, false, nil
	}

	settings := s.Settings
	if read.CuminConfig != nil {
		applied, err := s.Settings.WithRepository([]byte(read.CuminConfig.Text))
		if err != nil {
			return nil, false, fmt.Errorf("%s:\n%w", read.CuminConfig.Path, err)
		}
		settings = applied
	}

	var criteriaText *string
	if read.CuminRiskCriteria != nil {
		criteriaText = &read.CuminRiskCriteria.Text
	}
	criteria, source, err := roles.RiskCriteria(criteriaText, s.SettingsDir)
	if err != nil {
		return nil, false, err
	}

	applied := &RepositorySettings{
		Settings:           settings,
		RiskCriteria:       criteria,
		RiskCriteriaSource: source,
		FromRepository:     read.CuminConfig != nil,
		configOID:          configOID,
		criteriaOID:        criteriaOID,
	}
	if s.repositorySettings == nil {
		s.repositorySettings = map[string]*RepositorySettings{}
	}
	s.repositorySettings[key] = applied
	return applied, true, nil
}

// blobOID is the blob of a file that the snapshot read, or an empty string
// when the repository does not have the file.
func blobOID(file *github.RepositoryFile) string {
	if file == nil {
		return ""
	}
	return file.OID
}
