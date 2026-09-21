package cihook55

import "testing"

func TestCaseByCaseNonEmpty(t *testing.T) {
	if len(CaseByCase()) == 0 {
		t.Fatal("plan cas par cas vide")
	}
	if PlanPauseBeforeCache == "" || PlanStamp == "" {
		t.Fatal("plans inproc")
	}
}
