package main

import (
	"context"
	"testing"

	"github.com/forward-mcp/internal/adapters/evalrerun"
	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/domain/services/eval"
)

// TestSeedCaseGradesAccurateAgainstFixture is the end-to-end check for the
// grader with the checked-in sample case, answer and recording.
func TestSeedCaseGradesAccurateAgainstFixture(t *testing.T) {
	cases, answers, err := load("testdata/case-bgp-idle-01.json", "testdata/answer-bgp-idle-01.json", "", "")
	if err != nil {
		t.Fatal(err)
	}
	rerun, err := evalrerun.LoadFixture("testdata/fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	r := eval.New(rerun).Grade(context.Background(), cases[0], answers[cases[0].ID])
	if !r.Accurate {
		t.Fatalf("seed case should grade accurate: %+v", r)
	}
	if len(r.UncitedFacts) != 0 {
		t.Errorf("seed answer cites every fact: %v", r.UncitedFacts)
	}
	if s := eval.Summarize([]entities.GradeReport{r}); s.Accuracy != 1 {
		t.Errorf("summary: %+v", s)
	}
}

func TestLoadDirectoryLayout(t *testing.T) {
	cases, answers, err := load("", "", "testdata", "testdata")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 || cases[0].ID != "bgp-idle-01" {
		t.Fatalf("cases: %+v", cases)
	}
	if _, ok := answers["bgp-idle-01"]; !ok {
		t.Fatal("answer not paired by case id")
	}
}
