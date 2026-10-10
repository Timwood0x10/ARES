package evolution_test

// The UngatedPatcher patch path
// (GenomePopulationAdapter.submitToCoordinator → UngatedPatcher.Evaluate →
// PatchExecutor.Apply) is explicitly NOT gated by StrategyLifecycle's gate
// chain — the type is named "Ungated" for exactly that reason (A1-c). This
// test pins the declaration in doc.go so the trust-root boundary stays
// honest: if a future change routes the patch path through lifecycle gates,
// this test must be updated alongside the doc.go declaration (and the type
// renamed back).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCoordinatorPatchPathNotGatedByLifecycle verifies the doc.go trust-root
// declaration explicitly acknowledges the UngatedPatcher patch path as
// ungated. It scans the doc.go file for the UNGATED PATCH PATH marker — if
// the marker is absent, the declaration has drifted from the documented
// boundary.
func TestCoordinatorPatchPathNotGatedByLifecycle(t *testing.T) {
	repo := findModuleRoot(t)
	docPath := filepath.Join(repo, "internal", "runtime", "ares_evolution", "doc.go")
	raw, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read doc.go: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "UNGATED PATCH PATH") {
		t.Fatal("doc.go trust-root declaration is missing the UNGATED PATCH PATH marker — " +
			"the Coordinator patch path bypasses StrategyLifecycle gates; this must be " +
			"explicitly documented (A1-c). Either gate the patch path through " +
			"StrategyLifecycle or add the UNGATED PATCH PATH declaration to doc.go.")
	}
	// The declaration must name both submitToCoordinator and
	// UngatedPatcher.Evaluate so the boundary is unambiguous.
	if !strings.Contains(body, "submitToCoordinator") {
		t.Fatal("doc.go UNGATED PATCH PATH declaration must name submitToCoordinator")
	}
	if !strings.Contains(body, "UngatedPatcher.Evaluate") {
		t.Fatal("doc.go UNGATED PATCH PATH declaration must name UngatedPatcher.Evaluate")
	}
}
