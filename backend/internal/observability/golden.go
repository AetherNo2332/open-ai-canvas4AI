package observability

import (
	"errors"
	"strings"
)

type GoldenCase struct {
	ID                  string   `json:"id"`
	DatasetVersion      string   `json:"datasetVersion"`
	EvalVersion         string   `json:"evalVersion"`
	JudgeModel          string   `json:"judgeModel,omitempty"`
	InputSummary        string   `json:"inputSummary"`
	ExpectedResult      string   `json:"expectedResult"`
	AllowedTools        []string `json:"allowedTools,omitempty"`
	ForbiddenTools      []string `json:"forbiddenTools,omitempty"`
	DeterministicChecks []string `json:"deterministicChecks"`
	QualityDimensions   []string `json:"qualityDimensions,omitempty"`
}

func ValidateGoldenCase(item GoldenCase) error {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.DatasetVersion) == "" || strings.TrimSpace(item.EvalVersion) == "" {
		return errors.New("golden case requires id, datasetVersion and evalVersion")
	}
	if strings.TrimSpace(item.InputSummary) == "" || strings.TrimSpace(item.ExpectedResult) == "" {
		return errors.New("golden case requires summarized input and expected result")
	}
	if len(item.DeterministicChecks) == 0 {
		return errors.New("golden case requires deterministic checks")
	}
	if len(item.InputSummary) > 2000 || len(item.ExpectedResult) > 4000 {
		return errors.New("golden case content exceeds redacted limits")
	}
	return nil
}
