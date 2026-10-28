package app

import ()

func (s *Service) SkillPackageFilesAtVersion(userID, skillID, versionID, contentHash string) ([]SkillPackageFileItem, error) {
	return s.skillDomain().SkillPackageFilesAtVersion(userID, skillID, versionID, contentHash)
}

func (s *Service) SkillPackageFileAtVersion(userID, skillID, versionID, contentHash, filePath string) (*SkillPackageFileContent, error) {
	return s.skillDomain().SkillPackageFileAtVersion(userID, skillID, versionID, contentHash, filePath)
}
