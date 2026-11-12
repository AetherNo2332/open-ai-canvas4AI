package architecture

import "testing"

func TestUnknownInternalPackageRequiresClassification(t *testing.T) {
	root := testBackend(t, map[string]string{
		"internal/newdomain/rules.go": "package newdomain\n",
	})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Rule != "package_classification" {
		t.Fatalf("unknown package must be reported before assigning a role: %+v", findings)
	}
	findings, err = Check(root, Baseline{Version: 1, Allowances: []Allowance{{
		ID: findings[0].ID, Max: 1, Reason: "attempted classification bypass",
	}}})
	if err != nil || !hasRule(findings, "package_classification") {
		t.Fatalf("classification cannot be baselined: %+v, %v", findings, err)
	}
}

func TestClassifiedDomainsCanStillImportEachOther(t *testing.T) {
	root := testBackend(t, map[string]string{
		"internal/canvas/rules.go": "package canvas\nimport _ \"yingce/backend/internal/assets\"\n",
	})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil || len(findings) != 0 {
		t.Fatalf("classification must not forbid every cross-domain import: %+v, %v", findings, err)
	}
}
